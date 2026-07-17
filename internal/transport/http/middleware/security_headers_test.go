package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestSecurityHeaders(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(SecurityHeaders())
	r.GET("/", func(c *gin.Context) { c.Status(http.StatusNoContent) })

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/", nil))

	want := map[string]string{
		"Strict-Transport-Security": "max-age=31536000; includeSubDomains",
		"X-Frame-Options":           "DENY",
		"X-Content-Type-Options":    "nosniff",
		"Referrer-Policy":           "no-referrer",
	}
	for name, value := range want {
		if got := w.Header().Get(name); got != value {
			t.Errorf("%s = %q, want %q", name, got, value)
		}
	}

	csp := w.Header().Get("Content-Security-Policy")
	for _, directive := range []string{
		"default-src 'self'",
		"script-src 'self' https://static.cloudflareinsights.com",
		"style-src 'self' 'unsafe-inline'",
		"frame-ancestors 'none'",
		"form-action 'self'",
	} {
		if !containsCSPDirective(csp, directive) {
			t.Errorf("CSP %q does not contain directive %q", csp, directive)
		}
	}
}

func containsCSPDirective(csp, want string) bool {
	for _, directive := range splitCSP(csp) {
		if directive == want {
			return true
		}
	}
	return false
}

func splitCSP(csp string) []string {
	var directives []string
	for len(csp) > 0 {
		i := 0
		for i < len(csp) && csp[i] != ';' {
			i++
		}
		directive := csp[:i]
		for len(directive) > 0 && directive[0] == ' ' {
			directive = directive[1:]
		}
		for len(directive) > 0 && directive[len(directive)-1] == ' ' {
			directive = directive[:len(directive)-1]
		}
		if directive != "" {
			directives = append(directives, directive)
		}
		if i == len(csp) {
			break
		}
		csp = csp[i+1:]
	}
	return directives
}
