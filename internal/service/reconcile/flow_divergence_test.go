package reconcile

import (
	"context"
	"testing"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

const visionFlow = "xtls-rprx-vision"

func issuesWithCode(r *Report, code string) []Issue {
	var out []Issue
	for _, i := range r.Issues {
		if i.Code == code {
			out = append(out, i)
		}
	}
	return out
}

// realityInbound is a VLESS+Reality inbound whose existing clients carry the
// Vision flow — the shape an admin imports from a panel they already ran.
func realityInbound(id int) ports.Inbound {
	return ports.Inbound{
		ID: id, Protocol: "vless", Port: 443,
		StreamSettings: `{"network":"tcp","security":"reality"}`,
		Settings:       `{"decryption":"none","clients":[{"id":"abc","email":"someone@x","flow":"` + visionFlow + `"}]}`,
	}
}

// THE regression test for this detector, and the one that was missing.
//
// TestFlowRenderDiverges only ever called the predicate directly, so it stayed
// green while the predicate's ONLY caller sat inside checkOne — a loop that
// iterates legacy ownership rows, which the shared-client migration DROPs. A
// fully unit-tested detector was unreachable on every migrated install, which
// is exactly where 3.7.0 made the state it detects reachable.
//
// So this test asserts through RunOnce with ZERO ownership rows: the migrated
// steady state. It fails if the check ever moves back onto an ownership-driven
// path.
func TestRunOnce_ReportsFlowDivergenceWithNoOwnershipRows(t *testing.T) {
	node := &domain.Node{ID: 1, PanelID: 1, InboundID: 3, DesiredProtocol: "vless", Enabled: true} // Flow blank
	user := &domain.User{ID: 7, UUID: "uuid-7", GroupID: 9, Enabled: true}
	live := realityInbound(node.InboundID)
	client := &recClient{inbounds: []ports.Inbound{live}}
	syncer := &recSyncer{}
	svc := &Service{
		users: &recUserRepo{users: []*domain.User{user}},
		// Empty: the legacy table is gone, which is the whole point.
		ownership:        &recOwnershipRepo{byUser: map[int64][]*domain.XUIClientEntry{}},
		nodes:            &recNodeRepo{nodes: []*domain.Node{node}},
		groups:           &recGroupRepo{groups: []*domain.Group{{ID: user.GroupID, TagFilter: domain.TagFilter{All: true}}}},
		settings:         recSettings{},
		audit:            &recAudit{},
		pool:             recPool{c: client},
		syncer:           syncer,
		axisAReversePush: true,
	}

	report, err := svc.RunOnce(context.Background(), LevelFull)
	if err != nil {
		t.Fatal(err)
	}
	if report.Scanned != 0 {
		t.Fatalf("precondition: a migrated install scans no ownership rows, got Scanned=%d", report.Scanned)
	}
	got := issuesWithCode(report, "flow_render_divergence")
	if len(got) != 1 {
		t.Fatalf("want exactly 1 flow_render_divergence on a migrated install, got %d; all issues: %+v",
			len(got), report.Issues)
	}
	if got[0].InboundID != node.InboundID || got[0].PanelID != node.PanelID {
		t.Fatalf("issue must identify the node's inbound, got %+v", got[0])
	}
	// Report-only: naming a flow is the operator's call, so nothing may be
	// written to the panel or to Node.Flow on account of this issue.
	if syncer.addCalls != 0 || syncer.enableCalls != 0 || syncer.rotateCalls != 0 {
		t.Fatalf("divergence must not heal via the syncer: add=%d enable=%d rotate=%d",
			syncer.addCalls, syncer.enableCalls, syncer.rotateCalls)
	}
	if len(client.updated) != 0 {
		t.Fatalf("divergence must not push to the panel, got %d inbound updates", len(client.updated))
	}
}

// One issue per NODE, not per client: Node.Flow is a node column, so a fleet
// with many users on one mis-imported node must not produce one report each.
// (Under the old per-client placement this scaled with the user count.)
func TestCheckNodes_FlowDivergenceIsReportedOncePerNode(t *testing.T) {
	nodes := []*domain.Node{
		{ID: 1, PanelID: 1, InboundID: 3, DesiredProtocol: "vless", Enabled: true},
		{ID: 2, PanelID: 1, InboundID: 4, DesiredProtocol: "vless", Enabled: true},
	}
	live := []ports.Inbound{realityInbound(3), realityInbound(4)}
	repo := &recNodeRepo{nodes: nodes}
	svc := &Service{nodes: repo, pool: recPool{c: &recClient{inbounds: live}}, axisAReversePush: true}

	report := &Report{}
	cache := cacheFromInbounds(1, live)
	for _, e := range cache {
		e.flow = visionFlow
	}
	svc.checkNodes(context.Background(), report, cache, nil)

	if got := issuesWithCode(report, "flow_render_divergence"); len(got) != 2 {
		t.Fatalf("want 1 issue per diverging node (2), got %d: %+v", len(got), report.Issues)
	}
}

// The negative that keeps the test above honest: an admin who named the flow
// owns it, so PSP renders and pushes the same value and there is nothing to
// report. Without this, a detector that fired unconditionally would pass.
func TestCheckNodes_NoFlowDivergenceWhenNodeFlowIsSet(t *testing.T) {
	node := &domain.Node{ID: 1, PanelID: 1, InboundID: 3, DesiredProtocol: "vless", Flow: visionFlow, Enabled: true}
	live := []ports.Inbound{realityInbound(3)}
	svc := &Service{
		nodes: &recNodeRepo{nodes: []*domain.Node{node}},
		pool:  recPool{c: &recClient{inbounds: live}}, axisAReversePush: true,
	}

	report := &Report{}
	cache := cacheFromInbounds(1, live)
	for _, e := range cache {
		e.flow = visionFlow
	}
	svc.checkNodes(context.Background(), report, cache, nil)

	if got := issuesWithCode(report, "flow_render_divergence"); len(got) != 0 {
		t.Fatalf("a node with its own flow set is not diverging, got %+v", got)
	}
}
