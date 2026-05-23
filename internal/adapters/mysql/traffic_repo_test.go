package mysql

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
)

func TestTrafficSnapshotsReturnNotFoundWhenEmpty(t *testing.T) {
	db, err := Open("sqlite", filepath.Join(t.TempDir(), "panel.db"))
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

// TestLatestForUsers pins the v3.5.0-beta.9 batched read PollOnce now uses to
// pre-fetch every user's most-recent snapshot in one SQL call instead of
// N per-user LatestForUser SELECTs. Three properties matter:
//   1. tie-breaking matches LatestForUser exactly (the highest-id row wins,
//      so the batched form can't silently pick a different row when two
//      snapshots ever share a captured_at)
//   2. users with no snapshots are absent from the map (caller treats absence
//      as ErrNotFound)
//   3. empty input returns an empty map, not nil — so the caller can map-index
//      it without a nil guard
func TestLatestForUsers(t *testing.T) {
	db, err := Open("sqlite", filepath.Join(t.TempDir(), "panel.db"))
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
	db, err := Open("sqlite", filepath.Join(t.TempDir(), "panel.db"))
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
	mustInsert(t, &domain.TrafficSnapshot{UserID: 1, TotalBytes: 200, CapturedAt: cutoff})                     // keep (strict <)
	mustInsert(t, &domain.TrafficSnapshot{UserID: 1, TotalBytes: 300, CapturedAt: cutoff.Add(48 * time.Hour)}) // keep

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

