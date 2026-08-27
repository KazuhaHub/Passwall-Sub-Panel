// Package metrics is a dependency-free instrumentation layer for the hot
// paths PSP needs to reason about — the traffic poll and the 3X-UI push
// fan-out.
//
// Why not Prometheus: PSP ships as a single binary that operators run on
// a VPS, usually with no scrape infrastructure anywhere near it. Pulling
// in the client library would add a transitive dependency tree and a
// second HTTP surface to secure, to answer questions that a JSON blob
// behind the existing admin auth answers just as well. The recording API
// below is deliberately shaped like Prometheus' (counters, gauges,
// histograms with explicit bucket bounds) so that if a deployment ever
// does want a real scrape endpoint, the call sites don't move — only
// Snapshot does.
//
// Everything here is safe for concurrent use and allocation-free on the
// record path: counters and gauges are single atomic adds, a histogram
// observation is a binary search over a dozen bounds plus a few atomic
// ops. The traffic poll records a few thousand observations per cycle at
// most, so the cost is unmeasurable against a single HTTP round trip.
package metrics

import (
	"math"
	"sort"
	"sync/atomic"
	"time"
)

// atomicFloat64 is the float64 atomic the standard library omits. Stored
// as the IEEE-754 bit pattern so the CAS loops below compare exact
// representations rather than float values (NaN never reaches these
// call sites — every observation is a duration or a count).
type atomicFloat64 struct{ bits atomic.Uint64 }

func (f *atomicFloat64) Load() float64 { return math.Float64frombits(f.bits.Load()) }

func (f *atomicFloat64) Store(v float64) { f.bits.Store(math.Float64bits(v)) }

func (f *atomicFloat64) Add(v float64) {
	for {
		old := f.bits.Load()
		next := math.Float64bits(math.Float64frombits(old) + v)
		if f.bits.CompareAndSwap(old, next) {
			return
		}
	}
}

// Max raises the stored value to v if v is larger.
func (f *atomicFloat64) Max(v float64) {
	for {
		old := f.bits.Load()
		if math.Float64frombits(old) >= v {
			return
		}
		if f.bits.CompareAndSwap(old, math.Float64bits(v)) {
			return
		}
	}
}

// Counter is a monotonically increasing tally.
type Counter struct {
	name string
	help string
	v    atomic.Int64
}

// Inc adds one.
func (c *Counter) Inc() { c.v.Add(1) }

// Add adds n.
func (c *Counter) Add(n int64) { c.v.Add(n) }

// Value returns the current tally.
func (c *Counter) Value() int64 { return c.v.Load() }

func (c *Counter) reset() { c.v.Store(0) }

// Gauge is a value that moves in both directions, and additionally
// remembers the high-water mark since the last reset. The peak is the
// interesting half for the questions this package exists to answer:
// "did the push semaphore ever saturate" is a question about the maximum,
// not about whatever the depth happened to be when the snapshot was read.
type Gauge struct {
	name string
	help string
	v    atomic.Int64
	peak atomic.Int64
}

// Inc adds one and updates the peak.
func (g *Gauge) Inc() { g.Add(1) }

// Dec subtracts one.
func (g *Gauge) Dec() { g.Add(-1) }

// Add adds n (which may be negative) and updates the peak.
func (g *Gauge) Add(n int64) {
	cur := g.v.Add(n)
	for {
		old := g.peak.Load()
		if cur <= old || g.peak.CompareAndSwap(old, cur) {
			return
		}
	}
}

// Set replaces the value and updates the peak.
func (g *Gauge) Set(n int64) {
	g.v.Store(n)
	for {
		old := g.peak.Load()
		if n <= old || g.peak.CompareAndSwap(old, n) {
			return
		}
	}
}

// Value returns the current value.
func (g *Gauge) Value() int64 { return g.v.Load() }

// Peak returns the high-water mark since the last reset.
func (g *Gauge) Peak() int64 { return g.peak.Load() }

// reset rebases the peak on the live value but does NOT zero the value:
// the value tracks something still in flight (in-flight pushes, queued
// waiters), and zeroing it would make the next Dec drive it negative.
func (g *Gauge) reset() { g.peak.Store(g.v.Load()) }

// Histogram is a cumulative bucket histogram with explicit upper bounds,
// plus count/sum/max so the mean and the true maximum survive bucketing.
type Histogram struct {
	name    string
	help    string
	unit    string
	bounds  []float64
	buckets []atomic.Int64 // len(bounds)+1; the last is the +Inf overflow
	count   atomic.Int64
	sum     atomicFloat64
	max     atomicFloat64
}

// Observe records one sample.
func (h *Histogram) Observe(v float64) {
	// SearchFloat64s returns the first index with bounds[i] >= v — exactly
	// the bucket whose inclusive upper bound contains v. When v exceeds
	// every bound it returns len(bounds), the +Inf bucket.
	h.buckets[sort.SearchFloat64s(h.bounds, v)].Add(1)
	h.count.Add(1)
	h.sum.Add(v)
	h.max.Max(v)
}

// ObserveDuration records a duration in milliseconds, the unit every
// latency histogram in this package is declared in. Microsecond
// granularity is kept so sub-millisecond work (a local DB read) doesn't
// collapse into a single zero bucket.
func (h *Histogram) ObserveDuration(d time.Duration) {
	h.Observe(float64(d.Microseconds()) / 1000)
}

// ObserveSince records the time elapsed since t.
func (h *Histogram) ObserveSince(t time.Time) { h.ObserveDuration(time.Since(t)) }

// Count returns the number of samples recorded.
func (h *Histogram) Count() int64 { return h.count.Load() }

func (h *Histogram) reset() {
	for i := range h.buckets {
		h.buckets[i].Store(0)
	}
	h.count.Store(0)
	h.sum.Store(0)
	h.max.Store(0)
}

// quantile estimates the q-th quantile by linear interpolation inside the
// bucket the rank falls in. Estimation error is bounded by the bucket
// width, which is why the bucket sets below are 1-2-5 rather than powers
// of ten — a P95 that could only be reported as "somewhere between 100ms
// and 1s" would not settle any of the questions this instrumentation
// exists to answer.
//
// A sample in the +Inf bucket has no upper bound to interpolate toward, so
// the observed max stands in for it. That makes the estimate exact when
// every overflow sample sits at the max and conservative otherwise, which
// is the right direction to err for a latency tail.
func (h *Histogram) quantile(q float64) float64 {
	total := h.count.Load()
	if total == 0 {
		return 0
	}
	target := q * float64(total)
	var cum int64
	prev := 0.0
	for i := range h.buckets {
		c := h.buckets[i].Load()
		upper := h.max.Load()
		if i < len(h.bounds) {
			upper = h.bounds[i]
		}
		if c == 0 {
			prev = upper
			continue
		}
		if float64(cum+c) < target {
			cum += c
			prev = upper
			continue
		}
		if upper < prev {
			// Only reachable in the +Inf bucket when max is stale relative
			// to a concurrent Observe. Degrade to the bucket floor rather
			// than report a quantile below the preceding bound.
			return prev
		}
		return prev + (target-float64(cum))/float64(c)*(upper-prev)
	}
	return h.max.Load()
}
