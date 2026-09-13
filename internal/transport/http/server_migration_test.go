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

type routerMigrationPreview struct{ calls int }

func (f *routerMigrationPreview) Preview(_ context.Context, id int64, _ string, _ bool) (*domain.ServerMigrationPreview, error) {
	f.calls++
	return &domain.ServerMigrationPreview{ServerID: id, Blockers: []domain.MigrationIssue{}, Warnings: []domain.MigrationIssue{}}, nil
}

func TestServerMigrationRouterAdministratorReadOnlyBoundary(t *testing.T) {
	gin.SetMode(gin.TestMode)
	admin := &domain.User{ID: 1, UPN: "admin@example.test", Enabled: true, Role: domain.RoleAdmin}
	operator := &domain.User{ID: 2, UPN: "operator@example.test", Enabled: true, Role: domain.RoleOperator}
	users := routerReleaseUsers{users: map[int64]*domain.User{1: admin, 2: operator}}
	issuer := jwtutil.NewIssuer(strings.Repeat("test-only-key", 3), func() jwtutil.Params {
		return jwtutil.Params{AccessTTL: time.Hour, RefreshTTL: time.Hour, Issuer: "migration-test"}
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
	preview := &routerMigrationPreview{}
	router := NewRouter(Deps{
		Cfg:   &config.Config{ConfigDir: t.TempDir()},
		Repos: ports.Repos{User: users, Settings: &dispatchSettingsRepo{}},
		Auth:  authSvc, User: user.New(users, nil, nil, nil, nil, nil, nil, nil), ServerMigration: preview,
	})
	for _, test := range []struct {
		token  string
		status int
	}{{"", stdhttp.StatusUnauthorized}, {operatorToken, stdhttp.StatusForbidden}, {adminToken, stdhttp.StatusOK}} {
		w := httptest.NewRecorder()
		req := httptest.NewRequest(stdhttp.MethodGet, "https://panel.example/api/admin/servers/12/node-migration-preview", nil)
		if test.token != "" {
			req.Header.Set("Authorization", "Bearer "+test.token)
		}
		router.ServeHTTP(w, req)
		if w.Code != test.status {
			t.Fatalf("code=%d want=%d body=%s", w.Code, test.status, w.Body.String())
		}
	}
	if preview.calls != 1 {
		t.Fatalf("unauthorized request reached preview %d times", preview.calls)
	}
	w := httptest.NewRecorder()
	req := httptest.NewRequest(stdhttp.MethodPost, "https://panel.example/api/admin/servers/12/node-migration-preview", nil)
	req.Header.Set("Authorization", "Bearer "+adminToken)
	router.ServeHTTP(w, req)
	if w.Code != stdhttp.StatusMethodNotAllowed || preview.calls != 1 {
		t.Fatalf("live conversion write exposed: %d %s", w.Code, w.Body.String())
	}
}
