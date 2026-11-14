package app

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/KazuhaHub/passwall-sub-panel/internal/adapters/sqlstore"
	"github.com/KazuhaHub/passwall-sub-panel/internal/config"
	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
)

// Without Deps.Dest the risk worker compiles and dest_block simply never has a
// row. So: one refresh through the assembled application must leave an account
// with no recording node on unknown/no_collector.
func TestBuildJudgesDestBlock(t *testing.T) {
	ctx := t.Context()
	directory := t.TempDir()
	cfg := &config.Config{
		Listen: "127.0.0.1:0", JWTSecret: strings.Repeat("j", 48), EncryptionKey: strings.Repeat("e", 48),
		ConfigDir: filepath.Join(directory, "config"), DataDir: filepath.Join(directory, "data"),
	}
	a, err := Build(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = a.Shutdown(shutdownCtx)
		sqlstore.ConfigureSecretKey("")
	})
	u := &domain.User{
		UPN: "db@example.test", Email: "db@example.test", SSOProvider: domain.SSOProviderLocal, SSOSubject: "db@example.test",
		Role: domain.RoleUser, Enabled: true, UUID: "44444444-4444-4444-8444-444444444444", SubToken: "fixture-destblock-token", TrafficResetPeriod: domain.ResetMonthly,
	}
	if err := a.repos.User.Create(ctx, u); err != nil {
		t.Fatal(err)
	}
	if err := a.risk.RefreshOnce(ctx); err != nil {
		t.Fatal(err)
	}
	db, err := sqlstore.Open(cfg.DBKind(), cfg.DBDSN())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if sqlDB, err := db.DB(); err == nil {
			_ = sqlDB.Close()
		}
	})
	var state, code string
	if err := db.Raw("SELECT state, code FROM risk_signals WHERE user_id = ? AND kind = ?", u.ID, "dest_block").Row().Scan(&state, &code); err != nil {
		t.Fatalf("no dest_block row (is risk.Deps.Dest wired?): %v", err)
	}
	if state != "unknown" || code != "no_collector" {
		t.Fatalf("dest_block = %s/%s", state, code)
	}
}
