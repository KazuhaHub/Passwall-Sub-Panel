package destpolicy

import (
	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"testing"
	"time"
)

func TestUsageAvailabilityRequiresCurrentProofWithoutHitRules(t *testing.T) {
	now := time.Now().UTC()
	for _, tc := range []struct {
		name   string
		change func(*domain.DestStatusPanel)
		want   bool
	}{
		{"rule free", func(*domain.DestStatusPanel) {}, true},
		{"pending", func(p *domain.DestStatusPanel) { p.Runtime.ReportedSHA256 = "old" }, false},
		{"off", func(p *domain.DestStatusPanel) { p.Collect = domain.AuditCollectOff }, false},
		{"revision changed", func(p *domain.DestStatusPanel) { p.CollectRevision++ }, false},
		{"offline", func(p *domain.DestStatusPanel) { old := now.Add(-181 * time.Second); p.Agent.LastSeen = &old }, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := statusFixture(now)
			p.Collect = domain.AuditCollectHitsAndUsage
			p.Facts.Collect = "hits_and_usage"
			p.Facts.Hits = false
			tc.change(&p)
			got, err := BuildDestinationStatus(domain.DestStatusContext{Panels: []domain.DestStatusPanel{p}}, 60, 60, now)
			if err != nil || len(got.Nodes) != 1 || got.Nodes[0].UsageCollecting != tc.want || got.Nodes[0].Collecting {
				t.Fatalf("usage proof %+v: %v", got, err)
			}
		})
	}
}
