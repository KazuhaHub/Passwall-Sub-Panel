package handler

import (
	"context"
	"net/http"

	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
	"github.com/KazuhaHub/passwall-sub-panel/internal/version"
	"github.com/gin-gonic/gin"
)

func (h *AdminServersHandler) WithNodeReleaseCatalog(catalog ports.NodeReleaseCatalog) *AdminServersHandler {
	h.nodeReleases = catalog
	return h
}

// nodeReleaseTag is the ADDRESS of a published Node release, as the panel itself
// states it.
//
// IT IS A LOOKUP RATHER THAN A DERIVATION, because a version no longer determines
// an address. Four releases were published under a namespace that every release
// since does not use — `release/4.0.1.2` against `v4.0.1.3` — and no version string
// says which. The catalog is where this panel states what it published (the same
// field the front end renders and the download paths are built from), so a caller
// addressing a release asks here instead of rebuilding the mapping.
//
// AN UNREADABLE OR SILENT CATALOG FALLS BACK TO THE CURRENT NAMESPACE, which is
// the right answer for the release an operator is about to install and the wrong
// one for the four that already exist. That degradation is named rather than
// hidden: the alternative is refusing to render an installation because GitHub
// could not be reached, and the panel already had no catalog to show in that case.
func (h *AdminServersHandler) nodeReleaseTag(ctx context.Context, releaseVersion string) string {
	if h.nodeReleases != nil {
		if list, err := h.nodeReleases.List(ctx); err == nil {
			for _, entry := range list.Releases {
				if entry.Version == releaseVersion && entry.ReleaseTag != "" {
					return entry.ReleaseTag
				}
			}
		}
	}
	tag, _ := version.ReleaseTagFor(releaseVersion)
	return tag
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
