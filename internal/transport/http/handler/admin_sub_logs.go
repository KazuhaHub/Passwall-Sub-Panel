package handler

import (
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

// AdminSubLogHandler exposes /api/admin/sub-logs — paginated subscription
// log retrieval with optional user ID / time-range filters.
type AdminSubLogHandler struct {
	repo     ports.SubLogRepo
	settings ports.SettingsRepo
}

func NewAdminSubLogHandler(repo ports.SubLogRepo, settings ports.SettingsRepo) *AdminSubLogHandler {
	return &AdminSubLogHandler{repo: repo, settings: settings}
}

func (h *AdminSubLogHandler) List(c *gin.Context) {
	p := parsePagination(c)
	filter := ports.SubLogFilter{
		Pagination: p,
		Search:     firstNonEmpty(p.Keyword, c.Query("search")),
	}
	if v := c.Query("user_id"); v != "" {
		if id, err := strconv.ParseInt(v, 10, 64); err == nil {
			filter.UserID = &id
		}
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

func (h *AdminSubLogHandler) Clear(c *gin.Context) {
	if err := h.repo.Clear(c.Request.Context()); err != nil {
		respondError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

func (h *AdminSubLogHandler) Purge(c *gin.Context) {
	s, err := h.settings.Load(c.Request.Context(), ports.UISettings{})
	if err != nil {
		respondError(c, err)
		return
	}
	if s.SubLogRetentionDays <= 0 {
		c.JSON(http.StatusOK, gin.H{"deleted": 0})
		return
	}
	cutoff := time.Now().AddDate(0, 0, -s.SubLogRetentionDays)
	deleted, err := h.repo.DeleteBefore(c.Request.Context(), cutoff)
	if err != nil {
		respondError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"deleted": deleted})
}
