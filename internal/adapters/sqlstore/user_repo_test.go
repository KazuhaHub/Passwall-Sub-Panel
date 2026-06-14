package sqlstore

import (
	"context"
	"testing"
	"time"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

func TestCreateUsersWithUPN(t *testing.T) {
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
		UPN: "carol@example.test", PasswordHash: "h", Role: domain.RoleUser,
		SubToken: "sub-carol", UUID: "00000000-0000-0000-0000-000000000003",
		GroupID: 1, TrafficResetPeriod: domain.ResetMonthly, Enabled: true,
	}
	if err := repo.Create(ctx, u); err != nil {
		t.Fatalf("create: %v", err)
	}

	// Simulate UseEmergencyAccess granting a window via the dedicated writer.
	// (The broad Update now OMITS the emergency columns — see pollOwnedColumns —
	// so emergency state is set only through GrantEmergencyAccess.)
	until := timeNowUTCPlusHour()
	if err := repo.GrantEmergencyAccess(ctx, u.ID, until, 1, 5<<30); err != nil {
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

// TestBatchUpdateTrafficState pins three properties of the v3.5.0-beta.9
// batched write that PollOnce now uses end-of-cycle:
//  1. happy path: every row's traffic-owned columns land
//  2. emergency columns are preserved (mirrors TestUpdateTrafficStatePreservesEmergency
//     — the batch path must honor the same column scope, otherwise the
//     "stale poll snapshot revokes a live emergency grant" race comes back)
//  3. failure path: a zero-ID row aborts the batch (transaction rolls back,
//     no partial writes)
func TestBatchUpdateTrafficState(t *testing.T) {
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

	// Three users; the middle one has a live emergency grant we must not
	// clobber.
	mk := func(upn, sub, uuid string) *domain.User {
		return &domain.User{
			UPN: upn, PasswordHash: "h", Role: domain.RoleUser,
			SubToken: sub, UUID: uuid,
			GroupID: 1, TrafficResetPeriod: domain.ResetMonthly, Enabled: true,
		}
	}
	a := mk("a@example.test", "sub-a", "00000000-0000-0000-0000-00000000000a")
	b := mk("b@example.test", "sub-b", "00000000-0000-0000-0000-00000000000b")
	c := mk("c@example.test", "sub-c", "00000000-0000-0000-0000-00000000000c")
	for _, u := range []*domain.User{a, b, c} {
		if err := repo.Create(ctx, u); err != nil {
			t.Fatalf("create %s: %v", u.UPN, err)
		}
	}
	// Grant via the dedicated writer; the broad Update no longer writes the
	// emergency columns (pollOwnedColumns).
	until := timeNowUTCPlusHour()
	if err := repo.GrantEmergencyAccess(ctx, b.ID, until, 1, 5<<30); err != nil {
		t.Fatalf("grant emergency on b: %v", err)
	}

	t.Run("happy path writes all rows in one tx", func(t *testing.T) {
		// Carry only the columns the poll owns; the batch must NOT touch
		// the rest. ID is required.
		updates := []*domain.User{
			{ID: a.ID, LifetimeUpBytes: 1, LifetimeDownBytes: 2, LifetimeTotalBytes: 3, PeriodBaselineBytes: 10},
			{ID: b.ID, LifetimeUpBytes: 4, LifetimeDownBytes: 5, LifetimeTotalBytes: 9, PeriodBaselineBytes: 20},
			{ID: c.ID, LifetimeUpBytes: 6, LifetimeDownBytes: 7, LifetimeTotalBytes: 13, PeriodBaselineBytes: 30},
		}
		if err := repo.BatchUpdateTrafficState(ctx, updates); err != nil {
			t.Fatalf("BatchUpdateTrafficState: %v", err)
		}
		for _, want := range updates {
			got, err := repo.GetByID(ctx, want.ID)
			if err != nil {
				t.Fatalf("get %d: %v", want.ID, err)
			}
			if got.LifetimeTotalBytes != want.LifetimeTotalBytes {
				t.Errorf("user %d lifetime_total = %d, want %d", want.ID, got.LifetimeTotalBytes, want.LifetimeTotalBytes)
			}
			if got.PeriodBaselineBytes != want.PeriodBaselineBytes {
				t.Errorf("user %d period_baseline = %d, want %d", want.ID, got.PeriodBaselineBytes, want.PeriodBaselineBytes)
			}
		}
		// Emergency on b must be untouched.
		gotB, _ := repo.GetByID(ctx, b.ID)
		if gotB.EmergencyUntil == nil {
			t.Error("batch path clobbered emergency_until on b — the v3.3.0-beta.6 invariant must hold for batched writes too")
		}
		if gotB.EmergencyBaselineBytes != 5<<30 {
			t.Errorf("emergency_baseline_bytes = %d, want %d", gotB.EmergencyBaselineBytes, int64(5<<30))
		}
	})

	t.Run("empty input is a no-op", func(t *testing.T) {
		if err := repo.BatchUpdateTrafficState(ctx, nil); err != nil {
			t.Errorf("nil input: %v", err)
		}
		if err := repo.BatchUpdateTrafficState(ctx, []*domain.User{}); err != nil {
			t.Errorf("empty slice: %v", err)
		}
	})

	t.Run("zero-ID row aborts the whole batch", func(t *testing.T) {
		// Re-read current values; the batch should NOT have partially
		// applied the good row before hitting the bad one.
		preA, _ := repo.GetByID(ctx, a.ID)
		bad := []*domain.User{
			{ID: a.ID, LifetimeTotalBytes: 999_999},
			{ID: 0, LifetimeTotalBytes: 1}, // missing ID — must reject
		}
		if err := repo.BatchUpdateTrafficState(ctx, bad); err == nil {
			t.Fatal("BatchUpdateTrafficState accepted zero-ID row, want error")
		}
		postA, _ := repo.GetByID(ctx, a.ID)
		if postA.LifetimeTotalBytes != preA.LifetimeTotalBytes {
			t.Errorf("a.LifetimeTotalBytes = %d after aborted batch, want unchanged %d (transaction did not roll back)",
				postA.LifetimeTotalBytes, preA.LifetimeTotalBytes)
		}
	})
}

// TestListSearchIsCaseInsensitive locks the contract that the admin user
// search ignores case. It passes trivially on SQLite (whose LIKE is already
// ASCII-case-insensitive); its real job is to pin the LOWER()-based query so a
// future edit can't silently drop it and regress on Postgres, where LIKE is
// case-sensitive. The same SQL runs verbatim on all three backends.
func TestListSearchIsCaseInsensitive(t *testing.T) {
	db, err := openTestDB(t)
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
