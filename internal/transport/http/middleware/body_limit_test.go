package middleware

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestBodyLimit(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(BodyLimit(4))
	r.POST("/", func(c *gin.Context) {
		body, err := io.ReadAll(c.Request.Body)
		if err != nil {
			var tooLarge *http.MaxBytesError
			if !errors.As(err, &tooLarge) {
				t.Fatalf("read body error = %T %v, want *http.MaxBytesError", err, err)
			}
			c.Status(http.StatusRequestEntityTooLarge)
			return
		}
		c.String(http.StatusOK, string(body))
	})

	tests := []struct {
		name   string
		body   string
		status int
	}{
		{name: "below limit", body: "abc", status: http.StatusOK},
		{name: "at limit", body: "abcd", status: http.StatusOK},
		{name: "over limit", body: "abcde", status: http.StatusRequestEntityTooLarge},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(tt.body))
			r.ServeHTTP(w, req)
			if w.Code != tt.status {
				t.Fatalf("status = %d, want %d", w.Code, tt.status)
			}
		})
	}
}

func TestBodyLimitAllowsRequestsWithoutBody(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(BodyLimit(1))
	r.GET("/", func(c *gin.Context) { c.Status(http.StatusNoContent) })

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/", nil))
	if w.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusNoContent)
	}
}

func TestBodyLimitByPathUsesExactOverride(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(BodyLimitByPath(4, map[string]int64{"/v1/node/sync": 8}))
	read := func(c *gin.Context) {
		if _, err := io.ReadAll(c.Request.Body); err != nil {
			c.Status(http.StatusRequestEntityTooLarge)
			return
		}
		c.Status(http.StatusNoContent)
	}
	r.POST("/v1/node/sync", read)
	r.POST("/other", read)

	for _, test := range []struct {
		path   string
		status int
	}{
		{path: "/v1/node/sync", status: http.StatusNoContent},
		{path: "/other", status: http.StatusRequestEntityTooLarge},
	} {
		response := httptest.NewRecorder()
		r.ServeHTTP(response, httptest.NewRequest(http.MethodPost, test.path, strings.NewReader("12345678")))
		if response.Code != test.status {
			t.Fatalf("%s status = %d, want %d", test.path, response.Code, test.status)
		}
	}
}
