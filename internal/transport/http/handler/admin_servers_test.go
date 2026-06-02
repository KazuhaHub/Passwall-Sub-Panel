package handler

import (
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/KazuhaHub/passwall-sub-panel/internal/pkg/jwtutil"
	"github.com/KazuhaHub/passwall-sub-panel/internal/transport/http/middleware"
)

// TestActorFromGin pins that the audit actor is read from the JWT claims (via
// ClaimsFrom) — the old code read a "upn" context key the middleware never set,
// so every server-CRUD audit row was attributed to "admin".
func TestActorFromGin(t *testing.T) {
	gin.SetMode(gin.TestMode)

	c1, _ := gin.CreateTestContext(httptest.NewRecorder())
	if got := actorFromGin(c1); got != "admin" {
		t.Errorf("no claims → %q, want admin fallback", got)
	}

	c2, _ := gin.CreateTestContext(httptest.NewRecorder())
	c2.Set(middleware.CtxClaims, &jwtutil.Claims{UPN: "alice@example.com"})
	if got := actorFromGin(c2); got != "alice@example.com" {
		t.Errorf("claims UPN → %q, want alice@example.com", got)
	}
}
