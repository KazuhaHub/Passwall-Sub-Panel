package handler

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/pkg/jwtutil"
	"github.com/KazuhaHub/passwall-sub-panel/internal/transport/http/middleware"
)

// claimsCtx returns a test gin.Context with the given role attached as JWT
// claims, matching how middleware.RequireAuth would populate it. Used to
// drive the operator/admin permission guards.
func claimsCtx(role domain.Role) (*gin.Context, *httptest.ResponseRecorder) {
	gin.SetMode(gin.TestMode)
	rr := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rr)
	if role != "" {
		c.Set(middleware.CtxClaims, &jwtutil.Claims{Role: role, UPN: "tester"})
	}
	return c, rr
}

func TestGuardOperatorRoleAssignment_AdminCanAssignAnything(t *testing.T) {
	// Sanity: admins are never blocked, regardless of target role.
	for _, r := range []domain.Role{domain.RoleUser, domain.RoleOperator, domain.RoleAdmin} {
		c, rr := claimsCtx(domain.RoleAdmin)
		if !guardOperatorRoleAssignment(c, r) {
			t.Errorf("admin caller blocked when assigning role=%q", r)
		}
		if rr.Code != 200 && rr.Code != 0 {
			t.Errorf("admin caller wrote status %d, expected no response", rr.Code)
		}
	}
}

func TestGuardOperatorRoleAssignment_OperatorCanOnlyAssignUser(t *testing.T) {
	// Operator promoting another account to operator OR admin would be a
	// self-elevation path — must be blocked at the handler regardless of
	// what the UI shows.
	cases := []struct {
		role    domain.Role
		allowed bool
	}{
		{domain.RoleUser, true},
		{domain.RoleOperator, false},
		{domain.RoleAdmin, false},
	}
	for _, tc := range cases {
		c, rr := claimsCtx(domain.RoleOperator)
		got := guardOperatorRoleAssignment(c, tc.role)
		if got != tc.allowed {
			t.Errorf("operator -> role=%q: got allowed=%v, want %v", tc.role, got, tc.allowed)
		}
		if !tc.allowed && rr.Code != http.StatusForbidden {
			t.Errorf("operator -> role=%q: status=%d, want 403", tc.role, rr.Code)
		}
	}
}

func TestGuardOperatorRoleAssignment_NoClaimsTreatedAsAllowed(t *testing.T) {
	// The guard short-circuits when claims are absent so it doesn't
	// double-up against the RequireAuth middleware. The auth gate is
	// supposed to reject these before they reach the handler — if we
	// blocked here too the error message would be wrong.
	c, _ := claimsCtx("") // no claims set
	if !guardOperatorRoleAssignment(c, domain.RoleAdmin) {
		t.Fatal("no-claims request must defer to RequireAuth, not 403 from this guard")
	}
}

func TestGuardOperatorRoleAssignment_EmptyRoleAllowed(t *testing.T) {
	// Update requests where the role field isn't being changed pass
	// targetRole="" — must not 403 (otherwise every operator update
	// would fail).
	c, _ := claimsCtx(domain.RoleOperator)
	if !guardOperatorRoleAssignment(c, "") {
		t.Fatal("empty targetRole means 'not changing role'; operator must be allowed")
	}
}
