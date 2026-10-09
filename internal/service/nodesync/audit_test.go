package nodesync

import (
	"strings"
	"testing"

	protocol "github.com/KazuhaHub/passwall-protocol/protocol"
)

func TestAuditNeverRemainsInControlReportClones(t *testing.T) {
	for _, clone := range []struct {
		name string
		run  func(protocol.NodeReport) protocol.NodeReport
	}{{"full", cloneReport}, {"observation", cloneObservationReport}} {
		t.Run(clone.name, func(t *testing.T) {
			report := protocol.NodeReport{AgentID: "agt_audit", Have: emptyProtocolHave(), Audit: &protocol.AuditObservation{BatchID: strings.Repeat("a", 32), Kind: "block", Hour: 1_800_000_000_000, CollectRevision: 1, Dropped: 1}}
			out := clone.run(report)
			if out.Audit != nil {
				t.Fatal("transient audit batch retained in control cache")
			}
			if report.Audit == nil || out.AgentID != report.AgentID || len(out.Have) != 3 {
				t.Fatal("cloning changed input or removed control")
			}
		})
	}
}
