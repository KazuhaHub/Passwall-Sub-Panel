package alert

import (
	"context"
	"time"

	"github.com/KazuhaHub/passwall-sub-panel/internal/pkg/log"
)

// RiskFlagCounter counts the accounts with any observe-only risk signal
// flagged and written at or after since (the risk_signals store).
//
// Accounts, not rows: one account flagged on two signals is one account to
// review, and a count of rows would give the bell a number nobody can check
// against the risk tab. Flagged only — suspect stays below the line, as it
// does for the geo entry.
type RiskFlagCounter interface {
	CountFlaggedUsers(ctx context.Context, since time.Time) (int64, error)
}

// riskAlerts produces the risk signals' ONE bell entry (V3-D5): a singleton
// carrying the number of accounts with any signal flagged, the geo entries'
// shape and for their reasons (see geoAlerts) — no names in a feed every
// open admin tab polls, and a noisy signal cannot turn the bell into a list.
// One entry for the four kinds rather than one per kind: the bell's job is
// "look at the risk tab", and the tab says which signal and why.
//
// Bounded by freshness (see bellFreshness), for the reason that bound
// exists. The worker rewrites every row once per refresh, and the freshness
// is at least two refreshes, so a flag stays in the window while it is still
// being judged; what falls out is a row the worker stopped rewriting — a
// dead loop, a kind skipped for days — which is history, and the tab's
// "last computed" column is where it shows.
//
// The v2 concurrent-location flags are not counted here; geo_anomaly counts
// them. The two entries answer different questions, and folding one into
// the other would make a count that matches neither tab.
//
// A warning, admin-only (Type.AdminOnly), nil-tolerant, and a failing count
// is logged and skipped like every other source here, never blanking the
// feed.
func (s *Service) riskAlerts(ctx context.Context, freshness time.Duration) []Alert {
	if s.d.RiskFlags == nil {
		return nil
	}
	n, err := s.d.RiskFlags.CountFlaggedUsers(ctx, s.now().Add(-freshness))
	if err != nil {
		log.Warn("alert: count risk-flagged users", "err", err)
		return nil
	}
	if n <= 0 {
		return nil
	}
	return []Alert{{
		Key:      string(TypeRiskSignals),
		Type:     TypeRiskSignals,
		Severity: SeverityWarning,
		Count:    int(n),
	}}
}
