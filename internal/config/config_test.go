package config

import (
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	gomysql "github.com/go-sql-driver/mysql"
)

// Regression (M8 / low54): the discrete MySQL block must assemble a DSN that
// round-trips through the driver's own parser with the correct address and DB
// name. The old raw fmt.Sprintf mis-encoded an IPv6 host literal (::1 →
// "[::1:3306]:3306") and broke on a '/'-containing DB name.
func TestMySQLDiscreteBlockHandlesIPv6AndSlashDBName(t *testing.T) {
	cfg := Config{MySQL: MySQLConfig{
		Host: "::1", Port: 3306, User: "psp", Password: "pw:secret", Database: "db/with/slash",
	}}
	cfg.applyDefaults()

	if got := cfg.DBKind(); got != "mysql" {
		t.Fatalf("DBKind() = %q, want mysql", got)
	}
	dsn := cfg.DBDSN()
	parsed, err := gomysql.ParseDSN(dsn)
	if err != nil {
		t.Fatalf("driver cannot parse assembled DSN %q: %v", dsn, err)
	}
	if parsed.Addr != "[::1]:3306" {
		t.Fatalf("IPv6 addr = %q, want [::1]:3306 (raw Sprintf produced [::1:3306]:3306)", parsed.Addr)
	}
	if parsed.DBName != "db/with/slash" {
		t.Fatalf("DBName = %q, want db/with/slash (must survive '/')", parsed.DBName)
	}
	if parsed.User != "psp" || parsed.Passwd != "pw:secret" {
		t.Fatalf("creds mangled: user=%q passwd=%q", parsed.User, parsed.Passwd)
	}
	if !parsed.ParseTime {
		t.Fatalf("default params should set parseTime=true; got DSN %q", dsn)
	}
}

// TestEncryptionKeyEnvAlias pins that PSP_SECRET_KEY_MATERIAL — the variable
// the deploy docs / boot WARN have always told operators to set — actually
// feeds the encryption key (as a fallback for PSP_ENCRYPTION_KEY). It used to
// be a no-op, so docs-following operators silently fell back to jwt_secret.
func TestEncryptionKeyEnvAlias(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte("jwt_secret: \""+strings.Repeat("a", 32)+"\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	t.Setenv("PSP_ENCRYPTION_KEY", "")
	t.Setenv("PSP_SECRET_KEY_MATERIAL", strings.Repeat("k", 32))
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.SecretKeyMaterial() != strings.Repeat("k", 32) {
		t.Errorf("PSP_SECRET_KEY_MATERIAL not honored: SecretKeyMaterial()=%q", cfg.SecretKeyMaterial())
	}

	// PSP_ENCRYPTION_KEY still wins when both are set.
	t.Setenv("PSP_ENCRYPTION_KEY", strings.Repeat("e", 32))
	cfg2, err := Load(path)
	if err != nil {
		t.Fatalf("load2: %v", err)
	}
	if cfg2.SecretKeyMaterial() != strings.Repeat("e", 32) {
		t.Errorf("PSP_ENCRYPTION_KEY should win over the alias: got %q", cfg2.SecretKeyMaterial())
	}
}

func TestSecurityWarnings(t *testing.T) {
	healthy := Config{JWTSecret: strings.Repeat("a", 32), EncryptionKey: strings.Repeat("b", 32)}
	if w := healthy.SecurityWarnings(); len(w) != 0 {
		t.Fatalf("healthy config produced warnings: %v", w)
	}
	coupled := Config{JWTSecret: strings.Repeat("a", 32)}
	if w := coupled.SecurityWarnings(); len(w) != 1 || !strings.Contains(w[0], "encryption_key is empty") {
		t.Fatalf("want one coupled-key warning, got %v", w)
	}
	weak := Config{JWTSecret: "short", EncryptionKey: "x"}
	if w := weak.SecurityWarnings(); len(w) != 2 {
		t.Fatalf("want 2 weak-key warnings, got %v", w)
	}
}

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
