package handler

import (
	"context"
	"errors"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
)

// Only the read side is available to live HTTP handlers. Applying a backend
// conversion requires the separate, stopped-panel maintenance command.
type ServerMigrationPreviewer interface {
	Preview(context.Context, int64, string, bool) (*domain.ServerMigrationPreview, error)
}

func (h *AdminServersHandler) WithServerMigrationPreviewer(s ServerMigrationPreviewer) *AdminServersHandler {
	h.serverMigration = s
	return h
}

func (h *AdminServersHandler) NodeMigrationPreview(c *gin.Context) {
	privateNodeResponse(c)
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid server ID"})
		return
	}
	allowRestricted := false
	if raw, present := c.GetQuery("allow_restricted_reality"); present {
		if raw != "true" && raw != "false" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "allow_restricted_reality must be true or false"})
			return
		}
		allowRestricted = raw == "true"
	}
	if h.serverMigration == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "server migration preview is unavailable"})
		return
	}
	preview, err := h.serverMigration.Preview(c.Request.Context(), id, c.Query("core_version"), allowRestricted)
	if err != nil {
		switch {
		case errors.Is(err, domain.ErrNotFound):
			c.JSON(http.StatusNotFound, gin.H{"error": "server not found"})
		case errors.Is(err, domain.ErrValidation), errors.Is(err, domain.ErrConflict):
			c.JSON(http.StatusBadRequest, gin.H{"error": "migration preview requires an existing 3X-UI server"})
		default:
			// Database errors can include SQL containing encrypted configuration.
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "cannot load server migration preview"})
		}
		return
	}
	c.JSON(http.StatusOK, preview)
}
