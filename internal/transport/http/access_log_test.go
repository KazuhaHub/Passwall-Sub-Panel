package http

import (
	"bytes"
	stdhttp "net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
)

func TestAccessLoggerUsesXrayStyleLine(t *testing.T) {
	params := gin.LogFormatterParams{
		TimeStamp:  time.Date(2026, 9, 16, 3, 4, 5, 600, time.FixedZone("local", -7*60*60)),
		StatusCode: 200,
		Latency:    1250 * time.Microsecond,
		ClientIP:   "192.0.2.10",
		Method:     stdhttp.MethodGet,
		Path:       "/api/health",
	}

	want := "2026/09/16 10:04:05.000000 [Info] passwall-sub-panel: http request status=200 method=GET path=/api/health latency=1.25ms client_ip=192.0.2.10\n"
	if got := xrayAccessLogFormatter(params); got != want {
		t.Fatalf("access log = %q, want %q", got, want)
	}
}

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
