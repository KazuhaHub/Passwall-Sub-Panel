package handler

import (
	"errors"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/service/destpolicy"
	"github.com/gin-gonic/gin"
)

type AdminDestinationExceptionsHandler struct{ manager *destpolicy.ExceptionManager }

func NewAdminDestinationExceptionsHandler(manager *destpolicy.ExceptionManager) *AdminDestinationExceptionsHandler {
	return &AdminDestinationExceptionsHandler{manager: manager}
}
func (h *AdminDestinationExceptionsHandler) Create(c *gin.Context) {
	if h.manager == nil {
		c.JSON(503, gin.H{"error": "destination exceptions unavailable"})
		return
	}
	var req struct {
		Target  string `json:"target"`
		Match   string `json:"match"`
		Scope   string `json:"scope"`
		GroupID *int64 `json:"group_id"`
	}
	if !destinationPolicyDecode(c, &req) {
		return
	}
	if req.Scope == "group" {
		if req.GroupID == nil || *req.GroupID <= 0 {
			c.JSON(400, gin.H{"error": "dest_policy_invalid", "field": "group_id"})
			return
		}
		result, err := h.manager.Group(c.Request.Context(), *req.GroupID, req.Target, req.Match)
		if err != nil {
			if errors.Is(err, domain.ErrNotFound) {
				c.JSON(404, gin.H{"error": "dest_group_not_found"})
				return
			}
			destinationPolicyError(c, err)
			return
		}
		c.JSON(200, gin.H{"group_id": *req.GroupID, "list_id": result.Commit.ListID, "entry": result.Entry})
		return
	}
	if req.Scope != "global" || req.GroupID != nil {
		field := "scope"
		if req.Scope == "global" {
			field = "group_id"
		}
		c.JSON(400, gin.H{"error": "dest_policy_invalid", "field": field})
		return
	}
	result, err := h.manager.Global(c.Request.Context(), req.Target, req.Match)
	if err != nil {
		destinationPolicyError(c, err)
		return
	}
	view := gin.H{"list_id": result.Commit.ListID, "policy_id": result.Commit.PolicyID, "entry": result.Entry}
	status := 200
	if result.Commit.Created {
		status = 201
		view["created"] = gin.H{"list_id": result.Commit.ListID, "policy_id": result.Commit.PolicyID}
	}
	c.JSON(status, view)
}
