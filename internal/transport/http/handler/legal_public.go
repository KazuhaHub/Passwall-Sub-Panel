package handler

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
	"github.com/gin-gonic/gin"
)

type LegalPublicHandler struct{ repo ports.LegalRepo }

func NewLegalPublicHandler(repo ports.LegalRepo) *LegalPublicHandler {
	return &LegalPublicHandler{repo: repo}
}

func (h *LegalPublicHandler) Get(c *gin.Context) {
	if h.repo == nil {
		respondPublicError(c, domain.ErrUnavailable)
		return
	}
	doc, err := h.repo.Public(c.Request.Context(), c.Param("kind"), c.Query("lang"))
	if err != nil {
		respondPublicError(c, err)
		return
	}
	body, err := json.Marshal(doc)
	if err != nil {
		respondPublicError(c, err)
		return
	}
	// Hash the complete representation, including fallback, global consent
	// and effective collection policy. A settings change must invalidate a
	// previously cached document even when its Markdown version is unchanged.
	etag := fmt.Sprintf("\"%x\"", sha256.Sum256(body))
	c.Header("ETag", etag)
	c.Header("Cache-Control", "no-cache")
	for _, candidate := range strings.Split(c.GetHeader("If-None-Match"), ",") {
		candidate = strings.TrimSpace(candidate)
		if candidate == "*" || strings.TrimPrefix(candidate, "W/") == etag {
			c.Status(http.StatusNotModified)
			return
		}
	}
	c.Data(http.StatusOK, "application/json; charset=utf-8", body)
}
