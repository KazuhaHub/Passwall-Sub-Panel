package alert

import (
	"context"
	"time"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/pkg/log"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

// defaultPollInterval is the traffic poll's shipped cadence, what the
// settings store fills in for an unset cron_traffic_pull_minutes. The bell
// assumes it when the settings cannot be read, so an outage keeps the
// shipped floor rather than dropping it.
const defaultPollInterval = 5 * time.Minute

// bellFreshness is how long a flag nobody re-judged keeps each bell entry
// lit: risk.alert_freshness_hours (24 by default), resolved once per List
// by domain.RiskRuntimeFromSettings, and raised to two judgements of the
// entry's own source.
//
// The concurrent-location entry (geo) is floored at two traffic polls,
// because the poll re-saves every client-holding user's row each cycle it
// judges them — idle ones included, with the streak frozen — so a flagged
// account that simply stopped connecting stays inside this window, which is
// the point: idle is not innocence. What falls out is a row the detector
// stopped JUDGING (the user lost every client, or the poll itself is dead).
// A week-old latch nobody is re-evaluating is history, not something to look
// at now, and a dead poll is diagnostics' finding to report, not this one's.
// The poll interval has no ceiling, so a window shorter than two polls would
// drop a latched flag off the bell between two judgements and bring it back
// at the next.
//
// The risk entry is floored at two worker refreshes for the same reason;
// RiskRuntimeFromSettings already folds that floor into AlertFreshness.
//
// Unreadable settings are the shipped values — a 24-hour window at a
// five-minute poll — never a bell gone dark because the settings table did.
func (s *Service) bellFreshness(ctx context.Context) (geo, risk time.Duration) {
	var set ports.UISettings
	poll := defaultPollInterval
	if s.d.Settings != nil {
		if loaded, err := s.d.Settings.Load(ctx, ports.UISettings{}); err == nil {
			set = loaded
			if loaded.CronTrafficPullMinutes > 0 {
				poll = time.Duration(loaded.CronTrafficPullMinutes) * time.Minute
			}
		} else {
			log.Warn("alert: settings unreadable; the bell uses the shipped freshness", "err", err)
		}
	}
	rt := domain.RiskRuntimeFromSettings(set.RiskRuntimeSettings())
	return rt.GeoBellFreshness(poll), rt.AlertFreshness
}

// GeoFlagCounter counts accounts whose concurrent-location flag is latched
// and whose record was judged at or after since. The latch, not the last
// state: an account that went idle or unreadable while flagged is still
// flagged (the streak freezes), and counting only state=flagged would let it
// drop off the bell by disconnecting.
type GeoFlagCounter interface {
	CountFlagged(ctx context.Context, since time.Time) (int64, error)
}

// ServiceReasonCounter counts accounts whose service axis carries exactly
// reason.
type ServiceReasonCounter interface {
	CountByServiceDisabledReason(ctx context.Context, reason domain.AutoDisabledReason) (int64, error)
}

// geoAlerts produces the location detector's two bell entries, each a
// SINGLETON carrying a count — the login_security shape, not one row per
// account.
//
// One row per account would put names in a feed every open admin tab polls
// once a minute, each costing a user lookup, and would turn a bad geo
// database into a bell with forty rows. The bell's job is "look at the Geo
// tab"; the tab lists the accounts, with the evidence beside each.
//
//   - geo_anomaly: accounts whose flag is latched (GeoFlagCounter). It clears
//     when a later judgement clears the latch, or when nobody has judged the
//     row for freshness (see bellFreshness).
//   - geo_auto_suspended: accounts the detector itself has suspended
//     (geo_auto). It clears when the time box lifts them or an admin
//     resumes them. geo_anomaly the service reason is a person's decision
//     and is deliberately not counted: the bell reports what the automation
//     did, not what someone already knows because they did it.
//
// Both are warnings: a signal to review, not an outage. Both are admin-only
// (Type.AdminOnly). Either source may be nil, and a failing count is logged
// and skipped like every other source here, never blanking the feed.
func (s *Service) geoAlerts(ctx context.Context, freshness time.Duration) []Alert {
	var out []Alert
	if s.d.GeoFlags != nil {
		n, err := s.d.GeoFlags.CountFlagged(ctx, s.now().Add(-freshness))
		if err != nil {
			log.Warn("alert: count geo-flagged users", "err", err)
		} else if n > 0 {
			out = append(out, Alert{
				Key:      string(TypeGeoAnomaly),
				Type:     TypeGeoAnomaly,
				Severity: SeverityWarning,
				Count:    int(n),
			})
		}
	}
	if s.d.ServiceHolds != nil {
		n, err := s.d.ServiceHolds.CountByServiceDisabledReason(ctx, domain.DisabledGeoAutoSuspend)
		if err != nil {
			log.Warn("alert: count geo auto-suspended users", "err", err)
		} else if n > 0 {
			out = append(out, Alert{
				Key:      string(TypeGeoAutoSuspended),
				Type:     TypeGeoAutoSuspended,
				Severity: SeverityWarning,
				Count:    int(n),
			})
		}
	}
	return out
}
