package nodecompat

import (
	"testing"
	"time"

	nodeprotocol "github.com/KazuhaHub/passwall-protocol/protocol"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/pkg/compatadmission"
)

var observedAt = time.Date(2026, 9, 19, 11, 0, 0, 0, time.UTC)

func agent(protocol int, capabilities []string, at time.Time) *domain.NodeAgent {
	return &domain.NodeAgent{
		ObservedProtocolVersion: protocol,
		ObservedCapabilities:    capabilities,
		ProtocolObservedAt:      &at,
	}
}

// The staleness bound is the ADR 0032 offline-reconcile window rather than a
// new number: that document already defines how long an absence may go before
// the panel stops trusting its own record, and inventing a second one would give
// two answers to the same question.
func TestObservationAgeBoundIsTheOfflineReconcileWindow(t *testing.T) {
	policy := Policy(30 * 24 * time.Hour)
	if policy.MaxObservationAge != 30*24*time.Hour {
		t.Fatalf("max observation age = %s, want the offline reconcile window", policy.MaxObservationAge)
	}
	// An observation inside the window is fresh; the settled ADR 0032 default is
	// 30 days, so a three-day-old report must not read as stale.
	recent := Decide(agent(1, nodeprotocol.AgentUpgradeCapabilities(), observedAt.Add(-72*time.Hour)),
		compatadmission.OperationUpgradeEligibility, observedAt, policy)
	if recent.Status != compatadmission.StatusVerified {
		t.Fatalf("a three-day-old observation read as %s (%s)", recent.Status, recent.Reason)
	}
	old := Decide(agent(1, nodeprotocol.AgentUpgradeCapabilities(), observedAt.Add(-40*24*time.Hour)),
		compatadmission.OperationUpgradeEligibility, observedAt, policy)
	if old.Reason != compatadmission.ReasonObservationStale {
		t.Fatalf("a forty-day-old observation read as %s, want stale", old.Reason)
	}
}

func TestDecidePassesTheAgentObservationThrough(t *testing.T) {
	policy := Policy(30 * 24 * time.Hour)

	t.Run("never observed", func(t *testing.T) {
		got := Decide(&domain.NodeAgent{}, compatadmission.OperationBaseSync, observedAt, policy)
		if got.Allowed || got.Reason != compatadmission.ReasonUnverified {
			t.Fatalf("got allowed=%v reason=%s", got.Allowed, got.Reason)
		}
	})

	t.Run("upgrade capabilities present", func(t *testing.T) {
		got := Decide(agent(1, nodeprotocol.AgentUpgradeCapabilities(), observedAt),
			compatadmission.OperationUpgradeEligibility, observedAt, policy)
		if !got.Allowed || got.Status != compatadmission.StatusVerified {
			t.Fatalf("got allowed=%v status=%s reason=%s", got.Allowed, got.Status, got.Reason)
		}
	})

	t.Run("upgrade capability withdrawn", func(t *testing.T) {
		// The same agent that could upgrade an hour ago, reporting again without
		// the helper. The decision is recomputed, never remembered.
		got := Decide(agent(1, []string{nodeprotocol.CapabilityTaskExecutionV1}, observedAt),
			compatadmission.OperationUpgradeEligibility, observedAt, policy)
		if got.Allowed || got.Reason != compatadmission.ReasonCapabilityMissing {
			t.Fatalf("got allowed=%v reason=%s", got.Allowed, got.Reason)
		}
	})

	t.Run("unreviewed protocol generation", func(t *testing.T) {
		got := Decide(agent(9, nodeprotocol.AgentUpgradeCapabilities(), observedAt),
			compatadmission.OperationUpgradeEligibility, observedAt, policy)
		if got.Allowed || got.Reason != compatadmission.ReasonProtocolIncompatible {
			t.Fatalf("got allowed=%v reason=%s", got.Allowed, got.Reason)
		}
	})

	t.Run("the omitted version is the legacy spelling of v1", func(t *testing.T) {
		got := Decide(agent(0, nodeprotocol.AgentUpgradeCapabilities(), observedAt),
			compatadmission.OperationUpgradeEligibility, observedAt, policy)
		if !got.Allowed {
			t.Fatalf("the legacy spelling must still be admitted: %s", got.Reason)
		}
	})

	t.Run("a compatible and capable peer may take the upgrade", func(t *testing.T) {
		// ONE ANSWER, NOT TWO. Eligibility and the action used to differ by a
		// per-edge review, so a row could read as eligible while every request was
		// refused for an edge nobody had recorded. What decides now is whether the
		// peer is compatible and advertises the capability — the judgement PSP owns.
		a := agent(1, nodeprotocol.AgentUpgradeCapabilities(), observedAt)
		if display := Decide(a, compatadmission.OperationUpgradeEligibility, observedAt, policy); !display.Allowed {
			t.Fatalf("a compatible peer must be eligible: %s", display.Reason)
		}
		if got := compatadmission.Decide(Request(a, compatadmission.OperationRemoteUpgrade, observedAt, policy)); !got.Allowed {
			t.Fatalf("the same peer must be admitted for the action: %s", got.Reason)
		}
	})
}

// The policy's protocol range is PSP'S OWN DECLARATION, not the shared package's.
//
// THIS TEST USED TO ASSERT THE OPPOSITE, and it passed for the same reason its
// replacement does: both numbers are 1. It read
// nodeprotocol.Min/MaxSupportedProtocolVersion and was named
// TestPolicyUsesTheSharedProtocolRange, while
// domain/nodeagent_generations_test.go asserted that the shared package knowing
// a generation must not be the same thing as this panel accepting it. Nothing
// had to choose between them until a generation was added, which is exactly when
// finding out would have been most expensive.
func TestPolicyUsesPSPsOwnGenerationDeclaration(t *testing.T) {
	policy := Policy(time.Hour)
	declared := domain.SupportedNodeProtocolGenerations()
	if policy.MinProtocolVersion != declared.Min || policy.MaxProtocolVersion != declared.Max {
		t.Fatalf("policy range = %d..%d, PSP declares %d..%d",
			policy.MinProtocolVersion, policy.MaxProtocolVersion, declared.Min, declared.Max)
	}
	// The legacy zero mapping is deliberately still the shared package's: which
	// generations this panel admits is PSP's decision, but that an omitted
	// protocol_version means v1 is a property of the wire format itself.
	if policy.LegacyZeroMapsTo != nodeprotocol.EffectiveProtocolVersion(0) {
		t.Fatalf("legacy mapping = %d, shared = %d", policy.LegacyZeroMapsTo, nodeprotocol.EffectiveProtocolVersion(0))
	}
	// The revision must name the generation it was applied under, so a stored
	// decision can be read back after the policy moves.
	if policy.Revision == "" {
		t.Fatal("a policy without a revision cannot be recorded with its decisions")
	}
}

// The policy follows the range it is HANDED, which is the part that can be
// proved today.
//
// TestPolicyUsesPSPsOwnGenerationDeclaration above cannot fail while PSP's
// declaration and the shared package's constants are both 1 — pointing Policy
// back at the dependency would keep it green. It becomes load-bearing at exactly
// the moment it matters: the day the shared package gains a generation, the
// domain declaration stays 1..1 (its own test holds that line) and the two
// sources finally differ. This test covers the mechanism in the meantime.
func TestPolicyFollowsTheGenerationRangeItIsGiven(t *testing.T) {
	ahead := nodeprotocol.GenerationRange{Min: nodeprotocol.ProtocolVersion1, Max: nodeprotocol.ProtocolVersion1 + 1}
	if got := PolicyIn(ahead, time.Hour); got.MinProtocolVersion != ahead.Min || got.MaxProtocolVersion != ahead.Max {
		t.Fatalf("PolicyIn ignored the declared range: got %d..%d, want %d..%d",
			got.MinProtocolVersion, got.MaxProtocolVersion, ahead.Min, ahead.Max)
	}
	// A wider range must also change the revision, so a decision recorded under
	// one range is not mistaken for a decision recorded under another.
	if PolicyIn(ahead, time.Hour).Revision == Policy(time.Hour).Revision {
		t.Fatal("two different generation ranges produced the same revision")
	}
	// And the shipped policy is NOT that range: this is the panel refusing to
	// admit a generation nobody in this repository has reviewed.
	if Policy(time.Hour).MaxProtocolVersion >= ahead.Max {
		t.Fatalf("the shipped policy admits generation %d", ahead.Max)
	}
}

// A generation the SHARED PACKAGE gains must not widen this policy on its own.
//
// domain/nodeagent_generations_test.go holds this line for the declaration; this
// holds it for the policy that actually feeds compatadmission, and therefore for
// the remote-upgrade admission path in internal/service/nodeagentupgrade. A
// go.mod bump alone must not move it.
func TestPolicyDoesNotFollowTheSharedPackageUpward(t *testing.T) {
	policy := Policy(time.Hour)
	ahead := nodeprotocol.ProtocolVersion1 + 1

	// Stand-in for "the contract gained a generation": a caller that DOES declare
	// it accepts it, so the harness is measuring the range and not something else.
	declares := nodeprotocol.GenerationRange{Min: nodeprotocol.ProtocolVersion1, Max: ahead}
	if comp := nodeprotocol.AssessCompatibilityIn(ahead, nodeprotocol.AgentUpgradeCapabilities(), declares); !comp.ProtocolSupported {
		t.Fatal("the harness is wrong: a caller that declares the generation must accept it")
	}

	if policy.MaxProtocolVersion >= ahead {
		t.Fatalf("policy admits generation %d, which this repository has not reviewed", ahead)
	}

	observed := time.Now().UTC()
	agent := &domain.NodeAgent{
		ObservedProtocolVersion: ahead,
		ObservedCapabilities:    nodeprotocol.AgentUpgradeCapabilities(),
		ProtocolObservedAt:      &observed,
	}
	decision := Decide(agent, compatadmission.OperationRemoteUpgrade, observed, policy)
	if decision.Allowed || decision.Reason != compatadmission.ReasonProtocolIncompatible {
		t.Fatalf("an undeclared generation reached the upgrade path: allowed=%v reason=%s",
			decision.Allowed, decision.Reason)
	}
}
