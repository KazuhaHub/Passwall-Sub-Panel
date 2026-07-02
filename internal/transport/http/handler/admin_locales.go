package handler

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
	"github.com/KazuhaHub/passwall-sub-panel/internal/service/locale"
)

// maxLocalePackBytes caps a single upload. A fully-translated pack (7 namespaces,
// admin.json alone ~57 KiB) lands around ~150 KiB, so 512 KiB is generous while
// still rejecting an absurd body early — tighter than the global 1 MiB BodyLimit.
const maxLocalePackBytes = 512 << 10

// AdminLocalesHandler exposes CRUD for runtime-uploaded UI language packs under
// /api/admin/locales. One JSON file per language code. Writes are admin-only
// (router.go wires PUT/DELETE on adminGroup); List is staff-visible.
type AdminLocalesHandler struct {
	repo ports.LocaleRepo
}

func NewAdminLocalesHandler(repo ports.LocaleRepo) *AdminLocalesHandler {
	return &AdminLocalesHandler{repo: repo}
}

// localeMetaDTO is the manifest row (no translation bodies).
type localeMetaDTO struct {
	Code        string `json:"code"`
	Name        string `json:"name"`
	Author      string `json:"author,omitempty"`
	BaseVersion string `json:"base_version,omitempty"`
	ETag        string `json:"etag,omitempty"`
}

// localePackDTO is the full upload/wire shape (mirrors the on-disk JSON file).
type localePackDTO struct {
	Format       int                       `json:"psp_language_pack"`
	Code         string                    `json:"code"`
	Name         string                    `json:"name"`
	Author       string                    `json:"author"`
	BaseLanguage string                    `json:"base_language"`
	BaseVersion  string                    `json:"base_version"`
	Namespaces   map[string]map[string]any `json:"namespaces"`
}

func (d localePackDTO) toDomain() *domain.LocalePack {
	return &domain.LocalePack{
		Format:       d.Format,
		Code:         d.Code,
		Name:         d.Name,
		Author:       d.Author,
		BaseLanguage: d.BaseLanguage,
		BaseVersion:  d.BaseVersion,
		Namespaces:   d.Namespaces,
	}
}

func metaToDTO(m domain.LocaleMeta) localeMetaDTO {
	return localeMetaDTO{Code: m.Code, Name: m.Name, Author: m.Author, BaseVersion: m.BaseVersion, ETag: m.ETag}
}

func (h *AdminLocalesHandler) List(c *gin.Context) {
	metas, err := h.repo.List(c.Request.Context())
	if err != nil {
		respondError(c, err)
		return
	}
	out := make([]localeMetaDTO, len(metas))
	for i, m := range metas {
		out[i] = metaToDTO(m)
	}
	c.JSON(http.StatusOK, out)
}

func (h *AdminLocalesHandler) Save(c *gin.Context) {
	// Enforce the per-pack cap before decoding: MaxBytesReader makes an oversized
	// body fail the bind, so we never buffer/parse a huge upload.
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxLocalePackBytes)
	var req localePackDTO
	if err := c.ShouldBindJSON(&req); err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			c.JSON(http.StatusRequestEntityTooLarge, gin.H{"error": "language pack too large"})
			return
		}
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	code := c.Param("code")
	// The body's code is authoritative (it's the filename stem). Accept a body
	// that omits code by adopting the URL param; reject a genuine mismatch so a
	// PUT to /locales/de-DE can never silently write fr-FR.
	if req.Code == "" {
		req.Code = code
	}
	if code != "" && req.Code != code {
		c.JSON(http.StatusBadRequest, gin.H{"error": "language code in URL and body must match"})
		return
	}
	pack := req.toDomain()
	if err := locale.Validate(pack); err != nil {
		respondError(c, err) // ErrValidation → 400 with the author-facing message
		return
	}
	if err := h.repo.Save(c.Request.Context(), pack); err != nil {
		respondError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

func (h *AdminLocalesHandler) Delete(c *gin.Context) {
	code := c.Param("code")
	// Built-ins live in the JS bundle, not on disk — there is nothing to delete
	// and no way to "restore" them, so guard explicitly rather than 404.
	if locale.IsReserved(code) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "built-in language cannot be deleted"})
		return
	}
	if err := h.repo.Delete(c.Request.Context(), code); err != nil {
		respondError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}
