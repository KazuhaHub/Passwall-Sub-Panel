package metrics

import "sync"

// A Vec is a family of metrics that share a name and differ by one label
// value — panel operation, poll stage. Children are created lazily on
// first use and never removed, so the label space must be bounded and
// small (a fixed set of operation names, not user IDs or panel
// addresses). Nothing enforces that; it is a call-site contract.
//
// One label rather than Prometheus' arbitrary label sets: every question
// Phase 0 asks is "this measurement, broken down one way". Supporting
// label tuples would mean a key-encoding scheme and a much wider snapshot
// format for no reader that wants it.

// HistogramVec is a Histogram family keyed by a single label value.
type HistogramVec struct {
	name   string
	help   string
	unit   string
	label  string
	bounds []float64

	mu       sync.RWMutex
	children map[string]*Histogram
}

// NewHistogramVec declares a histogram family. Call from a package-level var.
func NewHistogramVec(name, help, unit, label string, bounds []float64) *HistogramVec {
	return &HistogramVec{
		name: name, help: help, unit: unit, label: label, bounds: bounds,
		children: make(map[string]*Histogram),
	}
}

// With returns the child histogram for a label value, creating and
// registering it on first use. The read lock covers the steady state;
// the write lock is taken once per distinct label value, ever.
func (v *HistogramVec) With(value string) *Histogram {
	v.mu.RLock()
	h := v.children[value]
	v.mu.RUnlock()
	if h != nil {
		return h
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	// Re-check: two goroutines can both miss the read lock, and the
	// second must get the first one's child, not a fresh one that
	// silently discards the first's samples.
	if h = v.children[value]; h != nil {
		return h
	}
	h = NewHistogram(v.name+"{"+v.label+"="+value+"}", v.help, v.unit, v.bounds)
	v.children[value] = h
	return h
}

// CounterVec is a Counter family keyed by a single label value.
type CounterVec struct {
	name  string
	help  string
	label string

	mu       sync.RWMutex
	children map[string]*Counter
}

// NewCounterVec declares a counter family. Call from a package-level var.
func NewCounterVec(name, help, label string) *CounterVec {
	return &CounterVec{name: name, help: help, label: label, children: make(map[string]*Counter)}
}

// With returns the child counter for a label value, creating and
// registering it on first use.
func (v *CounterVec) With(value string) *Counter {
	v.mu.RLock()
	c := v.children[value]
	v.mu.RUnlock()
	if c != nil {
		return c
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	if c = v.children[value]; c != nil {
		return c
	}
	c = NewCounter(v.name+"{"+v.label+"="+value+"}", v.help)
	v.children[value] = c
	return c
}
