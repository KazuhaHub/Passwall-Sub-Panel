package handler

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

type AdminSyncTasksHandler struct {
	repo ports.SyncTaskRepo
}

func NewAdminSyncTasksHandler(repo ports.SyncTaskRepo) *AdminSyncTasksHandler {
	return &AdminSyncTasksHandler{repo: repo}
}

func (h *AdminSyncTasksHandler) List(c *gin.Context) {
	p := parsePagination(c)
	var status *domain.SyncTaskStatus
	if v := c.Query("status"); v != "" {
		s := domain.SyncTaskStatus(v)
		status = &s
	}
	var typ *domain.SyncTaskType
	if v := c.Query("type"); v != "" {
		t := domain.SyncTaskType(v)
		typ = &t
	}
	items, total, err := h.repo.List(c.Request.Context(), ports.SyncTaskFilter{
		Pagination: p,
		Status:     status,
		Type:       typ,
	})
	if err != nil {
		respondError(c, err)
		return
	}
	c.JSON(http.StatusOK, pagedEnvelope(items, total, p))
}

func (h *AdminSyncTasksHandler) Retry(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid id"})
		return
	}
	if _, err := h.repo.GetByID(c.Request.Context(), id); err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": "Not found"})
			return
		}
		respondError(c, err)
		return
	}
	if err := h.repo.RetryNow(c.Request.Context(), id); err != nil {
		respondError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

// PurgeFinished wipes every non-active sync task (anything not pending or
// running). Powers the "一键清空" button.
func (h *AdminSyncTasksHandler) PurgeFinished(c *gin.Context) {
	n, err := h.repo.DeleteFinished(c.Request.Context())
	if err != nil {
		respondError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"deleted": n})
}

func (h *AdminSyncTasksHandler) Cancel(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid id"})
		return
	}
	if _, err := h.repo.GetByID(c.Request.Context(), id); err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": "Not found"})
			return
		}
		respondError(c, err)
		return
	}
	if err := h.repo.Cancel(c.Request.Context(), id); err != nil {
		respondError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}
