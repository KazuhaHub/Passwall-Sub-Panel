package handler

import (
	"io/fs"
	"mime"
	"net/http"
	"path/filepath"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/KazuhaHub/passwall-sub-panel/internal/web"
)

// StaticSPA serves the embedded SPA bundle and falls back to index.html for
// any non-asset path (so React Router's history mode works).
//
// Wire it as a NoRoute handler so /api and /sub keep their precedence.
//
// Caching policy:
//   - /assets/*  → Cache-Control: public, max-age=1y, immutable. Vite hashes
//     these filenames on every content change, so an old URL can never refer
//     to new content; long caching is safe.
//   - index.html and other root files (favicon etc.) → no-cache so a redeploy
//     propagates immediately. The hashed-asset URLs inside index.html update
//     on every build, which is what triggers the browser to pick up the new
//     bundle.
func StaticSPA(c *gin.Context) {
	sub, err := fs.Sub(web.DistFS, "dist")
	if err != nil {
		c.String(http.StatusInternalServerError, "frontend bundle not embedded")
		return
	}

	requested := strings.TrimPrefix(c.Request.URL.Path, "/")
	if requested == "" {
		requested = "index.html"
	}

	// Try the literal asset first.
	if b, err := fs.ReadFile(sub, requested); err == nil {
		ct := mime.TypeByExtension(filepath.Ext(requested))
		if ct == "" {
			ct = "application/octet-stream"
		}
		setCacheHeaders(c, requested)
		c.Data(http.StatusOK, ct, b)
		return
	}

	// Asset-shaped paths (/assets/...) shouldn't fall back — return 404.
	if strings.HasPrefix(requested, "assets/") {
		c.String(http.StatusNotFound, "not found")
		return
	}

	// SPA fallback to index.html.
	if b, err := fs.ReadFile(sub, "index.html"); err == nil {
		setCacheHeaders(c, "index.html")
		c.Data(http.StatusOK, "text/html; charset=utf-8", b)
		return
	}

	c.String(http.StatusNotFound,
		"Frontend bundle not built. Run `cd web && npm install && npm run build` then rebuild the Go binary.")
}

// setCacheHeaders applies the Vite-aware caching policy described on
// StaticSPA. Kept separate so the SPA fallback path can reuse it without
// duplicating the branch.
func setCacheHeaders(c *gin.Context, path string) {
	if strings.HasPrefix(path, "assets/") {
		// Filename carries an 8-char content hash; new content = new URL.
		c.Header("Cache-Control", "public, max-age=31536000, immutable")
		return
	}
	// index.html (and root files like favicon.ico) must revalidate so a new
	// deploy is picked up on the next page load instead of after the user
	// clears their cache.
	c.Header("Cache-Control", "no-cache")
}
