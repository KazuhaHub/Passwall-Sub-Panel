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
// any non-asset path (so Vue Router's history mode works).
//
// Wire it as a NoRoute handler so /api and /sub keep their precedence.
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
		c.Data(http.StatusOK, "text/html; charset=utf-8", b)
		return
	}

	c.String(http.StatusNotFound,
		"Frontend bundle not built. Run `cd web && npm install && npm run build` then rebuild the Go binary.")
}
