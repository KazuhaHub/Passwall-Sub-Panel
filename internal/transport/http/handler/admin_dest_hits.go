package handler

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
)

// DestHitStore is the read side of the hit records (sqlstore.DestHitRepo).
type DestHitStore interface {
	List(ctx context.Context, f domain.DestHitFilter) ([]domain.DestHitRow, int64, error)
	Summary(ctx context.Context, f domain.DestHitFilter) (domain.DestHitSummary, error)
	Sources(ctx context.Context, sinceMS, untilMS int64) ([]string, error)
	SourceCounts(ctx context.Context, sinceMS int64) ([]domain.DestSourceCount, error)
	LastHits(ctx context.Context, sinceMS int64, sources []string) (map[string]int64, error)
	UserRecent(ctx context.Context, userID, sinceMS int64) ([]domain.DestUserSourceHits, error)
	UserDests(ctx context.Context, userID, sinceMS int64, source, action string, limit int) ([]string, []int64, error)
	DroppedInRange(ctx context.Context, sinceMS, untilMS int64) (int64, error)
}

const (
	codeHitsInvalid = "dest_hits_invalid"
	codeHitsRange   = "dest_hits_range"
	maxHitsRange    = 31 * 24 * time.Hour
)

func (h *AdminDestHandler) hitsReady(c *gin.Context) bool {
	return h.ready(c, h.w.Hits != nil && h.w.Admin != nil)
}

// sourceNames maps a stored source to the name an admin knows it by: a
// policy's name, "the source rule of <inbound>", or nil for what no longer
// exists (a deleted policy, a deleted group): "the allowlist of <group>".
type sourceNamer struct {
	h        *AdminDestHandler
	ctx      context.Context
	policies map[string]string
	nodes    map[int64]string
	groups   map[int64]string
}

func (h *AdminDestHandler) namer(ctx context.Context) *sourceNamer {
	n := &sourceNamer{h: h, ctx: ctx, policies: map[string]string{}, nodes: map[int64]string{}, groups: map[int64]string{}}
	if h.w.Policies != nil {
		if list, err := h.w.Policies.List(ctx); err == nil {
			for _, p := range list {
				n.policies["p"+strconv.FormatInt(p.ID, 10)] = p.Name
			}
		}
	}
	return n
}

func (n *sourceNamer) name(source string) *string {
	switch {
	case strings.HasPrefix(source, "p"):
		if v, ok := n.policies[source]; ok {
			return &v
		}
	case strings.HasPrefix(source, "g"):
		id, err := strconv.ParseInt(source[1:], 10, 64)
		if err != nil || n.h.w.Groups == nil {
			return nil
		}
		if v, ok := n.groups[id]; ok {
			return &v
		}
		if g, err := n.h.w.Groups.GetByID(n.ctx, id); err == nil {
			n.groups[id] = g.Name
			v := g.Name
			return &v
		}
	case strings.HasPrefix(source, "s"):
		id, err := strconv.ParseInt(source[1:], 10, 64)
		if err != nil || n.h.w.Nodes == nil {
			return nil
		}
		if v, ok := n.nodes[id]; ok {
			return &v
		}
		if node, err := n.h.w.Nodes.GetByID(n.ctx, id); err == nil {
			n.nodes[id] = node.DisplayName
			v := node.DisplayName
			return &v
		}
	}
	return nil
}

func sourceKind(source string) string {
	switch {
	case strings.HasPrefix(source, "s"):
		return "source"
	case strings.HasPrefix(source, "g"):
		return "group"
	}
	return "policy"
}

type destHitItem struct {
	HourMS     int64   `json:"hour_ms"`
	PanelID    int64   `json:"panel_id"`
	PanelName  *string `json:"panel_name"`
	UserID     int64   `json:"user_id"`
	UPN        *string `json:"upn"`
	Source     string  `json:"source"`
	SourceName *string `json:"source_name"`
	SourceKind string  `json:"source_kind"`
	Action     string  `json:"action"`
	Dest       string  `json:"dest"`
	Port       int     `json:"port"`
	Count      int64   `json:"count"`
	FirstMS    int64   `json:"first_ms"`
	LastMS     int64   `json:"last_ms"`
}

func badHits(c *gin.Context, code, field, msg string) {
	c.JSON(http.StatusBadRequest, gin.H{"error": msg, "code": code, "field": field})
}

// Hits answers GET /api/admin/dest/hits: the hit records, paged.
//
// Query: user_id, panel_id, source, source_kind (policy|source|group), action
// (block|observe), since and until (UTC ms, at most 31 days apart; default the
// policy window up to now), include_trial, q (destination keyword),
// group_by (none|site|user|policy), page, page_size. The response adds
// summary {block, deny, observe, users} (the time/user/panel selection, narrowed
// to the exact source when one is given, whatever the action and source_kind
// filters: the convert dialog reads the accounts a policy hit from summary.users
// of a source-scoped request), sources (every source with rows in
// the range, name null when it no longer exists) and dropped_in_range (rows
// that were reported and are not here). A row never carries a client address:
// there is none.
func (h *AdminDestHandler) Hits(c *gin.Context) {
	if !h.hitsReady(c) {
		return
	}
	ctx := c.Request.Context()
	q := c.Request.URL.Query()
	now := time.Now().UTC()
	var f domain.DestHitFilter
	intParam := func(name string) (int64, bool, bool) {
		v := q.Get(name)
		if v == "" {
			return 0, false, true
		}
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil || n < 0 {
			badHits(c, codeHitsInvalid, name, name+" must be a non-negative integer")
			return 0, false, false
		}
		return n, true, true
	}
	var ok bool
	if f.UserID, _, ok = intParam("user_id"); !ok {
		return
	}
	if f.PanelID, _, ok = intParam("panel_id"); !ok {
		return
	}
	since, hasSince, ok := intParam("since")
	if !ok {
		return
	}
	until, hasUntil, ok := intParam("until")
	if !ok {
		return
	}
	if !hasUntil {
		until = now.Add(time.Hour).UnixMilli()
	}
	if !hasSince {
		since = until - int64(h.w.Admin.HitWindowDays(ctx))*24*int64(time.Hour/time.Millisecond)
	}
	if until <= since || time.Duration(until-since)*time.Millisecond > maxHitsRange {
		badHits(c, codeHitsRange, "since", "the range is at most 31 days")
		return
	}
	hour := int64(time.Hour / time.Millisecond)
	f.SinceMS, f.UntilMS = since/hour*hour, until
	f.Source = q.Get("source")
	if len(f.Source) > 24 {
		badHits(c, codeHitsInvalid, "source", "bad source")
		return
	}
	switch f.Action = q.Get("action"); f.Action {
	case "", domain.DestHitBlock, domain.DestHitObserve:
	default:
		badHits(c, codeHitsInvalid, "action", "action is block or observe")
		return
	}
	switch f.SourceKind = q.Get("source_kind"); f.SourceKind {
	case "", "policy", "source", "group":
	default:
		badHits(c, codeHitsInvalid, "source_kind", "source_kind is policy, source or group")
		return
	}
	switch g := q.Get("group_by"); g {
	case "", "none", "site", "user", "policy":
		f.GroupBy = g
	default:
		badHits(c, codeHitsInvalid, "group_by", "group_by is none, site, user or policy")
		return
	}
	f.IncludeTrial = q.Get("include_trial") == "1" || q.Get("include_trial") == "true"
	f.Keyword = q.Get("q")
	page, _, ok := intParam("page")
	if !ok {
		return
	}
	size, _, ok := intParam("page_size")
	if !ok {
		return
	}
	// Bounded before the conversion: the repository clamps the page size,
	// but an int on a 32-bit build cannot hold every int64 the query parsed.
	const maxPageParam = 1 << 30
	if page > maxPageParam || size > maxPageParam {
		badHits(c, codeHitsInvalid, "page", "page and page_size are at most 1073741824")
		return
	}
	f.Page, f.PageSize = int(page), int(size)

	rows, total, err := h.w.Hits.List(ctx, f)
	if err != nil {
		respondError(c, err)
		return
	}
	summary, err := h.w.Hits.Summary(ctx, f)
	if err != nil {
		respondError(c, err)
		return
	}
	srcs, err := h.w.Hits.Sources(ctx, f.SinceMS, f.UntilMS)
	if err != nil {
		respondError(c, err)
		return
	}
	dropped, err := h.w.Hits.DroppedInRange(ctx, f.SinceMS, f.UntilMS)
	if err != nil {
		respondError(c, err)
		return
	}
	namer := h.namer(ctx)
	upns, panels := map[int64]*string{}, map[int64]*string{}
	items := make([]destHitItem, 0, len(rows))
	for _, r := range rows {
		it := destHitItem{
			HourMS: r.HourMS, PanelID: r.PanelID, UserID: r.UserID, Source: r.Source, SourceKind: sourceKind(r.Source),
			Action: r.Action, Dest: r.Dest, Port: r.Port, Count: r.Count, FirstMS: r.FirstMS, LastMS: r.LastMS,
		}
		if r.Source != "" {
			it.SourceName = namer.name(r.Source)
		}
		if r.UserID > 0 && h.w.Users != nil {
			if _, seen := upns[r.UserID]; !seen {
				upns[r.UserID] = nil
				if u, err := h.w.Users.GetByID(ctx, r.UserID); err == nil {
					v := u.UPN
					upns[r.UserID] = &v
				}
			}
			it.UPN = upns[r.UserID]
		}
		if r.PanelID > 0 && h.w.Panels != nil {
			if _, seen := panels[r.PanelID]; !seen {
				panels[r.PanelID] = nil
				if p, err := h.w.Panels.GetByID(ctx, r.PanelID); err == nil {
					v := p.Name
					panels[r.PanelID] = &v
				}
			}
			it.PanelName = panels[r.PanelID]
		}
		items = append(items, it)
	}
	sources := make([]gin.H, 0, len(srcs))
	for _, s := range srcs {
		sources = append(sources, gin.H{"source": s, "name": namer.name(s)})
	}
	sizeOut := f.PageSize
	if sizeOut <= 0 || sizeOut > 200 {
		sizeOut = 50
	}
	c.JSON(http.StatusOK, gin.H{
		"items": items, "total": total, "page": max(f.Page, 1), "page_size": sizeOut,
		"group_by": f.GroupBy, "since": f.SinceMS, "until": f.UntilMS,
		"summary": gin.H{"block": summary.Block, "deny": summary.Deny, "observe": summary.Observe, "users": summary.Users},
		"sources": sources, "dropped_in_range": dropped,
	})
}

// policyHits fills hits_recent, would_block_recent and last_hit_at of the
// policy views. The counts stay null while no node is recording.
func (h *AdminDestHandler) policyHits(ctx context.Context, views []destPolicyView) int {
	days := h.w.Admin.HitWindowDays(ctx)
	if h.w.Hits == nil {
		return days
	}
	collecting, err := h.w.Admin.AnyCollecting(ctx)
	if err != nil {
		return days
	}
	since := time.Now().UTC().Add(-time.Duration(days) * 24 * time.Hour).Truncate(time.Hour).UnixMilli()
	counts, err := h.w.Hits.SourceCounts(ctx, since)
	if err != nil {
		return days
	}
	// The latest hit is asked only for the policies listed that can record one
	// (an allow records nothing), within the retained window: never a scan of
	// the whole table.
	var recorders []string
	for i := range views {
		if views[i].Action != domain.DestActionAllow {
			recorders = append(recorders, "p"+strconv.FormatInt(views[i].ID, 10))
		}
	}
	retained := time.Now().UTC().Add(-time.Duration(h.w.Admin.HitRetentionDays(ctx)) * 24 * time.Hour).Truncate(time.Hour).UnixMilli()
	last, _ := h.w.Hits.LastHits(ctx, retained, recorders)
	block, observe := map[string]int64{}, map[string]int64{}
	for _, c := range counts {
		if c.Action == domain.DestHitObserve {
			observe[c.Source] += c.Count
		} else {
			block[c.Source] += c.Count
		}
	}
	for i := range views {
		if views[i].Action == domain.DestActionAllow {
			// Nothing an allow policy does is recorded: null, not zero.
			continue
		}
		src := "p" + strconv.FormatInt(views[i].ID, 10)
		if at, ok := last[src]; ok {
			views[i].LastHitAt = &at
		}
		if !collecting {
			continue
		}
		n := block[src]
		views[i].HitsRecent = &n
		if views[i].State == domain.DestStateReport {
			w := observe[src]
			views[i].WouldBlockRecent = &w
		}
	}
	return days
}

// userHits builds the recent_hits and hits_available parts of the account view.
func (h *AdminDestHandler) userHits(ctx context.Context, userID int64, out gin.H) error {
	available, err := h.w.Admin.UserHitsAvailable(ctx, userID)
	if err != nil {
		return err
	}
	days := h.w.Admin.HitWindowDays(ctx)
	since := time.Now().UTC().Add(-time.Duration(days) * 24 * time.Hour).Truncate(time.Hour).UnixMilli()
	recent, err := h.w.Hits.UserRecent(ctx, userID, since)
	if err != nil {
		return err
	}
	namer := h.namer(ctx)
	items := make([]gin.H, 0, len(recent))
	for _, r := range recent {
		dests, panels, err := h.w.Hits.UserDests(ctx, userID, since, r.Source, r.Action, 3)
		if err != nil {
			return err
		}
		items = append(items, gin.H{
			"source": r.Source, "source_name": namer.name(r.Source), "action": r.Action, "count": r.Count,
			"top_dests": dests, "panels": panels, "last_at": r.LastMS,
		})
	}
	out["hits_available"] = available
	out["recent_hits"] = gin.H{"days": days, "items": items}
	return nil
}
