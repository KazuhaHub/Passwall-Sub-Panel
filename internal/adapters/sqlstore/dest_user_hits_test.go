package sqlstore

import (
	"context"
	"errors"
	"fmt"
	"math"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func TestDestinationUserHitsOneSnapshotAcrossClientsCountsNamesAndLosses(t *testing.T) {
	r, q, p, u, policy := hitReadFixture(t)
	if r.db.Dialector.Name() == "sqlite" {
		t.Skip("concurrent account snapshot acceptance runs on real MySQL and PostgreSQL")
	}
	pool, err := r.db.DB()
	if err != nil {
		t.Fatal(err)
	}
	oldMax := pool.Stats().MaxOpenConnections
	pool.SetMaxOpenConns(2)
	t.Cleanup(func() { pool.SetMaxOpenConns(oldMax) })
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	entered, release := make(chan struct{}), make(chan struct{})
	var enterOnce, releaseOnce sync.Once
	finish := func() { releaseOnce.Do(func() { close(release) }) }
	defer finish()
	if err := r.db.Callback().Row().Before("gorm:row").Register("user_hit_snapshot_barrier", func(tx *gorm.DB) {
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
	t.Cleanup(func() { _ = r.db.Callback().Row().Remove("user_hit_snapshot_barrier") })
	type result struct {
		hits domain.DestRecentHits
		err  error
	}
	done := make(chan result, 1)
	go func() { hits, err := r.ReadDestinationUserHits(ctx, u, q.Since, q.Until); done <- result{hits, err} }()
	select {
	case <-entered:
	case <-ctx.Done():
		t.Fatal("account read did not reach losses")
	}
	if err := r.privateDB(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(&pspClientRow{UserID: u, PanelID: p + 20, Email: "new-client@example.test"}).Error; err != nil {
			return err
		}
		if err := tx.Model(&destHitRow{}).Where("user_id = ? AND source = ? AND action = ?", u, fmt.Sprintf("p%d", policy), "block").Update("count", 99).Error; err != nil {
			return err
		}
		if err := tx.Model(&destPolicyRow{}).Where("id = ?", policy).Update("name", "New account policy").Error; err != nil {
			return err
		}
		return tx.Create(&destAuditLossHourlyRow{PanelID: p + 20, ObservedHourMS: q.Since.UnixMilli(), Kind: "block", Reason: "queue_full", Rows: 17}).Error
	}); err != nil {
		t.Fatal(err)
	}
	finish()
	select {
	case got := <-done:
		if got.err != nil || len(got.hits.ClientPanelIDs) != 0 || got.hits.Items[0].Count != 7 || *got.hits.Items[0].SourceName != "Read policy" || got.hits.Losses.Rows != 13 {
			t.Fatal("account read combined snapshots")
		}
	case <-ctx.Done():
		t.Fatal("account snapshot did not finish")
	}
	got, err := r.ReadDestinationUserHits(ctx, u, q.Since, q.Until)
	if err != nil || !reflect.DeepEqual(got.ClientPanelIDs, []int64{p + 20}) || got.Items[0].Count != 198 || *got.Items[0].SourceName != "New account policy" || got.Losses.Rows != 30 {
		t.Fatal("later account read did not see committed client/count/name/loss changes")
	}
}

func TestDestinationUserHitsSeparateFrozenActionsAndPreserveHistory(t *testing.T) {
	r, q, p, u, policy := hitReadFixture(t)
	if err := r.db.Create(&pspClientRow{UserID: u, PanelID: p + 20, Email: "current-read@example.test"}).Error; err != nil {
		t.Fatal(err)
	}
	if err := r.db.Model(&destPolicyRow{}).Where("id = ?", policy).Updates(map[string]any{"name": "New name", "action": "observe"}).Error; err != nil {
		t.Fatal(err)
	}
	got, err := r.ReadDestinationUserHits(t.Context(), u, q.Since, q.Until)
	if err != nil || len(got.Items) != 3 || !reflect.DeepEqual(got.ClientPanelIDs, []int64{p + 20}) {
		t.Fatalf("user hit summary unavailable or wrong scope: %v", err)
	}
	if got.Items[0].Source != fmt.Sprintf("p%d", policy) || got.Items[0].Action != "block" || got.Items[0].Count != 7 || got.Items[0].SourceName == nil || *got.Items[0].SourceName != "New name" || len(got.Items[0].Panels) != 2 || got.Items[0].Panels[0].PanelID != p || got.Items[0].Panels[1].PanelID != p+1 || got.Items[0].Panels[1].Name != nil {
		t.Fatal("historical actions, labels, counts or panels changed")
	}
	if len(got.Items[0].TopDests) != 2 || got.Items[0].TopDests[0].Dest != "literal100%_match!.test" || got.Items[0].TopDests[0].Port != 80 || got.Items[0].TopDests[0].Count != 6 {
		t.Fatal("top destinations omitted their port/count")
	}
	if got.Losses != (domain.DestAuditLosses{Rows: 13, Events: 3, Unmatched: 4, Scope: "panel"}) {
		t.Fatal("losses included usage/trial/unrelated nodes or changed units")
	}
	if err := r.db.Delete(&destPolicyRow{}, policy).Error; err != nil {
		t.Fatal(err)
	}
	got, err = r.ReadDestinationUserHits(t.Context(), u, q.Since, q.Until)
	if err != nil || len(got.Items) != 3 || got.Items[0].SourceName != nil || got.Items[0].Count != 7 {
		t.Fatal("deleted definition hid or reclassified history")
	}
	got, err = r.ReadDestinationUserHits(t.Context(), 888888, q.Since, q.Until)
	if err != nil || got.Items == nil || len(got.Items) != 0 || len(got.ClientPanelIDs) != 0 || got.Losses.Scope != "panel" || got.Losses.Complete {
		t.Fatal("empty history was unavailable or complete")
	}
}

func TestDestinationUserHitsTopThreeAreAggregatedStableAndSaturating(t *testing.T) {
	r, p, u, _, now := auditCleanupFixture(t)
	hour := now.Truncate(time.Hour)
	var rows []destHitRow
	for i, dest := range []string{"a.test", "b.test", "c.test", "d.test"} {
		row := cleanupHit(p, u, hour.UnixMilli(), "p987", "block", dest, 443)
		row.Count = int64(5 - i)
		rows = append(rows, row)
	}
	repeated := cleanupHit(p, u, hour.Add(-time.Hour).UnixMilli(), "p987", "block", "d.test", 443)
	repeated.Count = 9
	rows = append(rows, repeated)
	saturated := cleanupHit(p, u, hour.UnixMilli(), "p988", "block", "huge.test", 80)
	saturated.Count = math.MaxInt64
	rows = append(rows, saturated)
	saturated.HourMS = hour.Add(-time.Hour).UnixMilli()
	rows = append(rows, saturated)
	trial := cleanupHit(p, u, hour.UnixMilli(), "g12", "observe", "private-trial.test", 0)
	rows = append(rows, trial)
	if err := r.db.Create(&rows).Error; err != nil {
		t.Fatal(err)
	}
	got, err := r.ReadDestinationUserHits(t.Context(), u, hour.Add(-2*time.Hour), hour.Add(time.Hour))
	if err != nil || len(got.Items) != 2 || got.Items[0].Count != math.MaxInt64 || got.Items[0].TopDests[0].Count != math.MaxInt64 {
		t.Fatal("saturation overflow or trial escaped group scope")
	}
	want := []domain.DestUserHitDestination{{Dest: "d.test", Port: 443, Count: 11}, {Dest: "a.test", Port: 443, Count: 5}, {Dest: "b.test", Port: 443, Count: 4}}
	if !reflect.DeepEqual(got.Items[1].TopDests, want) || got.Items[1].Count != 23 {
		t.Fatalf("top3 must aggregate across hours before limiting: %v", got.Items[1].TopDests)
	}
	again, err := r.ReadDestinationUserHits(t.Context(), u, hour.Add(-2*time.Hour), hour.Add(time.Hour))
	if err != nil || !reflect.DeepEqual(got, again) {
		t.Fatal("summary ordering changed between reads")
	}
}

func TestDestinationUserHitsRejectsInvalidOrFailedReadsWithoutPrivateTrace(t *testing.T) {
	r, q, _, u, _ := hitReadFixture(t)
	for _, tc := range []struct {
		id           int64
		since, until time.Time
	}{{0, q.Since, q.Until}, {-1, q.Since, q.Until}, {u, q.Until, q.Since}, {u, q.Since, q.Since.Add(32 * 24 * time.Hour)}} {
		if _, err := r.ReadDestinationUserHits(t.Context(), tc.id, tc.since, tc.until); !errors.Is(err, domain.ErrValidation) {
			t.Fatal("invalid summary query accepted")
		}
	}
	recorder := &auditTraceRecorder{Interface: logger.Discard}
	r.db = r.db.Session(&gorm.Session{Logger: recorder})
	problem := errors.New("private account destination query secret")
	if err := r.db.Callback().Row().Before("gorm:row").Register("user-hits-private-failure", func(tx *gorm.DB) {
		if tx.Statement.Table == "dest_audit_loss_hourly" {
			tx.AddError(problem)
		}
	}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = r.db.Callback().Row().Remove("user-hits-private-failure") })
	got, err := r.ReadDestinationUserHits(t.Context(), u, q.Since, q.Until)
	if err != errAuditStorage || strings.Contains(err.Error(), "private") || !reflect.DeepEqual(got, domain.DestRecentHits{}) || len(recorder.queries) != 0 {
		t.Fatalf("failed summary exposed partial history or trace: %v", err)
	}
}
