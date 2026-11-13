package destpolicy

import (
	"testing"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
)

// The desired policy equals the trimmed LKG (only removals since the LKG), and the node refuses it.
func TestFallback_DesiredEqualToTheTrimmedLKGIsNotMintedAgainAfterARefusal(t *testing.T) {
	h := newHarness(t)
	l := h.list("l", "DOMAIN-SUFFIX,a.example.com")
	h.policy("A", domain.DestActionBlock, domain.DestStateOn, func(p *domain.DestPolicy) { p.ListIDs = []int64{l.ID} })
	if err := h.repos.Exemptions.Upsert(h.ctx, &domain.DestExemption{UserID: 11}); err != nil {
		t.Fatal(err)
	}
	h.publishNow()
	f := base1()
	full := h.compile(f)
	h.observe(applied(full.Digest))
	f.users = []int64{10, 12} // user 11 leaves this panel
	d := h.compile(f)
	if d.Digest == full.Digest {
		t.Fatal("setup")
	}
	for i := 0; i < 4; i++ {
		h.observe(rejected(d.Digest))
		res := h.compile(f)
		if i > 0 && res.Digest == d.Digest {
			t.Fatalf("round %d: the refused policy %s is minted again for ever (meta rs=%.12s applied=%.12s minted=%.12s desired=%.12s)",
				i, d.Digest[:12], h.meta().RejectedSHA256, h.meta().AppliedSHA256, h.meta().MintedSHA256, h.meta().DesiredSHA256)
		}
	}
}

// A new generation whose desired policy is the very one the node refused is no
// reason to retry: only RejectedGeneration advances.
func TestFallback_ANewGenerationWithTheSameRefusedPolicyIsNotRetried(t *testing.T) {
	h, a, first := withLKG(t)
	h.editPolicy(a, "DOMAIN-SUFFIX,b.example.com")
	second := h.compile(base1())
	h.observe(rejected(second.Digest))
	h.policy("unrelated", domain.DestActionBlock, domain.DestStateOff, func(p *domain.DestPolicy) { p.Inline = domain.DestPolicyInline{Ports: "99"} })
	h.publishNow()
	if res := h.compile(base1()); res.Digest != first.Digest {
		t.Fatalf("the refused policy was retried on an unchanged definition: %q", res.Digest)
	}
	if m := h.meta(); m.RejectedGeneration != h.generation() {
		t.Fatalf("rejected generation = %d, want %d", m.RejectedGeneration, h.generation())
	}
}

// While the LKG stays unreadable it is read at most once a minute.
func TestFallback_AnUnreadableBodyIsRetriedOnlyOncePerMinute(t *testing.T) {
	h, a, _ := withLKG(t)
	h.editPolicy(a, "DOMAIN-SUFFIX,b.example.com")
	second := h.compile(base1())
	h.observe(rejected(second.Digest))
	h.counts.Lock()
	h.counts.failBody = true
	h.counts.Unlock()
	in := CompileInput{PanelID: 9, AgentID: "agt_9", Capabilities: []string{policyCap}, Snapshot: base1().snapshot()}
	for i := 0; i < 3; i++ {
		_, _ = h.svc.Compile(h.ctx, in)
	}
	h.counts.Lock()
	reads := h.counts.bodyReads
	h.counts.Unlock()
	for i := 0; i < 5; i++ {
		if res, err := h.svc.Compile(h.ctx, in); err != nil || res.Policy != nil {
			t.Fatalf("(%v, %v)", res.Policy, err)
		}
	}
	h.counts.Lock()
	defer h.counts.Unlock()
	if h.counts.bodyReads != reads {
		t.Fatalf("%d re-reads inside the minute", h.counts.bodyReads-reads)
	}
}
