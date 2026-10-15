package sqlstore

import (
	"fmt"
	"strings"
	"testing"

	"gorm.io/gorm"
)

func TestRequiredInt64MatchesDriverScalarConversions(t *testing.T) {
	r, _ := inboundBatchFixture(t, 0)
	db, err := r.db.DB()
	if err != nil {
		t.Fatal(err)
	}
	for _, expression := range []string{
		"0", "-17", "9223372036854775807", "-9223372036854775808",
		"'9007199254740993'", "'9223372036854775808'", "'invalid-integer'", "NULL", "'1.5'",
	} {
		t.Run(expression, func(t *testing.T) {
			plain, scanned := int64(71), int64(71)
			plainErr := db.QueryRowContext(t.Context(), "SELECT "+expression).Scan(&plain)
			scanErr := db.QueryRowContext(t.Context(), "SELECT "+expression).Scan(requiredInt64{&scanned})
			if fmt.Sprint(plainErr) != fmt.Sprint(scanErr) || plain != scanned {
				t.Fatalf("scalar conversion differs: plain=%d/%v scanner=%d/%v", plain, plainErr, scanned, scanErr)
			}
		})
	}
}

func TestFleetAccountingRejectsNullRequiredIdentifiers(t *testing.T) {
	for _, column := range []string{"id", "user_id", "panel_id"} {
		t.Run(column, func(t *testing.T) {
			r, _ := inboundBatchFixture(t, 0)
			r.fleetCapacity.Store(601)
			db, err := r.db.DB()
			if err != nil {
				t.Fatal(err)
			}
			before := db.Stats().InUse
			columns := []string{"1 AS id", "2 AS user_id", "3 AS panel_id", "4 AS last_raw_up_bytes", "5 AS last_raw_down_bytes", "6 AS last_counter_epoch"}
			for i, name := range []string{"id", "user_id", "panel_id"} {
				if name == column {
					columns[i] = "NULL AS " + name
				}
			}
			const hook = "fleet-null-required-identifier"
			if err := r.db.Callback().Row().Before("gorm:row").Register(hook, func(tx *gorm.DB) {
				tx.Statement.SQL.Reset()
				tx.Statement.SQL.WriteString("SELECT " + strings.Join(columns, ", "))
				tx.Statement.Vars = nil
			}); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = r.db.Callback().Row().Remove(hook) })
			if result, err := r.ListFleetAccounting(t.Context()); result != nil || err == nil || !strings.Contains(err.Error(), "converting NULL to int64") {
				t.Fatalf("NULL identifier returned accounting: %v, %v", result, err)
			}
			if db.Stats().InUse != before || r.fleetCapacity.Load() != 601 {
				t.Fatal("failed scan retained cursor or changed successful capacity")
			}
		})
	}
}
