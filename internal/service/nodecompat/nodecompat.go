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

	nodeprotocol "github.com/KazuhaHub/passwall-protocol/protocol"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/pkg/compatadmission"
)

// Policy builds the compatibility policy this build applies.
//
// THE PROTOCOL RANGE IS PSP'S OWN DECLARATION, not the shared package's.
//
// It read nodeprotocol.Min/MaxSupportedProtocolVersion until 2026-09-22, with a
// comment arguing that taking the numbers from the contract stopped the panel
// claiming a range its wire layer does not speak. That reasoning inverts: the
// shared package is a DEPENDENCY, so reading its constants here means a go.mod
// bump widens what this panel admits with no review in this repository — which
// is the exact failure GenerationRange was introduced to prevent
// (passwall-protocol/protocol/compatibility.go). The handoff reached
// internal/domain and stopped one layer short of the policy that feeds
// compatadmission, and therefore of the remote-upgrade admission path.
//
// Two tests disagreed about this and both passed, because Min and Max are both
// 1: this package asserted the policy follows the shared range, and
// domain/nodeagent_generations_test.go asserted the opposite. The domain one is
// the decision; this file now follows it.
//
// maxObservationAge is the operator-facing bound on how old a report may be
// before it stops authorising high-risk work. Callers pass the ADR 0032
// offline-reconcile window: that document already defines how long an absence
// may go before the panel stops trusting its own record, and a second number
// would be a second answer to the same question.
func Policy(maxObservationAge time.Duration) compatadmission.Policy {
	return PolicyIn(domain.SupportedNodeProtocolGenerations(), maxObservationAge)
}

// PolicyIn builds the policy against a generation range the CALLER declares.
//
// It exists for the same reason AssessCompatibilityIn exists one layer up, and
// the reason is testability rather than flexibility: a range read from a
// constant inside this function cannot be exercised against any other value, so
// no test can show the policy follows the range it was handed rather than one it
// reached for. While PSP's declaration and the shared package's constants are
// both 1, that difference is invisible from the outside — which is precisely the
// window in which the wiring can be changed back without anything failing.
//
// Production has exactly one caller of this, Policy above.
func PolicyIn(generations nodeprotocol.GenerationRange, maxObservationAge time.Duration) compatadmission.Policy {
	upgrade := nodeprotocol.AgentUpgradeCapabilities()
	return compatadmission.Policy{
		// The revision names the generation the decision was taken under, so a
		// stored decision can still be read once the policy moves. It spells a
		// one-generation range as the bare number it has always been, so today's
		// value is byte-identical and a widened range is visibly different.
		Revision:           generationRevision(generations),
		MinProtocolVersion: generations.Min,
		MaxProtocolVersion: generations.Max,
		// The legacy zero mapping stays the SHARED package's, and that is not an
		// inconsistency with the range above. Which generations this panel admits
		// is PSP's decision; that an omitted protocol_version means v1 is a fact
		// about the wire format itself, recorded once in the contract.
		LegacyZeroMapsTo:  nodeprotocol.EffectiveProtocolVersion(0),
		MaxObservationAge: maxObservationAge,
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

func generationRevision(generations nodeprotocol.GenerationRange) string {
	if generations.Min == generations.Max {
		return fmt.Sprintf("native-protocol-v%d", generations.Max)
	}
	return fmt.Sprintf("native-protocol-v%d..%d", generations.Min, generations.Max)
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

// Request builds the request for one operation. The operation is the only thing
// that varies: there used to be a second axis — whether a specific upgrade edge had
// been verified — which made the display question and the action question differ
// about the same peer.
func Request(agent *domain.NodeAgent, operation compatadmission.Operation, now time.Time, policy compatadmission.Policy) compatadmission.Request {
	return compatadmission.Request{
		Operation: operation,
		Observed:  Observation(agent),
		Refused:   Refusal(agent),
		Now:       now,
		Policy:    policy,
	}
}

// Refusal converts the persisted refusal columns. Nil means this panel is not
// currently refusing the agent's reports — either it never has, or it has since
// accepted one, which clears them.
func Refusal(agent *domain.NodeAgent) *compatadmission.Refusal {
	if agent == nil || agent.RefusedAt == nil || agent.RefusedReason == "" {
		return nil
	}
	refusal := &compatadmission.Refusal{
		Reason:  agent.RefusedReason,
		At:      *agent.RefusedAt,
		FirstAt: *agent.RefusedAt,
	}
	if agent.RefusedFirstAt != nil {
		refusal.FirstAt = *agent.RefusedFirstAt
	}
	if agent.RefusedProtocolVersion != nil {
		refusal.ProtocolVersion = *agent.RefusedProtocolVersion
	}
	return refusal
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
		// THE NUMBER IS THE REFUSED ONE WHEN THERE IS ONE. Reading
		// ObservedProtocolVersion here would print the last generation the panel
		// ACCEPTED — 1 — in a sentence explaining that the panel refused 2. The
		// observation is deliberately frozen at the last good value, so it is the
		// wrong field for this message by construction.
		reported := nodeprotocol.EffectiveProtocolVersion(agent.ObservedProtocolVersion)
		if agent.CurrentlyRefused() && agent.RefusedProtocolVersion != nil {
			reported = *agent.RefusedProtocolVersion
		}
		generations := domain.SupportedNodeProtocolGenerations()
		return fmt.Sprintf("native agent protocol version %d is outside the reviewed range %d..%d",
			reported, generations.Min, generations.Max)
	case compatadmission.ReasonReportRefused:
		return "native agent reports are being refused by this panel; the node keeps serving its last applied configuration"
	case compatadmission.ReasonCapabilityMissing:
		// Read through the shared protocol assessment rather than re-deriving:
		// this is a projection of the observation for the message, not a second
		// decision. It is the same function the wire layer uses, so the names in
		// the message cannot drift from the names on the wire.
		compatibility := nodeprotocol.AssessCompatibilityIn(agent.ObservedProtocolVersion, agent.ObservedCapabilities, domain.SupportedNodeProtocolGenerations())
		return fmt.Sprintf("native agent does not currently advertise remote-upgrade capabilities: %s",
			strings.Join(compatibility.MissingAgentUpgrade, ", "))
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
