package risk

import (
	"context"
	"fmt"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/pkg/log"
)

// Explicit off needs no telemetry. Enabled signals share one all-account
// read; failed or partial input never overwrites their previous verdicts.
func (s *Service) destinationBlocks(ctx context.Context, r *refresh) error {
	var enabled []*domain.User
	for _, u := range r.users {
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("risk refresh: %w", err)
		}
		p, ok := r.policies[u.GroupID]
		if !ok {
			continue
		}
		if p.risk.DestBlockOff {
			v, ev := domain.EvaluateDestBlock(domain.DestBlockPolicy{Off: true}, domain.DestBlockInput{})
			addVerdict(r, u.ID, domain.RiskKindDestBlock, v, ev)
		} else {
			enabled = append(enabled, u)
		}
	}
	if len(enabled) == 0 {
		return nil
	}
	inputs, err := s.d.Destination.ReadDestinationRisk(ctx, r.now)
	if cerr := ctx.Err(); cerr != nil {
		return fmt.Errorf("risk refresh: %w", cerr)
	}
	if err != nil {
		r.partial = true
		log.Warn("risk signals: destination telemetry unreadable; enabled destination signals keep their previous rows")
		return nil
	}
	for _, u := range enabled {
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("risk refresh: %w", err)
		}
		p := r.policies[u.GroupID].risk
		v, ev := domain.EvaluateDestBlock(domain.DestBlockPolicy{Threshold: p.DestBlockThreshold}, inputs[u.ID])
		addVerdict(r, u.ID, domain.RiskKindDestBlock, v, ev)
	}
	return nil
}
