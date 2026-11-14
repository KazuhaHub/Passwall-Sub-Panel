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

// Build hands the hourly cleanup the real consent store: after one pass the
// consent record of a deleted account is gone and a living account's stays.
// Left unwired the purge returns at once on a nil store and a record that names
// a deleted person stays for ever.
func TestBuildPrunesLegalConsentOrphans(t *testing.T) {
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
		if err := a.Shutdown(shutdownCtx); err != nil {
			t.Error(err)
		}
		sqlstore.ConfigureSecretKey("")
	})
	newUser := func(n string) *domain.User {
		t.Helper()
		u := &domain.User{
			UPN: n + "@example.test", Email: n + "@example.test", SSOProvider: domain.SSOProviderLocal, SSOSubject: n + "@example.test",
			Role: domain.RoleUser, Enabled: true, UUID: "88888888-8888-4888-8888-" + strings.Repeat(n[:1], 12),
			SubToken: "fixture-legal-prune-" + n, TrafficResetPeriod: domain.ResetMonthly,
		}
		if err := a.repos.User.Create(ctx, u); err != nil {
			t.Fatal(err)
		}
		return u
	}
	kept, gone := newUser("kim"), newUser("lee")

	db, err := sqlstore.Open(cfg.DBKind(), cfg.DBDSN())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if sqlDB, err := db.DB(); err == nil {
			_ = sqlDB.Close()
		}
	})
	repo := sqlstore.NewLegalRepo(db)
	for _, u := range []*domain.User{kept, gone} {
		if err := repo.RecordConsent(ctx, domain.LegalConsent{UserID: u.ID, ConsentVersion: 1, Method: domain.LegalConsentRegister}); err != nil {
			t.Fatal(err)
		}
	}
	if err := a.repos.User.Delete(ctx, gone.ID); err != nil {
		t.Fatal(err)
	}

	a.pruneLegalConsents(ctx)

	var n int64
	if err := db.Raw("SELECT COUNT(*) FROM legal_consents").Scan(&n).Error; err != nil {
		t.Fatal(err)
	}
	if ok, _ := repo.HasConsentAtLeast(ctx, kept.ID, 1); n != 1 || !ok {
		t.Fatalf("after the purge: %d rows, the living account's consent present = %v; want only that one — is the store wired into App?", n, ok)
	}
}

// The assembled router serves the public document and the admin API from the
// wiring Build fills, not from a nil: the routes answer 503 or 404 when left
// out, which compiles and fails only when somebody opens the page.
func TestBuildWiresTheLegalAPI(t *testing.T) {
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
	if a.legal == nil {
		t.Fatal("Build did not wire the legal service into App")
	}
	if _, err := a.legal.Public(ctx, domain.LegalKindTerms, "zh-CN"); err == nil {
		t.Fatal("legal is disabled on a fresh panel, so the public document must not be served")
	}
}
