package destpolicy

import (
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

// Sniffing on an inbound becomes insufficient after the LKG applied: the fallback is the LKG,
// whose domain rules need exactly the sniffing that is now missing.
func TestFallback_TheFallbackMustPassTheSniffingPrecheckToo(t *testing.T) {
	h, a, first := withLKG(t)
	h.editPolicy(a, "DOMAIN-SUFFIX,b.example.com")
	bad := base1()
	bad.nodes[1].Sniffing = `{"enabled":true,"destOverride":["http","tls","quic"],"routeOnly":true}`
	res := h.compile(bad)
	if res.Digest == first.Digest || res.Policy != nil {
		t.Fatalf("the sniffing fallback is the LKG %.12s, whose domain rules fail the same precheck (rules %v)", first.Digest, ruleIDs(res.Policy))
	}
	// The LKG is not refused by the node; it just cannot apply now.
	if m := h.meta(); m.AppliedSHA256 != first.Digest {
		t.Fatalf("the LKG record was dropped: %+v", m)
	}
	found := false
	for _, is := range res.Issues {
		found = found || (is.Code == IssueSniffInsufficient && is.Key == "lst_2")
	}
	if !found {
		t.Fatalf("issues = %+v", res.Issues)
	}
	// Fixed: the LKG is still there to fall back to.
	if again := h.compile(base1()); again.Policy == nil {
		t.Fatal("not recovered")
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
