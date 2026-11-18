package route

import (
	"context"
	"errors"
	"fmt"
	"time"

	nodeprotocol "github.com/KazuhaHub/passwall-protocol/protocol"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/service/destlist"
)

// LinkState is the tunnel one exit of a node is reached through, as the
// administrator sees it. A routed exit that is not ready fails the destinations
// routed to it; it does not take the node off subscriptions.
type LinkState struct {
	PanelID    int64
	ServerName string
	// Gone says the exit server no longer exists: the node's listener is
	// withheld from its server's config until the route is changed or removed.
	Gone   bool
	Exists bool
	ID     int64
	// Ready says both ends hold exactly what was last sent them.
	Ready bool
	// InApplied and OutApplied say each end applied what it was sent: the exit
	// and the entry.
	InApplied  bool
	OutApplied bool
	// PortsDiffer says the exit listens on one port and the entry dials another
	// (InPort, OutPort: what each end was last sent): the tunnel is not ready
	// even though both ends applied, until the end that is behind syncs.
	PortsDiffer bool
	InPort      int
	OutPort     int
	Quality     *domain.ProbeQuality
}

// RouteState is what is known of one route's delivery.
type RouteState struct {
	ID string
	// Entries are the destination entries the route compiles to.
	Entries int
	// Sent says the route is in the config sent to the server; a route that
	// compiled to nothing is not.
	Sent  bool
	Notes []Note
	// Link is the tunnel to the exit, nil for a local route.
	Link *LinkState
}

// View is a node's routes with their state.
type View struct {
	Support Support
	Routes  []domain.DestRoute
	Items   []RouteState
	// Default is where the node leaves when no route matches, with its tunnel
	// when it has one. The node is published only while that tunnel is ready.
	Default     domain.DestRouteTarget
	DefaultLink *LinkState
	// Sent says the server's config holds the node's routes, Running that the
	// server confirmed them.
	Sent      bool
	Running   bool
	SentAt    *time.Time
	AppliedAt *time.Time
	Warnings  []Warning
	Budget    *BudgetView
}

// Stored are a node's saved routes, as they are now.
func (s *Service) Stored(ctx context.Context, nodeID int64) ([]domain.DestRoute, error) {
	stored, err := s.Routes.Get(ctx, nodeID)
	return stored.Routes, err
}

// View returns a node's routes, their delivery state and the warnings the
// current routes carry.
func (s *Service) View(ctx context.Context, nodeID int64) (View, error) {
	node, err := s.Nodes.GetByID(ctx, nodeID)
	if err != nil {
		return View{}, err
	}
	if node.IsSeparator() {
		return View{}, refuse(CodeSeparator, "a separator has no routes", nil)
	}
	support, err := s.SupportOf(ctx, node.PanelID)
	if err != nil {
		return View{}, err
	}
	stored, err := s.Routes.Get(ctx, node.ID)
	if err != nil {
		return View{}, err
	}
	view := View{Support: support, Routes: node.DestRoutes, Sent: stored.Sent != "", Running: stored.Running(), SentAt: stored.SentAt, AppliedAt: stored.AppliedAt}
	if view.Routes == nil {
		view.Routes = []domain.DestRoute{}
	}
	view.Default = domain.DestRouteTarget{Kind: domain.DestRouteLocal}
	if node.EgressPanelID != 0 && node.EgressPanelID != node.PanelID {
		view.Default = domain.DestRouteTarget{Kind: domain.DestRouteServer, PanelID: node.EgressPanelID}
	}
	if len(node.DestRoutes) == 0 {
		return view, nil
	}
	probes := s.probeRows(ctx)
	links := map[int64]*LinkState{}
	stateOf := func(panel int64) *LinkState {
		if state, ok := links[panel]; ok {
			return state
		}
		state := &LinkState{PanelID: panel}
		if p, err := s.Panels.GetByID(ctx, panel); err == nil && p != nil {
			state.ServerName = p.Name
		} else if errors.Is(err, domain.ErrNotFound) {
			state.Gone = true
		}
		if link, err := s.Links.LinkRow(ctx, node.PanelID, panel); err == nil {
			state.Exists, state.ID, state.Ready = true, link.ID, link.Ready()
			state.InApplied = link.InSent != "" && link.InApplied == link.InSent
			state.OutApplied = link.OutSent != "" && link.OutApplied == link.OutSent
			state.InPort, state.OutPort = link.InSentPort, link.OutSentPort
			state.PortsDiffer = !domain.TransitLinkPortsAgree(link.InSentPort, link.OutSentPort)
			key := string(nodeprotocol.NewLinkKey(link.ID))
			var rows []domain.ProbeState
			for _, row := range probes {
				if row.PanelID == node.PanelID && row.ObjectKey == key {
					rows = append(rows, row)
				}
			}
			if len(rows) > 0 {
				quality := domain.QualityOf(rows, s.Now())
				state.Quality = &quality
			}
		}
		links[panel] = state
		return state
	}
	if view.Default.Kind == domain.DestRouteServer {
		view.DefaultLink = stateOf(view.Default.PanelID)
	}
	panels := map[int64]bool{}
	for _, route := range node.DestRoutes {
		if route.Target.Kind == domain.DestRouteServer {
			panels[route.Target.PanelID] = true
		}
	}
	keys, err := s.keysFor(ctx, node.PanelID, panels)
	if err != nil {
		return View{}, err
	}
	index, err := s.listIndex(ctx)
	if err != nil {
		return View{}, err
	}
	data := s.dataKey(ctx)
	out, err := s.compileNode(ctx, node, node.DestRoutes, func(panel int64) (nodeprotocol.LinkKey, bool) {
		key, ok := keys[panel]
		return key, ok
	}, index, data, false, false)
	if err != nil {
		return View{}, err
	}
	sent := map[string]bool{}
	for _, rule := range out.Rules {
		sent[rule.ID] = true
	}
	for _, route := range node.DestRoutes {
		item := RouteState{ID: route.ID, Entries: out.Entries[route.ID], Sent: sent[route.ID]}
		for _, note := range out.Notes {
			if note.Route == route.ID {
				item.Notes = append(item.Notes, note)
			}
		}
		if route.Target.Kind == domain.DestRouteServer {
			item.Link = stateOf(route.Target.PanelID)
		}
		view.Items = append(view.Items, item)
	}
	if !support.Supported {
		view.Warnings = append(view.Warnings, Warning{Code: "routes_not_sent", Params: map[string]any{"reason": support.Reason},
			Message: "the routes are saved but not sent: " + supportRefusal(support.Reason).Message})
	}
	for _, note := range out.Notes {
		view.Warnings = append(view.Warnings, noteWarning(node, note))
	}
	view.Warnings = append(view.Warnings, redundancy(node, node.DestRoutes)...)
	if support.Supported && s.Policies != nil && s.Desired != nil {
		budget, warnings, err := s.budgetView(ctx, node, keys, index, data)
		if err != nil {
			var refusal *Refusal
			if !errors.As(err, &refusal) {
				return View{}, err
			}
			view.Warnings = append(view.Warnings, Warning{Code: WarnBudget, Message: refusal.Message, Params: refusal.Params})
		}
		view.Budget = budget
		view.Warnings = append(view.Warnings, warnings...)
	}
	return view, nil
}

// budgetView is the budget of the saved routes: the same count as a save, read
// as a report rather than a verdict.
func (s *Service) budgetView(ctx context.Context, node *domain.Node, keys map[int64]nodeprotocol.LinkKey, index listIndex, data string) (*BudgetView, []Warning, error) {
	return s.budget(ctx, node, node.DestRoutes, keys, index, data)
}

func (s *Service) probeRows(ctx context.Context) []domain.ProbeState {
	if s.Probes == nil {
		return nil
	}
	rows, err := s.Probes.List(ctx)
	if err != nil {
		return nil
	}
	return rows
}

// ListUses are the nodes whose routes name each list, for refusing to delete a
// list and for its "used by" (destlist.RouteUses).
func (s *Service) ListUses(ctx context.Context) (map[int64][]destlist.RouteRef, error) {
	all, err := s.Routes.List(ctx)
	if err != nil {
		return nil, fmt.Errorf("route: list routes: %w", err)
	}
	out := map[int64][]destlist.RouteRef{}
	for _, item := range all {
		var name string
		seen := map[int64]bool{}
		for _, route := range item.Routes {
			for _, id := range route.ListIDs {
				if seen[id] {
					continue
				}
				seen[id] = true
				if name == "" {
					if node, err := s.Nodes.GetByID(ctx, item.NodeID); err == nil {
						name = node.DisplayName
					}
				}
				out[id] = append(out[id], destlist.RouteRef{NodeID: item.NodeID, Name: name})
			}
		}
	}
	return out, nil
}
