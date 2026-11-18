package destpolicy

import (
	"testing"

	"github.com/KazuhaHub/passwall-protocol/protocol"
	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
)

func (h *harness) compileRouted(agent string, routes map[int64][]RouteExit, egress map[int64]int64, caps ...string) Result {
	h.t.Helper()
	res, err := h.svc.Compile(h.ctx, CompileInput{
		PanelID: 9, AgentID: agent, Capabilities: caps, Snapshot: base1().snapshot(),
		Config: protocol.ConfigBody{Core: protocol.CoreSelection{Engine: "xray"}}, AuditCollect: domain.AuditCollectHits,
		Egress: egress, RouteExits: routes,
	})
	if err != nil {
		h.t.Fatal(err)
	}
	return res
}

// Through the whole compile: a policy scoped to the entry's server covers a
// routed listener only on the exits that leave from it, and the recording of
// hits asks for the egress of each.
func TestCompileSplitsAPanelScopedPolicyByTheExitsOfARoutedListener(t *testing.T) {
	h := newHarness(t)
	h.policy("entry-only", domain.DestActionBlock, domain.DestStateOn, func(p *domain.DestPolicy) {
		p.Inline = domain.DestPolicyInline{Ports: "25"}
		p.Scope, p.PanelIDs = domain.DestScopePanels, []int64{9}
	})
	h.policy("everywhere", domain.DestActionBlock, domain.DestStateOn, func(p *domain.DestPolicy) { p.Inline = domain.DestPolicyInline{Ports: "26"} })
	h.publishNow()
	caps := []string{policyCap, hitsCap, egressCap}
	routes := map[int64][]RouteExit{1: {{Key: protocol.LocalExit, PanelID: 9}, {Key: "lnk_5", PanelID: 2}}}

	plain := h.compileRouted("agt_plain", nil, nil, caps...)
	routed := h.compileRouted("agt_routed", routes, nil, caps...)
	if plain.Policy == nil || routed.Policy == nil {
		t.Fatal("no policy")
	}
	if err := protocol.ValidateDestinationPolicy(routed.Policy); err != nil {
		t.Fatal(err)
	}
	for _, r := range plain.Policy.Rules {
		if len(r.Exits) != 0 {
			t.Fatalf("a node without routes got an exit-scoped rule: %+v", r)
		}
	}
	// The entry-only policy is now confined to the exit that leaves from server 9.
	var confined []protocol.DestinationRule
	for _, r := range routed.Policy.Rules {
		if len(r.Exits) > 0 {
			confined = append(confined, r)
		}
	}
	if len(confined) != 1 || len(confined[0].Exits) != 1 || confined[0].Exits[0] != protocol.LocalExit ||
		len(confined[0].Inbounds) != 1 || confined[0].Inbounds[0] != protocol.NewListenerKey(1) || confined[0].Ports != "25" {
		t.Fatalf("confined rules = %+v", confined)
	}
	if routed.Digest == plain.Digest {
		t.Fatal("routes did not change the policy")
	}
	// Hits carry their egress, because there is a link to tell apart.
	if !routed.Policy.AuditEgress || plain.Policy.AuditEgress {
		t.Fatalf("AuditEgress routed %v plain %v", routed.Policy.AuditEgress, plain.Policy.AuditEgress)
	}
	// Routes that all leave locally have nothing to tell apart and nothing to split.
	local := h.compileRouted("agt_local", map[int64][]RouteExit{1: {{Key: protocol.LocalExit, PanelID: 9}}}, nil, caps...)
	if local.Digest != h.compileRouted("agt_local2", nil, nil, caps...).Digest {
		t.Fatal("routes that only leave locally changed the policy")
	}
}

// A routed node's compile is cached by what it depends on: moving an exit to
// another server compiles again.
func TestCompileKeyFollowsTheExitsOfARoutedListener(t *testing.T) {
	h := newHarness(t)
	h.policy("server-2-only", domain.DestActionBlock, domain.DestStateOn, func(p *domain.DestPolicy) {
		p.Inline = domain.DestPolicyInline{Ports: "25"}
		p.Scope, p.PanelIDs = domain.DestScopePanels, []int64{2}
	})
	h.publishNow()
	caps := []string{policyCap, hitsCap}
	exits := func(panel int64) map[int64][]RouteExit {
		return map[int64][]RouteExit{1: {{Key: protocol.LocalExit, PanelID: 9}, {Key: "lnk_5", PanelID: panel}}}
	}
	toTwo := h.compileRouted("agt_a", exits(2), nil, caps...)
	toThree := h.compileRouted("agt_a", exits(3), nil, caps...)
	if toTwo.Digest == toThree.Digest {
		t.Fatalf("an exit that moved to another server was answered from the cache: %s", toTwo.Digest)
	}
	// With the exit on server 3 the policy (scoped to server 2) covers nothing of the node.
	if toThree.Policy != nil {
		t.Fatalf("policy for a node whose exits are out of scope = %+v", toThree.Policy)
	}
}

func TestPlanPolicyIsReadOnlyAndSeesTheSamePolicy(t *testing.T) {
	h := newHarness(t)
	h.policy("smtp", domain.DestActionBlock, domain.DestStateOn, func(p *domain.DestPolicy) { p.Inline = domain.DestPolicyInline{Ports: "25"} })
	h.publishNow()
	caps := []string{policyCap, hitsCap}
	in := CompileInput{PanelID: 9, AgentID: "agt_plan", Capabilities: caps, Snapshot: base1().snapshot(),
		Config: protocol.ConfigBody{Core: protocol.CoreSelection{Engine: "sing-box"}}, AuditCollect: domain.AuditCollectHits}
	plan, err := h.svc.PlanPolicy(h.ctx, in)
	if err != nil || plan.Policy == nil || plan.Engine != "sing-box" || len(plan.Policy.Rules) != 1 {
		t.Fatalf("plan = %+v %v", plan, err)
	}
	if len(plan.Clients) == 0 {
		t.Fatalf("clients = %+v", plan.Clients)
	}
	compiled := h.compileRouted("agt_plan2", nil, nil, caps...)
	// The engine differs only in what a sing-box node is told; the rules are the same.
	if compiled.Policy == nil || len(compiled.Policy.Rules) != len(plan.Policy.Rules) || compiled.Policy.Rules[0].Ports != plan.Policy.Rules[0].Ports {
		t.Fatalf("compile %+v vs plan %+v", compiled.Policy, plan.Policy)
	}
	// Planning minted nothing and moved no delivery state.
	if meta, err := h.svc.metaFor(h.ctx, "agt_plan"); err != nil || meta.MintedSHA256 != "" || meta.DesiredSHA256 != "" {
		t.Fatalf("planning wrote delivery state: %+v %v", meta, err)
	}
}
