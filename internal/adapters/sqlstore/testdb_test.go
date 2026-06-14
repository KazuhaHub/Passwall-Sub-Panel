package sqlstore

import (
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"gorm.io/gorm"
)

// openTestDB opens a fresh, isolated database for one test and is the single
// seam the whole mysql-adapter suite uses to get a DB. With no env config — the
// default, including a plain local `go test` — it is an in-process SQLite temp
// file (zero setup). In CI the cross-dialect matrix sets:
//
//	PSP_TEST_DB_KIND = sqlite | postgres | mysql
//	PSP_TEST_DB_DSN  = connection string for the shared server (postgres/mysql)
//
// against a service container, and each test then gets its OWN schema
// (Postgres) / database (MySQL) on that server so the suite stays isolated even
// though it shares one server. Returns (db, error) so it is a drop-in for the
// previous Open("sqlite", …) the tests used.
//
// NOTE: the postgres/mysql branches are exercised only under the CI matrix —
// they cannot be run locally without those servers. The SQLite branch is the
// one covered by `go test` everywhere else.
func openTestDB(t *testing.T) (*gorm.DB, error) {
	t.Helper()
	switch kind := os.Getenv("PSP_TEST_DB_KIND"); kind {
	case "", "sqlite":
		return Open("sqlite", filepath.Join(t.TempDir(), "panel.db"))
	case "postgres":
		return openIsolatedPostgresTestDB(t)
	case "mysql":
		return openIsolatedMySQLTestDB(t)
	default:
		t.Fatalf("unknown PSP_TEST_DB_KIND %q (want sqlite|postgres|mysql)", kind)
		return nil, nil
	}
}

// testDBSeq disambiguates schema/database names within one process; os.Getpid()
// disambiguates across the parallel package test binaries `go test ./...` runs.
var testDBSeq atomic.Int64

func uniqueTestNamespace() string {
	return fmt.Sprintf("psptest_%d_%d", os.Getpid(), testDBSeq.Add(1))
}

// openIsolatedPostgresTestDB creates a unique DATABASE on the shared Postgres
// and returns a connection scoped to it; dropped on cleanup. This mirrors the
// MySQL path (a fresh database per test) instead of a schema + search_path,
// which avoids depending on the driver honouring search_path in the DSN.
// PSP_TEST_DB_DSN is a libpq/pgx URL pointing at an existing maintenance
// database, e.g. postgres://psp:psp@localhost:5432/psptest?sslmode=disable
func openIsolatedPostgresTestDB(t *testing.T) (*gorm.DB, error) {
	base := os.Getenv("PSP_TEST_DB_DSN")
	dbName := uniqueTestNamespace()
	admin, err := Open("postgres", base)
	if err != nil {
		return nil, err
	}
	// CREATE DATABASE can't run inside a transaction; Exec issues it directly.
	if err := admin.Exec(`CREATE DATABASE "` + dbName + `"`).Error; err != nil {
		closeGormDB(admin)
		return nil, err
	}
	t.Cleanup(func() {
		// FORCE (PG13+) terminates any lingering pooled connections so the drop
		// can't fail with "database is being accessed by other users".
		_ = admin.Exec(`DROP DATABASE IF EXISTS "` + dbName + `" WITH (FORCE)`).Error
		closeGormDB(admin)
	})
	return Open("postgres", swapPostgresDBName(base, dbName))
}

// swapPostgresDBName rewrites the database segment of a postgres:// URL DSN.
func swapPostgresDBName(dsn, dbName string) string {
	if u, err := url.Parse(dsn); err == nil && u.Scheme != "" {
		u.Path = "/" + dbName
		return u.String()
	}
	return dsn
}

// openIsolatedMySQLTestDB creates a unique database on the shared MySQL and
// returns a connection scoped to it; dropped on cleanup. PSP_TEST_DB_DSN must
// carry a {schema} placeholder where the database name goes, e.g.
// psp:psp@tcp(localhost:3306)/{schema}?parseTime=true&multiStatements=true
func openIsolatedMySQLTestDB(t *testing.T) (*gorm.DB, error) {
	base := os.Getenv("PSP_TEST_DB_DSN")
	if !strings.Contains(base, "{schema}") {
		t.Fatalf("PSP_TEST_DB_DSN for mysql must contain a {schema} placeholder, got %q", base)
	}
	dbName := uniqueTestNamespace()
	// Admin connection selects no database (empty {schema}) so it can CREATE one.
	admin, err := Open("mysql", strings.Replace(base, "{schema}", "", 1))
	if err != nil {
		return nil, err
	}
	if err := admin.Exec("CREATE DATABASE `" + dbName + "`").Error; err != nil {
		closeGormDB(admin)
		return nil, err
	}
	t.Cleanup(func() {
		_ = admin.Exec("DROP DATABASE IF EXISTS `" + dbName + "`").Error
		closeGormDB(admin)
	})
	return Open("mysql", strings.Replace(base, "{schema}", dbName, 1))
}

func closeGormDB(db *gorm.DB) {
	if s, err := db.DB(); err == nil {
		_ = s.Close()
	}
}
