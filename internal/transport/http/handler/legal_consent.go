package handler

import (
	"net/http"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
	"github.com/KazuhaHub/passwall-sub-panel/internal/transport/http/middleware"
	"github.com/gin-gonic/gin"
)

type LegalConsentHandler struct{ repo ports.LegalRepo }

func NewLegalConsentHandler(repo ports.LegalRepo) *LegalConsentHandler {
	return &LegalConsentHandler{repo: repo}
}

func (h *LegalConsentHandler) Accept(c *gin.Context) {
	claims := middleware.ClaimsFrom(c)
	if claims == nil {
		respondPublicError(c, domain.ErrUnauthorized)
		return
	}
	if h.repo == nil {
		respondPublicError(c, domain.ErrUnavailable)
		return
	}
	var req struct {
		ConsentVersion int64 `json:"consent_version"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		respondPublicError(c, domain.ErrValidation)
		return
	}
	if err := h.repo.Accept(c.Request.Context(), claims.UserID, req.ConsentVersion); err != nil {
		respondPublicError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true})
}
