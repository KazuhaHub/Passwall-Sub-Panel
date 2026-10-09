package sqlstore

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

func openUserRepoForTest(t *testing.T) ports.UserRepo {
	t.Helper()
	db, err := openTestDB(t)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	if err := ensureTestSchema(db); err != nil {
		t.Fatalf("schema: %v", err)
	}
	t.Cleanup(func() {
		if sqlDB, _ := db.DB(); sqlDB != nil {
			_ = sqlDB.Close()
		}
	})
	return NewRepos(db).User
}

func createResetTestUser(t *testing.T, repo ports.UserRepo, upn, sub, uuid string) *domain.User {
	t.Helper()
	u := &domain.User{
		UPN: upn, PasswordHash: "h", Role: domain.RoleUser,
		SubToken: sub, UUID: uuid, GroupID: 1,
		TrafficResetPeriod: domain.ResetMonthly, Enabled: true,
	}
	if err := repo.Create(context.Background(), u); err != nil {
		t.Fatalf("create %s: %v", upn, err)
	}
	return u
}

// TestResetEmergencyAccessPersistsWhereUpdateDoesNot pins the admin emergency
// reset's persistence. ResetEmergencyUsage used to zero the three emergency
// fields in memory and save them with Update, which omits all three
// (pollOwnedColumns): the request returned 204 and the row kept its used
// count, window and baseline, on every dialect. ResetEmergencyAccess is the
// column-scoped writer that actually lands the reset, and it touches nothing
// else — not a neighbouring user, not the traffic or service columns beside
// the emergency ones.
func TestResetEmergencyAccessPersistsWhereUpdateDoesNot(t *testing.T) {
	repo := openUserRepoForTest(t)
	ctx := context.Background()
	u := createResetTestUser(t, repo, "reset@example.test", "sub-reset", "00000000-0000-0000-0000-0000000000e1")
	other := createResetTestUser(t, repo, "keep@example.test", "sub-keep", "00000000-0000-0000-0000-0000000000e2")

	until := timeNowUTCPlusHour()
	for _, id := range []int64{u.ID, other.ID} {
		if err := repo.GrantEmergencyAccess(ctx, id, until, 2, 7<<30); err != nil {
			t.Fatalf("grant %d: %v", id, err)
		}
	}
	if err := repo.UpdateTrafficState(ctx, &domain.User{ID: u.ID, LifetimeTotalBytes: 9 << 30, PeriodBaselineBytes: 1 << 30}); err != nil {
		t.Fatalf("seed traffic state: %v", err)
	}
	if err := repo.UpdateServiceState(ctx, u.ID, domain.DisabledTrafficExceeded, "emergency access active", nil); err != nil {
		t.Fatalf("seed service state: %v", err)
	}

	t.Run("update drops the reset", func(t *testing.T) {
		// The pre-fix path, verbatim: load, zero in memory, Update.
		loaded, err := repo.GetByID(ctx, u.ID)
		if err != nil {
			t.Fatalf("get: %v", err)
		}
		loaded.EmergencyUsedCount = 0
		loaded.EmergencyUntil = nil
		loaded.EmergencyBaselineBytes = 0
		if err := repo.Update(ctx, loaded); err != nil {
			t.Fatalf("Update: %v", err)
		}
		got, err := repo.GetByID(ctx, u.ID)
		if err != nil {
			t.Fatalf("get: %v", err)
		}
		if got.EmergencyUsedCount != 2 || got.EmergencyUntil == nil || got.EmergencyBaselineBytes != 7<<30 {
			t.Fatalf("Update wrote the emergency columns (used=%d until=%v baseline=%d); they are owned by the targeted writers and must stay omitted",
				got.EmergencyUsedCount, got.EmergencyUntil, got.EmergencyBaselineBytes)
		}
	})

	t.Run("reset writer lands it", func(t *testing.T) {
		if err := repo.ResetEmergencyAccess(ctx, u.ID); err != nil {
			t.Fatalf("ResetEmergencyAccess: %v", err)
		}
		got, err := repo.GetByID(ctx, u.ID)
		if err != nil {
			t.Fatalf("get: %v", err)
		}
		if got.EmergencyUsedCount != 0 || got.EmergencyUntil != nil || got.EmergencyBaselineBytes != 0 {
			t.Fatalf("reset did not persist: used=%d until=%v baseline=%d",
				got.EmergencyUsedCount, got.EmergencyUntil, got.EmergencyBaselineBytes)
		}
		if got.LifetimeTotalBytes != 9<<30 || got.PeriodBaselineBytes != 1<<30 {
			t.Fatalf("reset touched traffic state: lifetime=%d baseline=%d", got.LifetimeTotalBytes, got.PeriodBaselineBytes)
		}
		if got.ServiceDisabledReason != domain.DisabledTrafficExceeded || got.ServiceDisableDetail != "emergency access active" {
			t.Fatalf("reset touched service state: %q/%q", got.ServiceDisabledReason, got.ServiceDisableDetail)
		}
		kept, err := repo.GetByID(ctx, other.ID)
		if err != nil {
			t.Fatalf("get other: %v", err)
		}
		if kept.EmergencyUsedCount != 2 || kept.EmergencyUntil == nil || kept.EmergencyBaselineBytes != 7<<30 {
			t.Fatalf("reset leaked onto another user: used=%d until=%v baseline=%d",
				kept.EmergencyUsedCount, kept.EmergencyUntil, kept.EmergencyBaselineBytes)
		}
	})

	t.Run("zero id is refused", func(t *testing.T) {
		if err := repo.ResetEmergencyAccess(ctx, 0); err == nil {
			t.Fatal("ResetEmergencyAccess(0) must refuse instead of issuing an unscoped UPDATE")
		}
	})
}

// TestUserUpdateOmitsOwnedColumns pins, field by field, every domain value
// userRepo.Update does NOT persist: each column in pollOwnedColumns that
// domain.User carries. A service method that sets one of these and saves with
// Update is a silent no-op — the emergency reset was — and only a real
// database shows it, because a fake Update that stores the whole struct
// persists it happily. This table is therefore also the list the fakes'
// Update must keep (user.memoryUserRepo via keepUpdateOmittedFields,
// traffic.fakeUserRepo.Update). A column dropped from pollOwnedColumns fails
// here and names the field the fakes must stop keeping. A column added to it
// fails "table covers the omit list" until it is pinned here, which is the
// prompt to keep it in both fakes in the same change. Without that check a
// newly owned column let the fakes go on persisting it through Update, the
// very shape that hid the emergency reset.
func TestUserUpdateOmitsOwnedColumns(t *testing.T) {
	repo := openUserRepoForTest(t)
	ctx := context.Background()
	u := createResetTestUser(t, repo, "owned@example.test", "sub-owned", "00000000-0000-0000-0000-0000000000e3")

	// Seed every owned column through its own writer, so each starts non-zero.
	at := time.Now().UTC().Add(-time.Hour).Truncate(time.Second)
	if err := repo.UpdateTrafficState(ctx, &domain.User{
		ID: u.ID, LifetimeUpBytes: 11, LifetimeDownBytes: 22, LifetimeTotalBytes: 33,
		PeriodBaselineBytes: 3, PeriodBaselineUpBytes: 1, PeriodBaselineDownBytes: 2,
		LifetimeBaselineAt: &at, TrafficPeriodStart: &at,
	}); err != nil {
		t.Fatalf("seed traffic state: %v", err)
	}
	if err := repo.BatchUpdateLastOnline(ctx, map[int64]time.Time{u.ID: at}); err != nil {
		t.Fatalf("seed last online: %v", err)
	}
	if _, advanced, err := repo.AdvanceBlockViolation(ctx, u.ID, at, at, "blocked"); err != nil || !advanced {
		t.Fatalf("seed block violation: advanced=%v err=%v", advanced, err)
	}
	if err := repo.GrantEmergencyAccess(ctx, u.ID, at.Add(3*time.Hour), 2, 7<<30); err != nil {
		t.Fatalf("seed emergency: %v", err)
	}
	if err := repo.UpdateServiceState(ctx, u.ID, domain.DisabledServiceManual, "paused", &at); err != nil {
		t.Fatalf("seed service state: %v", err)
	}
	if err := repo.SetTOTP(ctx, u.ID, "JBSWY3DPEHPK3PXP", true, []string{"hash"}); err != nil {
		t.Fatalf("seed totp: %v", err)
	}
	seeded, err := repo.GetByID(ctx, u.ID)
	if err != nil {
		t.Fatalf("get seeded: %v", err)
	}

	// An edit-dialog Save that changes every owned field, plus one ordinary
	// column so the test also proves the Save itself ran.
	later := at.Add(24 * time.Hour)
	edit := *seeded
	edit.Remark = "edited"
	edit.LifetimeUpBytes, edit.LifetimeDownBytes, edit.LifetimeTotalBytes = 0, 0, 0
	edit.PeriodBaselineBytes, edit.PeriodBaselineUpBytes, edit.PeriodBaselineDownBytes = 0, 0, 0
	edit.LifetimeBaselineAt, edit.TrafficPeriodStart = &later, &later
	edit.LastOnlineAt = nil
	edit.BlockViolationCount, edit.LastBlockViolationAt = 0, nil
	edit.EmergencyUntil, edit.EmergencyUsedCount, edit.EmergencyBaselineBytes = nil, 0, 0
	edit.ServiceDisabledReason, edit.ServiceDisableDetail, edit.ServiceDisabledAt = domain.DisabledNone, "", nil
	edit.TOTPEnabled = false
	if err := repo.Update(ctx, &edit); err != nil {
		t.Fatalf("Update: %v", err)
	}
	got, err := repo.GetByID(ctx, u.ID)
	if err != nil {
		t.Fatalf("get after update: %v", err)
	}
	if got.Remark != "edited" {
		t.Fatalf("remark = %q, want %q — Update did not run, so nothing below is evidence", got.Remark, "edited")
	}

	owned := []struct {
		column string
		field  func(*domain.User) any
	}{
		{"lifetime_up_bytes", func(u *domain.User) any { return u.LifetimeUpBytes }},
		{"lifetime_down_bytes", func(u *domain.User) any { return u.LifetimeDownBytes }},
		{"lifetime_total_bytes", func(u *domain.User) any { return u.LifetimeTotalBytes }},
		{"period_baseline_bytes", func(u *domain.User) any { return u.PeriodBaselineBytes }},
		{"period_baseline_up_bytes", func(u *domain.User) any { return u.PeriodBaselineUpBytes }},
		{"period_baseline_down_bytes", func(u *domain.User) any { return u.PeriodBaselineDownBytes }},
		{"lifetime_baseline_at", func(u *domain.User) any { return u.LifetimeBaselineAt }},
		{"traffic_period_start", func(u *domain.User) any { return u.TrafficPeriodStart }},
		{"last_online_at", func(u *domain.User) any { return u.LastOnlineAt }},
		{"block_violation_count", func(u *domain.User) any { return u.BlockViolationCount }},
		{"last_block_violation_at", func(u *domain.User) any { return u.LastBlockViolationAt }},
		{"emergency_until", func(u *domain.User) any { return u.EmergencyUntil }},
		{"emergency_used_count", func(u *domain.User) any { return u.EmergencyUsedCount }},
		{"emergency_baseline_bytes", func(u *domain.User) any { return u.EmergencyBaselineBytes }},
		{"service_disabled_reason", func(u *domain.User) any { return u.ServiceDisabledReason }},
		{"service_disable_detail", func(u *domain.User) any { return u.ServiceDisableDetail }},
		{"service_disabled_at", func(u *domain.User) any { return u.ServiceDisabledAt }},
		{"totp_enabled", func(u *domain.User) any { return u.TOTPEnabled }},
	}

	t.Run("table covers the omit list", func(t *testing.T) {
		// Owned, but domain.User does not carry them, so no fake can persist
		// them through Update and there is no field to pin.
		pinned := map[string]bool{"totp_secret": true, "recovery_codes": true, "permission_overrides": true}
		for _, tc := range owned {
			pinned[tc.column] = true
		}
		omitted := map[string]bool{}
		for _, c := range pollOwnedColumns {
			omitted[c] = true
			if !pinned[c] {
				t.Errorf("pollOwnedColumns has %q but this table does not pin it: add it here, and keep its field in both fakes' Update (user.keepUpdateOmittedFields, traffic.fakeUserRepo.Update)", c)
			}
		}
		for c := range pinned {
			if !omitted[c] {
				t.Errorf("%q is pinned here but is no longer in pollOwnedColumns: drop it here, and stop keeping its field (if domain.User has one) in both fakes' Update", c)
			}
		}
	})

	for _, tc := range owned {
		t.Run(tc.column, func(t *testing.T) {
			if want, have := tc.field(seeded), tc.field(got); !reflect.DeepEqual(want, have) {
				t.Fatalf("Update wrote %s: %v -> %v; it is an owned column (pollOwnedColumns), written only by its targeted writer",
					tc.column, ownedValue(want), ownedValue(have))
			}
		})
	}
}

// ownedValue renders a *time.Time by value so a failure message shows the
// instant, not the pointer.
func ownedValue(v any) any {
	if p, ok := v.(*time.Time); ok && p != nil {
		return *p
	}
	return v
}
