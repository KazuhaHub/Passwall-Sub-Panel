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
)

// THE REVIEW ACTIONS ARE A ROUTER DEP THAT COMPILES WHEN LEFT OUT.
//
// Deps.RiskReview is optional: without it the four review routes answer
// 503, every handler and service test still passes on its fakes, and the
// drawer's 忽略 / 信任 buttons do nothing but fail. Only a request through the
// assembled application shows the service was built and handed over — and
// built over the real pieces: the attention the risk center computes (a
// geo_auto hold read from the users row is something to dismiss), the
// risk_reviews store writing its row and its record in one transaction, the
// audit middleware in front of it, and the user service lifting the
// detector's hold when a trust asks for it.
//
// Driven with a JWT like an admin's browser: a held account is dismissed
// (the record names the admin by id and the levels, never the note, which
// only the audit row keeps), then trusted with its service resumed.
func TestBuildWiresTheRiskReview(t *testing.T) {
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

	newUser := func(name, uuid string, role domain.Role) *domain.User {
		t.Helper()
		u := &domain.User{
			UPN: name + "@example.test", Email: name + "@example.test", SSOProvider: domain.SSOProviderLocal,
			SSOSubject: name + "@example.test", Role: role, Enabled: true, UUID: uuid,
			SubToken: "fixture-" + name + "-subscription-token", TrafficResetPeriod: domain.ResetMonthly,
		}
		if err := a.repos.User.Create(ctx, u); err != nil {
			t.Fatal(err)
		}
		return u
	}
	admin := newUser("review-admin", "77777777-7777-4777-8777-000000000001", domain.RoleAdmin)
	held := newUser("review-held", "77777777-7777-4777-8777-000000000002", domain.RoleUser)
	if ok, err := a.repos.User.SetServiceStateIfClear(ctx, held.ID, domain.DisabledGeoAutoSuspend, "detail", time.Now()); err != nil || !ok {
		t.Fatalf("hold the account = %v, %v", ok, err)
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
	base := "/api/admin/risk-center/users/" + strconv.FormatInt(held.ID, 10)
	call := func(method, path, body string) map[string]any {
		t.Helper()
		req := httptest.NewRequest(method, path, bytes.NewBufferString(body))
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		a.server.Handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("%s %s = %d: %s — are the review actions wired into the router?", method, path, rec.Code, rec.Body.String())
		}
		var out map[string]any
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
			t.Fatalf("decode %s: %v (%s)", path, err, rec.Body.String())
		}
		return out
	}

	const note = "spoke to the user, travelling"
	dismissed := call(http.MethodPost, base+"/dismiss", `{"note":"`+note+`","expected":{"geo_auto":"suspended"}}`)
	if review, _ := dismissed["review"].(map[string]any); review["dismissed"] != true || review["note"] != note {
		t.Fatalf("dismiss = %v, want the account dismissed with the note — is the risk center its attention reader?", dismissed)
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
	uid := held.ID
	records, _, err := sqlstore.NewFlagRecordRepo(db).List(ctx, ports.FlagRecordFilter{
		UserID: &uid, Source: domain.FlagSourceReview, Event: string(domain.FlagReviewDismissed),
		Pagination: ports.Pagination{Page: 1, PageSize: 10},
	})
	if err != nil {
		t.Fatal(err)
	}
	wantParams := `{"by":` + strconv.FormatInt(admin.ID, 10) + `,"levels":{"geo_auto":"suspended"}}`
	if len(records) != 1 || string(records[0].Params) != wantParams {
		t.Fatalf("dismissed records = %+v, want one with params %s", records, wantParams)
	}
	if strings.Contains(string(records[0].Params), note) || strings.Contains(string(records[0].Params), admin.UPN) {
		t.Fatalf("the record %s carries the note or the admin's name", records[0].Params)
	}

	// The audit middleware writes on the async dispatcher: wait for the row.
	action := "create_or_run /api/admin/risk-center/users/:id/dismiss"
	var audits []*domain.AuditEntry
	deadline := time.Now().Add(5 * time.Second)
	for {
		audits, _, err = a.repos.Audit.List(ctx, ports.AuditFilter{Action: action, Pagination: ports.Pagination{Page: 1, PageSize: 10}})
		if err != nil {
			t.Fatal(err)
		}
		if len(audits) > 0 || time.Now().After(deadline) {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if len(audits) != 1 || audits[0].Actor != admin.UPN || !strings.Contains(audits[0].BeforeJSON, note) {
		t.Fatalf("dismiss audit rows = %+v, want one by %s keeping the request's note", audits, admin.UPN)
	}

	trusted := call(http.MethodPost, base+"/trust", `{"resume_service":true}`)
	if review, _ := trusted["review"].(map[string]any); review["trusted"] != true || trusted["resumed"] != true {
		t.Fatalf("trust = %v, want trusted and resumed — is the user service the resumer?", trusted)
	}
	got, err := a.repos.User.GetByID(ctx, held.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.ServiceDisabledReason != domain.DisabledNone {
		t.Fatalf("service reason after trust with resume = %q, want cleared", got.ServiceDisabledReason)
	}
}
