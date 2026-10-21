package nodehealth

import (
	"testing"
	"time"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
)

func restartHistory(minutes []int, counts []uint64) Window {
	started := healthBase.Add(-time.Hour)
	window := Window{EffectivePeriod: time.Minute}
	for i, minute := range minutes {
		count := counts[i]
		window.Points = append(window.Points, Point{Sample: domain.NodeHostMetricSample{
			BootID: "boot", AgentRuntimeStartedAt: &started, ReceivedAt: healthBase.Add(time.Duration(minute) * time.Minute),
			CoreUnexpectedExitCount: &count,
		}})
	}
	window.Now = window.Points[len(window.Points)-1].Sample.ReceivedAt
	return window
}

func TestRestartLoopRequiresUnexpectedExitEvidence(t *testing.T) {
	window := restartHistory([]int{0, 1, 2, 3, 4, 5}, []uint64{0, 0, 0, 0, 0, 0})
	for i := range window.Points {
		total := uint64(i * 2)
		window.Points[i].Sample.CoreRestartCount = &total
	}
	if got := evaluateRestartLoop(window); got != nil {
		t.Fatalf("normal deployments alerted: %+v", got)
	}
	for i := range window.Points {
		window.Points[i].Sample.CoreUnexpectedExitCount = nil
	}
	if got := evaluateRestartLoop(window); got != nil {
		t.Fatalf("legacy total-only telemetry alerted: %+v", got)
	}
}

func TestRestartLoopUsesSlidingWindowNotChainedGaps(t *testing.T) {
	window := restartHistory([]int{0, 1, 10, 19, 28}, []uint64{0, 1, 2, 3, 4})
	if got := evaluateRestartLoop(window); got != nil {
		t.Fatalf("exits nine minutes apart accumulated: %+v", got)
	}
}

func TestRestartLoopTriggerEscalationAndRecovery(t *testing.T) {
	window := restartHistory([]int{0, 1, 2, 3}, []uint64{0, 1, 2, 3})
	finding := evaluateRestartLoop(window)
	if finding == nil || finding.Severity != SeverityWarning || finding.CurrentValue != 3 {
		t.Fatalf("three unexpected exits: %+v", finding)
	}
	window = restartHistory([]int{0, 1, 2, 3, 4, 5}, []uint64{0, 1, 2, 3, 4, 5})
	finding = evaluateRestartLoop(window)
	if finding == nil || finding.Severity != SeverityCritical || finding.CurrentValue != 5 {
		t.Fatalf("five unexpected exits: %+v", finding)
	}
	window.Now = window.Now.Add(29 * time.Minute)
	if evaluateRestartLoop(window) == nil {
		t.Fatal("cleared before recovery hold")
	}
	window.Now = window.Now.Add(time.Minute)
	if evaluateRestartLoop(window) != nil {
		t.Fatal("did not clear after thirty quiet minutes")
	}
}

func TestRestartLoopDoesNotCrossAgentEpochOrBlindInterval(t *testing.T) {
	window := restartHistory([]int{0, 1, 2}, []uint64{0, 2, 3})
	next := healthBase.Add(time.Minute)
	window.Points[2].Sample.AgentRuntimeStartedAt = &next
	if got := evaluateRestartLoop(window); got != nil {
		t.Fatalf("crossed agent lifetime: %+v", got)
	}
	window = restartHistory([]int{0, 15}, []uint64{0, 5})
	if got := evaluateRestartLoop(window); got != nil {
		t.Fatalf("invented clustered events in a blind interval: %+v", got)
	}
}
