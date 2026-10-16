package http

import (
	"context"
	stdhttp "net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/KazuhaHub/passwall-sub-panel/internal/config"
	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/pkg/jwtutil"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
	"github.com/KazuhaHub/passwall-sub-panel/internal/service/auth"
	"github.com/KazuhaHub/passwall-sub-panel/internal/service/user"
	"github.com/gin-gonic/gin"
)

type vlessRouteScopedSettings struct{ ports.ScopedSettings }

func (vlessRouteScopedSettings) LoadForUser(context.Context, *domain.User, ports.UISettings) (ports.UISettings, error) {
	return ports.UISettings{TOTPEnabled: true, Require2FAForStaff: true}, nil
}

func TestVlessRouteEndpointsPreserveNodeReadAndAdminWriteGates(t *testing.T) {
	gin.SetMode(gin.TestMode)
	users := routerReleaseUsers{users: map[int64]*domain.User{
		1: {ID: 1, UPN: "admin", Enabled: true, Role: domain.RoleAdmin},
		2: {ID: 2, UPN: "operator", Enabled: true, Role: domain.RoleOperator},
		3: {ID: 3, UPN: "user", Enabled: true, Role: domain.RoleUser},
	}}
	issuer := jwtutil.NewIssuer(strings.Repeat("test-only-key", 3), func() jwtutil.Params {
		return jwtutil.Params{AccessTTL: time.Hour, RefreshTTL: time.Hour, Issuer: "route-auth-test"}
	})
	authSvc := auth.New(issuer)
	router := NewRouter(Deps{Cfg: &config.Config{ConfigDir: t.TempDir()}, Repos: ports.Repos{User: users, Settings: &dispatchSettingsRepo{}}, Auth: authSvc, User: user.New(users, nil, nil, nil, nil, nil, nil, nil)})
	for _, id := range []int64{0, 1, 2, 3} {
		var token string
		if id > 0 {
			var err error
			token, _, err = authSvc.IssueTokens(users.users[id])
			if err != nil {
				t.Fatal(err)
			}
		}
		for _, endpoint := range []struct {
			method, path string
			staffRead    bool
		}{{"GET", "/api/admin/nodes/1/vless-route-lines", true}, {"GET", "/api/admin/nodes/1/vless-route-outbounds", false}, {"PUT", "/api/admin/nodes/1/vless-route-lines", false}} {
			expected := stdhttp.StatusServiceUnavailable // Authorized requests reach the intentionally absent service.
			if id == 0 {
				expected = stdhttp.StatusUnauthorized
			} else if id == 3 || id == 2 && !endpoint.staffRead {
				expected = stdhttp.StatusForbidden
			}
			req := httptest.NewRequest(endpoint.method, endpoint.path, strings.NewReader(`{"revision":0,"lines":[]}`))
			req.Header.Set("Content-Type", "application/json")
			if token != "" {
				req.Header.Set("Authorization", "Bearer "+token)
			}
			w := httptest.NewRecorder()
			router.ServeHTTP(w, req)
			if w.Code != expected {
				t.Fatalf("user %d %s %s: %d want %d (%s)", id, endpoint.method, endpoint.path, w.Code, expected, w.Body.String())
			}
		}
	}
}

func TestVlessRouteWritesRespectRequiredSecondFactorEnrollment(t *testing.T) {
	gin.SetMode(gin.TestMode)
	admin := &domain.User{ID: 1, UPN: "admin", Enabled: true, Role: domain.RoleAdmin, PasswordHash: "fixture-local-password"}
	users := routerReleaseUsers{users: map[int64]*domain.User{1: admin}}
	issuer := jwtutil.NewIssuer(strings.Repeat("test-only-key", 3), func() jwtutil.Params {
		return jwtutil.Params{AccessTTL: time.Hour, RefreshTTL: time.Hour, Issuer: "route-2fa-test"}
	})
	authSvc := auth.New(issuer)
	token, _, err := authSvc.IssueTokens(admin)
	if err != nil {
		t.Fatal(err)
	}
	router := NewRouter(Deps{Cfg: &config.Config{ConfigDir: t.TempDir()}, Repos: ports.Repos{User: users, Settings: &dispatchSettingsRepo{}, ScopedSettings: vlessRouteScopedSettings{}}, Auth: authSvc, User: user.New(users, nil, nil, nil, nil, nil, nil, nil)})
	req := httptest.NewRequest("PUT", "/api/admin/nodes/1/vless-route-lines", strings.NewReader(`{"revision":0,"lines":[]}`))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != stdhttp.StatusForbidden || !strings.Contains(w.Body.String(), "2fa_enrollment_required") {
		t.Fatalf("write bypassed enrollment gate: %d %s", w.Code, w.Body.String())
	}
}
