package handler

import (
	"context"
	"errors"
	"net/http"
	"slices"

	"github.com/gin-gonic/gin"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/service/destpolicy"
)

// ---- policies ----

type destPolicyView struct {
	ID          int64                   `json:"id"`
	Name        string                  `json:"name"`
	Description string                  `json:"description"`
	Action      domain.DestAction       `json:"action"`
	State       domain.DestState        `json:"state"`
	ListIDs     []int64                 `json:"list_ids"`
	Inline      domain.DestPolicyInline `json:"inline"`
	Scope       domain.DestScope        `json:"scope"`
	GroupIDs    []int64                 `json:"group_ids"`
	PanelIDs    []int64                 `json:"panel_ids"`
	Priority    int                     `json:"priority"`
	TemplateKey string                  `json:"template_key"`
	// CountsAsRisk: the policy's enforced blocks feed the dest_block risk signal.
	CountsAsRisk bool  `json:"counts_as_risk"`
	CreatedAt    int64 `json:"created_at"`
	UpdatedAt    int64 `json:"updated_at"`
	// ListStates says what each list the policy uses is right now: ready,
	// not_ready or empty. How the compile treated the policy is Note.
	ListStates []destListState        `json:"list_states"`
	Note       *destpolicy.PolicyNote `json:"note"`
	// ScopeMissing: a group or a panel the policy is scoped to is gone.
	ScopeMissing bool `json:"scope_missing"`
	// HitsRecent is the policy's BLOCK hits in the hit window, LastHitAt its
	// latest hit of any kind, and WouldBlockRecent (report-state policies only)
	// the connections it would have blocked: its report-only hits. HitsRecent and
	// WouldBlockRecent are null while no node is recording.
	HitsRecent       *int64 `json:"hits_recent"`
	WouldBlockRecent *int64 `json:"would_block_recent"`
	LastHitAt        *int64 `json:"last_hit_at"`
}

type destListState struct {
	ID    int64  `json:"id"`
	State string `json:"state"`
}

func nonNil(in []int64) []int64 {
	if in == nil {
		return []int64{}
	}
	return in
}

func (h *AdminDestHandler) policyView(ctx context.Context, p *domain.DestPolicy, notes map[int64]*destpolicy.PolicyNote) destPolicyView {
	v := destPolicyView{
		ID: p.ID, Name: p.Name, Description: p.Description, Action: p.Action, State: p.State, ListIDs: nonNil(p.ListIDs),
		Inline: p.Inline, Scope: p.Scope, GroupIDs: nonNil(p.GroupIDs), PanelIDs: nonNil(p.PanelIDs), Priority: p.Priority,
		TemplateKey: p.TemplateKey, CountsAsRisk: p.CountsAsRisk, CreatedAt: msOf(p.CreatedAt), UpdatedAt: msOf(p.UpdatedAt),
		ListStates: []destListState{}, Note: notes[p.ID],
	}
	for _, id := range p.ListIDs {
		st := "ready"
		if l, err := h.w.Lists.GetList(ctx, id); err != nil {
			st = "not_ready"
		} else if !l.Ready() {
			st = "not_ready"
		} else if l.EntryCount == 0 {
			st = "empty"
		}
		v.ListStates = append(v.ListStates, destListState{ID: id, State: st})
	}
	switch p.Scope {
	case domain.DestScopeGroups:
		for _, id := range p.GroupIDs {
			if h.w.Groups != nil {
				if _, err := h.w.Groups.GetByID(ctx, id); errors.Is(err, domain.ErrNotFound) {
					v.ScopeMissing = true
				}
			}
		}
	case domain.DestScopePanels:
		for _, id := range p.PanelIDs {
			if h.w.Panels != nil {
				if _, err := h.w.Panels.GetByID(ctx, id); errors.Is(err, domain.ErrNotFound) {
					v.ScopeMissing = true
				}
			}
		}
	}
	return v
}

// ListPolicies answers GET /api/admin/dest/policies: the allow and block
// policies in evaluation order, with the budget. A report-only policy is in
// block with state "report"; observe is always empty (kept for the shape).
func (h *AdminDestHandler) ListPolicies(c *gin.Context) {
	if !h.ready(c, h.w.Admin != nil && h.w.Policies != nil && h.w.Lists != nil && h.w.Policy != nil) {
		return
	}
	ctx := c.Request.Context()
	policies, err := h.w.Policies.List(ctx)
	if err != nil {
		respondError(c, err)
		return
	}
	budget, err := h.w.Admin.Budget(ctx, nil)
	if err != nil {
		respondError(c, err)
		return
	}
	var notes map[int64]*destpolicy.PolicyNote
	if info, err := h.w.Policy.Info(ctx); err == nil {
		notes = info.Policies
	}
	allow, block := []destPolicyView{}, []destPolicyView{}
	slices.SortStableFunc(policies, func(a, b *domain.DestPolicy) int { return a.Priority - b.Priority })
	for _, p := range policies {
		v := h.policyView(ctx, p, notes)
		if p.Action == domain.DestActionAllow {
			allow = append(allow, v)
		} else {
			block = append(block, v)
		}
	}
	hitDays := h.policyHits(ctx, block)
	h.policyHits(ctx, allow)
	exemptions := 0
	if h.w.Exemptions != nil {
		if list, err := h.w.Exemptions.List(ctx); err == nil {
			exemptions = len(list)
		}
	}
	c.JSON(http.StatusOK, gin.H{
		"allow": allow, "block": block, "observe": []destPolicyView{}, "exemptions": gin.H{"count": exemptions},
		"allowlist_groups": []any{}, "hit_window_days": hitDays, "budget": budget,
	})
}

type destPolicyRequest struct {
	Name        string                  `json:"name"`
	Description string                  `json:"description"`
	Action      domain.DestAction       `json:"action"`
	State       domain.DestState        `json:"state"`
	ListIDs     []int64                 `json:"list_ids"`
	Inline      domain.DestPolicyInline `json:"inline"`
	Scope       domain.DestScope        `json:"scope"`
	GroupIDs    []int64                 `json:"group_ids"`
	PanelIDs    []int64                 `json:"panel_ids"`
	TemplateKey string                  `json:"template_key"`
	// CountsAsRisk is a pointer so that omitting it on a PUT keeps the stored
	// value (see destpolicy.PolicyInput).
	CountsAsRisk *bool `json:"counts_as_risk"`
	// ID and UpdatedAt are for a preview of an edit and for PUT.
	ID        int64  `json:"id"`
	UpdatedAt *int64 `json:"updated_at"`
}

func (r destPolicyRequest) input() destpolicy.PolicyInput {
	scope := r.Scope
	if scope == "" {
		scope = domain.DestScopeAll
	}
	return destpolicy.PolicyInput{
		Name: r.Name, Description: r.Description, Action: r.Action, State: r.State, ListIDs: r.ListIDs, Inline: r.Inline,
		Scope: scope, GroupIDs: r.GroupIDs, PanelIDs: r.PanelIDs, TemplateKey: r.TemplateKey,
		CountsAsRisk: r.CountsAsRisk,
	}
}

var destPolicyCodes = errCodes{stale: "dest_policy_stale", exists: "dest_name_taken"}

func (h *AdminDestHandler) policyReady(c *gin.Context) bool {
	return h.ready(c, h.w.Admin != nil && h.w.Policies != nil && h.w.Lists != nil)
}

// PreviewPolicy answers POST /api/admin/dest/policies/preview: the budget as it
// would be after saving. NOTHING IS WRITTEN and it is not audited. It judges
// the input like the save would (a refused policy is the same 400) but does not
// refuse an over-limit result: the page shows the figure.
func (h *AdminDestHandler) PreviewPolicy(c *gin.Context) {
	if !h.policyReady(c) {
		return
	}
	var req destPolicyRequest
	if !bindDestJSON(c, &req) {
		return
	}
	ctx := c.Request.Context()
	var existing *domain.DestPolicy
	if req.ID != 0 {
		p, err := h.w.Policies.GetByID(ctx, req.ID)
		if err != nil {
			respondError(c, err)
			return
		}
		existing = p
	}
	p, err := h.w.Admin.PreparePolicy(ctx, req.input(), existing)
	if err != nil {
		h.destError(c, err, destPolicyCodes)
		return
	}
	budget, err := h.w.Admin.Budget(ctx, p)
	if err != nil {
		respondError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"budget": budget, "policy": h.policyView(ctx, p, nil)})
}

// CreatePolicy answers POST /api/admin/dest/policies. The priority is the
// server's: the end of the policy's action.
func (h *AdminDestHandler) CreatePolicy(c *gin.Context) {
	if !h.policyReady(c) {
		return
	}
	var req destPolicyRequest
	if !bindDestJSON(c, &req) {
		return
	}
	ctx := c.Request.Context()
	p, err := h.w.Admin.PreparePolicy(ctx, req.input(), nil)
	if err == nil {
		err = h.w.Admin.CheckBudget(ctx, p)
	}
	if err == nil {
		err = h.w.Policies.Create(ctx, p)
	}
	if err != nil {
		h.destError(c, err, destPolicyCodes)
		return
	}
	c.JSON(http.StatusCreated, gin.H{"policy": h.policyView(ctx, p, nil)})
}

// UpdatePolicy answers PUT /api/admin/dest/policies/:id with the whole policy
// and the updated_at it was read at (409 dest_policy_stale when it changed). The
// priority stays unless the action changed, which moves the policy to the end of
// its new action; reordering is PUT /policies/order.
func (h *AdminDestHandler) UpdatePolicy(c *gin.Context) {
	if !h.policyReady(c) {
		return
	}
	id, ok := pathID(c, "id")
	if !ok {
		return
	}
	var req destPolicyRequest
	if !bindDestJSON(c, &req) {
		return
	}
	ctx := c.Request.Context()
	existing, err := h.w.Policies.GetByID(ctx, id)
	if err != nil {
		respondError(c, err)
		return
	}
	p, err := h.w.Admin.PreparePolicy(ctx, req.input(), existing)
	if err == nil {
		err = h.w.Admin.CheckBudget(ctx, p)
	}
	if err == nil {
		err = h.w.Policies.Update(ctx, p, timeFromMS(req.UpdatedAt))
	}
	if err != nil {
		h.destError(c, err, destPolicyCodes)
		return
	}
	c.JSON(http.StatusOK, gin.H{"policy": h.policyView(ctx, p, nil)})
}

// DeletePolicy answers DELETE /api/admin/dest/policies/:id.
func (h *AdminDestHandler) DeletePolicy(c *gin.Context) {
	if !h.policyReady(c) {
		return
	}
	id, ok := pathID(c, "id")
	if !ok {
		return
	}
	if err := h.w.Policies.Delete(c.Request.Context(), id); err != nil {
		h.destError(c, err, destPolicyCodes)
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

type destOrderRequest struct {
	Action domain.DestAction `json:"action"`
	IDs    []int64           `json:"ids"`
}

// ReorderPolicies answers PUT /api/admin/dest/policies/order: the new order of
// one action's policies. The ids must be exactly the action's current policies,
// or it is 409 dest_policy_order_stale (somebody added or removed one).
func (h *AdminDestHandler) ReorderPolicies(c *gin.Context) {
	if !h.policyReady(c) {
		return
	}
	var req destOrderRequest
	if !bindDestJSON(c, &req) {
		return
	}
	if !req.Action.Valid() {
		c.JSON(http.StatusBadRequest, gin.H{"error": "action is block or allow", "code": destpolicy.CodePolicyInvalid, "field": "action"})
		return
	}
	if err := h.w.Policies.Reorder(c.Request.Context(), req.Action, req.IDs); err != nil {
		if errors.Is(err, domain.ErrValidation) {
			// The store refuses a list that is not exactly the action's current
			// policies: the page's copy is stale.
			c.JSON(http.StatusConflict, gin.H{"error": "the policies changed since the order was read", "code": "dest_policy_order_stale"})
			return
		}
		h.destError(c, err, errCodes{})
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true})
}
