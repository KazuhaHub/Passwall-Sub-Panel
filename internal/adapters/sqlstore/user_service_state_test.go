package sqlstore

import (
	"context"
	"testing"
	"time"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
)

// TestUpdateOmitsServiceState locks the invariant that the generic full-row
// userRepo.Update path does NOT write the service-suspension columns
// (service_disabled_reason / service_disable_detail / service_disabled_at).
// Those columns are owned by the targeted UpdateServiceState writer, exactly
// like block_violation_count / emergency_* / totp_* (pollOwnedColumns).
//
// Without the omit, an admin's read-modify-Save of a user profile (UpdateProfile
// loads the row, mutates an unrelated field, and Save()s the whole struct) that
// brackets a concurrent auto-suspend reverts service_disabled_reason from its
// stale in-memory snapshot. For blocked_client / service_manual — whose
// ServiceStatus derives ONLY from the column, with no live re-derivation —
// that silently un-suspends the user and the next push re-enables their 3X-UI
// client: an enforcement bypass. (traffic_exceeded / expired self-heal because
// ServiceStatus re-derives them from PeriodUsed / ExpireAt.)
func TestUpdateOmitsServiceState(t *testing.T) {
	db, err := openTestDB(t)
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := ensureTestSchema(db); err != nil {
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
		UPN: "svc@example.test", Role: domain.RoleUser, SubToken: "st-svc",
		UUID: "00000000-0000-0000-0000-0000000000bb", GroupID: 1,
		TrafficResetPeriod: domain.ResetMonthly, Enabled: true,
	}
	if err := repo.Create(ctx, u); err != nil {
		t.Fatalf("create: %v", err)
	}

	// The admin opens the edit dialog while the user is still active: capture
	// their snapshot. Loading via GetByID (not a hand-built struct) mirrors
	// UpdateProfile's real GetByID -> mutate -> Save read-modify-write and keeps
	// created_at populated — a zero time.Time fails MySQL strict mode as
	// '0000-00-00'. At this point ServiceDisabledReason is None (active).
	adminSnapshot, err := repo.GetByID(ctx, u.ID)
	if err != nil {
		t.Fatalf("GetByID (admin snapshot): %v", err)
	}

	// Meanwhile a blocked-client auto-suspend lands via the column-scoped writer.
	now := time.Date(2026, 6, 25, 12, 0, 0, 0, time.UTC)
	if err := repo.UpdateServiceState(ctx, u.ID, domain.DisabledBlockedClient, "too many clients", &now); err != nil {
		t.Fatalf("UpdateServiceState: %v", err)
	}

	// The admin clicks Save, carrying the STALE (pre-suspend) snapshot whose
	// service columns still read "active" — the clobber window UpdateProfile hits
	// when a suspend lands between its GetByID and its Save.
	adminSnapshot.Remark = "admin edited the remark"
	if err := repo.Update(ctx, adminSnapshot); err != nil {
		t.Fatalf("Update: %v", err)
	}

	// The blocked-client suspension MUST survive the stale Save.
	got, err := repo.GetByID(ctx, u.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if got.ServiceDisabledReason != domain.DisabledBlockedClient {
		t.Fatalf("service suspension clobbered by generic Update: ServiceDisabledReason = %q, want %q",
			got.ServiceDisabledReason, domain.DisabledBlockedClient)
	}
	if got.ServiceDisableDetail != "too many clients" {
		t.Fatalf("service detail clobbered: %q, want %q", got.ServiceDisableDetail, "too many clients")
	}
	if got.ServiceDisabledAt == nil {
		t.Fatal("service_disabled_at clobbered to nil by generic Update")
	}
	// The profile edit the admin DID intend must still land.
	if got.Remark != "admin edited the remark" {
		t.Fatalf("admin remark edit lost: %q", got.Remark)
	}
}
