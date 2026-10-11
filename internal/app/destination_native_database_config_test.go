package app

import (
	"errors"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"github.com/KazuhaHub/passwall-sub-panel/internal/config"
	"github.com/go-sql-driver/mysql"
)

// Server databases belong exclusively to the ephemeral hosted job. Never take
// a caller-supplied DSN or reuse an operator's existing database.
func nativeDestinationDatabaseConfig(kind string, base config.Config) (config.Config, error) {
	if base.DBKind() != "sqlite" || base.MySQL.DSN != "" || base.Postgres.DSN != "" {
		return config.Config{}, errors.New("native database base configuration is not isolated")
	}
	switch kind {
	case "", "sqlite":
	case "mysql":
		base.MySQL.DSN = "psp:fixture-native-database-password@tcp(127.0.0.1:3306)/psp_native_acceptance?parseTime=true&charset=utf8mb4&loc=UTC"
	case "postgres":
		base.Postgres.DSN = "postgres://psp:fixture-native-database-password@127.0.0.1:5432/psp_native_acceptance?sslmode=disable"
	default:
		return config.Config{}, errors.New("unknown native database selection")
	}
	return base, nil
}

// Only fixed read-only count assertions call this helper. Query parameters stay
// separate from SQL; PostgreSQL numbers the same existing placeholders.
func nativeDestinationCountSQL(kind, query string) string {
	if kind != "postgres" {
		return query
	}
	parts := strings.Split(query, "?")
	var sql strings.Builder
	sql.WriteString(parts[0])
	for i, part := range parts[1:] {
		sql.WriteByte('$')
		sql.WriteString(strconv.Itoa(i + 1))
		sql.WriteString(part)
	}
	return sql.String()
}

func TestNativeDatabaseConfigurationIsClosedAndPreservesOwnedDirectories(t *testing.T) {
	base := config.Config{ConfigDir: "owned-config", DataDir: "owned-data", JWTSecret: "fixture-jwt", EncryptionKey: "fixture-key"}
	for _, kind := range []string{"", "sqlite", "mysql", "postgres"} {
		cfg, err := nativeDestinationDatabaseConfig(kind, base)
		if err != nil || cfg.ConfigDir != base.ConfigDir || cfg.DataDir != base.DataDir || cfg.JWTSecret != base.JWTSecret || cfg.EncryptionKey != base.EncryptionKey {
			t.Fatal("native database selection changed the fixture identity")
		}
		expected := kind
		if expected == "" {
			expected = "sqlite"
		}
		if cfg.DBKind() != expected {
			t.Fatal("native database selection fell back to a different driver")
		}
		switch kind {
		case "mysql":
			dsn, err := mysql.ParseDSN(cfg.DBDSN())
			if err != nil || dsn.Net != "tcp" || dsn.Addr != "127.0.0.1:3306" || dsn.DBName != "psp_native_acceptance" || dsn.User != "psp" || !dsn.ParseTime {
				t.Fatal("MySQL escaped the dedicated fixture service")
			}
		case "postgres":
			dsn, err := url.Parse(cfg.DBDSN())
			if err != nil || dsn.Scheme != "postgres" || dsn.Host != "127.0.0.1:5432" || dsn.Path != "/psp_native_acceptance" || dsn.User.Username() != "psp" || dsn.Query().Get("sslmode") != "disable" {
				t.Fatal("PostgreSQL escaped the dedicated fixture service")
			}
		default:
			if cfg.MySQL.DSN != "" || cfg.Postgres.DSN != "" {
				t.Fatal("SQLite fixture acquired a server connection")
			}
		}
	}
	for _, kind := range []string{"MYSQL", " postgres", "mysql ", "sqlite:foreign.db", "unknown"} {
		if _, err := nativeDestinationDatabaseConfig(kind, base); err == nil {
			t.Fatal("unknown selection was accepted or silently fell back")
		}
	}
	for _, foreign := range []config.Config{
		{MySQL: config.MySQLConfig{DSN: "sqlite:foreign.db"}},
		{MySQL: config.MySQLConfig{Host: "foreign.example"}},
		{Postgres: config.PostgresConfig{DSN: "postgres://foreign.example/existing"}},
	} {
		if _, err := nativeDestinationDatabaseConfig("mysql", foreign); err == nil {
			t.Fatal("fixture selection replaced an existing database")
		}
	}
}

func TestNativeDatabaseCountQueryKeepsParametersSeparate(t *testing.T) {
	const query = "SELECT COALESCE(SUM(count),0) FROM dest_hits WHERE panel_id=? AND user_id=? AND source=? AND action='block'"
	for _, kind := range []string{"sqlite", "mysql"} {
		if nativeDestinationCountSQL(kind, query) != query {
			t.Fatal("existing count query changed")
		}
	}
	if nativeDestinationCountSQL("postgres", query) != "SELECT COALESCE(SUM(count),0) FROM dest_hits WHERE panel_id=$1 AND user_id=$2 AND source=$3 AND action='block'" {
		t.Fatal("PostgreSQL query lost its independent parameters")
	}
	const plain = "SELECT COUNT(*) FROM dest_hits WHERE site='(ip)'"
	if nativeDestinationCountSQL("postgres", plain) != plain {
		t.Fatal("parameter-free query changed")
	}
}
