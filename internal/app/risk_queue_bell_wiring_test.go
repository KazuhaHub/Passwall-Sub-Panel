package app

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
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

// THE BELL'S RISK SOURCE IS AN OPTIONAL DEP, WHICH MAKES FORGETTING IT
// SILENT.
//
// alert.Deps.RiskQueue (the risk center, handed over by Build through the
// router) is nil-tolerant: leave it out and the feed simply never produces
// the risk_queue entry, every unit test — which builds alert.Service
// directly — stays green, and the admin is never told that an account needs
// action. Only a request through the assembled application sees it, and only
// there is the count the risk center's own: a latched location flag and the
// detector's own hold each make an account urgent, a dismissal takes one
// back out through the review service, and the handler never computes the
// entry for an operator.
func TestBuildWiresTheRiskQueueBell(t *testing.T) {
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
	admin := newUser("bell-admin", domain.RoleAdmin, "55555555-5555-4555-8555-555555555551")
	operator := newUser("bell-operator", domain.RoleOperator, "55555555-5555-4555-8555-555555555554")
	flagged := newUser("bell-flagged", domain.RoleUser, "55555555-5555-4555-8555-555555555552")
	held := newUser("bell-held", domain.RoleUser, "55555555-5555-4555-8555-555555555553")

	// A latched flag judged just now, written the way the poll writes it.
	// The streak store is not on App, so this opens the same database file
	// Build opened.
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
	// The detector's own hold, written through its conditional write.
	if ok, err := a.repos.User.SetServiceStateIfClear(ctx, held.ID, domain.DisabledGeoAutoSuspend, "detail", time.Now()); err != nil || !ok {
		t.Fatalf("hold the account = %v, %v", ok, err)
	}

	// Tokens the application's own verifier accepts: same secret, same
	// issuer setting.
	set, err := a.repos.Settings.Load(ctx, ports.UISettings{})
	if err != nil {
		t.Fatal(err)
	}
	issuer := jwtutil.NewIssuer(cfg.JWTSecret, func() jwtutil.Params {
		return jwtutil.Params{AccessTTL: time.Hour, RefreshTTL: time.Hour, Issuer: set.JWTIssuer}
	})
	tokenOf := func(u *domain.User) string {
		t.Helper()
		token, err := issuer.IssueAccess(u.ID, u.UPN, u.Role, u.TokenVersion)
		if err != nil {
			t.Fatal(err)
		}
		return token
	}
	call := func(u *domain.User, method, path, body string) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(method, path, bytes.NewBufferString(body))
		req.Header.Set("Authorization", "Bearer "+tokenOf(u))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		a.server.Handler.ServeHTTP(rec, req)
		return rec
	}
	feed := func(u *domain.User) []alert.Alert {
		t.Helper()
		rec := call(u, http.MethodGet, "/api/admin/alerts", "")
		if rec.Code != http.StatusOK {
			t.Fatalf("GET /api/admin/alerts as %s = %d: %s", u.Role, rec.Code, rec.Body.String())
		}
		var body struct {
			Alerts []alert.Alert `json:"alerts"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatalf("decode: %v (%s)", err, rec.Body.String())
		}
		return body.Alerts
	}
	riskQueue := func(items []alert.Alert) []alert.Alert {
		var out []alert.Alert
		for _, item := range items {
			if item.Type == alert.TypeRiskQueue {
				out = append(out, item)
			}
		}
		return out
	}

	if got := riskQueue(feed(admin)); len(got) != 1 || got[0].Count != 2 {
		t.Fatalf("risk_queue entries = %+v, want one with count 2 (the flagged and the held account) — is the risk center wired into the alert service?", got)
	}

	// Dismissing the flagged account through the review service takes it
	// off the bell: the count is the queue's, not a separate tally of flags.
	dismiss := call(admin, http.MethodPost,
		"/api/admin/risk-center/users/"+strconv.FormatInt(flagged.ID, 10)+"/dismiss", `{"expected":{"geo":"flagged"}}`)
	if dismiss.Code != http.StatusOK {
		t.Fatalf("dismiss = %d: %s", dismiss.Code, dismiss.Body.String())
	}
	if got := riskQueue(feed(admin)); len(got) != 1 || got[0].Count != 1 {
		t.Fatalf("risk_queue entries after a dismissal = %+v, want one with count 1", got)
	}

	if got := riskQueue(feed(operator)); len(got) != 0 {
		t.Fatalf("operator's feed has risk_queue %+v; the risk center is admin-only", got)
	}
}
