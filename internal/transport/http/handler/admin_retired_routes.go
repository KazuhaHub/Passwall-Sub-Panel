package handler

import (
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

// PreviewRetiredRoutes lists owned legacy rules without updating the server.
func (h *AdminServersHandler) PreviewRetiredRoutes(c *gin.Context) {
	h.retiredRoutes(c, "")
}

// CleanRetiredRoutes applies a confirmed cleanup to the previewed template.
func (h *AdminServersHandler) CleanRetiredRoutes(c *gin.Context) {
	var in struct {
		Confirm  bool   `json:"confirm"`
		Revision string `json:"revision"`
	}
	if err := c.ShouldBindJSON(&in); err != nil || !in.Confirm || len(in.Revision) != 64 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Confirm a current cleanup preview"})
		return
	}
	if _, err := hex.DecodeString(in.Revision); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid cleanup preview revision"})
		return
	}
	h.retiredRoutes(c, in.Revision)
}

func (h *AdminServersHandler) retiredRoutes(c *gin.Context, revision string) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid server id"})
		return
	}
	ctx := c.Request.Context()
	if revision != "" {
		defer h.retiredRouteLocks.Lock(id)()
	}
	panel, err := h.repo.GetByID(ctx, id)
	if err != nil {
		mapServerError(c, err)
		return
	}
	if domain.NormalizePanelKind(panel.Kind) != domain.PanelKind3XUI {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Retired route cleanup is only available for 3X-UI"})
		return
	}
	client, err := h.pool.Get(id)
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": "3X-UI client unavailable"})
		return
	}
	cleaner, ok := client.(ports.RetiredRouteCleaner)
	if !ok {
		c.JSON(http.StatusNotImplemented, gin.H{"error": ports.ErrPanelCapabilityUnsupported.Error()})
		return
	}
	result, err := cleaner.RetiredRouteCleanup(ctx, revision)
	if errors.Is(err, domain.ErrConflict) {
		c.JSON(http.StatusConflict, gin.H{"error": "The template changed since the preview", "code": "retired_route_preview_stale"})
		return
	}
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
		return
	}
	if revision != "" && len(result.RuleTags) > 0 && h.audit != nil {
		_ = h.audit.Insert(ctx, &domain.AuditEntry{
			Actor: actorFromGin(c), Action: "retired_vless_routes_cleaned",
			Target: fmt.Sprintf("panel=%d rules=%d", id, len(result.RuleTags)), At: time.Now(),
		})
	}
	if result.RuleTags == nil {
		result.RuleTags = []string{}
	}
	privateNodeResponse(c)
	c.JSON(http.StatusOK, result)
}
