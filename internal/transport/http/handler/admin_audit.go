package handler

import (
	"net/http"
	"strconv"
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
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(c.DefaultQuery("page_size", "50"))
	filter := ports.AuditFilter{
		Pagination: ports.Pagination{Page: page, PageSize: pageSize},
		Actor:      c.Query("actor"),
		Action:     c.Query("action"),
		Search:     c.Query("search"),
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
	c.JSON(http.StatusOK, gin.H{"items": items, "total": total})
}

func (h *AdminAuditHandler) Clear(c *gin.Context) {
	if err := h.repo.Clear(c.Request.Context()); err != nil {
		respondError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}
