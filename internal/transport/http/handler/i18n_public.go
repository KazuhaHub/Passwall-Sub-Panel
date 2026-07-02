package handler

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
	"github.com/KazuhaHub/passwall-sub-panel/internal/service/locale"
)

// I18nPublicHandler serves uploaded language packs to the SPA. These endpoints
// are PUBLIC (registered outside the auth groups): the login screen must be able
// to render in a custom language before anyone authenticates. They expose only
// UI translation text — no secrets — which is an intentional, documented choice.
type I18nPublicHandler struct {
	repo ports.LocaleRepo
}

func NewI18nPublicHandler(repo ports.LocaleRepo) *I18nPublicHandler {
	return &I18nPublicHandler{repo: repo}
}

// Langs returns the manifest of uploaded packs (the SPA already knows the two
// compiled-in built-ins). Body-less rows keep this cheap on the boot path.
func (h *I18nPublicHandler) Langs(c *gin.Context) {
	metas, err := h.repo.List(c.Request.Context())
	if err != nil {
		respondPublicError(c, err)
		return
	}
	out := make([]localeMetaDTO, len(metas))
	for i, m := range metas {
		out[i] = metaToDTO(m)
	}
	c.JSON(http.StatusOK, out)
}

// Bundle returns one pack's translation namespaces, with an ETag so the browser
// can revalidate (Cache-Control: no-cache → conditional GET → 304 when unchanged).
// Reserved built-in codes 404 here — they are served by the JS bundle, never disk.
func (h *I18nPublicHandler) Bundle(c *gin.Context) {
	lang := c.Param("lang")
	if locale.IsReserved(lang) {
		c.JSON(http.StatusNotFound, gin.H{"error": "Not found"})
		return
	}
	// Resolve the current ETag from the (mtime-cached) manifest so we can answer
	// a conditional request without reading the full body.
	metas, err := h.repo.List(c.Request.Context())
	if err != nil {
		respondPublicError(c, err)
		return
	}
	var etag string
	found := false
	for _, m := range metas {
		if m.Code == lang {
			etag, found = m.ETag, true
			break
		}
	}
	if !found {
		c.JSON(http.StatusNotFound, gin.H{"error": "Not found"})
		return
	}
	if etag != "" && c.GetHeader("If-None-Match") == etag {
		c.Status(http.StatusNotModified)
		return
	}
	pack, err := h.repo.Get(c.Request.Context(), lang)
	if err != nil {
		respondPublicError(c, err)
		return
	}
	if etag != "" {
		c.Header("ETag", etag)
	}
	c.Header("Cache-Control", "no-cache")
	c.JSON(http.StatusOK, gin.H{"namespaces": pack.Namespaces})
}
