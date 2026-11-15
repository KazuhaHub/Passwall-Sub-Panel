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

// DestUsageStore is the per-site usage read side (sqlstore.DestHitRepo).
type DestUsageStore interface {
	UserUsage(ctx context.Context, userID, panelID, sinceMS, untilMS int64, limit int) (domain.DestUsageSummary, error)
	UsageDropped(ctx context.Context, sinceMS, untilMS, panelID int64) (int64, error)
}

const (
	codeUsageInvalid = "dest_usage_invalid"
	// usageTopSites is how many sites an answer lists; the rest are one line.
	usageTopSites = 20
)

type destUsageRequest struct {
	// Range is "24h" or "<N>d". N is clamped to the usage retention.
	Range   string `json:"range"`
	PanelID *int64 `json:"panel_id"`
}

// usageDays reads a range as whole days: "24h" is one, "<N>d" is N.
func usageDays(r string) (int, bool) {
	r = strings.TrimSpace(r)
	if r == "24h" {
		return 1, true
	}
	n, found := strings.CutSuffix(r, "d")
	if !found {
		return 0, false
	}
	v, err := strconv.ParseInt(n, 10, 32)
	if err != nil || v < 1 {
		return 0, false
	}
	return int(v), true
}

// Usage answers POST /api/admin/dest/users/:id/usage: one account's per-site
// connection counts over a range, its twenty most-used sites and the rest as one
// line. There is no read across accounts: usage is looked up per person.
//
// IT IS A POST ON PURPOSE. The audit middleware records every write, so each
// read of somebody's browsing leaves a row by itself, with the account and the
// range in it, and that row is an administrator's (adminOnlyAuditTargets covers
// everything under /api/admin/dest/). A GET would need a hand-written audit call
// that a later refactor could drop.
func (h *AdminDestHandler) Usage(c *gin.Context) {
	if !h.ready(c, h.w.Usage != nil && h.w.Admin != nil && h.w.Users != nil) {
		return
	}
	id, ok := pathID(c, "id")
	if !ok {
		return
	}
	var req destUsageRequest
	if !bindDestJSON(c, &req) {
		return
	}
	bad := func(field string) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "range is 24h or <N>d, and panel_id a positive id", "code": codeUsageInvalid, "field": field})
	}
	requested, ok := usageDays(req.Range)
	if !ok {
		bad("range")
		return
	}
	var panelID int64
	if req.PanelID != nil {
		if *req.PanelID < 1 {
			bad("panel_id")
			return
		}
		panelID = *req.PanelID
	}
	ctx := c.Request.Context()
	if _, err := h.w.Users.GetByID(ctx, id); err != nil {
		respondError(c, err)
		return
	}
	days := min(requested, h.w.Admin.UsageRetentionDays(ctx))
	until := time.Now().UTC().Add(time.Hour).Truncate(time.Hour).UnixMilli()
	since := time.Now().UTC().Add(-time.Duration(days) * 24 * time.Hour).Truncate(time.Hour).UnixMilli()
	sum, err := h.w.Usage.UserUsage(ctx, id, panelID, since, until, usageTopSites)
	if err != nil {
		respondError(c, err)
		return
	}
	dropped, err := h.w.Usage.UsageDropped(ctx, since, until, panelID)
	if err != nil {
		respondError(c, err)
		return
	}
	nodes, err := h.w.Admin.UserUsageNodes(ctx, id)
	if err != nil {
		respondError(c, err)
		return
	}
	if panelID != 0 {
		kept := nodes[:0]
		for _, n := range nodes {
			if n.PanelID == panelID {
				kept = append(kept, n)
			}
		}
		nodes = kept
	}
	items := make([]gin.H, 0, len(sum.Items))
	for _, it := range sum.Items {
		items = append(items, gin.H{"site": it.Site, "count": it.Count})
	}
	c.Header("Cache-Control", "no-store")
	c.JSON(http.StatusOK, gin.H{
		"effective_days": days, "nodes": nodes, "items": items,
		"others": gin.H{"sites": sum.OtherSites, "count": sum.OtherCount},
		"total":  sum.Total, "dropped_in_range": dropped,
	})
}

// userUsage fills the usage part of the account view.
func (h *AdminDestHandler) userUsage(ctx context.Context, userID int64, out gin.H) error {
	nodes, err := h.w.Admin.UserUsageNodes(ctx, userID)
	if err != nil {
		return err
	}
	out["usage_available"] = len(nodes) > 0
	out["usage_nodes"] = nodes
	return nil
}
