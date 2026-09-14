package handler

import (
	"net/http"

	"github.com/KazuhaHub/passwall-sub-panel/internal/version"
	"github.com/gin-gonic/gin"
)

// GetSUIRelease is a public-metadata read behind the existing administrator and
// enrollment gates. Keeping the wait here lets List/Test remain independent of
// GitHub latency while the page still obtains its first cold-cache update hint.
func (h *AdminServersHandler) GetSUIRelease(c *gin.Context) {
	c.Header("Cache-Control", "private, no-store")
	c.Header("X-Content-Type-Options", "nosniff")
	h.refreshLatestSUI()
	tag, err := version.LatestSUIRelease(c.Request.Context())
	if err != nil {
		// Never expose transport URLs or upstream response contents.
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "S-UI release metadata is unavailable"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"version": tag})
}
