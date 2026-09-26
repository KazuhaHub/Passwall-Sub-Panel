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
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

// THE SUSPENDER AND THE AUDIT LOG ARE LATE-BOUND, WHICH MAKES FORGETTING THEM
// SILENT.
//
// Without SetGeoSuspender the traffic service counts every due ban as
// skipped_unwired and never lifts anything; without SetAuditRepo the
// transitions leave no audit row. Both are nil-tolerant by design, the traffic
// tests wire fakes, and so deleting either line from Build compiles and leaves
// every other test green. This drives the poll of the application Build
// assembled: a geo_auto suspension two hours old (the shipped time box is 60
// minutes) must be lifted by the real user service and audited in the real
// database.
func TestBuildWiresTheGeoAutoSuspension(t *testing.T) {
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

	u := &domain.User{
		UPN: "geo-auto@example.test", Email: "geo-auto@example.test", SSOProvider: domain.SSOProviderLocal,
		SSOSubject: "geo-auto@example.test", Role: domain.RoleUser, Enabled: true,
		UUID: "44444444-4444-4444-8444-444444444444", SubToken: "fixture-geo-auto-subscription-token",
		TrafficResetPeriod: domain.ResetMonthly,
	}
	if err := a.repos.User.Create(ctx, u); err != nil {
		t.Fatal(err)
	}
	began := time.Now().Add(-2 * time.Hour)
	if err := a.repos.User.UpdateServiceState(ctx, u.ID, domain.DisabledGeoAutoSuspend, "detail", &began); err != nil {
		t.Fatal(err)
	}

	if err := a.traffic.PollOnce(ctx); err != nil {
		t.Fatalf("PollOnce: %v", err)
	}

	got, err := a.repos.User.GetByID(ctx, u.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.ServiceDisabledReason != domain.DisabledNone {
		t.Fatalf("service reason = %q after a poll two hours into a 60-minute suspension, want lifted — is the suspender wired?",
			got.ServiceDisabledReason)
	}
	rows, _, err := a.repos.Audit.List(ctx, ports.AuditFilter{Action: "geo_auto_lift", Pagination: ports.Pagination{Page: 1, PageSize: 10}})
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].Actor != "geo-detector" {
		t.Fatalf("geo_auto_lift audit rows = %+v, want one by geo-detector — is the audit log wired?", rows)
	}
}
