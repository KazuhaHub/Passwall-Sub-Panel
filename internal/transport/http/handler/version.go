package handler

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/KazuhaHub/passwall-sub-panel/internal/version"
)

// Version is the public /api/version endpoint. Returns the build
// identity so the SPA's About dialog and external monitoring can tell
// what binary is live. Intentionally unauthenticated — the answer is
// the same string the boot log already prints in cleartext.
func Version(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"version":    version.Version,
		"commit":     version.Commit,
		"build_date": version.BuildDate,
	})
}
