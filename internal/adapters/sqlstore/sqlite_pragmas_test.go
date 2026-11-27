package sqlstore

import (
	"database/sql"
	"path/filepath"
	"testing"

	_ "github.com/glebarez/go-sqlite"
)

func TestOpenSQLiteUsesWALAndBusyTimeout(t *testing.T) {
	path := filepath.Join(t.TempDir(), "panel.db")
	db, err := Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer closeGormDB(db)

	var mode string
	if err := db.Raw("PRAGMA journal_mode").Scan(&mode).Error; err != nil {
		t.Fatal(err)
	}
	if mode != "wal" {
		t.Errorf("journal_mode = %q, want wal", mode)
	}
	var sync int
	if err := db.Raw("PRAGMA synchronous").Scan(&sync).Error; err != nil {
		t.Fatal(err)
	}
	if sync != 1 { // 1 = NORMAL
		t.Errorf("synchronous = %d, want 1 (NORMAL)", sync)
	}
	var busy int
	if err := db.Raw("PRAGMA busy_timeout").Scan(&busy).Error; err != nil {
		t.Fatal(err)
	}
	if busy != 5000 {
		t.Errorf("busy_timeout = %d, want 5000", busy)
	}
}

// An install upgraded from a build that opened with the default rollback
// journal must convert in place on first open, keeping its data.
func TestOpenSQLiteConvertsExistingRollbackJournalDB(t *testing.T) {
	path := filepath.Join(t.TempDir(), "panel.db")
	legacy, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := legacy.Exec(`CREATE TABLE t (v TEXT); INSERT INTO t VALUES ('kept')`); err != nil {
		t.Fatal(err)
	}
	var mode string
	if err := legacy.QueryRow("PRAGMA journal_mode").Scan(&mode); err != nil || mode != "delete" {
		t.Fatalf("legacy journal_mode = %q, %v; want delete", mode, err)
	}
	legacy.Close()

	db, err := Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer closeGormDB(db)
	if err := db.Raw("PRAGMA journal_mode").Scan(&mode).Error; err != nil {
		t.Fatal(err)
	}
	if mode != "wal" {
		t.Errorf("journal_mode after open = %q, want wal", mode)
	}
	var v string
	if err := db.Raw("SELECT v FROM t").Scan(&v).Error; err != nil || v != "kept" {
		t.Errorf("row after conversion = %q, %v", v, err)
	}
}

// A caller-supplied pragma wins over the default (the test template relies on
// this), and in-memory DSNs are left alone: WAL does not apply to them.
func TestSQLiteDSNDefaults(t *testing.T) {
	cases := []struct{ in, want string }{
		{"/d/panel.db", "/d/panel.db?_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)&_pragma=busy_timeout(5000)"},
		{"/d/p.db?_pragma=journal_mode(MEMORY)&_pragma=synchronous(OFF)", "/d/p.db?_pragma=journal_mode(MEMORY)&_pragma=synchronous(OFF)&_pragma=busy_timeout(5000)"},
		{"file:x?mode=memory&cache=shared", "file:x?mode=memory&cache=shared"},
		{":memory:", ":memory:"},
	}
	for _, c := range cases {
		if got := sqliteDSN(c.in); got != c.want {
			t.Errorf("sqliteDSN(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
