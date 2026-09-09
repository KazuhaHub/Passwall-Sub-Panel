package protocol

// SyncResponse is PSP's half of the round trip.
//
// One endpoint, one round trip, both directions (§8.3). The response must be
// computed KNOWING what the agent just reported — the quota baseline is the
// counter received in this very message — so it cannot be split into two calls.
type SyncResponse struct {
	Envelope   Envelope                `json:"envelope"`
	Config     Segment[ConfigBody]     `json:"config"`
	Roster     Segment[RosterBody]     `json:"roster"`
	Directives Segment[DirectivesBody] `json:"directives"`
	// Tasks are the five CALLS from §2.1 — reality probes, core version list,
	// core install, agent upgrade, TLS material — turned into task/result pairs
	// because "when the call returns" does not exist once the node dials out.
	//
	// ADR 0025 Q0 requires this cost to be paid openly rather than assumed away:
	// turning a call into state costs interaction latency of up to one
	// heartbeat. NextPollSeconds is how PSP shortens it when work is waiting;
	// it is not a second channel.
	Tasks []Task `json:"tasks,omitempty"`
}

// Envelope carries everything that must NOT influence an ETag.
//
// This type exists to make that structural. §8.3 requires content-derived
// validators and content-idempotent minting; a timestamp inside a segment would
// change its digest every round, re-mint it every round, and cancel the
// steady-state skip that the whole conditional-fetch design is for. Freshness
// still has to be reported — so it is reported HERE, where it is outside every
// digest by construction rather than by remembering.
type Envelope struct {
	ComputedAtMS int64 `json:"computed_at_ms"`
	// NumeratorAsOfMS and NumeratorOldestReportAgeMS describe the AGGREGATE's
	// own staleness — the sum over agents is a mosaic of readings taken at
	// different instants, and the oldest one bounds how wrong it can be.
	NumeratorAsOfMS            int64 `json:"numerator_as_of_ms"`
	NumeratorOldestReportAgeMS int64 `json:"numerator_oldest_report_age_ms"`
	// OverburnHeadroomBytes is the cross-node residual, COMPUTED not asserted:
	//
	//	Σ(baseline + headroom) − Σ(latest reported counter)
	//
	// This is the honest answer to "the aggregate is a heartbeat old, so at the
	// moment of enforcement it is already wrong". The node never claims the
	// fleet-wide sum satisfies the quota; it claims only that its own row may
	// pass at most `headroom` more bytes from a stated origin. How much the
	// fleet can collectively overshoot is this number, and PSP recomputes it
	// every round rather than filling in a bound that was estimated once.
	OverburnHeadroomBytes int64 `json:"overburn_headroom_bytes"`
	// NextPollSeconds lets PSP pull the next round trip in when work is queued.
	NextPollSeconds int `json:"next_poll_seconds"`
}

// Task is a call turned into state.
type Task struct {
	ID   string `json:"id"`
	Kind string `json:"kind"`
	Args []byte `json:"args,omitempty"`
}

// TaskResult is its other half, returned on a later report.
type TaskResult struct {
	ID     string `json:"id"`
	OK     bool   `json:"ok"`
	Result []byte `json:"result,omitempty"`
	Error  string `json:"error,omitempty"`
}

// Segment evolution (§8.4): the directives segment evolves ADDITIVELY and
// unknown fields are ignored. Only a structural violation — a missing
// for_roster_version, self-contradictory coverage, an entry with no key —
// rejects the segment wholesale.
//
// The narrowing matters: under §8.3's blanket "reject the malformed segment",
// upgrading PSP before the fleet would stop service for every new user on every
// older agent. Additive evolution makes a version skew survivable in the
// direction it will actually happen.
