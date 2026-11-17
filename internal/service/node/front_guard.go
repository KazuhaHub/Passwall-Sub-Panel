package node

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/pkg/log"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
	"github.com/KazuhaHub/passwall-sub-panel/internal/service/front"
)

// SetFronts binds the SNI front store and the front service. Unbound, no save
// is checked against shared ports. The service may be nil in a build that only
// guards (a listener that would join a shared port is then refused instead of
// offered).
func (s *Service) SetFronts(fronts ports.TransitFrontRepo, svc *front.Service) {
	s.fronts, s.frontSvc = fronts, svc
}

// lockFronts serializes this save with the front saves of the same servers, so
// a loopback or port pick cannot race. It returns the unlock.
func (s *Service) lockFronts(panelIDs ...int64) func() {
	if s.frontSvc == nil {
		return func() {}
	}
	slices.Sort(panelIDs)
	panelIDs = slices.Compact(panelIDs)
	unlocks := make([]func(), 0, len(panelIDs))
	for _, id := range panelIDs {
		if id != 0 {
			unlocks = append(unlocks, s.frontSvc.Lock(id))
		}
	}
	return func() {
		for i := len(unlocks) - 1; i >= 0; i-- {
			unlocks[i]()
		}
	}
}

// liveFronts lists the fronts whose server still exists. A front of a deleted
// server is an orphan: it must never block an edit of anything.
func (s *Service) liveFronts(ctx context.Context) ([]domain.TransitFront, error) {
	if s.fronts == nil {
		return nil, nil
	}
	all, err := s.fronts.List(ctx)
	if err != nil || s.pool == nil {
		return all, err
	}
	alive := map[int64]bool{}
	for _, panel := range s.pool.List() {
		if panel != nil {
			alive[panel.ID] = true
		}
	}
	out := all[:0:0]
	for _, f := range all {
		if alive[f.PanelID] {
			out = append(out, f)
		}
	}
	return out, nil
}

func frontRouteText(f domain.TransitFront, route domain.FrontRoute) string {
	return fmt.Sprintf("shared port %d, route %s", f.Port, strings.Join(route.ServerNames, ", "))
}

// checkInboundAgainstFronts holds a node's listener to the fronts of its server
// and to the fronts that route to it as a landing. A change that would leave a
// front pointing at something that is no longer there is refused with the front
// and the route named. A listener that lands on a shared port it is not behind
// is not refused: it is offered the front, which needs the administrator's
// confirmation, and joins it on the same save (finishJoin). It is never moved
// silently.
func (s *Service) checkInboundAgainstFronts(ctx context.Context, n *domain.Node) (*frontChange, error) {
	if s.fronts == nil || n == nil || n.IsSeparator() {
		return nil, nil
	}
	fronts, err := s.liveFronts(ctx)
	if err != nil {
		return nil, err
	}
	var sharedPort *domain.TransitFront
	leaving := false
	for i, f := range fronts {
		if f.PanelID == n.PanelID && n.PanelID != 0 {
			switch {
			case n.ID != 0 && f.HasListener(n.ID):
				if n.DesiredPort != f.Port {
					// The listener is moving off the shared port: it leaves the front,
					// which is offered below and needs the admin's confirmation.
					leaving = true
					break
				}
				fr := domain.FrontableOf(n)
				if !fr.OK {
					return nil, fmt.Errorf("%w: node %q is behind the shared port %d and would no longer be a listener a front can carry (%s): remove it from that front first",
						domain.ErrValidation, n.DisplayName, f.Port, fr.Reason)
				}
				var routed []string
				for _, route := range f.Routes {
					if route.Target.Kind == domain.FrontTargetListener && route.Target.NodeID == n.ID {
						routed = append(routed, route.ServerNames...)
						if bad := domain.UnacceptedFrontName(fr.ServerNames, route.ServerNames); bad != "" {
							return nil, fmt.Errorf("%w: node %q no longer serves %q, which %s sends to it: change the route first",
								domain.ErrValidation, n.DisplayName, bad, frontRouteText(f, route))
						}
					}
				}
				if domain.MissingRenderedName(fr.RenderedName, routed) {
					return nil, fmt.Errorf("%w: node %q now renders %q in its subscription, which no route of shared port %d sends to it: change the route first",
						domain.ErrValidation, n.DisplayName, fr.RenderedName, f.Port)
				}
			case n.DesiredPort > 0 && n.DesiredPort == f.Port:
				sharedPort = &fronts[i]
			}
			for _, item := range f.Listeners {
				if n.DesiredPort > 0 && item.LoopbackPort == n.DesiredPort && item.NodeID != n.ID {
					return nil, fmt.Errorf("%w: port %d is the loopback port of a listener behind the shared port %d",
						domain.ErrValidation, n.DesiredPort, f.Port)
				}
			}
			if what, reserved := domain.NodeReservedPort(n.DesiredPort); reserved && n.DesiredPort == f.Port {
				return nil, fmt.Errorf("%w: port %d is Node's %s", domain.ErrValidation, n.DesiredPort, what)
			}
		}
		// As a landing: a route sends the clients of a relay line, by the
		// landing's own SNI, to it.
		if n.ID == 0 {
			continue
		}
		viaLine := map[string][]string{}
		for _, route := range f.Routes {
			if route.Target.Kind == domain.FrontTargetForward && route.Target.NodeID == n.ID {
				viaLine[route.Target.LineID] = append(viaLine[route.Target.LineID], route.ServerNames...)
			}
		}
		for _, route := range f.Routes {
			if route.Target.Kind != domain.FrontTargetForward || route.Target.NodeID != n.ID {
				continue
			}
			landed := domain.LandingOfRelay(n)
			if !landed.OK {
				return nil, fmt.Errorf("%w: node %q is the landing of a relay line routed by %s and would no longer be a TCP TLS or REALITY listener (%s): change the route first",
					domain.ErrValidation, n.DisplayName, frontRouteText(f, route), landed.Reason)
			}
			if bad := domain.UnacceptedFrontName(landed.ServerNames, route.ServerNames); bad != "" {
				return nil, fmt.Errorf("%w: node %q no longer serves %q, which %s sends to its relay line: change the route first",
					domain.ErrValidation, n.DisplayName, bad, frontRouteText(f, route))
			}
			if domain.MissingRenderedName(landed.RenderedName, viaLine[route.Target.LineID]) {
				return nil, fmt.Errorf("%w: node %q now renders %q for its relay line, which no route of %s sends to it: change the route first",
					domain.ErrValidation, n.DisplayName, landed.RenderedName, frontRouteText(f, route))
			}
		}
	}
	if s.frontSvc == nil {
		if leaving {
			return nil, fmt.Errorf("%w: node %q is behind a shared port: remove it from that front first", domain.ErrValidation, n.DisplayName)
		}
		if sharedPort != nil {
			return nil, fmt.Errorf("%w: port %d is a shared port of this server: add the node to that front instead, or choose another port",
				domain.ErrValidation, sharedPort.Port)
		}
		return nil, nil
	}
	// A listener that moves from one shared port straight onto another does both
	// at once, in one confirmation: leaving the first front and joining the second.
	var leave *front.LeaveOffer
	var join *front.JoinOffer
	if leaving {
		if leave, err = s.frontSvc.LeaveOffer(ctx, n, false); err != nil {
			return nil, err
		}
	}
	if sharedPort != nil {
		if join, err = s.frontSvc.JoinOffer(ctx, n); err != nil {
			return nil, err
		}
	}
	if leave == nil && join == nil {
		return nil, nil
	}
	confirmation := front.ConfirmationFrom(ctx)
	if !confirmation.Confirmed {
		return nil, front.MoveRefusal(n, leave, join)
	}
	if leave != nil {
		if err := s.frontSvc.LeaveCheck(ctx, n, leave, false); err != nil {
			return nil, err
		}
	}
	if join != nil {
		if err := s.frontSvc.JoinCheck(ctx, n, join, confirmation.LoopbackPort); err != nil {
			return nil, err
		}
	}
	return &frontChange{leave: leave, join: join}, nil
}

// frontChange is what a node save will do to a front once the node is stored:
// join one, or leave one.
type frontChange struct {
	join  *front.JoinOffer
	leave *front.LeaveOffer
	// removed is true when the node is being deleted rather than moved.
	removed bool
}

// leaveChange offers taking a fronted listener out of its front, asking first
// and checking the result, or does nothing when it is behind none.
func (s *Service) leaveChange(ctx context.Context, n *domain.Node, removed bool) (*frontChange, error) {
	if s.frontSvc == nil {
		return nil, fmt.Errorf("%w: node %q is behind a shared port: remove it from that front first", domain.ErrValidation, n.DisplayName)
	}
	offer, err := s.frontSvc.LeaveOffer(ctx, n, removed)
	if err != nil || offer == nil {
		return nil, err
	}
	if !front.ConfirmationFrom(ctx).Confirmed {
		return nil, offer.LeaveRefusal(n, removed)
	}
	if err := s.frontSvc.LeaveCheck(ctx, n, offer, removed); err != nil {
		return nil, err
	}
	return &frontChange{leave: offer, removed: removed}, nil
}

// finishFront does what the save was offered: adds the stored listener to the
// front, or takes it out, in the same operation, and reports it for the audit.
// A failure here (it was checked beforehand, so it is a race or an outage) is
// returned; the node is then on a shared port it is not behind, which the sync
// answers by withholding what would collide and recording an issue.
func (s *Service) finishFront(ctx context.Context, n *domain.Node, change *frontChange) error {
	if change == nil || s.frontSvc == nil {
		return nil
	}
	confirmation := front.ConfirmationFrom(ctx)
	// Leaving comes first: the listener is off the first port before it takes a
	// place on the second. Each is reported as soon as it is done, so the audit
	// shows what happened even if the next step fails.
	if change.leave != nil {
		left, err := s.frontSvc.Leave(ctx, n, change.leave, change.removed)
		if err != nil {
			log.Warn("front leave failed after the node was saved", "node_id", n.ID, "err", err)
			return err
		}
		confirmation.Left = left
	}
	if change.join != nil {
		joined, err := s.frontSvc.Join(ctx, n, change.join, confirmation.LoopbackPort)
		if err != nil {
			log.Warn("front join failed after the node was saved", "node_id", n.ID, "err", err)
			return err
		}
		confirmation.Joined = joined
	}
	return nil
}

// checkRemovalAgainstFronts holds a node's deletion to the fronts that use it.
// A listener behind a shared port is taken out of it (asked first); a landing
// that a route points at cannot be removed until the route is.
func (s *Service) checkRemovalAgainstFronts(ctx context.Context, n *domain.Node) (*frontChange, error) {
	if s.fronts == nil || n == nil || n.IsSeparator() {
		return nil, nil
	}
	fronts, err := s.liveFronts(ctx)
	if err != nil {
		return nil, err
	}
	behind := false
	for _, f := range fronts {
		if f.PanelID == n.PanelID && f.HasListener(n.ID) {
			behind = true
		}
		for _, route := range f.Routes {
			if route.Target.Kind == domain.FrontTargetForward && route.Target.NodeID == n.ID {
				return nil, fmt.Errorf("%w: a relay line of node %q is routed by %s: remove the route first",
					domain.ErrValidation, n.DisplayName, frontRouteText(f, route))
			}
		}
	}
	if !behind {
		return nil, nil
	}
	return s.leaveChange(ctx, n, true)
}

// checkRelaysAgainstFronts refuses a relay-line edit that would leave a route
// of a front without its line, or with a line that no longer enters where the
// front is.
func (s *Service) checkRelaysAgainstFronts(ctx context.Context, landing *domain.Node) error {
	if s.fronts == nil || landing == nil || landing.ID == 0 {
		return nil
	}
	fronts, err := s.liveFronts(ctx)
	if err != nil {
		return err
	}
	for _, f := range fronts {
		for _, route := range f.Routes {
			if route.Target.Kind != domain.FrontTargetForward || route.Target.NodeID != landing.ID {
				continue
			}
			ok := false
			for _, line := range landing.Relays {
				if line.ID == route.Target.LineID && line.Managed() && line.EntryPanelID == f.PanelID && line.Port == f.Port {
					ok = true
				}
			}
			if !ok {
				return fmt.Errorf("%w: a relay line of node %q is routed by %s on its entry server: remove the route before moving, retargeting or deleting the line",
					domain.ErrValidation, landing.DisplayName, frontRouteText(f, route))
			}
		}
	}
	return nil
}

// frontedBinds are the (server, port) pairs that are a shared port: a relay
// line may enter there, next to the listeners behind the front and next to
// other lines, because the front routes them apart by name.
func (s *Service) frontedBinds(ctx context.Context) (map[bindKey]bool, map[bindKey]string, error) {
	shared := map[bindKey]bool{}
	loopback := map[bindKey]string{}
	fronts, err := s.liveFronts(ctx)
	if err != nil {
		return nil, nil, err
	}
	for _, f := range fronts {
		shared[bindKey{f.PanelID, f.Port}] = true
		for _, item := range f.Listeners {
			loopback[bindKey{f.PanelID, item.LoopbackPort}] = fmt.Sprintf("loopback port of a listener behind the shared port %d", f.Port)
		}
	}
	return shared, loopback, nil
}

type bindKey struct {
	panel int64
	port  int
}

// relayPanels are the servers a node's save touches fronts of: its own and the
// entry server of each relay line.
func relayPanels(n *domain.Node) []int64 {
	out := []int64{n.PanelID}
	for _, line := range n.Relays {
		out = append(out, line.EntryPanelID)
	}
	return out
}
