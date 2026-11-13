package http

import (
	"context"
	"encoding/json"
	"io"
	stdhttp "net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/KazuhaHub/passwall-sub-panel/internal/adapters/sqlstore"
	"github.com/KazuhaHub/passwall-sub-panel/internal/config"
	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/pkg/jwtutil"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
	"github.com/KazuhaHub/passwall-sub-panel/internal/service/audit"
	"github.com/KazuhaHub/passwall-sub-panel/internal/service/auth"
	"github.com/KazuhaHub/passwall-sub-panel/internal/service/user"
)

// destRouter assembles the real router with an administrator and an operator
// and returns a request helper.
func destRouter(t *testing.T, settings ports.SettingsRepo, auditRepo ports.AuditRepo) (do func(method, target, token, body string) *httptest.ResponseRecorder, adminToken, operatorToken string) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	admin := &domain.User{ID: 1, UPN: "admin@example.test", Enabled: true, Role: domain.RoleAdmin}
	operator := &domain.User{ID: 2, UPN: "operator@example.test", Enabled: true, Role: domain.RoleOperator}
	users := routerReleaseUsers{users: map[int64]*domain.User{1: admin, 2: operator}}
	issuer := jwtutil.NewIssuer(strings.Repeat("test-only-key", 3), func() jwtutil.Params {
		return jwtutil.Params{AccessTTL: time.Hour, RefreshTTL: time.Hour, Issuer: "dest-test"}
	})
	authSvc := auth.New(issuer)
	var err error
	if adminToken, _, err = authSvc.IssueTokens(admin); err != nil {
		t.Fatal(err)
	}
	if operatorToken, _, err = authSvc.IssueTokens(operator); err != nil {
		t.Fatal(err)
	}
	deps := Deps{
		Cfg:   &config.Config{ConfigDir: t.TempDir()},
		Repos: ports.Repos{User: users, Settings: settings, Audit: auditRepo},
		Auth:  authSvc, User: user.New(users, nil, nil, nil, nil, nil, nil, nil),
	}
	if auditRepo != nil {
		deps.Audit = audit.New(auditRepo)
	}
	router := NewRouter(deps)
	return func(method, target, token, body string) *httptest.ResponseRecorder {
		t.Helper()
		var reqBody io.Reader
		if body != "" {
			reqBody = strings.NewReader(body)
		}
		req := httptest.NewRequest(method, "https://panel.example"+target, reqBody)
		if reqBody != nil {
			req.Header.Set("Content-Type", "application/json")
		}
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
		return w
	}, adminToken, operatorToken
}

// ACCESS CONTROL IS THE OWNER'S, like the risk center: its settings decide how
// fast a policy reaches every node and whether a new rule data release is
// applied unattended. staffGroup shares the /api/admin prefix, so only a
// request through the assembled router shows which group the route landed in,
// and the gate is only real if a refused request never reaches the store.
func TestDestSettingsRoutesAreAdminOnly(t *testing.T) {
	settings := &routerPolicySettings{}
	do, adminToken, operatorToken := destRouter(t, settings, nil)
	for _, route := range []struct{ method, body, want string }{
		{stdhttp.MethodGet, "", `"dest_list_refresh_hours"`},
		{stdhttp.MethodPut, `{"settings":{"dest_list_refresh_hours":12}}`, `"dest_list_refresh_hours":12`},
	} {
		for _, c := range []struct {
			name, token string
			status      int
		}{
			{"anonymous", "", stdhttp.StatusUnauthorized},
			{"operator", operatorToken, stdhttp.StatusForbidden},
			{"administrator", adminToken, stdhttp.StatusOK},
		} {
			t.Run(route.method+" as "+c.name, func(t *testing.T) {
				saves := settings.saves
				w := do(route.method, "/api/admin/dest/settings", c.token, route.body)
				if w.Code != c.status {
					t.Fatalf("= %d, want %d: %s", w.Code, c.status, w.Body.String())
				}
				switch {
				case c.status == stdhttp.StatusOK:
					if !strings.Contains(w.Body.String(), route.want) {
						t.Fatalf("the administrator's answer lacks %s: %s", route.want, w.Body.String())
					}
				case strings.Contains(w.Body.String(), route.want) || settings.saves != saves:
					t.Fatalf("a refused request reached the settings store: saves %d→%d, body %s", saves, settings.saves, w.Body.String())
				}
			})
		}
	}
	if settings.settings.DestListRefreshHours != 12 {
		t.Fatalf("the administrator's PUT was not saved: %+v", settings.settings.AccessControlSettings())
	}
}

// ITS AUDIT ROWS ARE THE OWNER'S TOO: every write under /api/admin leaves a row
// with its route, params and body, and the audit read is staffGroup. A write
// under /api/admin/dest/ is read only by an administrator — the real audit
// store, the rows AuditWrites wrote.
func TestDestAuditRowsAreAdminOnly(t *testing.T) {
	db, err := sqlstore.Open("sqlite", filepath.Join(t.TempDir(), "audit.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if s, e := db.DB(); e == nil {
			_ = s.Close()
		}
	})
	if err := sqlstore.EnsureSchema(db); err != nil {
		t.Fatal(err)
	}
	auditRepo := sqlstore.NewRepos(db).Audit
	do, adminToken, operatorToken := destRouter(t, &routerPolicySettings{}, auditRepo)

	if w := do(stdhttp.MethodPut, "/api/admin/dest/settings", adminToken, `{"settings":{"dest_rules_update_mode":"auto"}}`); w.Code != stdhttp.StatusOK {
		t.Fatalf("the administrator's write = %d: %s", w.Code, w.Body.String())
	}
	if err := auditRepo.Insert(context.Background(), &domain.AuditEntry{
		Actor: "operator@example.test", Action: "update /api/admin/users/:id", Target: "/api/admin/users/:id", At: time.Now(),
	}); err != nil {
		t.Fatal(err)
	}
	type page struct {
		Items []struct {
			Target string `json:"target"`
		} `json:"items"`
		Total int64 `json:"total"`
	}
	read := func(token, query string) (page, string) {
		t.Helper()
		w := do(stdhttp.MethodGet, "/api/admin/audit"+query, token, "")
		if w.Code != stdhttp.StatusOK {
			t.Fatalf("GET /api/admin/audit%s = %d: %s", query, w.Code, w.Body.String())
		}
		var p page
		if err := json.Unmarshal(w.Body.Bytes(), &p); err != nil {
			t.Fatal(err)
		}
		return p, w.Body.String()
	}
	for _, query := range []string{"", "?search=dest", "?search=" + "dest_rules_update_mode"} {
		p, raw := read(operatorToken, query)
		if strings.Contains(raw, "/api/admin/dest/") || strings.Contains(raw, "dest_rules_update_mode") {
			t.Fatalf("the operator's audit page%s carries an access-control row: %s", query, raw)
		}
		if query == "" && (p.Total != 1 || len(p.Items) != 1) {
			t.Fatalf("the operator's page = %+v; want only the one row outside access control", p)
		}
	}
	p, raw := read(adminToken, "")
	if p.Total != 2 || !strings.Contains(raw, "/api/admin/dest/settings") {
		t.Fatalf("the administrator's page = %+v; want both rows: %s", p, raw)
	}
}
