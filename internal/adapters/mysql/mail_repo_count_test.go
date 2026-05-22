package mysql

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
)

// TestMailRepoCountSentInWindow covers the per-day cap primitive behind the
// blocked-client warning: CountSentInWindow counts (user, kind) rows whose
// window_key starts with the date prefix, so the mailer can stop after N sends
// in a day. Each send uses a distinct "date#seq" window_key.
func TestMailRepoCountSentInWindow(t *testing.T) {
	db, err := Open("sqlite", filepath.Join(t.TempDir(), "panel.db"))
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
