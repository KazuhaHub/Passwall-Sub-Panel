package nodemetrics

import (
	"context"
	"fmt"
	"time"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/pkg/metrics"
)

// Maintenance: the hourly rollup and the retention prune.
//
// ROLLUP ALWAYS RUNS BEFORE PRUNE, and they are one call for that reason. The
// reverse order would delete raw rows before they had been aggregated, which
// loses the hour permanently — and it would happen on the first tick after every
// boot, which is when it is least likely to be noticed.

// rollupBatchBuckets bounds how many hours one pass computes per agent.
//
// A panel that has been down for a week would otherwise try to aggregate every
// missing hour in one tick, holding a long read and a long write. Catching up
// over several ticks is invisible; a stalled maintenance loop is not.
const rollupBatchBuckets = 48

// RollupAndPrune runs one maintenance pass.
//
// It returns an error only for a repository failure that makes the pass
// impossible. An agent whose buckets could not be computed is logged and skipped:
// the maintenance loop runs on a timer, and one node's bad hour must not stop
// every other node's aggregation.
func (s *Service) RollupAndPrune(ctx context.Context, now time.Time) error {
	now = now.UTC()
	// Only COMPLETE buckets are aggregated. A bucket still accumulating would be
	// computed from a partial hour, and re-computing it on every tick would
	// overwrite it with a different partial answer each time.
	completeBefore := RollupBucketStart(now.Add(-NodeMetricRollupSafetyLag))

	agents, err := s.repo.AgentIDs(ctx)
	if err != nil {
		return err
	}
	for _, agentID := range agents {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err := s.rollupAgent(ctx, agentID, completeBefore); err != nil {
			// Counted rather than propagated: one agent's history must not stop
			// the pass that keeps every other agent's history bounded.
			metrics.NodeHostRollupTotal.With(metrics.NodeHostRollupError).Inc()
		}
	}
	return s.prune(ctx, now)
}

func (s *Service) rollupAgent(ctx context.Context, agentID string, completeBefore time.Time) error {
	// The rollup resumes from whatever has already been aggregated, so a restart
	// or a long outage catches up without recomputing history. The window read is
	// the retention-bounded one rather than "everything ever": an hour already
	// rolled up long ago does not need to be found again.
	existing, err := s.repo.HourlyRange(ctx, agentID, completeBefore.Add(-NodeMetricRawRetention), completeBefore)
	if err != nil {
		return err
	}
	start := completeBefore.Add(-NodeMetricRawRetention)
	if len(existing) > 0 {
		start = existing[len(existing)-1].BucketStart.Add(time.Hour)
	}

	written := 0
	for bucket := start; bucket.Before(completeBefore); bucket = bucket.Add(time.Hour) {
		if written >= rollupBatchBuckets {
			// The rest of the backlog waits for the next tick; nothing is lost.
			break
		}
		row, empty, err := s.rollupBucket(ctx, agentID, bucket)
		if err != nil {
			return err
		}
		if empty {
			// An hour with no samples is not written: an empty hourly row would
			// draw as a covered hour with no data, where a missing one draws as
			// what it is.
			continue
		}
		if err := s.repo.UpsertHourly(ctx, []domain.NodeHostMetricHourly{row}); err != nil {
			return err
		}
		written++
	}
	if written == 0 {
		metrics.NodeHostRollupTotal.With(metrics.NodeHostRollupEmpty).Inc()
	} else {
		metrics.NodeHostRollupTotal.With(metrics.NodeHostRollupWritten).Inc()
	}
	return nil
}

// rollupBucket computes one hour, including the sample before it.
func (s *Service) rollupBucket(ctx context.Context, agentID string, bucket time.Time) (domain.NodeHostMetricHourly, bool, error) {
	bucketEnd := bucket.Add(time.Hour)
	// includePredecessor is what makes the FIRST interval of the hour
	// computable. Without it the earliest part of every hour would be a gap for
	// no reason but the window's own edge, and a full hour would report less
	// coverage than it had.
	samples, err := s.repo.RawRange(ctx, agentID, bucket, bucketEnd, true)
	if err != nil {
		return domain.NodeHostMetricHourly{}, false, err
	}
	if len(samples) == 0 {
		return domain.NodeHostMetricHourly{}, true, nil
	}
	// The range read returns the predecessor first when there is one; it is a
	// baseline and not a data point, so it is separated here rather than counted.
	var predecessor *domain.NodeHostMetricSample
	if samples[0].ReceivedAt.Before(bucket) {
		predecessor = &samples[0]
		samples = samples[1:]
	}
	if len(samples) == 0 {
		return domain.NodeHostMetricHourly{}, true, nil
	}
	sampleIDs := make(map[string]struct{}, len(samples))
	for index := range samples {
		sampleIDs[samples[index].SampleID] = struct{}{}
	}
	interfaces, err := s.interfacesFor(ctx, agentID, sampleIDs, bucket, bucketEnd)
	if err != nil {
		return domain.NodeHostMetricHourly{}, false, err
	}
	row := RollupHour(agentID, bucket, predecessor, samples, interfaces)
	return row, false, nil
}

// interfacesFor reads the interface rows belonging to a set of samples.
//
// It queries per interface name rather than once per sample: the range read is
// keyed by (agent, name, received_at), so one read per name is the shape the
// index supports, and a name is a bounded set the host's own layout decides.
func (s *Service) interfacesFor(ctx context.Context, agentID string, sampleIDs map[string]struct{}, from, to time.Time) ([]domain.NodeInterfaceMetricSample, error) {
	rows, err := s.repo.InterfaceNames(ctx, agentID)
	if err != nil {
		return nil, err
	}
	var collected []domain.NodeInterfaceMetricSample
	for _, name := range rows {
		samples, err := s.repo.InterfaceRange(ctx, agentID, name, from, to)
		if err != nil {
			return nil, err
		}
		for index := range samples {
			if _, wanted := sampleIDs[samples[index].SampleID]; wanted {
				collected = append(collected, samples[index])
			}
		}
	}
	return collected, nil
}

// prune applies retention, in bounded batches.
func (s *Service) prune(ctx context.Context, now time.Time) error {
	// The cutoffs are floored to a whole UTC hour, which is a silent-corruption
	// guard rather than tidiness: a partial hour deleted between the rollup and
	// the next pass would be aggregated from the rows that survived, and the
	// bucket would silently shrink.
	rawCutoff := now.Add(-NodeMetricRawRetention).UTC().Truncate(time.Hour)
	hourlyCutoff := now.Add(-NodeMetricHourlyRetention).UTC().Truncate(time.Hour)
	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		result, err := s.repo.Prune(ctx, domain.NodeHostPruneRequest{
			RawBefore: rawCutoff, HourlyBefore: hourlyCutoff, Limit: nodeHostPruneBatch,
		})
		if err != nil {
			return fmt.Errorf("prune node host metrics: %w", err)
		}
		metrics.NodeHostPrunedRowsTotal.With(metrics.NodeHostPruneRawTable).Add(result.RawDeleted)
		metrics.NodeHostPrunedRowsTotal.With(metrics.NodeHostPruneInterfaceTable).Add(result.InterfaceDeleted)
		metrics.NodeHostPrunedRowsTotal.With(metrics.NodeHostPruneHourlyTable).Add(result.HourlyDeleted)
		// The loop stops on a short batch, which is what makes a large purge take
		// several bounded transactions instead of one long one.
		if result.RawDeleted < int64(nodeHostPruneBatch) && result.InterfaceDeleted < int64(nodeHostPruneBatch) &&
			result.HourlyDeleted < int64(nodeHostPruneBatch) {
			return nil
		}
	}
}

// nodeHostPruneBatch bounds one retention transaction. It matches the
// repository's own default so the loop and the repo agree about what "short"
// means.
const nodeHostPruneBatch = 5000
