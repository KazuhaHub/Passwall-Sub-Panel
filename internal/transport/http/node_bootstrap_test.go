package http

import (
	stdhttp "net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/KazuhaHub/passwall-sub-panel/internal/config"
	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/pkg/jwtutil"
	"github.com/KazuhaHub/passwall-sub-panel/internal/pkg/operationgate"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
	"github.com/KazuhaHub/passwall-sub-panel/internal/service/auth"
	"github.com/KazuhaHub/passwall-sub-panel/internal/service/user"
	"github.com/gin-gonic/gin"
)

func TestNodeBootstrapAdministratorBoundaryAndCallbackRoute(t *testing.T) {
	gin.SetMode(gin.TestMode)
	users := routerReleaseUsers{users: map[int64]*domain.User{
		1: {ID: 1, UPN: "owner@example.test", Enabled: true, Role: domain.RoleAdmin},
		2: {ID: 2, UPN: "operator@example.test", Enabled: true, Role: domain.RoleOperator},
	}}
	issuer := jwtutil.NewIssuer(strings.Repeat("test-only-signing-key", 2), func() jwtutil.Params {
		return jwtutil.Params{AccessTTL: time.Hour, RefreshTTL: time.Hour, Issuer: "bootstrap-router"}
	})
	authSvc := auth.New(issuer)
	admin, _, err := authSvc.IssueTokens(users.users[1])
	if err != nil {
		t.Fatal(err)
	}
	operator, _, err := authSvc.IssueTokens(users.users[2])
	if err != nil {
		t.Fatal(err)
	}
	router := NewRouter(Deps{Cfg: &config.Config{ConfigDir: t.TempDir()}, Repos: ports.Repos{User: users, Settings: &dispatchSettingsRepo{}}, Auth: authSvc, User: user.New(users, nil, nil, nil, nil, nil, nil, nil), OperationGate: operationgate.New()})
	for _, path := range []string{"/api/admin/servers/41/node-install-command", "/api/admin/servers/41/node-migration-command"} {
		for _, test := range []struct {
			token string
			want  int
		}{{"", 401}, {operator, 403}} {
			req := httptest.NewRequest(stdhttp.MethodPost, "https://panel.example"+path, strings.NewReader(`{"version":"v0.0.1-beta3"}`))
			req.Header.Set("Content-Type", "application/json")
			if test.token != "" {
				req.Header.Set("Authorization", "Bearer "+test.token)
			}
			w := httptest.NewRecorder()
			router.ServeHTTP(w, req)
			if w.Code != test.want {
				t.Fatalf("%s: %d want %d", path, w.Code, test.want)
			}
		}
	}
	// A panel JWT is not a node-completion ticket. This public route must not
	// fall through to SPA success and must reject before any exclusive wait.
	req := httptest.NewRequest(stdhttp.MethodPost, "https://panel.example/api/node-bootstrap/complete", strings.NewReader(`{"old_backend_stopped":true}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+admin)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != 401 {
		t.Fatalf("callback: %d %s", w.Code, w.Body.String())
	}
	w = httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest(stdhttp.MethodGet, "https://panel.example/node-bootstrap/"+strings.Repeat("a", 43), nil))
	if w.Code != 410 || w.Header().Get("Cache-Control") != "no-store, private" {
		t.Fatal("unknown bootstrap route exposed SPA success")
	}
}
