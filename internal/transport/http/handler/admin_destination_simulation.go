package handler

import (
	"context"

	"github.com/KazuhaHub/passwall-sub-panel/internal/service/destpolicy"
	"github.com/gin-gonic/gin"
)

type AdminDestinationTestHandler struct {
	test func(context.Context, destpolicy.DestinationTestInput) (destpolicy.DestinationTestResult, error)
}

func NewAdminDestinationTestHandler(test func(context.Context, destpolicy.DestinationTestInput) (destpolicy.DestinationTestResult, error)) *AdminDestinationTestHandler {
	return &AdminDestinationTestHandler{test: test}
}
func (h *AdminDestinationTestHandler) Test(c *gin.Context) {
	var req struct {
		Target  string  `json:"target"`
		Port    *int64  `json:"port"`
		Network *string `json:"network"`
		UserID  *int64  `json:"user_id"`
		PanelID *int64  `json:"panel_id"`
	}
	if !destinationPolicyDecode(c, &req) {
		return
	}
	input := destpolicy.DestinationTestInput{Target: req.Target, Port: 443, Network: "tcp"}
	field := ""
	if req.Port != nil {
		if *req.Port < 1 || *req.Port > 65535 {
			field = "port"
		} else {
			input.Port = uint16(*req.Port)
		}
	}
	if req.Network != nil {
		if *req.Network != "tcp" && *req.Network != "udp" {
			field = "network"
		} else {
			input.Network = *req.Network
		}
	}
	if req.UserID != nil {
		if *req.UserID <= 0 {
			field = "user_id"
		} else {
			input.UserID = *req.UserID
		}
	}
	if req.PanelID != nil {
		if *req.PanelID <= 0 {
			field = "panel_id"
		} else {
			input.PanelID = *req.PanelID
		}
	}
	if field != "" {
		c.JSON(400, gin.H{"error": "dest_policy_invalid", "field": field})
		return
	}
	if _, _, err := destpolicy.NormalizeTestTarget(input.Target); err != nil {
		destinationPolicyError(c, err)
		return
	}
	if h.test == nil {
		c.JSON(503, gin.H{"error": "destination test unavailable"})
		return
	}
	result, err := h.test(c.Request.Context(), input)
	if err != nil {
		destinationPolicyError(c, err)
		return
	}
	c.JSON(200, result)
}
