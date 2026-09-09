package protocol

// NodeReport is the agent's half of the round trip: everything it observes,
// and nothing it desires.
//
// LOOK AT WHAT IS NOT HERE. There is no field by which an agent states its
// configuration, its port, its protocol, its credentials or its limits. §5's
// second hard constraint is that an agent reports "I applied version N", never
// "this is my config" — because the latter is something PSP would swallow as a
// new desired value, at which point confirmation degenerates into PSP agreeing
// with itself. That constraint is usually a discipline; here it is a property
// of the type, checkable by reading the struct.
//
// The same rule closes ADR 0025 debt 3(d) at the protocol boundary: a health
// probe's target host and port may only come from the desired document, so
// there is nowhere in this message to put them.
type NodeReport struct {
	AgentID string `json:"agent_id"`

	// Have is what the agent currently holds per stream. The validators live in
	// the BODY, in one place — not in HTTP conditional headers. §8.3: those
	// headers are defined on GET, this is a POST that never returns 304, so
	// middleware and proxies would not treat them as intended; and a header
	// plus a body field would be two writers of one fact with no precedence
	// rule between them.
	Have map[string]StreamState `json:"have"`

	// Objects is per-object convergence. What is ATOMIC is the desired-state
	// commit, not runtime convergence — so accepted-version and per-object
	// status are separate fields and are ALLOWED to disagree.
	Objects []ObjectStatus `json:"objects"`

	// Clients is a FULL enumeration INCLUDING ZERO VALUES. Absence from this
	// list is a protocol issue, never idleness.
	//
	// §7.3 records the upstream implementation this guards against: V2bX skips
	// the whole report when no traffic moved and drops users below a threshold,
	// so "the reporter died" and "nobody used this node" became the same
	// observation. PSP cannot absorb that either — its poll short-circuits on a
	// zero delta and writes neither lifetime nor baseline.
	Clients []ClientCounters `json:"clients"`

	// Subjects carries the shadow concurrency evaluation. Empty in a build that
	// has not implemented it; that is distinguishable from all-zeros.
	Subjects []SubjectObservation `json:"subjects,omitempty"`

	// Issues are conditions the agent cannot reconcile by itself. This is
	// CONTESTED's outlet (§5, hole 4): the terminal action is not to fix it,
	// but to record a stable code and hand it to a person.
	Issues []Issue `json:"issues,omitempty"`
}

// StreamState is what the agent holds for one stream.
type StreamState struct {
	// Applied is only ever a version whose BYTES the agent actually received
	// and persisted. Never one read off an "unchanged" response.
	Applied Version `json:"applied"`
	ETag    ETag    `json:"etag"`
}

// ObjectState is how far one object got. Four states, and the two failure
// states are distinguished because they need different responses.
type ObjectState string

const (
	// ObjectApplied — in effect.
	ObjectApplied ObjectState = "applied"
	// ObjectPending — accepted, not yet in effect. Retrying may help.
	ObjectPending ObjectState = "pending"
	// ObjectRejected — the agent will not apply this content. Retrying the SAME
	// content cannot help, and PSP does not re-mint unchanged content, so
	// nothing will change on its own. This is the one state that cannot
	// self-heal, which is why it must be able to time out into an Issue.
	ObjectRejected ObjectState = "rejected"
	// ObjectBlocked — waiting on another object, named in BlockedOn. A client
	// attached to a listener the core rejected is blocked, NOT applied:
	// reporting it applied would be a green light on a client nobody can reach.
	ObjectBlocked ObjectState = "blocked"
)

// ObjectStatus is one object's convergence.
type ObjectStatus struct {
	Stream string      `json:"stream"`
	Key    string      `json:"key"`
	State  ObjectState `json:"state"`
	// SinceVersion and FirstFailedAtMS give the un-converged state a LENGTH.
	// A boolean can say "not converged" but not "for how long", and without a
	// length nothing can time out — which is ADR 0024's gate for accepting that
	// a successful write now means "intent recorded", not "the node has it".
	SinceVersion    Version `json:"since_version,omitempty"`
	FirstFailedAtMS int64   `json:"first_failed_at_ms,omitempty"`
	IssueCode       string  `json:"issue_code,omitempty"`
	BlockedOn       string  `json:"blocked_on,omitempty"`
}

// GateState is what the quota gate is doing for one client row.
type GateState string

const (
	// GateUnconfigured — no headroom known, or the counter reads below the
	// baseline. NOT the same as unlimited: PSP's own enforcement is exact while
	// it is up, and this gate is only the net for when it is not.
	GateUnconfigured GateState = "unconfigured"
	GateArmed        GateState = "armed"
	GateClosed       GateState = "closed"
)

// ClientCounters is one client row's observation. Cumulative, never a delta.
//
// §7.3: V2bX zeroes its counters BEFORE sending, so one failed POST loses that
// window permanently — with no retry and nothing anywhere knowing how much went
// missing, and it happens precisely when the panel is unreachable. Cumulative
// values replay; a lost round self-heals.
type ClientCounters struct {
	Key ClientKey `json:"key"`
	// Present distinguishes "this row is on me and idle" from "this row is not
	// on me". Both would otherwise be zero counters.
	Present   bool  `json:"present"`
	UpBytes   int64 `json:"up_bytes"`
	DownBytes int64 `json:"down_bytes"`
	// CounterEpoch changes when the local counter restarts. PSP CARRIES THE
	// BASELINE FORWARD on a change rather than reading the drop as usage — the
	// distinction between a reset and a reduction, which a bare monotonic
	// check cannot make.
	CounterEpoch uint64    `json:"counter_epoch"`
	Gate         GateState `json:"gate"`
}

// SubjectObservation is the shadow concurrency evaluation (v1 observe-only).
type SubjectObservation struct {
	Subject SubjectKey `json:"subject"`
	// IPLocalCount is distinct source IPs for this person ON THIS AGENT.
	IPLocalCount int `json:"ip_local_count"`
	// IPWouldDenySinceLastReport counts admissions this agent WOULD have
	// refused under the purely local predicate |local| > limit.
	//
	// Local on purpose: it never goes stale, and it is the one concurrency
	// predicate a node can decide from its own state plus a pushed constant
	// (ADR 0025 Q3b's "yes" side). v1 measures what enforcing it would have
	// cost, because that false-rejection rate is the only possible evidence for
	// deciding whether to arm it — and it cannot be reconstructed afterwards
	// from PSP's 120-second snapshots, since admission happens at connect time.
	IPWouldDenySinceLastReport int `json:"ip_would_deny_since_last_report"`
}

// Issue is a stable code plus context. Codes are protocol surface: renaming one
// silently breaks whatever alerts on it.
type Issue struct {
	Code   string `json:"code"`
	Key    string `json:"key,omitempty"`
	Detail string `json:"detail,omitempty"`
}

// Issue codes with a ruling attached (§8.3, §8.4).
const (
	// IssueRosterAheadOfConfig — the roster references a config version the
	// agent does not hold yet. SELF-HEALING NORMAL; only escalate if it
	// persists past K rounds.
	IssueRosterAheadOfConfig = "roster_ahead_of_config"
	// IssueAttachmentUnknownListener — versions are closed and the listener is
	// still missing. PSP's own defect; alarm on PSP, not on the node.
	IssueAttachmentUnknownListener = "attachment_unknown_listener"
	// IssueDirectivesAheadOfRoster — same ruling as roster/config.
	IssueDirectivesAheadOfRoster = "directives_ahead_of_roster"
	// IssueDirectiveUnknownClient — a directive names a client not in the
	// roster. PSP's defect.
	IssueDirectiveUnknownClient = "directive_unknown_client"
)
