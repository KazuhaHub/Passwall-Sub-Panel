package app

import (
	"context"
	"sync/atomic"
	"time"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/pkg/log"
	"github.com/KazuhaHub/passwall-sub-panel/internal/pkg/operationgate"
	"github.com/KazuhaHub/passwall-sub-panel/internal/pkg/safego"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
	"github.com/KazuhaHub/passwall-sub-panel/internal/service/destaudit"
	"github.com/KazuhaHub/passwall-sub-panel/internal/service/nodesync"
)

type destinationAuditRunner interface {
	nodesync.AuditCollector
	StopOffers()
	Run(context.Context) destaudit.Summary
}

func (a *App) startDestinationAudit() {
	if a.destAudit == nil {
		return
	}
	a.destAuditStart.Do(func() {
		safego.GoTracked(&a.bgWG, "destination-audit-ingest", func() {
			summary := a.destAudit.Run(a.destAuditCtx)
			if summary.Batches > 0 || summary.LossKeys > 0 {
				log.Warn("destination audit shutdown incomplete", "batches", summary.Batches, "rows", summary.Rows, "loss_keys", summary.LossKeys, "loss_rows", summary.LossRows)
			}
		})
	})
}

// Each transaction/query participates in online backend admission. Holding a
// read permit for the lifetime of an idle worker would block every migration.
type admittedDestinationAuditStore struct {
	store ports.DestAuditStore
	gate  *operationgate.Gate
}

func (r admittedDestinationAuditStore) ResolveDestinationAuditUsers(ctx context.Context, ids []int64) (out map[int64]bool, err error) {
	err = r.gate.RunRead(ctx, func(ctx context.Context) error { out, err = r.store.ResolveDestinationAuditUsers(ctx, ids); return err })
	return
}
func (r admittedDestinationAuditStore) BeginDestinationAudit(ctx context.Context, b domain.DestAuditBatch) (out domain.DestAuditBegin, err error) {
	err = r.gate.RunRead(ctx, func(ctx context.Context) error { out, err = r.store.BeginDestinationAudit(ctx, b); return err })
	return
}
func (r admittedDestinationAuditStore) WriteDestinationAuditChunk(ctx context.Context, b domain.DestAuditChunk) (out string, err error) {
	err = r.gate.RunRead(ctx, func(ctx context.Context) error { out, err = r.store.WriteDestinationAuditChunk(ctx, b); return err })
	return
}
func (r admittedDestinationAuditStore) FlushDestinationAuditLoss(ctx context.Context, b domain.DestAuditLossBatch) error {
	return r.gate.RunRead(ctx, func(ctx context.Context) error { return r.store.FlushDestinationAuditLoss(ctx, b) })
}
func (r admittedDestinationAuditStore) WatchDestinationAuditControls(ctx context.Context, fn func([]domain.DestAuditControl)) error {
	return r.gate.RunRead(ctx, func(ctx context.Context) error { return r.store.WatchDestinationAuditControls(ctx, fn) })
}

// At most one count-only storage warning per minute. All individual attempts
// still contribute to diagnostics; error objects and peer values never log.
func destinationAuditObserver() func(destaudit.Event) {
	var next atomic.Int64
	return func(e destaudit.Event) {
		if e.Outcome != "ingest_error" && e.Outcome != "loss_flush_error" {
			return
		}
		now := time.Now().Unix()
		previous := next.Load()
		if now < previous || !next.CompareAndSwap(previous, now+60) {
			return
		}
		log.Warn("destination audit storage incomplete", "agent_id", e.AgentID, "rows", e.Rows, "reason", e.Outcome)
	}
}
