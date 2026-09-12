package sqlstore

import (
	"context"
	"database/sql"
	"strings"
	"testing"
	"time"

	mysqldriver "gorm.io/driver/mysql"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
	"gorm.io/gorm/migrator"
	"gorm.io/gorm/schema"
)

type schemaSQLRecorder struct {
	logger.Interface
	queries []string
}

func (recorder *schemaSQLRecorder) Trace(_ context.Context, _ time.Time, query func() (string, int64), _ error) {
	statement, _ := query()
	recorder.queries = append(recorder.queries, statement)
}

func TestMySQLPreservingSchemaMigratorBuildsTablesAndUniqueIndexesOffline(t *testing.T) {
	// Use the actual MySQL dialect and its optional migration interfaces,
	// without requiring a server or issuing any database reads/writes.
	connection, err := sql.Open("mysql", "test:test@tcp(127.0.0.1:1)/offline")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = connection.Close() })
	recorder := &schemaSQLRecorder{Interface: logger.Discard}
	db, err := gorm.Open(mysqldriver.New(mysqldriver.Config{
		Conn: connection, SkipInitializeWithVersion: true,
	}), &gorm.Config{DryRun: true, DisableAutomaticPing: true, Logger: recorder})
	if err != nil {
		t.Fatal(err)
	}
	original := db.Dialector
	migrationDB := db.Session(&gorm.Session{})
	migrationDB.Dialector = preservingMySQLSchemaDialect{db.Dialector}
	if err := migrationDB.Migrator().CreateTable(&userRow{}, &pspClientRow{}); err != nil {
		t.Fatal(err)
	}
	if err := migrationDB.Migrator().CreateIndex(&userRow{}, "idx_users_upn"); err != nil {
		t.Fatal(err)
	}
	queries := strings.Join(recorder.queries, "\n")
	for _, want := range []string{"CREATE TABLE `users`", "UNIQUE INDEX `idx_users_upn`", "CREATE TABLE `psp_clients`", "CREATE UNIQUE INDEX `idx_users_upn`"} {
		if !strings.Contains(queries, want) {
			t.Fatalf("preserving migrator did not retain expected MySQL DDL %q: %s", want, queries)
		}
	}
	if db.Dialector != original {
		t.Fatal("schema session changed the caller's dialect")
	}
	// A model field must not trigger a driver metadata read or redundant
	// index DROP. Unsupported column UNIQUE additions fail explicitly.
	if err := migrationDB.Migrator().MigrateColumnUnique(&userRow{}, &schema.Field{DBName: "uuid"}, nil); err != nil {
		t.Fatal(err)
	}
	statement := &gorm.Statement{DB: migrationDB}
	if err := statement.Parse(&userRow{}); err != nil {
		t.Fatal(err)
	}
	column := migrator.ColumnType{
		DataTypeValue: sql.NullString{String: "varchar", Valid: true},
		LengthValue:   sql.NullInt64{Int64: 36, Valid: true},
		NullableValue: sql.NullBool{Bool: false, Valid: true},
		UniqueValue:   sql.NullBool{Bool: true, Valid: true},
	}
	if err := migrationDB.Migrator().MigrateColumn(&userRow{}, statement.Schema.FieldsByDBName["uuid"], column); err != nil {
		t.Fatal(err)
	}
	if len(recorder.queries) != 3 {
		t.Fatal("column uniqueness migration emitted unexpected DDL")
	}
	if err := migrationDB.Migrator().MigrateColumnUnique(&userRow{}, &schema.Field{DBName: "uuid", Unique: true}, nil); err == nil {
		t.Fatal("unsupported column UNIQUE migration was silently skipped")
	}
}
