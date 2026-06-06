package handler

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/KazuhaHub/passwall-sub-panel/internal/pkg/log"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
	"github.com/KazuhaHub/passwall-sub-panel/internal/service/captcha"
)

// requireCaptcha verifies the captcha for a self-service form when the given
// per-context toggle is on. It returns true to proceed; on failure it writes a
// 400 (with captcha_required so the SPA shows/refreshes the widget) and returns
// false. A verify ERROR (misconfigured provider / siteverify outage) fails
// CLOSED — the gate must not silently open. When enabled is false it's a no-op.
func requireCaptcha(c *gin.Context, capSvc *captcha.Service, s ports.UISettings, enabled bool, id, answer, token string) bool {
	if !enabled || capSvc == nil {
		return true
	}
	ok, err := capSvc.Verify(c.Request.Context(), s, captcha.Response{
		ChallengeID: id, Answer: answer, Token: token, RemoteIP: c.ClientIP(),
	})
	if err != nil {
		log.Warn("captcha verify error", "err", err)
	}
	if err != nil || !ok {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Captcha is required or incorrect", "captcha_required": true})
		return false
	}
	return true
}
