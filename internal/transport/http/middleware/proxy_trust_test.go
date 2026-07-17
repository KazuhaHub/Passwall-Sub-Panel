package middleware

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestProxyHeadersTrustedDefaultsToFalse(t *testing.T) {
	if ProxyHeadersTrusted(context.Background()) {
		t.Fatal("context without ProxyTrust marker must fail closed")
	}
}

func TestProxyTrust(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	if err := r.SetTrustedProxies([]string{"10.0.0.0/8"}); err != nil {
		t.Fatal(err)
	}
	r.Use(ProxyTrust())
	r.GET("/", func(c *gin.Context) {
		if ProxyHeadersTrusted(c.Request.Context()) {
			c.Status(http.StatusNoContent)
			return
		}
		c.Status(http.StatusForbidden)
	})

	tests := []struct {
		name       string
		remoteAddr string
		forwarded  string
		wantStatus int
	}{
		{
			name:       "trusted proxy",
			remoteAddr: "10.0.0.2:4321",
			forwarded:  "203.0.113.9",
			wantStatus: http.StatusNoContent,
		},
		{
			name:       "direct client cannot self-assert forwarded headers",
			remoteAddr: "203.0.113.9:4321",
			forwarded:  "198.51.100.1",
			wantStatus: http.StatusForbidden,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			req.RemoteAddr = tt.remoteAddr
			req.Header.Set("X-Forwarded-For", tt.forwarded)
			r.ServeHTTP(w, req)
			if w.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d", w.Code, tt.wantStatus)
			}
		})
	}
}
