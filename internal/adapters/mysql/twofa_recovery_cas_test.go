package mysql

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
)

// TestConsumeRecoveryCode_CAS proves the recovery-code consume is an atomic
// compare-and-swap: a second consume carrying the SAME (now-stale) prior list
// loses, which is what stops two concurrent redemptions of one one-time code
// from both succeeding (the double-spend the review flagged).
func TestConsumeRecoveryCode_CAS(t *testing.T) {
	db, err := Open("sqlite", filepath.Join(t.TempDir(), "panel.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if err := EnsureSchema(db); err != nil {
		t.Fatalf("schema: %v", err)
	}
	t.Cleanup(func() {
		if s, e := db.DB(); e == nil {
			_ = s.Close()
		}
	})
	repo := NewRepos(db).User
	ctx := context.Background()

	u := &domain.User{UPN: "u@x", Role: domain.RoleUser, Enabled: true, SubToken: "t1", UUID: "uu", GroupID: 1}
	if err := repo.Create(ctx, u); err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := repo.SetRecoveryCodes(ctx, u.ID, []string{"A", "B", "C"}); err != nil {
		t.Fatalf("seed recovery: %v", err)
	}

	// First consume (prev == current) wins.
	won, err := repo.ConsumeRecoveryCode(ctx, u.ID, []string{"A", "B", "C"}, []string{"B", "C"})
	if err != nil || !won {
		t.Fatalf("first consume should win: won=%v err=%v", won, err)
	}
	// Second consume with the STALE prev must lose — the row already moved on.
	won2, err := repo.ConsumeRecoveryCode(ctx, u.ID, []string{"A", "B", "C"}, []string{"B", "C"})
	if err != nil {
		t.Fatalf("second consume err: %v", err)
	}
	if won2 {
		t.Fatal("stale-prev consume must LOSE the CAS — this is the double-spend guard")
	}
	// Stored list reflects only the winner's write.
	_, _, codes, err := repo.GetTOTP(ctx, u.ID)
	if err != nil {
		t.Fatalf("gettotp: %v", err)
	}
	if len(codes) != 2 || codes[0] != "B" || codes[1] != "C" {
		t.Fatalf("stored codes = %v, want [B C]", codes)
	}
	// A consume against the CURRENT value wins again.
	won3, err := repo.ConsumeRecoveryCode(ctx, u.ID, []string{"B", "C"}, []string{"C"})
	if err != nil || !won3 {
		t.Fatalf("current-prev consume should win: won=%v err=%v", won3, err)
	}
}
