package app

import (
	"testing"

	"github.com/KazuhaHub/passwall-sub-panel/internal/pkg/metrics"
)

type nativeLatencyBucket struct {
	UpperMS float64 `json:"upper_ms"`
	Inf     bool    `json:"inf"`
	Count   int64   `json:"count"`
}

type nativeLatencyHistogram struct {
	Name    string                `json:"name"`
	Count   int64                 `json:"count"`
	Buckets []nativeLatencyBucket `json:"buckets"`
}

// Difference cumulative buckets rather than borrowing the process-wide maximum
// or quantiles. A reset or half-counted observation cannot form a valid window.
func nativeLatencyDifference(before, after metrics.HistogramSnapshot) (nativeLatencyHistogram, bool) {
	out := nativeLatencyHistogram{Name: after.Name, Count: after.Count - before.Count}
	if before.Name != after.Name || before.Unit != "ms" || after.Unit != "ms" || before.Count < 0 || out.Count < 0 || len(before.Buckets) != len(metrics.LatencyBucketsMS)+1 || len(after.Buckets) != len(before.Buckets) {
		return out, false
	}
	var previousBefore, previousAfter, previousDelta int64
	for i, a := range after.Buckets {
		b := before.Buckets[i]
		inf := i == len(metrics.LatencyBucketsMS)
		upper := float64(0)
		if !inf {
			upper = metrics.LatencyBucketsMS[i]
		}
		delta := a.CumulativeSum - b.CumulativeSum
		if a.LE != upper || b.LE != upper || a.Inf != inf || b.Inf != inf || b.CumulativeSum < previousBefore || a.CumulativeSum < previousAfter || delta < previousDelta || b.CumulativeSum > before.Count || a.CumulativeSum > after.Count || delta > out.Count {
			return out, false
		}
		out.Buckets = append(out.Buckets, nativeLatencyBucket{upper, inf, delta})
		previousBefore, previousAfter, previousDelta = b.CumulativeSum, a.CumulativeSum, delta
	}
	return out, previousBefore == before.Count && previousAfter == after.Count && previousDelta == out.Count
}

func TestNativeLatencyWindowRejectsResetAndIncompleteSnapshots(t *testing.T) {
	snapshot := func(count int64) metrics.HistogramSnapshot {
		h := metrics.HistogramSnapshot{Name: "fixture", Unit: "ms", Count: count}
		for _, upper := range metrics.LatencyBucketsMS {
			h.Buckets = append(h.Buckets, metrics.BucketSnapshot{LE: upper, CumulativeSum: count})
		}
		h.Buckets = append(h.Buckets, metrics.BucketSnapshot{Inf: true, CumulativeSum: count})
		return h
	}
	before, after := snapshot(100), snapshot(103)
	if got, ok := nativeLatencyDifference(before, after); !ok || got.Count != 3 || got.Buckets[0].Count != 3 {
		t.Fatal("window reused historical samples")
	}
	for _, change := range []func(*metrics.HistogramSnapshot){
		func(h *metrics.HistogramSnapshot) { h.Count = 99 },
		func(h *metrics.HistogramSnapshot) { h.Count++ },
		func(h *metrics.HistogramSnapshot) { h.Buckets[0].CumulativeSum = 99 },
		func(h *metrics.HistogramSnapshot) { h.Buckets[1].CumulativeSum = 102 },
		func(h *metrics.HistogramSnapshot) { h.Buckets[0].LE = 9 },
		func(h *metrics.HistogramSnapshot) { h.Buckets = h.Buckets[:len(h.Buckets)-1] },
		func(h *metrics.HistogramSnapshot) { h.Name = "other" },
		func(h *metrics.HistogramSnapshot) { h.Unit = "bytes" },
	} {
		bad := snapshot(103)
		change(&bad)
		if _, ok := nativeLatencyDifference(before, bad); ok {
			t.Fatal("reset, inconsistent or foreign histogram accepted")
		}
	}
}
