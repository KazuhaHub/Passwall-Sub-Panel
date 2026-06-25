package handler

import (
	"context"
	"errors"
	"net/http/httptest"
	"testing"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

// errOnGetUserRepo errors on GetByID; every other UserRepo method is the nil
// embedded interface (operatorMayView only ever calls GetByID).
type errOnGetUserRepo struct{ ports.UserRepo }

func (errOnGetUserRepo) GetByID(context.Context, int64) (*domain.User, error) {
	return nil, errors.New("db down")
}

// operatorMayView must FAIL CLOSED when the target lookup errors: an operator
// caller must NOT be allowed to read a user's traffic just because the role
// check couldn't be evaluated (a transient DB error). Mirrors the fail-closed
// posture of ensureOperatorAllowed — a guard that can't prove access is allowed
// denies. Otherwise a DB blip lets an operator read an admin/operator's usage.
func TestOperatorMayView_FailsClosedOnLookupError(t *testing.T) {
	c, rr := claimsCtx(domain.RoleOperator)
	c.Request = httptest.NewRequest("GET", "/api/admin/traffic/user/1/servers", nil)
	h := &AdminTrafficHandler{users: errOnGetUserRepo{}}
	if h.operatorMayView(c, 1) {
		t.Fatal("operatorMayView must return false (deny) when the target lookup errors")
	}
	if rr.Code < 400 {
		t.Fatalf("a denied view must write a non-2xx status, got %d", rr.Code)
	}
}

// Admins bypass the operator gate entirely — no target lookup, always allowed
// (so the fail-closed change can't regress the admin path).
func TestOperatorMayView_AdminBypassesLookup(t *testing.T) {
	c, _ := claimsCtx(domain.RoleAdmin)
	h := &AdminTrafficHandler{users: errOnGetUserRepo{}} // would error if consulted
	if !h.operatorMayView(c, 1) {
		t.Fatal("admin caller must be allowed without a target lookup")
	}
}
