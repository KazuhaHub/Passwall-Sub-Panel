package rollup

import (
	"context"
	"testing"
	"time"
)

// The hourly cleanup rolls up before it prunes. It used to read every raw
// snapshot of the 7-day window each hour and rewrite every bucket, although only
// the hours since the previous pass can have changed. These tests pin what the
// bounded pass must still do.

const hourlyUp = "SELECT up_bytes FROM traffic_snapshots_hourly WHERE user_id=? AND bucket_start=?"

func upOf(t *testing.T, svc *Service, user int64, bucket time.Time) int64 {
	t.Helper()
	var row struct{ UpBytes int64 }
	if n := countRows(t, svc.db, "SELECT COUNT(*) FROM traffic_snapshots_hourly WHERE user_id=? AND bucket_start=?", user, bucket); n == 0 {
		return -1
	}
	scanOne(t, svc.db, &row, hourlyUp, user, bucket)
	return row.UpBytes
}

// The first pass of a process is the backfill: it emits every bucket, however old.
func TestRollupBeforePruneFirstPassIsFull(t *testing.T) {
	svc, g := newServiceFromTest(t)
	now := time.Date(2026, 6, 20, 12, 30, 0, 0, time.UTC)
	svc.now = func() time.Time { return now }
	old := now.Add(-5 * 24 * time.Hour).Truncate(time.Hour)
	insertUserSnap(t, g, 1, old.Add(-10*time.Minute), 0, 0, 0)
	insertUserSnap(t, g, 1, old.Add(50*time.Minute), 600, 0, 600)

	if err := svc.RollupBeforePrune(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := upOf(t, svc, 1, old); got != 500 {
		t.Fatalf("the first pass must backfill a 5-day-old bucket: up=%d, want 500", got)
	}
}

// A later pass leaves the buckets the previous pass finished alone and emits
// what changed since: here a sentinel written into an old, final bucket must
// survive (a full pass would overwrite it), and the new hour must appear.
func TestRollupBeforePruneLaterPassEmitsOnlySinceThePreviousOne(t *testing.T) {
	svc, g := newServiceFromTest(t)
	ctx := context.Background()
	now := time.Date(2026, 6, 20, 12, 30, 0, 0, time.UTC)
	svc.now = func() time.Time { return now }
	old := now.Add(-5 * 24 * time.Hour).Truncate(time.Hour)
	insertUserSnap(t, g, 1, old.Add(-10*time.Minute), 0, 0, 0)
	insertUserSnap(t, g, 1, old.Add(50*time.Minute), 600, 0, 600)
	if err := svc.RollupBeforePrune(ctx); err != nil {
		t.Fatal(err)
	}
	if err := g.Exec("UPDATE traffic_snapshots_hourly SET up_bytes = 7 WHERE user_id = 1 AND bucket_start = ?", old).Error; err != nil {
		t.Fatal(err)
	}

	// An hour later: one new closed hour of traffic.
	now = now.Add(time.Hour)
	fresh := time.Date(2026, 6, 20, 12, 0, 0, 0, time.UTC)
	insertUserSnap(t, g, 1, fresh.Add(-5*time.Minute), 1000, 0, 1000)
	insertUserSnap(t, g, 1, fresh.Add(55*time.Minute), 1600, 0, 1600)
	if err := svc.RollupBeforePrune(ctx); err != nil {
		t.Fatal(err)
	}
	if got := upOf(t, svc, 1, old); got != 7 {
		t.Fatalf("a later pass must not rewrite a bucket finished days ago: up=%d, want the sentinel 7", got)
	}
	if got := upOf(t, svc, 1, fresh); got != 550 {
		t.Fatalf("the hour since the previous pass must be emitted: up=%d, want 550", got)
	}
}

// A pass that failed has finished nothing: the next one still covers everything
// since the last pass that succeeded, however long ago that was.
func TestRollupBeforePruneFailedPassDoesNotMoveTheMark(t *testing.T) {
	svc, g := newServiceFromTest(t)
	ctx := context.Background()
	now := time.Date(2026, 6, 20, 12, 30, 0, 0, time.UTC)
	svc.now = func() time.Time { return now }
	insertUserSnap(t, g, 1, now.Add(-20*time.Minute), 0, 0, 0)
	if err := svc.RollupBeforePrune(ctx); err != nil {
		t.Fatal(err)
	}

	// Traffic lands five hours later; the pass at +9h fails; the pass at +10h
	// is far past the usual look-back from "now" but must still emit that hour.
	mid := time.Date(2026, 6, 20, 17, 0, 0, 0, time.UTC)
	insertUserSnap(t, g, 1, mid.Add(-5*time.Minute), 100, 0, 100)
	insertUserSnap(t, g, 1, mid.Add(55*time.Minute), 700, 0, 700)
	now = now.Add(9 * time.Hour)
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if err := svc.RollupBeforePrune(cancelled); err == nil {
		t.Fatal("a pass on a cancelled context must fail")
	}
	now = now.Add(time.Hour)
	if err := svc.RollupBeforePrune(ctx); err != nil {
		t.Fatal(err)
	}
	if got := upOf(t, svc, 1, mid); got != 550 {
		t.Fatalf("the hour missed by the failed pass must be emitted by the next: up=%d, want 550", got)
	}
}

// A clock that went backwards makes "since the previous pass" meaningless: the
// pass is a full one again.
func TestRollupBeforePruneClockSetBackIsFull(t *testing.T) {
	svc, g := newServiceFromTest(t)
	ctx := context.Background()
	now := time.Date(2026, 6, 20, 12, 30, 0, 0, time.UTC)
	svc.now = func() time.Time { return now }
	if err := svc.RollupBeforePrune(ctx); err != nil {
		t.Fatal(err)
	}
	now = now.Add(-3 * 24 * time.Hour)
	old := now.Add(-2 * 24 * time.Hour).Truncate(time.Hour)
	insertUserSnap(t, g, 1, old.Add(-10*time.Minute), 0, 0, 0)
	insertUserSnap(t, g, 1, old.Add(50*time.Minute), 600, 0, 600)
	if err := svc.RollupBeforePrune(ctx); err != nil {
		t.Fatal(err)
	}
	if got := upOf(t, svc, 1, old); got != 500 {
		t.Fatalf("after the clock went back the pass must be full: up=%d, want 500", got)
	}
}

// The margin behind the previous pass covers the longest segment that can still
// reach back across it: the heartbeat, plus the hour its bucket starts in.
func TestBeforePruneMargin(t *testing.T) {
	for _, c := range []struct{ heartbeat, want time.Duration }{
		{time.Hour, 3 * time.Hour},
		{2 * time.Hour, 4 * time.Hour},
		{150 * time.Minute, 270 * time.Minute},
		{5 * time.Hour, 7 * time.Hour},
	} {
		if got := beforePruneMargin(c.heartbeat); got != c.want {
			t.Errorf("beforePruneMargin(%v) = %v, want %v", c.heartbeat, got, c.want)
		}
	}
}

// A slow poll cadence has a long heartbeat, and a segment may then reach back
// many hours. The bucket the segment starts in has to be recomputed with a
// sample from before its :00 in hand, or it is treated as left-incomplete and
// keeps the smaller value written before the segment existed.
//
// 180-minute polls (heartbeat 7.5h), with every other poll missed: samples at
// 00:30, 06:30 and 12:30. The 06:00 hour gets half an hour of the first
// segment and half an hour of the second.
func TestRollupBeforePruneLongHeartbeatMatchesTheFullPass(t *testing.T) {
	svc, g := newServiceFromTest(t)
	svc.heartbeat = heartbeatFor(180 * time.Minute)
	ctx := context.Background()
	day := time.Date(2026, 6, 20, 0, 0, 0, 0, time.UTC)
	now := day.Add(10 * time.Minute)
	svc.now = func() time.Time { return now }

	samples := map[time.Duration]int64{30 * time.Minute: 0, 6*time.Hour + 30*time.Minute: 3600, 12*time.Hour + 30*time.Minute: 7200}
	// Hourly passes at :10 from 00:10 to 13:10, each seeing the samples taken so far.
	for h := 0; h <= 13; h++ {
		now = day.Add(time.Duration(h)*time.Hour + 10*time.Minute)
		for at, total := range samples {
			if at <= now.Sub(day) && at > now.Sub(day)-time.Hour {
				insertUserSnap(t, g, 1, day.Add(at), total, 0, total)
			}
		}
		if err := svc.RollupBeforePrune(ctx); err != nil {
			t.Fatal(err)
		}
	}
	if got := upOf(t, svc, 1, day.Add(6*time.Hour)); got != 600 {
		t.Fatalf("06:00 bucket = %d, want 600 (300 from each segment)", got)
	}
}

// The mark alone decides the window, so one full pass a day is kept as the
// corrector for anything a bounded window cannot see: a bucket whose only
// earlier sample lies further back than the window reaches.
func TestRollupBeforePruneRunsAFullPassOnceADay(t *testing.T) {
	svc, g := newServiceFromTest(t)
	ctx := context.Background()
	now := time.Date(2026, 6, 20, 12, 30, 0, 0, time.UTC)
	svc.now = func() time.Time { return now }
	old := now.Add(-5 * 24 * time.Hour).Truncate(time.Hour)
	insertUserSnap(t, g, 1, old.Add(-10*time.Minute), 0, 0, 0)
	insertUserSnap(t, g, 1, old.Add(50*time.Minute), 600, 0, 600)
	if err := svc.RollupBeforePrune(ctx); err != nil {
		t.Fatal(err)
	}
	spoil := func() {
		t.Helper()
		if err := g.Exec("UPDATE traffic_snapshots_hourly SET up_bytes = 7 WHERE user_id = 1 AND bucket_start = ?", old).Error; err != nil {
			t.Fatal(err)
		}
	}
	spoil()
	for h := 1; h < 24; h++ {
		now = now.Add(time.Hour)
		if err := svc.RollupBeforePrune(ctx); err != nil {
			t.Fatal(err)
		}
	}
	if got := upOf(t, svc, 1, old); got != 7 {
		t.Fatalf("within a day of the full pass the old bucket must be left alone, got %d", got)
	}
	now = now.Add(time.Hour)
	if err := svc.RollupBeforePrune(ctx); err != nil {
		t.Fatal(err)
	}
	if got := upOf(t, svc, 1, old); got != 500 {
		t.Fatalf("a day after the last full pass the next one must be full again: up=%d, want 500", got)
	}
}

// User snapshots are stamped in the panel's timezone, the bound in the
// process's. SQLite compares the two as text, so rows stamped with an offset
// behind the bound's read hours "earlier" than they are and used to fall out of
// the window: with the panel on Los Angeles time and the process on UTC, no
// user bucket was ever written by a bounded pass.
func TestBoundedPassesSeeRowsStampedInAnotherZone(t *testing.T) {
	svc, g := newServiceFromTest(t)
	ctx := context.Background()
	la := time.FixedZone("PDT", -7*3600)
	now := time.Date(2026, 6, 20, 12, 30, 0, 0, time.UTC)
	svc.now = func() time.Time { return now }
	if err := svc.RollupBeforePrune(ctx); err != nil { // the full pass, on an empty table
		t.Fatal(err)
	}
	hour := time.Date(2026, 6, 20, 12, 0, 0, 0, time.UTC)
	insertUserSnap(t, g, 1, hour.Add(-5*time.Minute).In(la), 1000, 0, 1000)
	insertUserSnap(t, g, 1, hour.Add(55*time.Minute).In(la), 1600, 0, 1600)
	now = now.Add(time.Hour)
	if err := svc.RollupRecent(ctx); err != nil {
		t.Fatal(err)
	}
	if got := upOf(t, svc, 1, hour); got != 550 {
		t.Fatalf("the per-poll pass must see rows stamped in another zone: up=%d, want 550", got)
	}
	if err := g.Exec("DELETE FROM traffic_snapshots_hourly").Error; err != nil {
		t.Fatal(err)
	}
	if err := svc.RollupBeforePrune(ctx); err != nil {
		t.Fatal(err)
	}
	if got := upOf(t, svc, 1, hour); got != 550 {
		t.Fatalf("the hourly pass must see rows stamped in another zone: up=%d, want 550", got)
	}
}
