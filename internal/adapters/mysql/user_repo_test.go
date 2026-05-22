package mysql

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

func TestCreateUsersWithUPN(t *testing.T) {
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

	repo := NewRepos(db).User
	ctx := context.Background()
	users := []*domain.User{
		{
			UPN:                "alice@example.test",
			PasswordHash:       "hash",
			Role:               domain.RoleUser,
			SubToken:           "sub-token-alice",
			UUID:               "00000000-0000-0000-0000-000000000001",
			GroupID:            1,
			TrafficResetPeriod: domain.ResetMonthly,
			Enabled:            true,
		},
		{
			UPN:                "bob@example.test",
			PasswordHash:       "hash",
			Role:               domain.RoleUser,
			SubToken:           "sub-token-bob",
			UUID:               "00000000-0000-0000-0000-000000000002",
			GroupID:            1,
			TrafficResetPeriod: domain.ResetMonthly,
			Enabled:            true,
		},
	}

	for _, u := range users {
		if err := repo.Create(ctx, u); err != nil {
			t.Fatalf("create %s: %v", u.UPN, err)
		}
	}
}

// TestUpdateTrafficStatePreservesEmergency pins the v3.3.0-beta.6 fix: the
// per-cycle traffic write must NOT touch the emergency-access columns, so a
// poll that loaded a stale user snapshot can't silently revoke an emergency
// window granted concurrently mid-cycle. ClearEmergencyAccess is the only poll
// path allowed to clear it.
func TestUpdateTrafficStatePreservesEmergency(t *testing.T) {
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
	repo := NewRepos(db).User
	ctx := context.Background()

	u := &domain.User{
		UPN: "carol@example.test", PasswordHash: "h", Role: domain.RoleUser,
		SubToken: "sub-carol", UUID: "00000000-0000-0000-0000-000000000003",
		GroupID: 1, TrafficResetPeriod: domain.ResetMonthly, Enabled: true,
	}
	if err := repo.Create(ctx, u); err != nil {
		t.Fatalf("create: %v", err)
	}

	// Simulate UseEmergencyAccess granting a window (full-row Update).
	until := timeNowUTCPlusHour()
	u.EmergencyUntil = &until
	u.EmergencyBaselineBytes = 5 << 30
	if err := repo.Update(ctx, u); err != nil {
		t.Fatalf("grant emergency: %v", err)
	}

	// A stale poll snapshot with NO emergency calls UpdateTrafficState. The fix
	// means this must NOT clobber the live grant.
	stale := &domain.User{ID: u.ID, LifetimeTotalBytes: 1 << 20}
	if err := repo.UpdateTrafficState(ctx, stale); err != nil {
		t.Fatalf("UpdateTrafficState: %v", err)
	}
	got, err := repo.GetByID(ctx, u.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.EmergencyUntil == nil {
		t.Fatal("UpdateTrafficState clobbered emergency_until — the stale poll write revoked a live grant")
	}
	if got.EmergencyBaselineBytes != 5<<30 {
		t.Fatalf("emergency_baseline_bytes = %d, want %d", got.EmergencyBaselineBytes, int64(5<<30))
	}
	if got.LifetimeTotalBytes != 1<<20 {
		t.Fatalf("lifetime_total_bytes = %d, want %d (poll-owned column should persist)", got.LifetimeTotalBytes, 1<<20)
	}

	// ClearEmergencyAccess is the explicit path that ends the window.
	if err := repo.ClearEmergencyAccess(ctx, u.ID); err != nil {
		t.Fatalf("ClearEmergencyAccess: %v", err)
	}
	got, err = repo.GetByID(ctx, u.ID)
	if err != nil {
		t.Fatalf("get after clear: %v", err)
	}
	if got.EmergencyUntil != nil || got.EmergencyBaselineBytes != 0 {
		t.Fatalf("ClearEmergencyAccess did not clear: until=%v baseline=%d", got.EmergencyUntil, got.EmergencyBaselineBytes)
	}
}

func timeNowUTCPlusHour() time.Time { return time.Now().UTC().Add(time.Hour) }

// TestListSearchIsCaseInsensitive locks the contract that the admin user
// search ignores case. It passes trivially on SQLite (whose LIKE is already
// ASCII-case-insensitive); its real job is to pin the LOWER()-based query so a
// future edit can't silently drop it and regress on Postgres, where LIKE is
// case-sensitive. The same SQL runs verbatim on all three backends.
func TestListSearchIsCaseInsensitive(t *testing.T) {
	db, err := Open("sqlite", filepath.Join(t.TempDir(), "panel.db"))
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := EnsureSchema(db); err != nil {
		t.Fatalf("schema: %v", err)
	}
	sqlDB, _ := db.DB()
	defer sqlDB.Close()

	repo := NewRepos(db).User
	ctx := context.Background()
	u := &domain.User{
		UPN:                "Carol@Example.Test",
		DisplayName:        "Carol Danvers",
		Email:              "Carol@Example.Test",
		PasswordHash:       "hash",
		Role:               domain.RoleUser,
		SubToken:           "sub-token-carol",
		UUID:               "00000000-0000-0000-0000-000000000003",
		GroupID:            1,
		TrafficResetPeriod: domain.ResetMonthly,
		Enabled:            true,
	}
	if err := repo.Create(ctx, u); err != nil {
		t.Fatalf("create: %v", err)
	}

	// Each query uses a different case than the stored value.
	for _, q := range []string{"carol", "CAROL", "example.test", "DANVERS"} {
		got, total, err := repo.List(ctx, ports.UserFilter{Search: q})
		if err != nil {
			t.Fatalf("list search %q: %v", q, err)
		}
		if total != 1 || len(got) != 1 {
			t.Fatalf("search %q: got total=%d len=%d, want exactly 1 match", q, total, len(got))
		}
	}
}
