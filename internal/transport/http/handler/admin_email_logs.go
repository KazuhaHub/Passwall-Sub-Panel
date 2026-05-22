package handler

import (
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

// AdminEmailLogHandler exposes /api/admin/email-logs — paginated read /
// clear / purge over mail_sent. Mirrors AdminSubLogHandler so the front-
// end Logs tab can reuse the same shape (Subscription / Audit / Email).
//
// "Email log" here means a successful outbound notification: the row
// gets inserted by mailer.Service after sendSMTP returns nil, keyed on
// (user_id, kind, window_key) so the same reminder window never logs
// twice.
type AdminEmailLogHandler struct {
	repo     ports.MailRepo
	settings ports.SettingsRepo
}

func NewAdminEmailLogHandler(repo ports.MailRepo, settings ports.SettingsRepo) *AdminEmailLogHandler {
	return &AdminEmailLogHandler{repo: repo, settings: settings}
}

func (h *AdminEmailLogHandler) List(c *gin.Context) {
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(c.DefaultQuery("page_size", "50"))
	filter := ports.EmailLogFilter{
		Pagination: ports.Pagination{Page: page, PageSize: pageSize},
		Search:     c.Query("search"),
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
	items, total, err := h.repo.ListSent(c.Request.Context(), filter)
	if err != nil {
		respondError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"items": items, "total": total})
}

func (h *AdminEmailLogHandler) Clear(c *gin.Context) {
	if err := h.repo.ClearSent(c.Request.Context()); err != nil {
		respondError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

func (h *AdminEmailLogHandler) Purge(c *gin.Context) {
	s, err := h.settings.Load(c.Request.Context(), ports.UISettings{})
	if err != nil {
		respondError(c, err)
		return
	}
	if s.MailSentRetentionDays <= 0 {
		c.JSON(http.StatusOK, gin.H{"deleted": 0})
		return
	}
	cutoff := time.Now().AddDate(0, 0, -s.MailSentRetentionDays)
	deleted, err := h.repo.DeleteSentBefore(c.Request.Context(), cutoff)
	if err != nil {
		respondError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"deleted": deleted})
}
