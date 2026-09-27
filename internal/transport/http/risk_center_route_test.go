package http

import (
	"context"
	stdhttp "net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/KazuhaHub/passwall-sub-panel/internal/config"
	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/pkg/jwtutil"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
	"github.com/KazuhaHub/passwall-sub-panel/internal/service/auth"
	"github.com/KazuhaHub/passwall-sub-panel/internal/service/riskcenter"
	"github.com/KazuhaHub/passwall-sub-panel/internal/service/user"
)

// routerRiskCenter counts every call, whichever route made it.
type routerRiskCenter struct{ calls int }

func (r *routerRiskCenter) Live(context.Context, riskcenter.LiveQuery) (riskcenter.LiveView, error) {
	r.calls++
	return riskcenter.LiveView{Users: []riskcenter.LiveUser{{UserID: 7, UPN: "alice"}}, Total: 1}, nil
}

func (r *routerRiskCenter) Refresh(context.Context) (riskcenter.RefreshResult, error) {
	r.calls++
	return riskcenter.RefreshResult{Refreshed: true, Snapshot: &domain.LiveConnSnapshot{Source: domain.LiveSnapshotFromRefresh}}, nil
}

func (r *routerRiskCenter) History(context.Context, ports.ConnectionHistoryFilter) ([]domain.ConnectionRecord, map[int64]string, int64, error) {
	r.calls++
	return []domain.ConnectionRecord{{UserID: 7, IP: "203.0.113.7"}}, nil, 1, nil
}

func (r *routerRiskCenter) Flags(context.Context, ports.FlagRecordFilter) ([]domain.FlagRecord, int64, error) {
	r.calls++
	return []domain.FlagRecord{{ID: 1, UserID: 7}}, 1, nil
}

// THE RISK CENTER IS THE OWNER'S. It lists accounts beside their IP
// addresses — live, and for up to 90 days in the connection history — and
// every change the detectors made about them, on signals rather than proof.
// staffGroup shares the /api/admin prefix, so a path says nothing about its
// gate: only a request through the assembled router shows which group each
// route landed in, and the gate is only real if a refused request never
// reaches the service. All four routes, each as anonymous, operator and
// administrator.
func TestRiskCenterRoutesAreAdminOnly(t *testing.T) {
	gin.SetMode(gin.TestMode)
	admin := &domain.User{ID: 1, UPN: "admin@example.test", Enabled: true, Role: domain.RoleAdmin}
	operator := &domain.User{ID: 2, UPN: "operator@example.test", Enabled: true, Role: domain.RoleOperator}
	users := routerReleaseUsers{users: map[int64]*domain.User{1: admin, 2: operator}}
	issuer := jwtutil.NewIssuer(strings.Repeat("test-only-key", 3), func() jwtutil.Params {
		return jwtutil.Params{AccessTTL: time.Hour, RefreshTTL: time.Hour, Issuer: "risk-center-test"}
	})
	authSvc := auth.New(issuer)
	adminToken, _, err := authSvc.IssueTokens(admin)
	if err != nil {
		t.Fatal(err)
	}
	operatorToken, _, err := authSvc.IssueTokens(operator)
	if err != nil {
		t.Fatal(err)
	}
	svc := &routerRiskCenter{}
	router := NewRouter(Deps{
		Cfg:   &config.Config{ConfigDir: t.TempDir()},
		Repos: ports.Repos{User: users, Settings: &dispatchSettingsRepo{}},
		Auth:  authSvc, User: user.New(users, nil, nil, nil, nil, nil, nil, nil), RiskCenter: svc,
	})
	for _, route := range []struct{ method, path, body string }{
		{stdhttp.MethodGet, "/api/admin/risk-center/live", `"upn":"alice"`},
		{stdhttp.MethodPost, "/api/admin/risk-center/live/refresh", `"refreshed":true`},
		{stdhttp.MethodGet, "/api/admin/risk-center/connections", `"ip":"203.0.113.7"`},
		{stdhttp.MethodGet, "/api/admin/risk-center/flags", `"id":1`},
	} {
		for _, test := range []struct {
			name, token string
			status      int
		}{
			{"anonymous", "", stdhttp.StatusUnauthorized},
			{"operator", operatorToken, stdhttp.StatusForbidden},
			{"administrator", adminToken, stdhttp.StatusOK},
		} {
			t.Run(route.method+" "+route.path+" as "+test.name, func(t *testing.T) {
				before := svc.calls
				req := httptest.NewRequest(route.method, "https://panel.example"+route.path, nil)
				if test.token != "" {
					req.Header.Set("Authorization", "Bearer "+test.token)
				}
				w := httptest.NewRecorder()
				router.ServeHTTP(w, req)
				if w.Code != test.status {
					t.Fatalf("%s %s as %s = %d, want %d: %s", route.method, route.path, test.name, w.Code, test.status, w.Body.String())
				}
				switch test.status {
				case stdhttp.StatusOK:
					if svc.calls != before+1 || !strings.Contains(w.Body.String(), route.body) {
						t.Fatalf("the administrator's request did not reach the service: calls %d→%d, body %s", before, svc.calls, w.Body.String())
					}
				default:
					if svc.calls != before {
						t.Fatal("a refused request reached the service")
					}
				}
			})
		}
	}
}
