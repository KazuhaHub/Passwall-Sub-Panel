package destpolicy

import (
	"testing"
	"time"

	"github.com/KazuhaHub/passwall-protocol/protocol"
)

// The node refuses D because of its core; the operator switches the engine (or fixes the inbounds),
// then the admin publishes unrelated edits. D is never tried again without a manual retry.
func TestFallback_ARefusedPolicyIsRetriedWhenTheNodeChanged(t *testing.T) {
	h, a, _ := withLKG(t)
	h.editPolicy(a, "DOMAIN-SUFFIX,b.example.com")
	d := h.compile(base1())
	h.observe(&protocol.PolicyStatus{Digest: d.Digest, State: protocol.PolicyStateRejected, IssueCode: protocol.IssueDestinationPolicyRejected})
	h.compile(base1())
	// operator changes the node: engine switch, then sniffing on an inbound changes
	h.compileEngine(base1(), "sing-box")
	f := base1()
	f.nodes[0].Sniffing = `{"enabled":true,"destOverride":["http","tls","quic"],"routeOnly":false,"metadataOnly":false}`
	// an unrelated publish
	h.policy("other", "block", "off", nil)
	h.publishNow()
	res := h.compileEngine(f, "sing-box")
	if res.Digest != d.Digest {
		t.Fatalf("D (%.12s) is not retried after the node changed and a new generation; minted %.12s, meta rs=%.12s", d.Digest, res.Digest, h.meta().RejectedSHA256)
	}
}

// The failure bookkeeping of an unreadable LKG starts over when the record it is
// about changes.
func TestBodyReadFailuresAreKeyedByTheLKG(t *testing.T) {
	h := newHarness(t)
	now := time.Now()
	old := bodyKey("agt", "lkg-1")
	for i := 0; i < bodyReadMaxAttempts; i++ {
		h.svc.bodyReadGaveUp(old, now)
	}
	if !h.svc.bodyReadBackedOff(old, now) {
		t.Fatal("setup: not backed off")
	}
	if h.svc.bodyReadBackedOff(bodyKey("agt", "lkg-2"), now) {
		t.Fatal("a new LKG inherited the old one's back-off")
	}
}
