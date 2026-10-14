package sqlstore

import (
	"context"
	"database/sql"
	"errors"
	"reflect"
	"strings"
	"testing"

	"gorm.io/gorm"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
)

func TestFleetAccountingReadsOnlyCurrentBaselinesWithoutFilteringClients(t *testing.T) {
	r, _ := inboundBatchFixture(t, 0)
	var rows []pspClientRow
	for i := int64(1); i <= 601; i++ {
		rows = append(rows, pspClientRow{ID: i, UserID: i % 19, PanelID: i % 7,
			Email: "private-fleet-email", UUID: "private-fleet-uuid", Password: "private-fleet-password",
			DesiredEnable: i%2 == 0, DesiredMinted: i%3 == 0,
			LastRawUpBytes: i * 3, LastRawDownBytes: i * 5, LastCounterEpoch: uint64(i % 4)})
	}
	if err := r.db.CreateInBatches(rows, 100).Error; err != nil {
		t.Fatal(err)
	}
	full, err := r.ListAll(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	queries := 0
	const hook = "fleet-accounting-query-shape"
	if err := r.db.Callback().Row().After("gorm:row").Register(hook, func(tx *gorm.DB) {
		queries++
		want := []string{"id", "user_id", "panel_id", "last_raw_up_bytes", "last_raw_down_bytes", "last_counter_epoch"}
		if tx.Statement.Table != "psp_clients" || !reflect.DeepEqual(tx.Statement.Selects, want) {
			t.Error("fleet accounting read fetched definitions or changed its table")
		}
		if tx.Error == nil {
			if _, ok := tx.Statement.Dest.(*sql.Rows); !ok {
				t.Error("fleet accounting retained an intermediate ORM row collection")
			}
		}
		if _, filtered := tx.Statement.Clauses["WHERE"]; filtered {
			t.Error("fleet accounting excluded clients from the denominator")
		}
	}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = r.db.Callback().Row().Remove(hook) })
	projected, err := r.ListFleetAccounting(t.Context())
	if err != nil || len(projected) != 601 || queries != 1 {
		t.Fatal("fleet accounting was not one complete read")
	}
	for i, client := range projected {
		original := full[i]
		want := domain.PSPClientAccounting{ID: original.ID, UserID: original.UserID, PanelID: original.PanelID,
			LastRawUpBytes: original.LastRawUpBytes, LastRawDownBytes: original.LastRawDownBytes, LastCounterEpoch: original.LastCounterEpoch}
		if !reflect.DeepEqual(client, want) {
			t.Fatal("projection changed ordering, ownership or counter baseline")
		}
	}
	if err := r.db.Model(&pspClientRow{}).Where("id = ?", 1).Updates(map[string]any{"last_raw_up_bytes": 999, "last_counter_epoch": 17}).Error; err != nil {
		t.Fatal(err)
	}
	latest, err := r.ListFleetAccounting(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, c := range latest {
		if c.ID == 1 {
			found = c.LastRawUpBytes == 999 && c.LastCounterEpoch == 17
		}
	}
	if !found {
		t.Fatal("fleet accounting reused a stale baseline")
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if result, err := r.ListFleetAccounting(ctx); result != nil || !errors.Is(err, context.Canceled) {
		t.Fatal("cancelled fleet read returned partial accounting")
	}
}

func TestFleetAccountingPreservesNullAndWideCounterBaselines(t *testing.T) {
	r, _ := inboundBatchFixture(t, 0)
	if err := r.db.Create(&pspClientRow{ID: 1, UserID: 2, PanelID: 3, LastCounterEpoch: 1 << 55, LastRawUpBytes: -17, LastRawDownBytes: 42}).Error; err != nil {
		t.Fatal(err)
	}
	if err := r.db.Create(&pspClientRow{ID: 4, UserID: 5, PanelID: 6}).Error; err != nil {
		t.Fatal(err)
	}
	if err := r.db.Model(&pspClientRow{}).Where("id = ?", 4).Updates(map[string]any{"last_raw_up_bytes": nil, "last_raw_down_bytes": nil, "last_counter_epoch": nil}).Error; err != nil {
		t.Fatal(err)
	}
	full, err := r.ListAll(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	projected, err := r.ListFleetAccounting(t.Context())
	if err != nil || len(projected) != 2 {
		t.Fatal("nullable fleet projection failed")
	}
	for i, got := range projected {
		original := full[i]
		want := domain.PSPClientAccounting{ID: original.ID, UserID: original.UserID, PanelID: original.PanelID, LastRawUpBytes: original.LastRawUpBytes, LastRawDownBytes: original.LastRawDownBytes, LastCounterEpoch: original.LastCounterEpoch}
		if !reflect.DeepEqual(got, want) {
			t.Fatal("fleet projection changed nullable, signed or wide integer baselines")
		}
	}
}

func TestFleetAccountingClosesCursorAndRejectsPartialScan(t *testing.T) {
	r, _ := inboundBatchFixture(t, 0)
	db, err := r.db.DB()
	if err != nil {
		t.Fatal(err)
	}
	before := db.Stats().InUse
	// The first row is valid; the second cannot be converted to the typed ID.
	// Use real SQL rows on each dialect; change only this fixture's SQL before
	// the normal row callback opens its cursor, including on one-connection DBs.
	const faultSQL = `SELECT id, user_id, panel_id, last_raw_up_bytes, last_raw_down_bytes, last_counter_epoch FROM (
		SELECT '1' AS id, 2 AS user_id, 3 AS panel_id, 4 AS last_raw_up_bytes, 5 AS last_raw_down_bytes, 6 AS last_counter_epoch, 1 AS ordinal
		UNION ALL SELECT 'invalid-integer', 2, 3, 4, 5, 6, 2
	) AS faulty_rows ORDER BY ordinal`
	const hook = "fleet-accounting-scan-fault"
	if err := r.db.Callback().Row().Before("gorm:row").Register(hook, func(tx *gorm.DB) {
		tx.Statement.SQL.Reset()
		tx.Statement.SQL.WriteString(faultSQL)
		tx.Statement.Vars = nil
	}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = r.db.Callback().Row().Remove(hook) })
	if result, err := r.ListFleetAccounting(t.Context()); result != nil || err == nil || !strings.Contains(err.Error(), "invalid-integer") {
		t.Fatal("failed scan returned partial quota accounting")
	}
	if db.Stats().InUse != before {
		t.Fatal("failed fleet scan retained its SQL cursor")
	}
}
