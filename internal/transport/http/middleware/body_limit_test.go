package middleware

import (
	"encoding/json"
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

func prefixLimitRouter(defaultLimit int64, exact, prefixes map[string]int64) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(BodyLimitByPathPrefix(defaultLimit, exact, prefixes))
	read := func(c *gin.Context) {
		if _, err := io.ReadAll(c.Request.Body); err != nil {
			c.Status(http.StatusRequestEntityTooLarge)
			return
		}
		c.Status(http.StatusNoContent)
	}
	r.Any("/*any", read)
	return r
}

func postN(r *gin.Engine, path string, n int) int {
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodPost, path, strings.NewReader(strings.Repeat("a", n))))
	return w.Code
}

func TestBodyLimitByPathPrefixHitAndMiss(t *testing.T) {
	r := prefixLimitRouter(1<<20, nil, map[string]int64{"/api/admin/rules/": 8 << 20})
	if got := postN(r, "/api/admin/rules/big", 2<<20); got != http.StatusNoContent {
		t.Fatalf("prefix hit 2 MiB = %d, want 204", got)
	}
	if got := postN(r, "/api/admin/rules/big", 8<<20+1); got != http.StatusRequestEntityTooLarge {
		t.Fatalf("prefix hit over 8 MiB = %d, want 413", got)
	}
	// Miss: a sibling path that merely shares a leading substring, and an
	// unrelated admin path, keep the default.
	for _, p := range []string{"/api/admin/rules", "/api/admin/rulesets/x", "/api/admin/users"} {
		if got := postN(r, p, 1<<20+1); got != http.StatusRequestEntityTooLarge {
			t.Fatalf("%s 1 MiB+1 = %d, want 413", p, got)
		}
		if got := postN(r, p, 1<<20); got != http.StatusNoContent {
			t.Fatalf("%s 1 MiB = %d, want 204", p, got)
		}
	}
}

func TestBodyLimitByPathPrefixExactAndLongestWin(t *testing.T) {
	r := prefixLimitRouter(4, map[string]int64{"/a/exact": 6}, map[string]int64{"/a/": 10, "/a/b/": 20})
	for _, test := range []struct {
		path string
		n    int
		want int
	}{
		{"/a/exact", 6, http.StatusNoContent},
		{"/a/exact", 7, http.StatusRequestEntityTooLarge}, // exact beats the wider prefix
		{"/a/x", 10, http.StatusNoContent},
		{"/a/x", 11, http.StatusRequestEntityTooLarge},
		{"/a/b/c", 20, http.StatusNoContent}, // longest prefix wins
		{"/a/b/c", 21, http.StatusRequestEntityTooLarge},
		{"/z", 5, http.StatusRequestEntityTooLarge},
	} {
		if got := postN(r, test.path, test.n); got != test.want {
			t.Fatalf("%s n=%d = %d, want %d", test.path, test.n, got, test.want)
		}
	}
}

func TestBodyLimitByPathPrefixValidation(t *testing.T) {
	for name, prefixes := range map[string]map[string]int64{
		"empty":        {"": 1},
		"no slash end": {"/api/admin/rules": 1},
		"zero limit":   {"/a/": 0},
		"no leading /": {"a/": 1},
	} {
		func() {
			defer func() {
				if recover() == nil {
					t.Fatalf("%s: expected panic", name)
				}
			}()
			BodyLimitByPathPrefix(1, nil, prefixes)
		}()
	}
}

func TestAbortBodyTooLargeShape(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(BodyLimit(4))
	r.POST("/", func(c *gin.Context) {
		_, err := io.ReadAll(c.Request.Body)
		if !AbortBodyTooLarge(c, err) {
			t.Fatalf("AbortBodyTooLarge(%v) = false", err)
		}
	})
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/", strings.NewReader("abcde")))
	if w.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d", w.Code)
	}
	var out map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if out["code"] != "body_too_large" || out["limit_bytes"] != float64(4) || out["error"] == "" {
		t.Fatalf("body = %s", w.Body.String())
	}
	if AbortBodyTooLarge(&gin.Context{}, errors.New("other")) {
		t.Fatal("non-limit error must not be treated as too large")
	}
}
