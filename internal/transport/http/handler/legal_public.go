package handler

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/service/legal"
)

// LegalPublicHandler serves GET /api/legal/:kind, the terms and privacy
// documents the login, register and user pages link to. No session: a person
// deciding whether to register has none.
type LegalPublicHandler struct{ svc *legal.Service }

// NewLegalPublicHandler serves the documents out of svc. A nil svc answers 404
// like a panel with the feature off.
func NewLegalPublicHandler(svc *legal.Service) *LegalPublicHandler {
	return &LegalPublicHandler{svc: svc}
}

type legalPublicResponse struct {
	Kind           domain.LegalKind      `json:"kind"`
	Version        int                   `json:"version"`
	ConsentVersion int                   `json:"consent_version"`
	Locale         string                `json:"locale"`
	FallbackFrom   string                `json:"fallback_from,omitempty"`
	Content        string                `json:"content"`
	PublishedAt    time.Time             `json:"published_at"`
	DataCollection map[string]legal.Item `json:"data_collection"`
}

// Get answers the document in the requested language (lang), following the
// fallback chain. 404 while the feature is off, for an unknown kind and when
// nothing is published. The ETag is a digest of the whole answer, the
// data_collection list included: a retention an administrator changes makes it
// a different page even though the document did not move. no-cache on both the
// 200 and the 304, so the browser revalidates every time and a 304 does not
// evict the stored response (the same arrangement as /api/i18n/:lang).
func (h *LegalPublicHandler) Get(c *gin.Context) {
	kind, ok := domain.ParseLegalKind(c.Param("kind"))
	if !ok || h.svc == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Not found"})
		return
	}
	doc, err := h.svc.Public(c.Request.Context(), kind, c.Query("lang"))
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": "Not found"})
			return
		}
		respondPublicError(c, err)
		return
	}
	body, err := json.Marshal(legalPublicResponse{
		Kind: doc.Kind, Version: doc.Version, ConsentVersion: doc.ConsentVersion, Locale: doc.Locale, FallbackFrom: doc.FallbackFrom,
		Content: doc.Content, PublishedAt: doc.PublishedAt, DataCollection: doc.DataCollection,
	})
	if err != nil {
		respondPublicError(c, err)
		return
	}
	sum := sha256.Sum256(body)
	etag := `"` + hex.EncodeToString(sum[:16]) + `"`
	c.Header("ETag", etag)
	c.Header("Cache-Control", "no-cache")
	if etagMatches(c.GetHeader("If-None-Match"), etag) {
		c.Status(http.StatusNotModified)
		return
	}
	c.Data(http.StatusOK, "application/json; charset=utf-8", body)
}
