package destpolicy

import (
	"testing"

	"github.com/KazuhaHub/passwall-protocol/protocol"
)

// A report that carries no agent version says nothing about whether the agent
// was upgraded, so it must not lift a remembered refusal of the collect-only
// policy (liftStaleCollectRefusal already behaves so); collectOnlyRefused and
// the status reader must agree with it.
func TestCollectOnlyRefusal_EmptyVersionIsNotAChange(t *testing.T) {
	caps := []string{protocol.CapabilityDestinationPolicy, protocol.CapabilityAuditHits, protocol.CapabilityAuditUsage}
	s := &Service{collectRefused: map[string]collectRefusal{"a": {caps: capsToken(caps), version: "4.2.0"}}}

	if !s.collectOnlyRefused("a", caps, "") {
		t.Fatal("an empty reported version lifted the refusal in collectOnlyRefused")
	}
	if !s.CollectOnlyRefused("a", caps, "") {
		t.Fatal("an empty reported version hid the refusal from the status reader")
	}
	if _, ok := s.collectRefused["a"]; !ok {
		t.Fatal("the refusal was forgotten on an empty version")
	}
	// A real change still lifts it.
	if s.collectOnlyRefused("a", caps, "4.2.1") {
		t.Fatal("a different release did not lift the refusal")
	}
}
