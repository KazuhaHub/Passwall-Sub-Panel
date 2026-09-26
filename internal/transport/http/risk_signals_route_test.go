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
	"github.com/KazuhaHub/passwall-sub-panel/internal/service/user"
)

type routerRiskSignals struct{ calls int }

func (r *routerRiskSignals) List(context.Context) ([]domain.RiskSignal, error) {
	r.calls++
	return []domain.RiskSignal{{UserID: 7, Kind: domain.RiskKindDevices, State: domain.GeoStateFlagged, Code: domain.RiskCodeOver}}, nil
}

// THE RISK VIEW IS THE OWNER'S, NOT AN OPERATOR'S. It names accounts on
// signals rather than proof, which is why it sits on adminGroup beside the
// Geo tab — and staffGroup shares the /api/admin prefix, so the path alone
// says nothing about the gate. Only a request through the assembled router
// shows which group the route landed in, and the gate is only real if a
// refused request never reaches the store.
func TestRiskSignalsRouteIsAdminOnly(t *testing.T) {
	gin.SetMode(gin.TestMode)
	admin := &domain.User{ID: 1, UPN: "admin@example.test", Enabled: true, Role: domain.RoleAdmin}
	operator := &domain.User{ID: 2, UPN: "operator@example.test", Enabled: true, Role: domain.RoleOperator}
	users := routerReleaseUsers{users: map[int64]*domain.User{1: admin, 2: operator}}
	issuer := jwtutil.NewIssuer(strings.Repeat("test-only-key", 3), func() jwtutil.Params {
		return jwtutil.Params{AccessTTL: time.Hour, RefreshTTL: time.Hour, Issuer: "risk-signals-test"}
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
	lister := &routerRiskSignals{}
	router := NewRouter(Deps{
		Cfg:   &config.Config{ConfigDir: t.TempDir()},
		Repos: ports.Repos{User: users, Settings: &dispatchSettingsRepo{}},
		Auth:  authSvc, User: user.New(users, nil, nil, nil, nil, nil, nil, nil), RiskSignals: lister,
	})
	for _, test := range []struct {
		name, token string
		status      int
	}{
		{"anonymous", "", stdhttp.StatusUnauthorized},
		{"operator", operatorToken, stdhttp.StatusForbidden},
		{"administrator", adminToken, stdhttp.StatusOK},
	} {
		t.Run(test.name, func(t *testing.T) {
			before := lister.calls
			req := httptest.NewRequest(stdhttp.MethodGet, "https://panel.example/api/admin/risk-signals", nil)
			if test.token != "" {
				req.Header.Set("Authorization", "Bearer "+test.token)
			}
			w := httptest.NewRecorder()
			router.ServeHTTP(w, req)
			if w.Code != test.status {
				t.Fatalf("GET /api/admin/risk-signals as %s = %d, want %d: %s", test.name, w.Code, test.status, w.Body.String())
			}
			switch test.status {
			case stdhttp.StatusOK:
				if lister.calls != before+1 || !strings.Contains(w.Body.String(), `"user_id":7`) {
					t.Fatalf("the administrator's request did not read the store: calls %d→%d, body %s", before, lister.calls, w.Body.String())
				}
			default:
				if lister.calls != before {
					t.Fatalf("a refused request reached the store")
				}
			}
		})
	}
}
