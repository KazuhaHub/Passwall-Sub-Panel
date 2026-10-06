package handler

import (
	"context"
	"strconv"
	"time"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/service/destpolicy"
	"github.com/gin-gonic/gin"
)

type AdminDestinationUsersHandler struct {
	read func(context.Context, int64) (domain.DestUserAccess, error)
}

func NewAdminDestinationUsersHandler(read func(context.Context, int64) (domain.DestUserAccess, error)) *AdminDestinationUsersHandler {
	return &AdminDestinationUsersHandler{read: read}
}

func (h *AdminDestinationUsersHandler) Get(c *gin.Context) {
	if h.read == nil {
		c.JSON(503, gin.H{"error": "destination user access unavailable"})
		return
	}
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		c.JSON(400, gin.H{"error": "dest_policy_invalid", "field": "user_id"})
		return
	}
	// Usage reads require stage-4 availability and their own audited boundary.
	if _, exists := c.Request.URL.Query()["usage"]; exists {
		c.JSON(400, gin.H{"error": "dest_policy_invalid", "field": "usage"})
		return
	}
	access, err := h.read(c.Request.Context(), id)
	if err != nil {
		destinationPolicyError(c, err)
		return
	}
	var group, exemption any
	if access.Group != nil {
		g := access.Group
		group = gin.H{"id": g.ID, "name": g.Name, "mode": g.Mode, "stage": g.Stage}
	}
	if access.Exemption != nil {
		ex := *access.Exemption
		exemption = destinationExemptionView(destpolicy.ExemptionView{Exemption: ex, UPN: &access.UPN, CreatedByUPN: access.CreatedByUPN, Expired: ex.ExpiresAt != nil && !ex.ExpiresAt.After(time.Now().UTC())})
	}
	c.JSON(200, gin.H{"group": group, "exemption": exemption, "hits_available": nil, "recent_hits": nil, "usage_available": nil, "usage_nodes": nil})
}
