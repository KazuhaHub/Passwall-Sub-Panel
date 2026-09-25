package handler

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
	"github.com/KazuhaHub/passwall-sub-panel/internal/seed"
	groupsvc "github.com/KazuhaHub/passwall-sub-panel/internal/service/group"
	"github.com/KazuhaHub/passwall-sub-panel/internal/service/render"
)

// AdminRuleSetsHandler exposes CRUD for rule sets under /api/admin/rules.
type AdminRuleSetsHandler struct {
	repo       ports.RuleSetRepo
	templates  ports.TemplateRepo
	nodes      ruleNodeLister
	groups     *groupsvc.Service
	invalidate func()
	configDir  string
}

type ruleNodeLister interface {
	List(ctx context.Context) ([]*domain.Node, error)
}

func NewAdminRuleSetsHandler(repo ports.RuleSetRepo, nodes ruleNodeLister, groups *groupsvc.Service, invalidate func(), configDir string, templates ...ports.TemplateRepo) *AdminRuleSetsHandler {
	h := &AdminRuleSetsHandler{repo: repo, nodes: nodes, groups: groups, invalidate: invalidate, configDir: configDir}
	if len(templates) > 0 {
		h.templates = templates[0]
	}
	return h
}

type ruleSetDTO struct {
	Slug                     string                               `json:"slug"`
	Name                     string                               `json:"name"`
	Sort                     int                                  `json:"sort"`
	Enabled                  bool                                 `json:"enabled"`
	DirectSubscriptionDomain bool                                 `json:"direct_subscription_domain"`
	ProxyGroupOrder          []string                             `json:"proxy_group_order"`
	ProxyGroupMembers        map[string][]domain.ProxyGroupMember `json:"proxy_group_members,omitempty"`
	ProxyGroupOptions        map[string]domain.ProxyGroupOptions  `json:"proxy_group_options,omitempty"`
	// MihomoRules is accepted as a migration-only input. Responses leave it
	// empty, so old clients can upgrade without preserving a second rule stream.
	MihomoRules            string                         `json:"mihomo_rules,omitempty"`
	MihomoSubRules         []domain.MihomoSubRule         `json:"mihomo_sub_rules,omitempty"`
	MihomoRematchOutbounds []domain.MihomoRematchOutbound `json:"mihomo_rematch_outbounds,omitempty"`
	Content                string                         `json:"content"`
}

func ruleSetDTOFromDomain(r *domain.RuleSet) ruleSetDTO {
	return ruleSetDTO{
		Slug: r.Slug, Name: r.Name, Sort: r.Sort,
		Enabled:                  r.Enabled,
		DirectSubscriptionDomain: r.DirectSubscriptionDomain,
		ProxyGroupOrder:          r.ProxyGroupOrder,
		ProxyGroupMembers:        r.ProxyGroupMembers,
		ProxyGroupOptions:        r.ProxyGroupOptions,
		MihomoSubRules:           r.MihomoSubRules,
		MihomoRematchOutbounds:   r.MihomoRematchOutbounds,
		Content:                  r.Content,
	}
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
		out[i] = ruleSetDTOFromDomain(r)
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
	c.JSON(http.StatusOK, ruleSetDTOFromDomain(r))
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
	nodes, err := h.nodes.List(c.Request.Context())
	if err != nil {
		respondError(c, err)
		return
	}
	content := mergeLegacyRuleContent(req.MihomoRules, req.Content)
	advanced := render.NormalizeMihomoRuleFeatures(render.MihomoRuleFeatures{SubRules: req.MihomoSubRules, RematchOutbounds: req.MihomoRematchOutbounds})
	metadata := render.NormalizeProxyGroupMetadata(content, req.ProxyGroupOrder, req.ProxyGroupMembers, req.ProxyGroupOptions, advanced)
	inspection := render.InspectProxyGroupsWithMihomo(content, metadata.Members, metadata.Options, nodes, advanced)
	for _, issue := range inspection.Issues {
		if issue.Level == "error" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid proxy group members", "issues": inspection.Issues})
			return
		}
	}
	saved := &domain.RuleSet{
		Slug: req.Slug, Name: req.Name, Sort: req.Sort,
		Enabled:                  req.Enabled,
		DirectSubscriptionDomain: req.DirectSubscriptionDomain,
		ProxyGroupOrder:          metadata.Order,
		ProxyGroupMembers:        metadata.Members,
		ProxyGroupOptions:        render.NormalizeProxyGroupOptionsMap(metadata.Options),
		MihomoSubRules:           advanced.SubRules,
		MihomoRematchOutbounds:   advanced.RematchOutbounds,
		Content:                  content,
	}
	if issues, err := h.validateTemplateBindings(c.Request.Context(), saved); err != nil {
		respondError(c, err)
		return
	} else if hasRuleSetErrors(issues) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid Mihomo template binding", "issues": issues})
		return
	}
	if err := h.repo.Save(c.Request.Context(), saved); err != nil {
		if errors.Is(err, domain.ErrValidation) {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		respondError(c, err)
		return
	}
	if h.invalidate != nil {
		h.invalidate()
	}
	c.JSON(http.StatusOK, ruleSetDTOFromDomain(saved))
}

func (h *AdminRuleSetsHandler) validateTemplateBindings(ctx context.Context, candidate *domain.RuleSet) ([]render.ProxyGroupIssue, error) {
	if h.templates == nil || h.repo == nil {
		return nil, nil
	}
	templates, err := h.templates.List(ctx)
	if err != nil {
		return nil, err
	}
	for _, template := range templates {
		if template.ClientType != domain.ClientMihomo || !containsString(template.RuleSets, candidate.Slug) {
			continue
		}
		bound := make([]*domain.RuleSet, 0, len(template.RuleSets))
		for _, slug := range template.RuleSets {
			if slug == candidate.Slug {
				bound = append(bound, candidate)
				continue
			}
			ruleSet, err := h.repo.GetBySlug(ctx, slug)
			if err != nil {
				if errors.Is(err, domain.ErrNotFound) {
					continue
				}
				return nil, err
			}
			bound = append(bound, ruleSet)
		}
		if issues := render.ValidateMihomoTemplateBundle(bound, template.Content); hasRuleSetErrors(issues) {
			return issues, nil
		}
	}
	return nil, nil
}

func hasRuleSetErrors(issues []render.ProxyGroupIssue) bool {
	for _, issue := range issues {
		if issue.Level == "error" {
			return true
		}
	}
	return false
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

type inspectProxyGroupsRequest struct {
	Content                string                               `json:"content"`
	ProxyGroupMembers      map[string][]domain.ProxyGroupMember `json:"proxy_group_members"`
	ProxyGroupOptions      map[string]domain.ProxyGroupOptions  `json:"proxy_group_options"`
	MihomoRules            string                               `json:"mihomo_rules,omitempty"`
	MihomoSubRules         []domain.MihomoSubRule               `json:"mihomo_sub_rules,omitempty"`
	MihomoRematchOutbounds []domain.MihomoRematchOutbound       `json:"mihomo_rematch_outbounds,omitempty"`
	PreviewGroupID         int64                                `json:"preview_group_id,omitempty"`
}

// InspectProxyGroups is the draft-time compiler used by the rule-set editor.
// It never persists data and may therefore be used while the YAML rule body is
// still unsaved.
func (h *AdminRuleSetsHandler) InspectProxyGroups(c *gin.Context) {
	var req inspectProxyGroupsRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	nodes, err := h.nodes.List(c.Request.Context())
	if err != nil {
		respondError(c, err)
		return
	}
	preview := nodes
	if req.PreviewGroupID > 0 {
		g, err := h.groups.Get(c.Request.Context(), req.PreviewGroupID)
		if err != nil {
			respondError(c, err)
			return
		}
		preview, err = h.groups.NodesFor(c.Request.Context(), g)
		if err != nil {
			respondError(c, err)
			return
		}
	}
	content := mergeLegacyRuleContent(req.MihomoRules, req.Content)
	advanced := render.MihomoRuleFeatures{SubRules: req.MihomoSubRules, RematchOutbounds: req.MihomoRematchOutbounds}
	c.JSON(http.StatusOK, render.InspectProxyGroupsWithMihomo(content, req.ProxyGroupMembers, req.ProxyGroupOptions, nodes, advanced, preview))
}

func mergeLegacyRuleContent(legacy, content string) string {
	legacy = strings.TrimRight(legacy, "\r\n")
	if legacy == "" {
		return content
	}
	content = strings.TrimLeft(content, "\r\n")
	if content == "" {
		return legacy
	}
	return legacy + "\n" + content
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
	if h.invalidate != nil {
		h.invalidate()
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
	if h.invalidate != nil {
		h.invalidate()
	}
	c.Status(http.StatusNoContent)
}
