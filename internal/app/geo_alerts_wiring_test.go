package app

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/KazuhaHub/passwall-sub-panel/internal/adapters/sqlstore"
	"github.com/KazuhaHub/passwall-sub-panel/internal/config"
	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/pkg/jwtutil"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
	"github.com/KazuhaHub/passwall-sub-panel/internal/service/alert"
)

// THE BELL'S GEO SOURCES ARE OPTIONAL DEPS, WHICH MAKES FORGETTING THEM
// SILENT.
//
// alert.Deps.GeoFlags (the geo_streaks store, handed over by Build) and
// alert.Deps.ServiceHolds (the user repository, handed over by NewRouter) are
// both nil-tolerant: leave either out and the feed simply never produces that
// entry, every unit test — which builds alert.Service directly — stays green,
// and the admin is never told that the detector flagged or suspended anyone.
// Only a request through the assembled application sees it.
func TestBuildWiresTheGeoAlerts(t *testing.T) {
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

	newUser := func(name string, role domain.Role, uuid string) *domain.User {
		t.Helper()
		u := &domain.User{
			UPN: name + "@example.test", Email: name + "@example.test", SSOProvider: domain.SSOProviderLocal,
			SSOSubject: name + "@example.test", Role: role, Enabled: true,
			UUID: uuid, SubToken: "fixture-" + name + "-subscription-token",
			TrafficResetPeriod: domain.ResetMonthly,
		}
		if err := a.repos.User.Create(ctx, u); err != nil {
			t.Fatal(err)
		}
		return u
	}
	admin := newUser("geo-bell-admin", domain.RoleAdmin, "55555555-5555-4555-8555-555555555551")
	flagged := newUser("geo-bell-flagged", domain.RoleUser, "55555555-5555-4555-8555-555555555552")
	held := newUser("geo-bell-held", domain.RoleUser, "55555555-5555-4555-8555-555555555553")

	// A latched flag, written the way the poll writes it. The streak store is
	// not on App, so this opens the same database file Build opened.
	db, err := sqlstore.Open(cfg.DBKind(), cfg.DBDSN())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if sqlDB, err := db.DB(); err == nil {
			_ = sqlDB.Close()
		}
	})
	if err := sqlstore.NewGeoStreakRepo(db).Save(ctx, map[int64]domain.GeoRecord{
		flagged.ID: {UserID: flagged.ID, State: domain.GeoStateFlagged, Streak: domain.GeoStreak{Over: 3, Flagged: true}},
	}); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	if err := a.repos.User.UpdateServiceState(ctx, held.ID, domain.DisabledGeoAutoSuspend, "detail", &now); err != nil {
		t.Fatal(err)
	}

	// A token the application's own verifier accepts: same secret, same
	// issuer setting.
	set, err := a.repos.Settings.Load(ctx, ports.UISettings{})
	if err != nil {
		t.Fatal(err)
	}
	token, err := jwtutil.NewIssuer(cfg.JWTSecret, func() jwtutil.Params {
		return jwtutil.Params{AccessTTL: time.Hour, RefreshTTL: time.Hour, Issuer: set.JWTIssuer}
	}).IssueAccess(admin.ID, admin.UPN, admin.Role, admin.TokenVersion)
	if err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/admin/alerts", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	a.server.Handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/admin/alerts = %d: %s", rec.Code, rec.Body.String())
	}
	var body struct {
		Alerts []alert.Alert `json:"alerts"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v (%s)", err, rec.Body.String())
	}
	counts := map[alert.Type]int{}
	for _, item := range body.Alerts {
		counts[item.Type] = item.Count
	}
	if counts[alert.TypeGeoAnomaly] != 1 {
		t.Fatalf("geo_anomaly count = %d in %s, want 1 — is the geo_streaks store wired into the alert service?",
			counts[alert.TypeGeoAnomaly], rec.Body.String())
	}
	if counts[alert.TypeGeoAutoSuspended] != 1 {
		t.Fatalf("geo_auto_suspended count = %d in %s, want 1 — is the user repository wired into the alert service?",
			counts[alert.TypeGeoAutoSuspended], rec.Body.String())
	}
}
