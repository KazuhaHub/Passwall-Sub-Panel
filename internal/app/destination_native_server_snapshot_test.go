//go:build destination_acceptance && linux

package app

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/KazuhaHub/passwall-sub-panel/internal/config"
)

func nativeDestinationFixtureConfig(t *testing.T, base config.Config) config.Config {
	t.Helper()
	if os.Geteuid() != 0 || os.Getenv("GITHUB_ACTIONS") != "true" || os.Getenv("RUNNER_ENVIRONMENT") != "github-hosted" || os.Getenv("GITHUB_REPOSITORY") != "KazuhaHub/Passwall-Sub-Panel" || os.Getenv("PSP_NATIVE_DB_DSN") != "" {
		t.Fatal("native database fixture requires the dedicated disposable hosted job")
	}
	cfg, err := nativeDestinationDatabaseConfig(os.Getenv("PSP_NATIVE_DB_KIND"), base)
	nativeMust(t, err, "select owned native database")
	return cfg
}

func nativeOpenServerDestinationDatabase(t *testing.T, cfg config.Config) *sql.DB {
	t.Helper()
	want := nativeDestinationFixtureConfig(t, config.Config{})
	if cfg.DBKind() != want.DBKind() || cfg.DBDSN() != want.DBDSN() || cfg.DBKind() == "sqlite" {
		t.Fatal("server snapshot escaped the owned database")
	}
	driver := cfg.DBKind()
	if driver == "postgres" {
		driver = "pgx"
	}
	db, err := sql.Open(driver, cfg.DBDSN())
	nativeMust(t, err, "open owned server snapshot connection")
	db.SetMaxOpenConns(2)
	t.Cleanup(func() { nativeMust(t, db.Close(), "close owned server snapshot connection") })
	return db
}

func nativeServerDestinationIdentity(t *testing.T, db *sql.DB, kind string) [32]byte {
	t.Helper()
	var version, database, identity, schema string
	switch kind {
	case "mysql":
		nativeMust(t, db.QueryRowContext(t.Context(), "SELECT VERSION(), DATABASE(), @@server_uuid").Scan(&version, &database, &identity), "bind actual MySQL database identity")
		if !strings.HasPrefix(version, "8.") || identity == "" {
			t.Fatal("unexpected owned MySQL server identity")
		}
	case "postgres":
		nativeMust(t, db.QueryRowContext(t.Context(), "SELECT version(), current_database(), current_schema(), (SELECT oid::text FROM pg_database WHERE datname=current_database())").Scan(&version, &database, &schema, &identity), "bind actual PostgreSQL database identity")
		if !strings.HasPrefix(version, "PostgreSQL 16.") || schema != "public" || identity == "" {
			t.Fatal("unexpected owned PostgreSQL server identity")
		}
	default:
		t.Fatal("unsupported server snapshot identity")
	}
	if database != "psp_native_acceptance" {
		t.Fatal("snapshot selected a foreign database")
	}
	data, err := json.Marshal([]string{kind, version, database, schema, identity})
	nativeMust(t, err, "encode private server identity")
	return sha256.Sum256(data)
}

func nativeBindLegacyPanelDatabase(t *testing.T, f nativeLegacyPanelFixture) func(*testing.T) {
	t.Helper()
	if f.cfg.DBKind() == "sqlite" {
		before, err := os.Stat(f.panelDB)
		nativeMust(t, err, "bind original panel database")
		return func(test *testing.T) {
			after, err := os.Stat(f.panelDB)
			nativeMust(test, err, "read reupgraded panel database")
			if !os.SameFile(before, after) {
				test.Fatal("panel transition replaced an original database or identity")
			}
		}
	}
	db := nativeOpenServerDestinationDatabase(t, f.cfg)
	before := nativeServerDestinationIdentity(t, db, f.cfg.DBKind())
	return func(test *testing.T) {
		if nativeServerDestinationIdentity(test, db, f.cfg.DBKind()) != before {
			test.Fatal("panel transition replaced an original database or identity")
		}
	}
}

func nativeLegacyPanelDestinationSnapshot(t *testing.T, f nativeLegacyPanelFixture) map[string][32]byte {
	t.Helper()
	if f.cfg.DBKind() == "sqlite" {
		return nativePanelDestinationSnapshot(t, f.panelDB)
	}
	return nativeServerDestinationSnapshot(t, nativeOpenServerDestinationDatabase(t, f.cfg), f.cfg.DBKind())
}

func nativeVerifyLegacyDestinationSchema(t *testing.T, cfg config.Config) {
	t.Helper()
	if cfg.DBKind() != "sqlite" {
		if len(nativeServerDestinationSnapshot(t, nativeOpenServerDestinationDatabase(t, cfg), cfg.DBKind())) != 0 {
			t.Fatal("legacy database already contained destination schema")
		}
		return
	}
	db, err := sql.Open("sqlite", "file:"+filepath.Join(cfg.DataDir, "panel.db")+"?mode=ro")
	nativeMust(t, err, "open legacy schema read-only")
	var tables int
	err = db.QueryRowContext(t.Context(), "SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name GLOB 'dest_*'").Scan(&tables)
	closeErr := db.Close()
	nativeMust(t, err, "read actual pre-upgrade schema")
	nativeMust(t, closeErr, "close read-only legacy schema")
	if tables != 0 {
		t.Fatal("legacy database already contained destination schema")
	}
}

func nativeServerDestinationSnapshot(t *testing.T, db *sql.DB, kind string) map[string][32]byte {
	t.Helper()
	nativeServerDestinationIdentity(t, db, kind)
	tx, err := db.BeginTx(t.Context(), &sql.TxOptions{ReadOnly: true, Isolation: sql.LevelRepeatableRead})
	nativeMust(t, err, "bind read-only server destination snapshot")
	defer tx.Rollback()
	result := nativeServerDestinationSnapshotTx(t, tx, kind)
	nativeMust(t, tx.Commit(), "finish read-only server destination snapshot")
	return result
}

// Hash schema and complete row multisets privately. This reader performs no
// writes, including when the rollback-only regression supplies its own tx.
func nativeServerDestinationSnapshotTx(t *testing.T, tx *sql.Tx, kind string) map[string][32]byte {
	t.Helper()
	if kind != "mysql" && kind != "postgres" {
		t.Fatal("unsupported server snapshot dialect")
	}
	query := "SELECT table_name FROM information_schema.tables WHERE table_schema='public' AND table_type='BASE TABLE' AND LEFT(table_name,5)='dest_' ORDER BY table_name"
	if kind == "mysql" {
		query = "SELECT table_name FROM information_schema.tables WHERE table_schema=DATABASE() AND table_type='BASE TABLE' AND LEFT(table_name,5)='dest_' ORDER BY table_name"
	}
	tableRows, err := tx.QueryContext(t.Context(), query)
	nativeMust(t, err, "read owned destination table inventory")
	defer tableRows.Close()
	var names []string
	for tableRows.Next() {
		var name string
		nativeMust(t, tableRows.Scan(&name), "read owned destination table name")
		if len(names) >= 64 {
			t.Fatal("owned destination schema exceeded its bound")
		}
		names = append(names, name)
	}
	nativeMust(t, tableRows.Err(), "complete owned destination table inventory")
	nativeMust(t, tableRows.Close(), "close owned destination table inventory")
	result := make(map[string][32]byte, len(names))
	for _, name := range names {
		identifier, err := nativeDestinationTableIdentifier(kind, name)
		nativeMust(t, err, "validate owned destination snapshot table")
		var schema []any
		if kind == "mysql" {
			var actual, ddl string
			nativeMust(t, tx.QueryRowContext(t.Context(), "SHOW CREATE TABLE "+identifier).Scan(&actual, &ddl), "read private complete MySQL definition")
			if actual != name {
				t.Fatal("schema lookup changed its owned table")
			}
			schema = []any{ddl}
		} else {
			for _, statement := range []string{
				"SELECT c.oid::text,c.relkind,c.relpersistence FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname='public' AND c.relname=$1",
				"SELECT column_name,ordinal_position,column_default,is_nullable,data_type,udt_schema,udt_name,character_maximum_length,numeric_precision,numeric_scale,datetime_precision,is_identity,identity_generation,identity_start,identity_increment,identity_maximum,identity_minimum,identity_cycle,is_generated,generation_expression,collation_schema,collation_name,domain_schema,domain_name FROM information_schema.columns WHERE table_schema='public' AND table_name=$1 ORDER BY ordinal_position",
				"SELECT conname,contype,condeferrable,condeferred,convalidated,pg_get_constraintdef(oid,false) FROM pg_constraint WHERE conrelid=('public.' || $1)::regclass ORDER BY conname",
				"SELECT pg_get_indexdef(indexrelid),indisvalid,indisready,indisunique FROM pg_index WHERE indrelid=('public.' || $1)::regclass ORDER BY indexrelid",
				"SELECT sequencename,data_type,start_value,min_value,max_value,increment_by,cycle,cache_size,last_value FROM pg_sequences WHERE schemaname='public' AND sequencename IN (SELECT c.relname FROM pg_class c JOIN pg_depend d ON d.objid=c.oid WHERE d.classid='pg_class'::regclass AND d.refclassid='pg_class'::regclass AND d.refobjid=('public.' || $1)::regclass AND d.deptype IN ('a','i')) ORDER BY sequencename",
			} {
				columns, rows := nativeServerSnapshotRows(t, tx, statement, name)
				schema = append(schema, []any{columns, rows})
			}
		}
		columns, rows := nativeServerSnapshotRows(t, tx, "SELECT * FROM "+identifier)
		data, err := json.Marshal([]any{schema, columns, rows})
		nativeMust(t, err, "encode private complete server destination snapshot")
		result[name] = sha256.Sum256(data)
	}
	return result
}

func nativeServerSnapshotRows(t *testing.T, tx *sql.Tx, query string, args ...any) ([]string, []string) {
	t.Helper()
	rows, err := tx.QueryContext(t.Context(), query, args...)
	nativeMust(t, err, "read private server destination snapshot")
	defer rows.Close()
	columns, err := rows.Columns()
	nativeMust(t, err, "read private snapshot columns")
	encoded := make([]string, 0)
	for rows.Next() {
		values, pointers := make([]any, len(columns)), make([]any, len(columns))
		for i := range values {
			pointers[i] = &values[i]
		}
		nativeMust(t, rows.Scan(pointers...), "read private snapshot row")
		data, err := json.Marshal(values)
		nativeMust(t, err, "encode private snapshot row")
		if len(encoded) >= 10000 {
			t.Fatal("owned destination snapshot exceeded its bound")
		}
		encoded = append(encoded, string(data))
	}
	nativeMust(t, rows.Err(), "complete private snapshot rows")
	nativeMust(t, rows.Close(), "close private snapshot rows")
	sort.Strings(encoded)
	return columns, encoded
}

func TestNativeServerDestinationSnapshotDetectsChangedDataAndSchema(t *testing.T) {
	cfg := nativeDestinationFixtureConfig(t, config.Config{})
	if cfg.DBKind() == "sqlite" {
		t.Fatal("server snapshot regression requires the owned SQL cohort")
	}
	db := nativeOpenServerDestinationDatabase(t, cfg)
	before := nativeServerDestinationSnapshot(t, db, cfg.DBKind())
	if len(before) != 12 {
		t.Fatal("server snapshot regression requires the complete migrated schema")
	}
	tx, err := db.BeginTx(t.Context(), &sql.TxOptions{Isolation: sql.LevelRepeatableRead})
	nativeMust(t, err, "begin rollback-only snapshot regression")
	defer tx.Rollback()
	var id int64
	var name string
	nativeMust(t, tx.QueryRowContext(t.Context(), "SELECT id,name FROM dest_lists ORDER BY id LIMIT 1").Scan(&id, &name), "read owned snapshot fixture row")
	update := "UPDATE dest_lists SET name=? WHERE id=?"
	if cfg.DBKind() == "postgres" {
		update = "UPDATE dest_lists SET name=$1 WHERE id=$2"
	}
	_, err = tx.ExecContext(t.Context(), update, name+"-snapshot-probe", id)
	nativeMust(t, err, "mutate rollback-only private snapshot row")
	changed := nativeServerDestinationSnapshotTx(t, tx, cfg.DBKind())
	if changed["dest_lists"] == before["dest_lists"] {
		t.Fatal("server snapshot missed changed row data")
	}
	nativeMust(t, tx.Rollback(), "restore original snapshot row")
	if !reflect.DeepEqual(before, nativeServerDestinationSnapshot(t, db, cfg.DBKind())) {
		t.Fatal("rollback-only snapshot regression changed original data")
	}
	_, err = db.ExecContext(t.Context(), "CREATE INDEX native_snapshot_probe ON dest_lists(name)")
	nativeMust(t, err, "create owned schema-only snapshot probe")
	present := true
	drop := func(ctx context.Context) error {
		statement := "DROP INDEX native_snapshot_probe"
		if cfg.DBKind() == "mysql" {
			statement += " ON dest_lists"
		}
		_, err := db.ExecContext(ctx, statement)
		return err
	}
	t.Cleanup(func() {
		if present {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			nativeMust(t, drop(ctx), "remove owned snapshot probe")
		}
	})
	if nativeServerDestinationSnapshot(t, db, cfg.DBKind())["dest_lists"] == before["dest_lists"] {
		t.Fatal("server snapshot missed a schema-only index change")
	}
	nativeMust(t, drop(t.Context()), "remove owned schema-only snapshot probe")
	present = false
	if !reflect.DeepEqual(before, nativeServerDestinationSnapshot(t, db, cfg.DBKind())) {
		t.Fatal("schema-only snapshot regression changed original schema or data")
	}
}
