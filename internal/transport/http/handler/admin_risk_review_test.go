package handler

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/pkg/jwtutil"
	"github.com/KazuhaHub/passwall-sub-panel/internal/service/riskreview"
	"github.com/KazuhaHub/passwall-sub-panel/internal/transport/http/middleware"
)

// fakeRiskReview is the review actions as the handler sees them: it
// records what it was asked and answers what the test set.
type fakeRiskReview struct {
	calls    int
	action   string
	userID   int64
	by       riskreview.Actor
	note     string
	expected domain.AttentionLevels
	resume   bool

	review domain.RiskReview
	trust  riskreview.TrustResult
	err    error
}

func (f *fakeRiskReview) record(action string, userID int64, by riskreview.Actor) {
	f.calls++
	f.action, f.userID, f.by = action, userID, by
}

func (f *fakeRiskReview) Dismiss(_ context.Context, userID int64, by riskreview.Actor, note string, expected domain.AttentionLevels) (domain.RiskReview, error) {
	f.record("dismiss", userID, by)
	f.note, f.expected = note, expected
	return f.review, f.err
}

func (f *fakeRiskReview) Undismiss(_ context.Context, userID int64, by riskreview.Actor) (domain.RiskReview, error) {
	f.record("undismiss", userID, by)
	return f.review, f.err
}

func (f *fakeRiskReview) Trust(_ context.Context, userID int64, by riskreview.Actor, resume bool) (riskreview.TrustResult, error) {
	f.record("trust", userID, by)
	f.resume = resume
	return f.trust, f.err
}

func (f *fakeRiskReview) Untrust(_ context.Context, userID int64, by riskreview.Actor) (domain.RiskReview, error) {
	f.record("untrust", userID, by)
	return f.review, f.err
}

// reviewClaims is the acting admin, as RequireAuth leaves the claims.
var reviewClaims = &jwtutil.Claims{UserID: 1, UPN: "owner@example.test", Role: domain.RoleAdmin}

// serveRiskReview serves one review route with the :id parameter, an
// optional raw body (none when empty) and the given claims (none when nil).
func serveRiskReview(t *testing.T, h func(*gin.Context), method, id, body string, claims *jwtutil.Claims) *httptest.ResponseRecorder {
	t.Helper()
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	var req *http.Request
	if body == "" {
		req = httptest.NewRequest(method, "/api/admin/risk-center/users/"+id+"/x", nil)
	} else {
		req = httptest.NewRequest(method, "/api/admin/risk-center/users/"+id+"/x", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
	}
	c.Request = req
	c.Params = gin.Params{{Key: "id", Value: id}}
	if claims != nil {
		c.Set(middleware.CtxClaims, claims)
	}
	h(c)
	return rec
}

// reviewRoutes is the four actions with their methods.
func reviewRoutes(h *AdminRiskReviewHandler) []struct {
	name, method string
	h            func(*gin.Context)
} {
	return []struct {
		name, method string
		h            func(*gin.Context)
	}{
		{"dismiss", http.MethodPost, h.Dismiss},
		{"undismiss", http.MethodDelete, h.Undismiss},
		{"trust", http.MethodPost, h.Trust},
		{"untrust", http.MethodDelete, h.Untrust},
	}
}

// The body reaches the service as asked: the note trimmed, the levels the
// admin saw, and the acting admin from the token's claims — the id to
// store, the UPN for logs. Undismiss and untrust carry no body.
func TestAdminRiskReview_ForwardsTheRequestAndTheActor(t *testing.T) {
	svc := &fakeRiskReview{}
	h := NewAdminRiskReviewHandler(svc)
	rec := serveRiskReview(t, h.Dismiss, http.MethodPost, "7", `{"note":"  called her  ","expected":{"geo":"flagged","geo_auto":"suspended"}}`, reviewClaims)
	if rec.Code != http.StatusOK {
		t.Fatalf("dismiss = %d: %s", rec.Code, rec.Body.String())
	}
	wantExpected := domain.AttentionLevels{"geo": domain.FlagLevelFlagged, "geo_auto": domain.FlagLevelSuspended}
	if svc.action != "dismiss" || svc.userID != 7 || svc.note != "called her" || !reflect.DeepEqual(svc.expected, wantExpected) ||
		svc.by != (riskreview.Actor{ID: 1, UPN: "owner@example.test"}) {
		t.Fatalf("service asked %+v", svc)
	}

	rec = serveRiskReview(t, h.Trust, http.MethodPost, "8", `{"resume_service":true}`, reviewClaims)
	if rec.Code != http.StatusOK || svc.action != "trust" || svc.userID != 8 || !svc.resume || svc.by.ID != 1 {
		t.Fatalf("trust = %d, service asked %+v", rec.Code, svc)
	}
	for name, h := range map[string]func(*gin.Context){"undismiss": h.Undismiss, "untrust": h.Untrust} {
		rec = serveRiskReview(t, h, http.MethodDelete, "9", "", reviewClaims)
		if rec.Code != http.StatusOK || svc.action != name || svc.userID != 9 || svc.by.ID != 1 {
			t.Fatalf("%s = %d, service asked %+v", name, rec.Code, svc)
		}
	}
}

// Both bodies are optional: no body at all is {} — no note, no
// expectation (not an empty one, which would refuse every level), no resume.
func TestAdminRiskReview_EmptyBodyIsAnEmptyRequest(t *testing.T) {
	svc := &fakeRiskReview{}
	h := NewAdminRiskReviewHandler(svc)
	if rec := serveRiskReview(t, h.Dismiss, http.MethodPost, "7", "", reviewClaims); rec.Code != http.StatusOK {
		t.Fatalf("dismiss with no body = %d: %s", rec.Code, rec.Body.String())
	}
	if svc.note != "" || svc.expected != nil {
		t.Fatalf("dismiss with no body asked note %q, expected %#v; want none", svc.note, svc.expected)
	}
	svc.resume = true
	if rec := serveRiskReview(t, h.Trust, http.MethodPost, "7", "", reviewClaims); rec.Code != http.StatusOK || svc.resume {
		t.Fatalf("trust with no body = %d, resume %v; want 200 without a resume", rec.Code, svc.resume)
	}
}

// A body that does not decode is refused whole: nothing reaches the
// service, so nothing is half-applied.
func TestAdminRiskReview_MalformedBodyIs400(t *testing.T) {
	svc := &fakeRiskReview{}
	h := NewAdminRiskReviewHandler(svc)
	for _, tc := range []struct {
		name string
		h    func(*gin.Context)
		body string
	}{
		{"dismiss: not json", h.Dismiss, `note=hi`},
		{"dismiss: note not a string", h.Dismiss, `{"note":5}`},
		{"dismiss: expected not an object", h.Dismiss, `{"expected":["geo"]}`},
		{"dismiss: a level not a string", h.Dismiss, `{"expected":{"geo":2}}`},
		{"trust: resume not a boolean", h.Trust, `{"resume_service":"yes"}`},
	} {
		rec := serveRiskReview(t, tc.h, http.MethodPost, "7", tc.body, reviewClaims)
		if rec.Code != http.StatusBadRequest || decodeObject(t, rec)["error"] == nil {
			t.Fatalf("%s = %d %s, want 400 with an error", tc.name, rec.Code, rec.Body.String())
		}
	}
	if svc.calls != 0 {
		t.Fatalf("a malformed body reached the service %d times", svc.calls)
	}
}

// Every refusal the SPA must tell apart carries its code: a stale tab or a
// second admin gets a 409 naming what changed, a note over the limit a 400
// naming the limit. Any other validation error is a plain 400, a missing
// account a 404, anything else the shared mapping.
func TestAdminRiskReview_ErrorCodes(t *testing.T) {
	for _, tc := range []struct {
		err    error
		status int
		code   string
	}{
		{riskreview.ErrNothingToDismiss, http.StatusConflict, "nothing_to_dismiss"},
		{riskreview.ErrAlreadyDismissed, http.StatusConflict, "already_dismissed"},
		{riskreview.ErrNotDismissed, http.StatusConflict, "not_dismissed"},
		{riskreview.ErrAlreadyTrusted, http.StatusConflict, "already_trusted"},
		{riskreview.ErrNotTrusted, http.StatusConflict, "not_trusted"},
		{riskreview.ErrChanged, http.StatusConflict, "changed"},
		{riskreview.ErrNoteTooLong, http.StatusBadRequest, "note_too_long"},
		{fmt.Errorf("risk review of 7: %w", riskreview.ErrChanged), http.StatusConflict, "changed"},
		{fmt.Errorf("%w: unknown attention source \"x\"", domain.ErrValidation), http.StatusBadRequest, ""},
		{domain.ErrConflict, http.StatusConflict, ""},
		{domain.ErrNotFound, http.StatusNotFound, ""},
	} {
		svc := &fakeRiskReview{err: tc.err}
		h := NewAdminRiskReviewHandler(svc)
		for _, r := range reviewRoutes(h) {
			name := r.name
			rec := serveRiskReview(t, r.h, r.method, "7", "", reviewClaims)
			obj := decodeObject(t, rec)
			if rec.Code != tc.status || obj["error"] == nil {
				t.Fatalf("%s failing with %v = %d %s, want %d with an error", name, tc.err, rec.Code, rec.Body.String(), tc.status)
			}
			if got, _ := obj["code"].(string); got != tc.code {
				t.Fatalf("%s failing with %v: code %q, want %q", name, tc.err, got, tc.code)
			}
		}
	}
}

// The answer is the review row as the action stored it, in the drawer's
// review shape: the admins named by UPN where the acting admin is the one
// named (anyone else by id alone — the SPA shows #id), the accepted levels
// for display, lists as [] never null.
func TestAdminRiskReview_AnswersTheStoredReview(t *testing.T) {
	svc := &fakeRiskReview{review: domain.RiskReview{
		UserID: 7, DismissedAtMS: 1000, DismissedBy: 1, Note: "called her",
		Accepted: domain.DismissSnapshot{"geo": {Level: domain.FlagLevelFlagged, AtMS: 900}},
		Trusted:  true, TrustedAtMS: 500, TrustedBy: 4, UpdatedAtMS: 1000,
	}}
	h := NewAdminRiskReviewHandler(svc)
	rec := serveRiskReview(t, h.Dismiss, http.MethodPost, "7", "", reviewClaims)
	if rec.Code != http.StatusOK {
		t.Fatalf("dismiss = %d: %s", rec.Code, rec.Body.String())
	}
	review, _ := decodeObject(t, rec)["review"].(map[string]any)
	want := map[string]any{
		"dismissed": true, "dismissed_at_ms": float64(1000), "dismissed_by": float64(1), "dismissed_by_upn": "owner@example.test",
		"note": "called her", "levels": map[string]any{"geo": "flagged"}, "reopened": false, "lapsed": false,
		"escalated": []any{}, "trusted": true, "trusted_at_ms": float64(500), "trusted_by": float64(4), "trusted_by_upn": "",
	}
	if !reflect.DeepEqual(review, want) {
		t.Fatalf("review = %#v\nwant %#v", review, want)
	}

	svc.review = domain.RiskReview{UserID: 7, UpdatedAtMS: 1}
	rec = serveRiskReview(t, h.Undismiss, http.MethodDelete, "7", "", reviewClaims)
	review, _ = decodeObject(t, rec)["review"].(map[string]any)
	if review["dismissed"] != false || review["levels"] == nil || review["dismissed_by_upn"] != "" {
		t.Fatalf("undismissed review = %v, want not dismissed, levels {}", review)
	}
}

// Trust with resume answers whether the hold was lifted, and how a failed
// resume failed: lifted but the push could not be queued is a warning
// (resumed, resume_warning); not lifted is an error (not resumed,
// resume_error). Either way the trust stands: 200.
func TestAdminRiskReview_TrustResumeOutcomes(t *testing.T) {
	stored := domain.RiskReview{UserID: 7, Trusted: true, TrustedAtMS: 1, TrustedBy: 1, UpdatedAtMS: 1}
	pushErr := errors.New("push failed")
	for _, tc := range []struct {
		name string
		res  riskreview.TrustResult
		want map[string]any
	}{
		{"not asked", riskreview.TrustResult{Review: stored}, map[string]any{"resumed": false}},
		{"resumed", riskreview.TrustResult{Review: stored, Resumed: true}, map[string]any{"resumed": true}},
		{"resumed, push not queued", riskreview.TrustResult{Review: stored, Resumed: true, ResumeErr: pushErr},
			map[string]any{"resumed": true, "resume_warning": "push failed"}},
		{"not resumed", riskreview.TrustResult{Review: stored, ResumeErr: pushErr},
			map[string]any{"resumed": false, "resume_error": "push failed"}},
	} {
		svc := &fakeRiskReview{trust: tc.res}
		h := NewAdminRiskReviewHandler(svc)
		rec := serveRiskReview(t, h.Trust, http.MethodPost, "7", `{"resume_service":true}`, reviewClaims)
		if rec.Code != http.StatusOK {
			t.Fatalf("%s: trust = %d: %s", tc.name, rec.Code, rec.Body.String())
		}
		obj := decodeObject(t, rec)
		review, _ := obj["review"].(map[string]any)
		if review["trusted"] != true || review["trusted_by_upn"] != "owner@example.test" {
			t.Fatalf("%s: review = %v", tc.name, review)
		}
		delete(obj, "review")
		if !reflect.DeepEqual(obj, tc.want) {
			t.Fatalf("%s: answer = %v, want %v", tc.name, obj, tc.want)
		}
	}
}

// A bad id is refused before the service is asked; an action with no
// admin behind it (no claims) is refused too — every review names its
// admin.
func TestAdminRiskReview_BadIdAndNoActor(t *testing.T) {
	svc := &fakeRiskReview{}
	h := NewAdminRiskReviewHandler(svc)
	for _, id := range []string{"x", "0", "-3"} {
		rec := serveRiskReview(t, h.Dismiss, http.MethodPost, id, "", reviewClaims)
		if obj := decodeObject(t, rec); rec.Code != http.StatusBadRequest || obj["error"] != "Invalid id" {
			t.Fatalf("id %q = %d %s, want 400 Invalid id", id, rec.Code, rec.Body.String())
		}
	}
	for _, claims := range []*jwtutil.Claims{nil, {UPN: "nobody"}} {
		if rec := serveRiskReview(t, h.Trust, http.MethodPost, "7", "", claims); rec.Code != http.StatusUnauthorized {
			t.Fatalf("claims %+v = %d, want 401", claims, rec.Code)
		}
	}
	if svc.calls != 0 {
		t.Fatalf("a refused request reached the service %d times", svc.calls)
	}
}

// A deployment that did not wire the review actions answers 503 on every
// route, never a silent success.
func TestAdminRiskReview_UnwiredIs503(t *testing.T) {
	h := NewAdminRiskReviewHandler(nil)
	for _, r := range reviewRoutes(h) {
		if rec := serveRiskReview(t, r.h, r.method, "7", "", reviewClaims); rec.Code != http.StatusServiceUnavailable {
			t.Fatalf("%s unwired = %d, want 503", r.name, rec.Code)
		}
	}
}
