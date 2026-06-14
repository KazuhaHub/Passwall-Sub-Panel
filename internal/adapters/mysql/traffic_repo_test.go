package mysql

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
)

func TestTrafficSnapshotsReturnNotFoundWhenEmpty(t *testing.T) {
	db, err := openTestDB(t)
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := EnsureSchema(db); err != nil {
		t.Fatalf("schema: %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("unwrap db: %v", err)
	}
	defer func() {
		if err := sqlDB.Close(); err != nil {
			t.Fatalf("close db: %v", err)
		}
	}()

	repo := NewRepos(db).Traffic
	ctx := context.Background()

	if _, err := repo.LatestForUser(ctx, 1); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("LatestForUser error = %v, want ErrNotFound", err)
	}
	if _, err := repo.LastBefore(ctx, 1, time.Now()); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("LastBefore error = %v, want ErrNotFound", err)
	}
}

// TestLastBeforeForUserClients pins the per-node usage view's "today" source:
// the most recent snapshot strictly before the cutoff, grouped per (panel,
// inbound, email) client, for one user — keyed by ClientMatchKey.
func TestLastBeforeForUserClients(t *testing.T) {
	db, err := openTestDB(t)
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := EnsureSchema(db); err != nil {
		t.Fatalf("schema: %v", err)
	}
	sqlDB, _ := db.DB()
	t.Cleanup(func() { _ = sqlDB.Close() })

	repo := NewRepos(db).Traffic
	ctx := context.Background()
	cutoff := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)

	mk := func(uid, panel int64, inbound int, email string, total int64, at time.Time) *domain.ClientTrafficSnapshot {
		return &domain.ClientTrafficSnapshot{UserID: uid, PanelID: panel, InboundID: inbound, ClientEmail: email, TotalBytes: total, CapturedAt: at}
	}
	seed := []*domain.ClientTrafficSnapshot{
		// user 1, node A: two before cutoff (latest wins) + one after (ignored)
		mk(1, 10, 20, "u1-nA@x", 100, cutoff.Add(-2*time.Hour)),
		mk(1, 10, 20, "u1-nA@x", 150, cutoff.Add(-1*time.Hour)),
		mk(1, 10, 20, "u1-nA@x", 999, cutoff.Add(1*time.Hour)),
		// user 1, node B: one before cutoff
		mk(1, 10, 21, "u1-nB@x", 70, cutoff.Add(-3*time.Hour)),
		// user 2: different user, must not leak into user 1's result
		mk(2, 10, 20, "u2-nA@x", 500, cutoff.Add(-1*time.Hour)),
	}
	if err := repo.InsertClientBatch(ctx, seed); err != nil {
		t.Fatalf("seed: %v", err)
	}

	got, err := repo.LastBeforeForUserClients(ctx, 1, cutoff)
	if err != nil {
		t.Fatalf("LastBeforeForUserClients: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("want 2 client keys for user 1, got %d (%v)", len(got), got)
	}
	if a := got[domain.ClientMatchKey(10, 20, "u1-nA@x")]; a == nil || a.TotalBytes != 150 {
		t.Errorf("node A latest-before = %v, want total 150 (not 100, not the post-cutoff 999)", a)
	}
	if b := got[domain.ClientMatchKey(10, 21, "u1-nB@x")]; b == nil || b.TotalBytes != 70 {
		t.Errorf("node B latest-before = %v, want total 70", b)
	}
	if _, leaked := got[domain.ClientMatchKey(10, 20, "u2-nA@x")]; leaked {
		t.Error("user 2's client leaked into user 1's result")
	}
}

// TestLatestForUsers pins the v3.5.0-beta.9 batched read PollOnce now uses to
// pre-fetch every user's most-recent snapshot in one SQL call instead of
// N per-user LatestForUser SELECTs. Three properties matter:
//  1. tie-breaking matches LatestForUser exactly (the highest-id row wins,
//     so the batched form can't silently pick a different row when two
//     snapshots ever share a captured_at)
//  2. users with no snapshots are absent from the map (caller treats absence
//     as ErrNotFound)
//  3. empty input returns an empty map, not nil — so the caller can map-index
//     it without a nil guard
func TestLatestForUsers(t *testing.T) {
	db, err := openTestDB(t)
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := EnsureSchema(db); err != nil {
		t.Fatalf("schema: %v", err)
	}
	sqlDB, _ := db.DB()
	defer sqlDB.Close()

	repo := NewRepos(db).Traffic
	ctx := context.Background()

	// User 1 — two snapshots, second is newer.
	if err := repo.Insert(ctx, &domain.TrafficSnapshot{UserID: 1, TotalBytes: 100, CapturedAt: time.Now().Add(-time.Hour)}); err != nil {
		t.Fatal(err)
	}
	if err := repo.Insert(ctx, &domain.TrafficSnapshot{UserID: 1, TotalBytes: 200, CapturedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	// User 2 — tie-breaker case: two snapshots sharing captured_at. The
	// higher-id row must win, matching LatestForUser's `Order("id DESC")
	// .Limit(1)`. A naive MAX(captured_at) JOIN would return both.
	tied := time.Now().Add(-30 * time.Minute)
	if err := repo.Insert(ctx, &domain.TrafficSnapshot{UserID: 2, TotalBytes: 300, CapturedAt: tied}); err != nil {
		t.Fatal(err)
	}
	if err := repo.Insert(ctx, &domain.TrafficSnapshot{UserID: 2, TotalBytes: 400, CapturedAt: tied}); err != nil {
		t.Fatal(err)
	}
	// User 3 — never seen, must be absent from the result map.

	got, err := repo.LatestForUsers(ctx, []int64{1, 2, 3})
	if err != nil {
		t.Fatalf("LatestForUsers: %v", err)
	}
	if got[1] == nil || got[1].TotalBytes != 200 {
		t.Errorf("user 1 latest = %+v, want TotalBytes 200", got[1])
	}
	if got[2] == nil || got[2].TotalBytes != 400 {
		t.Errorf("user 2 latest = %+v, want TotalBytes 400 (highest-id tie-break)", got[2])
	}
	if _, ok := got[3]; ok {
		t.Errorf("user 3 should be absent from the map (no prior snapshot), got %+v", got[3])
	}
	// Cross-check against the single-user form for user 1 — they must agree.
	one, err := repo.LatestForUser(ctx, 1)
	if err != nil {
		t.Fatal(err)
	}
	if got[1].ID != one.ID {
		t.Errorf("batch picked id %d for user 1, single picked id %d — semantics drift", got[1].ID, one.ID)
	}

	// Empty input — empty map, no nil deref, no error.
	empty, err := repo.LatestForUsers(ctx, nil)
	if err != nil {
		t.Fatalf("empty input: %v", err)
	}
	if empty == nil {
		t.Fatal("empty input returned nil map; callers will panic on map-index")
	}
	if len(empty) != 0 {
		t.Errorf("empty input map size = %d, want 0", len(empty))
	}
}

// TestTrafficPruneBefore covers the v3.0.0 retention DELETE — guards against
// indexing regressions (the captured_at single-column index is what makes
// this query a range-scan instead of full-table). Verifies that both
// traffic_snapshots and client_traffic_snapshots are pruned in one call,
// and that the cutoff comparison is strict (rows AT cutoff survive).
func TestTrafficPruneBefore(t *testing.T) {
	db, err := openTestDB(t)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if err := EnsureSchema(db); err != nil {
		t.Fatalf("schema: %v", err)
	}
	sqlDB, _ := db.DB()
	defer sqlDB.Close()

	repo := NewRepos(db).Traffic
	ctx := context.Background()
	cutoff := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)

	mustInsert := func(t *testing.T, s *domain.TrafficSnapshot) {
		t.Helper()
		if err := repo.Insert(ctx, s); err != nil {
			t.Fatalf("insert: %v", err)
		}
	}
	mustInsertClient := func(t *testing.T, s *domain.ClientTrafficSnapshot) {
		t.Helper()
		if err := repo.InsertClient(ctx, s); err != nil {
			t.Fatalf("insert client: %v", err)
		}
	}

	mustInsert(t, &domain.TrafficSnapshot{UserID: 1, TotalBytes: 100, CapturedAt: cutoff.Add(-48 * time.Hour)}) // prune
	mustInsert(t, &domain.TrafficSnapshot{UserID: 1, TotalBytes: 200, CapturedAt: cutoff})                      // keep (strict <)
	mustInsert(t, &domain.TrafficSnapshot{UserID: 1, TotalBytes: 300, CapturedAt: cutoff.Add(48 * time.Hour)})  // keep

	mustInsertClient(t, &domain.ClientTrafficSnapshot{UserID: 1, PanelID: 10, InboundID: 1, ClientEmail: "a@x", TotalBytes: 10, CapturedAt: cutoff.Add(-time.Hour)}) // prune
	mustInsertClient(t, &domain.ClientTrafficSnapshot{UserID: 1, PanelID: 10, InboundID: 1, ClientEmail: "a@x", TotalBytes: 20, CapturedAt: cutoff.Add(time.Hour)})  // keep

	deleted, err := repo.PruneBefore(ctx, cutoff)
	if err != nil {
		t.Fatalf("PruneBefore: %v", err)
	}
	// 1 traffic_snapshot + 1 client_traffic_snapshot deleted = 2.
	if deleted != 2 {
		t.Errorf("deleted = %d, want 2", deleted)
	}

	since := time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC)
	until := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
	rows, err := repo.ListByUser(ctx, 1, since, until)
	if err != nil {
		t.Fatalf("ListByUser: %v", err)
	}
	if len(rows) != 2 {
		t.Errorf("rows after prune = %d, want 2 (cutoff row kept + later row kept)", len(rows))
	}
}

// TestListHourlyByUserAndNode covers the v3.6.x hourly chart readers: range
// filtering ([since, until) on the UTC bucket_start), ascending order, and
// that the wrong user/node and out-of-range buckets are excluded. These are
// the SOLE source for HistoryFor / NodeHistoryFor.
func TestListHourlyByUserAndNode(t *testing.T) {
	db, err := openTestDB(t)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if err := EnsureSchema(db); err != nil {
		t.Fatalf("schema: %v", err)
	}
	sqlDB, _ := db.DB()
	defer sqlDB.Close()

	ctx := context.Background()
	h := func(s string) time.Time {
		ts, perr := time.Parse(time.RFC3339, s)
		if perr != nil {
			t.Fatalf("parse %q: %v", s, perr)
		}
		return ts.UTC()
	}
	// Seed user-hourly buckets directly (rollup is the real writer).
	for _, r := range []struct {
		uid             int64
		at              string
		up, down, total int64
	}{
		{1, "2026-05-10T08:00:00Z", 10, 20, 30},
		{1, "2026-05-10T12:00:00Z", 5, 5, 10},
		{1, "2026-05-12T00:00:00Z", 1, 1, 2},     // outside [10th,11th) query range
		{2, "2026-05-10T09:00:00Z", 99, 99, 198}, // wrong user
	} {
		if err := db.Table("traffic_snapshots_hourly").Create(map[string]any{
			"user_id": r.uid, "bucket_start": h(r.at), "up_bytes": r.up, "down_bytes": r.down, "total_bytes": r.total,
		}).Error; err != nil {
			t.Fatalf("seed user hourly: %v", err)
		}
	}

	repo := NewRepos(db).Traffic
	// [10th 00:00, 11th 00:00) — only user 1's two May-10 buckets, ascending.
	got, err := repo.ListHourlyByUser(ctx, 1, h("2026-05-10T00:00:00Z"), h("2026-05-11T00:00:00Z"))
	if err != nil {
		t.Fatalf("ListHourlyByUser: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("user hourly rows = %d, want 2 (range + user filter)", len(got))
	}
	if !got[0].BucketStart.Before(got[1].BucketStart) {
		t.Fatalf("rows not ascending by bucket_start: %v", got)
	}
	if got[0].TotalBytes != 30 || got[1].TotalBytes != 10 {
		t.Fatalf("totals = %d,%d want 30,10", got[0].TotalBytes, got[1].TotalBytes)
	}

	// Node tier.
	nrepo := NewRepos(db).NodeTraffic
	for _, r := range []struct {
		nid             int64
		at              string
		up, down, total int64
	}{
		{7, "2026-05-10T08:00:00Z", 40, 60, 100},
		{7, "2026-05-20T08:00:00Z", 1, 1, 2}, // out of range
	} {
		if err := db.Table("node_traffic_snapshots_hourly").Create(map[string]any{
			"node_id": r.nid, "bucket_start": h(r.at), "up_bytes": r.up, "down_bytes": r.down, "total_bytes": r.total,
		}).Error; err != nil {
			t.Fatalf("seed node hourly: %v", err)
		}
	}
	ngot, err := nrepo.ListHourlyByNode(ctx, 7, h("2026-05-10T00:00:00Z"), h("2026-05-11T00:00:00Z"))
	if err != nil {
		t.Fatalf("ListHourlyByNode: %v", err)
	}
	if len(ngot) != 1 || ngot[0].TotalBytes != 100 {
		t.Fatalf("node hourly = %+v, want 1 row total 100 (range filter excludes May-20)", ngot)
	}
}

// TestSumHourlyAllUsersAndNodes covers the all-scope chart's single-query SUM
// (the N+1 fix): per-bucket SUM(up/down/total) across EVERY user/node, range-
// filtered, one row per bucket_start ascending. Verifies the GORM GROUP BY +
// aggregate-alias mapping actually works against the real driver.
func TestSumHourlyAllUsersAndNodes(t *testing.T) {
	db, err := openTestDB(t)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if err := EnsureSchema(db); err != nil {
		t.Fatalf("schema: %v", err)
	}
	sqlDB, _ := db.DB()
	defer sqlDB.Close()

	ctx := context.Background()
	h := func(s string) time.Time {
		ts, perr := time.Parse(time.RFC3339, s)
		if perr != nil {
			t.Fatalf("parse %q: %v", s, perr)
		}
		return ts.UTC()
	}
	// Users 1 and 2 share the 08:00 bucket (must sum); user 1 also has 12:00; one
	// row is outside the query range.
	for _, r := range []struct {
		uid             int64
		at              string
		up, down, total int64
	}{
		{1, "2026-05-10T08:00:00Z", 10, 20, 30},
		{2, "2026-05-10T08:00:00Z", 5, 5, 10},
		{1, "2026-05-10T12:00:00Z", 7, 3, 10},
		{2, "2026-05-12T00:00:00Z", 99, 99, 198}, // out of range
	} {
		if err := db.Table("traffic_snapshots_hourly").Create(map[string]any{
			"user_id": r.uid, "bucket_start": h(r.at), "up_bytes": r.up, "down_bytes": r.down, "total_bytes": r.total,
		}).Error; err != nil {
			t.Fatalf("seed user hourly: %v", err)
		}
	}
	repo := NewRepos(db).Traffic
	got, err := repo.SumHourlyAllUsers(ctx, h("2026-05-10T00:00:00Z"), h("2026-05-11T00:00:00Z"))
	if err != nil {
		t.Fatalf("SumHourlyAllUsers: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("buckets = %d, want 2 (one row per bucket_start, range-filtered)", len(got))
	}
	if !got[0].BucketStart.Before(got[1].BucketStart) {
		t.Fatalf("not ascending by bucket_start: %v", got)
	}
	if got[0].UpBytes != 15 || got[0].DownBytes != 25 || got[0].TotalBytes != 40 {
		t.Fatalf("08:00 summed up/down/total = %d/%d/%d, want 15/25/40 (user1+user2)",
			got[0].UpBytes, got[0].DownBytes, got[0].TotalBytes)
	}
	if got[1].TotalBytes != 10 {
		t.Fatalf("12:00 total = %d, want 10", got[1].TotalBytes)
	}

	nrepo := NewRepos(db).NodeTraffic
	for _, r := range []struct {
		nid             int64
		at              string
		up, down, total int64
	}{
		{7, "2026-05-10T08:00:00Z", 40, 60, 100},
		{8, "2026-05-10T08:00:00Z", 1, 2, 3},
	} {
		if err := db.Table("node_traffic_snapshots_hourly").Create(map[string]any{
			"node_id": r.nid, "bucket_start": h(r.at), "up_bytes": r.up, "down_bytes": r.down, "total_bytes": r.total,
		}).Error; err != nil {
			t.Fatalf("seed node hourly: %v", err)
		}
	}
	ngot, err := nrepo.SumHourlyAllNodes(ctx, h("2026-05-10T00:00:00Z"), h("2026-05-11T00:00:00Z"))
	if err != nil {
		t.Fatalf("SumHourlyAllNodes: %v", err)
	}
	if len(ngot) != 1 || ngot[0].TotalBytes != 103 {
		t.Fatalf("node summed = %+v, want 1 bucket total 103 (node7+node8)", ngot)
	}
}
