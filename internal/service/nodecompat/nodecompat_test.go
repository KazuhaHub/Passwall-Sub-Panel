package nodecompat

import (
	"testing"
	"time"

	nodeprotocol "github.com/KazuhaHub/passwall-node/protocol"

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

	t.Run("an unverified upgrade edge blocks the action but not eligibility", func(t *testing.T) {
		// The display question is "is this node eligible at all"; the action
		// question is "may we take THIS edge". One conversion builds the request;
		// the action sets the one field the display cannot know.
		a := agent(1, nodeprotocol.AgentUpgradeCapabilities(), observedAt)
		if display := Decide(a, compatadmission.OperationUpgradeEligibility, observedAt, policy); !display.Allowed {
			t.Fatalf("eligibility must not depend on a specific edge: %s", display.Reason)
		}
		action := Request(a, compatadmission.OperationRemoteUpgrade, observedAt, policy)
		if got := compatadmission.Decide(action); got.Reason != compatadmission.ReasonUpgradeEdgeMissing {
			t.Fatalf("an unverified edge read as %s, want upgrade-edge-missing", got.Reason)
		}
		action.UpgradeEdgeVerified = true
		if got := compatadmission.Decide(action); !got.Allowed {
			t.Fatalf("a verified edge must be admitted: %s", got.Reason)
		}
	})
}

// The policy's protocol range comes from the shared protocol package, so the
// panel cannot drift from the contract it actually speaks.
func TestPolicyUsesTheSharedProtocolRange(t *testing.T) {
	policy := Policy(time.Hour)
	if policy.MinProtocolVersion != nodeprotocol.MinSupportedProtocolVersion ||
		policy.MaxProtocolVersion != nodeprotocol.MaxSupportedProtocolVersion {
		t.Fatalf("policy range = %d..%d, shared range = %d..%d",
			policy.MinProtocolVersion, policy.MaxProtocolVersion,
			nodeprotocol.MinSupportedProtocolVersion, nodeprotocol.MaxSupportedProtocolVersion)
	}
	if policy.LegacyZeroMapsTo != nodeprotocol.EffectiveProtocolVersion(0) {
		t.Fatalf("legacy mapping = %d, shared = %d", policy.LegacyZeroMapsTo, nodeprotocol.EffectiveProtocolVersion(0))
	}
	// The revision must name the generation it was applied under, so a stored
	// decision can be read back after the policy moves.
	if policy.Revision == "" {
		t.Fatal("a policy without a revision cannot be recorded with its decisions")
	}
}
