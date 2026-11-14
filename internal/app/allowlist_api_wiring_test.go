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
	"github.com/KazuhaHub/passwall-sub-panel/internal/seed"
)

// THE ALLOWLIST GROUP API IS ASSEMBLED FROM SEVEN LATE-BOUND PARTS, and a part
// left out compiles and fails only when the page is opened (or, for the member
// sync, never: the heal sweep hides it). This drives the application Build
// assembled: the routes exist, a list the group holds says so in the list
// service, the base hosts come from the subscription templates the panel
// renders with, and the service knows every collaborator it needs.
func TestBuildWiresTheAllowlistGroupAPI(t *testing.T) {
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
	if a.destGroups == nil {
		t.Fatal("Build left the allowlist group service unwired")
	}
	if w := a.destGroups.Wired(); !w.Budget || !w.Resync || !w.Changed || !w.Hits || !w.BaseHosts || !w.Editor || !w.Settings {
		t.Fatalf("the allowlist group service is missing collaborators: %+v", w)
	}
	// The executable seeds the templates before Build; this fixture does it after.
	if err := seed.Ensure(cfg.ConfigDir); err != nil {
		t.Fatal(err)
	}

	admin := &domain.User{
		UPN: "al-admin@example.test", Email: "al-admin@example.test", SSOProvider: domain.SSOProviderLocal,
		SSOSubject: "al-admin@example.test", Role: domain.RoleAdmin, Enabled: true,
		UUID: "88888888-8888-4888-8888-888888888888", SubToken: "fixture-al-admin-token", TrafficResetPeriod: domain.ResetMonthly,
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
	call := func(method, path, body string) (int, map[string]any) {
		t.Helper()
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+token)
		if body != "" {
			req.Header.Set("Content-Type", "application/json")
		}
		rec := httptest.NewRecorder()
		a.server.Handler.ServeHTTP(rec, req)
		var out map[string]any
		_ = json.Unmarshal(rec.Body.Bytes(), &out)
		return rec.Code, out
	}

	g := &domain.Group{Slug: "guests", Name: "Guests", TagFilter: domain.TagFilter{All: true}}
	if err := a.repos.Group.Create(ctx, g); err != nil {
		t.Fatal(err)
	}
	l, _, err := a.destLists.CreateCustom(ctx, "news", domain.DestSideDest, []byte("news.example.com"))
	if err != nil {
		t.Fatal(err)
	}

	base := "/api/admin/dest/groups/" + itoa(g.ID)
	if code, body := call(http.MethodGet, "/api/admin/dest/groups", ""); code != http.StatusOK || len(body["items"].([]any)) != 1 {
		t.Fatalf("GET /dest/groups = %d %v — is Deps.Dest.AllowlistGroups wired into the router?", code, body)
	}
	// The suggestion is read from the templates the panel renders subscriptions with.
	code, pv := call(http.MethodGet, base+"/preview?state=report", "")
	if code != http.StatusOK || pv["base_hosts_source"] != "template" {
		t.Fatalf("preview = %d %v: the base hosts must come from the default templates", code, pv)
	}
	put := `{"state":"report","list_ids":[` + itoa(l.ID) + `],"base_hosts":["doh.example.com"]}`
	if code, body := call(http.MethodPut, base, put); code != http.StatusOK || body["state"] != "report" {
		t.Fatalf("PUT = %d %v", code, body)
	}
	// The list service knows the group holds the list.
	used, err := a.destLists.UsedBy(ctx)
	if err != nil || len(used[l.ID]) != 1 || used[l.ID][0].Kind != "group" {
		t.Fatalf("used by = %+v, %v: the list service does not read the allowlist groups", used[l.ID], err)
	}
	if err := a.destLists.DeleteList(ctx, l.ID); err == nil {
		t.Fatal("a list a group holds was deleted")
	}
	// The policy list names the group in force.
	code, pol := call(http.MethodGet, "/api/admin/dest/policies", "")
	if code != http.StatusOK || len(pol["allowlist_groups"].([]any)) != 1 {
		t.Fatalf("policies = %d %v", code, pol["allowlist_groups"])
	}
}
