package sqlstore

import (
	"context"
	"testing"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
)

// TestMailRepoCountSentInWindow covers the per-day cap primitive behind the
// blocked-client warning: CountSentInWindow counts (user, kind) rows whose
// window_key starts with the date prefix, so the mailer can stop after N sends
// in a day. Each send uses a distinct "date#seq" window_key.
func TestMailRepoCountSentInWindow(t *testing.T) {
	db, err := openTestDB(t)
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	t.Cleanup(func() {
		if sqlDB, _ := db.DB(); sqlDB != nil {
			_ = sqlDB.Close()
		}
	})
	if err := EnsureSchema(db); err != nil {
		t.Fatalf("schema: %v", err)
	}
	repo := &mailRepo{db: db}
	ctx := context.Background()
	k := domain.MailReminderBlockedClient

	for _, rec := range []struct {
		uid       int64
		windowKey string
	}{
		{1, "2026-05-21#0"},
		{1, "2026-05-21#1"},
		{1, "2026-05-20#0"},
		{2, "2026-05-21#0"},
	} {
		if err := repo.RecordSent(ctx, rec.uid, k, rec.windowKey, "u@example.com"); err != nil {
			t.Fatalf("RecordSent %v: %v", rec, err)
		}
	}

	cases := []struct {
		name      string
		uid       int64
		prefix    string
		wantCount int64
	}{
		{"user1 today = 2", 1, "2026-05-21", 2},
		{"user1 yesterday = 1", 1, "2026-05-20", 1},
		{"user1 other day = 0", 1, "2026-05-19", 0},
		{"user2 today = 1 (scoped per user)", 2, "2026-05-21", 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := repo.CountSentInWindow(ctx, tc.uid, k, tc.prefix)
			if err != nil {
				t.Fatalf("CountSentInWindow: %v", err)
			}
			if got != tc.wantCount {
				t.Fatalf("count(uid=%d, prefix=%q) = %d, want %d", tc.uid, tc.prefix, got, tc.wantCount)
			}
		})
	}
}

// TestMailRepoReserveSentSlot covers the race-safe cap primitive: the first
// reservation of a (user, kind, window_key) wins (true), a second on the same
// key loses (false) — so concurrent blocked-client warnings can't both clear
// the same per-day slot. A different key, user, or kind reserves independently.
func TestMailRepoReserveSentSlot(t *testing.T) {
	db, err := openTestDB(t)
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	t.Cleanup(func() {
		if sqlDB, _ := db.DB(); sqlDB != nil {
			_ = sqlDB.Close()
		}
	})
	if err := EnsureSchema(db); err != nil {
		t.Fatalf("schema: %v", err)
	}
	repo := &mailRepo{db: db}
	ctx := context.Background()
	k := domain.MailReminderBlockedClient

	// First reservation of slot 0 wins.
	won, err := repo.ReserveSentSlot(ctx, 1, k, "2026-05-21#0", "u@example.com")
	if err != nil {
		t.Fatalf("ReserveSentSlot first: %v", err)
	}
	if !won {
		t.Fatal("first reservation should win the slot")
	}

	// Second reservation of the SAME slot loses (the race-safety guarantee).
	won, err = repo.ReserveSentSlot(ctx, 1, k, "2026-05-21#0", "u@example.com")
	if err != nil {
		t.Fatalf("ReserveSentSlot dup: %v", err)
	}
	if won {
		t.Fatal("second reservation of the same slot must lose")
	}

	// A different slot / user / kind each reserve independently.
	for _, tc := range []struct {
		name      string
		uid       int64
		kind      domain.MailReminderKind
		windowKey string
	}{
		{"next slot", 1, k, "2026-05-21#1"},
		{"other user", 2, k, "2026-05-21#0"},
		{"other kind", 1, domain.MailReminderExpired, "2026-05-21#0"},
	} {
		won, err := repo.ReserveSentSlot(ctx, tc.uid, tc.kind, tc.windowKey, "u@example.com")
		if err != nil {
			t.Fatalf("ReserveSentSlot %s: %v", tc.name, err)
		}
		if !won {
			t.Fatalf("%s should win an independent slot", tc.name)
		}
	}
}
