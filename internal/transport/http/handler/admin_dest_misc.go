package handler

import (
	"errors"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/service/destcatalog"
	"github.com/KazuhaHub/passwall-sub-panel/internal/service/destlist"
	"github.com/KazuhaHub/passwall-sub-panel/internal/service/destpolicy"
	"github.com/KazuhaHub/passwall-sub-panel/internal/transport/http/middleware"
)

// ---- geosite ----

// GeositeStatus answers GET /api/admin/dest/geosite/status: the rule-data
// version panel (what is in use and how old, what the upstream offers, what is
// kept for rollback).
func (h *AdminDestHandler) GeositeStatus(c *gin.Context) {
	if !h.ready(c, h.w.Lists != nil) {
		return
	}
	c.JSON(http.StatusOK, h.w.Lists.GeositeStatus(c.Request.Context()))
}

// GeositeCategories answers GET /api/admin/dest/geosite/categories. With no
// geosite data yet it is 503 dest_geosite_unavailable.
func (h *AdminDestHandler) GeositeCategories(c *gin.Context) {
	if !h.ready(c, h.w.Lists != nil) {
		return
	}
	ctx := c.Request.Context()
	cats, err := h.w.Lists.GeositeCategories(ctx)
	if err != nil {
		h.destError(c, err, errCodes{})
		return
	}
	out := gin.H{"categories": cats}
	if cur := h.w.Lists.GeositeStatus(ctx).Current; cur != nil {
		out["updated_at"] = msOf(cur.DownloadedAt)
		out["release"] = cur.Tag
	}
	c.JSON(http.StatusOK, out)
}

type destGeositeRequest struct {
	Tag string `json:"tag"`
}

func dryRun(c *gin.Context) bool {
	switch strings.ToLower(c.Query("dry_run")) {
	case "1", "true", "yes":
		return true
	}
	return false
}

// GeositeUpdate answers POST /api/admin/dest/geosite/update. With ?dry_run=1
// it downloads and verifies a release, changes nothing and returns the
// per-category diff and the verdict (an update that would break a policy in
// force is reported as the rejection it would be). The apply names the same
// tag the dry run reviewed. A refused update is 409
// dest_geosite_update_rejected and the old data stays.
func (h *AdminDestHandler) GeositeUpdate(c *gin.Context) {
	if !h.ready(c, h.w.Lists != nil) {
		return
	}
	var req destGeositeRequest
	if c.Request.ContentLength != 0 {
		if !bindDestJSON(c, &req) {
			return
		}
	}
	res, err := h.w.Lists.GeositeUpdate(c.Request.Context(), destlist.UpdateOptions{Tag: req.Tag, DryRun: dryRun(c)})
	if err != nil {
		h.destError(c, err, errCodes{})
		return
	}
	c.JSON(http.StatusOK, res)
}

// GeositeRollback answers POST /api/admin/dest/geosite/rollback: back to the
// previous release, applied exactly like an update (and ?dry_run=1 previews it).
func (h *AdminDestHandler) GeositeRollback(c *gin.Context) {
	if !h.ready(c, h.w.Lists != nil) {
		return
	}
	res, err := h.w.Lists.GeositeRollback(c.Request.Context(), dryRun(c))
	if err != nil {
		h.destError(c, err, errCodes{})
		return
	}
	c.JSON(http.StatusOK, res)
}

// ---- catalog ----

// Catalog answers GET /api/admin/dest/catalog: the built-in rule groups, with
// the ones already added marked, and the groups that have no generic category
// (listed as unavailable, with why).
func (h *AdminDestHandler) Catalog(c *gin.Context) {
	if !h.ready(c, h.w.Catalog != nil) {
		return
	}
	v, err := h.w.Catalog.View(c.Request.Context())
	if err != nil {
		respondError(c, err)
		return
	}
	c.JSON(http.StatusOK, v)
}

type destInstantiateRequest struct {
	State  domain.DestState `json:"state"`
	Locale string           `json:"locale"`
}

// InstantiateCatalog answers POST /api/admin/dest/catalog/:key/instantiate: the
// lists a rule group needs and a policy over them, created in one transaction in
// the state the admin chose (there is no default; an allow policy cannot be report-only).
func (h *AdminDestHandler) InstantiateCatalog(c *gin.Context) {
	if !h.ready(c, h.w.Catalog != nil && h.w.Lists != nil) {
		return
	}
	var req destInstantiateRequest
	if !bindDestJSON(c, &req) {
		return
	}
	if err := destpolicy.CheckState(req.State); err != nil {
		h.destError(c, err, errCodes{})
		return
	}
	locale := req.Locale
	if locale == "" {
		locale = destcatalog.LocaleZH
	}
	ctx := c.Request.Context()
	p, err := h.w.Catalog.Instantiate(ctx, c.Param("key"), locale, req.State)
	if err != nil {
		h.destError(c, err, errCodes{exists: "dest_name_taken"})
		return
	}
	c.JSON(http.StatusCreated, gin.H{"policy": h.policyView(ctx, p, nil)})
}

// ---- exemptions ----

type destExemptionView struct {
	UserID       int64  `json:"user_id"`
	UPN          string `json:"upn"`
	Reason       string `json:"reason"`
	CreatedBy    int64  `json:"created_by"`
	CreatedByUPN string `json:"created_by_upn"`
	CreatedAt    int64  `json:"created_at"`
	ExpiresAt    *int64 `json:"expires_at"`
	// Expired: past its expiry and waiting for the hourly cleanup. It is
	// returned anyway, and no longer in the published policy.
	Expired bool `json:"expired"`
}

func (h *AdminDestHandler) exemptionView(c *gin.Context, e *domain.DestExemption) destExemptionView {
	v := destExemptionView{
		UserID: e.UserID, Reason: e.Reason, CreatedBy: e.CreatedBy, CreatedAt: msOf(e.CreatedAt), ExpiresAt: msPtr(e.ExpiresAt),
		Expired: e.ExpiresAt != nil && !e.ExpiresAt.After(time.Now()),
	}
	if h.w.Users != nil {
		if u, err := h.w.Users.GetByID(c.Request.Context(), e.UserID); err == nil {
			v.UPN = u.UPN
		}
		if e.CreatedBy != 0 {
			if u, err := h.w.Users.GetByID(c.Request.Context(), e.CreatedBy); err == nil {
				v.CreatedByUPN = u.UPN
			}
		}
	}
	return v
}

func (h *AdminDestHandler) exemptionReady(c *gin.Context) bool {
	return h.ready(c, h.w.Exemptions != nil && h.w.Users != nil)
}

// ListExemptions answers GET /api/admin/dest/exemptions.
func (h *AdminDestHandler) ListExemptions(c *gin.Context) {
	if !h.exemptionReady(c) {
		return
	}
	list, err := h.w.Exemptions.List(c.Request.Context())
	if err != nil {
		respondError(c, err)
		return
	}
	items := make([]destExemptionView, 0, len(list))
	for _, e := range list {
		items = append(items, h.exemptionView(c, e))
	}
	c.JSON(http.StatusOK, gin.H{"items": items})
}

// GetExemption answers GET /api/admin/dest/exemptions/:user_id.
func (h *AdminDestHandler) GetExemption(c *gin.Context) {
	if !h.exemptionReady(c) {
		return
	}
	id, ok := pathID(c, "user_id")
	if !ok {
		return
	}
	e, err := h.w.Exemptions.Get(c.Request.Context(), id)
	if err != nil {
		respondError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"exemption": h.exemptionView(c, e)})
}

type destExemptionRequest struct {
	UserID    int64  `json:"user_id"`
	Reason    string `json:"reason"`
	ExpiresAt *int64 `json:"expires_at"`
}

func (h *AdminDestHandler) saveExemption(c *gin.Context, userID int64, req destExemptionRequest, create bool) {
	ctx := c.Request.Context()
	if _, err := h.w.Users.GetByID(ctx, userID); err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			c.JSON(http.StatusBadRequest, gin.H{"error": "no such user", "code": destpolicy.CodeExemptionInvalid, "field": "user_id"})
			return
		}
		respondError(c, err)
		return
	}
	expires := timeFromMS(req.ExpiresAt)
	if err := destpolicy.ValidateExemption(req.Reason, expires, time.Now()); err != nil {
		h.destError(c, err, errCodes{})
		return
	}
	e := &domain.DestExemption{UserID: userID, Reason: req.Reason, ExpiresAt: expires}
	if prev, err := h.w.Exemptions.Get(ctx, userID); err == nil {
		if create {
			c.JSON(http.StatusConflict, gin.H{"error": "this account is already exempt", "code": destpolicy.CodeExemptionExists})
			return
		}
		e.CreatedBy, e.CreatedAt = prev.CreatedBy, prev.CreatedAt
	} else if !errors.Is(err, domain.ErrNotFound) {
		respondError(c, err)
		return
	} else if !create {
		respondError(c, err)
		return
	}
	if create {
		if claims := middleware.ClaimsFrom(c); claims != nil {
			e.CreatedBy = claims.UserID
		}
	}
	if err := h.w.Exemptions.Upsert(ctx, e); err != nil {
		h.destError(c, err, errCodes{})
		return
	}
	saved, err := h.w.Exemptions.Get(ctx, userID)
	if err != nil {
		respondError(c, err)
		return
	}
	status := http.StatusOK
	if create {
		status = http.StatusCreated
	}
	c.JSON(status, gin.H{"exemption": h.exemptionView(c, saved)})
}

// CreateExemption answers POST /api/admin/dest/exemptions. An exemption lets one
// account through every rule of the policy; a second one for the same account is
// 409 dest_exemption_exists.
func (h *AdminDestHandler) CreateExemption(c *gin.Context) {
	if !h.exemptionReady(c) {
		return
	}
	var req destExemptionRequest
	if !bindDestJSON(c, &req) {
		return
	}
	if req.UserID <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "user_id is required", "code": destpolicy.CodeExemptionInvalid, "field": "user_id"})
		return
	}
	h.saveExemption(c, req.UserID, req, true)
}

// UpdateExemption answers PUT /api/admin/dest/exemptions/:user_id: a new reason
// and expiry for an existing exemption.
func (h *AdminDestHandler) UpdateExemption(c *gin.Context) {
	if !h.exemptionReady(c) {
		return
	}
	id, ok := pathID(c, "user_id")
	if !ok {
		return
	}
	var req destExemptionRequest
	if !bindDestJSON(c, &req) {
		return
	}
	h.saveExemption(c, id, req, false)
}

// DeleteExemption answers DELETE /api/admin/dest/exemptions/:user_id.
func (h *AdminDestHandler) DeleteExemption(c *gin.Context) {
	if !h.exemptionReady(c) {
		return
	}
	id, ok := pathID(c, "user_id")
	if !ok {
		return
	}
	if err := h.w.Exemptions.Delete(c.Request.Context(), id); err != nil {
		respondError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

// ---- source rules ----

type destSourceRuleView struct {
	NodeID    int64            `json:"node_id"`
	Countries []string         `json:"countries"`
	CIDRs     []string         `json:"cidrs"`
	State     domain.DestState `json:"state"`
	UpdatedAt *int64           `json:"updated_at"`
	// Note says why a rule in force compiled to nothing: geoip_unavailable (a
	// country needs a GeoIP database and there is none) or empty.
	Note string `json:"note,omitempty"`
}

func sourceRuleView(r *domain.DestSourceRule, notes map[int64]string) destSourceRuleView {
	v := destSourceRuleView{NodeID: r.NodeID, Countries: r.Countries, CIDRs: r.CIDRs, State: r.State, Note: notes[r.NodeID]}
	if v.Countries == nil {
		v.Countries = []string{}
	}
	if v.CIDRs == nil {
		v.CIDRs = []string{}
	}
	if !r.UpdatedAt.IsZero() {
		ms := msOf(r.UpdatedAt)
		v.UpdatedAt = &ms
	}
	return v
}

func (h *AdminDestHandler) sourceReady(c *gin.Context) bool {
	return h.ready(c, h.w.SourceRules != nil && h.w.Admin != nil && h.w.Nodes != nil)
}

// ListSourceRules answers GET /api/admin/dest/source-rules: every inbound's
// rule, and whether the GeoIP database a country is expanded from exists.
func (h *AdminDestHandler) ListSourceRules(c *gin.Context) {
	if !h.sourceReady(c) {
		return
	}
	ctx := c.Request.Context()
	rules, err := h.w.SourceRules.List(ctx)
	if err != nil {
		respondError(c, err)
		return
	}
	var notes map[int64]string
	if h.w.Policy != nil {
		if info, err := h.w.Policy.Info(ctx); err == nil {
			notes = info.SourceNotes
		}
	}
	items := make([]destSourceRuleView, 0, len(rules))
	for _, r := range rules {
		items = append(items, sourceRuleView(r, notes))
	}
	geoip := false
	if h.w.Lists != nil {
		geoip = h.w.Lists.Countries.ActivePath(ctx) != ""
	}
	c.JSON(http.StatusOK, gin.H{"items": items, "geoip_available": geoip})
}

// GetSourceRule answers GET /api/admin/dest/source-rules/:node_id: the inbound's
// rule, or the empty one (off) when it has none. native says whether the inbound
// is on a native node; only those can have a rule.
func (h *AdminDestHandler) GetSourceRule(c *gin.Context) {
	if !h.sourceReady(c) {
		return
	}
	id, ok := pathID(c, "node_id")
	if !ok {
		return
	}
	ctx := c.Request.Context()
	node, err := h.w.Nodes.GetByID(ctx, id)
	if err != nil {
		respondError(c, err)
		return
	}
	native := false
	if h.w.Panels != nil {
		if p, err := h.w.Panels.GetByID(ctx, node.PanelID); err == nil {
			native = domain.NormalizePanelKind(p.Kind) == domain.PanelKindPSP
		}
	}
	rule, err := h.w.SourceRules.Get(ctx, id)
	if errors.Is(err, domain.ErrNotFound) {
		rule, err = &domain.DestSourceRule{NodeID: id, State: domain.DestStateOff}, nil
	}
	if err != nil {
		respondError(c, err)
		return
	}
	var notes map[int64]string
	if h.w.Policy != nil {
		if info, err := h.w.Policy.Info(ctx); err == nil {
			notes = info.SourceNotes
		}
	}
	geoip := false
	if h.w.Lists != nil {
		geoip = h.w.Lists.Countries.ActivePath(ctx) != ""
	}
	c.JSON(http.StatusOK, gin.H{"rule": sourceRuleView(rule, notes), "native": native, "geoip_available": geoip})
}

type destSourceRuleRequest struct {
	Countries []string         `json:"countries"`
	CIDRs     []string         `json:"cidrs"`
	State     domain.DestState `json:"state"`
	UpdatedAt *int64           `json:"updated_at"`
}

// PutSourceRule answers PUT /api/admin/dest/source-rules/:node_id. An inbound
// on a node that is not native is 409 dest_source_rule_not_native. updated_at,
// when sent, is the precondition (409 dest_source_rule_stale).
func (h *AdminDestHandler) PutSourceRule(c *gin.Context) {
	if !h.sourceReady(c) {
		return
	}
	id, ok := pathID(c, "node_id")
	if !ok {
		return
	}
	var req destSourceRuleRequest
	if !bindDestJSON(c, &req) {
		return
	}
	ctx := c.Request.Context()
	rule, err := h.w.Admin.PrepareSourceRule(ctx, id, destpolicy.SourceRuleInput{Countries: req.Countries, CIDRs: req.CIDRs, State: req.State})
	if err == nil {
		err = h.w.SourceRules.Upsert(ctx, rule, timeFromMS(req.UpdatedAt))
	}
	if err != nil {
		h.destError(c, err, errCodes{stale: "dest_source_rule_stale"})
		return
	}
	geoip := h.w.Lists != nil && h.w.Lists.Countries.ActivePath(ctx) != ""
	c.JSON(http.StatusOK, gin.H{"rule": sourceRuleView(rule, nil), "geoip_available": geoip})
}

// ---- exceptions ----

type destExceptionRequest struct {
	Target  string `json:"target"`
	Match   string `json:"match"`
	Scope   string `json:"scope"`
	GroupID int64  `json:"group_id"`
}

// AddException answers POST /api/admin/dest/exceptions: allow a site or a host
// everywhere. Only the global scope exists in this build; the first call creates
// the exceptions list and the allow policy over it (created is set), later
// calls append.
func (h *AdminDestHandler) AddException(c *gin.Context) {
	if !h.ready(c, h.w.Admin != nil) {
		return
	}
	var req destExceptionRequest
	if !bindDestJSON(c, &req) {
		return
	}
	if req.Scope != "" && req.Scope != "global" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "only the global scope is available", "code": destpolicy.CodeExceptionInvalid, "field": "scope"})
		return
	}
	res, err := h.w.Admin.AddException(c.Request.Context(), req.Target, req.Match)
	if err != nil {
		h.destError(c, err, errCodes{})
		return
	}
	out := gin.H{"entry": res.Entry}
	if res.Created {
		out["created"] = gin.H{"list_id": res.ListID, "policy_id": res.PolicyID}
	} else {
		out["created"] = nil
		out["list_id"], out["policy_id"] = res.ListID, res.PolicyID
	}
	c.JSON(http.StatusOK, out)
}

// ---- test ----

type destTestRequest struct {
	Target string `json:"target"`
	// Port is omitted, or 1..65535: an explicit 0 is refused.
	Port          *int   `json:"port"`
	Network       string `json:"network"`
	UserID        int64  `json:"user_id"`
	PanelID       int64  `json:"panel_id"`
	InboundNodeID int64  `json:"inbound_node_id"`
	SourceIP      string `json:"source_ip"`
	// ResolvedIPs are the addresses the admin says the target resolves to (at
	// most 16 literals; the panel never looks a name up). Omitted is "not given"
	// and an empty list is a failed resolution.
	ResolvedIPs []string `json:"resolved_ips"`
}

// toTestInput is the request as the service takes it.
func (r destTestRequest) toTestInput(port int) destpolicy.TestInput {
	return destpolicy.TestInput{
		Target: r.Target, Port: port, Network: r.Network, UserID: r.UserID, PanelID: r.PanelID,
		InboundNodeID: r.InboundNodeID, SourceIP: r.SourceIP, ResolvedIPs: r.ResolvedIPs,
	}
}

// Test answers POST /api/admin/dest/test: what the published policy does with
// one connection, step by step, through protocol.MatchDestination. No name is
// resolved (resolved_ips is what the admin says it resolves to). It is a POST and so leaves an audit row (who tested what for whom
// is administrator behaviour), which only administrators can read.
func (h *AdminDestHandler) Test(c *gin.Context) {
	if !h.ready(c, h.w.Admin != nil) {
		return
	}
	var req destTestRequest
	if !bindDestJSON(c, &req) {
		return
	}
	port := 0
	if req.Port != nil {
		if *req.Port < 1 || *req.Port > 65535 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "the port is 1..65535", "code": destpolicy.CodeTestInvalid, "field": "port"})
			return
		}
		port = *req.Port
	}
	res, err := h.w.Admin.Test(c.Request.Context(), req.toTestInput(port))
	if err != nil {
		h.destError(c, err, errCodes{})
		return
	}
	c.JSON(http.StatusOK, res)
}

// ---- one account ----

// User answers GET /api/admin/dest/users/:id: the account's group and
// exemption, and its recent hits (hits_available says whether any node of the
// account is recording, so an empty list can be told from nothing watched).
// The usage part stays null: per-site usage is not offered.
func (h *AdminDestHandler) User(c *gin.Context) {
	if !h.ready(c, h.w.Users != nil && h.w.Exemptions != nil) {
		return
	}
	id, ok := pathID(c, "id")
	if !ok {
		return
	}
	ctx := c.Request.Context()
	u, err := h.w.Users.GetByID(ctx, id)
	if err != nil {
		respondError(c, err)
		return
	}
	out := gin.H{
		"user_id": u.ID, "upn": u.UPN, "exemption": nil, "group": nil, "hits_available": nil, "recent_hits": nil,
		"usage_available": nil, "usage_nodes": nil,
	}
	if h.w.Groups != nil {
		if g, err := h.w.Groups.GetByID(ctx, u.GroupID); err == nil {
			out["group"] = gin.H{"id": g.ID, "name": g.Name, "mode": "open", "stage": nil}
		}
	}
	if e, err := h.w.Exemptions.Get(ctx, id); err == nil {
		out["exemption"] = h.exemptionView(c, e)
	} else if !errors.Is(err, domain.ErrNotFound) {
		respondError(c, err)
		return
	}
	if h.w.Hits != nil && h.w.Admin != nil {
		if err := h.userHits(ctx, id, out); err != nil {
			respondError(c, err)
			return
		}
	}
	c.JSON(http.StatusOK, out)
}

// ---- status and control ----

// Status answers GET /api/admin/dest/status: the publish state (generation,
// debounce, pause, the last publish error), the rule-data version and every
// node's delivery state.
func (h *AdminDestHandler) Status(c *gin.Context) {
	if !h.ready(c, h.w.Admin != nil) {
		return
	}
	v, err := h.w.Admin.Status(c.Request.Context())
	if err != nil {
		respondError(c, err)
		return
	}
	c.JSON(http.StatusOK, v)
}

// Publish answers POST /api/admin/dest/publish: publish now, skipping the
// debounce. A publish the validation refuses is 409 dest_policy_over_limit with
// the publish_error; the previous policy stays in force.
func (h *AdminDestHandler) Publish(c *gin.Context) {
	if !h.ready(c, h.w.Policy != nil) {
		return
	}
	st, err := h.w.Policy.Publish(c.Request.Context())
	if err != nil {
		h.destError(c, err, errCodes{})
		return
	}
	c.JSON(http.StatusOK, gin.H{"published_generation": st.PublishedGeneration, "generation": st.Generation, "unpublished": st.Unpublished()})
}

type destPauseRequest struct {
	Paused *bool `json:"paused"`
}

// Pause answers PUT /api/admin/dest/pause: the emergency switch. Paused, no
// node is sent a policy and every node stops enforcing on its next sync.
func (h *AdminDestHandler) Pause(c *gin.Context) {
	if !h.ready(c, h.w.Policy != nil) {
		return
	}
	var req destPauseRequest
	if !bindDestJSON(c, &req) {
		return
	}
	if req.Paused == nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "paused is required", "code": "dest_bad_request", "field": "paused"})
		return
	}
	if err := h.w.Policy.SetPaused(c.Request.Context(), *req.Paused); err != nil {
		h.destError(c, err, errCodes{})
		return
	}
	c.JSON(http.StatusOK, gin.H{"paused": *req.Paused})
}

// RetryAgent answers POST /api/admin/dest/agents/:agent_id/retry: clear a
// node's refusal so its next sync tries the current policy again.
func (h *AdminDestHandler) RetryAgent(c *gin.Context) {
	if !h.ready(c, h.w.Policy != nil) {
		return
	}
	id := strings.TrimSpace(c.Param("agent_id"))
	if id == "" || len(id) > 64 || slices.ContainsFunc([]rune(id), func(r rune) bool { return r <= ' ' }) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "bad agent id", "code": "dest_bad_id", "field": "agent_id"})
		return
	}
	retried, err := h.w.Policy.Retry(c.Request.Context(), id)
	if err != nil {
		h.destError(c, err, errCodes{})
		return
	}
	c.JSON(http.StatusOK, gin.H{"retried": retried})
}
