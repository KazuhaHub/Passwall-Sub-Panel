package mysql

import (
	"context"
	"testing"
	"time"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
)

// TestAdvanceBlockViolationGatesOnWindow locks the atomic, dedup-gated
// increment that stops concurrent /sub fetches from losing increments or
// double-firing auto-disable: a second call inside the window updates 0 rows
// (advanced=false), and only after the window elapses does the count advance.
func TestAdvanceBlockViolationGatesOnWindow(t *testing.T) {
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

	repo := NewRepos(db).User
	ctx := context.Background()
	u := &domain.User{
		UPN: "v@example.test", Role: domain.RoleUser, SubToken: "st-v",
		UUID: "00000000-0000-0000-0000-0000000000aa", GroupID: 1,
		TrafficResetPeriod: domain.ResetMonthly, Enabled: true,
	}
	if err := repo.Create(ctx, u); err != nil {
		t.Fatalf("create: %v", err)
	}

	base := time.Date(2026, 5, 18, 12, 0, 0, 0, time.UTC)
	win := 10 * time.Minute

	// 1st violation (no prior) → advances to 1.
	count, advanced, err := repo.AdvanceBlockViolation(ctx, u.ID, base.Add(-win), base, "c1")
	if err != nil {
		t.Fatal(err)
	}
	if !advanced || count != 1 {
		t.Fatalf("1st: advanced=%v count=%d, want true,1", advanced, count)
	}

	// 2nd within the window (lastAt was just set to base; base < base-win is
	// false) → 0 rows, not advanced. This is the concurrent-fetch dedup.
	count, advanced, err = repo.AdvanceBlockViolation(ctx, u.ID, base.Add(-win), base.Add(time.Minute), "c2")
	if err != nil {
		t.Fatal(err)
	}
	if advanced || count != 0 {
		t.Fatalf("2nd (inside window): advanced=%v count=%d, want false,0", advanced, count)
	}

	// 3rd after the window elapsed → advances to 2 (not 3 — the 2nd was gated).
	later := base.Add(2 * win)
	count, advanced, err = repo.AdvanceBlockViolation(ctx, u.ID, later.Add(-win), later, "c3")
	if err != nil {
		t.Fatal(err)
	}
	if !advanced || count != 2 {
		t.Fatalf("3rd (after window): advanced=%v count=%d, want true,2", advanced, count)
	}
}
