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
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"items": items, "total": total})
}
