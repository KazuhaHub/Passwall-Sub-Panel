package sqlstore

import (
	"context"
	"errors"
	"math"
	"reflect"
	"testing"
	"time"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func usageReadFixture(t *testing.T) (*DestAuditRepo, domain.DestUsageQuery, int64) {
	t.Helper()
	r, panel, user, _, now := auditCleanupFixture(t)
	q := domain.DestUsageQuery{UserID: user, Since: now.Truncate(time.Hour).Add(-time.Hour), Until: now, Limit: 2}
	hour := q.Since.UnixMilli()
	rows := []destUsageHourlyRow{
		{HourMS: hour, PanelID: panel, UserID: user, Site: "example.com", Count: 3},
		{HourMS: hour + 3600000, PanelID: panel, UserID: user, Site: "example.com", Count: 4},
		{HourMS: hour, PanelID: panel + 1, UserID: user, Site: "example.com", Count: 5},
		{HourMS: hour, PanelID: panel, UserID: user, Site: "(ip)", Count: 2},
		{HourMS: hour, PanelID: panel, UserID: user, Site: "other.test", Count: 2},
		{HourMS: hour, PanelID: panel, UserID: user + 1000, Site: "private-other-account.test", Count: 999},
		{HourMS: hour - 3600000, PanelID: panel, UserID: user, Site: "before.test", Count: 999},
		{HourMS: q.Until.Truncate(time.Hour).Add(time.Hour).UnixMilli(), PanelID: panel, UserID: user, Site: "after.test", Count: 999},
	}
	if err := r.db.Create(&rows).Error; err != nil {
		t.Fatal(err)
	}
	losses := []destAuditLossHourlyRow{
		{ObservedHourMS: hour, PanelID: panel, Kind: "usage", Reason: "queue_full", Rows: 3},
		{ObservedHourMS: hour, PanelID: panel + 1, Kind: "usage", Reason: "node_dropped", Events: 4, Unmatched: 5},
		{ObservedHourMS: hour, PanelID: panel, Kind: "block", Reason: "queue_full", Rows: 999},
		{ObservedHourMS: hour, PanelID: panel + 999, Kind: "usage", Reason: "queue_full", Rows: 999},
	}
	if err := r.db.Create(&losses).Error; err != nil {
		t.Fatal(err)
	}
	return r, q, panel
}

func TestDestinationUsageReadsMergeHoursNodesAndKeepPanelLossScope(t *testing.T) {
	r, q, panel := usageReadFixture(t)
	got, err := r.ReadDestinationUsage(t.Context(), q)
	if err != nil || got.TotalSites != 3 || got.TotalCount != 16 || !reflect.DeepEqual(got.Items, []domain.DestUsageSite{{Site: "example.com", Count: 12}, {Site: "(ip)", Count: 2}}) {
		t.Fatalf("usage aggregation: %+v %v", got, err)
	}
	if got.Losses != (domain.DestAuditLosses{Rows: 3, Events: 4, Unmatched: 5, Scope: "panel"}) {
		t.Fatalf("loss units/related panels: %+v", got.Losses)
	}
	q.PanelID = panel
	got, err = r.ReadDestinationUsage(t.Context(), q)
	if err != nil || got.TotalCount != 11 || got.Items[0].Count != 7 || got.Losses.Rows != 3 || got.Losses.Events != 0 {
		t.Fatalf("panel filter: %+v %v", got, err)
	}
	q.UserID += 2000
	got, err = r.ReadDestinationUsage(t.Context(), q)
	if err != nil || got.TotalCount != 0 || got.Items == nil || len(got.Items) != 0 || got.Losses.Scope != "panel" || got.Losses.Complete {
		t.Fatal("empty usage became unknown or attributed loss")
	}
}

func TestDestinationUsageReadSaturatesAndDoesNotReturnPartialOnFailure(t *testing.T) {
	r, q, panel := usageReadFixture(t)
	q.Limit = 1
	rows := []destUsageHourlyRow{{HourMS: q.Since.UnixMilli(), PanelID: panel, UserID: q.UserID, Site: "overflow.test", Count: math.MaxInt64}, {HourMS: q.Since.Add(time.Hour).UnixMilli(), PanelID: panel, UserID: q.UserID, Site: "overflow.test", Count: 1}}
	if err := r.db.Create(&rows).Error; err != nil {
		t.Fatal(err)
	}
	got, err := r.ReadDestinationUsage(t.Context(), q)
	if err != nil || got.TotalCount != math.MaxInt64 || len(got.Items) != 1 || got.Items[0].Count != math.MaxInt64 {
		t.Fatalf("overflowed usage: %+v %v", got, err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	got, err = r.ReadDestinationUsage(ctx, q)
	if !errors.Is(err, context.Canceled) || !reflect.DeepEqual(got, domain.DestUsagePage{}) {
		t.Fatalf("canceled read returned data: %+v %v", got, err)
	}
	q.UserID = 0
	if _, err := r.ReadDestinationUsage(t.Context(), q); !errors.Is(err, domain.ErrValidation) {
		t.Fatal("repository allowed fleet-wide usage")
	}
}

func TestDestinationUsageReadProjectsNoCredentialsAndSuppressesSQLFailures(t *testing.T) {
	r, q, _ := usageReadFixture(t)
	recorder := &auditTraceRecorder{Interface: logger.Discard}
	r.db = r.db.Session(&gorm.Session{Logger: recorder})
	seenUsage, seenLoss := false, false
	const callback = "private_usage_read_failure"
	if err := r.db.Callback().Row().Before("gorm:row").Register(callback, func(tx *gorm.DB) {
		switch tx.Statement.Table {
		case "dest_usage_hourly":
			seenUsage = true
			if !reflect.DeepEqual(tx.Statement.Selects, []string{"site", "count"}) {
				t.Error("usage query loaded private account fields")
			}
		case "dest_audit_loss_hourly":
			seenLoss = true
			if !reflect.DeepEqual(tx.Statement.Selects, []string{"rows", "events", "unmatched"}) {
				t.Error("loss query loaded private row fields")
			}
			_ = tx.AddError(errors.New("private-site-and-account-driver-detail"))
		default:
			t.Errorf("unexpected usage query table %s", tx.Statement.Table)
		}
	}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = r.db.Callback().Row().Remove(callback) })
	got, err := r.ReadDestinationUsage(t.Context(), q)
	if !seenUsage || !seenLoss || err != errAuditStorage || !reflect.DeepEqual(got, domain.DestUsagePage{}) || len(recorder.queries) != 0 {
		t.Fatalf("private/partial usage read escaped: usage=%t loss=%t result=%+v error=%v traces=%d", seenUsage, seenLoss, got, err, len(recorder.queries))
	}
}
