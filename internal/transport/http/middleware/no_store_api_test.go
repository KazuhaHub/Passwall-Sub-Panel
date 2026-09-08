package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

// newNoStoreRouter mirrors the real mount order: NoStoreAPI runs before the
// handler, so a handler that sets its own Cache-Control overwrites the default
// rather than being overwritten by it.
func newNoStoreRouter(t *testing.T) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(NoStoreAPI())
	r.GET("/api/admin/users", func(c *gin.Context) { c.Status(http.StatusNoContent) })
	r.POST("/api/enroll/tok", func(c *gin.Context) { c.Status(http.StatusNoContent) })
	r.GET("/api/version", func(c *gin.Context) { c.Status(http.StatusNoContent) })
	// Stands in for /api/i18n/:lang, which deliberately serves no-cache with an
	// ETag so bundles revalidate instead of re-downloading.
	r.GET("/api/i18n/en-US", func(c *gin.Context) {
		c.Header("Cache-Control", "no-cache")
		c.Status(http.StatusNoContent)
	})
	// Stands in for a subscription, served from NoRoute outside /api with its
	// own private, no-cache + ETag.
	r.GET("/s/abc", func(c *gin.Context) {
		c.Header("Cache-Control", "private, no-cache")
		c.Status(http.StatusNoContent)
	})
	r.GET("/assets/app.js", func(c *gin.Context) {
		c.Header("Cache-Control", "public, max-age=31536000, immutable")
		c.Status(http.StatusNoContent)
	})
	return r
}

func headerFor(t *testing.T, r *gin.Engine, method, path string) string {
	t.Helper()
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(method, path, nil))
	return w.Header().Get("Cache-Control")
}

// The defect this closes: API responses carried NO cache directive at all, and
// per RFC 9111 an absent directive is not "do not cache" — it hands the
// decision to a heuristic, and to whatever proxy sits in front. That is the
// shape that lets an admin save an entity, reopen it, and read the value from
// before the save.
func TestNoStoreAPI_MarksAPIResponsesUncacheable(t *testing.T) {
	r := newNoStoreRouter(t)
	for _, tc := range []struct{ method, path string }{
		{http.MethodGet, "/api/admin/users"},
		{http.MethodGet, "/api/version"},
		{http.MethodPost, "/api/enroll/tok"},
	} {
		if got := headerFor(t, r, tc.method, tc.path); got != "no-store" {
			t.Errorf("%s %s: Cache-Control = %q, want %q", tc.method, tc.path, got, "no-store")
		}
	}
}

// A handler that has thought about its own caching must win. Without this the
// change would silently disable i18n bundle revalidation, trading one
// performance regression for the correctness fix.
func TestNoStoreAPI_HandlerOverrideWins(t *testing.T) {
	r := newNoStoreRouter(t)
	if got := headerFor(t, r, http.MethodGet, "/api/i18n/en-US"); got != "no-cache" {
		t.Fatalf("a handler's own Cache-Control must survive: got %q, want %q", got, "no-cache")
	}
}

// Scoped to /api. Subscriptions and hashed static assets are served outside the
// prefix and set caching that is deliberate and, for assets, load-bearing —
// blanket no-store would make every page load re-download the bundle.
func TestNoStoreAPI_LeavesNonAPIPathsAlone(t *testing.T) {
	r := newNoStoreRouter(t)
	for path, want := range map[string]string{
		"/s/abc":         "private, no-cache",
		"/assets/app.js": "public, max-age=31536000, immutable",
	} {
		if got := headerFor(t, r, http.MethodGet, path); got != want {
			t.Errorf("%s: Cache-Control = %q, want %q", path, got, want)
		}
	}
}

// "/apiary" must not match the /api rule. The guard is a path-segment prefix,
// not a string prefix.
func TestNoStoreAPI_DoesNotMatchPrefixLookalikes(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(NoStoreAPI())
	r.GET("/apiary", func(c *gin.Context) { c.Status(http.StatusNoContent) })
	if got := headerFor(t, r, http.MethodGet, "/apiary"); got != "" {
		t.Fatalf("/apiary is not under /api/: Cache-Control = %q, want empty", got)
	}
}
