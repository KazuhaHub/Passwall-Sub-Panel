package handler

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/pkg/jwtutil"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
	"github.com/KazuhaHub/passwall-sub-panel/internal/transport/http/middleware"
	"github.com/gin-gonic/gin"
)

type releaseCatalogStub struct {
	result ports.NodeReleaseList
	err    error
	calls  int
	ctx    context.Context
}

func (s *releaseCatalogStub) List(ctx context.Context) (ports.NodeReleaseList, error) {
	s.calls++
	s.ctx = ctx
	return s.result, s.err
}

type releaseEnrollmentStub struct{ required bool }

func (s releaseEnrollmentStub) MustEnroll(context.Context, *domain.User) (bool, error) {
	return s.required, nil
}

func (s releaseEnrollmentStub) Get(context.Context, int64) (*domain.User, error) {
	return &domain.User{ID: 1, Role: domain.RoleAdmin}, nil
}

func releaseCatalogRequest(h *AdminServersHandler, role domain.Role, enroll bool, ctx context.Context) *httptest.ResponseRecorder {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	group := router.Group("/api/admin", func(c *gin.Context) {
		if role != "" {
			c.Set(middleware.CtxClaims, &jwtutil.Claims{UserID: 1, Role: role})
		}
		c.Next()
	}, middleware.RequireRole(domain.RoleAdmin), middleware.Require2FAEnrollment(releaseEnrollmentStub{enroll}, releaseEnrollmentStub{enroll}))
	group.GET("/servers/node-releases", h.ListNodeReleases)
	req := httptest.NewRequest(http.MethodGet, "https://panel.example/api/admin/servers/node-releases", nil).WithContext(ctx)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	return w
}

func TestNodeReleaseCatalogAdministratorAndEnrollmentGates(t *testing.T) {
	for _, test := range []struct {
		name   string
		role   domain.Role
		enroll bool
		status int
	}{
		{"anonymous", "", false, http.StatusUnauthorized},
		{"user", domain.RoleUser, false, http.StatusForbidden},
		{"operator", domain.RoleOperator, false, http.StatusForbidden},
		{"unenrolled administrator", domain.RoleAdmin, true, http.StatusForbidden},
		{"administrator", domain.RoleAdmin, false, http.StatusOK},
	} {
		t.Run(test.name, func(t *testing.T) {
			catalog := &releaseCatalogStub{}
			// Nil server/credential repositories make any accidental credential
			// dependency fail this test rather than silently minting credentials.
			h := NewAdminServersHandler(nil, nil, nil, nil, nil, nil).WithNodeReleaseCatalog(catalog)
			w := releaseCatalogRequest(h, test.role, test.enroll, context.Background())
			if w.Code != test.status {
				t.Fatalf("status = %d, want %d", w.Code, test.status)
			}
			wantCalls := 0
			if test.status == http.StatusOK {
				wantCalls = 1
			}
			if catalog.calls != wantCalls {
				t.Fatalf("unauthorized metadata lookup: calls = %d, want %d", catalog.calls, wantCalls)
			}
		})
	}
}

func TestNodeReleaseCatalogPrivateMetadataAndRequestContext(t *testing.T) {
	type contextKey struct{}
	ctx := context.WithValue(context.Background(), contextKey{}, "request-scoped")
	checked := time.Date(2026, 9, 12, 10, 0, 0, 0, time.UTC)
	catalog := &releaseCatalogStub{result: ports.NodeReleaseList{
		CheckedAt: checked,
		Releases: []ports.NodeReleaseCatalogEntry{{
			Version: "v0.0.1-beta3", Channel: "testing", PublishedAt: checked.Add(-time.Hour),
			ReleaseURL: "https://github.com/KazuhaHub/Passwall-Node/releases/tag/v0.0.1-beta3", Notes: "Reviewed installation and protocol compatibility.",
			Methods: []string{"manual"}, Platforms: []ports.NodeReleasePlatform{{OS: "windows", Arch: "arm64"}},
		}},
	}}
	h := NewAdminServersHandler(nil, nil, nil, nil, nil, nil).WithNodeReleaseCatalog(catalog)
	w := releaseCatalogRequest(h, domain.RoleAdmin, false, ctx)
	if w.Code != http.StatusOK || w.Header().Get("Cache-Control") != "private, no-store" || w.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Fatalf("metadata response is not private: status=%d headers=%v", w.Code, w.Header())
	}
	if catalog.ctx.Value(contextKey{}) != "request-scoped" {
		t.Fatal("request context was not passed to the catalog")
	}
	var result ports.NodeReleaseList
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(result, catalog.result) {
		t.Fatalf("response changed catalog metadata: %s", w.Body.String())
	}
	var object map[string]json.RawMessage
	if err := json.Unmarshal(w.Body.Bytes(), &object); err != nil {
		t.Fatal(err)
	}
	if len(object) != 2 || object["releases"] == nil || object["checked_at"] == nil {
		t.Fatalf("unexpected catalog contract: %s", w.Body.String())
	}
}

func TestNodeReleaseCatalogUnavailableIsGenericAndEmptyIsExplicit(t *testing.T) {
	for _, catalog := range []ports.NodeReleaseCatalog{
		nil,
		&releaseCatalogStub{err: errors.New("https://internal.example/?credential=must-not-appear response-secret")},
	} {
		h := NewAdminServersHandler(nil, nil, nil, nil, nil, nil).WithNodeReleaseCatalog(catalog)
		w := releaseCatalogRequest(h, domain.RoleAdmin, false, context.Background())
		if w.Code != http.StatusServiceUnavailable || w.Body.String() != `{"error":"Node release catalog is unavailable"}` || !strings.Contains(w.Header().Get("Cache-Control"), "no-store") {
			t.Fatalf("source failure was not a generic private 503: status=%d body=%s", w.Code, w.Body.String())
		}
	}
	h := NewAdminServersHandler(nil, nil, nil, nil, nil, nil).WithNodeReleaseCatalog(&releaseCatalogStub{})
	w := releaseCatalogRequest(h, domain.RoleAdmin, false, context.Background())
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"releases":[]`) {
		t.Fatalf("legitimately empty catalog must be an explicit array: %s", w.Body.String())
	}
}
