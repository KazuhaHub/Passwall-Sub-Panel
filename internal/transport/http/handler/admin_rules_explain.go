package handler

import (
	"context"
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/pkg/rulematch"
	"github.com/KazuhaHub/passwall-sub-panel/internal/service/render"
	"github.com/KazuhaHub/passwall-sub-panel/internal/transport/http/middleware"
)

type routeExplainer interface {
	ExplainRoute(context.Context, render.ExplainRequest) (*render.ExplainResponse, error)
}

// AdminRuleExplainHandler answers "where does this connection go in the
// subscription a real client would get?" It computes from a draft and writes
// nothing, so its route is registered as a read-only preview.
type AdminRuleExplainHandler struct{ renderer routeExplainer }

func NewAdminRuleExplainHandler(renderer routeExplainer) *AdminRuleExplainHandler {
	return &AdminRuleExplainHandler{renderer: renderer}
}

type explainProbeDTO struct {
	Host    string `json:"host"`
	IP      string `json:"ip"`
	Port    int    `json:"port"`
	Network string `json:"network"`
	Process string `json:"process"`
	SrcIP   string `json:"src_ip"`
	SrcPort int    `json:"src_port"`
}

// Explain handles POST /api/admin/rules/explain.
func (h *AdminRuleExplainHandler) Explain(c *gin.Context) {
	claims := middleware.ClaimsFrom(c)
	if claims == nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "No auth"})
		return
	}
	if claims.Role != domain.RoleAdmin {
		c.JSON(http.StatusForbidden, gin.H{"error": "Forbidden"})
		return
	}
	var req struct {
		TemplateSlug string          `json:"template_slug"`
		Draft        *ruleSetDTO     `json:"draft"`
		UserID       int64           `json:"user_id"`
		GroupID      int64           `json:"group_id"`
		Probe        explainProbeDTO `json:"probe"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if req.UserID < 0 || req.GroupID < 0 || h.renderer == nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid request"})
		return
	}
	in := render.ExplainRequest{
		TemplateSlug: req.TemplateSlug, UserID: req.UserID, GroupID: req.GroupID,
		Probe: rulematch.ProbeInput(req.Probe),
	}
	if req.Draft != nil {
		// Normalised exactly as the group preview does, so the draft is what a
		// save would store.
		content := mergeLegacyRuleContent(req.Draft.MihomoRules, req.Draft.Content)
		advanced := render.NormalizeMihomoRuleFeatures(render.MihomoRuleFeatures{SubRules: req.Draft.MihomoSubRules, RematchOutbounds: req.Draft.MihomoRematchOutbounds})
		metadata := render.NormalizeProxyGroupMetadata(content, req.Draft.ProxyGroupOrder, req.Draft.ProxyGroupMembers, req.Draft.ProxyGroupOptions, advanced)
		in.Draft = &domain.RuleSet{Slug: req.Draft.Slug, Name: req.Draft.Name, Sort: req.Draft.Sort, Enabled: req.Draft.Enabled,
			Content: content, DirectSubscriptionDomain: req.Draft.DirectSubscriptionDomain,
			ProxyGroupOrder: metadata.Order, ProxyGroupMembers: metadata.Members,
			ProxyGroupOptions: render.NormalizeProxyGroupOptionsMap(metadata.Options),
			MihomoSubRules:    advanced.SubRules, MihomoRematchOutbounds: advanced.RematchOutbounds}
	}
	result, err := h.renderer.ExplainRoute(c.Request.Context(), in)
	if err != nil {
		if errors.Is(err, render.ErrExplainUnsupportedClient) {
			c.JSON(http.StatusUnprocessableEntity, gin.H{"error": err.Error(), "code": "explain_unsupported_client"})
			return
		}
		respondError(c, err)
		return
	}
	c.JSON(http.StatusOK, result)
}
