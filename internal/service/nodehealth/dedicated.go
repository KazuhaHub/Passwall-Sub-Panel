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

// evaluateRestartLoop counts core restarts inside a rolling window.
//
// A COUNT OVER A WINDOW rather than a rate: three restarts in ten minutes is the
// condition the spec names, and expressing it as a rate would make two restarts
// in three minutes look worse than five in ten.
func evaluateRestartLoop(window Window) *Finding {
	restarts := 0.0
	var first, last time.Time
	for index := 1; index < len(window.Points); index++ {
		previous := window.Points[index-1].Sample
		current := window.Points[index].Sample
		delta := counterIncrease(previous.CoreRestartCount, current.CoreRestartCount)
		if delta == 0 {
			continue
		}
		if last.IsZero() || current.ReceivedAt.Sub(last) > restartWindow {
			// The window starts fresh: a restart hours ago is not part of a loop
			// that began a minute ago.
			restarts, first = 0, current.ReceivedAt
		}
		restarts += float64(delta)
		last = current.ReceivedAt
	}
	if last.IsZero() {
		return nil
	}
	// The recovery is a period with no restart at all, which is why it is longer
	// than the window: a core that restarts once every twenty minutes is not
	// looping, and it is also not healthy.
	if window.Now.Sub(last) >= restartRecovery {
		return nil
	}
	severity := SeverityWarning
	threshold := 3.0
	if restarts >= 5 {
		severity, threshold = SeverityCritical, 5.0
	} else if restarts < 3 {
		return nil
	}
	return &Finding{
		Code: CodeCoreRestartLoop, Severity: severity,
		StartedAt: first, LastSeenAt: last,
		CurrentValue: restarts, Threshold: threshold, Unit: "restarts",
	}
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
