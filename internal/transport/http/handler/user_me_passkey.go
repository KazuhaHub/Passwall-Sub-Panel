package handler

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/transport/http/middleware"
)

// passkeyDTO is the sanitized credential shape the profile/management UI sees —
// id + label + timestamps only, never the raw credential record or public key.
type passkeyDTO struct {
	ID         int64      `json:"id"`
	Name       string     `json:"name"`
	CreatedAt  time.Time  `json:"created_at"`
	LastUsedAt *time.Time `json:"last_used_at,omitempty"`
}

// passkeyList returns the caller's sanitized credential list (best-effort: a
// load error yields an empty list rather than failing the whole profile).
func (h *UserMeHandler) passkeyList(ctx context.Context, userID int64) []passkeyDTO {
	if h.passkey == nil {
		return []passkeyDTO{}
	}
	creds, err := h.passkey.List(ctx, userID)
	if err != nil {
		return []passkeyDTO{}
	}
	out := make([]passkeyDTO, 0, len(creds))
	for _, c := range creds {
		out = append(out, passkeyDTO{ID: c.ID, Name: c.Name, CreatedAt: c.CreatedAt, LastUsedAt: c.LastUsedAt})
	}
	return out
}

// BeginPasskeyEnroll starts registering a passkey for the logged-in user.
func (h *UserMeHandler) BeginPasskeyEnroll(c *gin.Context) {
	claims := middleware.ClaimsFrom(c)
	if claims == nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "No auth"})
		return
	}
	opts, sessionID, err := h.passkey.BeginRegistration(c.Request.Context(), claims.UserID)
	if err != nil {
		respondError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"session_id": sessionID, "publicKey": opts})
}

// FinishPasskeyEnroll verifies the attestation (request body) against the stashed
// session (?session=...) and stores the credential under ?name=. Returns the
// updated credential list.
func (h *UserMeHandler) FinishPasskeyEnroll(c *gin.Context) {
	claims := middleware.ClaimsFrom(c)
	if claims == nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "No auth"})
		return
	}
	sessionID := c.Query("session")
	name := c.Query("name")
	if _, err := h.passkey.FinishRegistration(c.Request.Context(), claims.UserID, sessionID, name, c.Request); err != nil {
		if errors.Is(err, domain.ErrValidation) {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		respondError(c, err)
		return
	}
	resp := gin.H{"passkeys": h.passkeyList(c.Request.Context(), claims.UserID)}
	// A passkey is now a second factor, so the account needs a printable fallback.
	// On the FIRST passkey (no recovery codes yet) mint a set and return it ONCE so
	// the UI can show it — exactly like enabling TOTP does. No-op if codes already
	// exist (e.g. the user also has TOTP, or this is their 2nd+ passkey).
	if h.twofa != nil {
		if codes, created, err := h.twofa.EnsureRecovery(c.Request.Context(), claims.UserID); err == nil && created {
			resp["recovery_codes"] = codes
		}
	}
	c.JSON(http.StatusOK, resp)
}

func (h *UserMeHandler) ListPasskeys(c *gin.Context) {
	claims := middleware.ClaimsFrom(c)
	if claims == nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "No auth"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"passkeys": h.passkeyList(c.Request.Context(), claims.UserID)})
}

type renamePasskeyRequest struct {
	Name string `json:"name" binding:"required"`
}

func (h *UserMeHandler) RenamePasskey(c *gin.Context) {
	claims := middleware.ClaimsFrom(c)
	if claims == nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "No auth"})
		return
	}
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid id"})
		return
	}
	var req renamePasskeyRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if err := h.passkey.Rename(c.Request.Context(), id, claims.UserID, req.Name); err != nil {
		if errors.Is(err, domain.ErrValidation) {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		respondError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

func (h *UserMeHandler) DeletePasskey(c *gin.Context) {
	claims := middleware.ClaimsFrom(c)
	if claims == nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "No auth"})
		return
	}
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid id"})
		return
	}
	if err := h.passkey.Delete(c.Request.Context(), id, claims.UserID); err != nil {
		respondError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}
