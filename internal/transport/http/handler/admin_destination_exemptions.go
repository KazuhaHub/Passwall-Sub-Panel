package handler

import (
	"errors"
	"strconv"
	"time"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/service/destpolicy"
	"github.com/KazuhaHub/passwall-sub-panel/internal/transport/http/middleware"
	"github.com/gin-gonic/gin"
)

type AdminDestinationExemptionsHandler struct{ manager *destpolicy.ExemptionManager }

func NewAdminDestinationExemptionsHandler(manager *destpolicy.ExemptionManager) *AdminDestinationExemptionsHandler {
	return &AdminDestinationExemptionsHandler{manager: manager}
}
func (h *AdminDestinationExemptionsHandler) available(c *gin.Context) bool {
	if h.manager == nil {
		c.JSON(503, gin.H{"error": "destination exemptions unavailable"})
		return false
	}
	return true
}
func destinationExemptionError(c *gin.Context, err error) {
	if errors.Is(err, domain.ErrAlreadyExists) {
		c.JSON(409, gin.H{"error": "dest_exemption_exists"})
		return
	}
	destinationPolicyError(c, err)
}
func destinationExemptionView(view destpolicy.ExemptionView) gin.H {
	ex := view.Exemption
	return gin.H{"user_id": ex.UserID, "upn": view.UPN, "reason": ex.Reason, "created_by": ex.CreatedBy, "created_by_upn": view.CreatedByUPN, "created_at": ex.CreatedAt.UnixMilli(), "expires_at": destinationTime(ex.ExpiresAt), "expired": view.Expired}
}
func destinationExemptionID(c *gin.Context) (int64, bool) {
	id, err := strconv.ParseInt(c.Param("user_id"), 10, 64)
	if err != nil || id <= 0 {
		c.JSON(400, gin.H{"error": "dest_policy_invalid", "field": "user_id"})
		return 0, false
	}
	return id, true
}
func decodeDestinationExemption(c *gin.Context, pathID int64) (domain.DestExemption, bool) {
	var req struct {
		UserID    *int64  `json:"user_id"`
		Reason    *string `json:"reason"`
		ExpiresAt *int64  `json:"expires_at"`
	}
	if !destinationPolicyDecode(c, &req) {
		return domain.DestExemption{}, false
	}
	field := ""
	id := pathID
	if req.UserID != nil {
		if *req.UserID <= 0 || pathID > 0 && *req.UserID != pathID {
			field = "user_id"
		}
		id = *req.UserID
	}
	if id <= 0 {
		field = "user_id"
	}
	if req.Reason == nil {
		field = "reason"
	}
	if req.ExpiresAt != nil && *req.ExpiresAt <= 0 {
		field = "expires_at"
	}
	if field != "" {
		c.JSON(400, gin.H{"error": "dest_policy_invalid", "field": field})
		return domain.DestExemption{}, false
	}
	ex := domain.DestExemption{UserID: id, Reason: *req.Reason}
	if req.ExpiresAt != nil {
		expiry := time.UnixMilli(*req.ExpiresAt).UTC()
		ex.ExpiresAt = &expiry
	}
	return ex, true
}
func (h *AdminDestinationExemptionsHandler) List(c *gin.Context) {
	if !h.available(c) {
		return
	}
	rows, err := h.manager.List(c.Request.Context())
	if err != nil {
		destinationExemptionError(c, err)
		return
	}
	items := make([]gin.H, 0, len(rows))
	for _, row := range rows {
		items = append(items, destinationExemptionView(row))
	}
	c.JSON(200, gin.H{"items": items})
}
func (h *AdminDestinationExemptionsHandler) Get(c *gin.Context) {
	if !h.available(c) {
		return
	}
	id, ok := destinationExemptionID(c)
	if !ok {
		return
	}
	view, err := h.manager.Get(c.Request.Context(), id)
	if err != nil {
		destinationExemptionError(c, err)
		return
	}
	c.JSON(200, destinationExemptionView(view))
}
func (h *AdminDestinationExemptionsHandler) Create(c *gin.Context) {
	if !h.available(c) {
		return
	}
	ex, ok := decodeDestinationExemption(c, 0)
	if !ok {
		return
	}
	view, err := h.manager.Save(c.Request.Context(), ex, c.GetInt64(middleware.CtxUserID), true)
	if err != nil {
		destinationExemptionError(c, err)
		return
	}
	c.JSON(201, destinationExemptionView(view))
}
func (h *AdminDestinationExemptionsHandler) Put(c *gin.Context) {
	if !h.available(c) {
		return
	}
	id, ok := destinationExemptionID(c)
	if !ok {
		return
	}
	ex, ok := decodeDestinationExemption(c, id)
	if !ok {
		return
	}
	view, err := h.manager.Save(c.Request.Context(), ex, c.GetInt64(middleware.CtxUserID), false)
	if err != nil {
		destinationExemptionError(c, err)
		return
	}
	c.JSON(200, destinationExemptionView(view))
}
func (h *AdminDestinationExemptionsHandler) Delete(c *gin.Context) {
	if !h.available(c) {
		return
	}
	id, ok := destinationExemptionID(c)
	if !ok {
		return
	}
	if err := h.manager.Delete(c.Request.Context(), id); err != nil {
		destinationExemptionError(c, err)
		return
	}
	c.Status(204)
}
