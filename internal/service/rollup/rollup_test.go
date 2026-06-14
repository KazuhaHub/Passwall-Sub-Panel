package rollup

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"gorm.io/gorm"

	"github.com/KazuhaHub/passwall-sub-panel/internal/adapters/sqlstore"
)

// newServiceFromTest spins up a fresh SQLite under t.TempDir(), runs
// EnsureSchema, and returns a rollup Service hooked up to it plus an
// exec/read helper. Tests run against the real GORM dialect so the
// OnConflict upsert and uniqueIndex shapes behave like production.
func newServiceFromTest(t *testing.T) (*Service, *gorm.DB) {
	t.Helper()
	g, err := sqlstore.Open("sqlite", filepath.Join(t.TempDir(), "panel.db"))
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := sqlstore.EnsureSchema(g); err != nil {
		t.Fatalf("ensure schema: %v", err)
	}
	t.Cleanup(func() {
		sqlDB, err := g.DB()
		if err == nil {
			_ = sqlDB.Close()
		}
	})
	return New(g, 0), g
}

func insertUserSnap(t *testing.T, g *gorm.DB, userID int64, ts time.Time, up, down, total int64) {
	t.Helper()
	if err := g.Exec(
		`INSERT INTO traffic_snapshots(user_id, up_bytes, down_bytes, total_bytes, captured_at) VALUES(?,?,?,?,?)`,
		userID, up, down, total, ts,
	).Error; err != nil {
		t.Fatalf("insert raw user snap: %v", err)
	}
}

func insertNodeSnap(t *testing.T, g *gorm.DB, nodeID int64, ts time.Time, up, down, total int64) {
	t.Helper()
	if err := g.Exec(
		`INSERT INTO node_traffic_snapshots(node_id, up_bytes, down_bytes, total_bytes, captured_at) VALUES(?,?,?,?,?)`,
		nodeID, up, down, total, ts,
	).Error; err != nil {
		t.Fatalf("insert raw node snap: %v", err)
	}
}

func insertClientSnap(t *testing.T, g *gorm.DB, userID, panelID int64, inboundID int, email string, ts time.Time, up, down, total int64) {
	t.Helper()
	if err := g.Exec(
		`INSERT INTO client_traffic_snapshots(user_id, panel_id, inbound_id, client_email, up_bytes, down_bytes, total_bytes, captured_at) VALUES(?,?,?,?,?,?,?,?)`,
		userID, panelID, inboundID, email, up, down, total, ts,
	).Error; err != nil {
		t.Fatalf("insert raw client snap: %v", err)
	}
}

// scanOne pulls a single row into dest via GORM Raw. Used because the
// hourly tables don't have a registered repo — we want to assert on raw
// column values directly.
func scanOne(t *testing.T, g *gorm.DB, dest any, query string, args ...any) {
	t.Helper()
	if err := g.Raw(query, args...).Scan(dest).Error; err != nil {
		t.Fatalf("scan %q: %v", query, err)
	}
}

func countRows(t *testing.T, g *gorm.DB, query string, args ...any) int64 {
	t.Helper()
	var n int64
	if err := g.Raw(query, args...).Scan(&n).Error; err != nil {
		t.Fatalf("count %q: %v", query, err)
	}
	return n
}

// Reference times shared across tests. h14 is far enough in the past
// that hourFloor(now) is well beyond it, so it always counts as a
// completed hour.
var (
	h14 = time.Date(2025, 5, 17, 14, 0, 0, 0, time.UTC)
	h15 = time.Date(2025, 5, 17, 15, 0, 0, 0, time.UTC)
)

// TestRollupUserDeltaWithinBucket: 4 raw rows in the 14:00 UTC hour for
// user 1 form a monotonic ramp 100→500. After rollup, the 14:00 hourly
// row should store the delta (400).
func TestRollupUserDeltaWithinBucket(t *testing.T) {
	svc, g := newServiceFromTest(t)
	ctx := context.Background()

	insertUserSnap(t, g, 1, h14.Add(5*time.Minute), 100, 50, 150)
	insertUserSnap(t, g, 1, h14.Add(10*time.Minute), 200, 100, 300)
	insertUserSnap(t, g, 1, h14.Add(40*time.Minute), 350, 175, 525)
	insertUserSnap(t, g, 1, h14.Add(55*time.Minute), 500, 250, 750)

	if err := svc.RollupOnce(ctx); err != nil {
		t.Fatalf("rollup: %v", err)
	}

	var got struct {
		UpBytes    int64
		DownBytes  int64
		TotalBytes int64
	}
	scanOne(t, g, &got,
		"SELECT up_bytes, down_bytes, total_bytes FROM traffic_snapshots_hourly WHERE user_id = ? AND bucket_start = ?",
		1, h14)
	if got.UpBytes != 400 || got.DownBytes != 200 || got.TotalBytes != 600 {
		t.Fatalf("delta = up=%d down=%d total=%d, want 400/200/600", got.UpBytes, got.DownBytes, got.TotalBytes)
	}
}

// TestRollupIncludesCurrentHourExcludesFuture: the still-filling CURRENT UTC
// hour IS rolled up (idempotent re-runs keep the chart's "today" live), while a
// row stamped in a FUTURE hour (clock skew) is excluded.
func TestRollupIncludesCurrentHourExcludesFuture(t *testing.T) {
	svc, g := newServiceFromTest(t)
	ctx := context.Background()

	nowHour := hourFloor(time.Now())
	// Two rows in the current (partial) hour — a 100→500 ramp. nowHour <= now
	// always; the second is stamped at now (before the rollup's now cutoff).
	insertUserSnap(t, g, 1, nowHour, 100, 50, 150)
	insertUserSnap(t, g, 1, time.Now(), 500, 250, 750)
	// A future-hour row must be excluded.
	insertUserSnap(t, g, 1, time.Now().Add(2*time.Hour), 9999, 9999, 9999)

	if err := svc.RollupOnce(ctx); err != nil {
		t.Fatalf("rollup: %v", err)
	}

	var got struct{ UpBytes int64 }
	scanOne(t, g, &got,
		"SELECT up_bytes FROM traffic_snapshots_hourly WHERE user_id = ? AND bucket_start = ?", 1, nowHour)
	if got.UpBytes != 400 {
		t.Fatalf("current-hour bucket should roll up live: up delta = %d, want 400", got.UpBytes)
	}
	if n := countRows(t, g,
		"SELECT COUNT(*) FROM traffic_snapshots_hourly WHERE user_id = ? AND bucket_start = ?",
		1, hourFloor(time.Now().Add(2*time.Hour))); n != 0 {
		t.Fatalf("future-hour row must not be rolled up, found %d hourly rows", n)
	}
}

// TestRollupIdempotent: running rollup three times on the same raw data
// must produce a single hourly row, not three. This is what makes "first
// run = backfill" safe even if rollup retries because of, say, the cron
// pricking it after a panel restart.
func TestRollupIdempotent(t *testing.T) {
	svc, g := newServiceFromTest(t)
	ctx := context.Background()

	insertUserSnap(t, g, 1, h14.Add(5*time.Minute), 100, 50, 150)
	insertUserSnap(t, g, 1, h14.Add(55*time.Minute), 500, 250, 750)

	for i := 0; i < 3; i++ {
		if err := svc.RollupOnce(ctx); err != nil {
			t.Fatalf("rollup iter %d: %v", i, err)
		}
	}

	if n := countRows(t, g, "SELECT COUNT(*) FROM traffic_snapshots_hourly"); n != 1 {
		t.Fatalf("expected exactly 1 hourly row after 3 rollups, got %d", n)
	}
	var got struct{ UpBytes int64 }
	scanOne(t, g, &got, "SELECT up_bytes FROM traffic_snapshots_hourly LIMIT 1")
	if got.UpBytes != 400 {
		t.Fatalf("up delta = %d, want 400", got.UpBytes)
	}
}

// TestRollupNodeAndSkipsClient: the node tier rolls up (node_id, bucket_start),
// but the client tier is intentionally NOT rolled up anymore — client_*_hourly
// was write-only dead storage (no chart reads it), so even with raw client
// snapshots present, no client hourly row is produced.
func TestRollupNodeAndSkipsClient(t *testing.T) {
	svc, g := newServiceFromTest(t)
	ctx := context.Background()

	insertClientSnap(t, g, 1, 10, 2, "alice@ex.com", h14.Add(5*time.Minute), 100, 50, 150)
	insertClientSnap(t, g, 1, 10, 2, "alice@ex.com", h14.Add(55*time.Minute), 500, 250, 750)

	insertNodeSnap(t, g, 7, h14.Add(5*time.Minute), 100, 50, 150)
	insertNodeSnap(t, g, 7, h14.Add(55*time.Minute), 500, 250, 750)

	if err := svc.RollupOnce(ctx); err != nil {
		t.Fatalf("rollup: %v", err)
	}

	var got struct{ UpBytes int64 }
	scanOne(t, g, &got,
		"SELECT up_bytes FROM node_traffic_snapshots_hourly WHERE node_id=?", 7)
	if got.UpBytes != 400 {
		t.Fatalf("node delta = %d, want 400", got.UpBytes)
	}

	// Client tier must NOT be rolled up anymore.
	if n := countRows(t, g, "SELECT COUNT(*) FROM client_traffic_snapshots_hourly"); n != 0 {
		t.Fatalf("client rollup was removed; expected 0 client hourly rows, got %d", n)
	}
}

// TestRollupSplitsTrafficAcrossHourBoundary: the segment that straddles the
// 14:00→15:00 boundary (14:55 reading 200 → 15:05 reading 260, +60 over 10min)
// is split by time fraction: half its 10min is in 14:00, half in 15:00, so +30
// to each. So 14:00 = 100 (in-hour) + 30 (boundary half) = 130, and 15:00 = 30
// (boundary half) + 140 (in-hour) = 170. Total 300 is conserved (= 400-100).
// This is the proportional/interpolation model (RRDtool-style), replacing the
// old carry-in that dumped the whole 60 into the later hour (15:00=200).
func TestRollupSplitsTrafficAcrossHourBoundary(t *testing.T) {
	svc, g := newServiceFromTest(t)
	ctx := context.Background()

	insertUserSnap(t, g, 1, h14.Add(5*time.Minute), 100, 0, 100)
	insertUserSnap(t, g, 1, h14.Add(55*time.Minute), 200, 0, 200)
	insertUserSnap(t, g, 1, h15.Add(5*time.Minute), 260, 0, 260)
	insertUserSnap(t, g, 1, h15.Add(55*time.Minute), 400, 0, 400)

	if err := svc.RollupOnce(ctx); err != nil {
		t.Fatalf("rollup: %v", err)
	}

	var first, second struct{ UpBytes int64 }
	scanOne(t, g, &first, "SELECT up_bytes FROM traffic_snapshots_hourly WHERE user_id=? AND bucket_start=?", 1, h14)
	scanOne(t, g, &second, "SELECT up_bytes FROM traffic_snapshots_hourly WHERE user_id=? AND bucket_start=?", 1, h15)
	if first.UpBytes != 130 {
		t.Fatalf("14:00 bucket up = %d, want 130 (100 in-hour + 30 boundary half)", first.UpBytes)
	}
	if second.UpBytes != 170 {
		t.Fatalf("15:00 bucket up = %d, want 170 (30 boundary half + 140 in-hour)", second.UpBytes)
	}
	if first.UpBytes+second.UpBytes != 300 {
		t.Fatalf("total must be conserved at 300, got %d", first.UpBytes+second.UpBytes)
	}
}

// TestRollupProportionalSplitFraction: a single 20-min segment 14:45→15:05 with
// +200 bytes splits by TIME — 15min (75%) in 14:00, 5min (25%) in 15:00 — so
// 14:00 gets 150 and 15:00 gets 50. Locks that the boundary split is
// time-proportional (interpolation), not a flat 50/50 or all-to-one-hour.
func TestRollupProportionalSplitFraction(t *testing.T) {
	svc, g := newServiceFromTest(t)
	ctx := context.Background()

	insertUserSnap(t, g, 1, h14.Add(45*time.Minute), 0, 0, 0)
	insertUserSnap(t, g, 1, h15.Add(5*time.Minute), 200, 0, 200)

	if err := svc.RollupOnce(ctx); err != nil {
		t.Fatalf("rollup: %v", err)
	}
	var a, b struct{ UpBytes int64 }
	scanOne(t, g, &a, "SELECT up_bytes FROM traffic_snapshots_hourly WHERE user_id=? AND bucket_start=?", 1, h14)
	scanOne(t, g, &b, "SELECT up_bytes FROM traffic_snapshots_hourly WHERE user_id=? AND bucket_start=?", 1, h15)
	if a.UpBytes != 150 {
		t.Fatalf("14:00 = %d, want 150 (75%% of 200)", a.UpBytes)
	}
	if b.UpBytes != 50 {
		t.Fatalf("15:00 = %d, want 50 (25%% of 200)", b.UpBytes)
	}
}

// TestRollupDropsTrafficAcrossLargeGap: when an hour is missing (panel was
// down), the segment spanning the gap (14:55 → 16:05, 70min > maxInterpGap) is
// DROPPED, not smeared across the missing 15:00 hour — we can't know when within
// the gap the traffic flowed. So 16:00 only counts its in-hour segment (900-500).
func TestRollupDropsTrafficAcrossLargeGap(t *testing.T) {
	svc, g := newServiceFromTest(t)
	ctx := context.Background()
	h16 := h14.Add(2 * time.Hour) // 15:00 deliberately absent

	insertUserSnap(t, g, 1, h14.Add(5*time.Minute), 100, 0, 100)
	insertUserSnap(t, g, 1, h14.Add(55*time.Minute), 200, 0, 200)
	insertUserSnap(t, g, 1, h16.Add(5*time.Minute), 500, 0, 500)
	insertUserSnap(t, g, 1, h16.Add(55*time.Minute), 900, 0, 900)

	if err := svc.RollupOnce(ctx); err != nil {
		t.Fatalf("rollup: %v", err)
	}
	var got struct{ UpBytes int64 }
	scanOne(t, g, &got, "SELECT up_bytes FROM traffic_snapshots_hourly WHERE user_id=? AND bucket_start=?", 1, h16)
	if got.UpBytes != 400 { // 900-500 in-hour; the cross-gap segment is dropped
		t.Fatalf("16:00 bucket across a gap up = %d, want 400 (gap segment dropped, not smeared)", got.UpBytes)
	}
}

// TestRollupCounterResetClampsToZero: if the counter reset across a segment
// (15:05's reading 50 is BELOW 14:55's 500 — an Xray restart), that segment's
// delta is negative and clamps to 0, so it contributes nothing and no bucket
// goes negative. The next clean segment (50→200) is counted normally.
func TestRollupCounterResetClampsToZero(t *testing.T) {
	svc, g := newServiceFromTest(t)
	ctx := context.Background()

	insertUserSnap(t, g, 1, h14.Add(5*time.Minute), 100, 0, 100)
	insertUserSnap(t, g, 1, h14.Add(55*time.Minute), 500, 0, 500)
	insertUserSnap(t, g, 1, h15.Add(5*time.Minute), 50, 0, 50) // reset
	insertUserSnap(t, g, 1, h15.Add(55*time.Minute), 200, 0, 200)

	if err := svc.RollupOnce(ctx); err != nil {
		t.Fatalf("rollup: %v", err)
	}
	var got struct{ UpBytes int64 }
	scanOne(t, g, &got, "SELECT up_bytes FROM traffic_snapshots_hourly WHERE user_id=? AND bucket_start=?", 1, h15)
	if got.UpBytes != 150 { // 50→200 clean segment; the 500→50 reset segment clamps to 0
		t.Fatalf("15:00 bucket after reset up = %d, want 150", got.UpBytes)
	}
}

// TestRollupBoundaryBucketSurvivesRawPrune: once the sample before an hour's :00
// ages off the 7-day prune frontier, that hour is no longer left-complete and a
// re-run would recompute it SHORT (missing its boundary segment). The persisted
// value must be preserved (keepIfNew / DO NOTHING), not overwritten. The 15:00
// bucket keeps its full 170 even after the 14:00 raw rows are gone.
func TestRollupBoundaryBucketSurvivesRawPrune(t *testing.T) {
	svc, g := newServiceFromTest(t)
	ctx := context.Background()

	insertUserSnap(t, g, 1, h14.Add(5*time.Minute), 100, 0, 100)
	insertUserSnap(t, g, 1, h14.Add(55*time.Minute), 200, 0, 200)
	insertUserSnap(t, g, 1, h15.Add(5*time.Minute), 260, 0, 260)
	insertUserSnap(t, g, 1, h15.Add(55*time.Minute), 400, 0, 400)

	if err := svc.RollupOnce(ctx); err != nil {
		t.Fatalf("rollup: %v", err)
	}
	var second struct{ UpBytes int64 }
	scanOne(t, g, &second, "SELECT up_bytes FROM traffic_snapshots_hourly WHERE user_id=? AND bucket_start=?", 1, h15)
	if second.UpBytes != 170 {
		t.Fatalf("15:00 delta = %d, want 170", second.UpBytes)
	}

	// Simulate the raw prune frontier advancing past the 14:00 hour.
	if err := g.Exec("DELETE FROM traffic_snapshots WHERE captured_at < ?", h15).Error; err != nil {
		t.Fatalf("prune raw: %v", err)
	}

	// Re-running the full rollup must not shrink the persisted 15:00 row even
	// though the sample before 15:00 (its boundary segment's left end) is gone.
	if err := svc.RollupOnce(ctx); err != nil {
		t.Fatalf("rollup after prune: %v", err)
	}
	scanOne(t, g, &second, "SELECT up_bytes FROM traffic_snapshots_hourly WHERE user_id=? AND bucket_start=?", 1, h15)
	if second.UpBytes != 170 {
		t.Fatalf("15:00 bucket shrank after raw prune: up = %d, want 170 (preserved)", second.UpBytes)
	}
}

// TestRollupRecentSkipsOldBuckets: RollupRecent (the per-poll pass) must emit
// only buckets within rollupRecentEmitWindow of now, leaving older final
// buckets to the hourly RollupOnce pass — that's what keeps the per-poll write
// volume bounded instead of re-upserting the whole raw window every cycle.
func TestRollupRecentSkipsOldBuckets(t *testing.T) {
	svc, g := newServiceFromTest(t)
	ctx := context.Background()
	// Pin the clock to a fixed UTC instant. Without this the test mixed
	// hourFloor(time.Now()) (UTC) with time.Now() (process TZ) fixtures, and the
	// SQLite driver compares captured_at as TZ-offset-bearing strings (see
	// withRecentRawWindow), so in any non-UTC environment the UTC current-hour
	// sample sorted below the local-offset window bound and was silently excluded
	// — the current-hour segment then had a single sample and emitted nothing.
	now := time.Date(2026, 6, 13, 12, 30, 0, 0, time.UTC)
	svc.now = func() time.Time { return now }

	// An old, long-closed hour…
	insertUserSnap(t, g, 1, h14.Add(5*time.Minute), 100, 0, 100)
	insertUserSnap(t, g, 1, h14.Add(55*time.Minute), 200, 0, 200)
	// …and the current, still-open hour: two UTC samples (matching the pinned
	// UTC clock) ~29 min apart — under the 1h heartbeat and strictly before the
	// cutoff (now), so they form one emittable segment in the current bucket.
	nowHour := hourFloor(now)
	insertUserSnap(t, g, 1, nowHour, 1000, 0, 1000)
	insertUserSnap(t, g, 1, now.Add(-time.Minute), 1400, 0, 1400)

	if err := svc.RollupRecent(ctx); err != nil {
		t.Fatalf("rollup recent: %v", err)
	}
	if n := countRows(t, g, "SELECT COUNT(*) FROM traffic_snapshots_hourly WHERE user_id=? AND bucket_start=?", 1, h14); n != 0 {
		t.Fatalf("RollupRecent must skip the old %v bucket, found %d", h14, n)
	}
	if n := countRows(t, g, "SELECT COUNT(*) FROM traffic_snapshots_hourly WHERE user_id=? AND bucket_start=?", 1, nowHour); n != 1 {
		t.Fatalf("RollupRecent must emit the current-hour bucket, found %d", n)
	}
	// The full pass backfills the old bucket too.
	if err := svc.RollupOnce(ctx); err != nil {
		t.Fatalf("rollup once: %v", err)
	}
	if n := countRows(t, g, "SELECT COUNT(*) FROM traffic_snapshots_hourly WHERE user_id=? AND bucket_start=?", 1, h14); n != 1 {
		t.Fatalf("RollupOnce must backfill the old %v bucket, found %d", h14, n)
	}
}

// TestHeartbeatFor: the gap heartbeat is max(1h floor, 2.5x poll interval), so a
// coarse poll cadence doesn't make every normal segment exceed the heartbeat and
// blank the chart, while small intervals keep the 1h floor.
func TestHeartbeatFor(t *testing.T) {
	cases := []struct {
		poll time.Duration
		want time.Duration
	}{
		{0, time.Hour},                       // unset → floor
		{5 * time.Minute, time.Hour},         // 2.5*5=12.5m < 1h → floor
		{24 * time.Minute, time.Hour},        // 2.5*24=60m == floor
		{30 * time.Minute, 75 * time.Minute}, // above the floor, scales 2.5x
		{90 * time.Minute, 225 * time.Minute},
	}
	for _, c := range cases {
		if got := heartbeatFor(c.poll); got != c.want {
			t.Errorf("heartbeatFor(%v) = %v, want %v", c.poll, got, c.want)
		}
	}
}

// TestHourFloor exercises the bucket-boundary math: any timestamp within
// a UTC hour truncates to the start of that hour, sub-second precision
// is dropped, and non-UTC inputs convert to UTC before truncating.
func TestHourFloor(t *testing.T) {
	la, err := time.LoadLocation("America/Los_Angeles")
	if err != nil {
		t.Fatalf("load LA tz: %v", err)
	}
	cases := []struct {
		name string
		in   time.Time
		want time.Time
	}{
		{"top of hour utc", time.Date(2026, 5, 17, 14, 0, 0, 0, time.UTC), time.Date(2026, 5, 17, 14, 0, 0, 0, time.UTC)},
		{"mid hour utc", time.Date(2026, 5, 17, 14, 37, 42, 123456789, time.UTC), time.Date(2026, 5, 17, 14, 0, 0, 0, time.UTC)},
		{"LA noon converts to 19:00 UTC", time.Date(2026, 5, 17, 12, 30, 0, 0, la), time.Date(2026, 5, 17, 19, 0, 0, 0, time.UTC)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := hourFloor(tc.in)
			if !got.Equal(tc.want) {
				t.Fatalf("hourFloor(%v) = %v, want %v", tc.in, got, tc.want)
			}
		})
	}
}
