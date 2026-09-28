package alert

import (
	"context"

	"github.com/KazuhaHub/passwall-sub-panel/internal/pkg/log"
)

// RiskQueueCounter counts the accounts the risk center lists as open AND
// flagged or held by the detector (riskcenter.Service.CountUrgent).
// Suspect-only accounts do not ring.
//
// The risk center's own number, not a tally of the stores: it applies the
// freshness window, the dismissals still in force and the trusted accounts'
// exemption exactly as the queue does, so the bell and the queue's
// 需立即处理 filter can never disagree — the day a dismissed account kept the
// bell lit, nobody would trust either.
type RiskQueueCounter interface {
	CountUrgent(ctx context.Context) (int64, error)
}

// riskQueueAlerts produces the risk center's ONE bell entry: a singleton
// carrying the number of accounts that need action now. It replaces the three
// entries the detectors used to ring on their own (latched location flags,
// the detector's own holds, flagged risk signals), which counted the same
// account up to three times and knew nothing of a dismissal.
//
// A count and no names, for the reason login_security is one: every open
// admin tab polls the feed once a minute, and the queue — where the entry
// leads, filtered to the same accounts — carries the names with the evidence
// beside each.
//
// Only what needs action NOW rings: flagged, or suspended by the detector.
// A suspect account is in the queue but is a "look when you have a minute",
// and a bell that lights for it is a bell that is always lit. That is also
// why the words are 「需立即处理」 and not the queue tab's 「待处理」: the
// number is not the size of the whole list.
//
// A warning (something to review, never an outage), admin-only
// (Type.AdminOnly), nil-tolerant, and a failing count is logged and skipped
// like every other source here, never blanking the feed. List runs it only
// for an admin (see List).
func (s *Service) riskQueueAlerts(ctx context.Context) []Alert {
	if s.d.RiskQueue == nil {
		return nil
	}
	n, err := s.d.RiskQueue.CountUrgent(ctx)
	if err != nil {
		log.Warn("alert: count accounts needing action", "err", err)
		return nil
	}
	if n <= 0 {
		return nil
	}
	return []Alert{{
		Key:      string(TypeRiskQueue),
		Type:     TypeRiskQueue,
		Severity: SeverityWarning,
		Count:    int(n),
	}}
}
