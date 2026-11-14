package destpolicy

import (
	"context"
	"testing"

	"github.com/KazuhaHub/passwall-protocol/protocol"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
)

type rvAgents struct{ agents []*domain.NodeAgent }

func (f rvAgents) List(context.Context) ([]*domain.NodeAgent, error) { return f.agents, nil }

type rvClients struct{ clients []*domain.PSPClient }

func (f rvClients) ListAll(context.Context) ([]*domain.PSPClient, error) { return f.clients, nil }

// The dest_block reader joins four sources: which nodes record (R17, derived),
// which nodes each account has a client on, which policies count as risk, and
// the block hits. Report-only hits are never in it.
func TestRiskView_InputsJoinRecordingNodesPoliciesAndBlockHits(t *testing.T) {
	h := newHarness(t)
	counted := h.policy("counted", domain.DestActionBlock, domain.DestStateOn, func(p *domain.DestPolicy) {
		p.Inline = domain.DestPolicyInline{Ports: "25"}
		p.CountsAsRisk = true
	})
	plain := h.policy("plain", domain.DestActionBlock, domain.DestStateOn, func(p *domain.DestPolicy) { p.Inline = domain.DestPolicyInline{Ports: "26"} })
	watch := h.policy("watch", domain.DestActionBlock, domain.DestStateReport, func(p *domain.DestPolicy) {
		p.Inline = domain.DestPolicyInline{Ports: "27"}
		p.CountsAsRisk = true
	})
	hour := int64(3600000) * 400
	rows := []domain.DestHit{
		{HourMS: hour, PanelID: 1, UserID: 10, Source: idOf(counted), Action: "block", Dest: "a", Port: 1, Count: 5, FirstMS: hour, LastMS: hour},
		{HourMS: hour, PanelID: 1, UserID: 10, Source: idOf(plain), Action: "block", Dest: "a", Port: 1, Count: 50, FirstMS: hour, LastMS: hour},
		{HourMS: hour, PanelID: 1, UserID: 10, Source: idOf(watch), Action: "observe", Dest: "a", Port: 1, Count: 70, FirstMS: hour, LastMS: hour},
		{HourMS: hour, PanelID: 1, UserID: 10, Source: "p9999", Action: "block", Dest: "a", Port: 1, Count: 9, FirstMS: hour, LastMS: hour}, // a deleted policy
		{HourMS: hour, PanelID: 2, UserID: 11, Source: idOf(counted), Action: "block", Dest: "a", Port: 1, Count: 2, FirstMS: hour, LastMS: hour},
	}
	if _, err := h.repos.Hits.StoreBatch(h.ctx, "agt_1", "b", h.clock.Now(), rows); err != nil {
		t.Fatal(err)
	}
	caps := []string{policyCap, hitsCap}
	agents := rvAgents{agents: []*domain.NodeAgent{
		{AgentID: "agt_1", PanelID: 1, ObservedCapabilities: caps, ObservedCoreEngine: domain.NodeCoreXray},
		{AgentID: "agt_2", PanelID: 2, ObservedCapabilities: caps, ObservedCoreEngine: domain.NodeCoreXray},
	}}
	// Only agent 1 runs a recorded, applied policy.
	if err := h.repos.Agents.Patch(h.ctx, "agt_1", domain.DestAgentPolicyPatch{
		MintedSHA256: ptr("d1"), ReportedSHA256: ptr("d1"), ReportedState: ptr("applied"), CollectEffective: ptr("hits"),
	}); err != nil {
		t.Fatal(err)
	}
	clients := rvClients{clients: []*domain.PSPClient{{UserID: 10, PanelID: 1}, {UserID: 10, PanelID: 2}, {UserID: 11, PanelID: 2}, {UserID: 12, PanelID: 1}}}
	view := NewRiskView(RiskViewDeps{Hits: h.repos.Hits, Policies: h.repos.Policies, Agents: agents, Meta: h.repos.Agents, Clients: clients})

	got, err := view.DestBlockInputs(h.ctx, 0)
	if err != nil {
		t.Fatal(err)
	}
	u10 := got[10]
	if u10.RecordingNodes != 1 || len(u10.Sources) != 1 || u10.Sources[0].Source != idOf(counted) || u10.Sources[0].Count != 5 {
		t.Fatalf("user 10 = %+v: one recording node (panel 1), and only the counted policy's block hits", u10)
	}
	if u11 := got[11]; u11.RecordingNodes != 0 {
		t.Fatalf("user 11 = %+v: its only node does not record", u11)
	}
	if u12 := got[12]; u12.RecordingNodes != 1 || len(u12.Sources) != 0 {
		t.Fatalf("user 12 = %+v: a recording node and no hits", u12)
	}
	_ = protocol.CollectHits
}

func ptr[T any](v T) *T { return &v }
