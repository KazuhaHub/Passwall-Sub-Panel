package destpolicy

import (
	"testing"

	"github.com/KazuhaHub/passwall-protocol/protocol"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
)

// COLLECTION IS COMPILED ONLY WHERE IT IS NEEDED. Policy.Collect makes the node
// attach hit webhooks to its rules, which changes the policy digest and so
// restarts the core. It is therefore emitted only when the panel's effective
// level is non-empty AND something in the policy records a hit (a block or a
// report-only rule): never for a policy of nothing but allows, never for a
// panel that has switched recording off, never for a node that cannot record.

const (
	hitsCap  = protocol.CapabilityAuditHits
	usageCap = protocol.CapabilityAuditUsage
)

func TestEffectiveCollect_Matrix(t *testing.T) {
	// Tier B (hits_and_usage) is not offered yet: no node declares usage, and a
	// panel set to it is recorded at tier A, whatever the node says, so what is
	// stored by a later build is never collected by this one by accident.
	cases := []struct {
		panel domain.AuditCollect
		caps  []string
		want  protocol.CollectLevel
	}{
		{domain.AuditCollectOff, nil, protocol.CollectOff},
		{domain.AuditCollectOff, []string{hitsCap}, protocol.CollectOff},
		{domain.AuditCollectOff, []string{hitsCap, usageCap}, protocol.CollectOff},
		{domain.AuditCollectOff, []string{usageCap}, protocol.CollectOff},

		{domain.AuditCollectHits, nil, protocol.CollectOff},
		{domain.AuditCollectHits, []string{hitsCap}, protocol.CollectHits},
		{domain.AuditCollectHits, []string{hitsCap, usageCap}, protocol.CollectHits},
		{domain.AuditCollectHits, []string{usageCap}, protocol.CollectOff},

		{domain.AuditCollectHitsAndUsage, nil, protocol.CollectOff},
		{domain.AuditCollectHitsAndUsage, []string{hitsCap}, protocol.CollectHits},
		{domain.AuditCollectHitsAndUsage, []string{hitsCap, usageCap}, protocol.CollectHits},
		{domain.AuditCollectHitsAndUsage, []string{usageCap}, protocol.CollectOff},

		// The default of a row with no value is hits; junk is off.
		{"", []string{hitsCap}, protocol.CollectHits},
		{"junk", []string{hitsCap}, protocol.CollectOff},
	}
	for _, c := range cases {
		if got := EffectiveCollect(c.panel, c.caps); got != c.want {
			t.Errorf("EffectiveCollect(%q, %v) = %q, want %q", c.panel, c.caps, got, c.want)
		}
	}
}

func (h *harness) compileCollect(f fleet, panel domain.AuditCollect, caps ...string) Result {
	h.t.Helper()
	res, err := h.svc.Compile(h.ctx, CompileInput{
		PanelID: 9, AgentID: "agt_9", Capabilities: caps, Snapshot: f.snapshot(),
		Config: protocol.ConfigBody{}, AuditCollect: panel,
	})
	if err != nil {
		h.t.Fatal(err)
	}
	return res
}

func TestCollect_EmittedForABlockPolicyOnANodeThatCanRecord(t *testing.T) {
	h := newHarness(t)
	h.policy("smtp", domain.DestActionBlock, domain.DestStateOn, func(p *domain.DestPolicy) { p.Inline = domain.DestPolicyInline{Ports: "25"} })
	h.publishNow()

	res := h.compileCollect(base1(), domain.AuditCollectHits, policyCap, hitsCap)
	if res.Policy == nil || res.Policy.Collect != protocol.CollectHits {
		t.Fatalf("collect = %+v", res.Policy)
	}
	if err := protocol.ValidateDestinationPolicy(res.Policy); err != nil {
		t.Fatal(err)
	}
	if m := h.meta(); m.CollectEffective != "hits" {
		t.Fatalf("collect_effective = %q, want hits", m.CollectEffective)
	}
}

func TestCollect_ReportOnlyRulesAreRecordedToo(t *testing.T) {
	h := newHarness(t)
	h.policy("w", domain.DestActionBlock, domain.DestStateReport, func(p *domain.DestPolicy) { p.Inline = domain.DestPolicyInline{Ports: "25"} })
	h.publishNow()
	if res := h.compileCollect(base1(), "", policyCap, hitsCap); res.Policy == nil || res.Policy.Collect != protocol.CollectHits {
		t.Fatalf("a report-only rule needs its hits collected: %+v", res.Policy)
	}
}

func TestCollect_NotEmittedWhereNothingRecords(t *testing.T) {
	h := newHarness(t)
	h.policy("allow", domain.DestActionAllow, domain.DestStateOn, func(p *domain.DestPolicy) { p.Inline = domain.DestPolicyInline{Ports: "443"} })
	h.publishNow()
	// Only allows: nothing records a hit, so collection would restart the core
	// for nothing.
	res := h.compileCollect(base1(), domain.AuditCollectHits, policyCap, hitsCap)
	if res.Policy == nil || res.Policy.Collect != protocol.CollectOff {
		t.Fatalf("an allow-only policy was sent collection: %+v", res.Policy)
	}
	if m := h.meta(); m.CollectEffective != "" {
		t.Fatalf("collect_effective = %q, want empty", m.CollectEffective)
	}
}

func TestCollect_RespectsThePanelAndTheNode(t *testing.T) {
	h := newHarness(t)
	h.policy("smtp", domain.DestActionBlock, domain.DestStateOn, func(p *domain.DestPolicy) { p.Inline = domain.DestPolicyInline{Ports: "25"} })
	h.publishNow()

	for _, c := range []struct {
		name  string
		panel domain.AuditCollect
		caps  []string
		want  protocol.CollectLevel
	}{
		{"panel off", domain.AuditCollectOff, []string{policyCap, hitsCap}, protocol.CollectOff},
		{"node cannot record", domain.AuditCollectHits, []string{policyCap}, protocol.CollectOff},
		{"default panel", "", []string{policyCap, hitsCap}, protocol.CollectHits},
	} {
		res := h.compileCollect(base1(), c.panel, c.caps...)
		if res.Policy == nil || res.Policy.Collect != c.want {
			t.Errorf("%s: collect = %+v, want %q", c.name, res.Policy, c.want)
		}
	}
}

// Changing the panel's level changes the digest (the node must be told), and
// changing nothing does not.
func TestCollect_AChangeOfLevelIsANewPolicyAndNoChangeIsNot(t *testing.T) {
	h := newHarness(t)
	h.policy("smtp", domain.DestActionBlock, domain.DestStateOn, func(p *domain.DestPolicy) { p.Inline = domain.DestPolicyInline{Ports: "25"} })
	h.publishNow()
	on := h.compileCollect(base1(), domain.AuditCollectHits, policyCap, hitsCap)
	again := h.compileCollect(base1(), domain.AuditCollectHits, policyCap, hitsCap)
	if on.Digest != again.Digest {
		t.Fatal("an unchanged level moved the digest")
	}
	off := h.compileCollect(base1(), domain.AuditCollectOff, policyCap, hitsCap)
	if off.Digest == on.Digest || off.Policy.Collect != protocol.CollectOff {
		t.Fatalf("switching recording off did not reach the node: %+v", off.Policy)
	}
	back := h.compileCollect(base1(), domain.AuditCollectHits, policyCap, hitsCap)
	if back.Digest != on.Digest {
		t.Fatal("switching back did not return to the same policy")
	}
	if m := h.meta(); m.CollectEffective != "hits" {
		t.Fatalf("collect_effective = %q", m.CollectEffective)
	}
}

func TestCollect_PausedAndEmptyAreStillNil(t *testing.T) {
	h := newHarness(t)
	if res := h.compileCollect(base1(), domain.AuditCollectHits, policyCap, hitsCap); res.Policy != nil {
		t.Fatalf("nothing in force produced %+v", res.Policy)
	}
	h.policy("smtp", domain.DestActionBlock, domain.DestStateOn, func(p *domain.DestPolicy) { p.Inline = domain.DestPolicyInline{Ports: "25"} })
	h.publishNow()
	if err := h.svc.SetPaused(h.ctx, true); err != nil {
		t.Fatal(err)
	}
	if res := h.compileCollect(base1(), domain.AuditCollectHits, policyCap, hitsCap); res.Policy != nil {
		t.Fatalf("a paused panel produced %+v", res.Policy)
	}
	if m, err := h.repos.Agents.Get(h.ctx, "agt_9"); err == nil && m.CollectEffective != "" {
		t.Fatalf("collect_effective = %q while nothing is minted", m.CollectEffective)
	}
}

// A node on the fallback runs the trimmed last-known-good, and recording follows
// the panel's CURRENT choice, not the one the old policy was minted with.
func TestCollect_TheFallbackFollowsTheCurrentLevel(t *testing.T) {
	h := newHarness(t)
	l := h.list("l", "DOMAIN-SUFFIX,a.example.com")
	pol := h.policy("A", domain.DestActionBlock, domain.DestStateOn, func(p *domain.DestPolicy) { p.ListIDs = []int64{l.ID} })
	h.publishNow()
	res := h.compileCollect(base1(), domain.AuditCollectHits, policyCap, hitsCap)
	h.observe(applied(res.Digest), policyCap, hitsCap)

	h.editPolicy(pol, "DOMAIN-SUFFIX,b.example.com")
	next := h.compileCollect(base1(), domain.AuditCollectHits, policyCap, hitsCap)
	h.observe(rejected(next.Digest), policyCap, hitsCap)

	// Recording is switched off while the node is on its fallback.
	fb := h.compileCollect(base1(), domain.AuditCollectOff, policyCap, hitsCap)
	if fb.Policy == nil || fb.Policy.Collect != protocol.CollectOff {
		t.Fatalf("the fallback kept recording after the panel switched it off: %+v", fb.Policy)
	}
	if err := protocol.ValidateDestinationPolicy(fb.Policy); err != nil {
		t.Fatal(err)
	}
	// And back on.
	fb2 := h.compileCollect(base1(), domain.AuditCollectHits, policyCap, hitsCap)
	if fb2.Policy == nil || fb2.Policy.Collect != protocol.CollectHits {
		t.Fatalf("the fallback did not resume recording: %+v", fb2.Policy)
	}
}

// R17: a node is "collecting" when it runs the minted policy with a non-empty
// level, declares the hit capability, is on Xray, and reported it applied.
func TestCollecting_DerivedFromWhatTheNodeRuns(t *testing.T) {
	good := domain.DestAgentPolicy{CollectEffective: "hits", MintedSHA256: "d", ReportedSHA256: "d", ReportedState: "applied"}
	caps := []string{policyCap, hitsCap}
	for _, c := range []struct {
		name   string
		mut    func(*domain.DestAgentPolicy)
		caps   []string
		engine domain.NodeCoreEngine
		want   bool
	}{
		{"applying what was minted", func(*domain.DestAgentPolicy) {}, caps, domain.NodeCoreXray, true},
		{"sing-box only enforces", func(*domain.DestAgentPolicy) {}, caps, domain.NodeCoreSingBox, false},
		{"no collection in the policy", func(m *domain.DestAgentPolicy) { m.CollectEffective = "" }, caps, domain.NodeCoreXray, false},
		{"still running an older policy", func(m *domain.DestAgentPolicy) { m.ReportedSHA256 = "old" }, caps, domain.NodeCoreXray, false},
		{"rejected", func(m *domain.DestAgentPolicy) { m.ReportedState = "rejected" }, caps, domain.NodeCoreXray, false},
		{"nothing reported", func(m *domain.DestAgentPolicy) { m.ReportedState, m.ReportedSHA256 = "", "" }, caps, domain.NodeCoreXray, false},
		{"no hit capability", func(*domain.DestAgentPolicy) {}, []string{policyCap}, domain.NodeCoreXray, false},
		{"no meta", nil, caps, domain.NodeCoreXray, false},
	} {
		m := good
		var arg *domain.DestAgentPolicy
		if c.mut != nil {
			c.mut(&m)
			arg = &m
		}
		if got := Collecting(arg, c.caps, c.engine); got != c.want {
			t.Errorf("%s: Collecting = %v, want %v", c.name, got, c.want)
		}
	}
}

// The fallback may only LOWER the recording level the LKG was minted with,
// never raise it. A node that applied L with no recording, then upgraded to an
// agent that can record, must be sent exactly L while it is on its fallback:
// L+recording is a different digest, a policy the node never applied, and if it
// refused that too the LKG would be dropped and the node would stop enforcing.
func TestCollect_TheFallbackNeverRaisesTheLevelOfTheLKG(t *testing.T) {
	h := newHarness(t)
	l := h.list("l", "DOMAIN-SUFFIX,a.example.com")
	pol := h.policy("A", domain.DestActionBlock, domain.DestStateOn, func(p *domain.DestPolicy) { p.ListIDs = []int64{l.ID} })
	h.publishNow()
	// The node cannot record yet: L carries no collect level.
	first := h.compileCollect(base1(), domain.AuditCollectHits, policyCap)
	if first.Policy == nil || first.Policy.Collect != protocol.CollectOff {
		t.Fatalf("setup: %+v", first.Policy)
	}
	h.observe(applied(first.Digest), policyCap)

	// It upgrades (gains audit.hits.v1); the new desired policy is refused.
	h.editPolicy(pol, "DOMAIN-SUFFIX,b.example.com")
	next := h.compileCollect(base1(), domain.AuditCollectHits, policyCap, hitsCap)
	if next.Policy == nil || next.Policy.Collect != protocol.CollectHits {
		t.Fatalf("setup: %+v", next.Policy)
	}
	h.observe(rejected(next.Digest), policyCap, hitsCap)

	fb := h.compileCollect(base1(), domain.AuditCollectHits, policyCap, hitsCap)
	if fb.Policy == nil || fb.Digest != first.Digest || fb.Policy.Collect != protocol.CollectOff {
		t.Fatalf("the fallback is not the policy the node applied: digest %q want %q, policy %+v", fb.Digest, first.Digest, fb.Policy)
	}
	// The node refusing the fallback's bytes cannot happen for the policy it
	// applied, but were the digest ever refused the LKG record must survive
	// the lookup: the next compile still resolves to the same fallback.
	again := h.compileCollect(base1(), domain.AuditCollectHits, policyCap, hitsCap)
	if again.Digest != first.Digest {
		t.Fatalf("the fallback moved: %q", again.Digest)
	}
}

func TestLowerCollect(t *testing.T) {
	for _, c := range []struct{ a, b, want protocol.CollectLevel }{
		{protocol.CollectOff, protocol.CollectHits, protocol.CollectOff},
		{protocol.CollectHits, protocol.CollectOff, protocol.CollectOff},
		{protocol.CollectHits, protocol.CollectHitsAndUsage, protocol.CollectHits},
		{protocol.CollectHitsAndUsage, protocol.CollectHits, protocol.CollectHits},
		{protocol.CollectHits, protocol.CollectHits, protocol.CollectHits},
	} {
		if got := lowerCollect(c.a, c.b); got != c.want {
			t.Errorf("lowerCollect(%q,%q) = %q, want %q", c.a, c.b, got, c.want)
		}
	}
}

// THE RISK SIGNAL MUST NOT FLAP ON A PUBLISH. Collecting (R17) is the honest
// "is it recording right now": after every publish it is false fleet-wide until
// each node applies the new bytes, because the node still reports the digest it
// ran before. The dest_block signal asks a steadier question: does the node
// record, as far as the panel can tell: it applied a policy with recording on,
// and the panel still wants recording.
func TestCollectingForRisk_AMintedPolicyNotYetAppliedDoesNotFlapIt(t *testing.T) {
	caps := []string{policyCap, hitsCap}
	steady := domain.DestAgentPolicy{
		CollectEffective: "hits", MintedSHA256: "d2", ReportedSHA256: "d1", ReportedState: "applied",
		AppliedSHA256: "d1", AppliedCollect: "hits",
	}
	for _, c := range []struct {
		name   string
		mut    func(*domain.DestAgentPolicy)
		caps   []string
		engine domain.NodeCoreEngine
		want   bool
		honest bool
	}{
		{"new policy published, node still on the recording one", func(*domain.DestAgentPolicy) {}, caps, domain.NodeCoreXray, true, false},
		{"applied what was minted", func(m *domain.DestAgentPolicy) { m.ReportedSHA256 = "d2"; m.AppliedSHA256 = "d2" }, caps, domain.NodeCoreXray, true, true},
		{"the one it runs never recorded", func(m *domain.DestAgentPolicy) { m.AppliedCollect = "" }, caps, domain.NodeCoreXray, false, false},
		{"the panel switched recording off", func(m *domain.DestAgentPolicy) { m.CollectEffective = "" }, caps, domain.NodeCoreXray, false, false},
		{"it reports a digest that is not the one it applied", func(m *domain.DestAgentPolicy) { m.ReportedSHA256 = "other" }, caps, domain.NodeCoreXray, false, false},
		{"refused", func(m *domain.DestAgentPolicy) { m.ReportedState = "rejected" }, caps, domain.NodeCoreXray, false, false},
		{"no hit capability", func(*domain.DestAgentPolicy) {}, []string{policyCap}, domain.NodeCoreXray, false, false},
		{"sing-box only enforces", func(*domain.DestAgentPolicy) {}, caps, domain.NodeCoreSingBox, false, false},
	} {
		m := steady
		c.mut(&m)
		if got := CollectingForRisk(&m, c.caps, c.engine); got != c.want {
			t.Errorf("%s: CollectingForRisk = %v, want %v", c.name, got, c.want)
		}
		if got := Collecting(&m, c.caps, c.engine); got != c.honest {
			t.Errorf("%s: Collecting = %v, want %v (it stays the honest one)", c.name, got, c.honest)
		}
	}
	if CollectingForRisk(nil, caps, domain.NodeCoreXray) {
		t.Error("no meta")
	}
}

// What the node last applied is recorded with its level, so the above has
// something to read.
func TestObserveStatus_RecordsTheRecordingLevelOfWhatApplied(t *testing.T) {
	h := newHarness(t)
	h.policy("smtp", domain.DestActionBlock, domain.DestStateOn, func(p *domain.DestPolicy) { p.Inline = domain.DestPolicyInline{Ports: "25"} })
	h.publishNow()
	res := h.compileCollect(base1(), domain.AuditCollectHits, policyCap, hitsCap)
	h.observe(applied(res.Digest), policyCap, hitsCap)
	if m := h.meta(); m.AppliedCollect != "hits" {
		t.Fatalf("applied_collect = %q", m.AppliedCollect)
	}
	// A publish: the node is behind, and the risk signal still holds.
	h.policy("other", domain.DestActionBlock, domain.DestStateOn, func(p *domain.DestPolicy) { p.Inline = domain.DestPolicyInline{Ports: "26"} })
	h.publishNow()
	next := h.compileCollect(base1(), domain.AuditCollectHits, policyCap, hitsCap)
	h.observe(applied(res.Digest), policyCap, hitsCap)
	m := h.meta()
	if next.Digest == res.Digest || m.MintedSHA256 != next.Digest {
		t.Fatalf("setup: the publish did not mint a new policy")
	}
	if Collecting(m, []string{policyCap, hitsCap}, domain.NodeCoreXray) {
		t.Fatal("the honest answer must be false until the node applies the new bytes")
	}
	if !CollectingForRisk(m, []string{policyCap, hitsCap}, domain.NodeCoreXray) {
		t.Fatal("the risk signal flapped on a publish")
	}
}
