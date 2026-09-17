package sqlstore

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"slices"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"gorm.io/gorm"
)

const reuseServerSchemaEnv = "PSP_TEST_DB_REUSE_SCHEMA"

// reusableSchemaDialector marks a connection whose schema was initialized by
// reusableServerTestPool. It deliberately changes no dialect behavior; the
// marker only lets ensureTestSchema avoid repeating the expensive metadata
// inspection and AutoMigrate pass for every ordinary repository test.
type reusableSchemaDialector struct{ gorm.Dialector }

// ensureTestSchema is the ordinary repository-test seam. Local SQLite tests
// and isolated migration tests still exercise the real EnsureSchema path. A
// reusable server database was fully migrated before it entered the pool, so
// rerunning that boot path would add cost without adding coverage.
func ensureTestSchema(db *gorm.DB) error {
	if _, ok := db.Dialector.(reusableSchemaDialector); ok {
		return nil
	}
	return EnsureSchema(db)
}

// openTestDB opens a data-isolated database for one ordinary repository test.
// With no env config — including a plain local `go test` — it is a fresh,
// in-process SQLite database. Cross-dialect CI can set
// PSP_TEST_DB_REUSE_SCHEMA=true: MySQL/Postgres then reuse a fully migrated
// database while clearing every data table between tests. Tests that inspect
// or mutate schema use openIsolatedTestDB instead.
func openTestDB(t *testing.T) (*gorm.DB, error) {
	t.Helper()
	kind := os.Getenv("PSP_TEST_DB_KIND")
	if reuseServerTestSchema() && (kind == "mysql" || kind == "postgres") {
		return reusableServerTestDBs.open(t, kind, os.Getenv("PSP_TEST_DB_DSN"))
	}
	return openIsolatedTestDB(t)
}

func reuseServerTestSchema() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(reuseServerSchemaEnv))) {
	case "1", "true", "yes":
		return true
	default:
		return false
	}
}

// openIsolatedTestDB always creates a fresh namespace. Keep this path explicit
// in tests whose subject is initialization, migration, DDL failure recovery or
// an actually empty database; sharing a prepared schema would invalidate what
// those tests claim to prove. PostgreSQL uses a schema instead of a database:
// DROP DATABASE forces a checkpoint, which made the schema-heavy CI suite
// spend minutes syncing disposable data.
func openIsolatedTestDB(t *testing.T) (*gorm.DB, error) {
	t.Helper()
	var (
		db  *gorm.DB
		err error
	)
	switch kind := os.Getenv("PSP_TEST_DB_KIND"); kind {
	case "", "sqlite":
		db, err = Open("sqlite", "file:"+uniqueTestNamespace()+"?mode=memory&cache=shared")
	case "postgres":
		db, err = openIsolatedPostgresTestDB(t)
	case "mysql":
		db, err = openIsolatedMySQLTestDB(t)
	default:
		t.Fatalf("unknown PSP_TEST_DB_KIND %q (want sqlite|postgres|mysql)", kind)
		return nil, nil
	}
	if err == nil {
		// Registered after server-side drop cleanup, so LIFO closes this pool
		// before DROP DATABASE. Duplicate caller cleanups are harmless.
		t.Cleanup(func() { closeGormDB(db) })
	}
	return db, err
}

// testDBSeq disambiguates database names within one process; os.Getpid()
// disambiguates concurrent package test binaries.
var testDBSeq atomic.Int64

func uniqueTestNamespace() string {
	return fmt.Sprintf("psptest_%d_%d", os.Getpid(), testDBSeq.Add(1))
}

func openIsolatedPostgresTestDB(t *testing.T) (*gorm.DB, error) {
	t.Helper()
	base := os.Getenv("PSP_TEST_DB_DSN")
	schemaName := uniqueTestNamespace()
	admin, err := Open("postgres", base)
	if err != nil {
		return nil, err
	}
	if err := admin.Exec(`CREATE SCHEMA "` + schemaName + `"`).Error; err != nil {
		closeGormDB(admin)
		return nil, err
	}
	closeGormDB(admin)
	t.Cleanup(func() {
		admin, err := Open("postgres", base)
		if err != nil {
			t.Errorf("open PostgreSQL schema cleanup connection: %v", err)
			return
		}
		defer closeGormDB(admin)
		if err := admin.Exec(`DROP SCHEMA IF EXISTS "` + schemaName + `" CASCADE`).Error; err != nil {
			t.Errorf("drop PostgreSQL test schema %s: %v", schemaName, err)
		}
	})
	return Open("postgres", postgresDSNWithSearchPath(base, schemaName))
}

// openDatabaseIsolatedTestDB is reserved for tests that intentionally switch
// between public and non-public PostgreSQL schemas. Ordinary migration tests
// should use openIsolatedTestDB so cleanup does not force a server checkpoint.
func openDatabaseIsolatedTestDB(t *testing.T) (*gorm.DB, error) {
	t.Helper()
	if os.Getenv("PSP_TEST_DB_KIND") != "postgres" {
		return openIsolatedTestDB(t)
	}
	base := os.Getenv("PSP_TEST_DB_DSN")
	dbName := uniqueTestNamespace()
	dsn, err := createServerTestDatabase("postgres", base, dbName)
	if err != nil {
		return nil, err
	}
	t.Cleanup(func() { _ = dropServerTestDatabase("postgres", base, dbName) })
	db, err := Open("postgres", dsn)
	if err == nil {
		t.Cleanup(func() { closeGormDB(db) })
	}
	return db, err
}

// swapPostgresDBName rewrites the database segment of a postgres:// URL DSN.
func swapPostgresDBName(dsn, dbName string) string {
	if u, err := url.Parse(dsn); err == nil && u.Scheme != "" {
		u.Path = "/" + dbName
		return u.String()
	}
	return dsn
}

func postgresDSNWithSearchPath(dsn, schemaName string) string {
	if u, err := url.Parse(dsn); err == nil && u.Scheme != "" {
		query := u.Query()
		query.Set("search_path", schemaName)
		u.RawQuery = query.Encode()
		return u.String()
	}
	return strings.TrimSpace(dsn) + " search_path=" + schemaName
}

func openIsolatedMySQLTestDB(t *testing.T) (*gorm.DB, error) {
	t.Helper()
	base := os.Getenv("PSP_TEST_DB_DSN")
	dbName := uniqueTestNamespace()
	dsn, err := createServerTestDatabase("mysql", base, dbName)
	if err != nil {
		return nil, err
	}
	t.Cleanup(func() { _ = dropServerTestDatabase("mysql", base, dbName) })
	return Open("mysql", dsn)
}

func createServerTestDatabase(kind, base, dbName string) (string, error) {
	switch kind {
	case "postgres":
		admin, err := Open("postgres", base)
		if err != nil {
			return "", err
		}
		defer closeGormDB(admin)
		if err := admin.Exec(`CREATE DATABASE "` + dbName + `"`).Error; err != nil {
			return "", err
		}
		return swapPostgresDBName(base, dbName), nil
	case "mysql":
		if !strings.Contains(base, "{schema}") {
			return "", fmt.Errorf("PSP_TEST_DB_DSN for mysql must contain a {schema} placeholder")
		}
		admin, err := Open("mysql", strings.Replace(base, "{schema}", "", 1))
		if err != nil {
			return "", err
		}
		defer closeGormDB(admin)
		if err := admin.Exec("CREATE DATABASE `" + dbName + "`").Error; err != nil {
			return "", err
		}
		return strings.Replace(base, "{schema}", dbName, 1), nil
	default:
		return "", fmt.Errorf("reusable test database does not support %q", kind)
	}
}

func dropServerTestDatabase(kind, base, dbName string) error {
	switch kind {
	case "postgres":
		admin, err := Open("postgres", base)
		if err != nil {
			return err
		}
		defer closeGormDB(admin)
		return admin.Exec(`DROP DATABASE IF EXISTS "` + dbName + `" WITH (FORCE)`).Error
	case "mysql":
		if !strings.Contains(base, "{schema}") {
			return fmt.Errorf("PSP_TEST_DB_DSN for mysql must contain a {schema} placeholder")
		}
		admin, err := Open("mysql", strings.Replace(base, "{schema}", "", 1))
		if err != nil {
			return err
		}
		defer closeGormDB(admin)
		return admin.Exec("DROP DATABASE IF EXISTS `" + dbName + "`").Error
	default:
		return fmt.Errorf("reusable test database does not support %q", kind)
	}
}

type reusableServerTestDatabase struct {
	kind      string
	baseDSN   string
	name      string
	dsn       string
	tables    []string
	discarded bool
}

// reusableServerTestPool is a lease pool rather than one global connection.
// Today's package has no t.Parallel calls, so CI creates one database. If a
// future test becomes parallel or nested, it receives another prepared
// database instead of racing the current test's cleanup.
type reusableServerTestPool struct {
	mu   sync.Mutex
	idle []*reusableServerTestDatabase
	all  []*reusableServerTestDatabase
}

var reusableServerTestDBs reusableServerTestPool

func (p *reusableServerTestPool) open(t *testing.T, kind, base string) (*gorm.DB, error) {
	t.Helper()
	lease, err := p.acquire(kind, base)
	if err != nil {
		return nil, err
	}
	db, err := Open(kind, lease.dsn)
	if err != nil {
		p.release(lease)
		return nil, err
	}
	db.Dialector = reusableSchemaDialector{Dialector: db.Dialector}
	t.Cleanup(func() {
		// Callers historically close their own pool too. Reset through a new
		// connection so those duplicate cleanups cannot prevent isolation.
		closeGormDB(db)
		if err := resetReusableServerTestDatabase(lease); err != nil {
			lease.discarded = true
			t.Errorf("reset reusable %s test database: %v", kind, err)
			return
		}
		p.release(lease)
	})
	return db, nil
}

func (p *reusableServerTestPool) acquire(kind, base string) (*reusableServerTestDatabase, error) {
	p.mu.Lock()
	for i := len(p.idle) - 1; i >= 0; i-- {
		candidate := p.idle[i]
		if candidate.kind == kind && candidate.baseDSN == base {
			p.idle = append(p.idle[:i], p.idle[i+1:]...)
			p.mu.Unlock()
			return candidate, nil
		}
	}
	p.mu.Unlock()

	created, err := newReusableServerTestDatabase(kind, base)
	if err != nil {
		return nil, err
	}
	p.mu.Lock()
	p.all = append(p.all, created)
	p.mu.Unlock()
	return created, nil
}

func (p *reusableServerTestPool) release(db *reusableServerTestDatabase) {
	if db.discarded {
		return
	}
	p.mu.Lock()
	p.idle = append(p.idle, db)
	p.mu.Unlock()
}

func (p *reusableServerTestPool) close() error {
	p.mu.Lock()
	all := append([]*reusableServerTestDatabase(nil), p.all...)
	p.idle = nil
	p.all = nil
	p.mu.Unlock()
	var failures []string
	for _, db := range all {
		if err := dropServerTestDatabase(db.kind, db.baseDSN, db.name); err != nil {
			failures = append(failures, fmt.Sprintf("%s/%s: %v", db.kind, db.name, err))
		}
	}
	if len(failures) > 0 {
		return fmt.Errorf("drop reusable test databases: %s", strings.Join(failures, "; "))
	}
	return nil
}

func newReusableServerTestDatabase(kind, base string) (*reusableServerTestDatabase, error) {
	dbName := uniqueTestNamespace()
	dsn, err := createServerTestDatabase(kind, base, dbName)
	if err != nil {
		return nil, err
	}
	keep := false
	defer func() {
		if !keep {
			_ = dropServerTestDatabase(kind, base, dbName)
		}
	}()
	db, err := Open(kind, dsn)
	if err != nil {
		return nil, err
	}
	defer closeGormDB(db)
	if err := EnsureSchema(db); err != nil {
		return nil, fmt.Errorf("initialize reusable %s schema: %w", kind, err)
	}
	tables, err := db.Migrator().GetTables()
	if err != nil {
		return nil, fmt.Errorf("list reusable %s schema tables: %w", kind, err)
	}
	sort.Strings(tables)
	keep = true
	return &reusableServerTestDatabase{
		kind: kind, baseDSN: base, name: dbName, dsn: dsn, tables: tables,
	}, nil
}

func resetReusableServerTestDatabase(lease *reusableServerTestDatabase) error {
	db, err := Open(lease.kind, lease.dsn)
	if err != nil {
		return err
	}
	defer closeGormDB(db)
	tables, err := db.Migrator().GetTables()
	if err != nil {
		return err
	}
	sort.Strings(tables)
	if !slices.Equal(tables, lease.tables) {
		return fmt.Errorf("schema drift: tables=%v, want %v", tables, lease.tables)
	}

	switch lease.kind {
	case "mysql":
		err = clearMySQLTables(db, tables)
	case "postgres":
		err = truncatePostgresTables(db, tables)
	default:
		err = fmt.Errorf("unsupported reusable dialect %q", lease.kind)
	}
	if err != nil {
		return err
	}
	if err := seedBuiltinRoles(db); err != nil {
		return fmt.Errorf("restore builtin roles: %w", err)
	}
	return nil
}

func clearMySQLTables(db *gorm.DB, tables []string) (result error) {
	sqlDB, err := db.DB()
	if err != nil {
		return err
	}
	conn, err := sqlDB.Conn(context.Background())
	if err != nil {
		return err
	}
	defer conn.Close()
	if _, err := conn.ExecContext(context.Background(), "SET FOREIGN_KEY_CHECKS=0"); err != nil {
		return err
	}
	defer func() {
		if _, err := conn.ExecContext(context.Background(), "SET FOREIGN_KEY_CHECKS=1"); result == nil && err != nil {
			result = err
		}
	}()
	for _, table := range tables {
		if table == "schema_migrations" {
			continue
		}
		quoted := "`" + strings.ReplaceAll(table, "`", "``") + "`"
		// DELETE avoids one implicit-commit DDL operation per table. Repository
		// tests never depend on auto-increment counters restarting at one; they
		// use returned IDs or explicit domain IDs.
		if _, err := conn.ExecContext(context.Background(), "DELETE FROM "+quoted); err != nil {
			return fmt.Errorf("clear %s: %w", table, err)
		}
	}
	return nil
}

func truncatePostgresTables(db *gorm.DB, tables []string) error {
	quoted := make([]string, 0, len(tables))
	for _, table := range tables {
		if table == "schema_migrations" {
			continue
		}
		quoted = append(quoted, `"`+strings.ReplaceAll(table, `"`, `""`)+`"`)
	}
	if len(quoted) == 0 {
		return nil
	}
	return db.Exec("TRUNCATE TABLE " + strings.Join(quoted, ", ") + " RESTART IDENTITY CASCADE").Error
}

func closeGormDB(db *gorm.DB) {
	if s, err := db.DB(); err == nil {
		_ = s.Close()
	}
}

func TestMain(m *testing.M) {
	code := m.Run()
	if err := reusableServerTestDBs.close(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		if code == 0 {
			code = 1
		}
	}
	os.Exit(code)
}
