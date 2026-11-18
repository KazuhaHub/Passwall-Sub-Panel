package domain

import (
	"time"
)

// ProbeHealth is the verdict on one probe window (transit.probe.v1).
//
// A node probes the links and forwards it enters through, and reports each as
// a rolling window of attempts. PSP turns the window into one of four states:
//
//   - down: every attempt failed, and there were at least ProbeMinDownAttempts
//     of them, so a single bad sample is never "down";
//   - degraded: failures are at least 20% of the attempts;
//   - ok: otherwise;
//   - unknown: no attempt yet, or the report is stale.
//
// UNKNOWN IS NOT A VERDICT AND NEVER WITHHOLDS. Silence reads as a healthy
// line in a table of green chips, so the state exists to be shown as "no data",
// and withholding on it would take a line away from users because an agent
// went quiet, which the line's own health checks already cover.
type ProbeHealth string

const (
	ProbeOK       ProbeHealth = "ok"
	ProbeDegraded ProbeHealth = "degraded"
	ProbeDown     ProbeHealth = "down"
	ProbeUnknown  ProbeHealth = "unknown"
)

// ProbeMinDownAttempts is how many attempts a window needs before "all failed"
// means down. ProbeDegradedPercent is the failure share that means degraded.
const (
	ProbeMinDownAttempts = 3
	ProbeDegradedPercent = 20
)

// ProbeStaleIntervals is how many report intervals old a result may get before
// it is unknown.
const ProbeStaleIntervals = 3

// ClassifyProbe is the verdict of one window.
func ClassifyProbe(attempts, failures int) ProbeHealth {
	switch {
	case attempts <= 0:
		return ProbeUnknown
	case failures >= attempts && attempts >= ProbeMinDownAttempts:
		return ProbeDown
	case failures > 0 && failures*100 >= attempts*ProbeDegradedPercent:
		return ProbeDegraded
	}
	return ProbeOK
}

// ProbeState is the stored result for one probed object of one server: a link
// (key lnk_N, kinds e2e and transport) or a forward (fwd_N, kind forward). The
// primary key is (PanelID, ObjectKey, Kind); PanelID is the entry server, the
// side that probes.
type ProbeState struct {
	PanelID   int64
	ObjectKey string
	Kind      string

	// The last window the agent reported.
	Attempts      int
	Failures      int
	RTTMinMS      int
	RTTMedianMS   int
	RTTMaxMS      int
	LastError     string
	LastSuccessMS int64
	LoopbackPort  int

	// State is the verdict of that window; Consecutive how many reports in a
	// row have given it; StateSince when the run began.
	State       ProbeHealth
	Consecutive int
	StateSince  time.Time
	// Withheld is the hysteresis flag: set after the configured number of
	// consecutive down windows, cleared after the same for ok ones. It is
	// stored as set; WithheldAt says whether it counts now.
	Withheld bool

	// ReportedAt is when the panel took the window. ValidUntil is when it goes
	// stale (ProbeStaleIntervals report intervals later), fixed at ingest from
	// the cadence then in force, so a read needs no settings.
	ReportedAt time.Time
	ValidUntil time.Time
	// AgentReportedMS is the agent's own clock for the report. It is compared
	// for equality only (with the window), never ordered: an agent clock that
	// steps back or forward must not freeze the row.
	AgentReportedMS int64
	// CountedAt is when the window last advanced the run. A window counts when
	// it differs from the stored one, or when a probe interval has passed since
	// it last counted (a link that stays down gives the same window every time).
	CountedAt time.Time
}

// SameWindow reports whether two rows hold the same measurement: what the
// agent measured, not when the panel took it.
func (p ProbeState) SameWindow(o ProbeState) bool {
	return p.Attempts == o.Attempts && p.Failures == o.Failures &&
		p.RTTMinMS == o.RTTMinMS && p.RTTMedianMS == o.RTTMedianMS && p.RTTMaxMS == o.RTTMaxMS &&
		p.LastError == o.LastError && p.LastSuccessMS == o.LastSuccessMS
}

// StaleAt reports whether the window is too old to say anything.
func (p ProbeState) StaleAt(now time.Time) bool { return !now.Before(p.ValidUntil) }

// HealthAt is the state to show: unknown when stale.
func (p ProbeState) HealthAt(now time.Time) ProbeHealth {
	if p.StaleAt(now) {
		return ProbeUnknown
	}
	return p.State
}

// WithheldAt reports whether this row withholds its object now. A stale or
// unknown row never does, whatever flag it carries.
func (p ProbeState) WithheldAt(now time.Time) bool {
	return p.Withheld && p.HealthAt(now) != ProbeUnknown
}

// LossPercent is the failed share of the window, rounded to the nearest whole
// percent. 0 for an empty window.
func (p ProbeState) LossPercent() int {
	if p.Attempts <= 0 {
		return 0
	}
	return (p.Failures*100 + p.Attempts/2) / p.Attempts
}

// ProbeQuality is the summary a node or a line carries: what an administrator's
// chip shows. Loaded state, never stored.
type ProbeQuality struct {
	State       ProbeHealth
	MedianRTTMS int
	LossPercent int
	Withheld    bool
	// Key is the probed object; Kind is the result the numbers come from.
	Key  string
	Kind string
	// ReportedAt is when the panel took the window (zero when never).
	ReportedAt time.Time
}

// Probe kinds, as the protocol names them.
const (
	ProbeKindE2E       = "e2e"
	ProbeKindTransport = "transport"
	ProbeKindForward   = "forward"
)

var probeSeverity = map[ProbeHealth]int{ProbeUnknown: 0, ProbeOK: 1, ProbeDegraded: 2, ProbeDown: 3}

// QualityOf summarises the results of one object. The numbers come from the
// e2e result of a link when there is one (it is what a user's traffic sees),
// else from the first result; the state is the worst of the results that have
// one, and the object is withheld when any of its results withholds it.
func QualityOf(rows []ProbeState, now time.Time) ProbeQuality {
	if len(rows) == 0 {
		return ProbeQuality{State: ProbeUnknown}
	}
	primary := rows[0]
	for _, row := range rows {
		if row.Kind == ProbeKindE2E {
			primary = row
			break
		}
	}
	quality := ProbeQuality{
		State: ProbeUnknown, Key: primary.ObjectKey, Kind: primary.Kind,
		MedianRTTMS: primary.RTTMedianMS, LossPercent: primary.LossPercent(), ReportedAt: primary.ReportedAt,
	}
	if primary.StaleAt(now) {
		quality.MedianRTTMS, quality.LossPercent = 0, 0
	}
	for _, row := range rows {
		if health := row.HealthAt(now); probeSeverity[health] > probeSeverity[quality.State] {
			quality.State = health
		}
		if row.WithheldAt(now) {
			quality.Withheld = true
		}
	}
	return quality
}

// ProbeHysteresis decides what one new window does to a stored row.
type ProbeHysteresis struct {
	// Withhold turns the flag on at all. Off, the flag is left exactly as it
	// was: this never sets it and never clears it.
	Withhold  bool
	DownAfter int
	OKAfter   int
}

// Advance returns the row after one more window. prev is nil for an object
// seen for the first time. The caller fills the window fields; Advance owns
// State, Consecutive, StateSince and Withheld.
//
// THE FLAG IS DECIDED FROM FRESH WINDOWS ONLY. A previous row that has gone
// stale is forgotten (its run and its flag), so an agent coming back after a
// gap starts from nothing instead of resuming a verdict from before it left; an
// unknown window clears the flag for the same reason; and with withholding off
// the flag is cleared, not left, so no race with the settings write can leave a
// line withheld.
func (h ProbeHysteresis) Advance(prev *ProbeState, window ProbeHealth, now time.Time) (state ProbeHealth, consecutive int, since time.Time, withheld bool) {
	state, consecutive, since = window, 1, now
	if prev != nil && !prev.ValidUntil.IsZero() && !now.Before(prev.ValidUntil) {
		prev = nil
	}
	if prev != nil {
		withheld = prev.Withheld
		if prev.State == window {
			consecutive, since = prev.Consecutive+1, prev.StateSince
			if consecutive < 0 || consecutive > 1<<30 {
				consecutive = 1 << 30
			}
		}
	}
	if !h.Withhold {
		return state, consecutive, since, false
	}
	switch {
	case window == ProbeDown && consecutive >= h.DownAfter:
		withheld = true
	case window == ProbeOK && consecutive >= h.OKAfter:
		withheld = false
	case window == ProbeUnknown:
		withheld = false
	}
	return
}
