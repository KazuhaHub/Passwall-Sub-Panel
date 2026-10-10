package handler

import (
	"context"
	"net/http"

	"github.com/gin-gonic/gin"
)

type DestinationPolicyRetrier interface {
	RetryDestinationPolicy(context.Context, string) (bool, error)
}

type AdminDestinationRetryHandler struct{ retry DestinationPolicyRetrier }

func NewAdminDestinationRetryHandler(retry DestinationPolicyRetrier) *AdminDestinationRetryHandler {
	return &AdminDestinationRetryHandler{retry: retry}
}

func (h *AdminDestinationRetryHandler) Retry(c *gin.Context) {
	if h.retry == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "destination policy retry unavailable"})
		return
	}
	changed, err := h.retry.RetryDestinationPolicy(c.Request.Context(), c.Param("agent_id"))
	if err != nil {
		respondPublicError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"retry_requested": changed})
}
