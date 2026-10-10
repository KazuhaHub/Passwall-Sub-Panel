package nodesync

import (
	"context"
	"strings"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
)

func (s *Service) RetryDestinationPolicy(ctx context.Context, agentID string) (bool, error) {
	if s == nil {
		return false, domain.ErrUnavailable
	}
	if agentID == "" || len(agentID) > 64 || strings.TrimSpace(agentID) != agentID {
		return false, domain.ErrValidation
	}
	retry, ok := s.policies.(interface {
		RetryDestinationPolicy(context.Context, string) (bool, error)
	})
	if !ok {
		return false, domain.ErrUnavailable
	}
	if err := ctx.Err(); err != nil {
		return false, err
	}
	// Retry cannot race observe -> compile -> mint and be overwritten by a
	// fallback candidate selected before the reset. Other agents stay concurrent.
	unlock := s.agentLocks.Lock(agentID)
	defer unlock()
	if err := ctx.Err(); err != nil {
		return false, err
	}
	changed, err := retry.RetryDestinationPolicy(ctx, agentID)
	if err == nil && changed {
		s.policyConfigCache.Clear()
	}
	return changed, err
}
