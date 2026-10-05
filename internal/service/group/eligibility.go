package group

import (
	"context"
	"time"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

func (s *Service) Eligible(ctx context.Context, node *domain.Node, g *domain.Group) (bool, error) {
	if g == nil || node == nil {
		return false, domain.ErrValidation
	}
	if err := ctx.Err(); err != nil {
		return false, err
	}
	if !Matches(node, g.TagFilter) {
		return false, nil
	}
	// The unconfigured legacy path cannot compile destination policies either.
	if s == nil || s.destinationEligibility == nil {
		return true, nil
	}
	if g.ID <= 0 {
		return false, domain.ErrValidation
	}
	now := s.now
	if now == nil {
		now = time.Now
	}
	mode, err := s.modeEligibilityCache.read(ctx, g.ID, now, func() (string, error) {
		mode, err := s.destinationEligibility.GroupEligibilityMode(ctx, g.ID)
		if err == nil && mode != "open" && mode != "allowlist" {
			return "", domain.ErrUnavailable
		}
		return mode, err
	})
	if err != nil {
		return false, err
	}
	switch mode {
	case "open":
		return true, nil
	case "allowlist":
		if node.PanelID <= 0 {
			return false, domain.ErrValidation
		}
		facts, err := s.panelEligibilityCache.read(ctx, node.PanelID, now, func() (ports.DestinationPanelEligibility, error) {
			return s.destinationEligibility.PanelDestinationEligibility(ctx, node.PanelID)
		})
		if err != nil {
			return false, err
		}
		return facts.Native && facts.PolicyCapable && !facts.FallbackBlocked, nil
	default:
		return false, domain.ErrUnavailable
	}
}

// InvalidateEligibility must precede enqueueing member resync. Nonpositive IDs
// invalidate all mode/panel facts, as required for mode or stage transitions.
func (s *Service) InvalidateEligibility(panelID int64) {
	if s == nil {
		return
	}
	if panelID > 0 {
		s.panelEligibilityCache.invalidate(panelID)
		return
	}
	s.modeEligibilityCache.invalidate(0)
	s.panelEligibilityCache.invalidate(0)
}
