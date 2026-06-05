package handler

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/KazuhaHub/passwall-sub-panel/internal/service/recovery"
)

// AuthRecoveryHandler exposes the public self-service password-recovery
// endpoints. Both sit behind the login rate limiter (wired in the router).
type AuthRecoveryHandler struct {
	recovery *recovery.Service
}

func NewAuthRecoveryHandler(r *recovery.Service) *AuthRecoveryHandler {
	return &AuthRecoveryHandler{recovery: r}
}

type forgotPasswordRequest struct {
	Ident string `json:"ident" binding:"required"`
}

// Forgot requests a reset email for the named account. It ALWAYS responds 200
// regardless of whether the account exists / has a password / has an email —
// the response must not let a caller enumerate accounts. The only side effect a
// real account sees is an email arriving.
func (h *AuthRecoveryHandler) Forgot(c *gin.Context) {
	var req forgotPasswordRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	// Errors are swallowed inside RequestReset (logged there); the handler can't
	// surface them without leaking existence.
	_ = h.recovery.RequestReset(c.Request.Context(), req.Ident)
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

type resetPasswordRequest struct {
	Token       string `json:"token"`
	Ident       string `json:"ident"`
	Code        string `json:"code"`
	NewPassword string `json:"new_password" binding:"required"`
}

// Reset verifies the token/code and applies the new password. 401 = invalid or
// expired token (deliberately generic); 400 = weak password.
func (h *AuthRecoveryHandler) Reset(c *gin.Context) {
	var req resetPasswordRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if err := h.recovery.Reset(c.Request.Context(), recovery.ResetInput{
		Token:       req.Token,
		Ident:       req.Ident,
		Code:        req.Code,
		NewPassword: req.NewPassword,
	}); err != nil {
		respondError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true})
}
