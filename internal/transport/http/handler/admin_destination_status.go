package handler

import (
	"context"
	"github.com/KazuhaHub/passwall-sub-panel/internal/service/destpolicy"
	"github.com/gin-gonic/gin"
)

type AdminDestinationStatusHandler struct {
	read func(context.Context) (destpolicy.DestinationStatus, error)
}

func NewAdminDestinationStatusHandler(read func(context.Context) (destpolicy.DestinationStatus, error)) *AdminDestinationStatusHandler {
	return &AdminDestinationStatusHandler{read: read}
}
func (h *AdminDestinationStatusHandler) Get(c *gin.Context) {
	if h.read == nil {
		c.JSON(503, gin.H{"error": "destination status unavailable"})
		return
	}
	view, err := h.read(c.Request.Context())
	if err != nil {
		destinationPolicyError(c, err)
		return
	}
	c.JSON(200, view)
}
