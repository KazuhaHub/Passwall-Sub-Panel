// Package nodecompat is the ONE place that turns a persisted Node agent
// observation into an admission decision.
//
// WHY A SINGLE CONVERSION POINT. The admission question is asked when an
// upgrade task is created and when a server list is rendered. Deriving the
// answer separately in both is how the list comes to offer an action the service
// refuses — not because either is wrong, but because they drifted. Both now call
// compatadmission through this file, so a change to the rule changes both.
//
// It does not decide anything itself: the policy and the decision both live in
// compatadmission, which is pure and testable without a database.
package nodecompat

import (
	"fmt"
	"strings"
	"time"

	nodeprotocol "github.com/KazuhaHub/passwall-node/protocol"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/pkg/compatadmission"
)

// Policy builds the compatibility policy this build applies.
//
// The protocol range comes from the shared protocol package rather than from
// numbers written here, so the panel cannot be configured into claiming a range
// its own wire layer does not speak.
//
// maxObservationAge is the operator-facing bound on how old a report may be
// before it stops authorising high-risk work. Callers pass the ADR 0032
// offline-reconcile window: that document already defines how long an absence
// may go before the panel stops trusting its own record, and a second number
// would be a second answer to the same question.
func Policy(maxObservationAge time.Duration) compatadmission.Policy {
	upgrade := nodeprotocol.AgentUpgradeCapabilities()
	return compatadmission.Policy{
		// The revision names the generation the decision was taken under, so a
		// stored decision can still be read once the policy moves.
		Revision:           fmt.Sprintf("native-protocol-v%d", nodeprotocol.ProtocolVersion1),
		MinProtocolVersion: nodeprotocol.MinSupportedProtocolVersion,
		MaxProtocolVersion: nodeprotocol.MaxSupportedProtocolVersion,
		LegacyZeroMapsTo:   nodeprotocol.EffectiveProtocolVersion(0),
		MaxObservationAge:  maxObservationAge,
		RequiredCapabilities: map[compatadmission.Operation][]string{
			// Base sync is decided by the protocol range alone (ADR 0033 §1), so
			// it requires no capability at all.
			compatadmission.OperationBaseSync:           {},
			compatadmission.OperationConfigWrite:        {},
			compatadmission.OperationUpgradeEligibility: upgrade,
			compatadmission.OperationRemoteUpgrade:      upgrade,
		},
		// NO KNOWN-BAD ENTRIES YET, and the reason is structural rather than an
		// oversight: a Node agent reports its protocol generation and its
		// capabilities, not its own program version, so nothing in this path can
		// name a release to refuse. The field is honoured by the model and is
		// exercised there; filling it needs a source of Node release identity,
		// which is the reviewed-release registry rather than the report.
		KnownBad: nil,
	}
}

// DefaultObservationAge is the bound used when no settings are available. It is
// the ADR 0032 default offline-reconcile window, which is also what the settings
// layer seeds, so the fallback and the configured value agree until an operator
// deliberately moves them apart.
func DefaultObservationAge() time.Duration {
	return time.Duration(domain.DefaultNodeTaskLifecyclePolicy().OfflineReconcileDays) * 24 * time.Hour
}

// Observation converts the persisted snapshot. A nil agent, or one that has
// never reported, returns nil — which the model reads as "nothing is known"
// rather than as a zero-valued report that might look like a fresh one.
func Observation(agent *domain.NodeAgent) *compatadmission.Observation {
	if agent == nil || agent.ProtocolObservedAt == nil {
		return nil
	}
	return &compatadmission.Observation{
		ObservedAt:      *agent.ProtocolObservedAt,
		ProtocolVersion: agent.ObservedProtocolVersion,
		// Copied: the caller must not be able to mutate a persisted row into a
		// different verdict after the fact.
		Capabilities: append([]string(nil), agent.ObservedCapabilities...),
	}
}

// Request builds the request for one operation. The caller that knows whether a
// specific upgrade edge has been verified sets UpgradeEdgeVerified on the result;
// display callers leave it false and ask OperationUpgradeEligibility instead,
// which is the question that does not depend on an edge.
func Request(agent *domain.NodeAgent, operation compatadmission.Operation, now time.Time, policy compatadmission.Policy) compatadmission.Request {
	return compatadmission.Request{
		Operation: operation,
		Observed:  Observation(agent),
		Now:       now,
		Policy:    policy,
	}
}

// Message renders a decision in the words this layer's operators already read.
//
// The model's Detail is deliberately generic — it knows about peers, not about
// Node agents — so the Node-specific wording lives here. Keeping the two apart
// is what stops a shared model from slowly acquiring the vocabulary of one of
// its callers, and it is why the operator-facing strings stayed unchanged when
// the decision moved.
func Message(agent *domain.NodeAgent, decision compatadmission.Decision) string {
	switch decision.Reason {
	case compatadmission.ReasonUnverified:
		return "native agent compatibility has not been observed; wait for a successful check-in"
	case compatadmission.ReasonObservationStale:
		return "native agent compatibility observation is too old to act on; wait for a successful check-in"
	case compatadmission.ReasonProtocolIncompatible:
		return fmt.Sprintf("native agent protocol version %d is outside the reviewed range %d..%d",
			nodeprotocol.EffectiveProtocolVersion(agent.ObservedProtocolVersion),
			nodeprotocol.MinSupportedProtocolVersion, nodeprotocol.MaxSupportedProtocolVersion)
	case compatadmission.ReasonCapabilityMissing:
		// Read through the shared protocol assessment rather than re-deriving:
		// this is a projection of the observation for the message, not a second
		// decision. It is the same function the wire layer uses, so the names in
		// the message cannot drift from the names on the wire.
		compatibility := nodeprotocol.AssessCompatibility(agent.ObservedProtocolVersion, agent.ObservedCapabilities)
		return fmt.Sprintf("native agent does not currently advertise remote-upgrade capabilities: %s",
			strings.Join(compatibility.MissingAgentUpgrade, ", "))
	case compatadmission.ReasonUpgradeEdgeMissing:
		// The model's own Detail says the edge is unverified without saying which
		// one, because it does not know this layer's vocabulary for ends. Naming
		// both here is what makes the refusal actionable: the operator can see
		// that the incompatibility is with the PAIR they asked for, not with the
		// node, which is still eligible.
		return "this node is compatible, but the requested upgrade path has not been verified; no upgrade is offered along an edge nobody checked"
	default:
		return decision.Detail
	}
}

// Decide answers an edge-agnostic question, which is every question except
// taking a particular upgrade. It is a convenience over Request so a caller that
// has no edge to declare cannot accidentally declare one.
func Decide(agent *domain.NodeAgent, operation compatadmission.Operation, now time.Time, policy compatadmission.Policy) compatadmission.Decision {
	return compatadmission.Decide(Request(agent, operation, now, policy))
}
