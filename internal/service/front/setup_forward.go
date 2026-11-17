package front

import (
	"context"
	"fmt"
	"slices"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
)

// SetupForward joins a new managed forward to a shared port, if present.
// During validation the landing may be an unsaved candidate. The node setup
// service holds the entry server lock through validation and apply.
func (s *Service) SetupForward(ctx context.Context, landing *domain.Node, line domain.RelayLine, dry bool) (int64, error) {
	fronts, err := s.fronts.ListByPanel(ctx, line.EntryPanelID)
	if err != nil {
		return 0, err
	}
	for _, f := range fronts {
		if f.Port != line.Port {
			continue
		}
		nodeID := landing.ID
		if nodeID == 0 {
			nodeID = pendingNodeID
		}
		transport := domain.LandingOfRelay(landing)
		if !transport.OK {
			return 0, fmt.Errorf("%w: shared port %d on server %d needs a landing with TCP TLS or REALITY and an SNI (%s)",
				domain.ErrValidation, f.Port, f.PanelID, transport.Reason)
		}
		routes := append(slices.Clone(f.Routes), domain.FrontRoute{
			ServerNames: transport.ServerNames,
			Target:      domain.FrontTarget{Kind: domain.FrontTargetForward, NodeID: nodeID, LineID: line.ID},
		})
		_, err = s.save(ctx, f.PanelID, &f, Input{Port: f.Port, Default: f.Default, Listeners: f.Listeners, Routes: routes, ConfirmMoveListeners: true},
			saveOpts{dry: dry, candidate: landing, keepDefault: true})
		return f.ID, err
	}
	return 0, nil
}

// UndoSetupForward removes only the route newly installed by a failed node setup.
func (s *Service) UndoSetupForward(ctx context.Context, id, nodeID int64, lineID string) error {
	f, err := s.fronts.Get(ctx, id)
	if err != nil {
		return err
	}
	routes := slices.DeleteFunc(slices.Clone(f.Routes), func(r domain.FrontRoute) bool {
		return r.Target.Kind == domain.FrontTargetForward && r.Target.NodeID == nodeID && r.Target.LineID == lineID
	})
	if len(routes) == len(f.Routes) {
		return nil
	}
	_, err = s.save(ctx, f.PanelID, &f, Input{Port: f.Port, Default: f.Default, Listeners: f.Listeners, Routes: routes, ConfirmMoveListeners: true}, saveOpts{keepDefault: true})
	return err
}
