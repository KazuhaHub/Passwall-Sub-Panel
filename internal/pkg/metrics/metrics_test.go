package metrics

import (
	"math"
	"sync"
	"testing"
	"time"
)

func TestCounterAndGauge(t *testing.T) {
	c := NewCounter("test_counter", "")
	c.Inc()
	c.Add(4)
	if got := c.Value(); got != 5 {
		t.Fatalf("counter = %d, want 5", got)
	}

	g := NewGauge("test_gauge", "")
	g.Inc()
	g.Inc()
	g.Inc()
	g.Dec()
	if got, peak := g.Value(), g.Peak(); got != 2 || peak != 3 {
		t.Fatalf("gauge = %d/peak %d, want 2/3", got, peak)
	}
	// reset rebases the peak on the live value rather than zeroing it —
	// the two in-flight items the gauge is tracking are still in flight.
	g.reset()
	if got, peak := g.Value(), g.Peak(); got != 2 || peak != 2 {
		t.Fatalf("after reset gauge = %d/peak %d, want 2/2", got, peak)
	}
}

func TestHistogramBucketing(t *testing.T) {
	h := NewHistogram("test_hist", "", "ms", []float64{1, 10, 100})
	// One sample per bucket including the +Inf overflow. Bounds are
	// inclusive, so 1 lands in the "<=1" bucket, not the "<=10" one.
	for _, v := range []float64{0.5, 1, 5, 50, 500} {
		h.Observe(v)
	}
	want := []int64{2, 1, 1, 1} // <=1, <=10, <=100, +Inf
	for i, w := range want {
		if got := h.buckets[i].Load(); got != w {
			t.Errorf("bucket %d = %d, want %d", i, got, w)
		}
	}
	if h.Count() != 5 {
		t.Errorf("count = %d, want 5", h.Count())
	}
	if got := h.sum.Load(); got != 556.5 {
		t.Errorf("sum = %v, want 556.5", got)
	}
	if got := h.max.Load(); got != 500 {
		t.Errorf("max = %v, want 500", got)
	}
}

func TestHistogramQuantileIsBoundedByTheBucket(t *testing.T) {
	// 1000 samples uniform over [0,100). The estimate can't be exact —
	// that's the point of a bucket histogram — but it must land inside
	// the bucket that genuinely contains the true quantile, which is the
	// only accuracy guarantee the Phase 0 numbers rest on.
	h := NewHistogram("test_q", "", "ms", LatencyBucketsMS)
	for i := 0; i < 1000; i++ {
		h.Observe(float64(i) / 10)
	}
	for _, tc := range []struct{ q, want float64 }{
		{0.50, 50},
		{0.90, 90},
		{0.99, 99},
	} {
		got := h.quantile(tc.q)
		if math.Abs(got-tc.want) > 10 {
			t.Errorf("quantile(%v) = %v, want within 10 of %v", tc.q, got, tc.want)
		}
	}
}

func TestHistogramQuantileEmptyAndSingle(t *testing.T) {
	h := NewHistogram("test_q2", "", "ms", LatencyBucketsMS)
	if got := h.quantile(0.5); got != 0 {
		t.Fatalf("empty quantile = %v, want 0", got)
	}
	h.Observe(42)
	// A single sample sits in the (20,50] bucket, so interpolation can
	// only place it somewhere in that range — never outside it.
	if got := h.quantile(0.5); got < 20 || got > 50 {
		t.Fatalf("single-sample quantile = %v, want within (20,50]", got)
	}
}

func TestHistogramQuantileInOverflowBucketUsesMax(t *testing.T) {
	// Every sample past the last bound has no upper bound to interpolate
	// toward; the observed max stands in for it, so the estimate must not
	// exceed the true max.
	h := NewHistogram("test_q3", "", "ms", []float64{1, 2})
	for i := 0; i < 10; i++ {
		h.Observe(1000)
	}
	if got := h.quantile(0.95); got > 1000 {
		t.Fatalf("overflow quantile = %v, must not exceed max 1000", got)
	}
}

func TestObserveDurationKeepsSubMillisecondResolution(t *testing.T) {
	h := NewHistogram("test_dur", "", "ms", LatencyBucketsMS)
	h.ObserveDuration(1500 * time.Microsecond)
	if got := h.max.Load(); got != 1.5 {
		t.Fatalf("max = %v, want 1.5 (a sub-ms read must not collapse to 0)", got)
	}
}

func TestConcurrentRecording(t *testing.T) {
	// The record path claims to be safe without locks; assert it under
	// -race rather than trusting the atomics by inspection.
	c := NewCounter("test_conc_counter", "")
	g := NewGauge("test_conc_gauge", "")
	h := NewHistogram("test_conc_hist", "", "ms", LatencyBucketsMS)

	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 500; j++ {
				c.Inc()
				g.Inc()
				h.Observe(float64(j % 300))
				g.Dec()
			}
		}()
	}
	// Snapshotting concurrently with recording is the real deployment
	// shape (an admin hits the endpoint mid-cycle) and must not race.
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 50; i++ {
			_ = Take()
		}
	}()
	wg.Wait()

	if got := c.Value(); got != 8000 {
		t.Errorf("counter = %d, want 8000", got)
	}
	if got := h.Count(); got != 8000 {
		t.Errorf("histogram count = %d, want 8000", got)
	}
	if got := g.Value(); got != 0 {
		t.Errorf("gauge = %d, want 0 (every Inc paired with a Dec)", got)
	}
}

func TestSnapshotBucketsAreCumulative(t *testing.T) {
	h := NewHistogram("test_snap", "", "ms", []float64{1, 10, 100})
	for _, v := range []float64{0.5, 5, 50, 500} {
		h.Observe(v)
	}
	var found *HistogramSnapshot
	for i, hs := range Take().Histograms {
		if hs.Name == "test_snap" {
			found = &Take().Histograms[i]
			break
		}
	}
	if found == nil {
		t.Fatal("histogram missing from snapshot")
	}
	want := []int64{1, 2, 3, 4}
	for i, w := range want {
		if found.Buckets[i].CumulativeSum != w {
			t.Errorf("cumulative bucket %d = %d, want %d", i, found.Buckets[i].CumulativeSum, w)
		}
	}
	if !found.Buckets[len(want)-1].Inf {
		t.Error("last bucket should be flagged +Inf")
	}
	if found.Mean != 555.5/4 {
		t.Errorf("mean = %v, want %v", found.Mean, 555.5/4)
	}
}
