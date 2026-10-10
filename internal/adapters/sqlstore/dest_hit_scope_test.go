package sqlstore

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func TestDestinationHitReadsLossesUseCurrentAndHistoricalRelatedPanels(t *testing.T) {
	r, q, p, u, policy := hitReadFixture(t)
	var group groupRow
	if err := r.db.Where("slug = ?", "cleanup").First(&group).Error; err != nil {
		t.Fatal(err)
	}
	if err := r.db.Model(&userRow{}).Where("id = ?", u).Update("group_id", group.ID).Error; err != nil {
		t.Fatal(err)
	}
	if err := r.db.Create(&pspClientRow{UserID: u, PanelID: p + 20, Email: "current-projection@example.test"}).Error; err != nil {
		t.Fatal(err)
	}
	if err := r.db.Create(&destAuditLossHourlyRow{PanelID: p + 20, ObservedHourMS: q.Since.UnixMilli(), Kind: "block", Reason: "queue_full", Rows: 17}).Error; err != nil {
		t.Fatal(err)
	}
	check := func(want int64) {
		t.Helper()
		got, err := r.ReadDestinationHits(t.Context(), q)
		if err != nil || got.Losses.Rows != want || got.Losses.Scope != "panel" || got.Losses.Complete {
			t.Fatalf("related panel losses rows=%d want=%d error=%v", got.Losses.Rows, want, err)
		}
	}
	q.UserID, q.Keyword, q.Action = u, "no-matching-record.test", "observe"
	check(30) // Both historical panels plus the current projection, never p+10.
	q.UserID, q.Source = 0, fmt.Sprintf("p%d", policy)
	check(1029) // A fleet-wide policy can affect panels without stored hits.
	if err := r.db.Model(&destPolicyRow{}).Where("id = ?", policy).Updates(map[string]any{"scope": "groups", "group_ids": jsonInt64s{group.ID}}).Error; err != nil {
		t.Fatal(err)
	}
	check(30)
	if err := r.db.Delete(&destPolicyRow{}, policy).Error; err != nil {
		t.Fatal(err)
	}
	check(13) // Deleted definitions retain their historical panel association.
	q.Source = fmt.Sprintf("g%d", group.ID)
	check(19)
	if err := r.db.Delete(&groupRow{}, group.ID).Error; err != nil {
		t.Fatal(err)
	}
	check(2)
	q.PanelID, q.UserID = p+10, u
	check(999) // An explicit node filter defines the node-level loss scope.
	q.PanelID, q.Source, q.UserID = 0, "", 987654321
	check(0)
}

func TestDestinationHitReadsUseFrozenActionsAndCurrentNullableNames(t *testing.T) {
	r, q, _, _, policy := hitReadFixture(t)
	q.Source, q.Action = fmt.Sprintf("p%d", policy), "block"
	if err := r.db.Model(&destPolicyRow{}).Where("id = ?", policy).Updates(map[string]any{"name": "Renamed policy", "action": "observe"}).Error; err != nil {
		t.Fatal(err)
	}
	got, err := r.ReadDestinationHits(t.Context(), q)
	if err != nil || got.Total != 2 || got.Summary.Block != 12 || got.Summary.Observe != 2 {
		t.Fatal("definition edit reclassified historical actions")
	}
	for _, row := range got.Records {
		if row.Action != "block" || row.SourceName == nil || *row.SourceName != "Renamed policy" {
			t.Fatal("historical action or current display label changed")
		}
	}
	if err := r.db.Delete(&destPolicyRow{}, policy).Error; err != nil {
		t.Fatal(err)
	}
	got, err = r.ReadDestinationHits(t.Context(), q)
	if err != nil || got.Total != 2 || got.Records[0].SourceName != nil {
		t.Fatal("deleted source hid historical records")
	}
}

func TestDestinationHitReadsGroupTiesAndDetailPagesAreDeterministic(t *testing.T) {
	r, p, u, _, now := auditCleanupFixture(t)
	hour := now.Truncate(time.Hour)
	rows := []destHitRow{}
	for _, dest := range []string{"b.example.test", "a.example.test"} {
		for _, port := range []int{443, 80} {
			rows = append(rows, cleanupHit(p, u, hour.UnixMilli(), "p1", "block", dest, port))
		}
	}
	rows = append(rows, cleanupHit(p, u, hour.UnixMilli(), "p2", "block", "other.test", 80))
	rows[4].Count = 4
	if err := r.db.Create(&rows).Error; err != nil {
		t.Fatal(err)
	}
	q := domain.DestHitQuery{Since: hour.Add(37 * time.Minute), Until: hour.Add(time.Hour), PageSize: 1}
	var seen []string
	for q.Page = 1; q.Page <= 5; q.Page++ {
		got, err := r.ReadDestinationHits(t.Context(), q)
		if err != nil || got.Total != 5 || len(got.Records) != 1 {
			t.Fatal("hour overlap or tied page lost a record")
		}
		v := got.Records[0]
		seen = append(seen, fmt.Sprintf("%s/%s/%d", v.Source, v.Dest, v.Port))
	}
	want := []string{"p1/a.example.test/80", "p1/a.example.test/443", "p1/b.example.test/80", "p1/b.example.test/443", "p2/other.test/80"}
	if !reflect.DeepEqual(seen, want) {
		t.Fatal("equal-hour detail pages lack a complete stable tie-breaker")
	}
	q.Page, q.GroupBy = 1, "site"
	first, err := r.ReadDestinationHits(t.Context(), q)
	q.Page = 2
	second, err2 := r.ReadDestinationHits(t.Context(), q)
	if err != nil || err2 != nil || first.Total != 2 || second.Total != 2 || len(first.Groups) != 1 || len(second.Groups) != 1 || first.Groups[0].Key != "example.test" || second.Groups[0].Key != "other.test" {
		t.Fatal("equal-count equal-time groups were paginated without a stable key")
	}
}

func TestDestinationHitReadsLateFailureDoesNotExposePartialPrivateResults(t *testing.T) {
	r, q, _, _, _ := hitReadFixture(t)
	recorder := &auditTraceRecorder{Interface: logger.Discard}
	r.db = r.db.Session(&gorm.Session{Logger: recorder})
	if err := r.db.Callback().Row().Before("gorm:row").Register("hit_loss_private_failure", func(tx *gorm.DB) {
		if tx.Statement.Table == "dest_audit_loss_hourly" {
			tx.AddError(errors.New("sensitive destination and account SQL detail"))
		}
	}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = r.db.Callback().Row().Remove("hit_loss_private_failure") })
	got, err := r.ReadDestinationHits(t.Context(), q)
	if err != errAuditStorage || !reflect.DeepEqual(got, domain.DestHitPage{}) || len(recorder.queries) != 0 {
		t.Fatal("late read failure disclosed private errors or partial success")
	}
}

func TestDestinationHitReadsOneSnapshotAcrossSummaryDetailsNamesAndLosses(t *testing.T) {
	r, q, p, _, policy := hitReadFixture(t)
	if r.db.Dialector.Name() == "sqlite" {
		t.Skip("concurrent snapshot acceptance runs on real MySQL and PostgreSQL")
	}
	pool, err := r.db.DB()
	if err != nil {
		t.Fatal(err)
	}
	oldMax := pool.Stats().MaxOpenConnections
	pool.SetMaxOpenConns(2)
	t.Cleanup(func() { pool.SetMaxOpenConns(oldMax) })
	q.PanelID, q.Source, q.Action = p, fmt.Sprintf("p%d", policy), "block"
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	entered, release := make(chan struct{}), make(chan struct{})
	var enterOnce, releaseOnce sync.Once
	finish := func() { releaseOnce.Do(func() { close(release) }) }
	defer finish()
	if err := r.db.Callback().Row().Before("gorm:row").Register("hit_snapshot_barrier", func(tx *gorm.DB) {
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
	t.Cleanup(func() { _ = r.db.Callback().Row().Remove("hit_snapshot_barrier") })
	type result struct {
		page domain.DestHitPage
		err  error
	}
	done := make(chan result, 1)
	go func() { page, err := r.ReadDestinationHits(ctx, q); done <- result{page, err} }()
	select {
	case <-entered:
	case <-ctx.Done():
		t.Fatal("read did not reach the loss table")
	}
	if err := r.privateDB(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&destHitRow{}).Where("panel_id = ? AND source = ? AND action = ?", p, q.Source, "block").Update("count", 99).Error; err != nil {
			return err
		}
		if err := tx.Model(&destPolicyRow{}).Where("id = ?", policy).Update("name", "New committed name").Error; err != nil {
			return err
		}
		return tx.Model(&destAuditLossHourlyRow{}).Where("panel_id = ? AND kind = ? AND reason = ?", p, "block", "queue_full").Update("rows", 99).Error
	}); err != nil {
		t.Fatal(err)
	}
	finish()
	select {
	case got := <-done:
		if got.err != nil || got.page.Summary.Block != 6 || len(got.page.Records) != 1 || got.page.Records[0].Count != 1 || got.page.Records[0].SourceName == nil || *got.page.Records[0].SourceName != "Read policy" || got.page.Losses.Rows != 2 {
			t.Fatal("read combined different committed snapshots")
		}
	case <-ctx.Done():
		t.Fatal("snapshot read did not finish")
	}
	got, err := r.ReadDestinationHits(ctx, q)
	if err != nil || got.Summary.Block != 104 || got.Records[0].Count != 99 || *got.Records[0].SourceName != "New committed name" || got.Losses.Rows != 99 {
		t.Fatal("later read did not see the new committed snapshot")
	}
}
