package destpolicy

import (
	"slices"
	"testing"

	"github.com/KazuhaHub/passwall-protocol/protocol"
)

func (h *harness) compileCore(f fleet, version string) Result {
	h.t.Helper()
	res, err := h.svc.Compile(h.ctx, CompileInput{
		PanelID: 9, AgentID: "agt_9", Capabilities: []string{policyCap}, Snapshot: f.snapshot(),
		Config: protocol.ConfigBody{Core: protocol.CoreSelection{Engine: "xray", Version: version}},
	})
	if err != nil {
		h.t.Fatal(err)
	}
	return res
}

// Only the core version changes after a refusal: the retry must fire.
func TestFallback_ACoreVersionChangeAloneRetriesTheRefusedPolicy(t *testing.T) {
	h, a, _ := withLKG(t)
	h.editPolicy(a, "DOMAIN-SUFFIX,b.example.com")
	d := h.compileCore(base1(), "25.1.1")
	h.observe(rejected(d.Digest))
	if fb := h.compileCore(base1(), "25.1.1"); fb.Digest == d.Digest {
		t.Fatal("setup: no fallback")
	}
	h.compileCore(base1(), "25.1.1") // cached
	res := h.compileCore(base1(), "25.3.6")
	if res.Digest != d.Digest {
		t.Fatalf("core version changed, D not retried: minted %.12s rs=%.12s", res.Digest, h.meta().RejectedSHA256)
	}
}

// Sniffing on an inbound becomes insufficient after the LKG applied. The new
// policy is not held back for it (no fallback): it is sent restricted to the
// inbound that can sniff, and the LKG record is untouched until the node
// applies something else. If the node then refuses it, the fallback is the LKG
// restricted the same way, since its domain rules need exactly the sniffing
// that is now missing on lst_2.
func TestFallback_TheFallbackIsRestrictedForSniffingToo(t *testing.T) {
	h, a, first := withLKG(t)
	h.editPolicy(a, "DOMAIN-SUFFIX,b.example.com")
	bad := base1()
	bad.nodes[1].Sniffing = `{"enabled":true,"destOverride":["http","tls","quic"],"routeOnly":true}`
	res := h.compile(bad)
	if res.Digest == first.Digest || res.Policy == nil || !slices.Equal(res.Policy.Rules[0].Inbounds, []protocol.ListenerKey{"lst_1"}) {
		t.Fatalf("the new policy must go out, restricted to lst_1 (rules %+v)", res.Policy)
	}
	if m := h.meta(); m.AppliedSHA256 != first.Digest || m.FallbackReason != "" {
		t.Fatalf("the LKG record must stay and nothing is a fallback: %+v", m)
	}
	if !hasSniffIssue(res, "lst_2") {
		t.Fatalf("issues = %+v", res.Issues)
	}
	// The node refuses it: the fallback is the LKG, restricted.
	h.observe(rejected(res.Digest))
	fb := h.compile(bad)
	if fb.Policy == nil || fb.Digest == res.Digest || fb.Digest == first.Digest ||
		!slices.Equal(fb.Policy.Rules[0].Inbounds, []protocol.ListenerKey{"lst_1"}) || !slices.Contains(fb.Policy.Rules[0].Domains, "domain:a.example.com") {
		t.Fatalf("fallback = %+v", fb.Policy)
	}
	if err := protocol.ValidateDestinationPolicy(fb.Policy); err != nil {
		t.Fatal(err)
	}
	// Fixed: the refusal was of the restricted bytes and the context changed, so
	// the desired policy is tried again, unrestricted.
	if again := h.compile(base1()); again.Policy == nil || len(again.Policy.Rules[0].Inbounds) != 0 || !slices.Contains(again.Policy.Rules[0].Domains, "domain:b.example.com") {
		t.Fatalf("not recovered: %+v", again.Policy)
	}
}

// Observed core version alone (the config's selection is unchanged) moves the
// refusal context.
func TestFallback_TheObservedCoreVersionIsPartOfTheRefusalContext(t *testing.T) {
	h, a, _ := withLKG(t)
	h.editPolicy(a, "DOMAIN-SUFFIX,b.example.com")
	in := CompileInput{PanelID: 9, AgentID: "agt_9", Capabilities: []string{policyCap}, Snapshot: base1().snapshot(), CoreVersion: "1.0"}
	d, _ := h.svc.Compile(h.ctx, in)
	h.observe(rejected(d.Digest))
	if fb, _ := h.svc.Compile(h.ctx, in); fb.Digest == d.Digest {
		t.Fatal("setup")
	}
	in.CoreVersion = "2.0"
	if res, _ := h.svc.Compile(h.ctx, in); res.Digest != d.Digest {
		t.Fatalf("a new observed core did not retry D: %.12s", res.Digest)
	}
}

// A report that leaves the core version out (a flip to "") is not a change.
func TestFallback_AnEmptyObservedCoreVersionIsNotAContextChange(t *testing.T) {
	h, a, _ := withLKG(t)
	h.editPolicy(a, "DOMAIN-SUFFIX,b.example.com")
	in := CompileInput{PanelID: 9, AgentID: "agt_9", Capabilities: []string{policyCap}, Snapshot: base1().snapshot(), CoreVersion: "1.0"}
	d, _ := h.svc.Compile(h.ctx, in)
	h.observe(rejected(d.Digest))
	fb, _ := h.svc.Compile(h.ctx, in)
	in.CoreVersion = ""
	if res, _ := h.svc.Compile(h.ctx, in); res.Digest != fb.Digest {
		t.Fatalf("an empty observed core version retried D: %.12s", res.Digest)
	}
}
