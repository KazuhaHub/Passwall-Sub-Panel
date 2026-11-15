package destpolicy

import (
	"context"
	"fmt"
	"slices"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

// AnyCollecting says whether any native node is recording hits right now
// (derived, R17). A policy's hit count is null where this is false: "0 hits"
// from a policy nobody is watching would be a claim nothing supports.
func (a *Admin) AnyCollecting(ctx context.Context) (bool, error) {
	rec, err := a.recordingPanels(ctx)
	return len(rec) > 0, err
}

// recordingPanels is the set of panels whose node is recording now.
func (a *Admin) recordingPanels(ctx context.Context) (map[int64]bool, error) {
	return a.panelsWhere(ctx, Collecting)
}

// panelsWhere is the set of panels whose node satisfies the derived question.
func (a *Admin) panelsWhere(ctx context.Context, derived func(*domain.DestAgentPolicy, []string, domain.NodeCoreEngine) bool) (map[int64]bool, error) {
	out := map[int64]bool{}
	if a.Agents == nil {
		return out, nil
	}
	agents, err := a.Agents.List(ctx)
	if err != nil {
		return nil, fmt.Errorf("agents: %w", err)
	}
	metas, err := a.Service.agents.List(ctx)
	if err != nil {
		return nil, fmt.Errorf("delivery state: %w", err)
	}
	metaOf := make(map[string]*domain.DestAgentPolicy, len(metas))
	for _, m := range metas {
		metaOf[m.AgentID] = m
	}
	for _, ag := range agents {
		if derived(metaOf[ag.AgentID], ag.ObservedCapabilities, ag.ObservedCoreEngine) {
			out[ag.PanelID] = true
		}
	}
	return out, nil
}

// UserHitsAvailable says whether at least one native node the account has a
// client on is recording: when it is false, "no hits" says nothing.
func (a *Admin) UserHitsAvailable(ctx context.Context, userID int64) (bool, error) {
	rec, err := a.recordingPanels(ctx)
	if err != nil || len(rec) == 0 || a.Clients == nil {
		return false, err
	}
	clients, err := a.Clients.ListByUser(ctx, userID)
	if err != nil {
		return false, err
	}
	for _, c := range clients {
		if rec[c.PanelID] {
			return true, nil
		}
	}
	return false, nil
}

// HitWindowDays is the window the policy list and the account view count over:
// seven days, shortened to the hit retention (a window longer than the data
// kept would say "7 days" about 3).
func (a *Admin) HitWindowDays(ctx context.Context) int {
	return min(7, a.HitRetentionDays(ctx))
}

// HitRetentionDays is how many days of hits are kept: the bound of any read
// that wants the latest hit of a source.
func (a *Admin) HitRetentionDays(ctx context.Context) int {
	days := ports.AccessControlEffectiveOf(zeroSettings()).HitRetentionDays
	if a.Settings != nil {
		if set, err := a.Settings.Load(ctx, zeroSettings()); err == nil {
			days = ports.AccessControlEffectiveOf(set).HitRetentionDays
		}
	}
	return days
}

// CollectingNodes is how many native nodes are recording hits right now
// (derived, R17): the number the privacy page states.
func (a *Admin) CollectingNodes(ctx context.Context) (int, error) {
	rec, err := a.recordingPanels(ctx)
	return len(rec), err
}

// CollectingUsageNodes is how many native nodes are recording per-site usage
// right now (derived like CollectingNodes): the number the privacy page states
// when the administrator has chosen to disclose it.
func (a *Admin) CollectingUsageNodes(ctx context.Context) (int, error) {
	rec, err := a.panelsWhere(ctx, CollectingUsage)
	return len(rec), err
}

// UsageNode is a node that records an account's per-site usage.
type UsageNode struct {
	PanelID int64  `json:"panel_id"`
	Name    string `json:"name"`
}

// UserUsageNodes are the nodes that are recording per-site usage now and on
// which the account has a client, by panel id. When it is empty, "no usage"
// says nothing: nothing was watching.
func (a *Admin) UserUsageNodes(ctx context.Context, userID int64) ([]UsageNode, error) {
	out := []UsageNode{}
	rec, err := a.panelsWhere(ctx, CollectingUsage)
	if err != nil || len(rec) == 0 || a.Clients == nil {
		return out, err
	}
	clients, err := a.Clients.ListByUser(ctx, userID)
	if err != nil {
		return nil, err
	}
	seen := map[int64]bool{}
	for _, c := range clients {
		if !rec[c.PanelID] || seen[c.PanelID] {
			continue
		}
		seen[c.PanelID] = true
		name := ""
		if a.Panels != nil {
			if p, err := a.Panels.GetByID(ctx, c.PanelID); err == nil && p != nil {
				name = p.Name
			}
		}
		out = append(out, UsageNode{PanelID: c.PanelID, Name: name})
	}
	slices.SortFunc(out, func(x, y UsageNode) int { return int(x.PanelID - y.PanelID) })
	return out, nil
}

// UsageRetentionDays is how many days of per-site usage are kept: the bound of
// any usage read.
func (a *Admin) UsageRetentionDays(ctx context.Context) int {
	days := ports.AccessControlEffectiveOf(zeroSettings()).UsageRetentionDays
	if a.Settings != nil {
		if set, err := a.Settings.Load(ctx, zeroSettings()); err == nil {
			days = ports.AccessControlEffectiveOf(set).UsageRetentionDays
		}
	}
	return days
}
