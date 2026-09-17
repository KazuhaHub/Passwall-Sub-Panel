package http

import (
	stdhttp "net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/KazuhaHub/passwall-sub-panel/internal/config"
	"github.com/KazuhaHub/passwall-sub-panel/internal/pkg/operationgate"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

// The SAML ACS endpoint is unauthenticated and hands attacker-controlled XML to
// goxmldsig. Non-exclusive canonicalisation of a SignedInfo costs O(depth^2)
// memory, and etree's 1024-node depth cap still leaves ~52 MiB reachable from a
// ~7 KiB body (GHSA-qhrp-hfff-vphr). Nothing bounds how many of those a single
// peer can have in flight except the per-IP login limiter, so its presence on
// this route is a security property, not a convenience.
func TestSAMLACSIsBehindTheLoginLimiter(t *testing.T) {
	gin.SetMode(gin.TestMode)
	const limit = 3
	router := NewRouter(Deps{
		Cfg:              &config.Config{ConfigDir: t.TempDir()},
		Repos:            ports.Repos{Settings: &dispatchSettingsRepo{settings: ports.UISettings{LoginPerIPPerMin: limit}}},
		LoginPerIPPerMin: limit,
		OperationGate:    operationgate.New(),
	})

	post := func() int {
		req := httptest.NewRequest(stdhttp.MethodPost, "https://panel.example/api/auth/saml/acs",
			strings.NewReader("SAMLResponse=x"))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.RemoteAddr = "203.0.113.9:40000"
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		return rec.Code
	}

	// SSO is not configured in this router, so an admitted request is answered
	// by the handler (404) rather than the limiter (429). That is what makes
	// this test discriminating: without the limiter every call returns 404.
	for i := 1; i <= limit; i++ {
		if code := post(); code == stdhttp.StatusTooManyRequests {
			t.Fatalf("request %d/%d was rate limited before the limit was reached", i, limit)
		}
	}
	for i := 0; i < 2; i++ {
		if code := post(); code != stdhttp.StatusTooManyRequests {
			t.Fatalf("request past the per-IP limit = %d, want 429 (ACS is not behind loginLimiter)", code)
		}
	}
}
