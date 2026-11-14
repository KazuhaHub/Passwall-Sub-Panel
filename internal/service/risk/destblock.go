package risk

import (
	"context"
	"fmt"
	"time"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/pkg/log"
)

// DestBlockReader is the destination-rule side of the dest_block signal, read
// only: for every account that has something to say, how many of the native
// nodes it has a client on are recording hits, and its BLOCK hits since the
// window start grouped by source, counting only policies an admin marked as
// counting toward risk. Report-only hits are never in it.
//
// One call per run, not per account: the answer is a few grouped queries and the
// run judges thousands of accounts.
type DestBlockReader interface {
	DestBlockInputs(ctx context.Context, sinceMS int64) (map[int64]domain.DestBlockInput, error)
}

// destBlock judges every account of a readable group. The window is the current
// hour and the 23 before it (24 hourly buckets). A read that fails skips the
// signal, the package's rule for a source it cannot read: the previous rows
// stay and the run is partial. An account absent from the answer has no
// recording node, which is unknown/no_collector and never clean.
func (s *Service) destBlock(ctx context.Context, r *refresh) error {
	since := r.now.UTC().Truncate(time.Hour).Add(-(domain.RiskDestBlockWindowHours - 1) * time.Hour).UnixMilli()
	inputs, err := s.d.Dest.DestBlockInputs(ctx, since)
	if err != nil {
		if ctx.Err() != nil {
			return fmt.Errorf("risk refresh: %w", ctx.Err())
		}
		r.partial = true
		log.Warn("risk signals: destination hits unreadable; dest_block keeps its previous rows", "err", err)
		return nil
	}
	for _, u := range r.users {
		policy, ok := r.policies[u.GroupID]
		if !ok {
			continue // unreadable group: previous rows kept
		}
		v, ev := domain.EvaluateDestBlock(domain.DestBlockPolicy{
			Off: policy.risk.DestBlockOff, Threshold: policy.risk.DestBlockThreshold,
		}, inputs[u.ID])
		addVerdict(r, u.ID, domain.RiskKindDestBlock, v, ev)
	}
	return nil
}
