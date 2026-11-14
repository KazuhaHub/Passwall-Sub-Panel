package destpolicy

import (
	"context"
	"fmt"
	"strconv"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
)

// RiskViewDeps are the read-only sources of the dest_block risk signal.
type RiskViewDeps struct {
	// Hits counts block hits per account and source (sqlstore.DestHitRepo).
	Hits interface {
		BlockCountsSince(ctx context.Context, sinceMS int64) (map[int64]map[string]int64, error)
	}
	// Policies lists the definitions, for which ones count as risk.
	Policies interface {
		List(ctx context.Context) ([]*domain.DestPolicy, error)
	}
	// Agents and Meta say which nodes record: the agent's engine and
	// capabilities, and the delivery state of its policy.
	Agents interface {
		List(ctx context.Context) ([]*domain.NodeAgent, error)
	}
	Meta interface {
		List(ctx context.Context) ([]*domain.DestAgentPolicy, error)
	}
	// Clients says which nodes each account has a client on.
	Clients interface {
		ListAll(ctx context.Context) ([]*domain.PSPClient, error)
	}
}

// RiskView implements the risk service's DestBlockReader. It is read-only.
type RiskView struct{ d RiskViewDeps }

// NewRiskView builds it.
func NewRiskView(d RiskViewDeps) *RiskView { return &RiskView{d: d} }

// DestBlockInputs answers, per account, how many of the native nodes it has a
// client on are recording hits (derived, R17, in the publish-stable form CollectingForRisk) and its BLOCK hits
// since sinceMS on policies that count as risk. Report-only hits are not in the
// hit counts at all (BlockCountsSince reads block rows), the allowlist trial
// rows have no account, and a deleted policy's hits are not counted.
func (v *RiskView) DestBlockInputs(ctx context.Context, sinceMS int64) (map[int64]domain.DestBlockInput, error) {
	policies, err := v.d.Policies.List(ctx)
	if err != nil {
		return nil, fmt.Errorf("dest block: policies: %w", err)
	}
	counted := map[string]bool{}
	for _, p := range policies {
		if p.CountsAsRisk {
			counted["p"+strconv.FormatInt(p.ID, 10)] = true
		}
	}
	agents, err := v.d.Agents.List(ctx)
	if err != nil {
		return nil, fmt.Errorf("dest block: agents: %w", err)
	}
	metas, err := v.d.Meta.List(ctx)
	if err != nil {
		return nil, fmt.Errorf("dest block: delivery state: %w", err)
	}
	metaOf := make(map[string]*domain.DestAgentPolicy, len(metas))
	for _, m := range metas {
		metaOf[m.AgentID] = m
	}
	recording := map[int64]bool{}
	for _, a := range agents {
		if CollectingForRisk(metaOf[a.AgentID], a.ObservedCapabilities, a.ObservedCoreEngine) {
			recording[a.PanelID] = true
		}
	}
	clients, err := v.d.Clients.ListAll(ctx)
	if err != nil {
		return nil, fmt.Errorf("dest block: clients: %w", err)
	}
	panelsOf := map[int64]map[int64]bool{}
	for _, c := range clients {
		if panelsOf[c.UserID] == nil {
			panelsOf[c.UserID] = map[int64]bool{}
		}
		panelsOf[c.UserID][c.PanelID] = true
	}
	hits, err := v.d.Hits.BlockCountsSince(ctx, sinceMS)
	if err != nil {
		return nil, err
	}
	out := map[int64]domain.DestBlockInput{}
	for uid, panels := range panelsOf {
		in := out[uid]
		for pid := range panels {
			if recording[pid] {
				in.RecordingNodes++
			}
		}
		out[uid] = in
	}
	for uid, bySource := range hits {
		in := out[uid]
		for source, n := range bySource {
			if counted[source] {
				in.Sources = append(in.Sources, domain.DestBlockSourceCount{Source: source, Count: n})
			}
		}
		out[uid] = in
	}
	return out, nil
}
