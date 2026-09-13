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

type routerReleaseCatalog struct{ calls int }

func (c *routerReleaseCatalog) List(context.Context) (ports.NodeReleaseList, error) {
	c.calls++
	return ports.NodeReleaseList{Releases: []ports.NodeReleaseCatalogEntry{}, CheckedAt: time.Now().UTC()}, nil
}

type routerReleaseUsers struct {
	ports.UserRepo
	users map[int64]*domain.User
}

func (r routerReleaseUsers) GetByID(_ context.Context, id int64) (*domain.User, error) {
	u := r.users[id]
	if u == nil {
		return nil, domain.ErrNotFound
	}
	return u, nil
}

func TestNodeReleaseCatalogRouterWiringAndStaticPath(t *testing.T) {
	gin.SetMode(gin.TestMode)
	u := &domain.User{ID: 1, UPN: "admin@example.test", Enabled: true, Role: domain.RoleAdmin}
	operator := &domain.User{ID: 2, UPN: "operator@example.test", Enabled: true, Role: domain.RoleOperator}
	users := routerReleaseUsers{users: map[int64]*domain.User{1: u, 2: operator}}
	issuer := jwtutil.NewIssuer(strings.Repeat("test-only-signing-key", 2), func() jwtutil.Params {
		return jwtutil.Params{AccessTTL: time.Hour, RefreshTTL: time.Hour, Issuer: "catalog-router-test"}
	})
	authService := auth.New(issuer)
	adminToken, _, err := authService.IssueTokens(u)
	if err != nil {
		t.Fatal(err)
	}
	operatorToken, _, err := authService.IssueTokens(operator)
	if err != nil {
		t.Fatal(err)
	}
	catalog := &routerReleaseCatalog{}
	router := NewRouter(Deps{
		Cfg:   &config.Config{ConfigDir: t.TempDir()},
		Repos: ports.Repos{User: users, Settings: &dispatchSettingsRepo{}},
		Auth:  authService, User: user.New(users, nil, nil, nil, nil, nil, nil, nil), NodeReleases: catalog,
	})
	for _, test := range []struct {
		name   string
		token  string
		status int
	}{
		{"anonymous", "", stdhttp.StatusUnauthorized},
		{"operator", operatorToken, stdhttp.StatusForbidden},
		{"administrator", adminToken, stdhttp.StatusOK},
	} {
		t.Run(test.name, func(t *testing.T) {
			req := httptest.NewRequest(stdhttp.MethodGet, "https://panel.example/api/admin/servers/node-releases", nil)
			if test.token != "" {
				req.Header.Set("Authorization", "Bearer "+test.token)
			}
			w := httptest.NewRecorder()
			router.ServeHTTP(w, req)
			if w.Code != test.status {
				t.Fatalf("static catalog route status = %d, want %d: %s", w.Code, test.status, w.Body.String())
			}
		})
	}
	if catalog.calls != 1 {
		t.Fatalf("catalog lookup calls = %d, want 1 authorized metadata lookup", catalog.calls)
	}
}
