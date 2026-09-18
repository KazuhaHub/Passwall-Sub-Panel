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
	"github.com/KazuhaHub/passwall-sub-panel/internal/service/nodediagnostics"
	"github.com/KazuhaHub/passwall-sub-panel/internal/service/user"
)

type routerDiagnostics struct {
	calls   int
	created bool
}

func (f *routerDiagnostics) Create(context.Context, int64, nodediagnostics.Request) (*nodediagnostics.Status, bool, error) {
	f.calls++
	return &nodediagnostics.Status{TaskID: "task-1", Status: domain.NodeAgentTaskQueued}, f.created, nil
}

func (f *routerDiagnostics) Get(context.Context, int64, string) (*nodediagnostics.Status, error) {
	f.calls++
	return &nodediagnostics.Status{TaskID: "task-1", Status: domain.NodeAgentTaskQueued}, nil
}

// SECTION 13.5 RETURNS A DIAGNOSTIC TO ADMINISTRATORS ALONE, and the result is
// the whole point of the endpoint: an operator who can see the server list must
// not be able to read the machine's detail through it.
func TestNodeDiagnosticsRouterAdministratorOnlyBoundary(t *testing.T) {
	gin.SetMode(gin.TestMode)
	admin := &domain.User{ID: 1, UPN: "admin@example.test", Enabled: true, Role: domain.RoleAdmin}
	operator := &domain.User{ID: 2, UPN: "operator@example.test", Enabled: true, Role: domain.RoleOperator}
	users := routerReleaseUsers{users: map[int64]*domain.User{1: admin, 2: operator}}
	issuer := jwtutil.NewIssuer(strings.Repeat("test-only-key", 3), func() jwtutil.Params {
		return jwtutil.Params{AccessTTL: time.Hour, RefreshTTL: time.Hour, Issuer: "diagnostics-test"}
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
	service := &routerDiagnostics{created: true}
	router := NewRouter(Deps{
		Cfg:   &config.Config{ConfigDir: t.TempDir()},
		Repos: ports.Repos{User: users, Settings: &dispatchSettingsRepo{}},
		Auth:  authSvc, User: user.New(users, nil, nil, nil, nil, nil, nil, nil), NodeDiagnostics: service,
	})
	for _, test := range []struct {
		name, method, path, token string
		status                    int
	}{
		{"anonymous cannot read a result", stdhttp.MethodGet, "/api/admin/servers/12/node-diagnostics/task-1", "", stdhttp.StatusUnauthorized},
		{"an operator cannot read a result", stdhttp.MethodGet, "/api/admin/servers/12/node-diagnostics/task-1", operatorToken, stdhttp.StatusForbidden},
		{"an operator cannot start a collection", stdhttp.MethodPost, "/api/admin/servers/12/node-diagnostics", operatorToken, stdhttp.StatusForbidden},
		{"an administrator can read a result", stdhttp.MethodGet, "/api/admin/servers/12/node-diagnostics/task-1", adminToken, stdhttp.StatusOK},
		{"an administrator can start one", stdhttp.MethodPost, "/api/admin/servers/12/node-diagnostics", adminToken, stdhttp.StatusAccepted},
	} {
		t.Run(test.name, func(t *testing.T) {
			before := service.calls
			w := httptest.NewRecorder()
			req := httptest.NewRequest(test.method, "https://panel.example"+test.path, strings.NewReader(`{}`))
			req.Header.Set("Content-Type", "application/json")
			if test.token != "" {
				req.Header.Set("Authorization", "Bearer "+test.token)
			}
			router.ServeHTTP(w, req)
			if w.Code != test.status {
				t.Fatalf("code=%d want=%d body=%s", w.Code, test.status, w.Body.String())
			}
			// The gate is only real if the service was never reached.
			if test.status == stdhttp.StatusForbidden || test.status == stdhttp.StatusUnauthorized {
				if service.calls != before {
					t.Fatalf("a refused request reached the service")
				}
			}
		})
	}
}

// A MERGED REQUEST IS NOT A NEW COLLECTION, and the status code has to say so:
// answering "accepted" would tell an operator that something started when the
// panel only handed back what was already running.
func TestNodeDiagnosticsReportsAMergeAsOKNotAccepted(t *testing.T) {
	gin.SetMode(gin.TestMode)
	admin := &domain.User{ID: 1, UPN: "admin@example.test", Enabled: true, Role: domain.RoleAdmin}
	users := routerReleaseUsers{users: map[int64]*domain.User{1: admin}}
	issuer := jwtutil.NewIssuer(strings.Repeat("test-only-key", 3), func() jwtutil.Params {
		return jwtutil.Params{AccessTTL: time.Hour, RefreshTTL: time.Hour, Issuer: "diagnostics-test"}
	})
	authSvc := auth.New(issuer)
	adminToken, _, err := authSvc.IssueTokens(admin)
	if err != nil {
		t.Fatal(err)
	}
	service := &routerDiagnostics{created: false}
	router := NewRouter(Deps{
		Cfg:   &config.Config{ConfigDir: t.TempDir()},
		Repos: ports.Repos{User: users, Settings: &dispatchSettingsRepo{}},
		Auth:  authSvc, User: user.New(users, nil, nil, nil, nil, nil, nil, nil), NodeDiagnostics: service,
	})
	w := httptest.NewRecorder()
	req := httptest.NewRequest(stdhttp.MethodPost, "https://panel.example/api/admin/servers/12/node-diagnostics", strings.NewReader(`{}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+adminToken)
	router.ServeHTTP(w, req)
	if w.Code != stdhttp.StatusOK {
		t.Fatalf("a merged request answered %d, want 200", w.Code)
	}
}
