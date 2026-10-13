package sqlstore

import (
	"context"
	"errors"
	"fmt"
	"math"
	"reflect"
	"testing"
	"time"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func groupTrialReadFixture(t *testing.T) (*DestAuditRepo, domain.DestGroupTrialQuery, int64, int64) {
	t.Helper()
	r, panel, user, group, now := auditCleanupFixture(t)
	q := domain.DestGroupTrialQuery{GroupID: group, Since: now.Truncate(time.Hour).Add(-time.Hour), Until: now}
	hour, source := q.Since.UnixMilli(), fmt.Sprintf("g%d", group)
	rows := []destHitRow{
		cleanupHit(panel, 0, hour, source, "observe", "a.foo.github.io", 0),
		cleanupHit(panel+1, 0, hour, source, "observe", "a.foo.github.io", 0),
		cleanupHit(panel, 0, hour+3600000, source, "observe", "a.foo.github.io", 0),
		cleanupHit(panel, 0, hour, source, "observe", "(ip)", 0),
		cleanupHit(panel, user, hour, source, "block", "private-host.example.test", 443),
		cleanupHit(panel, 0, hour, "g999999", "observe", "other-group.test", 0),
		cleanupHit(panel, user, hour, "p1", "observe", "global-observe.test", 443),
		cleanupHit(panel, 0, hour-3600000, source, "observe", "before.test", 0),
		cleanupHit(panel, 0, q.Until.Truncate(time.Hour).Add(time.Hour).UnixMilli(), source, "observe", "after.test", 0),
	}
	rows[0].Count, rows[1].Count, rows[2].Count = 3, 4, 5
	for i := range 25 {
		rows = append(rows, cleanupHit(panel, 0, hour, source, "observe", fmt.Sprintf("site%02d.test", i), 0))
	}
	if err := r.db.Create(&rows).Error; err != nil {
		t.Fatal(err)
	}
	if err := r.db.Model(&userRow{}).Where("id = ?", user).Update("group_id", group).Error; err != nil {
		t.Fatal(err)
	}
	if err := r.db.Create(&pspClientRow{UserID: user, PanelID: panel + 20, Email: "current-trial-projection@example.test"}).Error; err != nil {
		t.Fatal(err)
	}
	for _, row := range []destAuditLossHourlyRow{
		{PanelID: panel, ObservedHourMS: hour, Kind: "trial", Reason: "queue_full", Rows: 2},
		{PanelID: panel + 1, ObservedHourMS: hour, Kind: "trial", Reason: "node_dropped", Events: 3},
		{PanelID: panel + 20, ObservedHourMS: hour, Kind: "trial", Reason: "node_unmatched", Unmatched: 4},
		{PanelID: panel + 999, ObservedHourMS: hour, Kind: "trial", Reason: "queue_full", Rows: 999},
		{PanelID: panel, ObservedHourMS: hour, Kind: "block", Reason: "queue_full", Rows: 999},
		{PanelID: panel, ObservedHourMS: hour, Kind: "usage", Reason: "queue_full", Rows: 999},
	} {
		if err := r.db.Create(&row).Error; err != nil {
			t.Fatal(err)
		}
	}
	return r, q, panel, user
}

func TestDestinationGroupTrialReadsAllSitesWithoutRefoldingOrAccountData(t *testing.T) {
	r, q, _, _ := groupTrialReadFixture(t)
	got, err := r.ReadDestinationGroupTrial(t.Context(), q)
	if err != nil || len(got.Items) != 27 || got.TotalCount != 38 {
		t.Fatalf("complete trial report: %+v %v", got, err)
	}
	if got.Items[0].Site != "a.foo.github.io" || got.Items[0].Count != 12 || got.Items[0].LastAt != q.Since.Add(time.Hour).UnixMilli()+1 {
		t.Fatal("trial aggregation refolded the supplied site or lost hours/nodes")
	}
	if got.Items[1].Site != "(ip)" || got.Items[len(got.Items)-1].Site != "site24.test" {
		t.Fatal("trial report truncation or deterministic ordering changed")
	}
	if got.Losses != (domain.DestAuditLosses{Rows: 2, Events: 3, Unmatched: 4, Scope: "panel"}) || got.DroppedInRange != 2 {
		t.Fatalf("trial loss units/scope: %+v", got.Losses)
	}
	q.GroupID = 888888
	empty, err := r.ReadDestinationGroupTrial(t.Context(), q)
	if err != nil || empty.Items == nil || len(empty.Items) != 0 || empty.TotalCount != 0 || empty.DroppedInRange != 0 || empty.Losses.Scope != "panel" || empty.Losses.Complete {
		t.Fatal("empty group acquired another group's history or loss totals")
	}
}

func TestDestinationGroupTrialReadsSaturateAndFailWithoutPartialData(t *testing.T) {
	r, q, panel, _ := groupTrialReadFixture(t)
	rows := []destHitRow{
		cleanupHit(panel, 0, q.Since.UnixMilli(), fmt.Sprintf("g%d", q.GroupID), "observe", "overflow.test", 0),
		cleanupHit(panel+1, 0, q.Since.UnixMilli(), fmt.Sprintf("g%d", q.GroupID), "observe", "overflow.test", 0),
	}
	rows[0].Count = math.MaxInt64
	if err := r.db.Create(&rows).Error; err != nil {
		t.Fatal(err)
	}
	got, err := r.ReadDestinationGroupTrial(t.Context(), q)
	if err != nil || got.TotalCount != math.MaxInt64 || got.Items[0].Site != "overflow.test" || got.Items[0].Count != math.MaxInt64 {
		t.Fatal("trial count overflow wrapped or failed in SQL SUM")
	}
	if err := r.db.Model(&destHitRow{}).Where("dest = ?", "overflow.test").Update("count", -1).Error; err != nil {
		t.Fatal(err)
	}
	got, err = r.ReadDestinationGroupTrial(t.Context(), q)
	if err == nil || !reflect.DeepEqual(got, domain.DestGroupTrialData{}) {
		t.Fatal("corrupt trial counter returned partial sites")
	}
}

func TestDestinationGroupTrialReadsUseNarrowPrivateProjection(t *testing.T) {
	r, q, _, _ := groupTrialReadFixture(t)
	recorder := &auditTraceRecorder{Interface: logger.Discard}
	r.db = r.db.Session(&gorm.Session{Logger: recorder})
	seenSites, seenLoss := false, false
	const callback = "private_group_trial_read_failure"
	if err := r.db.Callback().Row().Before("gorm:row").Register(callback, func(tx *gorm.DB) {
		switch tx.Statement.Table {
		case "dest_hits":
			seenSites = true
			if !reflect.DeepEqual(tx.Statement.Selects, []string{"dest", "count", "last_at"}) {
				t.Error("trial report loaded account or raw hit fields")
			}
		case "dest_audit_loss_hourly":
			seenLoss = true
			if !reflect.DeepEqual(tx.Statement.Selects, []string{"rows", "events", "unmatched"}) {
				t.Error("trial report loaded private loss fields")
			}
			_ = tx.AddError(errors.New("private-group-site-driver-detail"))
		default:
			t.Errorf("unexpected trial query table %s", tx.Statement.Table)
		}
	}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = r.db.Callback().Row().Remove(callback) })
	got, err := r.ReadDestinationGroupTrial(t.Context(), q)
	if !seenSites || !seenLoss || err != errAuditStorage || !reflect.DeepEqual(got, domain.DestGroupTrialData{}) || len(recorder.queries) != 0 {
		t.Fatalf("private/partial trial report escaped: sites=%t loss=%t error=%v traces=%d", seenSites, seenLoss, err, len(recorder.queries))
	}
}

func TestDestinationGroupTrialReadsRejectInvalidAndCanceledBeforeStorage(t *testing.T) {
	r, q, _, _ := groupTrialReadFixture(t)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	got, err := r.ReadDestinationGroupTrial(ctx, q)
	if !errors.Is(err, context.Canceled) || !reflect.DeepEqual(got, domain.DestGroupTrialData{}) {
		t.Fatal("canceled trial read returned data or hid cancellation")
	}
	q.GroupID = 0
	if _, err := r.ReadDestinationGroupTrial(t.Context(), q); !errors.Is(err, domain.ErrValidation) {
		t.Fatal("repository allowed fleet-wide trial report")
	}
}
