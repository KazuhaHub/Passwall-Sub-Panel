package front

import (
	"context"
	"fmt"
	"slices"
	"sort"

	nodeprotocol "github.com/KazuhaHub/passwall-protocol/protocol"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/pkg/log"
)

// Reasons a front is not sent.
const (
	WithheldNoRoutes       = "no_routes"
	WithheldListenerUnfit  = "listener_unfit"
	WithheldListenerPort   = "listener_port"
	WithheldListenerOnPort = "listener_not_behind"
	WithheldContract       = "contract"
)

// Withheld is a front that is saved but not being sent, and why.
type Withheld struct {
	FrontID int64
	Port    int
	Reason  string
	// Detail names the object at fault.
	Detail string
}

// Plan is what a server is sent for its fronts.
type Plan struct {
	Fronts []nodeprotocol.Front
	// Forwards are the forwards the fronts route to, as the contract needs
	// them (TCP, on the front's port). The sync service replaces its own copy
	// of each with this one.
	Forwards []nodeprotocol.Forward
	// FrontedForwards are the keys of those forwards.
	FrontedForwards map[nodeprotocol.ForwardKey]bool
	// FrontIDs maps a sent front's key to its row.
	FrontIDs map[nodeprotocol.FrontKey]int64
	// Ports are the shared ports and every loopback port: ports the sync
	// service must treat as taken for anything that is not behind a front.
	Ports map[int]bool
	// Withheld are the saved fronts that are not in Fronts, with the reason.
	Withheld []Withheld
}

// PlanFor compiles a server's fronts. It returns an empty plan, and the config
// is then what it always was, unless the agent declared transit.sni.v1, its
// desired engine is Xray AND it reports Xray running. present are the nodes of
// the server that are in its config.
//
// A route whose target is unavailable (a landing that is disabled, a line that
// no longer enters here, a disabled listener) is left out, never a reason to
// cost the server its other routes. A front Node would refuse as a whole (a
// fronted listener that cannot be moved, an enabled listener on the shared port
// that is not behind it) is left out entirely and named in Withheld.
func (s *Service) PlanFor(ctx context.Context, panelID int64, capabilities []string, desired, observed domain.NodeCoreEngine, present []int64) (Plan, error) {
	if !slices.Contains(capabilities, nodeprotocol.CapabilityTransitSNI) ||
		domain.NormalizeNodeCoreEngine(desired) != domain.NodeCoreXray || observed != domain.NodeCoreXray {
		return Plan{}, nil
	}
	fronts, err := s.fronts.ListByPanel(ctx, panelID)
	if err != nil || len(fronts) == 0 {
		return Plan{}, err
	}
	nodes, err := s.nodes.List(ctx)
	if err != nil {
		return Plan{}, fmt.Errorf("front: list nodes: %w", err)
	}
	byID := make(map[int64]*domain.Node, len(nodes))
	for _, n := range nodes {
		if n != nil {
			byID[n.ID] = n
		}
	}
	inConfig := make(map[int64]bool, len(present))
	for _, id := range present {
		inConfig[id] = true
	}
	return s.compile(ctx, panelID, fronts, byID, inConfig, false)
}

// compile builds the plan. dry mints no forward rows: it is the save-time
// contract check, which must not write on a save it may refuse.
func (s *Service) compile(ctx context.Context, panelID int64, fronts []domain.TransitFront, byID map[int64]*domain.Node,
	inConfig map[int64]bool, dry bool) (Plan, error) {
	plan := Plan{FrontedForwards: map[nodeprotocol.ForwardKey]bool{}, FrontIDs: map[nodeprotocol.FrontKey]int64{}, Ports: map[int]bool{}}
	forwardSeen := map[nodeprotocol.ForwardKey]bool{}
	var placeholder int64 = 1 << 41
	for _, stored := range fronts {
		withhold := func(reason, detail string) {
			log.Warn("front withheld", "front", stored.ID, "panel_id", panelID, "reason", reason, "detail", detail)
			plan.Withheld = append(plan.Withheld, Withheld{FrontID: stored.ID, Port: stored.Port, Reason: reason, Detail: detail})
		}
		wire := nodeprotocol.Front{Key: nodeprotocol.NewFrontKey(stored.ID), Port: stored.Port}
		listenerOK := map[int64]bool{}
		behind := map[int64]bool{}
		broken := false
		for _, item := range stored.Listeners {
			behind[item.NodeID] = true
			node := byID[item.NodeID]
			switch {
			case node == nil || node.PanelID != panelID || !inConfig[item.NodeID]:
				// Not served on this server: nothing to move.
				continue
			case !node.Enabled:
				// A disabled listener is not served, and Node refuses a front that
				// names one. Its routes go with it; the others stay.
				continue
			case node.DesiredPort != stored.Port:
				withhold(WithheldListenerPort, node.DisplayName)
				broken = true
			case !domain.FrontableOf(node).OK:
				withhold(WithheldListenerUnfit, node.DisplayName)
				broken = true
			}
			if broken {
				break
			}
			listenerOK[item.NodeID] = true
			wire.Listeners = append(wire.Listeners, nodeprotocol.FrontedListener{
				Listener: nodeprotocol.NewListenerKey(item.NodeID), LoopbackPort: item.LoopbackPort,
			})
		}
		if broken {
			continue
		}
		// An enabled listener on the shared port that is not behind the front
		// would bind it too: Node refuses the whole config, so the front is not
		// sent and the listeners keep their ports.
		for _, node := range byID {
			if node != nil && node.PanelID == panelID && inConfig[node.ID] && node.Enabled && !node.IsSeparator() &&
				node.DesiredPort == stored.Port && !behind[node.ID] {
				withhold(WithheldListenerOnPort, node.DisplayName)
				broken = true
				break
			}
		}
		if broken {
			continue
		}
		var forwards []nodeprotocol.Forward
		for _, route := range stored.Routes {
			target := nodeprotocol.FrontTarget{}
			switch route.Target.Kind {
			case domain.FrontTargetListener:
				if !listenerOK[route.Target.NodeID] {
					continue
				}
				target.Listener = nodeprotocol.NewListenerKey(route.Target.NodeID)
			case domain.FrontTargetForward:
				placeholder++
				forward, ok, err := s.forwardOf(ctx, panelID, stored.Port, route.Target, byID, dry, placeholder)
				if err != nil {
					return Plan{}, err
				}
				if !ok {
					continue
				}
				target.Forward = forward.Key
				if !forwardSeen[forward.Key] {
					forwardSeen[forward.Key] = true
					forwards = append(forwards, forward)
				}
			default:
				// A link target is the seam: the model reserves it and nothing
				// stores one yet, so it is never sent.
				continue
			}
			wire.Routes = append(wire.Routes, nodeprotocol.FrontRoute{ServerNames: slices.Clone(route.ServerNames), Target: target})
		}
		if len(wire.Routes) == 0 {
			withhold(WithheldNoRoutes, "")
			continue
		}
		// A listener no remaining route reaches would be moved to a loopback
		// port nothing connects to: leave it where it was instead.
		reached := map[nodeprotocol.ListenerKey]bool{}
		for _, route := range wire.Routes {
			reached[route.Target.Listener] = true
		}
		kept := wire.Listeners[:0]
		for _, item := range wire.Listeners {
			if reached[item.Listener] {
				kept = append(kept, item)
			}
		}
		wire.Listeners = kept
		wire.Default = nodeprotocol.FrontTarget{External: &nodeprotocol.FrontExternal{Host: stored.Default.Host, Port: stored.Default.Port}}
		plan.Fronts = append(plan.Fronts, wire)
		plan.FrontIDs[wire.Key] = stored.ID
		plan.Ports[stored.Port] = true
		for _, item := range wire.Listeners {
			plan.Ports[item.LoopbackPort] = true
		}
		for _, forward := range forwards {
			plan.Forwards = append(plan.Forwards, forward)
			plan.FrontedForwards[forward.Key] = true
		}
	}
	sort.Slice(plan.Fronts, func(i, j int) bool { return plan.Fronts[i].Key < plan.Fronts[j].Key })
	sort.Slice(plan.Forwards, func(i, j int) bool { return plan.Forwards[i].Key < plan.Forwards[j].Key })
	return plan, nil
}

// forwardOf builds the forward a route targets: the managed line's forward,
// relaying TCP only (a front hands over a TCP connection), bound where the
// front is. ok is false when the line cannot be reached or its landing has no
// SNI to route by.
func (s *Service) forwardOf(ctx context.Context, panelID int64, port int, target domain.FrontTarget, byID map[int64]*domain.Node,
	dry bool, placeholder int64) (nodeprotocol.Forward, bool, error) {
	landing := byID[target.NodeID]
	if landing == nil {
		return nodeprotocol.Forward{}, false, nil
	}
	for _, line := range landing.Relays {
		if line.ID != target.LineID {
			continue
		}
		if line.Tunnel() || line.EntryPanelID != panelID || line.Port != port {
			return nodeprotocol.Forward{}, false, nil
		}
		if !domain.LandingOfRelay(landing).OK {
			log.Warn("front route withheld: its landing is not a TCP TLS or REALITY listener", "node_id", landing.ID, "line", line.ID)
			return nodeprotocol.Forward{}, false, nil
		}
		desired, ok := domain.DesiredManagedForward(landing, line)
		if !ok {
			return nodeprotocol.Forward{}, false, nil
		}
		rowID := placeholder
		if !dry {
			row, err := s.forwards.Ensure(ctx, landing.ID, line.ID)
			if err != nil {
				return nodeprotocol.Forward{}, false, fmt.Errorf("front: record line %s of node %d: %w", line.ID, landing.ID, err)
			}
			rowID = row.ID
		}
		forward := nodeprotocol.Forward{
			Key: nodeprotocol.NewForwardKey(rowID), Port: desired.Port, Target: desired.Target,
			TargetPort: desired.TargetPort, Network: nodeprotocol.ForwardNetworkTCP,
		}
		if err := nodeprotocol.ValidateForward(forward); err != nil {
			log.Warn("front route withheld: its forward is invalid", "node_id", landing.ID, "line", line.ID, "err", err)
			return nodeprotocol.Forward{}, false, nil
		}
		return forward, true, nil
	}
	return nodeprotocol.Forward{}, false, nil
}

// Diagnosis is what the editor shows about a server's saved fronts right now.
type Diagnosis struct {
	// Inactive is why no front is being sent at all (a Code* value), "" when
	// the server can run them.
	Inactive string
	// Withheld are the saved fronts that are not being sent, with the reason.
	Withheld []Withheld
}

// Diagnose answers, from what the panel holds now, whether the server's saved
// fronts are in effect. It needs no sync: it asks the same planner.
func (s *Service) Diagnose(ctx context.Context, panelID int64) (Diagnosis, error) {
	support, err := s.SupportOf(ctx, panelID)
	if err != nil {
		return Diagnosis{}, err
	}
	if !support.Supported {
		return Diagnosis{Inactive: support.Reason}, nil
	}
	fronts, err := s.fronts.ListByPanel(ctx, panelID)
	if err != nil || len(fronts) == 0 {
		return Diagnosis{}, err
	}
	nodes, err := s.nodes.List(ctx)
	if err != nil {
		return Diagnosis{}, err
	}
	byID := map[int64]*domain.Node{}
	inConfig := map[int64]bool{}
	for _, n := range nodes {
		if n != nil {
			byID[n.ID] = n
			if n.PanelID == panelID {
				inConfig[n.ID] = true
			}
		}
	}
	plan, err := s.compile(ctx, panelID, fronts, byID, inConfig, true)
	if err != nil {
		return Diagnosis{}, err
	}
	return Diagnosis{Withheld: plan.Withheld}, nil
}

// Without returns the plan minus the given fronts and the forwards only they
// route to.
func (p Plan) Without(drop map[nodeprotocol.FrontKey]bool) Plan {
	out := Plan{FrontedForwards: map[nodeprotocol.ForwardKey]bool{}, FrontIDs: map[nodeprotocol.FrontKey]int64{}, Ports: map[int]bool{}, Withheld: p.Withheld}
	for _, f := range p.Fronts {
		if drop[f.Key] {
			if id, ok := p.FrontIDs[f.Key]; ok {
				out.Withheld = append(slices.Clone(out.Withheld), Withheld{FrontID: id, Port: f.Port, Reason: WithheldContract})
			}
			continue
		}
		out.Fronts = append(out.Fronts, f)
		out.FrontIDs[f.Key] = p.FrontIDs[f.Key]
		out.Ports[f.Port] = true
		for _, item := range f.Listeners {
			out.Ports[item.LoopbackPort] = true
		}
		for _, route := range f.Routes {
			if route.Target.Forward != "" {
				out.FrontedForwards[route.Target.Forward] = true
			}
		}
	}
	for _, forward := range p.Forwards {
		if out.FrontedForwards[forward.Key] {
			out.Forwards = append(out.Forwards, forward)
		}
	}
	return out
}

// Unserved is a listener the server must not be sent: its front is not being
// sent (withheld, deleted, no capability, not Xray) and it would bind the shared
// port together with another listener, which Node refuses as a whole config.
type Unserved struct {
	NodeID  int64
	Name    string
	Port    int
	FrontID int64
	// With names the other listeners that would bind the same port.
	With []string
}

// UnservedListeners mirrors what Node does with a front it cannot run
// (WithoutFront): a front with two or more listeners that is not sent cannot
// give them all their public port, so none of them is sent either; a front with
// one listener lets it fall back to its public port. present are the nodes in
// the server's config; plan is what is actually being sent. It does not depend
// on whether fronts are enabled at all: the stored fronts are what ties the
// listeners together.
func (s *Service) UnservedListeners(ctx context.Context, panelID int64, plan Plan, present []int64) ([]Unserved, error) {
	fronts, err := s.fronts.ListByPanel(ctx, panelID)
	if err != nil || len(fronts) == 0 {
		return nil, err
	}
	nodes, err := s.nodes.List(ctx)
	if err != nil {
		return nil, err
	}
	sent := map[int64]bool{}
	for _, id := range plan.FrontIDs {
		sent[id] = true
	}
	inConfig := map[int64]bool{}
	for _, id := range present {
		inConfig[id] = true
	}
	var out []Unserved
	for _, f := range fronts {
		if sent[f.ID] {
			continue
		}
		var members, others []*domain.Node
		for _, n := range nodes {
			if n == nil || n.PanelID != panelID || n.IsSeparator() || !n.Enabled || !inConfig[n.ID] || n.DesiredPort != f.Port {
				continue
			}
			if f.HasListener(n.ID) {
				members = append(members, n)
			} else {
				others = append(others, n)
			}
		}
		if len(members)+len(others) < 2 || len(members) == 0 {
			continue
		}
		var with []string
		for _, n := range append(slices.Clone(members), others...) {
			with = append(with, nodeLabel(n))
		}
		for _, n := range members {
			out = append(out, Unserved{NodeID: n.ID, Name: n.DisplayName, Port: f.Port, FrontID: f.ID, With: with})
		}
	}
	return out, nil
}
