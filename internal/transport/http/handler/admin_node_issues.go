package handler

import (
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

// AdminNodeIssuesHandler exposes the durable native-agent issue inbox. An
// acknowledgement means an operator reviewed the condition; it does not claim
// that the node recovered.
type AdminNodeIssuesHandler struct {
	repo ports.NodeAgentIssueRepo
}

func NewAdminNodeIssuesHandler(repo ports.NodeAgentIssueRepo) *AdminNodeIssuesHandler {
	return &AdminNodeIssuesHandler{repo: repo}
}

func (h *AdminNodeIssuesHandler) List(c *gin.Context) {
	p := parsePagination(c)
	filter := ports.NodeAgentIssueFilter{
		Pagination: p,
		AgentID:    c.Query("agent_id"),
		Code:       c.Query("code"),
	}
	if raw := c.Query("acknowledged"); raw != "" {
		value, err := strconv.ParseBool(raw)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid acknowledged filter"})
			return
		}
		filter.Acknowledged = &value
	}
	items, total, err := h.repo.List(c.Request.Context(), filter)
	if err != nil {
		respondError(c, err)
		return
	}
	c.JSON(http.StatusOK, pagedEnvelope(items, total, p))
}

func (h *AdminNodeIssuesHandler) Acknowledge(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid id"})
		return
	}
	if err := h.repo.Acknowledge(c.Request.Context(), id, time.Now().UTC()); err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": "Not found"})
			return
		}
		respondError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}
