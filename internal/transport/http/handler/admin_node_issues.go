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

type nodeAgentIssueDTO struct {
	*domain.NodeAgentIssue
	ServerID   int64  `json:"server_id,omitempty"`
	ServerName string `json:"server_name,omitempty"`
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
		View:       domain.NodeAgentIssueView(c.Query("view")),
	}
	switch filter.View {
	case "", domain.NodeAgentIssueViewAll, domain.NodeAgentIssueViewAttention, domain.NodeAgentIssueViewDiagnostic:
	default:
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid node issue view"})
		return
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
	var labels map[string]ports.NodeAgentIssueServer
	if metadata, ok := h.repo.(ports.NodeAgentIssueServerRepo); ok && len(items) > 0 {
		agentIDs := make([]string, 0, len(items))
		seen := make(map[string]bool, len(items))
		for _, issue := range items {
			if issue != nil && !seen[issue.AgentID] {
				seen[issue.AgentID] = true
				agentIDs = append(agentIDs, issue.AgentID)
			}
		}
		// Labels are helpful context, not a prerequisite for reviewing the durable
		// report. A failed optional lookup must not make the issue inbox disappear.
		if result, err := metadata.ListIssueServers(c.Request.Context(), agentIDs); err == nil {
			labels = result
		}
	}
	dtos := make([]nodeAgentIssueDTO, len(items))
	for i, issue := range items {
		dtos[i].NodeAgentIssue = issue
		if issue != nil {
			label := labels[issue.AgentID]
			dtos[i].ServerID, dtos[i].ServerName = label.ServerID, label.ServerName
		}
	}
	c.JSON(http.StatusOK, pagedEnvelope(dtos, total, p))
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
