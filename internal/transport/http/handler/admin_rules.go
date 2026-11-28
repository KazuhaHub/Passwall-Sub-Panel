package handler

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"sync"

	"github.com/gin-gonic/gin"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
	"github.com/KazuhaHub/passwall-sub-panel/internal/seed"
	"github.com/KazuhaHub/passwall-sub-panel/internal/service/render"
	"github.com/KazuhaHub/passwall-sub-panel/internal/transport/http/middleware"
)

// AdminRuleSetsHandler exposes CRUD for rule sets under /api/admin/rules.
type AdminRuleSetsHandler struct {
	repo            ports.RuleSetRepo
	templates       ports.TemplateRepo
	nodes           ruleNodeLister
	invalidate      func()
	configDir       string
	previewRenderer interface {
		PreviewMihomoGroup(context.Context, int64, *domain.RuleSet) (*render.GroupSubscriptionPreview, error)
	}
	previewUsers ports.UserRepo
	// summaries memoizes what the list derives from a rule set's body (line
	// count, targets), keyed by slug and valid for one Revision (a content hash
	// of the stored file). The seeded default is ~474 KB, and parsing it on every
	// list request is exactly the cost the summary exists to avoid.
	summaryMu sync.Mutex
	summaries map[string]cachedRuleSetDerived
}

func (h *AdminRuleSetsHandler) WithGroupPreview(renderer *render.Service, users ports.UserRepo) *AdminRuleSetsHandler {
	h.previewRenderer, h.previewUsers = renderer, users
	return h
}

type ruleNodeLister interface {
	List(ctx context.Context) ([]*domain.Node, error)
}

func NewAdminRuleSetsHandler(repo ports.RuleSetRepo, nodes ruleNodeLister, invalidate func(), configDir string, templates ...ports.TemplateRepo) *AdminRuleSetsHandler {
	h := &AdminRuleSetsHandler{repo: repo, nodes: nodes, invalidate: invalidate, configDir: configDir}
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
	// Revision is read-only (responses only; ignored on input): a content hash
	// of the stored file. BaseRevision is the optimistic-concurrency token a
	// PUT may carry: the Revision the editor last loaded. Empty/absent keeps
	// the legacy unconditional overwrite.
	Revision     string `json:"revision,omitempty"`
	BaseRevision string `json:"base_revision,omitempty"`
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
		Revision:                 r.Revision,
	}
}

// ruleSetSummaryDTO is one row of the list: the fields a list or a picker reads,
// without the bodies. Content, the proxy-group member/option maps and the
// sub-rule and rematch bodies are left out (each can be hundreds of KB); the
// editor reads them with GET /rules/:slug. LineCount, SizeBytes and Targets are
// derived from Content on the server so the browser never needs it:
//   - LineCount is the non-blank lines of Content (what the list's Lines column shows);
//   - SizeBytes is len(Content);
//   - Targets is render.RuleTargets(Content): the ordered proxy-group names the rules send traffic to.
//
// Revision is the optimistic-concurrency token a bulk write compares against.
type ruleSetSummaryDTO struct {
	Slug                     string   `json:"slug"`
	Name                     string   `json:"name"`
	Sort                     int      `json:"sort"`
	Enabled                  bool     `json:"enabled"`
	DirectSubscriptionDomain bool     `json:"direct_subscription_domain"`
	ProxyGroupOrder          []string `json:"proxy_group_order"`
	Revision                 string   `json:"revision,omitempty"`
	LineCount                int      `json:"line_count"`
	SizeBytes                int      `json:"size_bytes"`
	Targets                  []string `json:"targets"`
}

type cachedRuleSetDerived struct {
	revision  string
	lineCount int
	targets   []string
}

func (h *AdminRuleSetsHandler) summaryOf(r *domain.RuleSet) ruleSetSummaryDTO {
	d := h.derivedOf(r)
	return ruleSetSummaryDTO{
		Slug: r.Slug, Name: r.Name, Sort: r.Sort, Enabled: r.Enabled,
		DirectSubscriptionDomain: r.DirectSubscriptionDomain,
		ProxyGroupOrder:          r.ProxyGroupOrder,
		Revision:                 r.Revision,
		LineCount:                d.lineCount,
		SizeBytes:                len(r.Content),
		Targets:                  d.targets,
	}
}

// derivedOf reads the body once per revision. A rule set with no revision (a
// repo that does not hash) is derived every time rather than cached under an
// empty key that would never invalidate.
func (h *AdminRuleSetsHandler) derivedOf(r *domain.RuleSet) cachedRuleSetDerived {
	if r.Revision != "" {
		h.summaryMu.Lock()
		hit, ok := h.summaries[r.Slug]
		h.summaryMu.Unlock()
		if ok && hit.revision == r.Revision {
			return hit
		}
	}
	d := cachedRuleSetDerived{revision: r.Revision, targets: render.RuleTargets(r.Content)}
	for _, line := range strings.Split(r.Content, "\n") {
		if strings.TrimSpace(line) != "" {
			d.lineCount++
		}
	}
	if r.Revision != "" {
		h.summaryMu.Lock()
		if h.summaries == nil {
			h.summaries = map[string]cachedRuleSetDerived{}
		}
		h.summaries[r.Slug] = d
		h.summaryMu.Unlock()
	}
	return d
}

func (h *AdminRuleSetsHandler) forgetSummary(slug string) {
	h.summaryMu.Lock()
	delete(h.summaries, slug)
	h.summaryMu.Unlock()
}

// List returns a summary per rule set, never the bodies (see ruleSetSummaryDTO).
// An external script that read `content` from this list now gets none of it and
// must call GET /api/admin/rules/:slug per rule set.
func (h *AdminRuleSetsHandler) List(c *gin.Context) {
	p := parsePagination(c)
	items, total, err := h.repo.ListPaged(c.Request.Context(), p)
	if err != nil {
		respondError(c, err)
		return
	}
	out := make([]ruleSetSummaryDTO, len(items))
	for i, r := range items {
		out[i] = h.summaryOf(r)
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
		// The cap lives in middleware; a body past it must read as 413, not as
		// a malformed-JSON 400, so the editor can say "too large".
		if middleware.AbortBodyTooLarge(c, err) {
			return
		}
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
	nodes = withoutTunnelListeners(nodes)
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
	var saveErr error
	if req.BaseRevision != "" {
		saveErr = h.repo.SaveIfRevision(c.Request.Context(), saved, req.BaseRevision)
	} else {
		saveErr = h.repo.Save(c.Request.Context(), saved)
	}
	if err := saveErr; err != nil {
		if errors.Is(err, domain.ErrStaleRevision) {
			body := gin.H{"error": "Rule set changed since it was loaded", "code": "ruleset_stale"}
			// The current document lets the editor offer a reload/merge; if it
			// vanished between the compare and this read, omit it.
			if cur, gerr := h.repo.GetBySlug(c.Request.Context(), req.Slug); gerr == nil {
				body["current"] = ruleSetDTOFromDomain(cur)
			}
			c.JSON(http.StatusConflict, body)
			return
		}
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
		if issues := render.ValidateMihomoTemplateBundle(bound); hasRuleSetErrors(issues) {
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
}

// InspectProxyGroups is the draft-time compiler used by the rule-set editor.
// It never persists data and may therefore be used while the YAML rule body is
// still unsaved.
func (h *AdminRuleSetsHandler) InspectProxyGroups(c *gin.Context) {
	var req inspectProxyGroupsRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		// The cap lives in middleware; a body past it must read as 413, not as
		// a malformed-JSON 400, so the editor can say "too large".
		if middleware.AbortBodyTooLarge(c, err) {
			return
		}
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	nodes, err := h.nodes.List(c.Request.Context())
	if err != nil {
		respondError(c, err)
		return
	}
	nodes = withoutTunnelListeners(nodes)
	content := mergeLegacyRuleContent(req.MihomoRules, req.Content)
	advanced := render.MihomoRuleFeatures{SubRules: req.MihomoSubRules, RematchOutbounds: req.MihomoRematchOutbounds}
	c.JSON(http.StatusOK, render.InspectProxyGroupsWithMihomo(content, req.ProxyGroupMembers, req.ProxyGroupOptions, nodes, advanced))
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
	h.forgetSummary(slug)
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

// withoutTunnelListeners leaves out the hidden listeners of tunnel lines: they
// are lines of their landing (which the inspector lists), never nodes of their
// own, so a proxy group can neither name nor match one.
func withoutTunnelListeners(nodes []*domain.Node) []*domain.Node {
	out := make([]*domain.Node, 0, len(nodes))
	for _, n := range nodes {
		if !n.IsTunnelChild() {
			out = append(out, n)
		}
	}
	return out
}
