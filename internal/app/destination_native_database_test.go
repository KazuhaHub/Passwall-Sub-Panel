//go:build destination_acceptance && linux

package app

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/KazuhaHub/passwall-sub-panel/internal/adapters/sqlstore"
	"github.com/KazuhaHub/passwall-sub-panel/internal/config"
)

func buildNativeDestinationPolicyFixture(t *testing.T) destinationPolicyFixture {
	t.Helper()
	if os.Geteuid() != 0 || os.Getenv("GITHUB_ACTIONS") != "true" || os.Getenv("RUNNER_ENVIRONMENT") != "github-hosted" || os.Getenv("GITHUB_REPOSITORY") != "KazuhaHub/Passwall-Sub-Panel" || os.Getenv("PSP_NATIVE_DB_DSN") != "" {
		t.Fatal("native database fixture requires the dedicated disposable hosted job")
	}
	directory := t.TempDir()
	cfg, err := nativeDestinationDatabaseConfig(os.Getenv("PSP_NATIVE_DB_KIND"), config.Config{
		Listen: "127.0.0.1:0", JWTSecret: strings.Repeat("j", 48), EncryptionKey: strings.Repeat("e", 48),
		ConfigDir: filepath.Join(directory, "config"), DataDir: filepath.Join(directory, "data"),
	})
	nativeMust(t, err, "select owned native database")
	a, err := Build(t.Context(), &cfg)
	nativeMust(t, err, "build native database fixture")
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		nativeMust(t, a.Shutdown(ctx), "close native database fixture")
		sqlstore.ConfigureSecretKey("")
	})
	nativeVerifyDestinationDatabase(t, a)
	return seedDestinationPolicyFixture(t, a)
}

func nativeVerifyDestinationDatabase(t *testing.T, a *App) {
	t.Helper()
	kind := os.Getenv("PSP_NATIVE_DB_KIND")
	if kind == "" {
		kind = "sqlite"
	}
	if a.cfg.DBKind() != kind {
		t.Fatal("native database configuration does not match the requested driver")
	}
	var version, database string
	switch kind {
	case "sqlite":
		nativeMust(t, a.database.QueryRowContext(t.Context(), "SELECT sqlite_version()").Scan(&version), "verify actual SQLite driver")
		if !strings.HasPrefix(version, "3.") {
			t.Fatal("native SQLite server version is not supported")
		}
	case "mysql":
		nativeMust(t, a.database.QueryRowContext(t.Context(), "SELECT VERSION(), DATABASE()").Scan(&version, &database), "verify actual MySQL driver")
		if !strings.HasPrefix(version, "8.") || database != "psp_native_acceptance" {
			t.Fatal("native MySQL connection is not the owned version and database")
		}
	case "postgres":
		nativeMust(t, a.database.QueryRowContext(t.Context(), "SELECT version(), current_database()").Scan(&version, &database), "verify actual PostgreSQL driver")
		if !strings.HasPrefix(version, "PostgreSQL 16.") || database != "psp_native_acceptance" {
			t.Fatal("native PostgreSQL connection is not the owned version and database")
		}
	default:
		t.Fatal("native database identity is unsupported")
	}
}
