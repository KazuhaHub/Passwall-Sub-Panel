package handler

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
)

func decodeErrBody(t *testing.T, w *httptest.ResponseRecorder) string {
	t.Helper()
	var body struct {
		Error string `json:"error"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v (raw: %s)", err, w.Body.String())
	}
	return body.Error
}

// respondPublicError backs the public, unauthenticated endpoints
// (register / verify-email / reset-password). A non-sentinel (internal) error
// MUST NOT have its raw string echoed to an anonymous caller — that leaks DB
// driver / table / constraint names and internal paths. It must return a
// generic 500.
func TestRespondPublicError_HidesRawInternalError(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	raw := fmt.Errorf("UNIQUE constraint failed: users.email (pgx driver, /var/lib/psp/panel.db)")
	respondPublicError(c, raw)
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", w.Code)
	}
	if got := decodeErrBody(t, w); got != "Internal server error" {
		t.Fatalf("public error body = %q, must be the generic message (raw internal error leaked)", got)
	}
}

// respondPublicError must still surface the user-facing domain sentinels
// (validation message verbatim; the right status for the rest) — only the
// non-sentinel default branch is sanitised.
func TestRespondPublicError_PreservesSentinels(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cases := []struct {
		err         error
		code        int
		msgContains string
	}{
		{fmt.Errorf("%w: email is required", domain.ErrValidation), http.StatusBadRequest, "email is required"},
		{domain.ErrConflict, http.StatusConflict, "Conflict"},
		{domain.ErrResourceExhausted, http.StatusTooManyRequests, "Resource exhausted"},
		{domain.ErrNotFound, http.StatusNotFound, "Not found"},
	}
	for _, tc := range cases {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		respondPublicError(c, tc.err)
		if w.Code != tc.code {
			t.Fatalf("err %v: status %d want %d", tc.err, w.Code, tc.code)
		}
		if got := decodeErrBody(t, w); !strings.Contains(got, tc.msgContains) {
			t.Fatalf("err %v: body %q want contains %q", tc.err, got, tc.msgContains)
		}
	}
}

// The diagnostic respondError (staff/admin-only handlers) intentionally still
// surfaces the raw detail in its default branch — guard so the public/staff
// split stays explicit.
func TestRespondError_StaffPathStillSurfacesDetail(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	respondError(c, fmt.Errorf("some internal diagnostic detail"))
	if got := decodeErrBody(t, w); got != "some internal diagnostic detail" {
		t.Fatalf("staff diagnostic body = %q, want the raw detail", got)
	}
}
