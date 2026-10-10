package sqlstore

import (
	"context"
	"errors"
	"math"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func TestDestAuditPanelStatsStoredUnitsWindowAndRequestedPanels(t *testing.T) {
	r, p, u, _, now := auditCleanupFixture(t)
	until := now.Truncate(time.Hour)
	since := until.Add(-24 * time.Hour)
	rows := []destHitRow{
		cleanupHit(p, u, since.Add(-time.Hour).UnixMilli(), "p12", "block", "too-old.test", 443),
		cleanupHit(p, u, since.UnixMilli(), "p12", "block", "at-start.test", 443),
		cleanupHit(p, u, until.Add(-time.Hour).UnixMilli(), "p12", "observe", "before-end.test", 443),
		cleanupHit(p, 0, until.Add(-time.Hour).UnixMilli(), "g1", "observe", "trial.test", 0),
		cleanupHit(p, u, until.UnixMilli(), "p12", "block", "at-end.test", 443),
		cleanupHit(p+1, u, since.UnixMilli(), "p12", "block", "other-panel.test", 443),
	}
	for i := range rows {
		rows[i].Count = int64((i + 1) * 10)
	}
	if err := r.db.Create(&rows).Error; err != nil {
		t.Fatal(err)
	}
	losses := []destAuditLossHourlyRow{
		{PanelID: p, ObservedHourMS: since.UnixMilli(), Kind: "block", Reason: "queue_full", Rows: 2},
		{PanelID: p, ObservedHourMS: since.UnixMilli(), Kind: "observe", Reason: "node_dropped", Events: 3},
		{PanelID: p, ObservedHourMS: since.UnixMilli(), Kind: "trial", Reason: "node_unmatched", Unmatched: 5},
		{PanelID: p, ObservedHourMS: since.UnixMilli(), Kind: "usage", Reason: "ingest_error", Rows: 7},
		{PanelID: p, ObservedHourMS: until.UnixMilli(), Kind: "block", Reason: "queue_full", Rows: 999},
		{PanelID: p + 1, ObservedHourMS: since.UnixMilli(), Kind: "block", Reason: "queue_full", Rows: 999},
	}
	if err := r.db.Create(&losses).Error; err != nil {
		t.Fatal(err)
	}
	recorder := &auditTraceRecorder{Interface: logger.Discard}
	r.db = r.db.Session(&gorm.Session{Logger: recorder})
	got, err := r.ReadDestinationAuditPanelStats(t.Context(), since, until, []int64{p, p, p + 2})
	want := map[int64]domain.DestAuditPanelStats{
		p:     {Hits: 90, Losses: domain.DestAuditLosses{Rows: 9, Events: 3, Unmatched: 5, Scope: "panel"}},
		p + 2: {Losses: domain.DestAuditLosses{Scope: "panel"}},
	}
	if err != nil || !reflect.DeepEqual(got, want) || len(recorder.queries) != 0 {
		t.Fatalf("stored panel totals %v error %v", got, err)
	}
	// The storage granularity is an hour, including buckets that overlap a
	// partial requested hour. The exact upper hour remains excluded.
	got, err = r.ReadDestinationAuditPanelStats(t.Context(), since.Add(37*time.Minute), until, []int64{p})
	if err != nil || got[p].Hits != 90 {
		t.Fatal("partial starting hour discarded available observations")
	}
}

func TestDestAuditPanelStatsReadsOneSnapshotWhileIngestionCommits(t *testing.T) {
	r, p, u, _, now := auditCleanupFixture(t)
	if r.db.Dialector.Name() == "sqlite" {
		t.Skip("concurrent server snapshot acceptance runs on real MySQL and PostgreSQL")
	}
	pool, err := r.db.DB()
	if err != nil {
		t.Fatal(err)
	}
	oldMax := pool.Stats().MaxOpenConnections
	pool.SetMaxOpenConns(2)
	t.Cleanup(func() { pool.SetMaxOpenConns(oldMax) })
	hour := now.Truncate(time.Hour)
	if err := r.db.Create(&[]destHitRow{cleanupHit(p, u, hour.UnixMilli(), "p12", "block", "snapshot.test", 443)}).Error; err != nil {
		t.Fatal(err)
	}
	if err := r.db.Create(&destAuditLossHourlyRow{PanelID: p, ObservedHourMS: hour.UnixMilli(), Kind: "block", Reason: "queue_full", Rows: 1}).Error; err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	entered, release := make(chan struct{}), make(chan struct{})
	var enterOnce, releaseOnce sync.Once
	finish := func() { releaseOnce.Do(func() { close(release) }) }
	defer finish()
	if err := r.db.Callback().Row().Before("gorm:row").Register("audit_stats_snapshot_barrier", func(tx *gorm.DB) {
		if tx.Statement.Table == "dest_audit_loss_hourly" {
			enterOnce.Do(func() { close(entered) })
			select {
			case <-release:
			case <-ctx.Done():
				tx.AddError(ctx.Err())
			}
		}
	}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = r.db.Callback().Row().Remove("audit_stats_snapshot_barrier") })
	type result struct {
		stats map[int64]domain.DestAuditPanelStats
		err   error
	}
	done := make(chan result, 1)
	go func() {
		stats, err := r.ReadDestinationAuditPanelStats(ctx, hour, hour.Add(time.Hour), []int64{p})
		done <- result{stats, err}
	}()
	select {
	case <-entered:
	case <-ctx.Done():
		t.Fatal("stats did not reach the second table")
	}
	if err := r.privateDB(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&destHitRow{}).Where("panel_id = ?", p).Update("count", 99).Error; err != nil {
			return err
		}
		return tx.Model(&destAuditLossHourlyRow{}).Where("panel_id = ?", p).Update("rows", 99).Error
	}); err != nil {
		t.Fatal(err)
	}
	finish()
	select {
	case got := <-done:
		if got.err != nil || got.stats[p].Hits != 1 || got.stats[p].Losses.Rows != 1 {
			t.Fatal("stats combined counters from different committed snapshots")
		}
	case <-ctx.Done():
		t.Fatal("stats did not finish")
	}
	got, err := r.ReadDestinationAuditPanelStats(ctx, hour, hour.Add(time.Hour), []int64{p})
	if err != nil || got[p].Hits != 99 || got[p].Losses.Rows != 99 {
		t.Fatal("a later read did not see the new committed counters")
	}
}

func TestDestAuditPanelStatsRejectsCorruptNegativeCounters(t *testing.T) {
	for _, kind := range []string{"hits", "loss"} {
		t.Run(kind, func(t *testing.T) {
			r, p, u, _, now := auditCleanupFixture(t)
			hour := now.Truncate(time.Hour)
			if kind == "hits" {
				row := cleanupHit(p, u, hour.UnixMilli(), "p12", "block", "negative.test", 443)
				row.Count = -1
				if err := r.db.Create(&row).Error; err != nil {
					t.Fatal(err)
				}
			} else {
				if err := r.db.Create(&destAuditLossHourlyRow{PanelID: p, ObservedHourMS: hour.UnixMilli(), Kind: "usage", Reason: "queue_full", Unmatched: -1}).Error; err != nil {
					t.Fatal(err)
				}
			}
			if got, err := r.ReadDestinationAuditPanelStats(t.Context(), hour, now, []int64{p}); got != nil || !errors.Is(err, domain.ErrUnavailable) {
				t.Fatal("corrupt counters became reassuring totals")
			}
		})
	}
}

func TestDestAuditPanelStatsSaturatesCountsAndBoundsINStatements(t *testing.T) {
	r, p, u, _, now := auditCleanupFixture(t)
	hour := now.Truncate(time.Hour)
	hits := []destHitRow{cleanupHit(p, u, hour.UnixMilli(), "p12", "block", "one.test", 443), cleanupHit(p, u, hour.UnixMilli(), "p12", "block", "two.test", 443)}
	for i := range hits {
		hits[i].Count = math.MaxInt64
	}
	if err := r.db.Create(&hits).Error; err != nil {
		t.Fatal(err)
	}
	for _, reason := range []string{"queue_full", "ingest_error"} {
		if err := r.db.Create(&destAuditLossHourlyRow{PanelID: p, ObservedHourMS: hour.UnixMilli(), Kind: "block", Reason: reason, Rows: math.MaxInt64, Events: math.MaxInt64, Unmatched: math.MaxInt64}).Error; err != nil {
			t.Fatal(err)
		}
	}
	ids := make([]int64, 513)
	for i := range ids {
		ids[i] = p + int64(i)
	}
	queries := 0
	if err := r.db.Callback().Row().After("gorm:row").Register("audit_stats_bounded_in", func(tx *gorm.DB) {
		if tx.Statement.Table == "dest_hits" || tx.Statement.Table == "dest_audit_loss_hourly" {
			queries++
			if len(tx.Statement.Vars) > 518 {
				t.Error("panel query exceeded 512 IDs")
			}
		}
	}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = r.db.Callback().Row().Remove("audit_stats_bounded_in") })
	got, err := r.ReadDestinationAuditPanelStats(t.Context(), hour, hour.Add(time.Hour), ids)
	want := domain.DestAuditPanelStats{Hits: math.MaxInt64, Losses: domain.DestAuditLosses{Rows: math.MaxInt64, Events: math.MaxInt64, Unmatched: math.MaxInt64, Scope: "panel"}}
	if err != nil || len(got) != 513 || got[p] != want || queries != 4 {
		t.Fatal("aggregated counters overflowed or panel chunk lost data")
	}
}

func TestDestAuditPanelStatsValidationFailureAndPrivateError(t *testing.T) {
	r, p, _, _, now := auditCleanupFixture(t)
	since := now.Truncate(time.Hour)
	for _, tc := range []struct {
		since, until time.Time
		ids          []int64
	}{
		{time.Time{}, now, []int64{p}}, {since, since, []int64{p}},
		{since, since.Add(-time.Hour), []int64{p}}, {since, since.Add(31*24*time.Hour + time.Millisecond), []int64{p}},
		{since, now, []int64{0}}, {since, now, []int64{-1}},
	} {
		if got, err := r.ReadDestinationAuditPanelStats(t.Context(), tc.since, tc.until, tc.ids); !errors.Is(err, domain.ErrValidation) || got != nil {
			t.Fatal("invalid scope reached storage")
		}
	}
	if got, err := r.ReadDestinationAuditPanelStats(t.Context(), since, now, nil); err != nil || len(got) != 0 || got == nil {
		t.Fatal("empty scope did not return an empty result")
	}
	recorder := &auditTraceRecorder{Interface: logger.Discard}
	r.db = r.db.Session(&gorm.Session{Logger: recorder})
	if err := r.db.Callback().Row().Before("gorm:row").Register("audit_stats_private_error", func(tx *gorm.DB) {
		if tx.Statement.Table == "dest_audit_loss_hourly" {
			tx.AddError(errors.New("private destination and account values"))
		}
	}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = r.db.Callback().Row().Remove("audit_stats_private_error") })
	if got, err := r.ReadDestinationAuditPanelStats(t.Context(), since, now, []int64{p}); got != nil || err != errAuditStorage || len(recorder.queries) != 0 {
		t.Fatal("failed read returned partial counters or leaked driver details")
	}
	_ = r.db.Callback().Row().Remove("audit_stats_private_error")
	ctx := t.Context()
	if got, err := r.ReadDestinationAuditPanelStats(ctx, since, since.Add(31*24*time.Hour), []int64{p}); err != nil || len(got) != 1 {
		t.Fatal("maximum window or retry rejected")
	}
}
