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

// A poll that read the trusted accounts before an admin's trust committed
// still carries that account's due ban into its apply phase. What stops the
// suspension is not the poll but the write: the production user repo refuses
// geo_auto for an account with a trusted risk_reviews row, inside the same
// conditional UPDATE. The fakes cannot emulate that, so this pins it end to
// end through the service the detector calls, on the repos Build assembles:
// a trusted account comes back (false, nil) and untouched, an untrusted one
// is suspended.
func TestBuildNeverGeoAutoSuspendsATrustedAccount(t *testing.T) {
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
	newUser := func(name, uuid string) *domain.User {
		t.Helper()
		u := &domain.User{
			UPN: name + "@example.test", Email: name + "@example.test", SSOProvider: domain.SSOProviderLocal,
			SSOSubject: name + "@example.test", Role: domain.RoleUser, Enabled: true, UUID: uuid,
			SubToken: "fixture-" + name + "-subscription-token", TrafficResetPeriod: domain.ResetMonthly,
		}
		if err := a.repos.User.Create(ctx, u); err != nil {
			t.Fatal(err)
		}
		return u
	}
	trusted := newUser("trusted", "99999999-9999-4999-8999-000000000001")
	untrusted := newUser("untrusted", "99999999-9999-4999-8999-000000000002")

	db, err := sqlstore.Open(cfg.DBKind(), cfg.DBDSN())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if sqlDB, err := db.DB(); err == nil {
			_ = sqlDB.Close()
		}
	})
	rev := domain.RiskReview{UserID: trusted.ID, Trusted: true, TrustedAtMS: 1, TrustedBy: 1, UpdatedAtMS: 1}
	if err := sqlstore.NewRiskReviewRepo(db).Save(ctx, rev,
		domain.ReviewFlag(trusted.ID, domain.FlagReviewTrusted, domain.ReviewFlagParams{By: 1}, time.Now())); err != nil {
		t.Fatal(err)
	}

	applied, _, err := a.user.SuspendServiceIfClear(ctx, trusted.ID, domain.DisabledGeoAutoSuspend, "")
	if err != nil || applied {
		t.Fatalf("geo_auto on the trusted account = %v, %v; want false, nil", applied, err)
	}
	got, err := a.repos.User.GetByID(ctx, trusted.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.ServiceDisabledReason != domain.DisabledNone || got.ServiceDisabledAt != nil {
		t.Fatalf("trusted account's service = %q at %v, want clear", got.ServiceDisabledReason, got.ServiceDisabledAt)
	}

	applied, _, err = a.user.SuspendServiceIfClear(ctx, untrusted.ID, domain.DisabledGeoAutoSuspend, "")
	if !applied {
		t.Fatalf("geo_auto on the untrusted account = %v, %v; want it applied", applied, err)
	}
	got, err = a.repos.User.GetByID(ctx, untrusted.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.ServiceDisabledReason != domain.DisabledGeoAutoSuspend {
		t.Fatalf("untrusted account's service reason = %q, want geo_auto", got.ServiceDisabledReason)
	}
}
