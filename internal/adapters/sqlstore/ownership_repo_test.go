package sqlstore

import (
	"context"
	"testing"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
)

// TestOwnershipBatchUpdateCounters covers the v3.5.0-beta.9 batched counter
// flush: PollOnce now appends per-client counter updates to its sink and
// drains them via one transaction-wrapped batch call at end-of-cycle. The
// per-row column scope must match the single-row UpdateCounters; an aborted
// batch must not partially apply.
func TestOwnershipBatchUpdateCounters(t *testing.T) {
	db, err := openTestDB(t)
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := EnsureSchema(db); err != nil {
		t.Fatalf("schema: %v", err)
	}
	t.Cleanup(func() {
		if sqlDB, _ := db.DB(); sqlDB != nil {
			_ = sqlDB.Close()
		}
	})

	repo := NewRepos(db).Ownership
	ctx := context.Background()

	mk := func(email string) *domain.XUIClientEntry {
		return &domain.XUIClientEntry{
			UserID: 1, PanelID: 10, InboundID: 20,
			ClientEmail: email,
			ClientUUID:  "00000000-0000-0000-0000-000000000000",
		}
	}
	a := mk("a@example.test")
	b := mk("b@example.test")
	c := mk("c@example.test")
	for _, e := range []*domain.XUIClientEntry{a, b, c} {
		if err := repo.Add(ctx, e); err != nil {
			t.Fatalf("add %s: %v", e.ClientEmail, err)
		}
	}

	t.Run("happy path writes lifetime + last-raw on every row", func(t *testing.T) {
		updates := []*domain.XUIClientEntry{
			{ID: a.ID, LifetimeUpBytes: 11, LifetimeDownBytes: 22, LifetimeTotalBytes: 33, LastRawUpBytes: 100, LastRawDownBytes: 200, LastRawTotalBytes: 300},
			{ID: b.ID, LifetimeUpBytes: 44, LifetimeDownBytes: 55, LifetimeTotalBytes: 99, LastRawUpBytes: 400, LastRawDownBytes: 500, LastRawTotalBytes: 900},
			{ID: c.ID, LifetimeUpBytes: 66, LifetimeDownBytes: 77, LifetimeTotalBytes: 143, LastRawUpBytes: 600, LastRawDownBytes: 700, LastRawTotalBytes: 1300},
		}
		if err := repo.BatchUpdateCounters(ctx, updates); err != nil {
			t.Fatalf("BatchUpdateCounters: %v", err)
		}
		for _, want := range updates {
			got, err := repo.GetByMatch(ctx, 10, 20, lookupEmail(want, []*domain.XUIClientEntry{a, b, c}))
			if err != nil {
				t.Fatalf("get %d: %v", want.ID, err)
			}
			if got.LifetimeTotalBytes != want.LifetimeTotalBytes {
				t.Errorf("id %d lifetime_total = %d, want %d", want.ID, got.LifetimeTotalBytes, want.LifetimeTotalBytes)
			}
			if got.LastRawTotalBytes != want.LastRawTotalBytes {
				t.Errorf("id %d last_raw_total = %d, want %d", want.ID, got.LastRawTotalBytes, want.LastRawTotalBytes)
			}
			// PanelID / InboundID / ClientEmail must be untouched (narrow
			// write contract — the batch must not rewrite identity columns).
			if got.PanelID != 10 || got.InboundID != 20 {
				t.Errorf("id %d identity columns rewritten: panel=%d inbound=%d", want.ID, got.PanelID, got.InboundID)
			}
		}
	})

	t.Run("empty input is a no-op", func(t *testing.T) {
		if err := repo.BatchUpdateCounters(ctx, nil); err != nil {
			t.Errorf("nil input: %v", err)
		}
		if err := repo.BatchUpdateCounters(ctx, []*domain.XUIClientEntry{}); err != nil {
			t.Errorf("empty slice: %v", err)
		}
	})

	t.Run("zero-ID row aborts the whole batch", func(t *testing.T) {
		preA, _ := repo.GetByMatch(ctx, 10, 20, "a@example.test")
		bad := []*domain.XUIClientEntry{
			{ID: a.ID, LifetimeTotalBytes: 999_999},
			{ID: 0, LifetimeTotalBytes: 1},
		}
		if err := repo.BatchUpdateCounters(ctx, bad); err == nil {
			t.Fatal("BatchUpdateCounters accepted zero-ID row, want error")
		}
		postA, _ := repo.GetByMatch(ctx, 10, 20, "a@example.test")
		if postA.LifetimeTotalBytes != preA.LifetimeTotalBytes {
			t.Errorf("a.LifetimeTotalBytes = %d after aborted batch, want unchanged %d (no rollback)",
				postA.LifetimeTotalBytes, preA.LifetimeTotalBytes)
		}
	})
}

// TestOwnershipListByUsers pins the v3.5.0-beta.15 batched-read shape:
// one SQL roundtrip buckets every requested user's ownership rows, users
// without rows are absent (not nil-valued), and empty input returns an
// empty non-nil map so callers don't need a guard.
func TestOwnershipListByUsers(t *testing.T) {
	db, err := openTestDB(t)
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := EnsureSchema(db); err != nil {
		t.Fatalf("schema: %v", err)
	}
	t.Cleanup(func() {
		if sqlDB, _ := db.DB(); sqlDB != nil {
			_ = sqlDB.Close()
		}
	})

	repo := NewRepos(db).Ownership
	ctx := context.Background()

	mk := func(uid int64, email string) *domain.XUIClientEntry {
		return &domain.XUIClientEntry{
			UserID: uid, PanelID: 10, InboundID: 20,
			ClientEmail: email,
			ClientUUID:  "00000000-0000-0000-0000-000000000000",
		}
	}
	// user 1 → 2 rows, user 2 → 1 row, user 3 → 0 rows
	entries := []*domain.XUIClientEntry{
		mk(1, "u1-c0@example.test"),
		mk(1, "u1-c1@example.test"),
		mk(2, "u2-c0@example.test"),
	}
	for _, e := range entries {
		if err := repo.Add(ctx, e); err != nil {
			t.Fatalf("add %s: %v", e.ClientEmail, err)
		}
	}

	t.Run("buckets rows by user_id; absent users omitted", func(t *testing.T) {
		got, err := repo.ListByUsers(ctx, []int64{1, 2, 3})
		if err != nil {
			t.Fatalf("ListByUsers: %v", err)
		}
		if len(got[1]) != 2 {
			t.Errorf("user 1 rows = %d, want 2", len(got[1]))
		}
		if len(got[2]) != 1 {
			t.Errorf("user 2 rows = %d, want 1", len(got[2]))
		}
		if _, ok := got[3]; ok {
			t.Errorf("user 3 should be absent from the map (no rows), got %+v", got[3])
		}
		// Cross-check against the single-user form for user 1 — same shape.
		single, err := repo.ListByUser(ctx, 1)
		if err != nil {
			t.Fatal(err)
		}
		if len(single) != len(got[1]) {
			t.Errorf("single ListByUser returned %d rows, batch returned %d — semantics drift",
				len(single), len(got[1]))
		}
	})

	t.Run("empty input returns empty non-nil map", func(t *testing.T) {
		got, err := repo.ListByUsers(ctx, nil)
		if err != nil {
			t.Fatalf("nil input: %v", err)
		}
		if got == nil {
			t.Fatal("nil-input returned nil map; callers will panic on map-index")
		}
		if len(got) != 0 {
			t.Errorf("nil-input map size = %d, want 0", len(got))
		}
	})
}

// lookupEmail maps a (ID-only) update entry back to its stored row's email
// by walking the originals. Lets the happy-path loop use GetByMatch instead
// of inventing a GetByID on the ownership repo.
func lookupEmail(target *domain.XUIClientEntry, originals []*domain.XUIClientEntry) string {
	for _, o := range originals {
		if o.ID == target.ID {
			return o.ClientEmail
		}
	}
	return ""
}
