package handler

import (
	"net/http"

	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
	"github.com/gin-gonic/gin"
)

func (h *AdminServersHandler) WithNodeReleaseCatalog(catalog ports.NodeReleaseCatalog) *AdminServersHandler {
	h.nodeReleases = catalog
	return h
}

// ListNodeReleases exposes only public release metadata through the existing
// administrator/2FA gate. It never reads or rotates a server's credentials.
func (h *AdminServersHandler) ListNodeReleases(c *gin.Context) {
	c.Header("Cache-Control", "private, no-store")
	c.Header("X-Content-Type-Options", "nosniff")
	if h.nodeReleases == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "Node release catalog is unavailable"})
		return
	}
	result, err := h.nodeReleases.List(c.Request.Context())
	if err != nil {
		// Raw upstream errors may contain request URLs or response contents.
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "Node release catalog is unavailable"})
		return
	}
	if result.Releases == nil {
		result.Releases = []ports.NodeReleaseCatalogEntry{}
	}
	c.JSON(http.StatusOK, result)
}
