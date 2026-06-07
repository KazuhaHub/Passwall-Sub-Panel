package handler

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/KazuhaHub/passwall-sub-panel/internal/transport/http/middleware"
)

// Passkey step-up lets a logged-in user authorize a sensitive 2FA-management
// action with a passkey assertion instead of a TOTP / recovery code — so someone
// who holds a passkey (their strong factor) but no longer has their authenticator
// can still: disable TOTP, or regenerate recovery codes. The assertion is
// allow-listed to THIS user's credentials (passkey.*ForUser), and the user id
// comes from the session, never from the assertion.

// StepUpPasskeyBegin starts a passkey assertion for the calling user. Errors
// (400) if the account has no passkey enrolled.
func (h *UserMeHandler) StepUpPasskeyBegin(c *gin.Context) {
	claims := middleware.ClaimsFrom(c)
	if claims == nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "No auth"})
		return
	}
	if h.passkey == nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Passkeys are not available"})
		return
	}
	opts, sessionID, err := h.passkey.BeginLoginForUser(c.Request.Context(), claims.UserID)
	if err != nil {
		respondError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"session_id": sessionID, "publicKey": opts})
}

// StepUpPasskeyFinish verifies the assertion (request body) for the stashed
// session (?session=) and, on success, performs ?action=: "disable" turns TOTP
// off, "recovery" rotates the recovery codes and returns the fresh set ONCE. The
// passkey assertion is the proof of possession, so no code is required.
func (h *UserMeHandler) StepUpPasskeyFinish(c *gin.Context) {
	claims := middleware.ClaimsFrom(c)
	if claims == nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "No auth"})
		return
	}
	if h.passkey == nil || h.twofa == nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Not available"})
		return
	}
	action := c.Query("action")
	if action != "disable" && action != "recovery" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Unknown step-up action"})
		return
	}
	if err := h.passkey.FinishLoginForUser(c.Request.Context(), claims.UserID, c.Query("session"), c.Request); err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Passkey verification failed"})
		return
	}
	switch action {
	case "disable":
		if err := h.twofa.DisableProven(c.Request.Context(), claims.UserID); err != nil {
			respondError(c, err)
			return
		}
		c.Status(http.StatusNoContent)
	case "recovery":
		codes, err := h.twofa.AdminRegenerateRecovery(c.Request.Context(), claims.UserID)
		if err != nil {
			respondError(c, err)
			return
		}
		c.JSON(http.StatusOK, gin.H{"recovery_codes": codes})
	}
}
