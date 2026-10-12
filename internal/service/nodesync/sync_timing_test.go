package nodesync

import (
	"encoding/json"
	"strings"
	"testing"

	protocol "github.com/KazuhaHub/passwall-protocol/protocol"
	"github.com/KazuhaHub/passwall-sub-panel/internal/pkg/metrics"
)

func syncTiming(t *testing.T) metrics.HistogramSnapshot {
	t.Helper()
	for _, histogram := range metrics.Take().Histograms {
		if histogram.Name == "psp_node_control_sync_ms" {
			return histogram
		}
	}
	t.Fatal("control timing histogram missing")
	return metrics.HistogramSnapshot{}
}

func TestControlSyncTimingCountsFailedAttemptsWithoutPrivateLabels(t *testing.T) {
	before := syncTiming(t)
	s := &Service{}
	for _, report := range []protocol.NodeReport{{}, {AgentID: "private-agent-marker", ReportedAtMS: -1}} {
		if _, err := s.Sync(t.Context(), report); err == nil {
			t.Fatal("invalid control report accepted")
		}
	}
	after := syncTiming(t)
	// Validation-only calls may be shorter than the histogram's microsecond
	// resolution. They must still contribute samples, including a zero duration.
	if after.Count != before.Count+2 || after.Sum < before.Sum || after.Unit != "ms" {
		t.Fatal("failed control attempts disappeared from actual elapsed timing")
	}
	data, err := json.Marshal(after)
	if err != nil || strings.Contains(string(data), "private-agent-marker") || strings.Contains(after.Name, "{") {
		t.Fatal("control timing exposed a peer identity")
	}
}
