package rollup

import (
	"database/sql"
	"strings"
	"testing"

	sqlitedriver "github.com/glebarez/sqlite"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

func dryRunOnConflictSQL(t *testing.T, db *gorm.DB, oc clause.OnConflict) string {
	t.Helper()
	rows := []map[string]any{{"user_id": 1, "bucket_start": "2026-01-01 00:00:00", "up_bytes": 1, "down_bytes": 2, "total_bytes": 3}}
	return db.ToSQL(func(tx *gorm.DB) *gorm.DB {
		return tx.Table("traffic_snapshots_hourly").Clauses(oc).Create(rows)
	})
}

// TestOnConflictClause_KeepActionPerDialect pins the v3.9.0-beta.6 fix: on the
// keep-on-conflict (insert-once) path, MySQL must NOT emit an empty "ON DUPLICATE
// KEY UPDATE" — that's invalid SQL (Error 1064 "near ”", observed wrecking the
// hourly traffic rollup on a production MySQL deployment) — while SQLite/Postgres
// keep the native DO NOTHING. SQL is generated offline via GORM DryRun, so no live
// MySQL is required.
func TestOnConflictClause_KeepActionPerDialect(t *testing.T) {
	cols := []string{"user_id", "bucket_start"}

	// MySQL — offline DryRun (lazy conn + skip version query + no ping = no dial).
	mconn, _ := sql.Open("mysql", "u:p@tcp(127.0.0.1:3306)/d")
	mdb, err := gorm.Open(mysql.New(mysql.Config{Conn: mconn, SkipInitializeWithVersion: true}),
		&gorm.Config{DryRun: true, DisableAutomaticPing: true})
	if err != nil {
		t.Fatalf("mysql dialector open: %v", err)
	}

	keepSQL := dryRunOnConflictSQL(t, mdb, onConflictClause("mysql", cols, true))
	i := strings.Index(keepSQL, "ON DUPLICATE KEY UPDATE")
	if i < 0 {
		t.Fatalf("MySQL keep: missing ON DUPLICATE KEY UPDATE: %q", keepSQL)
	}
	if strings.TrimSpace(keepSQL[i+len("ON DUPLICATE KEY UPDATE"):]) == "" {
		t.Fatalf("MySQL keep: EMPTY ON DUPLICATE KEY UPDATE — this is the Error 1064 bug: %q", keepSQL)
	}

	overwriteSQL := dryRunOnConflictSQL(t, mdb, onConflictClause("mysql", cols, false))
	if !strings.Contains(overwriteSQL, "ON DUPLICATE KEY UPDATE") || !strings.Contains(overwriteSQL, "up_bytes") {
		t.Fatalf("MySQL overwrite: expected counter assignments, got: %q", overwriteSQL)
	}

	// SQLite — keep must stay a native DO NOTHING (behavior unchanged by the fix).
	sdb, err := gorm.Open(sqlitedriver.Open(":memory:"), &gorm.Config{DryRun: true})
	if err != nil {
		t.Fatalf("sqlite open: %v", err)
	}
	liteSQL := dryRunOnConflictSQL(t, sdb, onConflictClause("sqlite", cols, true))
	if !strings.Contains(liteSQL, "DO NOTHING") {
		t.Fatalf("SQLite keep: expected DO NOTHING, got: %q", liteSQL)
	}
}
