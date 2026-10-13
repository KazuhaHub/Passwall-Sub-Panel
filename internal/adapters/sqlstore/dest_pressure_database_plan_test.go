package sqlstore

import (
	"net/url"
	"testing"

	"github.com/go-sql-driver/mysql"
)

// Server connections belong to the fixed, disposable services of this job.
// No operator-supplied connection string participates in pressure acceptance.
func nativeSQLPressureDatabase(kind, sqlitePath string, overrides ...string) (driver, dsn string, valid bool) {
	for _, value := range overrides {
		if value != "" {
			return "", "", false
		}
	}
	switch kind {
	case "", "sqlite":
		if sqlitePath == "" {
			return "", "", false
		}
		return "sqlite", sqlitePath, true
	case "mysql":
		return "mysql", "psp:fixture-native-pressure-password@tcp(127.0.0.1:33307)/psp_native_pressure?parseTime=true&charset=utf8mb4&loc=UTC", true
	case "postgres":
		return "postgres", "postgres://psp:fixture-native-pressure-password@127.0.0.1:35432/psp_native_pressure?sslmode=disable", true
	default:
		return "", "", false
	}
}

func TestNativeSQLPressureDatabaseOwnsExactLoopbackServicesAndRejectsOverrides(t *testing.T) {
	for _, kind := range []string{"", "sqlite", "mysql", "postgres"} {
		driver, dsn, valid := nativeSQLPressureDatabase(kind, "owned-pressure.db")
		if !valid {
			t.Fatal("owned pressure database was rejected")
		}
		switch kind {
		case "mysql":
			cfg, err := mysql.ParseDSN(dsn)
			if err != nil || driver != kind || cfg.Net != "tcp" || cfg.Addr != "127.0.0.1:33307" || cfg.DBName != "psp_native_pressure" || cfg.User != "psp" || !cfg.ParseTime {
				t.Fatal("pressure MySQL escaped its dedicated service")
			}
		case "postgres":
			cfg, err := url.Parse(dsn)
			if err != nil || driver != kind || cfg.Scheme != "postgres" || cfg.Host != "127.0.0.1:35432" || cfg.Path != "/psp_native_pressure" || cfg.User.Username() != "psp" || cfg.Query().Get("sslmode") != "disable" {
				t.Fatal("pressure PostgreSQL escaped its dedicated service")
			}
		default:
			if driver != "sqlite" || dsn != "owned-pressure.db" {
				t.Fatal("file SQLite pressure silently selected another database")
			}
		}
		for _, overrides := range [][]string{{"foreign"}, {"", "foreign"}, {"", "", "foreign"}} {
			if _, _, valid := nativeSQLPressureDatabase(kind, "owned-pressure.db", overrides...); valid {
				t.Fatal("pressure fixture accepted an operator database override")
			}
		}
	}
	for _, kind := range []string{"MYSQL", "postgres ", " mysql", "mysql://foreign", "sqlite:foreign.db"} {
		if _, _, valid := nativeSQLPressureDatabase(kind, "owned-pressure.db"); valid {
			t.Fatal("unknown pressure database selection silently fell back")
		}
	}
	if _, _, valid := nativeSQLPressureDatabase("sqlite", ""); valid {
		t.Fatal("pressure fixture accepted an empty file path")
	}
}
