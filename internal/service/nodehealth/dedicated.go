package nodehealth

import (
	"time"
)

// The four rules that are not a threshold on a single series.
//
// They are separate because their SHAPE differs rather than their thresholds: one
// counts events in a window, one measures a magnitude, one is about the absence
// of samples rather than their values, and one fires on an edge. Forcing them
// into the threshold table would have meant inventing fields nothing else uses.

// evaluateOOM reports an out-of-memory event.
//
// IT IS AN EDGE, NOT A LEVEL. An OOM is not a condition that holds; it is
// something that happened, and §10.4 gives it a bounded life: derived from
// counter deltas inside the caller's window, it disappears when the window
// passes rather than needing anything to clear it.
func evaluateOOM(window Window) *Finding {
	var finding *Finding
	for index := 1; index < len(window.Points); index++ {
		previous := window.Points[index-1].Sample
		current := window.Points[index].Sample
		severity := Severity("")
		value := 0.0
		threshold := 0.0
		if delta := counterIncrease(previous.CgroupOOMKillEvents, current.CgroupOOMKillEvents); delta > 0 {
			severity, value, threshold = SeverityCritical, float64(delta), 0
		} else if delta := counterIncrease(previous.CgroupOOMEvents, current.CgroupOOMEvents); delta > 0 {
			severity, value, threshold = SeverityWarning, float64(delta), 0
		}
		if severity == "" {
			continue
		}
		// A kill outranks a mere OOM within the same window, so the newest and
		// most severe event is what the operator sees.
		finding = &Finding{
			Code: CodeCgroupOOM, Severity: severity,
			StartedAt: current.ReceivedAt, LastSeenAt: current.ReceivedAt,
			CurrentValue: value, Threshold: threshold, Unit: "events",
		}
	}
	return finding
}

// counterIncrease is the increase between two optional counters, or zero when
// either is absent or the counter went backwards.
func counterIncrease(previous, current *uint64) uint64 {
	if previous == nil || current == nil || *current < *previous {
		return 0
	}
	return *current - *previous
}

// restartWindow is the period a restart loop is counted over, and restartRecovery
// is how long the loop must stay quiet before the finding clears.
const (
	restartWindow   = 10 * time.Minute
	restartRecovery = 30 * time.Minute
)

// evaluateRestartLoop requires explicit unexpected-exit telemetry. Total starts
// include normal deployments and are never evidence of a failure loop. Events
// are counted in a sliding ten-minute window, not a chain of ten-minute gaps.
func evaluateRestartLoop(window Window) *Finding {
	type event struct {
		at    time.Time
		count uint64
	}
	var events []event
	var finding *Finding
	var last time.Time
	for index := 1; index < len(window.Points); index++ {
		previous := window.Points[index-1].Sample
		current := window.Points[index].Sample
		// A new agent lifetime or missing counter is a new baseline, not a delta
		// against the previous process. This also prevents old total-only samples
		// from being interpreted as unexpected-exit history during an upgrade.
		if previous.CoreUnexpectedExitCount == nil || current.CoreUnexpectedExitCount == nil ||
			previous.AgentRuntimeStartedAt == nil || current.AgentRuntimeStartedAt == nil ||
			!previous.AgentRuntimeStartedAt.Equal(*current.AgentRuntimeStartedAt) ||
			previous.BootID != current.BootID || *current.CoreUnexpectedExitCount < *previous.CoreUnexpectedExitCount {
			events, finding, last = nil, nil, time.Time{}
			continue
		}
		at := current.ReceivedAt
		if !last.IsZero() && at.Sub(last) >= restartRecovery {
			finding = nil
		}
		delta := counterIncrease(previous.CoreUnexpectedExitCount, current.CoreUnexpectedExitCount)
		if delta == 0 {
			continue
		}
		// A long blind interval cannot establish that its exits clustered within
		// ten minutes. Do not manufacture event times from an aggregate counter.
		if at.Sub(previous.ReceivedAt) > restartWindow {
			events = nil
			continue
		}
		last = at
		for len(events) > 0 && events[0].at.Before(at.Add(-restartWindow)) {
			events = events[1:]
		}
		events = append(events, event{at, delta})
		var count uint64
		for _, event := range events {
			count += event.count
		}
		if count < 3 {
			continue
		}
		severity, threshold := SeverityWarning, 3.0
		if count >= 5 {
			severity, threshold = SeverityCritical, 5
		}
		if finding == nil {
			finding = &Finding{Code: CodeCoreRestartLoop, StartedAt: events[0].at,
				WindowSeconds: int(restartWindow.Seconds()), RecoverySeconds: int(restartRecovery.Seconds()), Unit: "events"}
		}
		if float64(count) >= finding.CurrentValue {
			finding.Severity, finding.Threshold, finding.CurrentValue = severity, threshold, float64(count)
		}
	}
	if finding == nil || window.Now.Sub(last) >= restartRecovery {
		return nil
	}
	finding.LastSeenAt = last
	return finding
}

// clockSkewRule watches the magnitude of the node's clock error.
//
// THE SIGN IS DISCARDED and the magnitude is what matters: a node running five
// minutes fast and one running five minutes slow both misplace a scheduled quota
// grant by five minutes. The comparison is the node's own collection instant
// against the panel's receipt of it, which includes the transit time — acceptable,
// because minutes of transit is itself a fault worth seeing.
var clockSkewRule = rule{
	code: CodeClockSkew, unit: "seconds",
	value: func(p Point) *float64 {
		seconds := p.Sample.CollectedAt.Sub(p.Sample.ReceivedAt).Seconds()
		if seconds < 0 {
			seconds = -seconds
		}
		return &seconds
	},
	warning:  condition{value: 120, samples: 3},
	critical: condition{value: 600, samples: 2},
	recovery: condition{value: 60, samples: 3},
}

// staleThresholds are multiples of the effective telemetry period.
const (
	staleWarningPeriods  = 2
	staleCriticalPeriods = 5
	staleRecoverySamples = 2
)

// recentlyFresh reports whether the newest `count` gaps are all inside the
// freshness bound.
//
// THE RECOVERY IS "CONSECUTIVE FRESH SAMPLES" AND NOT "ONE FRESH ONE", which is
// why it is checked rather than assumed. A node that reported once and went quiet
// again is not recovered, and clearing the alert on the single sample would make
// an intermittent node look healthy exactly when it is flapping.
func recentlyFresh(window Window, count int, bound time.Duration) bool {
	points := window.Points
	if len(points) < count {
		return false
	}
	for index := len(points) - count; index < len(points)-1; index++ {
		if points[index+1].Sample.ReceivedAt.Sub(points[index].Sample.ReceivedAt) > bound {
			return false
		}
	}
	return true
}

// evaluateMetricStale reports that telemetry stopped arriving.
//
// IT IS ABOUT THE ABSENCE OF SAMPLES, so it cannot be a rule over their values:
// the condition is that the newest sample is old, and no sample says that.
//
// The caller must not evaluate this for a node that has never advertised the
// capability. An older node is unsupported rather than stale, and reporting it
// as stale would put a permanent alarm on every node that predates the feature.
func evaluateMetricStale(window Window, capabilityObserved bool) *Finding {
	if !capabilityObserved || window.EffectivePeriod <= 0 || len(window.Points) == 0 {
		return nil
	}
	newest := window.Points[len(window.Points)-1].Sample.ReceivedAt
	age := window.Now.Sub(newest)
	warningGap := staleWarningPeriods * window.EffectivePeriod
	criticalGap := staleCriticalPeriods * window.EffectivePeriod
	severity := Severity("")
	threshold := 0.0
	switch {
	case age > criticalGap:
		severity, threshold = SeverityCritical, criticalGap.Seconds()
	case age > warningGap:
		severity, threshold = SeverityWarning, warningGap.Seconds()
	case !recentlyFresh(window, staleRecoverySamples, warningGap):
		// Neither stale by the threshold nor demonstrably fresh: the window does
		// not yet contain the samples that would clear it, so it stays reported.
		severity, threshold = SeverityWarning, warningGap.Seconds()
	default:
		return nil
	}
	lastSeen := newest
	return &Finding{
		Code: CodeMetricStale, Severity: severity,
		StartedAt: lastSeen.Add(warningGap), LastSeenAt: lastSeen,
		CurrentValue: age.Seconds(), Threshold: threshold, Unit: "seconds",
	}
}
