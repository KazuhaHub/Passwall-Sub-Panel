package handler

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
	"github.com/KazuhaHub/passwall-sub-panel/internal/seed"
)

// AdminRuleSetsHandler exposes CRUD for rule sets under /api/admin/rules.
type AdminRuleSetsHandler struct {
	repo      ports.RuleSetRepo
	configDir string
}

func NewAdminRuleSetsHandler(repo ports.RuleSetRepo, configDir string) *AdminRuleSetsHandler {
	return &AdminRuleSetsHandler{repo: repo, configDir: configDir}
}

type ruleSetDTO struct {
	Slug            string   `json:"slug"`
	Name            string   `json:"name"`
	Sort            int      `json:"sort"`
	Enabled         bool     `json:"enabled"`
	ProxyGroupOrder []string `json:"proxy_group_order"`
	Content         string   `json:"content"`
}

func (h *AdminRuleSetsHandler) List(c *gin.Context) {
	p := parsePagination(c)
	items, total, err := h.repo.ListPaged(c.Request.Context(), p)
	if err != nil {
		respondError(c, err)
		return
	}
	out := make([]ruleSetDTO, len(items))
	for i, r := range items {
		out[i] = ruleSetDTO{
			Slug: r.Slug, Name: r.Name, Sort: r.Sort,
			Enabled:         r.Enabled,
			ProxyGroupOrder: r.ProxyGroupOrder,
			Content:         r.Content,
		}
	}
	c.JSON(http.StatusOK, pagedEnvelope(out, total, p))
}

func (h *AdminRuleSetsHandler) Get(c *gin.Context) {
	slug := c.Param("slug")
	r, err := h.repo.GetBySlug(c.Request.Context(), slug)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": "Not found"})
			return
		}
		respondError(c, err)
		return
	}
	c.JSON(http.StatusOK, ruleSetDTO{
		Slug: r.Slug, Name: r.Name, Sort: r.Sort,
		Enabled:         r.Enabled,
		ProxyGroupOrder: r.ProxyGroupOrder,
		Content:         r.Content,
	})
}

func (h *AdminRuleSetsHandler) Save(c *gin.Context) {
	var req ruleSetDTO
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if req.Slug == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Slug required"})
		return
	}
	if err := h.repo.Save(c.Request.Context(), &domain.RuleSet{
		Slug: req.Slug, Name: req.Name, Sort: req.Sort,
		Enabled:         req.Enabled,
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

func (h *AdminRuleSetsHandler) Delete(c *gin.Context) {
	slug := c.Param("slug")
	// Seeded rulesets are protected for the same reason as seeded
	// templates — deletion would orphan a canonical default and the
	// Reset button can't recover from a deleted slug.
	if seed.HasSeededSlug("rulesets", slug) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Seeded ruleset cannot be deleted; use Reset to restore it instead"})
		return
	}
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

// Reset overwrites the on-disk ruleset file with the binary's embedded
// seed copy. Same pattern as AdminTemplatesHandler.Reset. 404 when the
// slug has no embedded counterpart.
func (h *AdminRuleSetsHandler) Reset(c *gin.Context) {
	slug := c.Param("slug")
	if err := seed.RestoreBySlug(h.configDir, "rulesets", slug); err != nil {
		if errors.Is(err, seed.ErrSeedNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": "No embedded default for this slug"})
			return
		}
		respondError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}
