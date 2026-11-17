package destpolicy

import (
	"testing"

	"github.com/KazuhaHub/passwall-protocol/protocol"
	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
)

const egressCap = protocol.CapabilityAuditHitsEgress

// compileEgress compiles node 9's policy with the inputs the audit-egress flag
// depends on: the engine ("" is Xray), the egress bindings, the recording level
// and the declared capabilities. The agent id is fresh per call group, so the
// compile cache of one scenario never answers another.
func (h *harness) compileEgress(agent, engine string, egress map[int64]int64, panel domain.AuditCollect, caps ...string) Result {
	h.t.Helper()
	res, err := h.svc.Compile(h.ctx, CompileInput{
		PanelID: 9, AgentID: agent, Capabilities: caps, Snapshot: base1().snapshot(),
		Config: protocol.ConfigBody{Core: protocol.CoreSelection{Engine: engine}}, AuditCollect: panel, Egress: egress,
	})
	if err != nil {
		h.t.Fatal(err)
	}
	return res
}

func egressHarness(t *testing.T) *harness {
	h := newHarness(t)
	h.policy("smtp", domain.DestActionBlock, domain.DestStateOn, func(p *domain.DestPolicy) { p.Inline = domain.DestPolicyInline{Ports: "25"} })
	h.publishNow()
	return h
}

// AuditEgress is set only when the agent declared the capability, the engine is
// Xray, the server has egress bindings, and recording is on. Anything less and
// the policy is byte-identical (same digest) to one compiled without the
// capability at all, so no such node restarts its core on the upgrade.
func TestAuditEgress_OnlyUnderAllConditions(t *testing.T) {
	tunnel := map[int64]int64{1: 2}
	base := []string{policyCap, hitsCap}
	withCap := append(slicesClone(base), egressCap)
	cases := []struct {
		name   string
		engine string
		egress map[int64]int64
		panel  domain.AuditCollect
		caps   []string
		want   bool
	}{
		{"all four conditions", "", tunnel, domain.AuditCollectHits, withCap, true},
		{"explicit xray", "xray", tunnel, domain.AuditCollectHits, withCap, true},
		{"usage tier", "", tunnel, domain.AuditCollectHitsAndUsage, append(slicesClone(withCap), usageCap), true},
		{"capability not declared", "", tunnel, domain.AuditCollectHits, base, false},
		{"no tunnels", "", nil, domain.AuditCollectHits, withCap, false},
		{"empty bindings", "", map[int64]int64{}, domain.AuditCollectHits, withCap, false},
		{"sing-box", "sing-box", tunnel, domain.AuditCollectHits, withCap, false},
		{"recording off", "", tunnel, domain.AuditCollectOff, withCap, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := egressHarness(t)
			got := h.compileEgress("agt_a", tc.engine, tc.egress, tc.panel, tc.caps...)
			if got.Policy == nil {
				t.Fatal("no policy")
			}
			if got.Policy.AuditEgress != tc.want {
				t.Fatalf("AuditEgress = %v, want %v", got.Policy.AuditEgress, tc.want)
			}
			if err := protocol.ValidateDestinationPolicy(got.Policy); err != nil {
				t.Fatal(err)
			}
			if tc.want {
				return
			}
			// Without the flag, the capability changes nothing: the same bytes as
			// the node would get had it never declared it.
			without := make([]string, 0, len(tc.caps))
			for _, c := range tc.caps {
				if c != egressCap {
					without = append(without, c)
				}
			}
			plain := h.compileEgress("agt_b", tc.engine, tc.egress, tc.panel, without...)
			if plain.Digest != got.Digest {
				t.Fatalf("digest changed by the capability alone: %s != %s", got.Digest, plain.Digest)
			}
		})
	}
}

func slicesClone(s []string) []string { return append([]string(nil), s...) }

// The capability is part of the compile key: a node that starts declaring it
// (an agent upgrade) is compiled again and gets the flag, with no other change.
func TestAuditEgress_CapabilityChangeRecompiles(t *testing.T) {
	h := egressHarness(t)
	tunnel := map[int64]int64{1: 2}
	before := h.compileEgress("agt_a", "", tunnel, domain.AuditCollectHits, policyCap, hitsCap)
	if before.Policy == nil || before.Policy.AuditEgress {
		t.Fatalf("before = %+v", before.Policy)
	}
	after := h.compileEgress("agt_a", "", tunnel, domain.AuditCollectHits, policyCap, hitsCap, egressCap)
	if after.Policy == nil || !after.Policy.AuditEgress {
		t.Fatalf("a changed capability set was answered from the cache: %+v", after.Policy)
	}
	if after.Digest == before.Digest {
		t.Fatal("the flag did not change the digest")
	}
}

// ---- every path that rebuilds a policy re-applies the flag ----

var egressFull = []string{policyCap, hitsCap, egressCap}

// The last-known-good fallback only ever lowers the flag: it keeps it where the
// LKG ran with it and the node still qualifies, drops it when the node no
// longer does, and never mints it into an LKG that never had it.
func TestAuditEgress_LastKnownGoodOnlyLowers(t *testing.T) {
	tunnel := map[int64]int64{1: 2}
	// run applies a first policy under firstEgress, publishes a second one that the
	// node rejects, and returns the fallback compiled under nowEgress.
	run := func(t *testing.T, firstEgress, nowEgress map[int64]int64) (first, fallback Result) {
		t.Helper()
		h := egressHarness(t)
		first = h.compileEgress("agt_9", "", firstEgress, domain.AuditCollectHits, egressFull...)
		if first.Policy == nil {
			t.Fatal("setup: nothing minted")
		}
		h.observe(applied(first.Digest), egressFull...)
		h.policy("B", domain.DestActionBlock, domain.DestStateOn, func(p *domain.DestPolicy) { p.Inline = domain.DestPolicyInline{Ports: "26"} })
		h.publishNow()
		second := h.compileEgress("agt_9", "", nowEgress, domain.AuditCollectHits, egressFull...)
		if second.Digest == first.Digest {
			t.Fatal("setup: the second policy is the first")
		}
		h.observe(rejected(second.Digest), egressFull...)
		fallback = h.compileEgress("agt_9", "", nowEgress, domain.AuditCollectHits, egressFull...)
		return first, fallback
	}
	t.Run("kept while the node still qualifies", func(t *testing.T) {
		first, fb := run(t, tunnel, tunnel)
		if fb.Digest != first.Digest || fb.Policy == nil || !fb.Policy.AuditEgress {
			t.Fatalf("fallback = %+v (digest %s, want %s with the flag)", fb.Policy, fb.Digest, first.Digest)
		}
	})
	t.Run("lowered once the node has no tunnel", func(t *testing.T) {
		first, fb := run(t, tunnel, nil)
		if fb.Policy == nil || fb.Policy.AuditEgress || fb.Digest == first.Digest {
			t.Fatalf("fallback = %+v: the flag must not outlive the tunnels", fb.Policy)
		}
	})
	t.Run("never minted into an LKG that ran without it", func(t *testing.T) {
		first, fb := run(t, nil, tunnel)
		if fb.Policy == nil || fb.Policy.AuditEgress || fb.Digest != first.Digest {
			t.Fatalf("fallback = %+v (digest %s, want %s): raising it would mint bytes the node never applied", fb.Policy, fb.Digest, first.Digest)
		}
	})
}

func hitsPolicy(rules ...protocol.DestinationRule) *protocol.DestinationPolicy {
	return &protocol.DestinationPolicy{Collect: protocol.CollectHits, AuditEgress: true, Rules: rules}
}

// enforcedOnly (the shed-report-only path) keeps the flag on what is left.
func TestAuditEgress_SurvivesEnforcedOnly(t *testing.T) {
	out := enforcedOnly(hitsPolicy(
		protocol.DestinationRule{ID: "p1", Action: protocol.RuleBlock, Ports: "25"},
		protocol.DestinationRule{ID: "p2", Action: protocol.RuleObserve, Ports: "26"},
	))
	if out == nil || len(out.Rules) != 1 || !out.AuditEgress {
		t.Fatalf("enforcedOnly = %+v", out)
	}
}

// settleResolve rebuilds the policy when it narrows report-only IP rules off a
// routeOnly inbound; the rebuilt one keeps the flag.
func TestAuditEgress_SurvivesSettleResolve(t *testing.T) {
	nodes := []*domain.Node{node(1, goodSniffing), node(2, routeOnlySniffing)}
	p := hitsPolicy(
		protocol.DestinationRule{ID: "p1", Action: protocol.RuleBlock, Ports: "25"},
		protocol.DestinationRule{ID: "p3", Action: protocol.RuleObserve, Private: true},
	)
	p.Resolve = protocol.ResolveOnDemand
	out, _, observe := settleResolve(p, nodes, xrayEngine)
	if !observe || out == nil || !out.AuditEgress {
		t.Fatalf("settleResolve = %+v (observe %v)", out, observe)
	}
}

// restrictForSniffing rebuilds the policy when an inbound cannot sniff; the
// rebuilt one keeps the flag.
func TestAuditEgress_SurvivesRestrictForSniffing(t *testing.T) {
	nodes := mixedFleet(weakSniffing).nodes
	p := hitsPolicy(
		protocol.DestinationRule{ID: "p1", Action: protocol.RuleBlock, Domains: []string{"domain:a.example.com"}},
		protocol.DestinationRule{ID: "p2", Action: protocol.RuleBlock, Ports: "25"},
	)
	out, info := restrictForSniffing(p, nodes, xrayEngine)
	if !info.changed || out == nil || !out.AuditEgress {
		t.Fatalf("restrictForSniffing = %+v (changed %v)", out, info.changed)
	}
}
