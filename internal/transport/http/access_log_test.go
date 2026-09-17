package http

import (
	"bytes"
	stdhttp "net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestAccessLoggerSuppressesCredentialBearingPaths(t *testing.T) {
	gin.SetMode(gin.TestMode)
	var logs bytes.Buffer
	g := gin.New()
	g.Use(newAccessLogger(&logs))
	for _, route := range credentialBearingRoutes {
		g.Handle(route.method, route.pattern, func(c *gin.Context) {
			c.Status(stdhttp.StatusNoContent)
		})
	}
	g.GET("/ordinary", func(c *gin.Context) { c.Status(stdhttp.StatusNoContent) })

	token := "one-time-token-must-not-enter-logs"
	for _, test := range []struct {
		method string
		path   string
	}{
		{method: stdhttp.MethodGet, path: "/enroll/" + token},
		{method: stdhttp.MethodPost, path: "/api/enroll/" + token},
		{method: stdhttp.MethodGet, path: "/node-bootstrap/" + token},
		// A wrong method still carries the credential and must not be logged.
		{method: stdhttp.MethodDelete, path: "/enroll/" + token},
	} {
		logs.Reset()
		w := httptest.NewRecorder()
		g.ServeHTTP(w, httptest.NewRequest(test.method, test.path, nil))
		if logs.Len() != 0 || strings.Contains(logs.String(), token) {
			t.Fatalf("%s %s leaked into access log: %q", test.method, test.path, logs.String())
		}
	}

	logs.Reset()
	w := httptest.NewRecorder()
	g.ServeHTTP(w, httptest.NewRequest(stdhttp.MethodGet, "/ordinary", nil))
	if !strings.Contains(logs.String(), "/ordinary") {
		t.Fatalf("ordinary request disappeared from access log: %q", logs.String())
	}
}
