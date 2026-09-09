package protocol

// The three streams (§8). Two DOCUMENTS plus one DIRECTIVE stream, each with
// its own version and content ETag, all delivered in one round trip.
//
// Membership rule for which stream a value belongs to (§8.4):
//
//	A value belongs in Directives if and only if computing it requires
//	summing across agents.
//
// That is also the whole reason Directives is a separate stream: its content is
// a function of observation, so it may be re-minted every cycle, and folding it
// into Roster would destroy the reload isolation that splitting bought.
const (
	StreamConfig     = "config"
	StreamRoster     = "roster"
	StreamDirectives = "directives"
)

// Segment is one stream as delivered. Unchanged segments carry no body.
//
// Note what is ABSENT: no timestamps. Freshness lives on Envelope, because
// anything inside a segment is inside its ETag, and a per-round timestamp there
// would re-mint the segment every round and cancel the skip.
type Segment[T any] struct {
	// Unchanged is true when the agent's presented ETag already matches. Body
	// is then nil and Version is informational only — the agent must NOT record
	// it as applied, because a version it never received bytes for would be
	// PSP's own claim handed back through the same round trip.
	Unchanged bool    `json:"unchanged"`
	Version   Version `json:"version"`
	ETag      ETag    `json:"etag"`
	Body      *T      `json:"body,omitempty"`
}

// ConfigBody is the listener set: what this agent should be listening on.
// Near-static — an operator changes it occasionally.
type ConfigBody struct {
	Listeners []Listener    `json:"listeners"`
	Coverage  SegmentCounts `json:"coverage"`
}

// Listener is one inbound PSP wants served.
type Listener struct {
	Key ListenerKey `json:"key"`
	// Config is the core-agnostic listener description. It is deliberately NOT
	// modelled field-by-field yet: §9 leaves the config-generation abstraction
	// open, and inventing its shape here would freeze a decision nobody has
	// made. What §8 does fix is the ENVELOPE around it.
	Config RawConfig `json:"config"`
}

// RosterBody is the client set: who is served on this agent, with credentials.
// Hot — it changes on every enable, expiry, membership move.
type RosterBody struct {
	Clients []Client `json:"clients"`
	// MinConfigVersion is the config version this roster was read WITH, in the
	// same database transaction (§8.3). It is AUDITABLE EVIDENCE that PSP
	// published a closed pair — not a gate the agent waits on.
	//
	// Making it a gate is the mistake this field invites, so the rule is
	// written where the field is: an agent that refuses to apply a roster until
	// its config catches up cannot self-heal from a rejected config segment. A
	// re-entrant join is strictly stronger than an ordering guarantee.
	MinConfigVersion Version       `json:"min_config_version"`
	Coverage         SegmentCounts `json:"coverage"`
}

// Client is one credential-carrying row.
type Client struct {
	Key ClientKey `json:"key"`
	// Subject is the person this row belongs to. Several rows can share one.
	Subject SubjectKey `json:"subject"`
	// Listeners is the attachment set. Attachment is a FIELD, not a verb —
	// §7.4's model choice, and the reason thirteen port methods collapsed into
	// one apply.
	//
	// An empty set still materialises the client. It must never delete it:
	// deletion restarts the counters at zero, and PSP reads a counter that went
	// backwards as a reset rather than as loss.
	Listeners []ListenerKey `json:"listeners"`
	Enabled   bool          `json:"enabled"`
	// ExpiresAtMS is epoch milliseconds; 0 means never.
	ExpiresAtMS int64      `json:"expires_at_ms"`
	Credentials Credential `json:"credentials"`
}

// DirectivesBody carries the values that need a cross-agent sum.
type DirectivesBody struct {
	// ForRosterVersion is the roster these were computed against. A directives
	// stream that leads the agent's roster is SELF-HEALING NORMAL, not an
	// alarm — the same ruling §8.3 gives min_config_version.
	ForRosterVersion Version      `json:"for_roster_version"`
	Quota            []QuotaEntry `json:"quota"`
	// IPShadow is observe-only in v1. The agent computes who it WOULD have
	// denied and reports it; it denies nobody.
	IPShadow []IPShadowEntry `json:"ip_shadow"`
	Coverage SegmentCounts   `json:"coverage"`
}

// QuotaEntry ships an ABSOLUTE ORIGIN, not a pointer (§8.4, ADR 0025 Q3b).
type QuotaEntry struct {
	// Client, not Subject. Addressing and epoch tracking stay per client row.
	Client ClientKey `json:"client"`
	// BaselineBytes is the cumulative counter PSP JUST RECEIVED for this row in
	// this same round trip — not the live counter at adoption time. That is
	// what stops re-anchoring from gifting a user a fresh stretch of usage
	// every time the directive is re-minted.
	BaselineBytes int64 `json:"baseline_bytes"`
	// HeadroomBytes is TRI-STATE and the pointer is load-bearing:
	//
	//	nil → no limit configured (or unreadable). The gate does not arm.
	//	  0 → limit configured and exhausted. The gate is closed.
	//	  N → N bytes remain from BaselineBytes.
	//
	// No omitempty, deliberately: with it, 0 would serialise as absent and
	// "exhausted" would arrive as "never configured" — the exact collision
	// traffic_cap.go already has, where headroom <= 0 returns 0 and the panel
	// reads 0 as unlimited. That defect does not get carried into a new format.
	//
	// It must also NOT be computed via TrafficFloorBytes: that returns 1 both
	// for "exhausted" and for "one byte left", so it cannot express this at all.
	HeadroomBytes *int64 `json:"headroom_bytes"`
}

// IPShadowEntry is per SUBJECT — concurrency is a property of the person, not
// of one credential row.
type IPShadowEntry struct {
	Subject SubjectKey `json:"subject"`
	// IPLimit is the person's concurrent source-IP cap. 0 means unlimited,
	// matching User.IPLimit, and in v1 nothing is enforced from it either way.
	IPLimit int `json:"ip_limit"`
}

// SegmentCounts is the denominator. §8.4: every bound, threshold and divisor is
// computed from PSP's OWN rows; EntriesStale may tag and alert but must never
// enter a bound or a divisor — otherwise worse coverage would produce a
// tighter claimed error, which is the failure this repo keeps defending against
// one level up.
type SegmentCounts struct {
	Entries      int `json:"entries"`
	EntriesStale int `json:"entries_stale"`
	Subjects     int `json:"subjects,omitempty"`
}

// RawConfig is an opaque core-agnostic blob until §9 settles config generation.
type RawConfig []byte

// Credential carries whatever the protocol in use needs. Modelled loosely for
// the same reason as RawConfig.
type Credential struct {
	UUID     string `json:"uuid,omitempty"`
	Password string `json:"password,omitempty"`
}
