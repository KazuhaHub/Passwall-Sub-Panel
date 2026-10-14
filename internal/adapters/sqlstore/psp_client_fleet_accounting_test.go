package sqlstore

import (
	"context"
	"errors"
	"reflect"
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
	if err := r.db.Callback().Query().After("gorm:query").Register(hook, func(tx *gorm.DB) {
		queries++
		want := []string{"id", "user_id", "panel_id", "last_raw_up_bytes", "last_raw_down_bytes", "last_counter_epoch"}
		if tx.Statement.Table != "psp_clients" || !reflect.DeepEqual(tx.Statement.Selects, want) {
			t.Error("fleet accounting read fetched definitions or changed its table")
		}
		if _, filtered := tx.Statement.Clauses["WHERE"]; filtered {
			t.Error("fleet accounting excluded clients from the denominator")
		}
	}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = r.db.Callback().Query().Remove(hook) })
	projected, err := r.ListFleetAccounting(t.Context())
	if err != nil || len(projected) != 601 || queries != 1 {
		t.Fatal("fleet accounting was not one complete read")
	}
	for i, client := range projected {
		original := full[i]
		want := &domain.PSPClient{ID: original.ID, UserID: original.UserID, PanelID: original.PanelID,
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
