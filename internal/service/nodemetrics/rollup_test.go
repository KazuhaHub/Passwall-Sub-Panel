package nodemetrics

import (
	"context"
	"testing"
	"time"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
)

// The rollup's acceptance conditions: time-proportional apportionment across the
// UTC boundary, coverage that counts only what was actually observed, and the
// ordering that keeps aggregation ahead of deletion.

func sampleWithCPU(sampleID string, at time.Time, total, idle uint64) domain.NodeHostMetricSample {
	sample := domain.NodeHostMetricSample{
		SampleID: sampleID, BootID: "boot-1", ResourceScope: "host",
		ReceivedAt: at, LogicalCPUs: 4, AgentID: "agt_1",
		SystemCPUTotal: u64(total), SystemCPUIdle: u64(idle),
	}
	return sample
}

// AN INTERVAL THAT CROSSES THE HOUR IS SPLIT, NOT ASSIGNED. Giving it wholly to
// either bucket makes one hour look busier and the next look quieter — a
// distortion that repeats every hour and never averages out.
func TestRollupApportionsAnIntervalAcrossTheBucketBoundary(t *testing.T) {
	bucket := time.Date(2026, 9, 18, 12, 0, 0, 0, time.UTC)
	// 11:59:30 to 12:00:30: thirty seconds on each side.
	predecessor := sampleWithCPU("0123456789abcdef0123456789abcdee", bucket.Add(-30*time.Second), 0, 0)
	inside := sampleWithCPU("0123456789abcdef0123456789abcdef", bucket.Add(30*time.Second), 1000, 500)

	hourly := RollupHour("agt_1", bucket, &predecessor, []domain.NodeHostMetricSample{inside}, nil)
	// Half the CPU was busy over the minute, and only the thirty seconds inside
	// the bucket count towards this hour.
	closeTo(t, "system cpu average", hourly.SystemCPUAverage, 50)
	if hourly.CoverageSeconds != 30 {
		t.Fatalf("coverage = %d, want 30 seconds of the interval", hourly.CoverageSeconds)
	}
	if hourly.SampleCount != 1 {
		t.Fatalf("sample count = %d", hourly.SampleCount)
	}
}

// COVERAGE IS THE HONEST DENOMINATOR. A gap — a reboot, an outage — contributes
// neither to the average nor to the seconds, so a partially observed hour is
// drawn as partial rather than as healthy.
func TestRollupCountsOnlyObservedIntervalsAsCoverage(t *testing.T) {
	bucket := time.Date(2026, 9, 18, 12, 0, 0, 0, time.UTC)
	// Two one-minute intervals, ten minutes apart: ten minutes of the hour is
	// unobserved, and the samples on either side of the gap must not be paired.
	first := sampleWithCPU("0123456789abcdef0123456789abcde1", bucket.Add(time.Minute), 600, 300)
	second := sampleWithCPU("0123456789abcdef0123456789abcde2", bucket.Add(11*time.Minute), 1200, 600)

	hourly := RollupHour("agt_1", bucket, nil, []domain.NodeHostMetricSample{first, second}, nil)
	// There is no predecessor before the bucket, so the first sample starts no
	// interval of its own; the one from it to the second sample is ten minutes,
	// which is inside the derivation window and IS valid. Coverage is those ten
	// minutes, not the sixty the hour would claim.
	if hourly.CoverageSeconds != 600 {
		t.Fatalf("coverage = %d, want 600 seconds", hourly.CoverageSeconds)
	}

	// A gap wider than the derivation window is not an interval at all.
	third := sampleWithCPU("0123456789abcdef0123456789abcde3", bucket.Add(50*time.Minute), 2000, 1000)
	hourly = RollupHour("agt_1", bucket, nil, []domain.NodeHostMetricSample{first, third}, nil)
	if hourly.CoverageSeconds != 0 {
		t.Fatalf("coverage = %d over a gap wider than the derivation window", hourly.CoverageSeconds)
	}
	if hourly.SystemCPUAverage != nil {
		t.Fatalf("a gap produced an average: %v", *hourly.SystemCPUAverage)
	}
}

// The buckets are UTC, so a display timezone — including one with a DST
// transition inside the hour — cannot re-bucket history that is already
// aggregated.
func TestRollupBucketsAreUTC(t *testing.T) {
	// A local time in a zone whose offset changes: the bucket is still the UTC
	// hour, and its boundaries do not move.
	zone := time.FixedZone("UTC+13", 13*60*60)
	local := time.Date(2026, 9, 18, 12, 30, 0, 0, zone)

	bucket := RollupBucketStart(local)
	if bucket.Location() != time.UTC {
		t.Fatalf("bucket location = %v, want UTC", bucket.Location())
	}
	if bucket.Minute() != 0 || bucket.Second() != 0 || bucket.Nanosecond() != 0 {
		t.Fatalf("bucket = %v, want a whole hour", bucket)
	}
	// 12:30 at UTC+13 is 23:30 the previous day in UTC, so the bucket is 23:00.
	want := time.Date(2026, 9, 17, 23, 0, 0, 0, time.UTC)
	if !bucket.Equal(want) {
		t.Fatalf("bucket = %v, want %v", bucket, want)
	}
}

// An hour nobody reported in is not written. An empty hourly row would draw as a
// covered hour with no data, where a missing one draws as what it is.
func TestRollupSkipsAnHourWithNoSamples(t *testing.T) {
	bucket := time.Date(2026, 9, 18, 12, 0, 0, 0, time.UTC)
	hourly := RollupHour("agt_1", bucket, nil, nil, nil)
	if hourly.CoverageSeconds != 0 || hourly.SampleCount != 0 {
		t.Fatalf("an empty hour produced %+v", hourly)
	}
	if hourly.SystemCPUAverage != nil {
		t.Fatal("an empty hour produced a value")
	}
}

// The predecessor is a BASELINE, not a data point: passing it must not add it to
// the sample count while still making the first interval computable.
func TestRollupUsesThePredecessorWithoutCountingIt(t *testing.T) {
	bucket := time.Date(2026, 9, 18, 12, 0, 0, 0, time.UTC)
	predecessor := sampleWithCPU("0123456789abcdef0123456789abcdee", bucket.Add(-time.Minute), 0, 0)
	inside := sampleWithCPU("0123456789abcdef0123456789abcdef", bucket.Add(time.Minute), 1000, 500)

	hourly := RollupHour("agt_1", bucket, &predecessor, []domain.NodeHostMetricSample{inside}, nil)
	if hourly.SampleCount != 1 {
		t.Fatalf("sample count = %d, want only the in-window sample", hourly.SampleCount)
	}
	closeTo(t, "system cpu average", hourly.SystemCPUAverage, 50)
	if hourly.CoverageSeconds != 60 {
		t.Fatalf("coverage = %d, want the full minute", hourly.CoverageSeconds)
	}
}

// ROLLUP MUST PRECEDE PRUNE, and this pins the order rather than trusting the
// reading of the code. Reversing them deletes raw rows before they have been
// aggregated, which loses those hours permanently — and it happens on the first
// tick after every boot, which is exactly when nobody is watching.
func TestMaintenanceRollsUpBeforeItPrunes(t *testing.T) {
	repo := &orderedRepo{}
	service, err := New(Options{Repo: repo, Now: func() time.Time { return deriveBase }})
	if err != nil {
		t.Fatal(err)
	}
	if err := service.RollupAndPrune(t.Context(), deriveBase); err != nil {
		t.Fatal(err)
	}
	if len(repo.order) < 2 {
		t.Fatalf("the pass did %v", repo.order)
	}
	rollupAt, pruneAt := -1, -1
	for index, call := range repo.order {
		if call == "hourly" && rollupAt < 0 {
			rollupAt = index
		}
		if call == "prune" && pruneAt < 0 {
			pruneAt = index
		}
	}
	if rollupAt < 0 || pruneAt < 0 || rollupAt > pruneAt {
		t.Fatalf("rollup at %d, prune at %d; the rollup must come first", rollupAt, pruneAt)
	}
}

// orderedRepo records the sequence of maintenance calls.
type orderedRepo struct {
	stubRepo
	order []string
}

func (r *orderedRepo) HourlyRange(context.Context, string, time.Time, time.Time) ([]domain.NodeHostMetricHourly, error) {
	r.order = append(r.order, "hourly")
	return nil, nil
}

func (r *orderedRepo) Prune(context.Context, domain.NodeHostPruneRequest) (domain.NodeHostPruneResult, error) {
	r.order = append(r.order, "prune")
	return domain.NodeHostPruneResult{}, nil
}

func (r *orderedRepo) AgentIDs(context.Context) ([]string, error) { return []string{"agt_1"}, nil }
