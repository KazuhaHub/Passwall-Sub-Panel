package config

import (
	"net/url"
	"path/filepath"
	"testing"
)

func TestDatabaseDefaultsToSQLiteWhenMySQLUnset(t *testing.T) {
	cfg := Config{DataDir: "./data"}
	cfg.applyDefaults()

	if got := cfg.DBKind(); got != "sqlite" {
		t.Fatalf("DBKind() = %q, want sqlite", got)
	}
	if got, want := cfg.DBDSN(), filepath.Join("./data", "panel.db"); got != want {
		t.Fatalf("DBDSN() = %q, want %q", got, want)
	}
}

func TestDatabaseUsesMySQLWhenDSNConfigured(t *testing.T) {
	cfg := Config{
		MySQL: MySQLConfig{
			DSN: "user:pass@tcp(127.0.0.1:3306)/passwall",
		},
	}
	cfg.applyDefaults()

	if got := cfg.DBKind(); got != "mysql" {
		t.Fatalf("DBKind() = %q, want mysql", got)
	}
	if got, want := cfg.DBDSN(), cfg.MySQL.DSN; got != want {
		t.Fatalf("DBDSN() = %q, want %q", got, want)
	}
}

func TestDatabaseUsesPostgresWhenURLDSNConfigured(t *testing.T) {
	for _, dsn := range []string{
		"postgres://psp:pw@127.0.0.1:5432/passwall?sslmode=disable",
		"postgresql://psp:pw@127.0.0.1:5432/passwall",
	} {
		cfg := Config{MySQL: MySQLConfig{DSN: dsn}}
		cfg.applyDefaults()

		if got := cfg.DBKind(); got != "postgres" {
			t.Fatalf("DBKind() = %q for %q, want postgres", got, dsn)
		}
		// The URL must pass through untouched — GORM's pgx driver consumes it.
		if got := cfg.DBDSN(); got != dsn {
			t.Fatalf("DBDSN() = %q, want %q (verbatim)", got, dsn)
		}
	}
}

// A keyword-form PG DSN placed in the mysql.dsn escape-hatch field is NOT
// auto-detected as postgres — only the postgres:// prefix is. Use the postgres
// block (or postgres.dsn) for keyword-form strings. This pins the documented
// limitation so a future "be clever" change is a conscious one.
func TestKeywordFormPostgresDSNInMySQLFieldIsNotAutoDetected(t *testing.T) {
	cfg := Config{MySQL: MySQLConfig{DSN: "host=127.0.0.1 user=psp dbname=passwall"}}
	cfg.applyDefaults()

	if got := cfg.DBKind(); got != "mysql" {
		t.Fatalf("DBKind() = %q, want mysql (keyword-form PG in mysql.dsn is not auto-detected)", got)
	}
}

func TestDatabaseUsesPostgresFromDiscreteBlock(t *testing.T) {
	cfg := Config{
		Postgres: PostgresConfig{
			Host:     "127.0.0.1",
			User:     "psp",
			Password: "secret",
			Database: "passwall",
		},
	}
	cfg.applyDefaults()

	if got := cfg.DBKind(); got != "postgres" {
		t.Fatalf("DBKind() = %q, want postgres", got)
	}
	// Default port 5432 and sslmode=disable are filled in by assembleDSN.
	want := "postgres://psp:secret@127.0.0.1:5432/passwall?sslmode=disable"
	if got := cfg.DBDSN(); got != want {
		t.Fatalf("DBDSN() = %q, want %q", got, want)
	}
}

// A password with URL-significant characters must survive percent-encoding so
// the assembled DSN stays parseable.
func TestPostgresDiscreteBlockEncodesSpecialPassword(t *testing.T) {
	cfg := Config{
		Postgres: PostgresConfig{
			Host:     "db.internal",
			Port:     6432,
			User:     "psp",
			Password: "p@ss/w:rd?",
			Database: "passwall",
			SSLMode:  "require",
		},
	}
	cfg.applyDefaults()

	dsn := cfg.DBDSN()
	u, err := url.Parse(dsn)
	if err != nil {
		t.Fatalf("assembled DSN does not parse: %v (dsn=%q)", err, dsn)
	}
	if pw, _ := u.User.Password(); pw != "p@ss/w:rd?" {
		t.Fatalf("decoded password = %q, want %q", pw, "p@ss/w:rd?")
	}
	if u.Host != "db.internal:6432" {
		t.Fatalf("host = %q, want db.internal:6432", u.Host)
	}
	if got := u.Query().Get("sslmode"); got != "require" {
		t.Fatalf("sslmode = %q, want require", got)
	}
}

// The raw postgres.dsn field overrides the discrete fields and is passed
// through verbatim (covers the libpq keyword form too).
func TestPostgresRawDSNOverridesDiscreteFields(t *testing.T) {
	raw := "host=db user=psp dbname=passwall sslmode=verify-full"
	cfg := Config{
		Postgres: PostgresConfig{
			DSN:  raw,
			Host: "ignored",
		},
	}
	cfg.applyDefaults()

	if got := cfg.DBKind(); got != "postgres" {
		t.Fatalf("DBKind() = %q, want postgres", got)
	}
	if got := cfg.DBDSN(); got != raw {
		t.Fatalf("DBDSN() = %q, want %q (verbatim)", got, raw)
	}
}

func TestDatabaseUsesExplicitSQLiteDSN(t *testing.T) {
	cfg := Config{
		MySQL: MySQLConfig{
			DSN: "sqlite:./custom/panel.db",
		},
	}
	cfg.applyDefaults()

	if got := cfg.DBKind(); got != "sqlite" {
		t.Fatalf("DBKind() = %q, want sqlite", got)
	}
	if got, want := cfg.DBDSN(), "./custom/panel.db"; got != want {
		t.Fatalf("DBDSN() = %q, want %q", got, want)
	}
}
