package domain

import (
	"testing"
	"time"

	nodeprotocol "github.com/KazuhaHub/passwall-protocol/protocol"
)

// PSP's supported wire generations are PSP's to declare. These tests hold the
// line that used to be blurred: the shared protocol package knowing about a
// generation must not be the same thing as this panel accepting it.
func TestSupportedGenerationsArePSPsOwnDeclaration(t *testing.T) {
	got := SupportedNodeProtocolGenerations()
	if got.Min != nodeprotocol.ProtocolVersion1 || got.Max != nodeprotocol.ProtocolVersion1 {
		t.Fatalf("PSP declares %d..%d, want %d..%d — widening this is a decision for this repository",
			got.Min, got.Max, nodeprotocol.ProtocolVersion1, nodeprotocol.ProtocolVersion1)
	}
}

func TestAGenerationTheSharedPackageDefinesIsStillRefused(t *testing.T) {
	// Stand-in for "the contract gained a generation". The range a consumer
	// passes is what decides; the shared package merely describes the
	// generations that exist.
	hypothetical := nodeprotocol.GenerationRange{Min: nodeprotocol.ProtocolVersion1, Max: nodeprotocol.ProtocolVersion1 + 1}
	caps := nodeprotocol.AgentUpgradeCapabilities()
	reported := nodeprotocol.ProtocolVersion1 + 1

	if comp := nodeprotocol.AssessCompatibilityIn(reported, caps, hypothetical); !comp.ProtocolSupported {
		t.Fatal("the harness is wrong: a caller that declares the generation must accept it")
	}
	if comp := nodeprotocol.AssessCompatibilityIn(reported, caps, SupportedNodeProtocolGenerations()); comp.ProtocolSupported {
		t.Fatal("PSP accepted a generation it has not declared support for")
	}
}

func TestProtocolCompatibilityUsesPSPsDeclaration(t *testing.T) {
	observed := time.Now().UTC()
	agent := &NodeAgent{
		ObservedProtocolVersion: nodeprotocol.ProtocolVersion1,
		ObservedCapabilities:    nodeprotocol.AgentUpgradeCapabilities(),
		ProtocolObservedAt:      &observed,
	}

	comp, ok := agent.ProtocolCompatibility()
	if !ok || !comp.ProtocolSupported || !comp.AgentUpgrade {
		t.Fatalf("a conforming v1 agent should be compatible: ok=%v %+v", ok, comp)
	}

	// The legacy spelling of v1 — a zero version — is still v1, not "unsupported".
	agent.ObservedProtocolVersion = 0
	if comp, ok := agent.ProtocolCompatibility(); !ok || !comp.ProtocolSupported {
		t.Fatalf("the legacy zero-to-v1 mapping regressed: ok=%v %+v", ok, comp)
	}

	// An unobserved agent has no opinion at all rather than a default.
	agent.ProtocolObservedAt = nil
	if _, ok := agent.ProtocolCompatibility(); ok {
		t.Fatal("an agent that has never reported must not answer compatibility")
	}
}
