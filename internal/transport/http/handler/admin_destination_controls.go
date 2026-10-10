package handler

import (
	"errors"

	"github.com/KazuhaHub/passwall-sub-panel/internal/service/destpolicy"
	"github.com/gin-gonic/gin"
)

type AdminDestinationControlsHandler struct{ controls *destpolicy.Controls }

func NewAdminDestinationControlsHandler(controls *destpolicy.Controls) *AdminDestinationControlsHandler {
	return &AdminDestinationControlsHandler{controls: controls}
}
func destinationPublicationView(result destpolicy.PublicationResult) gin.H {
	return gin.H{"generation": result.Generation, "published_generation": result.PublishedGeneration, "paused": result.Paused, "publish_error": result.PublishError}
}
func (h *AdminDestinationControlsHandler) Publish(c *gin.Context) {
	result, err := h.controls.Publish(c.Request.Context())
	var rejection *destpolicy.PublicationRejection
	if errors.As(err, &rejection) {
		c.JSON(409, gin.H{"error": "dest_policy_over_limit", "publish_error": rejection.Detail})
		return
	}
	if err != nil {
		destinationPolicyError(c, err)
		return
	}
	c.JSON(200, destinationPublicationView(result))
}
func (h *AdminDestinationControlsHandler) Pause(c *gin.Context) {
	var req struct {
		Paused *bool `json:"paused"`
	}
	if !destinationPolicyDecode(c, &req) {
		return
	}
	if req.Paused == nil {
		c.JSON(400, gin.H{"error": "dest_policy_invalid", "field": "paused"})
		return
	}
	result, err := h.controls.Pause(c.Request.Context(), *req.Paused)
	var saved *destpolicy.PausePublicationError
	if errors.As(err, &saved) {
		c.JSON(503, gin.H{"error": "dest_policy_publish_unavailable", "pause_saved": true, "paused": saved.Paused})
		return
	}
	if err != nil {
		destinationPolicyError(c, err)
		return
	}
	c.JSON(200, destinationPublicationView(result))
}
