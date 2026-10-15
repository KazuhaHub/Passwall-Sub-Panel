package nodesync

import (
	"time"

	protocol "github.com/KazuhaHub/passwall-protocol/protocol"
)

// Quota replies only need a known-counter bit and cumulative baseline. Full
// requests supersede the previous report, including a deliberately empty full
// enumeration. Partial requests use this call's immutable stored observation
// and its control-plane receipt time; no borrowed collections escape.
func (s *Service) quotaBaselines(agentID string, current protocol.NodeReport, now time.Time, freshnessSeconds int) (map[int64]int64, bool) {
	report := current
	if current.Partial {
		s.mu.RLock()
		received, found := s.reports[agentID]
		s.mu.RUnlock()
		if !found || fullReportStale(received.receivedAtMS, now, freshnessSeconds) {
			return nil, false
		}
		report = received.report
	}
	baselines := make(map[int64]int64, len(report.Clients))
	for _, counter := range report.Clients {
		if id, err := counter.Key.RowID(); err == nil {
			// Present=false still supplies a durable identity and counter. Keep
			// last-key-wins and saturating nonnegative sums exactly as before.
			baselines[id] = nonNegativeSum(counter.UpBytes, counter.DownBytes)
		}
	}
	return baselines, true
}
