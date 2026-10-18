package handler

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/service/render"
	"github.com/KazuhaHub/passwall-sub-panel/internal/transport/http/middleware"
)

func (h *AdminRuleSetsHandler) PreviewProxyGroups(c *gin.Context) {
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
		GroupID int64      `json:"preview_group_id"`
		Draft   ruleSetDTO `json:"draft"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if req.GroupID < 0 || h.previewRenderer == nil || h.previewUsers == nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Group preview unavailable"})
		return
	}
	if req.GroupID == 0 {
		admin, err := h.previewUsers.GetByID(c.Request.Context(), claims.UserID)
		if err != nil {
			respondError(c, err)
			return
		}
		req.GroupID = admin.GroupID
	}
	if req.GroupID <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Select a group"})
		return
	}
	// This endpoint is admin-only, like group CRUD; group existence is checked
	// by the renderer. It does not select or impersonate any group member.
	content := mergeLegacyRuleContent(req.Draft.MihomoRules, req.Draft.Content)
	advanced := render.NormalizeMihomoRuleFeatures(render.MihomoRuleFeatures{SubRules: req.Draft.MihomoSubRules, RematchOutbounds: req.Draft.MihomoRematchOutbounds})
	metadata := render.NormalizeProxyGroupMetadata(content, req.Draft.ProxyGroupOrder, req.Draft.ProxyGroupMembers, req.Draft.ProxyGroupOptions, advanced)
	draft := &domain.RuleSet{Slug: req.Draft.Slug, Name: req.Draft.Name, Sort: req.Draft.Sort, Enabled: req.Draft.Enabled,
		Content: content, DirectSubscriptionDomain: req.Draft.DirectSubscriptionDomain,
		ProxyGroupOrder: metadata.Order, ProxyGroupMembers: metadata.Members,
		ProxyGroupOptions: render.NormalizeProxyGroupOptionsMap(metadata.Options),
		MihomoSubRules:    advanced.SubRules, MihomoRematchOutbounds: advanced.RematchOutbounds}
	result, err := h.previewRenderer.PreviewMihomoGroup(c.Request.Context(), req.GroupID, draft)
	if err != nil {
		status := http.StatusInternalServerError
		if errors.Is(err, domain.ErrNotFound) {
			status = http.StatusNotFound
		}
		if errors.Is(err, domain.ErrValidation) {
			status = http.StatusBadRequest
		}
		c.JSON(status, gin.H{"error": err.Error(), "group_id": req.GroupID})
		return
	}
	c.JSON(http.StatusOK, result)
}
