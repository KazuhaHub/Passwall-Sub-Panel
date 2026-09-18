package nodehealth

import (
	"testing"
	"time"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/service/nodemetrics"
)

// The state machine's properties are what these tests defend: a spike is not an
// alert, a threshold has hysteresis, and an unreadable metric is not evidence
// that a condition cleared. The thresholds themselves are covered by the table
// at the end.

var healthBase = time.Date(2026, 9, 18, 12, 0, 0, 0, time.UTC)

func pointAt(at time.Time, derived nodemetrics.Derived) Point {
	return Point{
		Sample: domain.NodeHostMetricSample{
			SampleID: "0123456789abcdef0123456789abcdef", BootID: "boot-1",
			ReceivedAt: at, CollectedAt: at, ResourceScope: "host",
		},
		Derived: derived,
	}
}

// windowOf builds a window from a series of CPU percentages one minute apart.
func cpuWindow(percentages []*float64) Window {
	points := make([]Point, 0, len(percentages))
	for index, percentage := range percentages {
		points = append(points, pointAt(healthBase.Add(time.Duration(index)*time.Minute),
			nodemetrics.Derived{CPUPercent: percentage}))
	}
	return Window{Points: points, Now: points[len(points)-1].Sample.ReceivedAt.Add(time.Minute), EffectivePeriod: time.Minute}
}

func percent(value float64) *float64 { return &value }

func findingsByCode(findings []Finding) map[string]Finding {
	byCode := map[string]Finding{}
	for _, finding := range findings {
		byCode[finding.Code] = finding
	}
	return byCode
}

// A SINGLE SPIKE IS NOT AN ALERT. One sample over the threshold is a measurement
// artefact or a momentary burst, and alerting on it is how an operator learns to
// ignore the alert list.
func TestASingleSpikeIsNotAnAlert(t *testing.T) {
	series := make([]*float64, 0, 12)
	for index := 0; index < 12; index++ {
		// A flat 20% with one sample at 99%.
		value := 20.0
		if index == 5 {
			value = 99
		}
		series = append(series, percent(value))
	}
	findings := Evaluate(cpuWindow(series), Options{})
	if _, exists := findingsByCode(findings)[CodeCPUSaturated]; exists {
		t.Fatal("a single spike produced a CPU saturation finding")
	}
}

// A condition that HOLDS for its duration is an alert, and its duration is
// measured from when it began holding rather than from the sample that crossed
// the line.
func TestAConditionThatHoldsForItsDurationTriggers(t *testing.T) {
	series := make([]*float64, 0, 15)
	for index := 0; index < 15; index++ {
		series = append(series, percent(95))
	}
	findings := Evaluate(cpuWindow(series), Options{})
	finding, exists := findingsByCode(findings)[CodeCPUSaturated]
	if !exists {
		t.Fatal("a sustained saturation produced no finding")
	}
	if finding.Severity != SeverityWarning {
		// 95 is above the warning (90) and below the critical (97).
		t.Fatalf("severity = %q, want warning", finding.Severity)
	}
	if !finding.StartedAt.Equal(healthBase) {
		t.Fatalf("started at %s, want the first sample %s", finding.StartedAt, healthBase)
	}

	// And the same series above the critical threshold escalates.
	critical := make([]*float64, 0, 15)
	for index := 0; index < 15; index++ {
		critical = append(critical, percent(99))
	}
	finding = findingsByCode(Evaluate(cpuWindow(critical), Options{}))[CodeCPUSaturated]
	if finding.Severity != SeverityCritical {
		t.Fatalf("severity = %q, want critical", finding.Severity)
	}
	if finding.Threshold != 97 {
		t.Fatalf("threshold = %v, want the critical one", finding.Threshold)
	}
}

// HYSTERESIS IS THE WHOLE REASON THE RECOVERY IS A SEPARATE NUMBER. Without it a
// value resting on its own threshold opens and closes the alert every cycle, and
// an operator sees a notification storm from a machine that is merely busy.
func TestTheRecoveryThresholdIsLowerThanTheWarning(t *testing.T) {
	series := make([]*float64, 0, 40)
	for index := 0; index < 15; index++ {
		series = append(series, percent(95))
	}
	// Dropping to 85 is below the warning but above the recovery of 80: the
	// finding must persist.
	for index := 0; index < 25; index++ {
		series = append(series, percent(85))
	}
	if _, exists := findingsByCode(Evaluate(cpuWindow(series), Options{}))[CodeCPUSaturated]; !exists {
		t.Fatal("a value between the recovery and the warning cleared the finding")
	}

	// Dropping below the recovery, and staying there, clears it.
	for index := 0; index < 25; index++ {
		series = append(series, percent(50))
	}
	if _, exists := findingsByCode(Evaluate(cpuWindow(series), Options{}))[CodeCPUSaturated]; exists {
		t.Fatal("a sustained recovery did not clear the finding")
	}
}

// AN UNREADABLE METRIC IS NOT EVIDENCE THAT A CONDITION CLEARED. Letting a gap
// reset the state machine would make an alert disappear exactly when the node is
// least observable — the moment the operator most needs it.
func TestAGapDoesNotClearAnActiveCondition(t *testing.T) {
	series := make([]*float64, 0, 30)
	for index := 0; index < 15; index++ {
		series = append(series, percent(95))
	}
	// A long unreadable stretch, then the host is still saturated.
	for index := 0; index < 10; index++ {
		series = append(series, nil)
	}
	for index := 0; index < 5; index++ {
		series = append(series, percent(95))
	}
	finding, exists := findingsByCode(Evaluate(cpuWindow(series), Options{}))[CodeCPUSaturated]
	if !exists {
		t.Fatal("an unreadable stretch cleared the finding")
	}
	// The window is longer than the hold, so the gap could not have reset it and
	// the start time still names the original onset.
	if !finding.StartedAt.Equal(healthBase) {
		t.Fatalf("started at %s, want the original onset %s", finding.StartedAt, healthBase)
	}
}

// A metric the node does not report is a GAP, not a zero. A rule whose series is
// nil throughout produces nothing at all — which is what stops a container with
// no conntrack from alerting on a conntrack ratio of zero.
func TestAnUnreportedMetricProducesNoFinding(t *testing.T) {
	points := make([]Point, 0, 20)
	for index := 0; index < 20; index++ {
		point := pointAt(healthBase.Add(time.Duration(index)*time.Minute), nodemetrics.Derived{})
		// Conntrack is never reported, because the container cannot read it.
		points = append(points, point)
	}
	window := Window{Points: points, Now: healthBase.Add(21 * time.Minute), EffectivePeriod: time.Minute}
	for _, finding := range Evaluate(window, Options{}) {
		if finding.Code == CodeConntrackPressure || finding.Code == CodeFDPressure ||
			finding.Code == CodeMemoryPressure || finding.Code == CodeDiskSpaceLow {
			t.Fatalf("%s was reported from a metric nobody reported", finding.Code)
		}
	}
}

// The staleness rule is for nodes that CLAIMED the capability. An older node is
// unsupported rather than stale, and flagging it would put a permanent alarm on
// every node that predates the feature.
func TestStalenessAppliesOnlyToNodesThatClaimedTheCapability(t *testing.T) {
	points := []Point{
		pointAt(healthBase, nodemetrics.Derived{CPUPercent: percent(10)}),
		pointAt(healthBase.Add(time.Minute), nodemetrics.Derived{CPUPercent: percent(10)}),
	}
	// Ten minutes later with no further samples.
	window := Window{Points: points, Now: healthBase.Add(11 * time.Minute), EffectivePeriod: time.Minute}

	if _, exists := findingsByCode(Evaluate(window, Options{}))[CodeMetricStale]; exists {
		t.Fatal("a node that never claimed telemetry was reported as stale")
	}
	finding, exists := findingsByCode(Evaluate(window, Options{CapabilityObserved: true}))[CodeMetricStale]
	if !exists {
		t.Fatal("a node that claimed telemetry and went quiet was not reported as stale")
	}
	if finding.Severity != SeverityCritical {
		// Eleven minutes against a two-minute warning gap and a five-minute
		// critical gap.
		t.Fatalf("severity = %q, want critical", finding.Severity)
	}
}

// An OOM is an event rather than a condition, and a kill outranks a mere OOM.
func TestOOMPrefersAKillOverAnEvent(t *testing.T) {
	oom, kills := uint64(10), uint64(0)
	first := pointAt(healthBase, nodemetrics.Derived{})
	first.Sample.CgroupOOMEvents, first.Sample.CgroupOOMKillEvents = &oom, &kills
	secondOOM := oom + 1
	second := pointAt(healthBase.Add(time.Minute), nodemetrics.Derived{})
	second.Sample.CgroupOOMEvents, second.Sample.CgroupOOMKillEvents = &secondOOM, &kills

	window := Window{Points: []Point{first, second}, Now: healthBase.Add(2 * time.Minute), EffectivePeriod: time.Minute}
	finding, exists := findingsByCode(Evaluate(window, Options{}))[CodeCgroupOOM]
	if !exists || finding.Severity != SeverityWarning {
		t.Fatalf("a plain OOM = (%+v, %v), want a warning", finding, exists)
	}

	killed := kills + 1
	third := pointAt(healthBase.Add(2*time.Minute), nodemetrics.Derived{})
	third.Sample.CgroupOOMEvents, third.Sample.CgroupOOMKillEvents = &secondOOM, &killed
	window = Window{Points: []Point{first, second, third}, Now: healthBase.Add(3 * time.Minute), EffectivePeriod: time.Minute}
	finding, _ = findingsByCode(Evaluate(window, Options{}))[CodeCgroupOOM]
	if finding.Severity != SeverityCritical {
		t.Fatalf("an OOM kill = %q, want critical", finding.Severity)
	}
}

// The thresholds and holds are the spec's, and a table is what makes them
// auditable rather than merely present.
func TestThresholdsMatchTheSpecifiedDefaults(t *testing.T) {
	wanted := map[string]struct {
		warn, crit, recover float64
	}{
		CodeCPUSaturated:       {90, 97, 80},
		CodeCPUIOWaitHigh:      {20, 40, 10},
		CodeCPUStealHigh:       {10, 25, 5},
		CodeCPUThrottled:       {20, 50, 10},
		CodeLoadHigh:           {1.5, 3, 1},
		CodeMemoryPressure:     {10, 5, 15}, // available, so lower is worse
		CodeSwapPressure:       {50, 80, 30},
		CodeDiskSpaceLow:       {15, 5, 20},
		CodeInodeLow:           {15, 5, 20},
		CodeBandwidthSaturated: {80, 95, 60},
		CodeNetworkErrors:      {0.01, 0.05, 0.002},
		CodeTCPRetransHigh:     {5, 15, 2},
		CodeConntrackPressure:  {80, 95, 70},
		CodeFDPressure:         {80, 95, 70},
	}
	byCode := map[string]rule{}
	for _, entry := range rules {
		byCode[entry.code] = entry
	}
	for code, want := range wanted {
		entry, exists := byCode[code]
		if !exists {
			t.Fatalf("%s has no rule", code)
		}
		if entry.warning.value != want.warn || entry.critical.value != want.crit || entry.recovery.value != want.recover {
			t.Fatalf("%s thresholds = %v/%v/%v, want %v/%v/%v", code,
				entry.warning.value, entry.critical.value, entry.recovery.value, want.warn, want.crit, want.recover)
		}
	}
	// Every code the spec names has a rule, and every rule has a code the spec
	// names: a code with no rule is an alert that can never fire, and a rule for
	// an unnamed code is one nobody can document.
	named := []string{
		CodeMetricStale, CodeCPUSaturated, CodeCPUIOWaitHigh, CodeCPUStealHigh, CodeCPUThrottled,
		CodeLoadHigh, CodeMemoryPressure, CodeSwapPressure, CodeCgroupOOM, CodeDiskSpaceLow,
		CodeInodeLow, CodeFilesystemReadOnly, CodeBandwidthSaturated, CodeNetworkErrors,
		CodeTCPRetransHigh, CodeConntrackPressure, CodeFDPressure, CodeCoreRestartLoop,
		CodeSyncDegraded, CodeClockSkew, CodeCollectorSlow,
	}
	covered := map[string]bool{}
	for _, entry := range rules {
		covered[entry.code] = true
	}
	covered[CodeClockSkew] = true
	covered[CodeCgroupOOM] = true
	covered[CodeCoreRestartLoop] = true
	covered[CodeMetricStale] = true
	for _, code := range named {
		if !covered[code] {
			t.Fatalf("%s is specified but has no rule", code)
		}
	}
}
