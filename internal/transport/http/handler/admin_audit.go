package handler

import (
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

// AdminAuditHandler exposes /api/admin/audit — paginated audit log
// retrieval with optional actor / action / time-range filters.
type AdminAuditHandler struct {
	repo ports.AuditRepo
}

func NewAdminAuditHandler(repo ports.AuditRepo) *AdminAuditHandler {
	return &AdminAuditHandler{repo: repo}
}

func (h *AdminAuditHandler) List(c *gin.Context) {
	p := parsePagination(c)
	filter := ports.AuditFilter{
		Pagination: p,
		Actor:      c.Query("actor"),
		Action:     c.Query("action"),
		Search:     firstNonEmpty(p.Keyword, c.Query("search")),
	}
	if v := c.Query("since"); v != "" {
		if t, err := time.Parse(time.RFC3339, v); err == nil {
			filter.Since = &t
		}
	}
	if v := c.Query("until"); v != "" {
		if t, err := time.Parse(time.RFC3339, v); err == nil {
			filter.Until = &t
		}
	}
	items, total, err := h.repo.List(c.Request.Context(), filter)
	if err != nil {
		respondError(c, err)
		return
	}
	c.JSON(http.StatusOK, pagedEnvelope(items, total, p))
}

func (h *AdminAuditHandler) Clear(c *gin.Context) {
	if err := h.repo.Clear(c.Request.Context()); err != nil {
		respondError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}
