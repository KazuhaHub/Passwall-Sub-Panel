// Package nodehealth derives resource-health findings from stored node host
// telemetry.
//
// IT IS A PURE FUNCTION OVER A WINDOW OF HISTORY. Nothing here queries a
// repository, reads a clock or holds state between calls: the evaluator replays
// the trigger and recovery conditions from the samples themselves, so a panel
// restart neither loses an active condition nor re-opens one that had already
// recovered. That is the property that makes "the panel's health view" and "the
// panel's alerts" the same answer rather than two that drift.
package nodehealth

import (
	"time"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/service/nodemetrics"
)

type Severity string

const (
	SeverityWarning  Severity = "warning"
	SeverityCritical Severity = "critical"
)

// Finding is one active resource condition.
type Finding struct {
	Code     string
	Severity Severity
	// StartedAt is when the condition began HOLDING, not when the sample that
	// first crossed the threshold arrived: a condition reported as having started
	// at the first spike would claim a duration it had not yet earned.
	StartedAt  time.Time
	LastSeenAt time.Time
	// CurrentValue and Threshold are what the operator needs to judge it. They
	// are the raw comparison, not a percentage of it.
	CurrentValue float64
	Threshold    float64
	Unit         string
	// InterfaceName scopes an interface-specific finding. Empty for host-wide
	// ones, and part of the identity when present.
	InterfaceName string
}

// Point is one sample of the window with its own derived values.
//
// The derivation is supplied rather than computed here so the evaluator cannot
// disagree with the charts about what a sample means — both read the same
// numbers from the same function.
type Point struct {
	Sample  domain.NodeHostMetricSample
	Derived nodemetrics.Derived
}

// Window is the history one evaluation sees.
type Window struct {
	// Points is ordered oldest first, and every point after the first has the one
	// before it as its predecessor — so a gap is a point whose Derived values are
	// nil rather than a missing entry.
	Points []Point
	Now    time.Time
	// EffectivePeriod is the ACTUAL telemetry cadence, which is not necessarily
	// what was requested: an interval that falls between polls takes effect on
	// the first poll at or after it, and a freshness threshold computed against
	// the requested value would mark a healthy node stale every cycle.
	EffectivePeriod time.Duration
}

// warmingUpSamples is how many samples a rule needs before its verdict is a
// verdict rather than an absence of evidence.
const warmingUpSamples = 2

// Options carry what the evaluator cannot see in the samples.
type Options struct {
	// CapabilityObserved says this agent's most recent report advertised host
	// telemetry.
	//
	// IT GATES THE STALENESS RULE AND NOTHING ELSE. A node that never claimed the
	// capability is UNSUPPORTED rather than stale, and reporting it as stale would
	// put a permanent alarm on every node that predates the feature — which is
	// how a real signal gets ignored.
	CapabilityObserved bool
}

// Evaluate replays the window and reports the conditions still active at its end.
func Evaluate(window Window, options Options) []Finding {
	if len(window.Points) == 0 {
		return nil
	}
	var findings []Finding
	for index := range rules {
		rule := &rules[index]
		if finding := rule.evaluate(window); finding != nil {
			findings = append(findings, *finding)
		}
	}
	for _, extra := range []*Finding{
		clockSkewRule.evaluate(window),
		evaluateOOM(window),
		evaluateRestartLoop(window),
		evaluateMetricStale(window, options.CapabilityObserved),
	} {
		if extra != nil {
			findings = append(findings, *extra)
		}
	}
	return findings
}

// WarmingUp reports whether the window is too short to judge the longest
// condition this evaluator knows about.
//
// A SHORT WINDOW IS NOT A CLEAN BILL OF HEALTH. Reporting "healthy" from ten
// minutes of history would be a claim about a window the evaluator never saw, and
// the honest answer — "not enough history yet" — is a state the caller renders
// rather than a failure it hides.
func WarmingUp(window Window) bool {
	return len(window.Points) < warmingUpSamples
}

// condition is one side of a rule's threshold.
type condition struct {
	value float64
	// hold is how long the condition must persist. A duration is the ordinary
	// case; samples covers the rules the spec states as a sample count, which is
	// a deliberate difference and not a unit conversion to make silently.
	hold    time.Duration
	samples int
}

// rule is one alert code with its trigger and recovery conditions.
type rule struct {
	code string
	unit string
	// value extracts the series this rule watches. Nil means the sample cannot
	// answer for this rule, which is a gap rather than a zero — the distinction
	// the whole feature is built on.
	value func(Point) *float64
	// lowerIsWorse inverts the comparisons: for memory available, a small number
	// is the problem.
	lowerIsWorse bool
	warning      condition
	critical     condition
	recovery     condition
	// requireGrowth adds a direction to the warning threshold: the condition is
	// the threshold AND a rise across the hold, for the cases the spec states as
	// "and growing".
	requireGrowth bool
}

// evaluate walks the window and returns the condition still holding at its end.
//
// THE STATE MACHINE IS REPLAYED RATHER THAN SAMPLED. A single spike is not an
// alert, and neither is a value that crossed a threshold once: the condition has
// to hold for its duration, and the recovery has to hold for its own before the
// finding clears. Both halves are re-derived from the same history, so the answer
// does not depend on how often anyone looked.
func (r *rule) evaluate(window Window) *Finding {
	var (
		active        bool
		startedAt     time.Time
		warningSince  time.Time
		criticalSince time.Time
		recoverySince time.Time
		lastSeen      time.Time
		currentValue  *float64
	)
	for index := range window.Points {
		point := window.Points[index]
		value := r.value(point)
		if value == nil {
			// A SAMPLE THAT CANNOT ANSWER DOES NOT RESET THE STATE MACHINE. An
			// unreadable metric is not evidence that a condition cleared, and
			// letting it clear one would make an alert disappear exactly when the
			// node is least observable.
			continue
		}
		currentValue = value
		lastSeen = point.Sample.ReceivedAt

		warning := r.breaches(*value, r.warning)
		critical := r.breaches(*value, r.critical)
		recovered := r.recovers(*value)

		if active {
			if recovered {
				if recoverySince.IsZero() {
					recoverySince = point.Sample.ReceivedAt
				}
				if r.holdSatisfied(recoverySince, point.Sample.ReceivedAt, r.recovery, window) {
					active = false
					warningSince, criticalSince, recoverySince = time.Time{}, time.Time{}, time.Time{}
				}
			} else {
				recoverySince = time.Time{}
			}
			// A condition that escalates while already active is still the same
			// finding; only its severity moves.
			if warning {
				if warningSince.IsZero() {
					warningSince = point.Sample.ReceivedAt
				}
			} else {
				warningSince = time.Time{}
			}
			if critical {
				if criticalSince.IsZero() {
					criticalSince = point.Sample.ReceivedAt
				}
			} else {
				criticalSince = time.Time{}
			}
			continue
		}

		// Not yet active: each level must hold for its own duration before the
		// finding opens at that level.
		if warning {
			if warningSince.IsZero() {
				warningSince = point.Sample.ReceivedAt
			}
		} else {
			warningSince = time.Time{}
		}
		if critical {
			if criticalSince.IsZero() {
				criticalSince = point.Sample.ReceivedAt
			}
		} else {
			criticalSince = time.Time{}
		}
		if !warningSince.IsZero() && r.holdSatisfied(warningSince, point.Sample.ReceivedAt, r.warning, window) &&
			r.growthSatisfied(window, warningSince, currentValue) {
			active = true
			startedAt = warningSince
		}
		if !criticalSince.IsZero() && r.holdSatisfied(criticalSince, point.Sample.ReceivedAt, r.critical, window) {
			active = true
			startedAt = criticalSince
		}
	}
	if !active || currentValue == nil {
		return nil
	}
	severity := SeverityWarning
	if !criticalSince.IsZero() && r.holdSatisfied(criticalSince, window.Now, r.critical, window) {
		severity = SeverityCritical
	}
	return &Finding{
		Code: r.code, Severity: severity, StartedAt: startedAt, LastSeenAt: lastSeen,
		CurrentValue: *currentValue, Threshold: r.thresholdFor(severity), Unit: r.unit,
	}
}

// growthSatisfied reports whether a value rose across the hold window.
//
// It is what "and growing" means: the threshold alone cannot tell a busy swap
// file that has been at half its capacity for a month from one that reached half
// an hour ago and is still climbing.
func (r *rule) growthSatisfied(window Window, since time.Time, latest *float64) bool {
	if !r.requireGrowth || latest == nil {
		return true
	}
	earliest := -1.0
	for index := range window.Points {
		point := window.Points[index]
		if point.Sample.ReceivedAt.Before(since) {
			continue
		}
		value := r.value(point)
		if value == nil {
			continue
		}
		if earliest < 0 {
			earliest = *value
		}
	}
	return earliest >= 0 && *latest > earliest
}

func (r *rule) thresholdFor(severity Severity) float64 {
	if severity == SeverityCritical {
		return r.critical.value
	}
	return r.warning.value
}

// breaches reports whether a value is on the wrong side of a threshold.
func (r *rule) breaches(value float64, threshold condition) bool {
	if r.lowerIsWorse {
		return value <= threshold.value
	}
	return value >= threshold.value
}

func (r *rule) recovers(value float64) bool {
	if r.lowerIsWorse {
		return value >= r.recovery.value
	}
	return value <= r.recovery.value
}

// holdSatisfied reports whether a condition that began at `since` has held long
// enough by `at`.
func (r *rule) holdSatisfied(since, at time.Time, threshold condition, window Window) bool {
	if threshold.samples > 0 {
		// A sample-counted hold is converted at the ACTUAL cadence, because that
		// is what the spec's "N samples" means: an interval denominated in a
		// cadence the node does not achieve would be a different duration.
		required := time.Duration(threshold.samples) * window.EffectivePeriod
		return at.Sub(since) >= required
	}
	if threshold.hold <= 0 {
		return true
	}
	return at.Sub(since) >= threshold.hold
}
