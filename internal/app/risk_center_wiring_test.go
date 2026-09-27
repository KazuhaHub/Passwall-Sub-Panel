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
)

// THE RISK CENTER IS A ROUTER DEP THAT COMPILES WHEN LEFT OUT.
//
// Deps.RiskCenter is optional: without it every risk-center route answers
// 503, every handler and service test still passes on its fakes, and the
// admin's new menu shows nothing but errors. Only a request through the
// assembled application shows the service was built and handed over — and
// built over the real sources: the traffic service's snapshot and refresh,
// the flag records the stores write.
//
// Driven with a JWT like an admin's browser: the live view answers (no poll
// has run, so no snapshot, stale), a refresh reads every panel — there are
// none, so it reads nothing — and stores its snapshot, which the live view
// then shows, and a flag record appended to the store Build opened reaches
// the flag list.
func TestBuildWiresTheRiskCenter(t *testing.T) {
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

	admin := &domain.User{
		UPN: "center-admin@example.test", Email: "center-admin@example.test", SSOProvider: domain.SSOProviderLocal,
		SSOSubject: "center-admin@example.test", Role: domain.RoleAdmin, Enabled: true,
		UUID: "88888888-8888-4888-8888-888888888888", SubToken: "fixture-risk-center-admin-token",
		TrafficResetPeriod: domain.ResetMonthly,
	}
	if err := a.repos.User.Create(ctx, admin); err != nil {
		t.Fatal(err)
	}
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
	call := func(method, path string) map[string]any {
		t.Helper()
		req := httptest.NewRequest(method, path, nil)
		req.Header.Set("Authorization", "Bearer "+token)
		rec := httptest.NewRecorder()
		a.server.Handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("%s %s = %d: %s — is the risk center wired into the router?", method, path, rec.Code, rec.Body.String())
		}
		var out map[string]any
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
			t.Fatalf("decode %s: %v (%s)", path, err, rec.Body.String())
		}
		return out
	}

	live := call(http.MethodGet, "/api/admin/risk-center/live")
	if snap, _ := live["snapshot"].(map[string]any); snap == nil || snap["taken_at"] != nil || snap["stale"] != true {
		t.Fatalf("live before any reading = %v, want no snapshot, stale", live["snapshot"])
	}

	refreshed := call(http.MethodPost, "/api/admin/risk-center/live/refresh")
	if refreshed["refreshed"] != true || refreshed["source"] != domain.LiveSnapshotFromRefresh {
		t.Fatalf("refresh = %v, want a refresh snapshot — is the traffic service the risk center's live source?", refreshed)
	}
	live = call(http.MethodGet, "/api/admin/risk-center/live")
	if snap, _ := live["snapshot"].(map[string]any); snap == nil || snap["source"] != domain.LiveSnapshotFromRefresh || snap["taken_at"] == nil {
		t.Fatalf("live after the refresh = %v, want the refresh's snapshot", live["snapshot"])
	}

	// The same database file Build opened: the flag list must read the
	// store the producers write.
	db, err := sqlstore.Open(cfg.DBKind(), cfg.DBDSN())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if sqlDB, err := db.DB(); err == nil {
			_ = sqlDB.Close()
		}
	})
	if err := sqlstore.NewFlagRecordRepo(db).Append(ctx, []domain.FlagRecord{
		domain.GeoAutoFlag(admin.ID, domain.FlagAutoLiftedAdmin, "admin_resume", nil, time.Now()),
	}); err != nil {
		t.Fatal(err)
	}
	flags := call(http.MethodGet, "/api/admin/risk-center/flags")
	items, _ := flags["items"].([]any)
	if len(items) != 1 || flags["total"] != float64(1) {
		t.Fatalf("flags = %v, want the one record appended to the store Build opened", flags)
	}
	if rec, _ := items[0].(map[string]any); rec["event"] != string(domain.FlagAutoLiftedAdmin) || rec["upn"] != admin.UPN {
		t.Fatalf("flag record = %v", items[0])
	}
	history := call(http.MethodGet, "/api/admin/risk-center/connections")
	if history["total"] != float64(0) {
		t.Fatalf("connections = %v, want the empty history", history)
	}
}
