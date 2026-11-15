package http

import (
	stdhttp "net/http"
	"testing"
)

// The administration of the legal documents is admin-only: publishing text
// every user will be shown and moving the consent version is the owner's
// decision. The router is assembled for real, so a route that slipped into
// staffGroup would answer an operator.
func TestLegalAdminRoutesAreAdminOnly(t *testing.T) {
	f := newDestFixture(t, false)
	for _, r := range []destRoute{
		{"GET", "/api/admin/legal/terms", ""},
		{"POST", "/api/admin/legal/terms", `{"locale":"zh-CN","content":"x","consent_bump":true}`},
		{"GET", "/api/admin/legal/privacy", ""},
		{"GET", "/api/admin/legal/affected-users", ""},
	} {
		if w := f.do(r.method, r.path, f.operatorToken, r.body); w.Code != stdhttp.StatusForbidden {
			t.Errorf("operator %s %s = %d, want 403: %s", r.method, r.path, w.Code, w.Body.String())
		}
		if w := f.do(r.method, r.path, "", r.body); w.Code != stdhttp.StatusUnauthorized {
			t.Errorf("anonymous %s %s = %d, want 401", r.method, r.path, w.Code)
		}
	}
}

// The public document needs no session: it is what the login and register
// pages link to. With nothing wired (and legal off) it is simply not there.
func TestLegalPublicRouteNeedsNoSession(t *testing.T) {
	f := newDestFixture(t, false)
	w := f.do("GET", "/api/legal/privacy?lang=zh-CN", "", "")
	if w.Code != stdhttp.StatusNotFound {
		t.Errorf("anonymous GET /api/legal/privacy = %d, want 404 (not 401): %s", w.Code, w.Body.String())
	}
}

// Accepting is for a signed-in account.
func TestLegalAcceptRouteNeedsASession(t *testing.T) {
	f := newDestFixture(t, false)
	if w := f.do("POST", "/api/user/me/legal/accept", "", `{"consent_version":1}`); w.Code != stdhttp.StatusUnauthorized {
		t.Errorf("anonymous accept = %d, want 401", w.Code)
	}
}
