package mysql

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
)

// TestOwnershipBatchUpdateCounters covers the v3.5.0-beta.9 batched counter
// flush: PollOnce now appends per-client counter updates to its sink and
// drains them via one transaction-wrapped batch call at end-of-cycle. The
// per-row column scope must match the single-row UpdateCounters; an aborted
// batch must not partially apply.
func TestOwnershipBatchUpdateCounters(t *testing.T) {
	db, err := Open("sqlite", filepath.Join(t.TempDir(), "panel.db"))
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
