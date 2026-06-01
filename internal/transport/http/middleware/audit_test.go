package middleware

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

// captureBodyFor builds a test gin.Context around a request with the given
// content type + raw body and returns what captureRequestBody records.
func captureBodyFor(t *testing.T, contentType, body string) any {
	t.Helper()
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	req := httptest.NewRequest(http.MethodPost, "/api/auth/local/login", strings.NewReader(body))
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	c.Request = req
	return captureRequestBody(c)
}

func mustJSON(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return string(b)
}

// Regression (M2): a non-JSON request body must never be stored verbatim in the
// operator-readable audit log. A form-encoded login carries upn+password to the
// unauthenticated /api/auth/local/login; the password must be redacted.
func TestCaptureRequestBody_FormEncodedRedactsPassword(t *testing.T) {
	got := captureBodyFor(t, "application/x-www-form-urlencoded", "upn=alice@example.com&password=hunter2")
	s := mustJSON(t, got)
	if strings.Contains(s, "hunter2") {
		t.Fatalf("plaintext password leaked into audit body: %s", s)
	}
	if !strings.Contains(s, "[REDACTED]") {
		t.Fatalf("password key must be redacted, got %s", s)
	}
	if !strings.Contains(s, "alice@example.com") {
		t.Fatalf("non-sensitive upn should be visible for the audit trail, got %s", s)
	}
}

// A non-JSON, non-form body (e.g. malformed JSON with a trailing comma) must
// fall back to a shape-only placeholder, never the raw bytes.
func TestCaptureRequestBody_MalformedJSONNotStoredRaw(t *testing.T) {
	got := captureBodyFor(t, "application/json", `{"password":"hunter2",}`)
	s := mustJSON(t, got)
	if strings.Contains(s, "hunter2") {
		t.Fatalf("malformed-JSON body stored raw, leaking secret: %s", s)
	}
	if !strings.Contains(s, "unparsed_body") {
		t.Fatalf("want shape-only placeholder, got %s", s)
	}
}

// Valid JSON keeps the existing key-redaction behavior.
func TestCaptureRequestBody_ValidJSONRedacts(t *testing.T) {
	got := captureBodyFor(t, "application/json", `{"upn":"bob","password":"hunter2"}`)
	s := mustJSON(t, got)
	if strings.Contains(s, "hunter2") {
		t.Fatalf("JSON password not redacted: %s", s)
	}
	if !strings.Contains(s, "bob") {
		t.Fatalf("non-sensitive field dropped: %s", s)
	}
}

func TestShouldAuditPath_AdminWrites(t *testing.T) {
	cases := []struct {
		method, path string
		want         bool
	}{
		{"POST", "/api/admin/users", true},
		{"PUT", "/api/admin/settings/ui", true},
		{"PATCH", "/api/admin/nodes/1", true},
		{"DELETE", "/api/admin/users/5", true},
		// Reads must NOT trigger audit — the table would explode and
		// page-load traffic isn't security-interesting.
		{"GET", "/api/admin/users", false},
		{"HEAD", "/api/admin/users", false},
	}
	for _, c := range cases {
		if got := shouldAuditPath(c.path, c.method); got != c.want {
			t.Errorf("%s %s = %v, want %v", c.method, c.path, got, c.want)
		}
	}
}

func TestShouldAuditPath_LoginAttempt(t *testing.T) {
	if !shouldAuditPath("/api/auth/local/login", "POST") {
		t.Fatal("login POST must audit — covers brute-force / unauthorized attempts")
	}
	if shouldAuditPath("/api/auth/methods", "GET") {
		t.Fatal("auth methods discovery is read-only public; should not audit")
	}
}

func TestShouldAuditPath_TokenRefresh(t *testing.T) {
	if !shouldAuditPath("/api/auth/refresh", "POST") {
		t.Fatal("token refresh POST must audit — it mints fresh access+refresh credentials (the re-issuance event a post-incident review needs alongside login)")
	}
}

func TestShouldAuditPath_UserSelfServiceWrites(t *testing.T) {
	cases := []struct {
		method, path string
		want         bool
	}{
		{"POST", "/api/user/me/change-password", true},
		{"POST", "/api/user/me/emergency-access", true},
		{"POST", "/api/user/me/reset-credentials", true},
		{"PUT", "/api/user/me/rules", true},
		// Profile / traffic / history reads — too noisy to audit.
		{"GET", "/api/user/me", false},
		{"GET", "/api/user/me/traffic", false},
		{"GET", "/api/user/me/rules", false},
	}
	for _, c := range cases {
		if got := shouldAuditPath(c.path, c.method); got != c.want {
			t.Errorf("%s %s = %v, want %v", c.method, c.path, got, c.want)
		}
	}
}

func TestShouldAuditPath_NonAuditedTraffic(t *testing.T) {
	cases := []string{
		"/sub/abc123",
		"/health",
		"/assets/index.js",
		"/admin", // SPA routes — embedded UI, not API
		"/",
	}
	for _, path := range cases {
		// Even with POST these should not be audited.
		if shouldAuditPath(path, "POST") {
			t.Errorf("non-API path %s must not audit", path)
		}
	}
}

// resolveAuditActor logic without standing up a real gin context where
// possible. The "claims absent + login body" branch is the most-likely
// regression site since the previous middleware defaulted actor to
// "admin" which is wrong for an anonymous login attempt.
func TestResolveAuditActor_LoginBodyFallback(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(nil)
	// No claims set — anonymous request hitting /api/auth/local/login.
	body := map[string]any{"upn": "alice@example.com", "password": "[REDACTED]"}
	got := resolveAuditActor(c, "/api/auth/local/login", body)
	if got != "alice@example.com" {
		t.Fatalf("actor = %q, want alice@example.com (extracted from body)", got)
	}
}

func TestResolveAuditActor_FallsBackToAnonymousWhenNoClaimsNoBody(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(nil)
	got := resolveAuditActor(c, "/api/admin/users", nil)
	if got != "anonymous" {
		t.Fatalf("actor = %q, want anonymous (previous default of \"admin\" was misleading)", got)
	}
}

func TestResolveAuditActor_LoginBodyWithoutUPNStaysAnonymous(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(nil)
	// Malformed login body — still shouldn't crash, just falls back.
	got := resolveAuditActor(c, "/api/auth/local/login", map[string]any{"password": "x"})
	if got != "anonymous" {
		t.Fatalf("actor = %q, want anonymous for malformed login body", got)
	}
}

func TestActionName(t *testing.T) {
	// Sanity: the action verb mapping covers all methods the gate accepts.
	cases := []struct {
		method   string
		expected string
	}{
		{http.MethodPost, "create_or_run /x"},
		{http.MethodPut, "update /x"},
		{http.MethodPatch, "update /x"},
		{http.MethodDelete, "delete /x"},
	}
	for _, c := range cases {
		if got := actionName(c.method, "/x"); got != c.expected {
			t.Errorf("actionName(%s) = %q, want %q", c.method, got, c.expected)
		}
	}
}

func TestIsSensitiveKey(t *testing.T) {
	for _, k := range []string{"password", "api_token", "sub_token", "uuid", "client_secret", "private_key", "PASSWORD", "RefreshToken"} {
		if !isSensitiveKey(k) {
			t.Errorf("key %q should be sensitive (case-insensitive match)", k)
		}
	}
	for _, k := range []string{"upn", "email", "display_name", "remark"} {
		if isSensitiveKey(k) {
			t.Errorf("key %q must NOT be redacted — admins need to see who/what", k)
		}
	}
}
