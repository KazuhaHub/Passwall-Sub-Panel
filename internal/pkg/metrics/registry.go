package metrics

import (
	"sort"
	"sync"
	"sync/atomic"
	"time"
)

// Bucket sets. Both are 1-2-5 series so every decade gets three
// resolution points — enough that an interpolated P95 lands within a
// factor of ~1.6 of the truth anywhere in range.
var (
	// LatencyBucketsMS spans a fast local DB read to a panel that has
	// effectively hung (10s). A 3X-UI round trip over the public internet
	// lands in the 50–500ms region, which gets six buckets — the resolution
	// the RTT question actually needs.
	//
	// The three sub-millisecond bounds are not padding. The poll's own stage
	// timings are microsecond-scale on a small install, and with a single
	// [0, 0.5ms] floor bucket every one of them reported the same
	// indistinguishable midpoint — the breakdown could not tell a 3-microsecond
	// prefetch from a 400-microsecond one, which is exactly the comparison the
	// stage histogram exists to support.
	LatencyBucketsMS = []float64{
		0.05, 0.1, 0.25,
		0.5, 1, 2, 5, 10, 20, 50, 100, 200, 500,
		1000, 2000, 5000, 10000,
	}

	// CountBuckets spans per-cycle tallies: how many users moved bytes,
	// how many clients one user owns. Dense at the low end because the P
	// question ("is it 1, 2, or 3 clients per user") is decided there.
	CountBuckets = []float64{
		0, 1, 2, 3, 5, 8, 12, 20, 35, 60, 100,
		200, 500, 1000, 2000, 5000, 10000,
	}
)

// registry holds every metric declared in this package. Declaration
// happens in package-level var blocks at init, so the mutex is only
// contended during startup; the record path never touches it.
type registry struct {
	mu         sync.Mutex
	counters   []*Counter
	gauges     []*Gauge
	histograms []*Histogram
	since      time.Time
}

var reg = &registry{since: time.Now()}

// NewCounter declares a counter. Call from a package-level var.
func NewCounter(name, help string) *Counter {
	c := &Counter{name: name, help: help}
	reg.mu.Lock()
	defer reg.mu.Unlock()
	reg.counters = append(reg.counters, c)
	return c
}

// NewGauge declares a gauge. Call from a package-level var.
func NewGauge(name, help string) *Gauge {
	g := &Gauge{name: name, help: help}
	reg.mu.Lock()
	defer reg.mu.Unlock()
	reg.gauges = append(reg.gauges, g)
	return g
}

// NewHistogram declares a histogram over the given inclusive upper
// bounds, which must be sorted ascending. Call from a package-level var.
func NewHistogram(name, help, unit string, bounds []float64) *Histogram {
	// Copy: the caller passes one of the shared bucket vars above, and a
	// histogram must not be able to mutate the set every other histogram
	// is reading. Sorting defensively keeps SearchFloat64s correct even
	// if a future call site hand-writes an unsorted set.
	b := append([]float64(nil), bounds...)
	sort.Float64s(b)
	h := &Histogram{
		name:   name,
		help:   help,
		unit:   unit,
		bounds: b,
		// One slot per bound plus the +Inf overflow. atomic.Int64 carries
		// a noCopy, so the slice is allocated at its final length here and
		// never reallocated — every Observe indexes into it in place.
		buckets: make([]atomic.Int64, len(b)+1),
	}
	reg.mu.Lock()
	defer reg.mu.Unlock()
	reg.histograms = append(reg.histograms, h)
	return h
}

// Reset zeroes every counter and histogram and rebases every gauge peak,
// so an operator can start a clean measurement window ("reset, wait an
// hour, snapshot") instead of differencing two absolute readings by hand.
func Reset() {
	reg.mu.Lock()
	defer reg.mu.Unlock()
	for _, c := range reg.counters {
		c.reset()
	}
	for _, g := range reg.gauges {
		g.reset()
	}
	for _, h := range reg.histograms {
		h.reset()
	}
	reg.since = time.Now()
}

// CounterSnapshot is one counter's rendered state.
type CounterSnapshot struct {
	Name  string `json:"name"`
	Help  string `json:"help"`
	Value int64  `json:"value"`
}

// GaugeSnapshot is one gauge's rendered state.
type GaugeSnapshot struct {
	Name  string `json:"name"`
	Help  string `json:"help"`
	Value int64  `json:"value"`
	Peak  int64  `json:"peak"`
}

// HistogramSnapshot is one histogram's rendered state. Buckets are
// cumulative ("count of samples <= le"), matching Prometheus convention,
// so a consumer can difference adjacent entries to get the raw counts but
// can also read a CDF straight off the list.
type HistogramSnapshot struct {
	Name    string           `json:"name"`
	Help    string           `json:"help"`
	Unit    string           `json:"unit"`
	Count   int64            `json:"count"`
	Sum     float64          `json:"sum"`
	Mean    float64          `json:"mean"`
	Max     float64          `json:"max"`
	P50     float64          `json:"p50"`
	P90     float64          `json:"p90"`
	P95     float64          `json:"p95"`
	P99     float64          `json:"p99"`
	Buckets []BucketSnapshot `json:"buckets"`
}

// BucketSnapshot is one cumulative bucket. LE is the inclusive upper
// bound; the final bucket reports LE 0 with Inf true.
type BucketSnapshot struct {
	LE            float64 `json:"le"`
	Inf           bool    `json:"inf,omitempty"`
	CumulativeSum int64   `json:"count"`
}

// Snapshot is the whole registry rendered for the diagnostics endpoint.
type Snapshot struct {
	// SinceUnixMS is when the window opened (process start, or the last
	// Reset). Every counter below is "since that moment", so a consumer
	// can turn a tally into a rate without a second reading.
	SinceUnixMS int64               `json:"since_unix_ms"`
	WindowMS    int64               `json:"window_ms"`
	Counters    []CounterSnapshot   `json:"counters"`
	Gauges      []GaugeSnapshot     `json:"gauges"`
	Histograms  []HistogramSnapshot `json:"histograms"`
}

// Take renders the current state of every declared metric.
//
// Not atomic across metrics: a poll cycle running concurrently can be
// half-counted, so two related tallies may disagree by one cycle's worth.
// Making it atomic would mean a lock on every record, which is a real
// cost on the hot path to buy consistency that a measurement window of
// hours does not need.
func Take() Snapshot {
	reg.mu.Lock()
	defer reg.mu.Unlock()
	s := Snapshot{
		SinceUnixMS: reg.since.UnixMilli(),
		WindowMS:    time.Since(reg.since).Milliseconds(),
		Counters:    make([]CounterSnapshot, 0, len(reg.counters)),
		Gauges:      make([]GaugeSnapshot, 0, len(reg.gauges)),
		Histograms:  make([]HistogramSnapshot, 0, len(reg.histograms)),
	}
	for _, c := range reg.counters {
		s.Counters = append(s.Counters, CounterSnapshot{Name: c.name, Help: c.help, Value: c.Value()})
	}
	for _, g := range reg.gauges {
		s.Gauges = append(s.Gauges, GaugeSnapshot{Name: g.name, Help: g.help, Value: g.Value(), Peak: g.Peak()})
	}
	for _, h := range reg.histograms {
		count := h.count.Load()
		hs := HistogramSnapshot{
			Name: h.name, Help: h.help, Unit: h.unit,
			Count:   count,
			Sum:     h.sum.Load(),
			Max:     h.max.Load(),
			P50:     h.quantile(0.50),
			P90:     h.quantile(0.90),
			P95:     h.quantile(0.95),
			P99:     h.quantile(0.99),
			Buckets: make([]BucketSnapshot, 0, len(h.buckets)),
		}
		if count > 0 {
			hs.Mean = hs.Sum / float64(count)
		}
		var cum int64
		for i := range h.buckets {
			cum += h.buckets[i].Load()
			b := BucketSnapshot{CumulativeSum: cum}
			if i < len(h.bounds) {
				b.LE = h.bounds[i]
			} else {
				b.Inf = true
			}
			hs.Buckets = append(hs.Buckets, b)
		}
		s.Histograms = append(s.Histograms, hs)
	}
	return s
}
