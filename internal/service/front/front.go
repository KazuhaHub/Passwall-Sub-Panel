// Package front runs the panel's side of SNI-shared ports (transit.sni.v1): it
// stores and validates the fronts an administrator writes, compiles them into
// the Transit.Fronts a server is sent, and answers which ports a server uses.
//
// The rules the package holds:
//
//   - Settings apply literally. Every port, loopback port and server name in a
//     front is what the administrator saved; the panel pre-fills (a free
//     loopback port, a camouflage site, the names a listener serves) only in
//     the editor's suggestions and never at send time.
//   - A front goes only to a server that declared transit.sni.v1, whose desired
//     engine is Xray and whose agent reports Xray running. Anything else gets
//     no fronts, so its config is what it always was, and the save is refused
//     with the reason.
//   - Only a listener Node can move (TCP, TLS or REALITY, no PROXY header of its
//     own) can sit behind a front. Shadowsocks and the UDP protocols keep their
//     own ports.
//   - A route's names are what its target's clients send: a listener's own
//     server names, a relay line's landing's.
//   - Conflicts block saving and name the other object.
//   - A change that moves listeners, or stops serving lines, is a confirmed
//     action that names what it affects; it is never implied.
//   - The default for an unknown or absent SNI is an external site, and never
//     this server itself.
package front

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	nodeprotocol "github.com/KazuhaHub/passwall-protocol/protocol"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/pkg/keyedmutex"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
	"github.com/KazuhaHub/passwall-sub-panel/internal/service/transit"
)

// MaxFronts is the most fronts one server may hold.
const MaxFronts = nodeprotocol.MaxFronts

// dnsBudget is the one overall time a save may spend resolving names. It is a
// variable only so a test can shorten it.
var dnsBudget = 3 * time.Second

// Resolver turns a name into the addresses it resolves to.
type Resolver interface {
	LookupHost(ctx context.Context, host string) ([]string, error)
}

// NodeLister reads the fleet's nodes.
type NodeLister interface {
	List(ctx context.Context) ([]*domain.Node, error)
}

// PanelGetter reads one server.
type PanelGetter interface {
	GetByID(ctx context.Context, id int64) (*domain.XUIPanel, error)
}

// AgentGetter reads the Node agent of a server.
type AgentGetter interface {
	GetByPanelID(ctx context.Context, panelID int64) (*domain.NodeAgent, error)
}

// ForwardKeys mints the row a managed line's forward key comes from.
type ForwardKeys interface {
	Ensure(ctx context.Context, nodeID int64, relayID string) (domain.TransitForward, error)
}

// LinkLister reads the tunnels, for the port-usage view.
type LinkLister interface {
	ListLinks(ctx context.Context) ([]domain.TransitLink, error)
}

// Service is the front service.
type Service struct {
	fronts   ports.TransitFrontRepo
	nodes    NodeLister
	panels   PanelGetter
	agents   AgentGetter
	forwards ForwardKeys
	links    LinkLister
	resolver Resolver
	locks    keyedmutex.Map[int64]
	// reserved lists the link ports promised on a server to tunnels being set up
	// (transit.Service.ReservedPorts); nil knows none.
	reserved   func(panelID int64) []int
	probePorts func(context.Context, int64, time.Time) ([]domain.ProbeState, error)
}

// WithReservations lets the port views and pickers see the link ports promised
// to tunnels about to be made, which no listener or forward may be given.
func (s *Service) WithReservations(reserved func(panelID int64) []int) *Service {
	s.reserved = reserved
	return s
}

// WithProbePorts supplies the most recent internal listeners reported by Node.
func (s *Service) WithProbePorts(list func(context.Context, int64, time.Time) ([]domain.ProbeState, error)) *Service {
	s.probePorts = list
	return s
}

// New returns the service. links may be nil (the usage view then lists no
// link ports); resolver defaults to the system resolver.
func New(fronts ports.TransitFrontRepo, nodes NodeLister, panels PanelGetter, agents AgentGetter,
	forwards ForwardKeys, links LinkLister, resolver Resolver) *Service {
	if resolver == nil {
		resolver = net.DefaultResolver
	}
	return &Service{fronts: fronts, nodes: nodes, panels: panels, agents: agents, forwards: forwards, links: links, resolver: resolver}
}

// Lock serializes everything that reads and then writes the fronts and ports
// of one server: front saves, and the node saves that touch them. A loopback
// port picked while another save is in flight could otherwise be given twice.
// It returns the unlock. Callers that already hold it call the *Locked methods.
func (s *Service) Lock(panelID int64) func() { return s.locks.Lock(panelID) }

// Refusal is a save the service will not make. Code is stable (the editor
// writes its own wording from it and Params); Message is the English fallback.
type Refusal struct {
	Code    string
	Message string
	Params  map[string]any
}

func (r *Refusal) Error() string { return r.Message }

// Is lets a refusal be matched as a validation error, and the one that asks
// for a confirmation as a conflict.
func (r *Refusal) Is(target error) bool {
	if r.Code == CodeConfirmRequired {
		return target == domain.ErrConflict
	}
	return target == domain.ErrValidation
}

// Refusal codes.
const (
	CodeNotNode              = "not_node"
	CodeNotXray              = "engine_not_xray"
	CodeNotRunningXray       = "not_running_xray"
	CodeNoCapability         = "no_capability"
	CodeNoAgent              = "no_agent"
	CodeBadPort              = "bad_port"
	CodeTooManyFronts        = "too_many_fronts"
	CodePortHasFront         = "port_has_front"
	CodeNotFrontable         = "listener_not_frontable"
	CodeListenerPortMismatch = "listener_port_mismatch"
	CodeUnknownNode          = "unknown_node"
	CodeLoopbackBad          = "loopback_port_bad"
	CodePortConflict         = "port_conflict"
	CodeListenerOnPort       = "listener_on_port"
	CodeConfirmRequired      = "confirm_required"
	CodeNoRoutes             = "no_routes"
	CodeBadName              = "bad_server_name"
	CodeDuplicateName        = "duplicate_server_name"
	CodeNameNotServed        = "name_not_served"
	CodeBadTarget            = "bad_target"
	CodeLandingUnfit         = "landing_not_tls"
	CodeListenerUnrouted     = "listener_unrouted"
	CodeLinkUnsupported      = "link_unsupported"
	CodeDefaultMissing       = "default_missing"
	CodeDefaultNotExternal   = "default_not_external"
	CodeDefaultBad           = "default_bad"
	CodeDefaultIsSelf        = "default_is_self"
	CodeJoinNoName           = "join_needs_name"
	CodeRenderedNameMissing  = "rendered_name_missing"
	CodeListenerStillOnPort  = "listener_still_on_port"
	CodeListenersShare       = "listeners_share_port"
	CodeLeaveEmptiesFront    = "leave_empties_front"
	CodeRejectedByContract   = "contract_refused"
)

func refuse(code, format string, params map[string]any, args ...any) *Refusal {
	return &Refusal{Code: code, Message: fmt.Sprintf(format, args...), Params: params}
}

// Support is whether a server can run fronts, and when it cannot, why.
type Support struct {
	Supported bool
	// Reason is a Code* value when not supported.
	Reason string
}

// SupportOf decides from what the panel knows of the server. An agent that has
// not synced yet has declared nothing, so it cannot run fronts yet.
func (s *Service) SupportOf(ctx context.Context, panelID int64) (Support, error) {
	panel, err := s.panels.GetByID(ctx, panelID)
	if err != nil {
		return Support{}, err
	}
	if panel == nil || domain.NormalizePanelKind(panel.Kind) != domain.PanelKindPSP {
		return Support{Reason: CodeNotNode}, nil
	}
	agent, err := s.agents.GetByPanelID(ctx, panelID)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			return Support{Reason: CodeNoAgent}, nil
		}
		return Support{}, err
	}
	if agent == nil {
		return Support{Reason: CodeNoAgent}, nil
	}
	return supportOfAgent(agent), nil
}

func supportOfAgent(agent *domain.NodeAgent) Support {
	if domain.NormalizeNodeCoreEngine(agent.DesiredCoreEngine) != domain.NodeCoreXray {
		return Support{Reason: CodeNotXray}
	}
	if !slices.Contains(agent.ObservedCapabilities, nodeprotocol.CapabilityTransitSNI) {
		return Support{Reason: CodeNoCapability}
	}
	// The desired engine is what the panel asked for; what the agent reports
	// running is what a front would run on.
	if agent.ObservedCoreEngine != domain.NodeCoreXray {
		return Support{Reason: CodeNotRunningXray}
	}
	return Support{Supported: true}
}

func supportRefusal(reason string) *Refusal {
	switch reason {
	case CodeNotNode:
		return refuse(reason, "SNI sharing needs a Passwall Node server", nil)
	case CodeNotXray:
		return refuse(reason, "SNI sharing needs Xray: this server is set to another engine", nil)
	case CodeNotRunningXray:
		return refuse(reason, "SNI sharing needs Xray: this server's agent does not report Xray running", nil)
	case CodeNoCapability:
		return refuse(reason, "this server's agent has not declared transit.sni.v1; upgrade it and wait for it to sync", nil)
	default:
		return refuse(CodeNoAgent, "this server has no agent that has reported yet", nil)
	}
}

// Input is one front as the administrator wrote it.
type Input struct {
	Port      int
	Listeners []domain.FrontListener
	Routes    []domain.FrontRoute
	Default   domain.FrontTarget
	// ConfirmMoveListeners is the explicit go-ahead for a change that moves a
	// listener behind the front or back to its own port, or stops a relay line
	// being served. It is never implied by anything else in the request.
	ConfirmMoveListeners bool
}

// Saved is a stored front with notes the editor should show.
type Saved struct {
	Front    domain.TransitFront
	Warnings []string
}

// Warning codes.
const WarnDefaultUnresolved = "default_unresolved"

// Create stores a new front on a server.
func (s *Service) Create(ctx context.Context, panelID int64, in Input) (Saved, error) {
	defer s.Lock(panelID)()
	return s.save(ctx, panelID, nil, in, saveOpts{})
}

// Update replaces a front. The port is editable like everything else.
func (s *Service) Update(ctx context.Context, panelID, frontID int64, in Input) (Saved, error) {
	defer s.Lock(panelID)()
	existing, err := s.fronts.Get(ctx, frontID)
	if err != nil {
		return Saved{}, err
	}
	if existing.PanelID != panelID {
		return Saved{}, domain.ErrNotFound
	}
	return s.save(ctx, panelID, &existing, in, saveOpts{})
}

// Delete removes a front. Its listeners return to their public ports and the
// relay lines that enter on it stop being sent, so a delete that affects any is
// a confirmed action that names them.
func (s *Service) Delete(ctx context.Context, panelID, frontID int64, confirmed bool) (domain.TransitFront, error) {
	defer s.Lock(panelID)()
	existing, err := s.fronts.Get(ctx, frontID)
	if err != nil {
		return domain.TransitFront{}, err
	}
	if existing.PanelID != panelID {
		return domain.TransitFront{}, domain.ErrNotFound
	}
	nodes, err := s.nodes.List(ctx)
	if err != nil {
		return domain.TransitFront{}, err
	}
	// Two or more listeners on one port cannot all go back to it: Node refuses
	// a config in which they bind the same port. Each must move first.
	if sharing := sharingListeners(existing, nodes); len(sharing) >= 2 {
		return domain.TransitFront{}, shareRefusal(existing.Port, sharing, "delete")
	}
	if !confirmed {
		if impact := impactOf(existing, nodes); !impact.Empty() {
			return domain.TransitFront{}, confirmRefusal("deleting the shared port %d", impact, "delete")
		}
	}
	if _, err := s.fronts.Delete(ctx, panelID, frontID); err != nil {
		return domain.TransitFront{}, err
	}
	return existing, nil
}

// List returns a server's fronts.
func (s *Service) List(ctx context.Context, panelID int64) ([]domain.TransitFront, error) {
	return s.fronts.ListByPanel(ctx, panelID)
}

// saveOpts changes how save behaves for the callers that are not the editor.
type saveOpts struct {
	// dry validates and checks the contract but writes nothing, not even the
	// forward rows a compile would mint.
	dry bool
	// candidate is a node as it is about to be saved, standing in for (or added
	// to) what the repository holds: a listener that joins a front is checked
	// before it exists on its new port.
	candidate *domain.Node
	// shrink marks a change that only takes things out of a front (a listener
	// leaving, a node deleted, routes removed): it must stay possible when the
	// server can no longer run fronts, or a front with several listeners could
	// never be emptied.
	shrink bool
	// omit drops a node (one being deleted) from the fleet the save sees.
	omit int64
	// keepDefault keeps the front's stored default without checking it again: a
	// listener joining or leaving changes nothing about it, and checking it
	// would resolve its name a second time.
	keepDefault bool
}

// pendingNodeID stands for a node that is not stored yet.
const pendingNodeID int64 = 1<<40 - 2

func (s *Service) save(ctx context.Context, panelID int64, existing *domain.TransitFront, in Input, opts saveOpts) (Saved, error) {
	if !opts.shrink && !(existing != nil && onlyShrinks(*existing, in)) {
		support, err := s.SupportOf(ctx, panelID)
		if err != nil {
			return Saved{}, err
		}
		if !support.Supported {
			return Saved{}, supportRefusal(support.Reason)
		}
	}
	nodes, err := s.nodes.List(ctx)
	if err != nil {
		return Saved{}, fmt.Errorf("front: list nodes: %w", err)
	}
	if opts.omit != 0 {
		kept := nodes[:0:0]
		for _, n := range nodes {
			if n != nil && n.ID != opts.omit {
				kept = append(kept, n)
			}
		}
		nodes = kept
	}
	if opts.candidate != nil {
		nodes = withCandidate(nodes, opts.candidate)
	}
	fronts, err := s.fronts.ListByPanel(ctx, panelID)
	if err != nil {
		return Saved{}, err
	}
	var others []domain.TransitFront
	for _, f := range fronts {
		if existing == nil || f.ID != existing.ID {
			others = append(others, f)
		}
	}
	candidate, warnings, err := s.validate(ctx, panelID, nodes, others, existing, in, opts.keepDefault)
	if err != nil {
		return Saved{}, err
	}
	if err := s.contractCheck(ctx, panelID, nodes, append(slices.Clone(others), withID(candidate, existing))); err != nil {
		return Saved{}, err
	}
	if opts.dry {
		return Saved{Front: candidate, Warnings: warnings}, nil
	}
	var stored domain.TransitFront
	if existing == nil {
		stored, err = s.fronts.Create(ctx, candidate)
	} else {
		candidate.ID = existing.ID
		stored, err = s.fronts.Update(ctx, candidate)
	}
	if errors.Is(err, domain.ErrAlreadyExists) {
		return Saved{}, refuse(CodePortHasFront, "this server already has a front on port %d", map[string]any{"port": candidate.Port}, candidate.Port)
	}
	if err != nil {
		return Saved{}, err
	}
	return Saved{Front: stored, Warnings: warnings}, nil
}

// onlyShrinks reports whether an edit takes things out of a front and adds
// nothing: same port and default, listeners and routes a subset of what is saved.
func onlyShrinks(existing domain.TransitFront, in Input) bool {
	if in.Port != existing.Port || normalizeAddress(in.Default.Host) != normalizeAddress(existing.Default.Host) || in.Default.Port != existing.Default.Port {
		return false
	}
	have := map[domain.FrontListener]bool{}
	for _, item := range existing.Listeners {
		have[item] = true
	}
	for _, item := range in.Listeners {
		if !have[item] {
			return false
		}
	}
	routes := map[string]map[string]bool{}
	for _, route := range existing.Routes {
		names := map[string]bool{}
		for _, name := range route.ServerNames {
			names[name] = true
		}
		routes[targetKeyOf(route.Target)] = names
	}
	for _, route := range in.Routes {
		have := routes[targetKeyOf(route.Target)]
		if have == nil {
			return false
		}
		for _, name := range route.ServerNames {
			if !have[domain.NormalizeFrontName(name)] {
				return false
			}
		}
	}
	return true
}

// withCandidate substitutes the node about to be saved into the fleet.
func withCandidate(nodes []*domain.Node, candidate *domain.Node) []*domain.Node {
	out := make([]*domain.Node, 0, len(nodes)+1)
	replaced := false
	for _, n := range nodes {
		if n != nil && candidate.ID != 0 && n.ID == candidate.ID {
			out = append(out, candidate)
			replaced = true
			continue
		}
		out = append(out, n)
	}
	if !replaced {
		c := *candidate
		if c.ID == 0 {
			c.ID = pendingNodeID
		}
		out = append(out, &c)
	}
	return out
}

// withID gives the candidate the id it will have, so the contract check sees a
// stable key. A new front is checked under a key no stored one can have.
func withID(candidate domain.TransitFront, existing *domain.TransitFront) domain.TransitFront {
	if existing != nil {
		candidate.ID = existing.ID
	} else {
		candidate.ID = 1<<40 - 1
	}
	return candidate
}

// nodeLabel is how a node is named in a message.
func nodeLabel(n *domain.Node) string {
	if n == nil {
		return "?"
	}
	if strings.TrimSpace(n.DisplayName) != "" {
		return fmt.Sprintf("%q", n.DisplayName)
	}
	return fmt.Sprintf("#%d", n.ID)
}

// TargetLabel names a target for a message and for the usage view.
func TargetLabel(t domain.FrontTarget, nodes map[int64]*domain.Node) string {
	switch t.Kind {
	case domain.FrontTargetListener:
		if n := nodes[t.NodeID]; n.IsTunnelChild() {
			return tunnelListenerLabel(n, nodes[n.ParentNodeID])
		}
		return "listener of node " + nodeLabel(nodes[t.NodeID])
	case domain.FrontTargetForward:
		name := t.LineID
		if n := nodes[t.NodeID]; n != nil {
			for _, line := range n.Relays {
				if line.ID == t.LineID {
					name = firstNonEmpty(line.Name, line.Address, line.ID)
				}
			}
			return fmt.Sprintf("relay line %q of node %s", name, nodeLabel(n))
		}
		return "relay line " + name
	case domain.FrontTargetLink:
		return "tunnel exit"
	case domain.FrontTargetExternal:
		return fmt.Sprintf("external site %s:%d", t.Host, t.Port)
	}
	return string(t.Kind)
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

func lineLabel(n *domain.Node, lineID string) string {
	name := lineID
	if n != nil {
		for _, line := range n.Relays {
			if line.ID == lineID {
				name = firstNonEmpty(line.Name, line.Address, line.ID)
			}
		}
	}
	return fmt.Sprintf("%q of node %s", name, nodeLabel(n))
}

func (s *Service) validate(ctx context.Context, panelID int64, nodes []*domain.Node, others []domain.TransitFront,
	existing *domain.TransitFront, in Input, keepDefault bool) (domain.TransitFront, []string, error) {
	var warnings []string
	byID := make(map[int64]*domain.Node, len(nodes))
	var onPanel []*domain.Node
	for _, n := range nodes {
		if n == nil {
			continue
		}
		byID[n.ID] = n
		if n.PanelID == panelID && !n.IsSeparator() {
			onPanel = append(onPanel, n)
		}
	}
	if in.Port < 1 || in.Port > 65535 {
		return domain.TransitFront{}, nil, refuse(CodeBadPort, "the shared port must be between 1 and 65535", map[string]any{"port": in.Port})
	}
	if domain.InNodeProbeRange(in.Port) {
		return domain.TransitFront{}, nil, refuse(CodeBadPort, "port %d is in 127.0.0.1:%d-%d, which Node uses internally",
			map[string]any{"port": in.Port, "low": domain.NodeProbeLoopbackLow, "high": domain.NodeProbeLoopbackHigh},
			in.Port, domain.NodeProbeLoopbackLow, domain.NodeProbeLoopbackHigh)
	}
	if what, reserved := domain.NodeReservedPort(in.Port); reserved {
		return domain.TransitFront{}, nil, refuse(CodeBadPort, "port %d is Node's %s", map[string]any{"port": in.Port, "what": what}, in.Port, what)
	}
	if len(others)+1 > MaxFronts {
		return domain.TransitFront{}, nil, refuse(CodeTooManyFronts, "a server holds at most %d shared ports", map[string]any{"max": MaxFronts}, MaxFronts)
	}
	for _, f := range others {
		if f.Port == in.Port {
			return domain.TransitFront{}, nil, refuse(CodePortHasFront, "this server already shares port %d", map[string]any{"port": in.Port}, in.Port)
		}
	}

	// The ports in use on the server, other than this front's own.
	use, err := s.collectUse(ctx, panelID, onPanel, nodes, others)
	if err != nil {
		return domain.TransitFront{}, nil, err
	}

	front := domain.TransitFront{PanelID: panelID, Port: in.Port}

	// A listener cannot be taken out of a front that stays on its port: it would
	// bind the port the front binds. Its own port is changed first (that save
	// takes it out of the front); and the front's port cannot change while two or
	// more listeners share it, since they could not all go back.
	if existing != nil {
		inSet := map[int64]bool{}
		for _, item := range in.Listeners {
			inSet[item.NodeID] = true
		}
		portChanged := existing.Port != in.Port
		var still []string
		for _, item := range existing.Listeners {
			if node := byID[item.NodeID]; !inSet[item.NodeID] && !portChanged && node != nil && node.PanelID == panelID && node.DesiredPort == existing.Port {
				still = append(still, nodeLabel(node))
			}
		}
		if len(still) > 0 {
			return domain.TransitFront{}, nil, refuse(CodeListenerStillOnPort,
				"%s still listens on the shared port %d: change that listener's own port first (that save takes it out of the front), then remove it here",
				map[string]any{"nodes": still, "port": existing.Port}, strings.Join(still, ", "), existing.Port)
		}
		if portChanged {
			if sharing := sharingListeners(*existing, nodes); len(sharing) >= 2 {
				return domain.TransitFront{}, nil, shareRefusal(existing.Port, sharing, "port")
			}
		}
	}

	// Listeners moved behind the front.
	listenerSeen := map[int64]bool{}
	loopbackSeen := map[int]int64{}
	for _, item := range in.Listeners {
		node := byID[item.NodeID]
		if node == nil || node.PanelID != panelID || node.IsSeparator() {
			return domain.TransitFront{}, nil, refuse(CodeUnknownNode, "node %d is not a listener of this server", map[string]any{"node_id": item.NodeID}, item.NodeID)
		}
		if listenerSeen[item.NodeID] {
			return domain.TransitFront{}, nil, refuse(CodeBadTarget, "node %s is listed twice", map[string]any{"node": node.DisplayName}, nodeLabel(node))
		}
		listenerSeen[item.NodeID] = true
		if fr := domain.FrontableOf(node); !fr.OK {
			return domain.TransitFront{}, nil, refuse(CodeNotFrontable,
				"node %s cannot share a port: only a TCP TLS or REALITY listener without a PROXY header of its own can sit behind a front (reason: %s)",
				map[string]any{"node": node.DisplayName, "node_id": node.ID, "reason": fr.Reason, "protocol": node.DesiredProtocol}, nodeLabel(node), fr.Reason)
		}
		if node.DesiredPort != in.Port {
			return domain.TransitFront{}, nil, refuse(CodeListenerPortMismatch,
				"node %s listens on port %d, not on the shared port %d; a fronted listener keeps its public port",
				map[string]any{"node": node.DisplayName, "node_id": node.ID, "listen_port": node.DesiredPort, "port": in.Port}, nodeLabel(node), node.DesiredPort, in.Port)
		}
		if item.LoopbackPort < 1 || item.LoopbackPort > 65535 || item.LoopbackPort == in.Port || domain.InNodeProbeRange(item.LoopbackPort) {
			return domain.TransitFront{}, nil, refuse(CodeLoopbackBad,
				"loopback port %d of node %s must be between 1 and 65535, differ from the shared port and stay out of %d-%d",
				map[string]any{"node": node.DisplayName, "port": item.LoopbackPort, "low": domain.NodeProbeLoopbackLow, "high": domain.NodeProbeLoopbackHigh},
				item.LoopbackPort, nodeLabel(node), domain.NodeProbeLoopbackLow, domain.NodeProbeLoopbackHigh)
		}
		if what, reserved := domain.NodeReservedPort(item.LoopbackPort); reserved {
			return domain.TransitFront{}, nil, refuse(CodeLoopbackBad, "loopback port %d of node %s is Node's %s",
				map[string]any{"node": node.DisplayName, "port": item.LoopbackPort, "what": what, "low": domain.NodeProbeLoopbackLow, "high": domain.NodeProbeLoopbackHigh},
				item.LoopbackPort, nodeLabel(node), what)
		}
		if owners := use.owners(item.LoopbackPort); len(owners) > 0 {
			return domain.TransitFront{}, nil, refuse(CodePortConflict, "loopback port %d of node %s is already used by %s",
				map[string]any{"port": item.LoopbackPort, "node": node.DisplayName, "owner": owners[0].Label}, item.LoopbackPort, nodeLabel(node), owners[0].Label)
		}
		if other, taken := loopbackSeen[item.LoopbackPort]; taken {
			return domain.TransitFront{}, nil, refuse(CodePortConflict, "loopback port %d is given to node %s and to node %s",
				map[string]any{"port": item.LoopbackPort}, item.LoopbackPort, nodeLabel(byID[other]), nodeLabel(node))
		}
		loopbackSeen[item.LoopbackPort] = item.NodeID
		front.Listeners = append(front.Listeners, item)
	}
	sort.Slice(front.Listeners, func(i, j int) bool { return front.Listeners[i].NodeID < front.Listeners[j].NodeID })

	// The shared port itself: whatever else holds it must be a listener this
	// front moves, or a forward that is one of its targets.
	for _, owner := range use.owners(in.Port) {
		switch {
		case owner.Kind == KindListener && listenerSeen[owner.NodeID]:
			// Moved behind this front: that is the point.
		case owner.Kind == KindForward:
			// A managed line entering on this port is a target candidate.
		case owner.Kind == KindListener:
			return domain.TransitFront{}, nil, refuse(CodeListenerOnPort,
				"port %d is used by node %s, which is not behind this front: add it to the front or pick another port",
				map[string]any{"port": in.Port, "node": byID[owner.NodeID].DisplayName, "node_id": owner.NodeID}, in.Port, nodeLabel(byID[owner.NodeID]))
		default:
			return domain.TransitFront{}, nil, refuse(CodePortConflict, "port %d is already used by %s",
				map[string]any{"port": in.Port, "owner": owner.Label}, in.Port, owner.Label)
		}
	}

	// Routes.
	if len(in.Routes) == 0 {
		return domain.TransitFront{}, nil, refuse(CodeNoRoutes, "a shared port needs at least one route: a front that sends everything to its default shares nothing", nil)
	}
	if len(in.Routes) > nodeprotocol.MaxFrontRoutes {
		return domain.TransitFront{}, nil, refuse(CodeBadTarget, "a front holds at most %d routes", map[string]any{"max": nodeprotocol.MaxFrontRoutes}, nodeprotocol.MaxFrontRoutes)
	}
	nameOwner := map[string]domain.FrontTarget{}
	routedNames := map[string][]string{} // target key -> every name that reaches it
	referenced := map[int64]bool{}
	forwardTargets := map[string]bool{}
	for index, route := range in.Routes {
		names, err := normalizeNames(route.ServerNames)
		if err != nil {
			return domain.TransitFront{}, nil, err
		}
		target := route.Target
		if err := s.validateRouteTarget(panelID, in.Port, &target, names, byID, listenerSeen); err != nil {
			return domain.TransitFront{}, nil, err
		}
		for _, name := range names {
			if previous, dup := nameOwner[name]; dup {
				return domain.TransitFront{}, nil, refuse(CodeDuplicateName, "server name %q is already routed to %s",
					map[string]any{"name": name, "owner": TargetLabel(previous, byID), "route": index + 1}, name, TargetLabel(previous, byID))
			}
			nameOwner[name] = target
		}
		routedNames[targetKeyOf(target)] = append(routedNames[targetKeyOf(target)], names...)
		switch target.Kind {
		case domain.FrontTargetListener:
			referenced[target.NodeID] = true
		case domain.FrontTargetForward:
			forwardTargets[fmt.Sprintf("%d/%s", target.NodeID, target.LineID)] = true
		}
		front.Routes = append(front.Routes, domain.FrontRoute{ServerNames: names, Target: target})
	}
	for _, item := range front.Listeners {
		if !referenced[item.NodeID] {
			return domain.TransitFront{}, nil, refuse(CodeListenerUnrouted,
				"node %s is moved behind the front but no route sends it a server name",
				map[string]any{"node": byID[item.NodeID].DisplayName, "node_id": item.NodeID}, nodeLabel(byID[item.NodeID]))
		}
	}
	// Each target's clients send the SNI its subscription renders. The routes
	// that reach it must include that name, or those clients land on the default.
	for _, item := range front.Listeners {
		node := byID[item.NodeID]
		if rendered := domain.FrontableOf(node).RenderedName; domain.MissingRenderedName(rendered, routedNames[targetKeyOf(domain.FrontTarget{Kind: domain.FrontTargetListener, NodeID: item.NodeID})]) {
			return domain.TransitFront{}, nil, renderedRefusal(node, rendered)
		}
	}
	for key := range forwardTargets {
		var nodeID int64
		var lineID string
		fmt.Sscanf(key, "%d/%s", &nodeID, &lineID)
		landing := byID[nodeID]
		if rendered := domain.LandingOfRelay(landing).RenderedName; domain.MissingRenderedName(rendered, routedNames[targetKeyOf(domain.FrontTarget{Kind: domain.FrontTargetForward, NodeID: nodeID, LineID: lineID})]) {
			return domain.TransitFront{}, nil, renderedRefusal(landing, rendered)
		}
	}
	sort.SliceStable(front.Routes, func(i, j int) bool { return front.Routes[i].ServerNames[0] < front.Routes[j].ServerNames[0] })

	// A change that moves a listener behind the front or back to its own port,
	// or stops a relay line being served, is its own confirmed action.
	if !in.ConfirmMoveListeners {
		var moved, leavingListeners, leavingLines []string
		for _, item := range front.Listeners {
			if existing == nil || !existing.HasListener(item.NodeID) {
				moved = append(moved, nodeLabel(byID[item.NodeID]))
			}
		}
		if existing != nil {
			portChanged := existing.Port != in.Port
			for _, item := range existing.Listeners {
				if portChanged || !listenerSeen[item.NodeID] {
					leavingListeners = append(leavingListeners, nodeLabel(byID[item.NodeID]))
				}
			}
			for _, target := range existing.Targets() {
				if target.Kind == domain.FrontTargetForward && (portChanged || !forwardTargets[fmt.Sprintf("%d/%s", target.NodeID, target.LineID)]) {
					leavingLines = append(leavingLines, lineLabel(byID[target.NodeID], target.LineID))
				}
			}
		}
		if len(moved)+len(leavingListeners)+len(leavingLines) > 0 {
			return domain.TransitFront{}, nil, confirmRefusal("saving the shared port %d", Impact{
				Ports: []int{in.Port}, Moved: moved, Listeners: leavingListeners, Lines: leavingLines,
			}, "save")
		}
	}

	// Default.
	def, warn := in.Default, ""
	if !keepDefault {
		def, warn, err = s.validateDefault(ctx, panelID, in.Port, in.Default, onPanel)
		if err != nil {
			return domain.TransitFront{}, nil, err
		}
	}
	if warn != "" {
		warnings = append(warnings, warn)
	}
	front.Default = def
	return front, warnings, nil
}

func normalizeNames(raw []string) ([]string, error) {
	if len(raw) == 0 || len(raw) > nodeprotocol.MaxFrontRouteNames {
		return nil, refuse(CodeBadName, "a route needs between 1 and %d server names", map[string]any{"max": nodeprotocol.MaxFrontRouteNames}, nodeprotocol.MaxFrontRouteNames)
	}
	seen := map[string]bool{}
	out := make([]string, 0, len(raw))
	for _, name := range raw {
		name = domain.NormalizeFrontName(name)
		if err := validateServerName(name); err != nil {
			return nil, refuse(CodeBadName, "server name %q is not usable: %s", map[string]any{"name": name, "why": err.Error()}, name, err.Error())
		}
		if seen[name] {
			return nil, refuse(CodeDuplicateName, "server name %q is listed twice in one route", map[string]any{"name": name}, name)
		}
		seen[name] = true
		out = append(out, name)
	}
	sort.Strings(out)
	return out, nil
}

// validateServerName holds a name to what a ClientHello can carry and the
// router matches exactly: a lowercase DNS name with a dot, never an address and
// never a pattern.
func validateServerName(name string) error {
	if name == "" {
		return errors.New("it is empty")
	}
	if _, err := netip.ParseAddr(name); err == nil {
		return errors.New("it is an IP address, and a ClientHello carries a name")
	}
	if strings.Contains(name, "*") {
		return errors.New("patterns are not matched; write each name")
	}
	if !strings.Contains(name, ".") {
		return errors.New("it is not a fully qualified name")
	}
	return validateHostName(name)
}

// validateHostName accepts the lowercase DNS names the wire contract accepts.
func validateHostName(name string) error {
	if len(name) > 253 {
		return errors.New("it is longer than 253 bytes")
	}
	labels := strings.Split(name, ".")
	for _, label := range labels {
		if label == "" || len(label) > 63 {
			return errors.New("it has an empty or oversized label")
		}
		if label[0] == '-' || label[len(label)-1] == '-' {
			return errors.New("a label starts or ends with a hyphen")
		}
		for i := 0; i < len(label); i++ {
			c := label[i]
			if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-') {
				return errors.New("only lowercase letters, digits and hyphens are allowed (write an internationalized name in punycode)")
			}
		}
	}
	if last := labels[len(labels)-1]; strings.Trim(last, "0123456789") == "" {
		return errors.New("its last label is numeric")
	}
	return nil
}

// validateRouteTarget holds a route to what its target's clients send: a
// listener's route names are among the server names the listener serves, and a
// relay line's are the landing's (a managed line forwards unchanged and cannot
// override the SNI, so the subscription renders the landing's own name).
func (s *Service) validateRouteTarget(panelID int64, port int, target *domain.FrontTarget, names []string,
	byID map[int64]*domain.Node, fronted map[int64]bool) error {
	switch target.Kind {
	case domain.FrontTargetListener:
		node := byID[target.NodeID]
		if node == nil || !fronted[target.NodeID] {
			return refuse(CodeBadTarget, "a route to a listener names a node that is moved behind this front",
				map[string]any{"node_id": target.NodeID})
		}
		if bad := domain.UnacceptedFrontName(domain.FrontableOf(node).ServerNames, names); bad != "" {
			return refuse(CodeNameNotServed, "node %s does not serve %q, so its clients never send it: use one of its own server names",
				map[string]any{"node": node.DisplayName, "node_id": node.ID, "name": bad, "served": domain.FrontableOf(node).ServerNames}, nodeLabel(node), bad)
		}
		*target = domain.FrontTarget{Kind: domain.FrontTargetListener, NodeID: target.NodeID}
	case domain.FrontTargetForward:
		landing := byID[target.NodeID]
		if landing == nil {
			return refuse(CodeBadTarget, "a route to a relay line names a node that does not exist", map[string]any{"node_id": target.NodeID})
		}
		var line *domain.RelayLine
		for i := range landing.Relays {
			if landing.Relays[i].ID == target.LineID {
				line = &landing.Relays[i]
			}
		}
		if line == nil {
			return refuse(CodeBadTarget, "node %s has no relay line %q", map[string]any{"node": landing.DisplayName, "line_id": target.LineID}, nodeLabel(landing), target.LineID)
		}
		if line.Tunnel() {
			return refuse(CodeBadTarget, "relay line %q of node %s is a tunnel line, served by a listener of its own: route to that listener instead",
				map[string]any{"node": landing.DisplayName, "line_id": target.LineID}, firstNonEmpty(line.Name, line.ID), nodeLabel(landing))
		}
		if line.EntryPanelID != panelID {
			return refuse(CodeBadTarget, "relay line %q of node %s does not enter through this server",
				map[string]any{"node": landing.DisplayName, "line_id": target.LineID}, firstNonEmpty(line.Name, line.ID), nodeLabel(landing))
		}
		if line.Port != port {
			return refuse(CodeBadTarget, "relay line %q of node %s enters on port %d, not on the shared port %d: set its entry port to %d first",
				map[string]any{"node": landing.DisplayName, "line_id": target.LineID, "line_port": line.Port, "port": port},
				firstNonEmpty(line.Name, line.ID), nodeLabel(landing), line.Port, port, port)
		}
		// A front hands over a TCP connection by its SNI. A landing that is UDP,
		// or carries no TLS, has none: the line cannot be shared, and is never
		// quietly narrowed to TCP.
		landed := domain.LandingOfRelay(landing)
		if !landed.OK {
			return refuse(CodeLandingUnfit, "node %s is not a TCP TLS or REALITY listener (reason: %s): its relay line has no SNI to route by and cannot share a port",
				map[string]any{"node": landing.DisplayName, "node_id": landing.ID, "reason": landed.Reason, "protocol": landing.DesiredProtocol}, nodeLabel(landing), landed.Reason)
		}
		if bad := domain.UnacceptedFrontName(landed.ServerNames, names); bad != "" {
			return refuse(CodeNameNotServed, "node %s does not serve %q, so the relay line's clients never send it: use one of its own server names",
				map[string]any{"node": landing.DisplayName, "node_id": landing.ID, "name": bad, "served": landed.ServerNames}, nodeLabel(landing), bad)
		}
		*target = domain.FrontTarget{Kind: domain.FrontTargetForward, NodeID: target.NodeID, LineID: target.LineID}
	case domain.FrontTargetLink:
		return refuse(CodeLinkUnsupported, "a tunnel exit cannot share a port yet", nil)
	case domain.FrontTargetExternal:
		return refuse(CodeBadTarget, "an external site can only be the default, not a route", nil)
	default:
		return refuse(CodeBadTarget, "a route needs a target: a listener or a relay line", nil)
	}
	return nil
}

// normalizeAddress is a host as it is compared with another: lowercase, no
// brackets, no trailing dot, an IPv4-mapped address as the IPv4 one.
func normalizeAddress(raw string) string {
	raw = strings.ToLower(strings.TrimSpace(raw))
	raw = strings.Trim(raw, "[]")
	raw = strings.TrimSuffix(raw, ".")
	if addr, err := netip.ParseAddr(raw); err == nil {
		return addr.Unmap().String()
	}
	return raw
}

func (s *Service) validateDefault(ctx context.Context, panelID int64, port int, def domain.FrontTarget, onPanel []*domain.Node) (domain.FrontTarget, string, error) {
	if def.Kind == "" && def.Host == "" {
		return domain.FrontTarget{}, "", refuse(CodeDefaultMissing,
			"choose the site that unknown or absent server names go to: an external camouflage site", nil)
	}
	if def.Kind != domain.FrontTargetExternal {
		return domain.FrontTarget{}, "", refuse(CodeDefaultNotExternal, "the default must be an external site", nil)
	}
	host := normalizeAddress(def.Host)
	hostAddr, hostIsIP := netip.Addr{}, false
	if addr, err := netip.ParseAddr(host); err == nil {
		hostAddr, hostIsIP = addr, true
	} else if err := validateHostName(host); err != nil || host == "" {
		why := "it is empty"
		if err != nil {
			why = err.Error()
		}
		return domain.FrontTarget{}, "", refuse(CodeDefaultBad, "default site %q is not usable: %s", map[string]any{"host": host, "why": why}, host, why)
	}
	if def.Port < 1 || def.Port > 65535 {
		return domain.FrontTarget{}, "", refuse(CodeDefaultBad, "the default site's port must be between 1 and 65535", map[string]any{"host": host, "port": def.Port})
	}
	if host == "localhost" || strings.HasSuffix(host, ".localhost") {
		return domain.FrontTarget{}, "", refuse(CodeDefaultIsSelf, "default site %s is this machine", map[string]any{"host": host}, host)
	}
	if hostIsIP && (hostAddr.IsLoopback() || hostAddr.IsUnspecified() || (hostAddr.Is4() && hostAddr.As4()[0] == 0)) {
		return domain.FrontTarget{}, "", refuse(CodeDefaultIsSelf, "default site %s is this machine", map[string]any{"host": host}, host)
	}
	out := domain.FrontTarget{Kind: domain.FrontTargetExternal, Host: host, Port: def.Port}
	if def.Port != port {
		return out, "", nil
	}
	// The default must not be this server's own public address on the shared
	// port: a connection relayed there comes straight back into the front.
	raw, err := s.ownAddresses(ctx, panelID, onPanel)
	if err != nil {
		return domain.FrontTarget{}, "", err
	}
	selfRefusal := func(via string) *Refusal {
		return refuse(CodeDefaultIsSelf,
			"default site %s:%d is this server's own public address%s on the shared port: it would loop back into the front",
			map[string]any{"host": host, "port": port, "address": via}, host, port, via)
	}
	// The same spelling as one of the server's own addresses is refused whether
	// or not DNS can say anything about it.
	if raw.names[host] {
		return domain.FrontTarget{}, "", selfRefusal("")
	}
	toResolve := slices.Clone(raw.toResolve)
	if !hostIsIP {
		toResolve = append(toResolve, host)
	}
	resolved := s.resolveAll(ctx, toResolve)
	own := map[netip.Addr]bool{}
	for addr := range raw.ips {
		own[addr] = true
	}
	for _, name := range raw.toResolve {
		for _, text := range resolved[name] {
			if addr, err := netip.ParseAddr(text); err == nil {
				own[addr.Unmap()] = true
			}
		}
	}
	if hostIsIP {
		if own[hostAddr] {
			return domain.FrontTarget{}, "", selfRefusal("")
		}
		return out, "", nil
	}
	warning := ""
	if len(resolved[host]) == 0 {
		warning = WarnDefaultUnresolved
	}
	for _, text := range resolved[host] {
		if addr, err := netip.ParseAddr(text); err == nil && own[addr.Unmap()] {
			return domain.FrontTarget{}, "", selfRefusal(" (" + addr.Unmap().String() + ")")
		}
	}
	return out, warning, nil
}

// ownSet is what the panel knows this server by.
type ownSet struct {
	// names are the spellings (normalized) of every address the server has.
	names map[string]bool
	// ips are the ones that are IP literals.
	ips map[netip.Addr]bool
	// toResolve are the ones that are names, to turn into addresses.
	toResolve []string
}

// ownAddresses is the addresses the panel knows this server by: the address its
// agent last synced from, and every node address on the server.
func (s *Service) ownAddresses(ctx context.Context, panelID int64, onPanel []*domain.Node) (ownSet, error) {
	out := ownSet{names: map[string]bool{}, ips: map[netip.Addr]bool{}}
	candidates := []string{}
	if agent, err := s.agents.GetByPanelID(ctx, panelID); err == nil && agent != nil {
		candidates = append(candidates, agent.ObservedAddress)
	} else if err != nil && !errors.Is(err, domain.ErrNotFound) {
		return ownSet{}, err
	}
	for _, n := range onPanel {
		candidates = append(candidates, n.ServerAddress)
	}
	seen := map[string]bool{}
	for _, raw := range candidates {
		text := normalizeAddress(raw)
		if text == "" || seen[text] {
			continue
		}
		seen[text] = true
		out.names[text] = true
		if addr, err := netip.ParseAddr(text); err == nil {
			out.ips[addr.Unmap()] = true
		} else {
			out.toResolve = append(out.toResolve, text)
		}
	}
	return out, nil
}

// resolveAll resolves every name in parallel under one overall budget, so a
// save never waits longer than dnsBudget however many names it has. A name that
// does not answer in time is simply absent from the result.
func (s *Service) resolveAll(ctx context.Context, names []string) map[string][]string {
	out := map[string][]string{}
	if len(names) == 0 {
		return out
	}
	lookupCtx, cancel := context.WithTimeout(ctx, dnsBudget)
	defer cancel()
	var (
		mu sync.Mutex
		wg sync.WaitGroup
	)
	seen := map[string]bool{}
	for _, name := range names {
		if seen[name] {
			continue
		}
		seen[name] = true
		wg.Add(1)
		go func() {
			defer wg.Done()
			addrs, err := s.resolver.LookupHost(lookupCtx, name)
			if err != nil {
				return
			}
			mu.Lock()
			out[name] = addrs
			mu.Unlock()
		}()
	}
	wg.Wait()
	return out
}

// contractCheck compiles the server's fronts as they would be sent and holds
// them to the wire contract, so a save the agent would refuse is refused here.
// It writes nothing: the forwards it needs keys for get placeholders.
func (s *Service) contractCheck(ctx context.Context, panelID int64, nodes []*domain.Node, fronts []domain.TransitFront) error {
	byID := map[int64]*domain.Node{}
	present := map[int64]bool{}
	for _, n := range nodes {
		if n != nil {
			byID[n.ID] = n
			if n.PanelID == panelID {
				present[n.ID] = true
			}
		}
	}
	plan, err := s.compile(ctx, panelID, fronts, byID, present, true)
	if err != nil {
		return err
	}
	body := nodeprotocol.Transit{Fronts: plan.Fronts, Forwards: plan.Forwards}
	if err := nodeprotocol.ValidateTransit(&body); err != nil {
		return refuse(CodeRejectedByContract, "the wire contract refuses this front: %s", map[string]any{"why": err.Error()}, err.Error())
	}
	return nil
}

// Suggestions are pre-fills for the editor. Nothing here is stored or sent
// until the administrator saves it.

// SuggestLoopbackPort returns a free loopback port in the pre-fill range.
func (s *Service) SuggestLoopbackPort(ctx context.Context, panelID int64) (int, error) {
	nodes, err := s.nodes.List(ctx)
	if err != nil {
		return 0, err
	}
	fronts, err := s.fronts.ListByPanel(ctx, panelID)
	if err != nil {
		return 0, err
	}
	var onPanel []*domain.Node
	for _, n := range nodes {
		if n != nil && n.PanelID == panelID && !n.IsSeparator() {
			onPanel = append(onPanel, n)
		}
	}
	use, err := s.collectUse(ctx, panelID, onPanel, nodes, fronts)
	if err != nil {
		return 0, err
	}
	return suggestPort(use, randInt)
}

// SuggestPublicPort is a port nothing on the server holds, in the range a
// tunnel listener starts from, for the editor's "random" button. It reads and
// stores nothing.
func (s *Service) SuggestPublicPort(ctx context.Context, panelID int64) (int, error) {
	nodes, err := s.nodes.List(ctx)
	if err != nil {
		return 0, err
	}
	fronts, err := s.fronts.ListByPanel(ctx, panelID)
	if err != nil {
		return 0, err
	}
	var onPanel []*domain.Node
	for _, n := range nodes {
		if n != nil && n.PanelID == panelID && !n.IsSeparator() {
			onPanel = append(onPanel, n)
		}
	}
	use, err := s.collectUse(ctx, panelID, onPanel, nodes, fronts)
	if err != nil {
		return 0, err
	}
	const low, high = 20000, 59999
	span := high - low + 1
	start := randInt(span)
	for i := 0; i < span; i++ {
		port := low + (start+i)%span
		if !use.has(port) {
			return port, nil
		}
	}
	return 0, fmt.Errorf("front: no free port in %d-%d", low, high)
}

// SuggestCamouflage is a camouflage site a new default can start from, the
// same pool REALITY links use.
func (s *Service) SuggestCamouflage() domain.FrontTarget {
	sites := transit.RealityCamouflageSites
	return domain.FrontTarget{Kind: domain.FrontTargetExternal, Host: sites[randInt(len(sites))], Port: 443}
}

// targetKeyOf names a route target for grouping.
func targetKeyOf(t domain.FrontTarget) string {
	if t.Kind == domain.FrontTargetListener {
		return fmt.Sprintf("listener:%d", t.NodeID)
	}
	return fmt.Sprintf("forward:%d/%s", t.NodeID, t.LineID)
}

func renderedRefusal(node *domain.Node, rendered string) *Refusal {
	return refuse(CodeRenderedNameMissing,
		"the routes to node %s must include %q, the server name its subscription gives its clients; without it they would land on the default site",
		map[string]any{"node": node.DisplayName, "node_id": node.ID, "name": rendered}, nodeLabel(node), rendered)
}

// sharingListeners are a front's listeners that are enabled and sit on its
// port: the ones that would all bind it if the front went away.
func sharingListeners(f domain.TransitFront, nodes []*domain.Node) []string {
	byID := map[int64]*domain.Node{}
	for _, n := range nodes {
		if n != nil {
			byID[n.ID] = n
		}
	}
	var out []string
	for _, item := range f.Listeners {
		if n := byID[item.NodeID]; n != nil && n.Enabled && n.PanelID == f.PanelID && n.DesiredPort == f.Port {
			out = append(out, nodeLabel(n))
		}
	}
	return out
}

// shareRefusal refuses what would leave two or more listeners on one port.
func shareRefusal(port int, listeners []string, action string) *Refusal {
	return refuse(CodeListenersShare,
		"listeners %s share port %d: Node cannot run two listeners on one port, so change each one but one to its own port first (each such save takes it out of the front), then %s the shared port",
		map[string]any{"port": port, "nodes": listeners, "action": action}, strings.Join(listeners, ", "), port, action)
}
