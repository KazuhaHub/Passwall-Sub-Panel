package ports

import "github.com/KazuhaHub/passwall-sub-panel/internal/domain"

// The row shapes of the risk center's evidence-free reads. The queue, the
// bell and the Users page's levels all ask "which accounts are at attention,
// at what level, since when" for the whole fleet on every request; the
// verdicts' evidence is by far their widest column and none of those reads
// renders it, so the stores answer with these narrow rows and the evidence
// is loaded for one page of accounts at most.

// ServiceHold is one account whose service axis carries a given reason, with
// the time the hold was written (users.service_disabled_at in unix ms; 0 when
// the column is NULL). The reopen rule compares it with the hold time a
// dismissal accepted, so it must be exact to the millisecond.
type ServiceHold struct {
	UserID  int64
	SinceMS int64
}

// GeoAttentionRow is one geo_streaks row at attention, reduced to what
// domain.GeoAttentionOf reads — the latch and the ramp — and when the poll
// last judged it (updated_at, unix ms): the freshness bound and the verdict
// time a dismissal snapshots.
type GeoAttentionRow struct {
	UserID      int64
	Flagged     bool
	Over        int
	UpdatedAtMS int64
}

// SignalAttentionRow is one risk_signals row at attention (suspect or
// flagged), without its evidence, and when the worker last wrote it.
type SignalAttentionRow struct {
	UserID      int64
	Kind        domain.RiskKind
	State       domain.GeoState
	UpdatedAtMS int64
}
