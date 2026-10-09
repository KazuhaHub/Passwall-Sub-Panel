package handler

import (
	"net/http"
	"strconv"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
	"github.com/KazuhaHub/passwall-sub-panel/internal/transport/http/middleware"
	"github.com/gin-gonic/gin"
)

type LegalAdminHandler struct{ repo ports.LegalRepo }

func NewLegalAdminHandler(repo ports.LegalRepo) *LegalAdminHandler {
	return &LegalAdminHandler{repo: repo}
}

// Collection is authenticated so an administrator can preview the actual
// saved collection policy before enabling or publishing legal documents.
func (h *LegalAdminHandler) Collection(c *gin.Context) {
	if h.repo == nil {
		respondError(c, domain.ErrUnavailable)
		return
	}
	data, err := h.repo.DataCollection(c.Request.Context())
	if err != nil {
		respondError(c, err)
		return
	}
	c.JSON(http.StatusOK, data)
}

func (h *LegalAdminHandler) Latest(c *gin.Context) {
	if h.repo == nil {
		respondError(c, domain.ErrUnavailable)
		return
	}
	doc, err := h.repo.Latest(c.Request.Context(), c.Param("kind"), c.DefaultQuery("lang", "zh-CN"))
	if err != nil {
		respondError(c, err)
		return
	}
	c.JSON(http.StatusOK, doc)
}

func (h *LegalAdminHandler) History(c *gin.Context) {
	if h.repo == nil {
		respondError(c, domain.ErrUnavailable)
		return
	}
	before, err := strconv.ParseInt(c.DefaultQuery("before_id", "0"), 10, 64)
	if err != nil {
		respondError(c, domain.ErrValidation)
		return
	}
	limit, err := strconv.Atoi(c.DefaultQuery("limit", "50"))
	if err != nil {
		respondError(c, domain.ErrValidation)
		return
	}
	items, err := h.repo.HistoryByKind(c.Request.Context(), c.Param("kind"), before, limit)
	if err != nil {
		respondError(c, err)
		return
	}
	var next int64
	if len(items) == limit {
		next = items[len(items)-1].ID
	}
	c.JSON(http.StatusOK, gin.H{"items": items, "next_before_id": next})
}

func (h *LegalAdminHandler) Publish(c *gin.Context) {
	claims := middleware.ClaimsFrom(c)
	if claims == nil || claims.UserID <= 0 {
		respondError(c, domain.ErrUnauthorized)
		return
	}
	if h.repo == nil {
		respondError(c, domain.ErrUnavailable)
		return
	}
	var req struct {
		Locale      string `json:"locale"`
		Content     string `json:"content"`
		ConsentBump bool   `json:"consent_bump"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		respondError(c, domain.ErrValidation)
		return
	}
	p, err := h.repo.Publish(c.Request.Context(), domain.LegalDraft{Kind: c.Param("kind"), Locale: req.Locale, Content: req.Content, ConsentBump: req.ConsentBump, PublishedBy: claims.UserID})
	if err != nil {
		respondError(c, err)
		return
	}
	c.JSON(http.StatusOK, p)
}

func (h *LegalAdminHandler) AffectedUsers(c *gin.Context) {
	if h.repo == nil {
		respondError(c, domain.ErrUnavailable)
		return
	}
	n, err := h.repo.AffectedUsers(c.Request.Context())
	if err != nil {
		respondError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"count": n})
}
