package handler

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/transport/http/middleware"
)

// Begin2FA starts TOTP enrollment for the calling user: it generates a secret
// (stored disabled until confirmed) and returns the otpauth URL for the QR plus
// the raw secret for manual entry. Enrollment must be confirmed via Enable2FA.
func (h *UserMeHandler) Begin2FA(c *gin.Context) {
	claims := middleware.ClaimsFrom(c)
	if claims == nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "No auth"})
		return
	}
	otpauthURL, secret, err := h.twofa.Begin(c.Request.Context(), claims.UserID)
	if err != nil {
		respondError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"otpauth_url": otpauthURL, "secret": secret})
}

type enable2FARequest struct {
	Code string `json:"code" binding:"required"`
}

// Enable2FA confirms enrollment with a code from the authenticator app and, on
// success, returns one-time recovery codes to display ONCE.
func (h *UserMeHandler) Enable2FA(c *gin.Context) {
	claims := middleware.ClaimsFrom(c)
	if claims == nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "No auth"})
		return
	}
	var req enable2FARequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	codes, err := h.twofa.Enable(c.Request.Context(), claims.UserID, req.Code)
	if err != nil {
		if errors.Is(err, domain.ErrUnauthorized) {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "Invalid code"})
			return
		}
		respondError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"recovery_codes": codes})
}

type disable2FARequest struct {
	Code string `json:"code" binding:"required"`
}

// Disable2FA turns 2FA off, requiring a valid current TOTP or recovery code as
// proof of possession.
func (h *UserMeHandler) Disable2FA(c *gin.Context) {
	claims := middleware.ClaimsFrom(c)
	if claims == nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "No auth"})
		return
	}
	var req disable2FARequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if err := h.twofa.Disable(c.Request.Context(), claims.UserID, req.Code); err != nil {
		if errors.Is(err, domain.ErrUnauthorized) {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "Invalid code"})
			return
		}
		respondError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

type regenerate2FARecoveryRequest struct {
	Code string `json:"code" binding:"required"`
}

// RegenerateRecovery2FA rotates the calling user's one-time recovery codes,
// requiring a current TOTP or recovery code as step-up proof, and returns the
// fresh set to display ONCE. 2FA must already be enabled.
func (h *UserMeHandler) RegenerateRecovery2FA(c *gin.Context) {
	claims := middleware.ClaimsFrom(c)
	if claims == nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "No auth"})
		return
	}
	var req regenerate2FARecoveryRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	codes, err := h.twofa.RegenerateRecovery(c.Request.Context(), claims.UserID, req.Code)
	if err != nil {
		if errors.Is(err, domain.ErrUnauthorized) {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "Invalid code"})
			return
		}
		respondError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"recovery_codes": codes})
}
