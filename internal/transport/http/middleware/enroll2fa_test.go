package middleware

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/pkg/jwtutil"
)

type stubChecker struct {
	must bool
	err  error
}

func (s stubChecker) MustEnroll(context.Context, *domain.User) (bool, error) { return s.must, s.err }

type stubUsers struct {
	u   *domain.User
	err error
}

func (s stubUsers) Get(context.Context, int64) (*domain.User, error) { return s.u, s.err }

// run wires RequireAuth's claims into the context manually (the gate runs after
// RequireAuth) and invokes the gate against a route registered at fullPath.
func run(t *testing.T, checker EnrollChecker, users UserLookup, fullPath, reqPath string) int {
	t.Helper()
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(func(c *gin.Context) {
		c.Set(CtxClaims, &jwtutil.Claims{UserID: 1})
		c.Next()
	})
	r.Use(Require2FAEnrollment(checker, users))
	r.GET(fullPath, func(c *gin.Context) { c.Status(http.StatusOK) })
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, reqPath, nil)
	r.ServeHTTP(w, req)
	return w.Code
}

func okUsers() stubUsers { return stubUsers{u: &domain.User{ID: 1, PasswordHash: "x"}} }

func TestEnrollGate_BlocksWhenMustEnroll(t *testing.T) {
	if code := run(t, stubChecker{must: true}, okUsers(), "/api/user/me/rules", "/api/user/me/rules"); code != http.StatusForbidden {
		t.Fatalf("a gated user on a non-allowlisted route should 403, got %d", code)
	}
}

func TestEnrollGate_AllowsEnrollmentRoutes(t *testing.T) {
	for _, p := range []string{"/api/user/me", "/api/user/me/2fa/begin", "/api/user/me/2fa/enable", "/api/user/me/passkeys", "/api/user/me/passkeys/begin", "/api/user/me/passkeys/finish"} {
		if code := run(t, stubChecker{must: true}, okUsers(), p, p); code != http.StatusOK {
			t.Fatalf("allowlisted enrollment route %s must pass even when gated, got %d", p, code)
		}
	}
}

func TestEnrollGate_PassesWhenNotRequired(t *testing.T) {
	if code := run(t, stubChecker{must: false}, okUsers(), "/api/user/me/rules", "/api/user/me/rules"); code != http.StatusOK {
		t.Fatalf("a non-required user should pass, got %d", code)
	}
}

func TestEnrollGate_FailsOpenOnError(t *testing.T) {
	// Deliberate fail-open: a lookup/eval error must not 403 (availability over
	// strictness for this enrollment nudge; RequireAuth is the real boundary).
	if code := run(t, stubChecker{}, stubUsers{err: errors.New("db blip")}, "/api/user/me/rules", "/api/user/me/rules"); code != http.StatusOK {
		t.Fatalf("user-lookup error should fail open, got %d", code)
	}
	if code := run(t, stubChecker{err: errors.New("settings blip")}, okUsers(), "/api/user/me/rules", "/api/user/me/rules"); code != http.StatusOK {
		t.Fatalf("policy-eval error should fail open, got %d", code)
	}
}
