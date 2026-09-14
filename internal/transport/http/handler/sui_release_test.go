package handler

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/pkg/jwtutil"
	"github.com/KazuhaHub/passwall-sub-panel/internal/transport/http/middleware"
	"github.com/KazuhaHub/passwall-sub-panel/internal/version"
	"github.com/gin-gonic/gin"
)

func suiReleaseRequest(role domain.Role, enroll bool, ctx context.Context) *httptest.ResponseRecorder {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	group := router.Group("/api/admin", func(c *gin.Context) {
		if role != "" {
			c.Set(middleware.CtxClaims, &jwtutil.Claims{UserID: 1, Role: role})
		}
		c.Next()
	}, middleware.RequireRole(domain.RoleAdmin), middleware.Require2FAEnrollment(releaseEnrollmentStub{enroll}, releaseEnrollmentStub{enroll}))
	// Nil repositories prove that this public-metadata endpoint neither reads
	// node credentials nor creates upgrade tasks, servers or installation files.
	h := NewAdminServersHandler(nil, nil, nil, nil, nil, nil)
	group.GET("/servers/sui-release", h.GetSUIRelease)
	req := httptest.NewRequest(http.MethodGet, "https://panel.example/api/admin/servers/sui-release", nil).WithContext(ctx)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	return w
}

func TestSUIReleaseMetadataAdministratorAndEnrollmentGates(t *testing.T) {
	old := version.LatestSUI()
	t.Cleanup(func() { version.SetLatestSUI(old) })
	version.SetLatestSUI("v1.6.2")
	for _, tc := range []struct {
		role   domain.Role
		enroll bool
		status int
	}{
		{"", false, http.StatusUnauthorized},
		{domain.RoleUser, false, http.StatusForbidden},
		{domain.RoleOperator, false, http.StatusForbidden},
		{domain.RoleAdmin, true, http.StatusForbidden},
		{domain.RoleAdmin, false, http.StatusOK},
	} {
		w := suiReleaseRequest(tc.role, tc.enroll, context.Background())
		if w.Code != tc.status {
			t.Fatalf("role=%q enroll=%v response=%d %s", tc.role, tc.enroll, w.Code, w.Body.String())
		}
		if tc.status == http.StatusOK && (w.Body.String() != `{"version":"v1.6.2"}` ||
			w.Header().Get("Cache-Control") != "private, no-store" || w.Header().Get("X-Content-Type-Options") != "nosniff") {
			t.Fatalf("metadata contract/headers: %s %v", w.Body.String(), w.Header())
		}
	}
}

func TestSUIReleaseMetadataUnavailableIsGenericNotEmptySuccess(t *testing.T) {
	old := version.LatestSUI()
	t.Cleanup(func() { version.SetLatestSUI(old) })
	version.SetLatestSUI("")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	w := suiReleaseRequest(domain.RoleAdmin, false, ctx)
	if w.Code != http.StatusServiceUnavailable || w.Body.String() != `{"error":"S-UI release metadata is unavailable"}` ||
		w.Header().Get("Cache-Control") != "private, no-store" || w.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Fatalf("unavailable metadata response=%d %s headers=%v", w.Code, w.Body.String(), w.Header())
	}
}
