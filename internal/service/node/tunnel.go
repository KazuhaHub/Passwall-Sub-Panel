package node

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand/v2"
	"strings"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/pkg/log"
	"github.com/KazuhaHub/passwall-sub-panel/internal/pkg/realitykey"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
	"github.com/KazuhaHub/passwall-sub-panel/internal/service/front"
	"github.com/KazuhaHub/passwall-sub-panel/internal/service/inboundcfg"
	"github.com/KazuhaHub/passwall-sub-panel/internal/service/transit"
)

// Tunnel-mode relay lines (domain/transit_tunnel.go): a line of a landing node
// on an entry server that is carried by a hidden listener of its own. This file
// holds what the node service does to keep the two in step: making the listener
// when the line is made, deleting it with the line or the landing, switching a
// line between the two modes, and re-copying the landing's protocol.
//
// Everything here runs with the locks of the landing's server and of the entry
// server of each of its tunnel lines held (UpdateMetadata, SetEnabled and the
// delete paths take them), and uses the *Locked variants of the node writers,
// because those locks are not reentrant.

// TunnelConfirmation is the administrator's go-ahead for what removes a tunnel
// listener: deleting a tunnel line, switching it back to a direct forward, or
// deleting its landing. Without it the service refuses, naming the listeners.
type TunnelConfirmation struct {
	Confirmed bool
	// Plan is the plan token of the confirmation that was shown (params.plan of
	// the 409), echoed back by the client: the ports and kinds it listed. A save
	// that creates listeners is made only if it still plans exactly that.
	Plan string
}

type tunnelConfirmKey struct{}

// WithTunnelConfirmation puts a request's confirmation in its context.
func WithTunnelConfirmation(ctx context.Context, c *TunnelConfirmation) context.Context {
	return context.WithValue(ctx, tunnelConfirmKey{}, c)
}

func tunnelPlanOf(ctx context.Context) string {
	c, _ := ctx.Value(tunnelConfirmKey{}).(*TunnelConfirmation)
	if c == nil {
		return ""
	}
	return c.Plan
}

func tunnelConfirmed(ctx context.Context) bool {
	c, _ := ctx.Value(tunnelConfirmKey{}).(*TunnelConfirmation)
	return c != nil && c.Confirmed
}

type routeSinkKey struct{}

// routeSink collects the removed listeners that had routes by destination. The
// tunnels only their routes used are cleaned up by the entry point that holds
// the locks, after it has let go of them (the cleanup takes server locks).
type routeSink struct{ nodes []*domain.Node }

func withRouteSink(ctx context.Context) (context.Context, *routeSink) {
	sink := &routeSink{}
	return context.WithValue(ctx, routeSinkKey{}, sink), sink
}

// cleanup runs afterRemove for each collected listener.
func (s *Service) cleanup(ctx context.Context, sink *routeSink) {
	for _, n := range sink.nodes {
		s.afterRemove(ctx, n)
	}
}

// TunnelLinker makes the tunnel between two servers when a tunnel line first
// needs it, so the administrator sees it before the first sync. Late-bound
// (SetTunnelLinks); nil leaves it to the sync, which makes it the same way.
type TunnelLinker interface {
	EnsureLink(ctx context.Context, entry, exit int64) (domain.TransitLink, error)
	// PlanLink says which port on the exit server the tunnel from entry uses: the
	// existing link's, or the one a new link will be made with (reserved by this
	// call, so what a confirmation lists is what is then created).
	PlanLink(ctx context.Context, entry, exit int64) (port int, exists bool, err error)
	PlanLinkPort(ctx context.Context, entry, exit int64, requested int, occupied ...int) (port int, exists bool, err error)
	// RemoveLink deletes a tunnel that nothing uses.
	RemoveLink(ctx context.Context, entry, exit int64) error
}

// SetTunnelLinks binds the tunnel maker.
func (s *Service) SetTunnelLinks(l TunnelLinker) { s.tunnelLinks = l }

// Refusal parameters of a tunnel confirmation. kind is always "tunnel".
const tunnelKind = "tunnel"

// TunnelItem is one listener a confirmation names, or one the action creates.
type TunnelItem struct {
	NodeID        int64  `json:"node_id,omitempty"`
	Name          string `json:"name,omitempty"`
	LineID        string `json:"line_id"`
	LineName      string `json:"line_name"`
	EntryPanelID  int64  `json:"entry_panel_id"`
	Port          int    `json:"port,omitempty"`
	FrontPort     int    `json:"front_port,omitempty"`
	LandingNodeID int64  `json:"landing_node_id,omitempty"`
	LandingName   string `json:"landing_name,omitempty"`
	ForwardPort   int    `json:"forward_port,omitempty"`
	// ExitPanelID is the landing's server. LinkPort is the port the tunnel
	// listens on there, LinkNew says the tunnel does not exist yet and opens it.
	// The listener's own Port opens (or closes) on EntryPanelID.
	ExitPanelID int64 `json:"exit_panel_id,omitempty"`
	LinkPort    int   `json:"link_port,omitempty"`
	LinkNew     bool  `json:"link_new,omitempty"`
	// Kind is "forward" for an item that opens the direct forward's entry port
	// (ForwardPort on EntryPanelID) when a tunnel line goes back to a forward;
	// empty for a listener.
	Kind string `json:"kind,omitempty"`
}

func tunnelRefusal(action string, creates, removes []TunnelItem) *front.Refusal {
	names := make([]string, 0, len(removes))
	for _, item := range removes {
		names = append(names, fmt.Sprintf("%q (port %d)", firstText(item.Name, item.LineName), item.Port))
	}
	message := "this change creates and removes tunnel listeners and subscribers must refresh their subscription: confirm it first"
	if len(names) > 0 {
		message = fmt.Sprintf("this removes the tunnel listener %s, and subscribers must refresh their subscription: confirm it first", strings.Join(names, ", "))
	}
	return &front.Refusal{Code: front.CodeConfirmRequired, Message: message, Params: map[string]any{
		"kind": tunnelKind, "action": action, "creates": creates, "removes": removes, "refresh_needed": true,
		// What the client sends back with its confirmation.
		"plan": previewOf(creates),
	}}
}

func firstText(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

// tunnelEntryPanels are the servers the tunnel lines of a node enter through.
func tunnelEntryPanels(n *domain.Node) []int64 {
	var out []int64
	if n == nil {
		return out
	}
	for _, line := range n.Relays {
		if line.Tunnel() && line.EntryPanelID != 0 {
			out = append(out, line.EntryPanelID)
		}
	}
	return out
}

// tunnelChild loads the listener of a tunnel line, nil when it is not there.
func (s *Service) tunnelChild(ctx context.Context, line domain.RelayLine) (*domain.Node, error) {
	if !line.Tunnel() || line.TunnelNodeID == 0 {
		return nil, nil
	}
	child, err := s.nodes.GetByID(ctx, line.TunnelNodeID)
	if errors.Is(err, domain.ErrNotFound) {
		return nil, nil
	}
	return child, err
}

// tunnelItemOf describes an existing line and its listener for a confirmation.
func (s *Service) tunnelItemOf(ctx context.Context, landing *domain.Node, line domain.RelayLine, child *domain.Node) TunnelItem {
	item := TunnelItem{LineID: line.ID, LineName: firstText(line.Name, line.Address), EntryPanelID: line.EntryPanelID,
		LandingNodeID: landing.ID, LandingName: landing.DisplayName, ForwardPort: line.Port, ExitPanelID: landing.PanelID}
	if child != nil {
		item.NodeID, item.Name, item.Port = child.ID, child.DisplayName, child.DesiredPort
		item.FrontPort = s.frontPortOf(ctx, child)
	}
	return item
}

// frontPortOf is the shared port a listener sits behind, 0 when none.
func (s *Service) frontPortOf(ctx context.Context, n *domain.Node) int {
	if s.fronts == nil || n == nil {
		return 0
	}
	fronts, err := s.liveFronts(ctx)
	if err != nil {
		return 0
	}
	for _, f := range fronts {
		if f.PanelID == n.PanelID && f.HasListener(n.ID) {
			return f.Port
		}
	}
	return 0
}

// tunnelDeleteRefusal is the confirmation a landing's delete needs when it owns
// tunnel listeners: they go with it.
func (s *Service) tunnelDeleteRefusal(ctx context.Context, landing *domain.Node, action string) error {
	if tunnelConfirmed(ctx) {
		return nil
	}
	var removes []TunnelItem
	for _, line := range landing.Relays {
		child, err := s.tunnelChild(ctx, line)
		if err != nil {
			return err
		}
		if child != nil {
			removes = append(removes, s.tunnelItemOf(ctx, landing, line, child))
		}
	}
	if len(removes) == 0 {
		return nil
	}
	return tunnelRefusal(action, nil, removes)
}

// tunnelEnabledRefusal checks, before anything is saved, what the landing in
// its new state does to the shared ports its tunnel listeners sit behind:
// disabling one sends its names to the front's default site, and enabling one
// beside another listener on a port that is not being sent would leave both
// down. Each asks for the confirmation a plain node would, and a refusal here
// leaves everything as it was. landing carries the state about to be saved
// (Enabled and the lines' Enabled flags).
func (s *Service) tunnelEnabledRefusal(ctx context.Context, landing *domain.Node) error {
	if s.frontSvc == nil || s.fronts == nil || front.ConfirmationFrom(ctx).Confirmed {
		return nil
	}
	for _, line := range landing.Relays {
		child, err := s.tunnelChild(ctx, line)
		if err != nil {
			return err
		}
		if child == nil {
			continue
		}
		want := landing.Enabled && line.Enabled
		switch {
		case !want && child.Enabled:
			if refusal, err := s.frontSvc.DisableImpact(ctx, child); err != nil {
				return err
			} else if refusal != nil {
				return refusal
			}
		case want && !child.Enabled:
			if refusal, err := s.frontSvc.EnableRefusal(ctx, child); err != nil {
				return err
			} else if refusal != nil {
				return refusal
			}
		}
	}
	return nil
}

// removeTunnelListeners deletes every tunnel listener of a landing that is being
// deleted. The caller has the administrator's confirmation, which covers a
// listener leaving the shared port it sits behind.
func (s *Service) removeTunnelListeners(ctx context.Context, landing *domain.Node) error {
	for _, line := range landing.Relays {
		child, err := s.tunnelChild(ctx, line)
		if err != nil {
			return err
		}
		if child == nil {
			continue
		}
		if err := s.removeTunnelNode(ctx, child); err != nil {
			return err
		}
	}
	return nil
}

// removeTunnelNode deletes a tunnel listener: it leaves its shared port, its
// clients are detached with the line, and the row goes. Its server is native, so
// there is no upstream inbound to wait for. The caller holds its server's lock.
func (s *Service) removeTunnelNode(ctx context.Context, child *domain.Node) error {
	ctx = front.WithConfirmation(ctx, &front.Confirmation{Confirmed: true})
	change, err := s.checkRemovalAgainstFronts(ctx, child)
	if err != nil {
		return err
	}
	if err := s.cleaner.UnclaimAllForInbound(ctx, child.PanelID, child.InboundID); err != nil {
		return fmt.Errorf("unclaim owned clients: %w", err)
	}
	if err := s.finishFront(ctx, child, change); err != nil {
		return err
	}
	routed, err := s.removeNode(ctx, child.ID)
	if err != nil {
		return err
	}
	if sink, _ := ctx.Value(routeSinkKey{}).(*routeSink); sink != nil && routed != nil {
		sink.nodes = append(sink.nodes, routed)
	}
	s.inboundsChanged(ctx, child.PanelID)
	return nil
}

// followTunnelLines brings each tunnel listener of the landing in step with its
// line and the landing: enabled only while both are, the line's name,
// address and traffic factor (which the entry server's own planning reads off
// the listener). It writes only what differs.
func (s *Service) followTunnelLines(ctx context.Context, landing *domain.Node) error {
	for _, line := range landing.Relays {
		child, err := s.tunnelChild(ctx, line)
		if err != nil {
			return err
		}
		if child == nil {
			continue
		}
		want := *child
		want.DisplayName = domain.TunnelLineLabel(landing.DisplayName, firstText(line.Name, line.Address))
		want.ServerAddress = line.Address
		factor := line.EffectiveTrafficFactor()
		want.TrafficFactorMilli = &factor
		want.SortOrder = landing.SortOrder
		if child.DisplayName != want.DisplayName || child.ServerAddress != want.ServerAddress ||
			child.EffectiveTrafficFactor() != factor || child.SortOrder != want.SortOrder {
			if err := s.nodes.UpdateMetadata(ctx, &want); err != nil {
				return err
			}
		}
		enabled := landing.Enabled && line.Enabled
		if child.Enabled != enabled {
			if err := s.setEnabledLocked(ctx, child, enabled); err != nil {
				return err
			}
		}
	}
	return nil
}

// ---- Making the listener ----

// portOwner is the thing whose port is being chosen, which must not count as a
// conflict with itself: an existing listener, or the forward line a switch to a
// tunnel replaces.
type portOwner struct {
	selfID    int64
	landingID int64
	lineID    string
}

func (o portOwner) holds(forward bool, nodeID int64, lineID string) bool {
	if forward {
		return o.landingID != 0 && nodeID == o.landingID && lineID == o.lineID
	}
	return o.selfID != 0 && nodeID == o.selfID
}

// plannedListenerPort settles the port a new listener will be made with and
// remembers it, so that the confirmation that lists it and the retry that is
// confirmed agree: the port picked for a line is reused while it is still free.
// An explicit port is checked and used as given.
func (s *Service) plannedListenerPort(ctx context.Context, landingID int64, lineID string, entry int64, want int) (int, error) {
	owner := portOwner{landingID: landingID, lineID: lineID}
	key := fmt.Sprintf("%d/%s/%d", landingID, lineID, entry)
	if want == 0 {
		s.plannedMu.Lock()
		port, ok := s.planned[key]
		s.plannedMu.Unlock()
		if ok && s.checkTunnelPort(ctx, entry, port, owner) == nil {
			return port, nil
		}
	}
	port, err := s.tunnelPort(ctx, entry, want, owner)
	if err != nil {
		return 0, err
	}
	s.plannedMu.Lock()
	if s.planned == nil {
		s.planned = map[string]int{}
	}
	s.planned[key] = port
	s.plannedMu.Unlock()
	return port, nil
}

// tunnelPort picks a free public port on the entry server for a new listener,
// or checks the one the administrator named.
func (s *Service) tunnelPort(ctx context.Context, panelID int64, want int, owner portOwner) (int, error) {
	if want != 0 {
		if err := s.checkTunnelPort(ctx, panelID, want, owner); err != nil {
			return 0, err
		}
		return want, nil
	}
	for tries := 0; tries < 200; tries++ {
		port := 20000 + rand.IntN(40000)
		if s.checkTunnelPort(ctx, panelID, port, owner) == nil {
			return port, nil
		}
	}
	return 0, fmt.Errorf("%w: no free port was found on the entry server", domain.ErrValidation)
}

// checkTunnelPort refuses a listener port that something else on the server
// holds, naming it: another listener, a forward, a tunnel into the server, a
// shared port's loopback or Node's own sockets. A shared port itself is not a
// conflict: the listener joins it, with the usual confirmation.
func (s *Service) checkTunnelPort(ctx context.Context, panelID int64, port int, owner portOwner) error {
	if port < 1 || port > 65535 {
		return fmt.Errorf("%w: port %d is out of range", domain.ErrValidation, port)
	}
	if s.frontSvc != nil && s.fronts != nil {
		usage, err := s.frontSvc.Usage(ctx, panelID)
		if err != nil {
			return err
		}
		for _, entry := range usage {
			inside := entry.Port == port || (entry.RangeHigh != 0 && port >= entry.Port && port <= entry.RangeHigh)
			if !inside || entry.Kind == front.KindFront || owner.holds(entry.Kind == front.KindForward, entry.NodeID, entry.LineID) {
				continue
			}
			return fmt.Errorf("%w: port %d on the entry server is already used by the %s", domain.ErrValidation, port, entry.Label)
		}
		return nil
	}
	nodes, err := s.nodes.List(ctx)
	if err != nil {
		return err
	}
	for _, other := range nodes {
		if other == nil || other.IsSeparator() || (owner.selfID != 0 && other.ID == owner.selfID) {
			continue
		}
		if other.PanelID == panelID && other.DesiredPort == port {
			return fmt.Errorf("%w: port %d on the entry server is already used by the inbound of node %q", domain.ErrValidation, port, other.DisplayName)
		}
		for _, line := range other.Relays {
			if line.Managed() && line.EntryPanelID == panelID && line.Port == port && !owner.holds(true, other.ID, line.ID) {
				return fmt.Errorf("%w: port %d on the entry server is already used by a relay line of node %q", domain.ErrValidation, port, other.DisplayName)
			}
		}
	}
	return nil
}

// entryBinding makes a stream copied from the landing the entry's own where it
// would carry the landing's binding: no PROXY protocol is expected (the entry's
// users connect directly), and REALITY's xver is 0.
func entryBinding(stream map[string]any) {
	var visit func(v any)
	visit = func(v any) {
		switch t := v.(type) {
		case map[string]any:
			for k, x := range t {
				if k == "acceptProxyProtocol" {
					t[k] = false
					continue
				}
				if k == "xver" {
					t[k] = 0
					continue
				}
				visit(x)
			}
		case []any:
			for _, x := range t {
				visit(x)
			}
		}
	}
	visit(stream)
}

// tunnelSettings is the listener a new tunnel line starts from.
type tunnelSettings struct {
	Spec ports.InboundSpec
	Flow string
	// Copied is true when the landing's protocol was copied, false when the
	// landing could not be (see domain.TunnelProtocolCopyable) and the default
	// VLESS + REALITY + Vision listener was made.
	Copied bool
}

const defaultTunnelSniffing = `{"enabled":true,"destOverride":["http","tls","quic","fakedns"],"metadataOnly":false,"routeOnly":false}`

// newTunnelSettings builds the spec of a new tunnel listener: the landing's
// protocol with fresh REALITY secrets and names, or the default listener. All of
// it is stored on the listener, visible and editable; nothing is derived later.
func newTunnelSettings(landing *domain.Node, name string, port int, sni, target string) (tunnelSettings, error) {
	priv, pub, err := realitykey.GenerateKeypair()
	if err != nil {
		return tunnelSettings{}, err
	}
	shortID, err := realitykey.GenerateShortID()
	if err != nil {
		return tunnelSettings{}, err
	}
	if sni == "" {
		sni = transit.RealityCamouflageSites[rand.IntN(len(transit.RealityCamouflageSites))]
	}
	if target == "" {
		target = sni + ":443"
	}
	// What the landing's REALITY listener keeps when it is copied is its options;
	// the names and every secret are the entry's own and are made new.
	own := func(reality map[string]any) map[string]any {
		reality["target"] = target
		reality["serverNames"] = []string{sni}
		reality["privateKey"] = priv
		reality["shortIds"] = []string{shortID}
		delete(reality, "dest")
		// A post-quantum seed is a secret of the landing's listener, and its
		// verify key is the matching client half: neither is carried over.
		if _, has := reality["mldsa65Seed"]; has {
			reality["mldsa65Seed"] = ""
		}
		settings, _ := reality["settings"].(map[string]any)
		if settings == nil {
			settings = map[string]any{"fingerprint": "chrome", "serverName": "", "spiderX": "/", "mldsa65Verify": ""}
			reality["settings"] = settings
		}
		settings["publicKey"] = pub
		if _, has := settings["mldsa65Verify"]; has {
			settings["mldsa65Verify"] = ""
		}
		return reality
	}
	if domain.TunnelProtocolCopyable(landing) {
		if spec, err := inboundcfg.SpecFromNode(landing); err == nil {
			var stream map[string]any
			if json.Unmarshal([]byte(spec.StreamSettings), &stream) == nil && stream != nil {
				if reality, ok := stream["realitySettings"].(map[string]any); ok {
					stream["realitySettings"] = own(reality)
					entryBinding(stream)
					encoded, _ := json.Marshal(stream)
					spec.StreamSettings = string(encoded)
					// The landing's bind address and allocation are its own: the listener
					// starts on every address, and the administrator can change that.
					spec.Remark, spec.Port, spec.Enable, spec.ExpiryTime = name, port, true, 0
					spec.Listen, spec.Allocate = "", ""
					spec.Settings = tunnelListenerSettings(spec.Settings)
					return tunnelSettings{Spec: spec, Flow: landing.Flow, Copied: true}, nil
				}
			}
		}
	}
	reality := own(map[string]any{"show": false, "xver": 0, "minClientVer": "", "maxClientVer": "", "maxTimediff": 0, "mldsa65Seed": ""})
	stream := map[string]any{
		"network": "tcp", "security": "reality", "externalProxy": []any{},
		"realitySettings": reality,
		"tcpSettings":     map[string]any{"acceptProxyProtocol": false, "header": map[string]any{"type": "none"}},
	}
	encoded, _ := json.Marshal(stream)
	return tunnelSettings{Spec: ports.InboundSpec{
		Remark: name, Enable: true, Port: port, Protocol: string(domain.ProtoVLESS),
		Settings:       `{"clients":[],"decryption":"none","fallbacks":[]}`,
		StreamSettings: string(encoded), Sniffing: defaultTunnelSniffing,
	}, Flow: "xtls-rprx-vision"}, nil
}

// tunnelJob is one listener to make for a line of a landing.
type tunnelJob struct {
	index int
	port  int
	// linkNew: the tunnel to the landing's server did not exist when planned.
	linkNew bool
}

// tunnelWork is what one save of a landing's lines does to tunnel listeners,
// worked out before anything is written.
type tunnelWork struct {
	// refusal is the confirmation the save needs, settled with the plan but
	// returned only after the other validations, so a save that would fail anyway
	// is not first asked to be confirmed.
	refusal error
	// dropping are the listeners about to be removed: their ports do not count
	// against what takes their place.
	dropping []int64
	// links are the tunnels this save made (not ones that were there), to be
	// dropped again if the save is undone and nothing else uses them.
	links   [][2]int64
	creates []tunnelJob
	removes []*domain.Node
	removed []TunnelItem
	created []*domain.Node
}

// planTunnels settles the tunnel lines of a save: the server-owned fields come
// from the stored lines, a changed mode is allowed only through the switch, and
// the listeners to make and to delete are worked out and validated. It writes
// nothing. A removal needs the administrator's confirmation.
func (s *Service) planTunnels(ctx context.Context, previous, n *domain.Node, switching map[string]bool) (*tunnelWork, error) {
	work := &tunnelWork{}
	prevByID := make(map[string]domain.RelayLine, len(previous.Relays))
	for _, line := range previous.Relays {
		prevByID[line.ID] = line
	}
	var creates []TunnelItem
	kept := map[string]bool{}
	switched := false
	for i := range n.Relays {
		line := &n.Relays[i]
		prev, known := prevByID[line.ID]
		if known {
			kept[line.ID] = true
			if line.ModeUnresolved {
				line.Mode = prev.Mode
			}
			if line.Mode != prev.Mode {
				if !switching[line.ID] {
					return nil, fmt.Errorf("%w: relay line %d: the mode changes only through the confirmed mode switch", domain.ErrValidation, i+1)
				}
				switched = true
			}
			if prev.Tunnel() && line.Tunnel() {
				if line.EntryPanelID != prev.EntryPanelID {
					return nil, fmt.Errorf("%w: relay line %d: the entry server of a tunnel line cannot change: remove the line and add another", domain.ErrValidation, i+1)
				}
				line.TunnelNodeID, line.Port = prev.TunnelNodeID, 0
				continue
			}
		}
		if !line.Tunnel() {
			line.TunnelNodeID = 0
			if known && prev.Tunnel() {
				// Back to a direct forward: its entry port opens on the entry server.
				creates = append(creates, TunnelItem{Kind: "forward", LineID: line.ID, LineName: firstText(line.Name, line.Address),
					EntryPanelID: line.EntryPanelID, ForwardPort: line.Port, LandingNodeID: previous.ID, LandingName: previous.DisplayName})
			}
			continue
		}
		// A tunnel line made now (new, or switched from a forward).
		// The listener's port is settled now, so that what a confirmation lists
		// is the port it is then made with; so is the tunnel's port.
		port, err := s.plannedListenerPort(ctx, previous.ID, line.ID, line.EntryPanelID, line.Port)
		if err != nil {
			return nil, fmt.Errorf("relay line %q: %w", firstText(line.Name, line.Address), err)
		}
		job := tunnelJob{index: i, port: port}
		work.creates = append(work.creates, job)
		jobIndex := len(work.creates) - 1
		line.TunnelNodeID, line.Port = 0, 0
		item := TunnelItem{LineID: line.ID, LineName: firstText(line.Name, line.Address), EntryPanelID: line.EntryPanelID,
			LandingNodeID: previous.ID, LandingName: previous.DisplayName, Port: port, ExitPanelID: previous.PanelID}
		if s.tunnelLinks != nil && line.EntryPanelID != previous.PanelID {
			if linkPort, exists, err := s.tunnelLinks.PlanLink(ctx, line.EntryPanelID, previous.PanelID); err == nil {
				item.LinkPort, item.LinkNew = linkPort, !exists
				work.creates[jobIndex].linkNew = !exists
			}
		}
		creates = append(creates, item)
	}
	var removes []TunnelItem
	for _, prev := range previous.Relays {
		if !prev.Tunnel() {
			continue
		}
		if kept[prev.ID] {
			if line := lineByID(n.Relays, prev.ID); line != nil && line.Tunnel() {
				continue
			}
		}
		child, err := s.tunnelChild(ctx, prev)
		if err != nil {
			return nil, err
		}
		if child != nil {
			work.removes = append(work.removes, child)
			work.dropping = append(work.dropping, child.ID)
			removes = append(removes, s.tunnelItemOf(ctx, previous, prev, child))
		}
	}
	work.removed = removes
	if err := domain.ValidateTunnelRelays(previous, n.Relays); err != nil {
		return nil, err
	}
	if len(work.creates) > 0 {
		if err := s.checkTunnelEntries(previous, n, work); err != nil {
			return nil, err
		}
	}
	// Switching a line asks first, and so does removing a listener. A new line
	// in tunnel mode is the act of adding it: it creates its listener without.
	// The confirmation carries the ports and kinds that were listed; a confirmed
	// save is made only if it still plans exactly those (the client echoes them,
	// so this holds across admins and restarts), and otherwise asks again with the
	// fresh list, never quietly making others.
	if len(removes) > 0 || switched {
		action := "remove_line"
		if switched {
			action = "switch_mode"
		}
		if !tunnelConfirmed(ctx) || tunnelPlanOf(ctx) != previewOf(creates) {
			work.refusal = tunnelRefusal(action, creates, removes)
		}
	}
	return work, nil
}

// previewOf is the ports a confirmation lists, as one comparable string.
func previewOf(creates []TunnelItem) string {
	var b strings.Builder
	for _, c := range creates {
		fmt.Fprintf(&b, "%s/%s:%d/%d@%d,%d@%d,%v;", c.Kind, c.LineID, c.Port, c.ForwardPort, c.EntryPanelID, c.LinkPort, c.ExitPanelID, c.LinkNew)
	}
	return b.String()
}

func lineByID(lines []domain.RelayLine, id string) *domain.RelayLine {
	for i := range lines {
		if lines[i].ID == id {
			return &lines[i]
		}
	}
	return nil
}

// checkTunnelEntries checks the servers of the tunnel lines about to be made:
// the entry must be a Passwall Node server, and so must the landing's (the
// tunnel's exit).
func (s *Service) checkTunnelEntries(landing, n *domain.Node, work *tunnelWork) error {
	kinds := make(map[int64]domain.PanelKind)
	if s.pool != nil {
		for _, panel := range s.pool.List() {
			if panel != nil {
				kinds[panel.ID] = domain.NormalizePanelKind(panel.Kind)
			}
		}
	}
	if kinds[landing.PanelID] != domain.PanelKindPSP {
		return fmt.Errorf("%w: a tunnel line needs its landing on a Passwall Node server: the tunnel leaves from the landing's server", domain.ErrValidation)
	}
	for _, job := range work.creates {
		line := n.Relays[job.index]
		if kinds[line.EntryPanelID] != domain.PanelKindPSP {
			return fmt.Errorf("%w: relay line %d: the entry server must be a Passwall Node server", domain.ErrValidation, job.index+1)
		}
	}
	return nil
}

// makeTunnels creates the listeners of the planned lines and stores their ids
// on the lines. A failure undoes the ones already made.
func (s *Service) makeTunnels(ctx context.Context, landing, n *domain.Node, work *tunnelWork) error {
	for _, job := range work.creates {
		line := &n.Relays[job.index]
		child, err := s.makeTunnel(ctx, landing, *line, job.port)
		if err != nil {
			s.undoTunnels(ctx, work)
			if errors.Is(err, transit.ErrReservedPortTaken) {
				return s.freshRefusal(ctx, landing, n, work)
			}
			return err
		}
		if job.linkNew {
			work.links = append(work.links, [2]int64{child.PanelID, landing.PanelID})
		}
		line.TunnelNodeID, line.Port = child.ID, 0
		work.created = append(work.created, child)
	}
	return nil
}

// freshRefusal is the confirmation to ask again when the port promised to a new
// tunnel was taken before it was made: the same save, with the ports as they are
// planned now. It is a 409 like any other confirmation, so the editor lists them
// and the admin decides.
func (s *Service) freshRefusal(ctx context.Context, landing, n *domain.Node, work *tunnelWork) error {
	var creates []TunnelItem
	action := "create_line"
	for _, job := range work.creates {
		line := n.Relays[job.index]
		item := TunnelItem{LineID: line.ID, LineName: firstText(line.Name, line.Address), EntryPanelID: line.EntryPanelID,
			LandingNodeID: landing.ID, LandingName: landing.DisplayName, Port: job.port, ExitPanelID: landing.PanelID}
		if s.tunnelLinks != nil {
			if port, exists, err := s.tunnelLinks.PlanLink(ctx, line.EntryPanelID, landing.PanelID); err == nil {
				item.LinkPort, item.LinkNew = port, !exists
			}
		}
		if lineByID(landing.Relays, line.ID) != nil {
			action = "switch_mode"
		}
		creates = append(creates, item)
	}
	return tunnelRefusal(action, creates, nil)
}

// makeTunnel creates one listener for a line of the landing, on the line's
// entry server, leaving through the landing's server.
func (s *Service) makeTunnel(ctx context.Context, landing *domain.Node, line domain.RelayLine, port int) (*domain.Node, error) {
	port, err := s.tunnelPort(ctx, line.EntryPanelID, port, portOwner{landingID: landing.ID, lineID: line.ID})
	if err != nil {
		return nil, fmt.Errorf("relay line %q: %w", firstText(line.Name, line.Address), err)
	}
	name := domain.TunnelLineLabel(landing.DisplayName, firstText(line.Name, line.Address))
	settings, err := newTunnelSettings(landing, name, port, "", "")
	if err != nil {
		return nil, err
	}
	factor := line.EffectiveTrafficFactor()
	child := &domain.Node{
		PanelID: line.EntryPanelID, DisplayName: name, ServerAddress: line.Address, Flow: settings.Flow,
		// The listener belongs to no group and is never rendered as a node, so its
		// labels are the landing's; the line's own labels (ADR 0040) stay display
		// state of the line.
		Region: landing.Region, Tags: domain.CopyLineTags(landing.Tags), SortOrder: landing.SortOrder, Kind: domain.NodeKindReal,
		TrafficFactorMilli: &factor, EgressPanelID: landing.PanelID,
		ParentNodeID: landing.ID, ParentLineID: line.ID,
	}
	if err := s.createInboundLocked(ctx, child, settings.Spec); err != nil {
		// The row may have been stored before the failure (joining a shared port is
		// the step after it): it is removed, so nothing is left for undo to miss.
		if child.ID != 0 {
			_ = s.removeTunnelNode(ctx, child)
		}
		return nil, err
	}
	if child.ID == 0 {
		return nil, fmt.Errorf("the listener of tunnel line %q was not made", firstText(line.Name, line.Address))
	}
	// A line that is off (or whose landing is) gets a listener that is off,
	// settled here, before the line is saved, so that nothing can fail after.
	if !landing.Enabled || !line.Enabled {
		if err := s.nodes.UpdateEnabled(ctx, child.ID, false); err != nil {
			_ = s.removeTunnelNode(ctx, child)
			return nil, err
		}
		child.Enabled = false
	}
	if s.tunnelLinks != nil {
		if _, err := s.tunnelLinks.EnsureLink(ctx, child.PanelID, landing.PanelID); errors.Is(err, transit.ErrReservedPortTaken) {
			// The port a confirmation listed is gone: never make the tunnel on
			// another one behind its back; the save is undone and asks again.
			_ = s.removeTunnelNode(ctx, child)
			return nil, err
		} else if err != nil {
			// The sync makes the tunnel the same way; the line is withheld until
			// it exists, so this is not a failure of the save.
			log.Warn("tunnel line: the tunnel between the servers was not made yet", "entry", child.PanelID, "exit", landing.PanelID, "err", err)
		}
	}
	return child, nil
}

// undoTunnels deletes the listeners a failed save already made.
func (s *Service) undoTunnels(ctx context.Context, work *tunnelWork) {
	for _, child := range work.created {
		if err := s.removeTunnelNode(ctx, child); err != nil {
			log.Warn("tunnel line: a listener made by a failed save was not removed", "node_id", child.ID, "err", err)
		}
	}
	work.created = nil
	// A tunnel this save made goes with it when nothing else uses the pair.
	for _, pair := range work.links {
		s.dropLinkIfUnused(ctx, pair[0], pair[1])
	}
	work.links = nil
}

// dropLinkIfUnused removes the tunnel from entry to exit when no listener of the
// entry server leaves through it and no route does. The caller holds both
// servers' locks.
func (s *Service) dropLinkIfUnused(ctx context.Context, entry, exit int64) {
	if s.tunnelLinks == nil {
		return
	}
	nodes, err := s.nodes.List(ctx)
	if err != nil {
		return
	}
	for _, n := range nodes {
		if n == nil || n.PanelID != entry {
			continue
		}
		if n.EgressPanelID == exit {
			return
		}
		for _, route := range n.DestRoutes {
			if route.Target.Kind == domain.DestRouteServer && route.Target.PanelID == exit {
				return
			}
		}
	}
	if err := s.tunnelLinks.RemoveLink(ctx, entry, exit); err != nil {
		log.Warn("tunnel line: a tunnel made by a failed save was not removed", "entry", entry, "exit", exit, "err", err)
	}
}

// dropTunnels deletes the listeners of the lines a save removed or switched.
func (s *Service) dropTunnels(ctx context.Context, work *tunnelWork) error {
	for _, child := range work.removes {
		if err := s.removeTunnelNode(ctx, child); err != nil {
			return err
		}
	}
	return nil
}

// ---- Switching the mode of a line ----

// SwitchLineMode changes one relay line between a direct forward and a tunnel.
// It is an explicit, confirmed action: a refusal lists what it creates and
// removes (and says subscribers must refresh); with the confirmation in ctx it
// does it. Switching to a forward needs the entry port the forward will use
// (0 picks a free one); switching to a tunnel takes the listener's port (0
// picks one). The line keeps its id, its name, labels and traffic factor.
func (s *Service) SwitchLineMode(ctx context.Context, landingID int64, lineID, mode string, port int) (*domain.Node, error) {
	if mode != "" && mode != domain.RelayModeTunnel {
		return nil, fmt.Errorf("%w: unknown mode %q", domain.ErrValidation, mode)
	}
	ctx, sink := withRouteSink(ctx)
	defer s.cleanup(ctx, sink)
	// The line is read, and the switch planned from it, under the locks of both
	// servers: a concurrent save cannot change the lines between the two.
	landing, unlock, err := s.lockAndLoad(ctx, landingID, nil)
	if err != nil {
		return nil, err
	}
	defer unlock()
	if landing.IsTunnelChild() {
		return nil, fmt.Errorf("%w: node %q is the listener of a tunnel line", domain.ErrValidation, landing.DisplayName)
	}
	next := *landing
	next.Relays = append([]domain.RelayLine(nil), landing.Relays...)
	line := lineByID(next.Relays, lineID)
	if line == nil {
		return nil, fmt.Errorf("%w: node %d has no relay line %q", domain.ErrNotFound, landingID, lineID)
	}
	if line.EntryPanelID == 0 {
		return nil, fmt.Errorf("%w: only a line run by a Passwall Node server has a mode", domain.ErrValidation)
	}
	if line.Mode == mode {
		return landing, nil
	}
	line.Mode, line.ModeUnresolved = mode, false
	if mode == domain.RelayModeTunnel {
		line.Port = port
	} else {
		line.TunnelNodeID = 0
		if port == 0 {
			// Back to a forward with no port named: the forward takes the
			// listener's own port (a port the admin already sees and can edit),
			// so the same request always plans the same port.
			if stored := lineByID(landing.Relays, lineID); stored != nil && stored.Tunnel() {
				if child, err := s.tunnelChild(ctx, *stored); err != nil {
					return nil, err
				} else if child != nil {
					port = child.DesiredPort
				}
			}
			if port == 0 && line.Port != 0 {
				port = line.Port
			}
			if port == 0 {
				p, err := s.plannedListenerPort(ctx, landing.ID, lineID, line.EntryPanelID, 0)
				if err != nil {
					return nil, err
				}
				port = p
			}
		}
		line.Port = port
	}
	if err := s.updateMetadataLocked(ctx, landing, &next, map[string]bool{lineID: true}); err != nil {
		return nil, err
	}
	return s.nodes.GetByID(ctx, landingID)
}

// ---- Editing and re-copying the listener ----

// TunnelListenerEdit is what an administrator may set on the listener of a
// tunnel line from the line. Empty fields keep what is stored; every value is
// stored as given. The full listener (transport, sniffing, ...) is edited as a
// node.
type TunnelListenerEdit struct {
	Port        int
	ServerName  string
	Target      string
	ShortID     string
	PrivateKey  string
	PublicKey   string
	Fingerprint string
}

// tunnelOf finds a landing's tunnel line and its listener.
func (s *Service) tunnelOf(ctx context.Context, landingID int64, lineID string) (*domain.Node, domain.RelayLine, *domain.Node, error) {
	landing, err := s.nodes.GetByID(ctx, landingID)
	if err != nil {
		return nil, domain.RelayLine{}, nil, err
	}
	line := lineByID(landing.Relays, lineID)
	if line == nil || !line.Tunnel() {
		return nil, domain.RelayLine{}, nil, fmt.Errorf("%w: node %d has no tunnel line %q", domain.ErrNotFound, landingID, lineID)
	}
	child, err := s.tunnelChild(ctx, *line)
	if err != nil {
		return nil, domain.RelayLine{}, nil, err
	}
	if child == nil {
		return nil, domain.RelayLine{}, nil, fmt.Errorf("%w: the listener of tunnel line %q is gone", domain.ErrNotFound, lineID)
	}
	return landing, *line, child, nil
}

// UpdateTunnelListener sets the port and REALITY names and keys of a tunnel
// line's listener. A port that something else on the server holds is refused,
// naming it, as is a key pair that does not match.
func (s *Service) UpdateTunnelListener(ctx context.Context, landingID int64, lineID string, edit TunnelListenerEdit) error {
	_, _, peek, err := s.tunnelOf(ctx, landingID, lineID)
	if err != nil {
		return err
	}
	defer s.lockFronts(peek.PanelID)()
	// Built from the listener as it is under the lock, not as it was read before.
	_, _, child, err := s.tunnelOf(ctx, landingID, lineID)
	if err != nil {
		return err
	}
	spec, err := inboundcfg.SpecFromNode(child)
	if err != nil {
		return err
	}
	if edit.Port != 0 {
		spec.Port = edit.Port
	}
	var stream map[string]any
	if json.Unmarshal([]byte(spec.StreamSettings), &stream) != nil || stream == nil {
		return fmt.Errorf("%w: the listener has no stream settings to edit", domain.ErrValidation)
	}
	reality, _ := stream["realitySettings"].(map[string]any)
	if reality == nil {
		if edit.ServerName != "" || edit.Target != "" || edit.ShortID != "" || edit.PrivateKey != "" || edit.PublicKey != "" {
			return fmt.Errorf("%w: the listener does not use REALITY: edit it as a node", domain.ErrValidation)
		}
	} else {
		// The ids and names go to the entry's Xray as they are, so a bad one would
		// stop it from starting: check them with the link protocol's own rules.
		if err := domain.ValidateRealityNames(strings.ToLower(strings.TrimSpace(edit.ServerName)), strings.ToLower(strings.TrimSpace(edit.ShortID))); err != nil {
			return err
		}
		if edit.ServerName != "" {
			reality["serverNames"] = []string{strings.ToLower(strings.TrimSpace(edit.ServerName))}
			if edit.Target == "" {
				if _, hasDest := reality["dest"]; !hasDest {
					reality["target"] = strings.ToLower(strings.TrimSpace(edit.ServerName)) + ":443"
				}
			}
		}
		if edit.Target != "" {
			if _, hasDest := reality["dest"]; hasDest {
				reality["dest"] = strings.TrimSpace(edit.Target)
			} else {
				reality["target"] = strings.TrimSpace(edit.Target)
			}
		}
		if edit.ShortID != "" {
			reality["shortIds"] = []string{strings.ToLower(strings.TrimSpace(edit.ShortID))}
		}
		if edit.PrivateKey != "" {
			reality["privateKey"] = strings.TrimSpace(edit.PrivateKey)
		}
		settings, _ := reality["settings"].(map[string]any)
		if settings == nil {
			settings = map[string]any{}
			reality["settings"] = settings
		}
		if edit.PublicKey != "" {
			settings["publicKey"] = strings.TrimSpace(edit.PublicKey)
		}
		if edit.Fingerprint != "" {
			settings["fingerprint"] = strings.TrimSpace(edit.Fingerprint)
		}
		if names, _ := reality["serverNames"].([]string); len(names) == 0 {
			if anyNames, _ := reality["serverNames"].([]any); len(anyNames) == 0 {
				return fmt.Errorf("%w: a REALITY listener needs a server name", domain.ErrValidation)
			}
		}
		stream["realitySettings"] = reality
	}
	encoded, _ := json.Marshal(stream)
	spec.StreamSettings = string(encoded)
	return s.updateInboundConfigLockedNode(ctx, child, spec)
}

// TunnelSyncDiff is one setting a re-copy from the landing changes.
type TunnelSyncDiff struct {
	Field string `json:"field"`
	From  string `json:"from"`
	To    string `json:"to"`
}

// tunnelListenerSettings is the landing's protocol settings as the entry's
// listener gets them: no clients, no fallbacks (they point at ports of the
// landing's own server) and no decryption (a VLESS-encryption string holds the
// landing's server key). Decryption is "none", like a fresh listener's.
func tunnelListenerSettings(settingsJSON string) string {
	settingsJSON = inboundcfg.StripClients(settingsJSON)
	var m map[string]json.RawMessage
	if json.Unmarshal([]byte(settingsJSON), &m) != nil || m == nil {
		return settingsJSON
	}
	delete(m, "fallbacks")
	if _, has := m["decryption"]; has {
		m["decryption"] = json.RawMessage(`"none"`)
	}
	out, err := json.Marshal(m)
	if err != nil {
		return settingsJSON
	}
	return string(out)
}

// tunnelFromLanding is the listener's spec as a re-copy from the landing makes
// it: the landing's protocol, settings and transport, keeping the listener's
// own port, REALITY names and keys. ok is false when the landing's protocol
// cannot be copied (see domain.TunnelProtocolCopyable).
func tunnelFromLanding(landing, child *domain.Node) (spec ports.InboundSpec, flow string, ok bool, err error) {
	if !domain.TunnelProtocolCopyable(landing) {
		return ports.InboundSpec{}, "", false, nil
	}
	spec, err = inboundcfg.SpecFromNode(landing)
	if err != nil {
		return ports.InboundSpec{}, "", false, err
	}
	var theirs, mine map[string]any
	if json.Unmarshal([]byte(spec.StreamSettings), &theirs) != nil || theirs == nil {
		return ports.InboundSpec{}, "", false, nil
	}
	if json.Unmarshal([]byte(child.StreamSettings), &mine) != nil || mine == nil {
		return ports.InboundSpec{}, "", false, nil
	}
	own, _ := mine["realitySettings"].(map[string]any)
	reality, _ := theirs["realitySettings"].(map[string]any)
	if own == nil || reality == nil {
		return ports.InboundSpec{}, "", false, nil
	}
	for _, key := range []string{"serverNames", "target", "dest", "privateKey", "shortIds"} {
		delete(reality, key)
		if value, has := own[key]; has {
			reality[key] = value
		}
	}
	settings, _ := reality["settings"].(map[string]any)
	if settings == nil {
		settings = map[string]any{}
		reality["settings"] = settings
	}
	if ownSettings, _ := own["settings"].(map[string]any); ownSettings != nil {
		if key, has := ownSettings["publicKey"]; has {
			settings["publicKey"] = key
		}
	}
	// Secrets of the landing's listener are never carried over, whatever the
	// landing holds: the post-quantum seed and its verify key stay blank.
	if _, has := reality["mldsa65Seed"]; has {
		reality["mldsa65Seed"] = ""
	}
	if st, _ := reality["settings"].(map[string]any); st != nil {
		if _, has := st["mldsa65Verify"]; has {
			st["mldsa65Verify"] = ""
		}
	}
	theirs["realitySettings"] = reality
	entryBinding(theirs)
	encoded, _ := json.Marshal(theirs)
	spec.StreamSettings = string(encoded)
	spec.Settings = tunnelListenerSettings(spec.Settings)
	spec.Allocate = child.Allocate
	spec.Remark, spec.Port, spec.Listen, spec.Enable, spec.ExpiryTime = child.InboundRemark, child.DesiredPort, child.InboundListen, child.Enabled, 0
	return spec, landing.Flow, true, nil
}

// PreviewTunnelSync lists what re-copying the landing's protocol settings would
// change on a tunnel line's listener. It changes nothing.
func (s *Service) PreviewTunnelSync(ctx context.Context, landingID int64, lineID string) ([]TunnelSyncDiff, error) {
	landing, _, child, err := s.tunnelOf(ctx, landingID, lineID)
	if err != nil {
		return nil, err
	}
	spec, flow, ok, err := tunnelFromLanding(landing, child)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, fmt.Errorf("%w: the landing's protocol cannot be copied to an entry server (only a REALITY listener can): the listener keeps its own settings", domain.ErrValidation)
	}
	return diffTunnel(child, spec, flow), nil
}

// diffPlan is the digest of a diff (already redacted), the plan token of a sync.
func diffPlan(diffs []TunnelSyncDiff) string {
	encoded, _ := json.Marshal(diffs)
	sum := sha256.Sum256(encoded)
	return "sync:" + hex.EncodeToString(sum[:8])
}

func diffTunnel(child *domain.Node, spec ports.InboundSpec, flow string) []TunnelSyncDiff {
	var out []TunnelSyncDiff
	add := func(field, from, to string) {
		if from != to {
			out = append(out, TunnelSyncDiff{Field: field, From: from, To: to})
		}
	}
	add("protocol", child.DesiredProtocol, strings.ToLower(spec.Protocol))
	add("flow", child.Flow, flow)
	add("settings", compactJSON(redactSecrets(child.InboundSettings)), compactJSON(redactSecrets(spec.Settings)))
	add("stream_settings", compactJSON(redactSecrets(child.StreamSettings)), compactJSON(redactSecrets(spec.StreamSettings)))
	add("sniffing", compactJSON(child.Sniffing), compactJSON(spec.Sniffing))
	return out
}

// redactSecrets blanks every secret in a settings or stream document, so a diff
// (and the audit row made from it) never carries one: REALITY's private key and
// post-quantum seed and verify key, passwords and keys, and every client entry.
func redactSecrets(raw string) string {
	var v any
	if json.Unmarshal([]byte(raw), &v) != nil {
		return raw
	}
	var walk func(v any)
	walk = func(v any) {
		switch t := v.(type) {
		case map[string]any:
			for k, x := range t {
				switch k {
				case "decryption":
					if str, ok := x.(string); ok && (str == "" || str == "none") {
						continue
					}
					t[k] = "***"
				case "privateKey", "mldsa65Seed", "mldsa65Verify", "password", "psk", "key", "secret", "clients", "id", "auth", "uuid":
					if str, ok := x.(string); ok && str == "" {
						continue
					}
					t[k] = "***"
				default:
					walk(x)
				}
			}
		case []any:
			for _, x := range t {
				walk(x)
			}
		}
	}
	walk(v)
	out, _ := json.Marshal(v)
	return string(out)
}

func compactJSON(raw string) string {
	var v any
	if json.Unmarshal([]byte(raw), &v) != nil {
		return raw
	}
	out, _ := json.Marshal(v)
	return string(out)
}

// SyncTunnelFromLanding re-copies the landing's protocol settings onto the
// listener of a tunnel line, keeping its port, names and keys. It is explicit:
// without the administrator's confirmation it refuses with the differences.
func (s *Service) SyncTunnelFromLanding(ctx context.Context, landingID int64, lineID string) ([]TunnelSyncDiff, error) {
	landing, _, child, err := s.tunnelOf(ctx, landingID, lineID)
	if err != nil {
		return nil, err
	}
	defer s.lockFronts(landing.PanelID, child.PanelID)()
	// Read again under the locks: the diff is of what is stored now.
	landing, line, child, err := s.tunnelOf(ctx, landingID, lineID)
	if err != nil {
		return nil, err
	}
	spec, flow, ok, err := tunnelFromLanding(landing, child)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, fmt.Errorf("%w: the landing's protocol cannot be copied to an entry server (only a REALITY listener can): the listener keeps its own settings", domain.ErrValidation)
	}
	diffs := diffTunnel(child, spec, flow)
	// The plan is a digest of the redacted diff: the confirmed request echoes it,
	// and if the landing (or the listener) changed since the diff was shown, the
	// admin is asked again with the new diff.
	plan := diffPlan(diffs)
	if !tunnelConfirmed(ctx) || tunnelPlanOf(ctx) != plan {
		return diffs, &front.Refusal{Code: front.CodeConfirmRequired, Message: "re-copying the landing's settings changes the listener: confirm it first",
			Params: map[string]any{"kind": tunnelKind, "action": "sync_from_landing", "diff": diffs,
				"line_id": line.ID, "refresh_needed": true, "plan": plan}}
	}
	if len(diffs) == 0 {
		return diffs, nil
	}
	if child.Flow != flow {
		updated := *child
		updated.Flow = flow
		if err := s.nodes.UpdateMetadata(ctx, &updated); err != nil {
			return nil, err
		}
	}
	return diffs, s.updateInboundConfigLocked(ctx, child.ID, spec)
}

// tunnelListenerRefusal refuses to move a tunnel listener's port onto
// something else's: it runs on every inbound save of a tunnel listener.
func (s *Service) tunnelListenerRefusal(ctx context.Context, child *domain.Node, spec ports.InboundSpec) error {
	if !child.IsTunnelChild() {
		return nil
	}
	if err := realityKeyPairRefusal(spec.StreamSettings); err != nil {
		return err
	}
	if spec.Port == 0 || spec.Port == child.DesiredPort {
		return nil
	}
	return s.checkTunnelPort(ctx, child.PanelID, spec.Port, portOwner{selfID: child.ID})
}

// realityKeyPairRefusal refuses a REALITY stream whose public key is not the one
// its private key derives: clients given the public key could never connect.
func realityKeyPairRefusal(streamSettings string) error {
	var stream struct {
		RealitySettings struct {
			PrivateKey string `json:"privateKey"`
			Settings   struct {
				PublicKey string `json:"publicKey"`
			} `json:"settings"`
		} `json:"realitySettings"`
	}
	if json.Unmarshal([]byte(streamSettings), &stream) != nil {
		return nil
	}
	priv, pub := stream.RealitySettings.PrivateKey, stream.RealitySettings.Settings.PublicKey
	if priv == "" && pub == "" {
		return nil
	}
	derived, err := realitykey.PublicFromPrivate(priv)
	if err != nil {
		return fmt.Errorf("%w: REALITY private key: %v", domain.ErrValidation, err)
	}
	if pub != derived {
		return fmt.Errorf("%w: the REALITY public key does not match the private key: clients given it could not connect (the key pair is regenerated with 随机生成, or the public key is derived from the private key)", domain.ErrValidation)
	}
	return nil
}

// isOrphanListener reports whether a hidden tunnel listener has no line: its
// landing is gone, or no tunnel line of the landing is served by it.
func (s *Service) isOrphanListener(ctx context.Context, child *domain.Node) (bool, error) {
	if !child.IsTunnelChild() {
		return false, nil
	}
	landing, err := s.nodes.GetByID(ctx, child.ParentNodeID)
	if errors.Is(err, domain.ErrNotFound) {
		return true, nil
	}
	if err != nil {
		return false, err
	}
	for _, line := range landing.Relays {
		if line.ID == child.ParentLineID && line.Tunnel() && line.TunnelNodeID == child.ID {
			return false, nil
		}
	}
	return true, nil
}

// removeOrphanListener is the one way a hidden listener is deleted by itself:
// when its line is gone (a delete that was cut short), so that nothing is left
// that cannot be removed. A listener that still has a line is refused, pointing
// at the line.
func (s *Service) removeOrphanListener(ctx context.Context, child *domain.Node) error {
	// The landing's server (when the landing exists) and the entry server are
	// locked first, and the listener is judged an orphan under them: a save that
	// holds the same locks and has just made the listener, or is about to point a
	// line at it, cannot be raced.
	panels := []int64{child.PanelID}
	if parent, err := s.nodes.GetByID(ctx, child.ParentNodeID); err == nil {
		panels = append(panels, parent.PanelID)
	}
	defer s.lockFronts(panels...)()
	fresh, err := s.nodes.GetByID(ctx, child.ID)
	if errors.Is(err, domain.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	orphan, err := s.isOrphanListener(ctx, fresh)
	if err != nil {
		return err
	}
	if !orphan {
		return fmt.Errorf("%w: node %q is the listener of a tunnel line: remove the line instead", domain.ErrValidation, fresh.DisplayName)
	}
	return s.removeTunnelNode(ctx, fresh)
}
