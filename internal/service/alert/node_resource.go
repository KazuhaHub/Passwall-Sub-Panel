package alert

import (
	"context"
	"fmt"
	"time"

	"github.com/KazuhaHub/passwall-sub-panel/internal/pkg/log"
)

// NodeResourceSource reports each native server's active resource findings.
//
// IT IS AN INTERFACE RATHER THAN A DEPENDENCY so this package stays an
// aggregator: it knows how to turn findings into one alert per server, and
// nothing about how a finding is derived. The derivation is the health
// evaluator's, and the two must not be able to disagree about what a condition
// means.
type NodeResourceSource interface {
	ResourceFindings(ctx context.Context) ([]NodeResourceEntry, error)
}

// NodeResourceEntry is one server's active resource state.
type NodeResourceEntry struct {
	PanelID   int64
	PanelName string
	Findings  []NodeResourceFinding
	// Offline suppresses the threshold alerts entirely.
	//
	// A NODE THAT IS NOT REPORTING HAS ONE PROBLEM, which the connectivity badge
	// already shows. Its last metrics would otherwise keep firing every threshold
	// they happened to breach at the moment it went dark — a disk that was at 90%
	// would alert forever about a machine nobody can currently reach.
	Offline bool
}

// NodeResourceFinding is one active condition, reduced to what the bell needs.
type NodeResourceFinding struct {
	Code      string
	Critical  bool
	StartedAt time.Time
}

// nodeResource produces ONE alert per server.
//
// THE BELL IS FOR "SOMETHING NEEDS ATTENTION", NOT FOR A LIST. A server with four
// conditions is one row the operator has to deal with, and four rows for the same
// machine is how a bell badge stops meaning anything. The count says how many are
// behind it and the detail endpoint expands them.
//
// NOTHING CALLS THIS YET. The router wires Deps.NodeResource, but List does not
// include these alerts and the bell has no rendering for node_resource, so adding
// it to List is a product change rather than a lint fix.
//
//lint:ignore U1000 wired through Deps.NodeResource, deliberately not listed yet; see above.
func (s *Service) nodeResource(ctx context.Context) []Alert {
	if s.d.NodeResource == nil {
		return nil
	}
	entries, err := s.d.NodeResource.ResourceFindings(ctx)
	if err != nil {
		log.Warn("alert: node resource findings", "err", err)
		return nil
	}
	alerts := make([]Alert, 0, len(entries))
	for _, entry := range entries {
		if entry.Offline || len(entry.Findings) == 0 {
			continue
		}
		severity := SeverityWarning
		since := entry.Findings[0].StartedAt
		for _, finding := range entry.Findings {
			if finding.Critical {
				severity = SeverityError
			}
			// The EARLIEST onset, so "since" answers "how long has this machine
			// been unwell" rather than "when did the newest thing happen".
			if finding.StartedAt.Before(since) {
				since = finding.StartedAt
			}
		}
		onset := since
		alerts = append(alerts, Alert{
			Key:      fmt.Sprintf("node_resource:%d", entry.PanelID),
			Type:     TypeNodeResource,
			Severity: severity,
			TargetID: entry.PanelID, TargetName: entry.PanelName,
			Count: len(entry.Findings),
			Since: &onset,
		})
	}
	return alerts
}
