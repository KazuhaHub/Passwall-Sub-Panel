package app

import (
	"context"
	"time"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/pkg/log"
	"github.com/KazuhaHub/passwall-sub-panel/internal/pkg/metrics"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

func (a *App) pruneDestAudit(ctx context.Context) {
	if a.destAuditMaintenance == nil {
		return
	}
	var removed domain.DestAuditPruned
	err := a.operationGate.RunRead(ctx, func(ctx context.Context) error {
		settings := domain.DestinationSettings{}
		retention := &settings
		if a.settings != nil {
			loaded, err := a.settings.Load(ctx, ports.UISettings{})
			if err != nil {
				if ctx.Err() == nil {
					log.Warn("destination audit cleanup settings unavailable; configurable retention skipped")
				}
				retention = nil
			} else {
				settings = loaded.DestinationSettings()
			}
		}
		var err error
		removed, err = a.destAuditMaintenance.PruneDestinationAudit(ctx, time.Now().UTC(), retention)
		return err
	})
	if err != nil {
		if ctx.Err() == nil {
			log.Warn("destination audit cleanup failed")
		}
		return
	}
	for _, entry := range []struct {
		kind string
		rows int64
	}{
		{metrics.DestPruneHits, removed.Hits}, {metrics.DestPruneTrial, removed.Trial},
		{metrics.DestPruneUsage, removed.Usage}, {metrics.DestPruneLoss, removed.Loss},
		{metrics.DestPruneBatches, removed.Batches}, {metrics.DestPruneBudget, removed.Budget},
		{metrics.DestPruneOrphans, removed.Orphans},
	} {
		if entry.rows > 0 {
			metrics.DestPrunedRowsTotal.With(entry.kind).AddSaturating(uint64(entry.rows))
			log.Info("destination audit rows pruned", "kind", entry.kind, "rows", entry.rows)
		}
	}
}

// Expiry is a definition write, so it shares backend admission with other
// destination writers. Publication observes the committed generation normally.
func (a *App) pruneDestExemptions(ctx context.Context) {
	if a.destDefinitions == nil {
		return
	}
	var removed int64
	err := a.operationGate.RunRead(ctx, func(ctx context.Context) error {
		var err error
		removed, err = a.destDefinitions.PruneExpiredExemptions(ctx, time.Now().UTC())
		return err
	})
	if err != nil {
		if ctx.Err() == nil {
			// Driver errors may contain stored values. Keep cleanup diagnostics bounded.
			log.Warn("destination exemption cleanup failed")
		}
		return
	}
	if removed > 0 {
		metrics.DestPrunedRowsTotal.With(metrics.DestPruneExemptions).Add(removed)
		log.Info("destination exemptions pruned", "rows", removed)
	}
}

func (a *App) pruneDestOrphanExemptions(ctx context.Context) {
	if a.destDefinitions == nil {
		return
	}
	var removed int64
	err := a.operationGate.RunRead(ctx, func(ctx context.Context) error {
		var err error
		removed, err = a.destDefinitions.PruneOrphanedExemptions(ctx, time.Now().UTC())
		return err
	})
	if err != nil {
		if ctx.Err() == nil {
			log.Warn("destination exemption orphan cleanup failed")
		}
		return
	}
	if removed > 0 {
		metrics.DestPrunedRowsTotal.With(metrics.DestPruneOrphans).AddSaturating(uint64(removed))
		log.Info("destination exemption orphans pruned", "rows", removed)
	}
}
