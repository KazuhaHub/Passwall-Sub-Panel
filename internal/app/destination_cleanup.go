package app

import (
	"context"
	"time"

	"github.com/KazuhaHub/passwall-sub-panel/internal/pkg/log"
	"github.com/KazuhaHub/passwall-sub-panel/internal/pkg/metrics"
)

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
