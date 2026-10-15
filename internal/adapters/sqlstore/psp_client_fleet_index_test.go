package sqlstore

import (
	"reflect"
	"strings"
	"testing"
)

func TestFleetAccountingCoveringIndexIncludesExactlyTheCurrentNumericProjection(t *testing.T) {
	r, _ := inboundBatchFixture(t, 0)
	const name = "idx_psp_client_fleet_accounting"
	indexes, err := r.db.Migrator().GetIndexes(&pspClientRow{})
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, index := range indexes {
		if index.Name() != name {
			continue
		}
		found = true
		if !reflect.DeepEqual(index.Columns(), []string{"panel_id", "user_id", "id", "last_raw_up_bytes", "last_raw_down_bytes", "last_counter_epoch"}) {
			t.Fatal("fleet index changed ordering or included definitions")
		}
		if unique, known := index.Unique(); !known || unique {
			t.Fatal("accounting index must not constrain fleet membership")
		}
	}
	if !found {
		t.Fatal("current schema did not create the fleet accounting index")
	}
	if r.db.Dialector.Name() == "sqlite" {
		var plan []struct{ Detail string }
		if err := r.db.Raw("EXPLAIN QUERY PLAN SELECT id,user_id,panel_id,last_raw_up_bytes,last_raw_down_bytes,last_counter_epoch FROM psp_clients ORDER BY panel_id,user_id,id").Scan(&plan).Error; err != nil {
			t.Fatal(err)
		}
		covered := false
		for _, step := range plan {
			if strings.Contains(step.Detail, "COVERING INDEX "+name) {
				covered = true
			}
			if strings.Contains(strings.ToUpper(step.Detail), "TEMP B-TREE") {
				t.Fatal("fleet accounting still sorts temporary rows")
			}
		}
		if !covered {
			t.Fatal("SQLite fleet read did not use the covering index")
		}
	}
}

func TestFleetAccountingIndexUpgradePreservesExistingRowsAndCurrentWrites(t *testing.T) {
	db, err := openIsolatedTestDB(t)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if sqlDB, err := db.DB(); err == nil {
			_ = sqlDB.Close()
		}
	})
	if err := EnsureSchema(db); err != nil {
		t.Fatal(err)
	}
	const name = "idx_psp_client_fleet_accounting"
	if err := db.Migrator().DropIndex(&pspClientRow{}, name); err != nil {
		t.Fatal(err)
	}
	r := &pspClientRepo{db: db}
	rows := []pspClientRow{
		{ID: 81, UserID: 7, PanelID: 3, Email: "index-existing-a", UUID: "preserved-uuid", Password: "preserved-password", LastRawUpBytes: 19, LastRawDownBytes: 31, LastCounterEpoch: 1<<62 + 9},
		{ID: 82, UserID: 7, PanelID: 3, Email: "index-existing-b", DesiredMinted: true},
	}
	if err := db.Create(&rows).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&pspClientRow{}).Where("id = ?", 82).Updates(map[string]any{"last_raw_up_bytes": nil, "last_raw_down_bytes": nil, "last_counter_epoch": nil}).Error; err != nil {
		t.Fatal(err)
	}
	before, err := r.ListAll(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if err := EnsureSchema(db); err != nil {
		t.Fatal(err)
	}
	if !db.Migrator().HasIndex(&pspClientRow{}, name) {
		t.Fatal("upgrade did not create the accounting index")
	}
	after, err := r.ListAll(t.Context())
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatal("index upgrade changed existing definitions or baselines")
	}
	projected, err := r.ListFleetAccounting(t.Context())
	if err != nil || len(projected) != 2 || projected[0].LastCounterEpoch != rows[0].LastCounterEpoch || projected[1].LastCounterEpoch != 0 || projected[1].LastRawUpBytes != 0 {
		t.Fatal("indexed current read changed wide or NULL baselines")
	}
	if err := db.Model(&pspClientRow{}).Where("id = ?", 81).Updates(map[string]any{"last_raw_up_bytes": 999, "last_counter_epoch": 23}).Error; err != nil {
		t.Fatal(err)
	}
	updated, err := r.ListFleetAccounting(t.Context())
	if err != nil || len(updated) != 2 || updated[0].LastRawUpBytes != 999 || updated[0].LastCounterEpoch != 23 {
		t.Fatal("indexed read missed current counter update")
	}
}
