package destpolicy

import (
	"testing"

	"github.com/KazuhaHub/passwall-protocol/protocol"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
)

func (h *harness) compileOn(engine string, panel domain.AuditCollect, caps ...string) Result {
	h.t.Helper()
	res, err := h.svc.Compile(h.ctx, CompileInput{
		PanelID: 9, AgentID: "agt_9", Capabilities: caps, Snapshot: base1().snapshot(),
		Config: protocol.ConfigBody{Core: protocol.CoreSelection{Engine: engine}}, AuditCollect: panel,
	})
	if err != nil {
		h.t.Fatal(err)
	}
	return res
}

// A sing-box node records nothing, so it is never told to: tier B is capped at
// hits for it, and it never gets the collect-only policy. What it receives is
// what it received before tier B existed.
func TestCollectTierB_SingBoxIsCappedAtHits(t *testing.T) {
	caps := []string{policyCap, hitsCap, usageCap}
	h := newHarness(t)
	if res := h.compileOn("sing-box", domain.AuditCollectHitsAndUsage, caps...); res.Policy != nil {
		t.Fatalf("a sing-box node with nothing to enforce was sent %+v", res.Policy)
	}
	h.policy("smtp", domain.DestActionBlock, domain.DestStateOn, func(p *domain.DestPolicy) { p.Inline = domain.DestPolicyInline{Ports: "25"} })
	h.publishNow()
	tierB := h.compileOn("sing-box", domain.AuditCollectHitsAndUsage, caps...)
	hits := h.compileOn("sing-box", domain.AuditCollectHits, caps...)
	if tierB.Policy == nil || tierB.Policy.Collect != protocol.CollectHits || tierB.Digest != hits.Digest {
		t.Fatalf("sing-box on tier B = %+v, want exactly what a hits panel sends", tierB.Policy)
	}
	// Xray still gets tier B.
	if x := h.compileOn("xray", domain.AuditCollectHitsAndUsage, caps...); x.Policy == nil || x.Policy.Collect != protocol.CollectHitsAndUsage {
		t.Fatalf("xray = %+v", x.Policy)
	}
}

// A node that refuses the collect-only policy is not sent it again every sync:
// the refusal is remembered against the capabilities it was refused under, and
// the node gets none until they change or the admin retries.
func TestCollectTierB_ARefusedCollectOnlyPolicyIsNotMintedAgain(t *testing.T) {
	caps := []string{policyCap, hitsCap, usageCap}
	h := newHarness(t)
	first := h.compileCollect(base1(), domain.AuditCollectHitsAndUsage, caps...)
	if first.Policy == nil {
		t.Fatal("setup: no collect-only policy")
	}
	h.observe(rejected(first.Digest), caps...)
	again := h.compileCollect(base1(), domain.AuditCollectHitsAndUsage, caps...)
	if again.Policy != nil || h.meta().MintedSHA256 != "" || h.meta().CollectEffective != "" {
		t.Fatalf("the refused policy was minted again: %+v / %+v", again.Policy, h.meta())
	}
	// The same on the next sync (nothing re-asks).
	if third := h.compileCollect(base1(), domain.AuditCollectHitsAndUsage, caps...); third.Policy != nil {
		t.Fatalf("third sync: %+v", third.Policy)
	}
	// A change of capabilities is a reason to try again.
	if retried := h.compileCollect(base1(), domain.AuditCollectHitsAndUsage, policyCap, hitsCap, usageCap, resolveCap); retried.Policy == nil {
		t.Fatal("a capability change did not lift the refusal")
	}
	// And so is the admin's retry.
	h.observe(rejected(first.Digest), caps...)
	if res := h.compileCollect(base1(), domain.AuditCollectHitsAndUsage, caps...); res.Policy != nil {
		t.Fatal("setup: not refused again")
	}
	if ok, err := h.svc.Retry(h.ctx, "agt_9"); err != nil || !ok {
		t.Fatalf("Retry = %v, %v; want it to report a refusal was cleared", ok, err)
	}
	if res := h.compileCollect(base1(), domain.AuditCollectHitsAndUsage, caps...); res.Policy == nil {
		t.Fatal("the admin's retry did not lift the refusal")
	}
}

// The collect-only policy records no hit, so it is not "recording" for the hit
// derivations (and so not for the dest_block risk judgement): choosing tier B
// must not change what that signal decides.
func TestCollectTierB_TheCollectOnlyPolicyDoesNotCountAsRecordingHits(t *testing.T) {
	caps := []string{policyCap, hitsCap, usageCap}
	m := &domain.DestAgentPolicy{
		CollectEffective: "hits_and_usage", MintedSHA256: collectOnlyDigest, ReportedSHA256: collectOnlyDigest, ReportedState: "applied",
		AppliedSHA256: "old", AppliedCollect: "hits",
	}
	if Collecting(m, caps, domain.NodeCoreXray) || CollectingForRisk(m, caps, domain.NodeCoreXray) {
		t.Fatal("a node running only the collect-only policy was counted as recording hits")
	}
	if !CollectingUsage(m, caps, domain.NodeCoreXray) {
		t.Fatal("but it is collecting usage")
	}
	// With rules in the policy it is both.
	m.MintedSHA256, m.ReportedSHA256 = "rules", "rules"
	if !Collecting(m, caps, domain.NodeCoreXray) || !CollectingUsage(m, caps, domain.NodeCoreXray) {
		t.Fatal("a tier-B node running rules records hits and usage")
	}
}

// A node that upgrades keeps its capabilities when the new release is not what
// declares the missing one, yet the refusal may have been the old release's
// bug: a new agent version is a reason to offer the collect-only policy again.
func TestCollectTierB_ANewAgentVersionLiftsARefusalWithUnchangedCapabilities(t *testing.T) {
	caps := []string{policyCap, hitsCap, usageCap}
	h := newHarness(t)
	compile := func(version string) Result {
		res, err := h.svc.Compile(h.ctx, CompileInput{
			PanelID: 9, AgentID: "agt_9", Capabilities: caps, Snapshot: base1().snapshot(),
			Config: protocol.ConfigBody{}, AuditCollect: domain.AuditCollectHitsAndUsage, AgentVersion: version,
		})
		if err != nil {
			t.Fatal(err)
		}
		return res
	}
	first := compile("4.2.0")
	if first.Policy == nil {
		t.Fatal("setup: no collect-only policy")
	}
	h.observe(rejected(first.Digest), caps...)
	// The refusal is learned in the sync that carried the report; the compile of
	// that sync (same version) stays refused, and so do later ones.
	if res := compile("4.2.0"); res.Policy != nil {
		t.Fatalf("the refused policy was minted again: %+v", res.Policy)
	}
	if res := compile("4.2.0"); res.Policy != nil {
		t.Fatalf("a later sync at the same version minted it: %+v", res.Policy)
	}
	if res := compile("4.2.1"); res.Policy == nil {
		t.Fatal("an upgrade did not lift the refusal")
	}
}
