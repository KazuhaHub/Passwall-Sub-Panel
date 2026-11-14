package destpolicy

import (
	"context"
	"errors"
	"testing"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
)

func nodeOf(t *testing.T, v *StatusView, panel int64) NodeState {
	t.Helper()
	for _, n := range v.Nodes {
		if n.PanelID == panel {
			return n
		}
	}
	t.Fatalf("no node for panel %d", panel)
	return NodeState{}
}

// The status says, per node, what the panel is set to record (collect), what the
// node was told (collect_effective), whether it is recording now (collecting,
// derived: R17) and how many hits it produced in the last 24 hours.
func TestStatus_CarriesTheRecordingFieldsOfANode(t *testing.T) {
	ah := newAdminHarness(t)
	ctx := ah.ctx
	ah.admin.Hits = ah.repos.Hits
	agent := ah.admin.Agents.(fakeAgents).agents[0]
	agent.ObservedCapabilities = []string{policyCap, hitsCap}

	v, err := ah.admin.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	n := nodeOf(t, v, 9)
	if n.Collect != "hits" || n.CollectEffective != "" || n.Collecting {
		t.Fatalf("idle node = %+v: default level hits, nothing minted, so not collecting", n)
	}
	if n.Hits24h == nil || *n.Hits24h != 0 {
		t.Fatalf("hits_24h = %v, want 0 for a node that can record and has no hits", n.Hits24h)
	}
	if x := nodeOf(t, v, 10); x.Collect != "" || x.Collecting || x.Hits24h != nil {
		t.Fatalf("a 3X-UI node = %+v: recording does not apply", x)
	}

	ah.panels.panels[9].AuditCollect = domain.AuditCollectOff
	if err := ah.repos.Agents.Patch(ctx, "agt_9", domain.DestAgentPolicyPatch{
		MintedSHA256: ptr("d1"), ReportedSHA256: ptr("d1"), ReportedState: ptr("applied"), CollectEffective: ptr("hits"),
	}); err != nil {
		t.Fatal(err)
	}
	h := ah.clock.Now().Truncate(3600 * 1e9).UnixMilli()
	if _, err := ah.repos.Hits.StoreBatch(ctx, "agt_9", "b", ah.clock.Now(), []domain.DestHit{
		{HourMS: h, PanelID: 9, UserID: 5, Source: "p1", Action: "block", Dest: "a", Port: 1, Count: 4, FirstMS: h, LastMS: h},
		{HourMS: h, PanelID: 9, UserID: 0, Source: "g1", Action: "observe", Dest: "t", Port: 0, Count: 99, FirstMS: h, LastMS: h},
	}); err != nil {
		t.Fatal(err)
	}
	v, _ = ah.admin.Status(ctx)
	n = nodeOf(t, v, 9)
	if n.Collect != "off" || n.CollectEffective != "hits" || !n.Collecting || n.Hits24h == nil || *n.Hits24h != 4 {
		t.Fatalf("node = collect %q effective %q collecting %v hits %v", n.Collect, n.CollectEffective, n.Collecting, n.Hits24h)
	}

	// An agent without the capability is never collecting, and a sing-box one
	// only enforces.
	agent.ObservedCapabilities = []string{policyCap}
	v, _ = ah.admin.Status(ctx)
	if nodeOf(t, v, 9).Collecting {
		t.Fatal("a node that does not declare audit.hits.v1 is collecting")
	}
	agent.ObservedCapabilities = []string{policyCap, hitsCap}
	agent.ObservedCoreEngine = domain.NodeCoreSingBox
	v, _ = ah.admin.Status(ctx)
	if nodeOf(t, v, 9).Collecting {
		t.Fatal("a sing-box node is collecting")
	}
}

type failingCounter struct{}

func (failingCounter) PanelCounts(context.Context, int64) (map[int64]int64, error) {
	return nil, errors.New("hit table unreadable")
}

// The 24-hour count is a nicety: its failure nulls hits_24h and nothing else.
func TestStatus_AFailingHitCountOnlyNullsHits24h(t *testing.T) {
	ah := newAdminHarness(t)
	ah.admin.Hits = failingCounter{}
	agent := ah.admin.Agents.(fakeAgents).agents[0]
	agent.ObservedCapabilities = []string{policyCap, hitsCap}
	v, err := ah.admin.Status(ah.ctx)
	if err != nil {
		t.Fatalf("a failing hit count failed the status: %v", err)
	}
	n := nodeOf(t, v, 9)
	if n.Hits24h != nil || !n.Supports.Hits {
		t.Fatalf("node = %+v: hits_24h must be null and the rest intact", n)
	}
}
