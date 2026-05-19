package handler

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
	"github.com/KazuhaHub/passwall-sub-panel/internal/seed"
)

// AdminTemplatesHandler exposes CRUD for the YAML-backed config templates
// under /api/admin/templates. One file per slug.
type AdminTemplatesHandler struct {
	repo      ports.TemplateRepo
	configDir string
}

func NewAdminTemplatesHandler(repo ports.TemplateRepo, configDir string) *AdminTemplatesHandler {
	return &AdminTemplatesHandler{repo: repo, configDir: configDir}
}

type templateDTO struct {
	Slug            string   `json:"slug"`
	Name            string   `json:"name"`
	ClientType      string   `json:"client_type"`
	IsDefault       bool     `json:"is_default"`
	RuleSets        []string `json:"rule_sets"`
	ProxyGroupOrder []string `json:"proxy_group_order"`
	Content         string   `json:"content"`
}

func (h *AdminTemplatesHandler) List(c *gin.Context) {
	items, err := h.repo.List(c.Request.Context())
	if err != nil {
		respondError(c, err)
		return
	}
	out := make([]templateDTO, len(items))
	for i, t := range items {
		out[i] = templateDTO{
			Slug: t.Slug, Name: t.Name,
			ClientType:      string(t.ClientType),
			IsDefault:       t.IsDefault,
			RuleSets:        t.RuleSets,
			ProxyGroupOrder: t.ProxyGroupOrder,
			Content:         t.Content,
		}
	}
	c.JSON(http.StatusOK, gin.H{"items": out})
}

func (h *AdminTemplatesHandler) Get(c *gin.Context) {
	slug := c.Param("slug")
	t, err := h.repo.GetBySlug(c.Request.Context(), slug)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": "Not found"})
			return
		}
		respondError(c, err)
		return
	}
	c.JSON(http.StatusOK, templateDTO{
		Slug: t.Slug, Name: t.Name,
		ClientType:      string(t.ClientType),
		IsDefault:       t.IsDefault,
		RuleSets:        t.RuleSets,
		ProxyGroupOrder: t.ProxyGroupOrder,
		Content:         t.Content,
	})
}

func (h *AdminTemplatesHandler) Save(c *gin.Context) {
	var req templateDTO
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if req.Slug == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Slug required"})
		return
	}
	if err := h.repo.Save(c.Request.Context(), &domain.Template{
		Slug:            req.Slug,
		Name:            req.Name,
		ClientType:      domain.ClientType(req.ClientType),
		IsDefault:       req.IsDefault,
		RuleSets:        req.RuleSets,
		ProxyGroupOrder: req.ProxyGroupOrder,
		Content:         req.Content,
	}); err != nil {
		if errors.Is(err, domain.ErrValidation) {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		respondError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

func (h *AdminTemplatesHandler) Delete(c *gin.Context) {
	slug := c.Param("slug")
	if err := h.repo.Delete(c.Request.Context(), slug); err != nil {
		if errors.Is(err, domain.ErrValidation) {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		respondError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

// Reset overwrites the on-disk template file with the binary's embedded
// seed copy. Used by the admin "Reset to default" affordance for slugs
// that originated from the seed bundle (default-mihomo, default-sing-box).
// 404 when the slug has no embedded counterpart, so user-authored
// templates can't accidentally trigger a destructive write that has
// nothing to restore.
func (h *AdminTemplatesHandler) Reset(c *gin.Context) {
	slug := c.Param("slug")
	if err := seed.Restore(h.configDir, "templates/"+slug+".yaml"); err != nil {
		if errors.Is(err, seed.ErrSeedNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": "No embedded default for this slug"})
			return
		}
		respondError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}
