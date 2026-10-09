package handler

import (
	"encoding/json"
	"errors"
	"io"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/service/destlist"
	"github.com/KazuhaHub/passwall-sub-panel/internal/service/destpolicy"
	"github.com/gin-gonic/gin"
)

type AdminDestinationPoliciesHandler struct {
	admin *destpolicy.Administrator
	lists *destlist.Service
}

func NewAdminDestinationPoliciesHandler(admin *destpolicy.Administrator, lists *destlist.Service) *AdminDestinationPoliciesHandler {
	return &AdminDestinationPoliciesHandler{admin: admin, lists: lists}
}
func (h *AdminDestinationPoliciesHandler) available(c *gin.Context) bool {
	if h.admin == nil {
		c.JSON(503, gin.H{"error": "destination policies unavailable"})
		return false
	}
	return true
}
func destinationPolicyError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, domain.ErrConflict) && strings.Contains(err.Error(), "dest_exception_conflict"):
		c.JSON(409, gin.H{"error": "dest_policy_invalid", "field": "exception_bundle"})
	case errors.Is(err, domain.ErrAlreadyExists):
		c.JSON(409, gin.H{"error": "dest_name_taken"})
	case errors.Is(err, domain.ErrConflict) && strings.Contains(err.Error(), "dest_policy_stale"):
		c.JSON(409, gin.H{"error": "dest_policy_stale"})
	case errors.Is(err, domain.ErrConflict) && strings.Contains(err.Error(), "dest_policy_order_stale"):
		c.JSON(409, gin.H{"error": "dest_policy_order_stale"})
	case errors.Is(err, domain.ErrValidation) && strings.Contains(err.Error(), "dest_policy_no_match"):
		c.JSON(400, gin.H{"error": "dest_policy_no_match"})
	default:
		destinationListError(c, err)
	}
}
func destinationPolicyDecode(c *gin.Context, target any) bool {
	decoder := json.NewDecoder(c.Request.Body)
	decoder.DisallowUnknownFields()
	var trailing any
	if err := decoder.Decode(target); err != nil {
		c.JSON(400, gin.H{"error": "dest_policy_invalid", "field": "body"})
		return false
	}
	if err := decoder.Decode(&trailing); err != io.EOF {
		c.JSON(400, gin.H{"error": "dest_policy_invalid", "field": "body"})
		return false
	}
	return true
}

type destinationPolicyInput struct {
	ID           *int64                `json:"id"`
	Name         string                `json:"name"`
	Action       domain.DestAction     `json:"action"`
	ListIDs      []int64               `json:"list_ids"`
	Inline       *domain.DestInline    `json:"inline"`
	Scope        domain.DestScope      `json:"scope"`
	GroupIDs     []int64               `json:"group_ids"`
	Enabled      *bool                 `json:"enabled"`
	CountsAsRisk *bool                 `json:"counts_as_risk"`
	TemplateKey  string                `json:"template_key"`
	UpdatedAt    *int64                `json:"updated_at"`
	NewList      *destinationListInput `json:"new_list"`
}

func decodeDestinationPolicy(c *gin.Context, update, preview bool) (domain.DestPolicy, time.Time, *destinationListInput, bool) {
	var req destinationPolicyInput
	if !destinationPolicyDecode(c, &req) {
		return domain.DestPolicy{}, time.Time{}, nil, false
	}
	field := ""
	if req.Enabled == nil {
		field = "enabled"
	} else if req.CountsAsRisk == nil {
		field = "counts_as_risk"
	} else if req.Inline == nil {
		field = "inline"
	}
	if req.ID != nil && (!preview || *req.ID <= 0) {
		field = "id"
	}
	if update && (req.UpdatedAt == nil || *req.UpdatedAt <= 0) || req.UpdatedAt != nil && (*req.UpdatedAt <= 0 || !update && (!preview || req.ID == nil)) {
		field = "updated_at"
	}
	if list := req.NewList; list != nil {
		if update || req.ID != nil || list.Kind != domain.DestListGeosite || strings.TrimSpace(list.Name) == "" || utf8.RuneCountInString(list.Name) > 128 || list.Category == "" || len(list.Category) > 128 || len(list.Attrs) > 128 || list.SourceURL != "" || list.Text != "" || list.UpdatedAt != nil {
			field = "new_list"
		}
	}
	if field != "" {
		c.JSON(400, gin.H{"error": "dest_policy_invalid", "field": field})
		return domain.DestPolicy{}, time.Time{}, nil, false
	}
	policy := domain.DestPolicy{Name: req.Name, Action: req.Action, ListIDs: req.ListIDs, Inline: *req.Inline, Scope: req.Scope, GroupIDs: req.GroupIDs, Enabled: *req.Enabled, CountsAsRisk: *req.CountsAsRisk, TemplateKey: req.TemplateKey}
	if req.ID != nil {
		policy.ID = *req.ID
	}
	expected := time.Time{}
	if req.UpdatedAt != nil {
		expected = time.UnixMilli(*req.UpdatedAt).UTC()
	}
	return policy, expected, req.NewList, true
}

func (h *AdminDestinationPoliciesHandler) cachedTemplateList(c *gin.Context, input *destinationListInput) (domain.DestList, destlist.FetchResult, error) {
	if h.lists == nil {
		return domain.DestList{}, destlist.FetchResult{}, domain.ErrUnavailable
	}
	list := domain.DestList{Name: strings.TrimSpace(input.Name), Kind: domain.DestListGeosite, GeositeCategory: input.Category, GeositeAttrs: input.Attrs}
	result, err := h.lists.Preview(c.Request.Context(), list)
	if err != nil {
		return domain.DestList{}, result, err
	}
	now := time.Now().UTC()
	list.Entries, list.EntryCount, list.RegexpCount = result.Parsed.Entries, result.Parsed.EntryCount, result.Parsed.RegexpCount
	list.ContentSHA256, list.ParseReport, list.LastFetchedAt = result.Parsed.ContentSHA256, &result.Parsed.Report, &now
	return list, result, nil
}
func destinationPolicyID(c *gin.Context) (int64, bool) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		c.JSON(400, gin.H{"error": "dest_policy_invalid", "field": "id"})
		return 0, false
	}
	return id, true
}
func destinationPolicyView(p domain.DestPolicy) gin.H {
	lists, groups := slices.Clone(p.ListIDs), slices.Clone(p.GroupIDs)
	if lists == nil {
		lists = []int64{}
	}
	if groups == nil {
		groups = []int64{}
	}
	return gin.H{"id": p.ID, "name": p.Name, "action": p.Action, "list_ids": lists, "inline": p.Inline, "scope": p.Scope, "group_ids": groups, "priority": p.Priority, "enabled": p.Enabled, "counts_as_risk": p.CountsAsRisk, "template_key": p.TemplateKey, "created_at": p.CreatedAt.UnixMilli(), "updated_at": p.UpdatedAt.UnixMilli(), "hits_recent": nil, "last_hit_at": nil}
}
func (h *AdminDestinationPoliciesHandler) Create(c *gin.Context) {
	if !h.available(c) {
		return
	}
	p, expected, newList, ok := decodeDestinationPolicy(c, false, false)
	if !ok {
		return
	}
	var err error
	if newList != nil {
		var list domain.DestList
		list, _, err = h.cachedTemplateList(c, newList)
		if err == nil {
			err = h.admin.SaveWithList(c.Request.Context(), &p, &list)
		}
	} else {
		err = h.admin.Save(c.Request.Context(), &p, expected)
	}
	if err != nil {
		destinationPolicyError(c, err)
		return
	}
	c.JSON(201, destinationPolicyView(p))
}
func (h *AdminDestinationPoliciesHandler) Put(c *gin.Context) {
	if !h.available(c) {
		return
	}
	id, ok := destinationPolicyID(c)
	if !ok {
		return
	}
	p, expected, _, ok := decodeDestinationPolicy(c, true, false)
	if !ok {
		return
	}
	p.ID = id
	if err := h.admin.Save(c.Request.Context(), &p, expected); err != nil {
		destinationPolicyError(c, err)
		return
	}
	c.JSON(200, destinationPolicyView(p))
}
func (h *AdminDestinationPoliciesHandler) Preview(c *gin.Context) {
	if !h.available(c) {
		return
	}
	p, expected, newList, ok := decodeDestinationPolicy(c, false, true)
	if !ok {
		return
	}
	var budget destpolicy.Budget
	var err error
	response := gin.H{}
	if newList != nil {
		var list domain.DestList
		var result destlist.FetchResult
		list, result, err = h.cachedTemplateList(c, newList)
		if err == nil {
			budget, err = h.admin.PreviewWithList(c.Request.Context(), p, list)
			response["new_list_preview"] = gin.H{"parse_report": result.Parsed.Report, "entries": destinationEntrySamples(result.Parsed.Entries, 50), "content_sha256": result.Parsed.ContentSHA256, "entry_count": result.Parsed.EntryCount, "regexp_count": result.Parsed.RegexpCount, "http_status": result.HTTPStatus, "bytes": result.Bytes}
		}
	} else {
		budget, err = h.admin.Preview(c.Request.Context(), p, expected)
	}
	if err != nil {
		destinationPolicyError(c, err)
		return
	}
	response["budget"] = budget
	c.JSON(200, response)
}
func (h *AdminDestinationPoliciesHandler) Delete(c *gin.Context) {
	if !h.available(c) {
		return
	}
	id, ok := destinationPolicyID(c)
	if !ok {
		return
	}
	if err := h.admin.Delete(c.Request.Context(), id); err != nil {
		destinationPolicyError(c, err)
		return
	}
	c.Status(204)
}
func (h *AdminDestinationPoliciesHandler) Order(c *gin.Context) {
	if !h.available(c) {
		return
	}
	var req struct {
		Action domain.DestAction `json:"action"`
		IDs    []int64           `json:"ids"`
	}
	if !destinationPolicyDecode(c, &req) {
		return
	}
	if req.Action != domain.DestAllow && req.Action != domain.DestBlock && req.Action != domain.DestObserve {
		c.JSON(400, gin.H{"error": "dest_policy_invalid", "field": "action"})
		return
	}
	if err := h.admin.Order(c.Request.Context(), req.Action, req.IDs); err != nil {
		destinationPolicyError(c, err)
		return
	}
	c.Status(204)
}
func (h *AdminDestinationPoliciesHandler) List(c *gin.Context) {
	if !h.available(c) {
		return
	}
	overview, err := h.admin.Read(c.Request.Context())
	if err != nil {
		destinationPolicyError(c, err)
		return
	}
	lists := map[int64]domain.DestList{}
	for _, list := range overview.Definitions.Lists {
		lists[list.ID] = list
	}
	segments := map[domain.DestAction][]gin.H{domain.DestAllow: {}, domain.DestBlock: {}, domain.DestObserve: {}}
	policies := slices.Clone(overview.Definitions.Policies)
	slices.SortFunc(policies, func(a, b domain.DestPolicy) int {
		if a.Priority < b.Priority {
			return -1
		}
		if a.Priority > b.Priority {
			return 1
		}
		if a.ID < b.ID {
			return -1
		}
		if a.ID > b.ID {
			return 1
		}
		return 0
	})
	for _, p := range policies {
		view := destinationPolicyView(p)
		states := []gin.H{}
		for _, id := range p.ListIDs {
			list, exists := lists[id]
			state := "ready"
			if !exists {
				state = "missing"
			} else if list.Kind == domain.DestListCustom && list.EntryCount == 0 {
				state = "empty"
			} else if list.Kind != domain.DestListCustom && list.LastFetchedAt == nil {
				state = "pending"
			}
			if exists && list.LastError != "" {
				state = "failed"
			}
			if h.lists != nil && h.lists.IsRefreshing(id) {
				state = "refreshing"
			}
			// A failed refresh retains previously usable content. Match the compiler's
			// readiness check independently of the operational refresh state.
			available := exists && list.EntryCount > 0 && (list.Kind == domain.DestListCustom || list.LastFetchedAt != nil)
			states = append(states, gin.H{"id": id, "name": list.Name, "state": state, "available": available})
		}
		view["list_states"] = states
		missing := false
		for _, id := range p.GroupIDs {
			if _, exists := overview.Context.GroupNames[id]; !exists {
				missing = true
			}
		}
		view["scope_missing"] = missing
		segments[p.Action] = append(segments[p.Action], view)
	}
	groups := []gin.H{}
	for _, g := range overview.Definitions.Groups {
		if g.Mode != "allowlist" {
			continue
		}
		days := 0
		if g.StageChangedAt != nil {
			days = max(0, int(overview.At.Sub(*g.StageChangedAt)/(24*time.Hour)))
		}
		groups = append(groups, gin.H{"group_id": g.GroupID, "name": overview.Context.GroupNames[g.GroupID], "stage": g.Stage, "stage_days": days})
	}
	c.JSON(200, gin.H{"allow": segments[domain.DestAllow], "block": segments[domain.DestBlock], "observe": segments[domain.DestObserve], "exemptions": gin.H{"count": len(overview.Definitions.Exemptions)}, "allowlist_groups": groups, "hit_window_days": overview.Context.HitWindowDays, "budget": overview.Budget,
		"published_generation": overview.Context.PublishedGeneration, "published_has_access_control": overview.Context.PublishedHasAccessControl})
}
