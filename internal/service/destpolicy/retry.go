package destpolicy

import (
	"context"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

// RetryDestinationPolicy is invoked under nodesync's per-agent sync lock.
// Notifications follow persistence; a failed transaction invalidates nothing.
func (c *Compiler) RetryDestinationPolicy(ctx context.Context, agentID string) (bool, error) {
	if c == nil {
		return false, domain.ErrUnavailable
	}
	repo, ok := c.runtime.(ports.DestPolicyRetryRepo)
	if !ok {
		return false, domain.ErrUnavailable
	}
	changed, err := repo.RetryDestinationPolicy(ctx, agentID, c.now().UTC())
	if err != nil || !changed {
		return changed, err
	}
	c.preparedCache.Clear()
	c.selectionCache.Clear()
	if c.invalidate != nil {
		c.invalidate(agentID)
	}
	if c.allowlistResync != nil {
		c.allowlistResync(ctx, agentID)
	}
	return true, nil
}
