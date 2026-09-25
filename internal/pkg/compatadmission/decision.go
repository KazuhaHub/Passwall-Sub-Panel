// Package compatadmission decides whether the panel may proceed with one
// requested operation against one observed peer, and records why in a stable
// code a caller can branch on.
//
// WHY IT IS A FUNCTION AND NOT A SCATTERING OF CHECKS. The same question —
// "may we do this to that peer, now?" — is asked when a task is created, when a
// task is offered, when a page renders an action, and when an operator forces
// something through. Answering it in four places is how a UI ends up offering an
// action the service refuses, and how a capability withdrawn an hour ago keeps
// working because one of the four cached its answer.
//
// THE FOUR ANSWERS ARE INDEPENDENT, and the point of keeping them apart is that
// they fail differently. A protocol generation the panel cannot speak is not a
// reason to stop a peer that is serving traffic; thin evidence is not a reason
// to lock an operator out of the panel. So the decision names which one applied.
//
// It is pure: no clock, no database, no network. Everything it needs is in the
// request, including the time, so a decision can be replayed.
package compatadmission

import (
	"fmt"
	"time"
)

// Operation is what is being asked of the peer. The set is closed on purpose:
// each member has a different risk, and a new one has to be classified rather
// than inheriting the loosest rule.
type Operation string

const (
	// OperationRead is a plain read. It never needs a verdict, because a read
	// is what establishes the observation every other operation depends on.
	OperationRead Operation = "read"
	// OperationBaseSync is the standing sync that keeps a peer serving. It
	// tolerates a stale observation: losing contact must not stop traffic.
	OperationBaseSync Operation = "base-sync"
	// OperationConfigWrite changes what the peer runs. It requires a fresh
	// observation, because a withdrawn capability would otherwise be acted on.
	OperationConfigWrite Operation = "config-write"
	// OperationRemoteUpgrade replaces the peer's own binary. It requires a
	// fresh observation AND a verified upgrade edge.
	OperationRemoteUpgrade Operation = "remote-upgrade"
	// OperationUpgradeEligibility asks whether the peer could be upgraded at
	// all, which is what a server list shows. It is deliberately NOT the same
	// question as OperationRemoteUpgrade: eligibility says nothing about the
	// specific from/to edge, so a node can be shown as upgradeable while a
	// particular upgrade is still refused for want of an edge.
	OperationUpgradeEligibility Operation = "upgrade-eligibility"
)

// Reason is the stable code a caller branches on. Free text lives in Detail.
type Reason string

const (
	ReasonAllowed              Reason = "allowed"
	ReasonUnverified           Reason = "unverified"
	ReasonObservationStale     Reason = "observation-stale"
	ReasonProtocolIncompatible Reason = "protocol-incompatible"
	ReasonCapabilityMissing    Reason = "capability-missing"
	ReasonKnownBad             Reason = "known-bad"
	// ReasonReportRefused is the panel refusing the peer's reports for a
	// structural reason other than its wire generation. It is separate from
	// ReasonProtocolIncompatible because the remedy is different: one is a
	// pairing this panel has not reviewed, the other is a report it cannot read.
	ReasonReportRefused Reason = "report-refused"
)

// Refusal reasons, matching domain.NodeRefusal*.
const (
	RefusalProtocolGeneration = "protocol_generation"
	RefusalReportInvalid      = "report_invalid"
)

// Status is the runtime verdict, which is what an operator is shown. It is
// separate from Allowed: a stale observation still carries base sync, so the
// operation is allowed while the peer is reported as less than verified.
type Status string

const (
	StatusVerified     Status = "verified"
	StatusLimited      Status = "limited"
	StatusUnverified   Status = "unverified"
	StatusIncompatible Status = "incompatible"
)

// Observation is the most recent authenticated report, as persisted. A nil
// pointer means the peer has never reported.
type Observation struct {
	ObservedAt      time.Time
	ProtocolVersion int
	Capabilities    []string
}

// Policy is the applicable compatibility policy. It is data, not code, so the
// revision a decision used can be recorded alongside the decision.
type Policy struct {
	Revision           string
	MinProtocolVersion int
	MaxProtocolVersion int
	// LegacyZeroMapsTo is the generation an omitted version is read as. It is a
	// specific historical rule for specific endpoints, not a general "unknown
	// means compatible".
	LegacyZeroMapsTo     int
	MaxObservationAge    time.Duration
	RequiredCapabilities map[Operation][]string
	// KnownBad maps a peer program version to the reason it must not be used.
	// A version with a reason here is refused outright.
	KnownBad map[string]string
}

// Refusal is an AUTHENTICATED report the panel would not accept.
//
// IT IS NOT AN OBSERVATION, and keeping the two apart is the whole point. An
// observation is what the panel accepted and still acts on. Folding a refused
// generation into Observation.ProtocolVersion would look tidier and is a trap:
// the day the panel widens its range — which is the same release that fixes the
// pairing — that record would pass the protocol branch below and land in the
// capability branch, carrying the capabilities of a report from weeks earlier.
// The peer would be reported as missing capabilities it never lost, with an empty
// list after the colon, and that text is what the upgrade path hands an operator
// as its refusal reason.
type Refusal struct {
	ProtocolVersion int
	Reason          string
	At              time.Time
	FirstAt         time.Time
}

// current reports whether this refusal is still what the panel last did. A
// refusal older than the last accepted observation means the peer recovered.
func (r *Refusal) current(observed *Observation) bool {
	if r == nil || r.At.IsZero() {
		return false
	}
	if observed == nil {
		return true
	}
	return !r.At.Before(observed.ObservedAt)
}

// Request is everything the decision depends on, including the time. Passing
// the clock in rather than reading it is what makes a decision reproducible.
type Request struct {
	PeerVersion string
	Operation   Operation
	Observed    *Observation
	Now         time.Time
	Policy      Policy
	// Refused is the peer's most recent refused report, when the panel refused one
	// more recently than it accepted one. Nil means the panel is not currently
	// refusing this peer.
	Refused *Refusal
	// Force is an operator's explicit acknowledgement of THIN EVIDENCE. It is
	// not a way past a protocol generation the panel cannot speak, a release
	// known to break, or a capability the peer does not have.
	Force bool
}

// Decision is the answer, with enough recorded to re-derive it.
type Decision struct {
	Allowed          bool
	Status           Status
	Reason           Reason
	Detail           string
	EvidenceRevision string
}

// requiresFreshObservation separates the operations that change what a peer
// runs from the one that keeps it serving. A network failure must not stop
// traffic, so base sync proceeds on the last known-good observation; anything
// that acts on the peer's capabilities must not.
func requiresFreshObservation(operation Operation) bool {
	return operation == OperationConfigWrite ||
		operation == OperationRemoteUpgrade ||
		operation == OperationUpgradeEligibility
}

func effectiveProtocolVersion(reported, legacyZeroMapsTo int) int {
	if reported != 0 {
		return reported
	}
	if legacyZeroMapsTo != 0 {
		return legacyZeroMapsTo
	}
	return 1
}

// Decide evaluates one request. It never returns a Decision without a Detail or
// an EvidenceRevision: a refusal an operator cannot read, or one that does not
// say which policy produced it, is not actionable after the policy moves.
func Decide(request Request) Decision {
	policy := request.Policy
	decision := Decision{
		Allowed:          false,
		Status:           StatusUnverified,
		Reason:           ReasonUnverified,
		EvidenceRevision: policy.Revision,
	}

	// A read establishes the observation the other operations consume, so
	// refusing it would be circular. It is also the only thing an operator can
	// still usefully do against a peer nothing is known about.
	if request.Operation == OperationRead {
		decision.Allowed = true
		decision.Status = StatusVerified
		decision.Reason = ReasonAllowed
		decision.Detail = "a read is what establishes an observation; it is never gated on one"
		return decision
	}

	// A release known to break is refused whatever else reports otherwise, and
	// an operator's acknowledgement of thin evidence is not a reason to use it.
	if reason, bad := policy.KnownBad[request.PeerVersion]; bad {
		decision.Status = StatusIncompatible
		decision.Reason = ReasonKnownBad
		decision.Detail = fmt.Sprintf("%s is a known-bad release: %s", request.PeerVersion, reason)
		return decision
	}

	// A REFUSAL OUTRANKS AN OBSERVATION, and it is checked before the
	// no-observation branch on purpose: a node that has never been accepted but is
	// being refused right now must not read as "nothing is known about it". That
	// bucket is where a never-installed node lives, and making the two
	// indistinguishable is the defect this branch exists to end.
	//
	// It is placed before Force for the same reason the protocol branch is: an
	// operator acknowledging thin evidence cannot make the panel able to read a
	// report it is refusing.
	if refusal := request.Refused; refusal.current(request.Observed) {
		decision.Detail = fmt.Sprintf("the last authenticated report was refused at %s and has been refused since %s",
			refusal.At.UTC().Format(time.RFC3339), refusal.FirstAt.UTC().Format(time.RFC3339))
		switch refusal.Reason {
		case RefusalProtocolGeneration:
			// ADR 0033 section 4's definition of incompatible is conditional and
			// this is its condition: a generation outside the reviewed range, with
			// remote upgrade blocked. The peer keeps serving its last applied
			// configuration throughout, which is what the operator-facing wording
			// has to say rather than implying a fault.
			decision.Status = StatusIncompatible
			decision.Reason = ReasonProtocolIncompatible
			decision.Detail = fmt.Sprintf("peer reports protocol %d, reviewed range is %d..%d; %s",
				refusal.ProtocolVersion, policy.MinProtocolVersion, policy.MaxProtocolVersion, decision.Detail)
		default:
			// Not incompatible: the pairing may be fine and the report is not
			// readable. Limited is ADR 0033 section 4's "still serving, action
			// needs a person", which is exactly this peer's situation.
			decision.Status = StatusLimited
			decision.Reason = ReasonReportRefused
		}
		return decision
	}

	if request.Observed == nil {
		if request.Force {
			decision.Allowed = true
			decision.Status = StatusUnverified
			decision.Reason = ReasonAllowed
			decision.Detail = "no observation; an operator accepted the thin evidence"
			return decision
		}
		decision.Status = StatusUnverified
		decision.Reason = ReasonUnverified
		decision.Detail = "no authenticated report has ever been recorded for this peer"
		return decision
	}

	observed := request.Observed
	effective := effectiveProtocolVersion(observed.ProtocolVersion, policy.LegacyZeroMapsTo)
	if effective < policy.MinProtocolVersion || effective > policy.MaxProtocolVersion {
		// Deliberately before Force is consulted: this is the one answer an
		// operator cannot acknowledge their way past, because the panel and the
		// peer do not speak the same protocol and no amount of consent fixes it.
		decision.Status = StatusIncompatible
		decision.Reason = ReasonProtocolIncompatible
		decision.Detail = fmt.Sprintf("peer speaks protocol %d, reviewed range is %d..%d",
			effective, policy.MinProtocolVersion, policy.MaxProtocolVersion)
		return decision
	}

	stale := policy.MaxObservationAge > 0 && request.Now.Sub(observed.ObservedAt) > policy.MaxObservationAge
	if stale && requiresFreshObservation(request.Operation) {
		if request.Force {
			decision.Allowed = true
			decision.Status = StatusUnverified
			decision.Reason = ReasonAllowed
			decision.Detail = "the observation is stale; an operator accepted the thin evidence"
			return decision
		}
		decision.Status = StatusUnverified
		decision.Reason = ReasonObservationStale
		decision.Detail = fmt.Sprintf("the last observation is older than %s and %s acts on the peer", policy.MaxObservationAge, request.Operation)
		return decision
	}

	if missing := missingCapabilities(observed.Capabilities, policy.RequiredCapabilities[request.Operation]); len(missing) > 0 {
		// Not overridable: a capability the peer does not have is a fact about
		// the peer, not a gap in what is known about it.
		decision.Status = StatusLimited
		decision.Reason = ReasonCapabilityMissing
		decision.Detail = fmt.Sprintf("%s requires %v, which the peer does not currently report", request.Operation, missing)
		return decision
	}

	decision.Allowed = true
	decision.Reason = ReasonAllowed
	if stale {
		decision.Status = StatusLimited
		decision.Detail = "allowed; the observation is stale, so this is not reported as freshly verified"
		return decision
	}
	decision.Status = StatusVerified
	decision.Detail = "the observation is fresh and satisfies this operation"
	return decision
}

func missingCapabilities(present, required []string) []string {
	if len(required) == 0 {
		return nil
	}
	have := make(map[string]struct{}, len(present))
	for _, capability := range present {
		have[capability] = struct{}{}
	}
	var missing []string
	for _, capability := range required {
		if _, ok := have[capability]; !ok {
			missing = append(missing, capability)
		}
	}
	return missing
}
