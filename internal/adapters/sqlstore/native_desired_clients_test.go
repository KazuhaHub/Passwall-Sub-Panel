package sqlstore

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"gorm.io/gorm"
)

func TestNativeDesiredClientCursorPreservesEveryCurrentFieldAndHistoricNull(t *testing.T) {
	clients, _ := inboundBatchFixture(t, 0)
	stamp := time.Date(2026, time.October, 1, 12, 0, 0, 0, time.UTC)
	rows := []pspClientRow{
		{ID: 1, UserID: 2, PanelID: 7, Email: "complete@fixture.local", CredClass: 4, UUID: "fixture-uuid", Password: "fixture-password",
			DesiredEnable: true, DesiredExpiryTime: 123456, PanelQuotaHeadroom: 789, PanelIPLimit: 5, PanelDeviceLimit: 6, DesiredMinted: true, CreatedAt: stamp,
			LifetimeUpBytes: 11, LifetimeDownBytes: 12, LifetimeTotalBytes: 23, LastRawUpBytes: -17, LastRawDownBytes: 42, LastRawTotalBytes: 25, LastCounterEpoch: 1 << 55,
			PeriodBaselineUpBytes: 13, PeriodBaselineDownBytes: 14, PeriodBaselineTotalBytes: 27},
		{ID: 2, UserID: 3, PanelID: 7, Email: "null@fixture.local"},
		{ID: 3, UserID: 4, PanelID: 8, Email: "foreign@fixture.local", LastRawUpBytes: 999},
	}
	if err := clients.db.Create(&rows).Error; err != nil {
		t.Fatal(err)
	}
	if err := clients.db.Model(&pspClientRow{}).Where("id = ?", 2).Updates(map[string]any{
		"created_at": nil, "lifetime_up_bytes": nil, "lifetime_down_bytes": nil, "lifetime_total_bytes": nil,
		"last_raw_up_bytes": nil, "last_raw_down_bytes": nil, "last_raw_total_bytes": nil, "last_counter_epoch": nil,
		"period_baseline_up_bytes": nil, "period_baseline_down_bytes": nil, "period_baseline_total_bytes": nil,
	}).Error; err != nil {
		t.Fatal(err)
	}
	r := &nativeDesiredRepo{db: clients.db}
	compare := func() {
		t.Helper()
		var original []pspClientRow
		if err := clients.db.Where("panel_id = ?", 7).Order("id").Find(&original).Error; err != nil {
			t.Fatal(err)
		}
		full, err := r.Load(t.Context(), 7)
		if err != nil || len(full.Clients) != len(original) || len(original) != 2 {
			t.Fatalf("typed client snapshot changed current owner closure: %v", err)
		}
		for index, row := range original {
			want := rowToPSPClient(&row)
			if !reflect.DeepEqual(full.Clients[index].Client, want) {
				t.Fatal("typed cursor differs from independent complete model read")
			}
			scoped, err := r.LoadClient(t.Context(), 7, row.Email)
			if err != nil || len(scoped.Clients) != 1 || !reflect.DeepEqual(scoped.Clients[0].Client, want) {
				t.Fatalf("scoped cursor changed complete client fields: %v", err)
			}
		}
	}
	compare()
	if err := clients.db.Model(&pspClientRow{}).Where("id = ?", 1).Updates(map[string]any{"uuid": "updated-uuid", "desired_enable": false, "last_raw_up_bytes": 0, "last_counter_epoch": 19}).Error; err != nil {
		t.Fatal(err)
	}
	compare()
	empty, err := r.Load(t.Context(), 999)
	if err != nil || empty.Clients == nil || len(empty.Clients) != 0 {
		t.Fatal("empty current snapshot reused rows or changed its collection shape")
	}
}

func TestNativeDesiredClientCursorRejectsPartialScanAndReleasesTransaction(t *testing.T) {
	clients, _ := inboundBatchFixture(t, 0)
	if err := clients.db.Create(&[]pspClientRow{{ID: 1, UserID: 1, PanelID: 7, Email: "first@fixture.local"}, {ID: 2, UserID: 2, PanelID: 7, Email: "second@fixture.local"}}).Error; err != nil {
		t.Fatal(err)
	}
	r := &nativeDesiredRepo{db: clients.db}
	pool, err := clients.db.DB()
	if err != nil {
		t.Fatal(err)
	}
	before := pool.Stats().InUse
	const faultSQL = `SELECT CASE WHEN id = 2 THEN 'invalid-integer' ELSE '1' END AS id,
		user_id, panel_id, email, cred_class, uuid, password, desired_enable, desired_expiry_time,
		panel_quota_headroom, panel_ip_limit, panel_device_limit, desired_minted, created_at,
		lifetime_up_bytes, lifetime_down_bytes, lifetime_total_bytes, last_raw_up_bytes, last_raw_down_bytes,
		last_raw_total_bytes, last_counter_epoch, period_baseline_up_bytes, period_baseline_down_bytes,
		period_baseline_total_bytes FROM psp_clients ORDER BY user_id`
	const hook = "native-desired-client-scan-fault"
	if err := r.db.Callback().Row().Before("gorm:row").Register(hook, func(tx *gorm.DB) {
		if tx.Statement.Table == "psp_clients" {
			tx.Statement.SQL.Reset()
			tx.Statement.SQL.WriteString(faultSQL)
			tx.Statement.Vars = nil
		}
	}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = r.db.Callback().Row().Remove(hook) })
	if snapshot, err := r.Load(t.Context(), 7); snapshot != nil || err == nil || !strings.Contains(err.Error(), "invalid-integer") {
		t.Fatal("failed client scan published a partial desired snapshot")
	}
	_ = r.db.Callback().Row().Remove(hook)
	if pool.Stats().InUse != before {
		t.Fatal("failed client scan retained its cursor or transaction")
	}
	if snapshot, err := r.Load(t.Context(), 7); err != nil || len(snapshot.Clients) != 2 {
		t.Fatalf("failed scan poisoned the next current transaction: %v", err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if snapshot, err := r.Load(ctx, 7); snapshot != nil || !errors.Is(err, context.Canceled) {
		t.Fatal("cancelled desired read returned partial or stale state")
	}
}
