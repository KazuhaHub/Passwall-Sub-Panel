// Package route runs the panel's side of routing by destination
// (transit.route.v1): it stores and validates the routes an administrator
// writes for a node, compiles them into the Transit.Routes a server is sent,
// pre-computes the budget Passwall Node holds a routed compile to, and reports
// whether the routes run.
//
// The rules the package holds:
//
//   - Settings apply literally. A route is what the administrator saved: its
//     match, its position and its target. Lists are compiled to literal entries
//     at send time and the stored route never changes with them; nothing a list
//     or a link does rewrites a route.
//   - Routes go only to a server that declared transit.route.v1 (and
//     transit.link.v1). A server that did not would ignore them and send every
//     destination through the default exit with no error anywhere, so a save is
//     refused with the reason, and routes already saved are shown as not sent.
//   - The default exit stays the node's own (EgressPanelID): a route only
//     overrides it for the destinations it matches. "local" is the node's own
//     server whatever the default is.
//   - Every exit server a route names needs the tunnel from the node's server to
//     it. The link is made the way an egress link is, when the routes are saved,
//     and the administrator can see and edit it like any other.
//   - A node is withheld from subscriptions only by its default exit's link. A
//     routed exit that is down fails the destinations routed to it and is
//     reported; it never falls back to the default exit, and it never takes the
//     node off subscriptions.
//   - A regexp cannot be intersected with the destination policy that applies
//     per exit, so a route refuses a typed one and skips the regexp entries of a
//     list, and says so.
//   - What the node would refuse (the routed budget) is computed when the routes
//     are saved: refused beyond the node's hard limits, warned about from half of
//     them, naming the listener.
package route

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"
	"time"

	nodeprotocol "github.com/KazuhaHub/passwall-protocol/protocol"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/pkg/keyedmutex"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
	"github.com/KazuhaHub/passwall-sub-panel/internal/service/destlist"
	"github.com/KazuhaHub/passwall-sub-panel/internal/service/destpolicy"
)

// NodeStore reads the fleet's nodes.
type NodeStore interface {
	List(ctx context.Context) ([]*domain.Node, error)
	GetByID(ctx context.Context, id int64) (*domain.Node, error)
}

// PanelGetter reads one server.
type PanelGetter interface {
	GetByID(ctx context.Context, id int64) (*domain.XUIPanel, error)
}

// AgentGetter reads the Node agent of a server.
type AgentGetter interface {
	GetByPanelID(ctx context.Context, panelID int64) (*domain.NodeAgent, error)
}

// Links is the tunnel service (service/transit.Service).
type Links interface {
	EnsureLink(ctx context.Context, entry, exit int64) (domain.TransitLink, error)
	LinkRow(ctx context.Context, entry, exit int64) (domain.TransitLink, error)
	ServerAddress(ctx context.Context, panelID int64) (string, error)
	// OutUsable says whether the entry's half of the link to exit can be sent:
	// there is an address to dial and the half passes the wire contract.
	OutUsable(ctx context.Context, entry, exit int64) (bool, error)
	// LinkByID reads a link by its id, domain.ErrNotFound when none exists.
	LinkByID(ctx context.Context, id int64) (domain.TransitLink, error)
	// RemoveLink deletes the link of a pair no node uses any more.
	RemoveLink(ctx context.Context, entry, exit int64) error
}

// Lists is the destination list service (service/destlist.Service).
type Lists interface {
	Lists(ctx context.Context) ([]*domain.DestList, error)
	GetList(ctx context.Context, id int64) (*domain.DestList, error)
	Expand(ctx context.Context, l *domain.DestList) (*destlist.Expanded, error)
}

// Policies is the destination policy compiler (service/destpolicy.Service).
type Policies interface {
	PlanPolicy(ctx context.Context, in destpolicy.CompileInput) (destpolicy.Plan, error)
}

// Desired reads a server's desired config closure.
type Desired interface {
	Load(ctx context.Context, panelID int64) (*ports.NativeDesiredSnapshot, error)
}

// DataState reads the publish state whose GeoIP and geosite release decide what
// a list expands to.
type DataState interface {
	Get(ctx context.Context) (*domain.DestPolicyState, error)
}

// Probes reads the link probe rows, for the quality shown beside each exit.
type Probes interface {
	List(ctx context.Context) ([]domain.ProbeState, error)
}

// Deps is everything a Service is built from. Routes, Nodes, Panels, Agents and
// Links are required; the rest may be nil and the feature they serve degrades:
// without Lists a route cannot name a list, without Policies and Desired the
// budget and the approximation warnings are not computed, without Probes no
// quality is shown.
type Deps struct {
	Routes   ports.TransitRouteRepo
	Nodes    NodeStore
	Panels   PanelGetter
	Agents   AgentGetter
	Links    Links
	Lists    Lists
	Policies Policies
	Desired  Desired
	Data     DataState
	Probes   Probes
	// ServerLock serializes what picks a port on a server (the fronts' lock of
	// the same server), so a tunnel's port is never given twice. Nil takes none.
	ServerLock func(panelID int64) func()
	Now        func() time.Time
}

// Service is the routing service.
type Service struct {
	Deps
	locks keyedmutex.Map[int64]
	mu    sync.Mutex
	// cache holds the last compile of each node's routes, by node id.
	cache map[int64]cached
}

// New returns the service.
func New(d Deps) *Service {
	if d.Now == nil {
		d.Now = time.Now
	}
	return &Service{Deps: d, cache: map[int64]cached{}}
}

// Lock serializes everything that reads and then writes the routes of the nodes
// of one server.
func (s *Service) Lock(panelID int64) func() { return s.locks.Lock(panelID) }

// Refusal is a save the service will not make. Code is stable (the editor
// writes its own wording from it and Params); Message is the English fallback.
type Refusal struct {
	Code    string
	Message string
	Params  map[string]any
}

func (r *Refusal) Error() string { return r.Message }

// Is lets a refusal be matched as a validation error.
func (r *Refusal) Is(target error) bool { return target == domain.ErrValidation }

// Refusal codes.
const (
	CodeNotNode          = "not_node"
	CodeNoAgent          = "no_agent"
	CodeNoCapability     = "no_capability"
	CodeNoLinkCapability = "no_link_capability"
	CodeSeparator        = "separator"
	CodeBadRoute         = "bad_route"
	CodeBadTarget        = "bad_target"
	CodeBadList          = "bad_list"
	CodeBadEntry         = "bad_entry"
	CodeRegexp           = "regexp_in_route"
	CodeNoAddress        = "exit_no_address"
	CodeTooManyEntries   = "too_many_entries"
	CodeOverBudget       = "over_budget"
)

func refuse(code, format string, params map[string]any, args ...any) *Refusal {
	return &Refusal{Code: code, Message: fmt.Sprintf(format, args...), Params: params}
}

// Warning codes: a save that goes through may still carry these.
const (
	WarnApproximate  = "policy_approximate"
	WarnBudget       = "budget_near_limit"
	WarnListNotReady = "list_not_ready"
	WarnListRegexp   = "list_regexp_skipped"
	WarnListLastGood = "list_last_good"
	WarnRouteSkipped = "route_skipped"
	WarnExitNotReady = "exit_not_ready"
	WarnExitNoLink   = "exit_no_link_capability"
	WarnRedundant    = "redundant_route"
)

// Warning is a note on a save or a view: not a refusal, but something the
// administrator should read. Listener is the node the warning is about.
type Warning struct {
	Code     string
	Message  string
	Params   map[string]any
	Listener string
	RouteID  string
}

// Support is whether a server can run routes, and when it cannot, why.
type Support struct {
	Supported bool
	// Reason is a Code* value when not supported.
	Reason string
}

// SupportOf decides from what the panel knows of the server. An agent that has
// not synced yet has declared nothing, so it cannot run routes yet.
func (s *Service) SupportOf(ctx context.Context, panelID int64) (Support, error) {
	panel, err := s.Panels.GetByID(ctx, panelID)
	if err != nil {
		return Support{}, err
	}
	if panel == nil || domain.NormalizePanelKind(panel.Kind) != domain.PanelKindPSP {
		return Support{Reason: CodeNotNode}, nil
	}
	agent, err := s.Agents.GetByPanelID(ctx, panelID)
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
	if !slices.Contains(agent.ObservedCapabilities, nodeprotocol.CapabilityTransitLink) {
		return Support{Reason: CodeNoLinkCapability}
	}
	if !slices.Contains(agent.ObservedCapabilities, nodeprotocol.CapabilityTransitRoute) {
		return Support{Reason: CodeNoCapability}
	}
	return Support{Supported: true}
}

// supportRefusal is the refusal that says why a server cannot run routes.
func supportRefusal(reason string) *Refusal {
	switch reason {
	case CodeNotNode:
		return refuse(reason, "routing by destination needs a Passwall Node server", nil)
	case CodeNoLinkCapability:
		return refuse(reason, "this server's agent has not declared transit.link.v1, which routes need; upgrade it and wait for it to sync", nil)
	case CodeNoCapability:
		return refuse(reason, "this server's agent has not declared transit.route.v1; upgrade it and wait for it to sync", nil)
	default:
		return refuse(CodeNoAgent, "this server has no agent that has reported yet", nil)
	}
}
