package app

import (
	"testing"
	"time"
)

// TestHourAlignedCutoff pins that the raw-snapshot prune cutoff lands on a UTC
// hour boundary and never later than (now - retentionDays). Whole-hour-aligned
// deletes are what keep rollup from re-aggregating a partially-pruned hour into
// a smaller (regressed) hourly bucket — see pruneTrafficSnapshots.
func TestHourAlignedCutoff(t *testing.T) {
	const days = 7
	cases := []time.Time{
		time.Date(2026, 5, 21, 14, 37, 12, 500, time.UTC),
		time.Date(2026, 5, 21, 0, 0, 0, 0, time.UTC),
		time.Date(2026, 3, 1, 23, 59, 59, 0, time.UTC),
		// A non-UTC input must still produce a UTC hour floor.
		time.Date(2026, 5, 21, 14, 37, 0, 0, time.FixedZone("X", 5*3600)),
	}
	for _, now := range cases {
		got := hourAlignedCutoff(now, days)

		if got.Location() != time.UTC {
			t.Fatalf("now=%v: cutoff location = %v, want UTC", now, got.Location())
		}
		if got.Minute() != 0 || got.Second() != 0 || got.Nanosecond() != 0 {
			t.Fatalf("now=%v: cutoff %v is not hour-aligned", now, got)
		}
		// The cutoff is the hour floor of (now - days), so it must be <= that
		// instant and strictly within the same hour.
		want := now.AddDate(0, 0, -days).UTC()
		if got.After(want) {
			t.Fatalf("now=%v: cutoff %v is after now-%dd %v", now, got, days, want)
		}
		if want.Sub(got) >= time.Hour {
			t.Fatalf("now=%v: cutoff %v more than an hour before now-%dd %v", now, got, days, want)
		}
	}
}
