package sqlstore

import (
	"errors"
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func auditCleanupFixture(t *testing.T) (*DestAuditRepo, int64, int64, int64, time.Time) {
	t.Helper()
	r, b := auditIngestFixture(t, 0)
	u := userRow{UPN: "cleanup@example.test", UUID: "11111111-1111-4111-8111-111111111111", SubToken: "cleanup-fixture-token"}
	g := groupRow{Slug: "cleanup", Name: "Cleanup"}
	if err := r.db.Create(&u).Error; err != nil {
		t.Fatal(err)
	}
	if err := r.db.Create(&g).Error; err != nil {
		t.Fatal(err)
	}
	return r, b.PanelID, u.ID, g.ID, time.UnixMilli(b.HourMS).Add(37 * time.Minute).UTC()
}

func cleanupHit(panel, user, hour int64, source, action, dest string, port int) destHitRow {
	return destHitRow{PanelID: panel, UserID: user, HourMS: hour, Source: source, Action: action, Dest: dest, Port: port, Count: 1, FirstAt: time.UnixMilli(hour), LastAt: time.UnixMilli(hour + 1)}
}

func TestDestAuditCleanupDistinctRetentionsUTCAndExactTrialSource(t *testing.T) {
	r, p, u, g, now := auditCleanupFixture(t)
	settings := domain.DestinationSettings{HitRetentionDays: 30, TrialRetentionDays: 7, UsageRetentionDays: 3}
	hour := now.Truncate(time.Hour)
	hitCut, trialCut, usageCut := hour.Add(-30*24*time.Hour).UnixMilli(), hour.Add(-7*24*time.Hour).UnixMilli(), hour.Add(-3*24*time.Hour).UnixMilli()
	rows := []destHitRow{
		cleanupHit(p, u, hitCut-3600000, "p12", "block", "old-block.test", 443),
		cleanupHit(p, u, hitCut, "p12", "block", "boundary-block.test", 443),
		cleanupHit(p, u, hitCut-3600000, "p12", "observe", "old-observe.test", 443),
		cleanupHit(p, 0, trialCut-3600000, fmt.Sprintf("g%d", g), "observe", "old-trial.test", 0),
		cleanupHit(p, 0, trialCut, fmt.Sprintf("g%d", g), "observe", "boundary-trial.test", 0),
		cleanupHit(p, u, trialCut-3600000, fmt.Sprintf("g%dx1", g), "observe", "fragment.test", 443),
		cleanupHit(p, u, trialCut-3600000, fmt.Sprintf("G%d", g), "observe", "uppercase.test", 443),
	}
	if err := r.db.Create(&rows).Error; err != nil {
		t.Fatal(err)
	}
	usage := []destUsageHourlyRow{{HourMS: usageCut - 3600000, PanelID: p, UserID: u, Site: "old.test", Count: 1}, {HourMS: usageCut, PanelID: p, UserID: u, Site: "boundary.test", Count: 1}}
	if err := r.db.Create(&usage).Error; err != nil {
		t.Fatal(err)
	}
	losses := []destAuditLossHourlyRow{}
	for _, entry := range []struct {
		kind string
		cut  int64
	}{{"block", hitCut}, {"observe", hitCut}, {"trial", trialCut}, {"usage", usageCut}} {
		for _, offset := range []int64{-3600000, 0} {
			losses = append(losses, destAuditLossHourlyRow{ObservedHourMS: entry.cut + offset, PanelID: p, Kind: entry.kind, Reason: "queue_full", Rows: 1})
		}
	}
	if err := r.db.Create(&losses).Error; err != nil {
		t.Fatal(err)
	}
	got, err := r.PruneDestinationAudit(t.Context(), now.In(time.FixedZone("fixture", -7*3600)), &settings)
	if err != nil || got != (domain.DestAuditPruned{Hits: 2, Trial: 1, Usage: 1, Loss: 4}) {
		t.Fatalf("retention counts%+v error%v", got, err)
	}
	if auditRowCount(t, r.db, &destHitRow{}) != 4 || auditRowCount(t, r.db, &destUsageHourlyRow{}) != 1 || auditRowCount(t, r.db, &destAuditLossHourlyRow{}) != 4 {
		t.Fatal("retention crossed a UTC hour or trial shape")
	}
}

func TestDestAuditCleanupDedupKeepsFull72HoursAndFutureReceipts(t *testing.T) {
	r, _, _, _, now := auditCleanupFixture(t)
	cut := now.Add(-72 * time.Hour)
	ages := []time.Duration{24 * time.Hour, 48 * time.Hour, 72 * time.Hour, 72*time.Hour + time.Millisecond, -time.Hour}
	for i, age := range ages {
		row := destAuditBatchRow{AgentID: "agt_audit", BatchID: fmt.Sprintf("%032x", i+1), Kind: "block", HourMS: now.Truncate(time.Hour).UnixMilli(), ReceivedAt: now.Add(-age)}
		if err := r.db.Create(&row).Error; err != nil {
			t.Fatal(err)
		}
	}
	for i, delta := range []time.Duration{-time.Hour, 0, time.Hour} {
		row := destAuditIngestBudgetRow{AgentID: "agt_audit", ReceivedHourMS: cut.Truncate(time.Hour).Add(delta).UnixMilli(), Kind: []string{"block", "trial", "usage"}[i], RowsReserved: 1}
		if err := r.db.Create(&row).Error; err != nil {
			t.Fatal(err)
		}
	}
	got, err := r.PruneDestinationAudit(t.Context(), now, nil)
	if err != nil || got != (domain.DestAuditPruned{Batches: 1, Budget: 1}) || auditRowCount(t, r.db, &destAuditBatchRow{}) != 4 || auditRowCount(t, r.db, &destAuditIngestBudgetRow{}) != 2 {
		t.Fatalf("72h retention%+v error%v", got, err)
	}
}

func TestDestAuditCleanupOrphansPreserveAnonymousTrialAndReceiverLossMarker(t *testing.T) {
	r, p, u, g, now := auditCleanupFixture(t)
	hour := now.Truncate(time.Hour).UnixMilli()
	valid := cleanupHit(p, 0, hour, fmt.Sprintf("g%d", g), "observe", "valid-trial.test", 0)
	rows := []destHitRow{valid,
		cleanupHit(p, 0, hour, fmt.Sprintf("g%dx1", g), "observe", "bad-fragment.test", 0),
		cleanupHit(p, 0, hour, fmt.Sprintf("g0%d", g), "observe", "bad-leading-zero.test", 0),
		cleanupHit(p, 0, hour, fmt.Sprintf("G%d", g), "observe", "bad-case.test", 0),
		cleanupHit(p, 0, hour, fmt.Sprintf("g%d ", g), "observe", "bad-space.test", 0),
		cleanupHit(p, 0, hour, fmt.Sprintf("g%d\n", g), "observe", "bad-newline.test", 0),
		cleanupHit(p, 0, hour, fmt.Sprintf("g%d", g), "block", "bad-action.test", 0),
		cleanupHit(p, 0, hour, fmt.Sprintf("g%d", g), "observe", "bad-port.test", 443),
		cleanupHit(p, 0, hour, "g999999", "observe", "deleted-group.test", 0),
		cleanupHit(p, 999999, hour, "p12", "block", "deleted-user.test", 443),
		cleanupHit(999999, u, hour, "p12", "block", "deleted-panel.test", 443),
		cleanupHit(p, -1, hour, "p12", "block", "negative-user.test", 443),
	}
	if err := r.db.Create(&rows).Error; err != nil {
		t.Fatal(err)
	}
	if err := r.db.Create(&[]destUsageHourlyRow{{PanelID: p, UserID: 999999, HourMS: hour, Site: "deleted.test"}, {PanelID: 999999, UserID: u, HourMS: hour, Site: "deleted.test"}, {PanelID: p, UserID: 0, HourMS: hour, Site: "anonymous.test"}}).Error; err != nil {
		t.Fatal(err)
	}
	for i, entry := range []struct{ agent, kind string }{{"", "receiver_loss"}, {"", "block"}, {"deleted", "block"}, {"agt_audit", "block"}} {
		if err := r.db.Create(&destAuditBatchRow{AgentID: entry.agent, Kind: entry.kind, BatchID: fmt.Sprintf("%032x", i+1), ReceivedAt: now}).Error; err != nil {
			t.Fatal(err)
		}
	}
	if err := r.db.Create(&destAuditIngestBudgetRow{AgentID: "deleted", ReceivedHourMS: hour, Kind: "block"}).Error; err != nil {
		t.Fatal(err)
	}
	if err := r.db.Create(&destAgentPolicyRow{AgentID: "deleted"}).Error; err != nil {
		t.Fatal(err)
	}
	if err := r.db.Create(&destAuditLossHourlyRow{PanelID: 999999, ObservedHourMS: hour, Kind: "block", Reason: "queue_full"}).Error; err != nil {
		t.Fatal(err)
	}
	got, err := r.PruneDestinationAudit(t.Context(), now, nil)
	if err != nil || got.Orphans != 19 {
		t.Fatalf("orphan count%+v error%v", got, err)
	}
	if auditRowCount(t, r.db, &destHitRow{}) != 1 || auditRowCount(t, r.db, &destUsageHourlyRow{}) != 0 || auditRowCount(t, r.db, &destAuditBatchRow{}) != 2 {
		t.Fatal("anonymous trial or receiver marker deleted, or malformed rows kept")
	}
	// Returning the group to open is not an orphan; deleting it is.
	if err := r.db.Create(&destGroupModeRow{GroupID: g, Mode: "open"}).Error; err != nil {
		t.Fatal(err)
	}
	if got, err := r.PruneDestinationAudit(t.Context(), now, nil); err != nil || got.Orphans != 0 {
		t.Fatal("open mode removed historical trial")
	}
	if err := r.db.Delete(&groupRow{}, g).Error; err != nil {
		t.Fatal(err)
	}
	if got, err := r.PruneDestinationAudit(t.Context(), now, nil); err != nil || got.Orphans != 1 {
		t.Fatal("deleted group retained anonymous trial")
	}
	if got, err := r.PruneDestinationAudit(t.Context(), now.Add(72*time.Hour+time.Millisecond), nil); err != nil || got.Batches != 2 {
		t.Fatal("receiver loss marker escaped fixed retention")
	}
}

func TestDestAuditCleanupRollbackDoesNotReportDeletesOrLeakSQL(t *testing.T) {
	r, p, u, _, now := auditCleanupFixture(t)
	old := now.Add(-31 * 24 * time.Hour).Truncate(time.Hour).UnixMilli()
	if err := r.db.Create(&[]destHitRow{cleanupHit(p, u, old, "p12", "block", "private-destination.test", 443)}).Error; err != nil {
		t.Fatal(err)
	}
	if err := r.db.Create(&destUsageHourlyRow{HourMS: old, PanelID: p, UserID: u, Site: "private-site.test"}).Error; err != nil {
		t.Fatal(err)
	}
	recorder := &auditTraceRecorder{Interface: logger.Discard}
	r.db = r.db.Session(&gorm.Session{Logger: recorder})
	if err := r.db.Callback().Delete().Before("gorm:delete").Register("audit_cleanup_rollback", func(tx *gorm.DB) {
		if tx.Statement.Table == "dest_usage_hourly" {
			tx.AddError(errors.New("private SQL and account values"))
		}
	}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = r.db.Callback().Delete().Remove("audit_cleanup_rollback") })
	settings := domain.DestinationSettings{HitRetentionDays: 30, TrialRetentionDays: 7, UsageRetentionDays: 7}
	got, err := r.PruneDestinationAudit(t.Context(), now, &settings)
	if err != errAuditStorage || !reflect.DeepEqual(got, domain.DestAuditPruned{}) || len(recorder.queries) != 0 {
		t.Fatal("failed cleanup reported deletes or exposed SQL/error values")
	}
	_ = r.db.Callback().Delete().Remove("audit_cleanup_rollback")
	if auditRowCount(t, r.db, &destHitRow{}) != 1 || auditRowCount(t, r.db, &destUsageHourlyRow{}) != 1 {
		t.Fatal("failed cleanup did not roll back all tables")
	}
	got, err = r.PruneDestinationAudit(t.Context(), now, &settings)
	if err != nil || got.Hits != 1 || got.Usage != 1 {
		t.Fatal("cleanup could not retry after rollback")
	}
}
