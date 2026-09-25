package sqlstore

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"gorm.io/gorm"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
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

// serviceStateFixture opens a repository test database with one active user.
// Each case below creates the rows it needs on top.
func serviceStateFixture(t *testing.T) (ports.UserRepo, *gorm.DB) {
	t.Helper()
	db, err := openTestDB(t)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	if err := ensureTestSchema(db); err != nil {
		t.Fatalf("schema: %v", err)
	}
	t.Cleanup(func() { closeGormDB(db) })
	return NewRepos(db).User, db
}

func createServiceStateUser(t *testing.T, repo ports.UserRepo, n int) *domain.User {
	t.Helper()
	u := &domain.User{
		UPN: fmt.Sprintf("cas-%d@example.test", n), Role: domain.RoleUser, SubToken: fmt.Sprintf("st-cas-%d", n),
		UUID: fmt.Sprintf("00000000-0000-0000-0000-%012d", 900+n), GroupID: 1,
		TrafficResetPeriod: domain.ResetMonthly, Enabled: true,
	}
	if err := repo.Create(context.Background(), u); err != nil {
		t.Fatalf("create: %v", err)
	}
	return u
}

// The automatic suspension writes only a clear row. A row already held — by
// an admin, the blocked-client policy, the quota, anything — keeps its reason,
// its detail and its timestamp, because the detector is never the one to
// decide that its judgement outranks someone else's.
func TestSetServiceStateIfClear_WritesOnlyAClearRow(t *testing.T) {
	repo, _ := serviceStateFixture(t)
	ctx := context.Background()
	clear := createServiceStateUser(t, repo, 1)
	held := createServiceStateUser(t, repo, 2)
	heldAt := time.Date(2026, 9, 1, 8, 0, 0, 0, time.UTC)
	if err := repo.UpdateServiceState(ctx, held.ID, domain.DisabledServiceManual, "admin paused", &heldAt); err != nil {
		t.Fatalf("hold: %v", err)
	}
	at := time.Date(2026, 9, 24, 12, 0, 0, 0, time.UTC)

	if _, err := repo.SetServiceStateIfClear(ctx, clear.ID, domain.DisabledGeoAutoSuspend, "geo detail", at); err != nil {
		t.Fatalf("clear row: %v", err)
	}
	if _, err := repo.SetServiceStateIfClear(ctx, held.ID, domain.DisabledGeoAutoSuspend, "geo detail", at); err != nil {
		t.Fatalf("held row: %v", err)
	}

	got, err := repo.GetByID(ctx, clear.ID)
	if err != nil {
		t.Fatalf("get clear: %v", err)
	}
	if got.ServiceDisabledReason != domain.DisabledGeoAutoSuspend || got.ServiceDisableDetail != "geo detail" ||
		got.ServiceDisabledAt == nil || !got.ServiceDisabledAt.Equal(at) {
		t.Fatalf("clear row = %q/%q/%v, want geo_auto/geo detail/%v",
			got.ServiceDisabledReason, got.ServiceDisableDetail, got.ServiceDisabledAt, at)
	}
	got, err = repo.GetByID(ctx, held.ID)
	if err != nil {
		t.Fatalf("get held: %v", err)
	}
	if got.ServiceDisabledReason != domain.DisabledServiceManual || got.ServiceDisableDetail != "admin paused" ||
		got.ServiceDisabledAt == nil || !got.ServiceDisabledAt.Equal(heldAt) {
		t.Fatalf("held row = %q/%q/%v, want service_manual/admin paused/%v unchanged",
			got.ServiceDisabledReason, got.ServiceDisableDetail, got.ServiceDisabledAt, heldAt)
	}
}

// "Did it write" is the whole contract: the caller mails, pushes and audits
// only on true. The second call finds the row it just wrote and must say no.
func TestSetServiceStateIfClear_ReportsWhetherItWrote(t *testing.T) {
	repo, _ := serviceStateFixture(t)
	ctx := context.Background()
	u := createServiceStateUser(t, repo, 1)
	at := time.Date(2026, 9, 24, 12, 0, 0, 0, time.UTC)

	first, err := repo.SetServiceStateIfClear(ctx, u.ID, domain.DisabledGeoAutoSuspend, "d", at)
	if err != nil || !first {
		t.Fatalf("first = %v, %v; want true, nil", first, err)
	}
	second, err := repo.SetServiceStateIfClear(ctx, u.ID, domain.DisabledGeoAutoSuspend, "d", at.Add(time.Minute))
	if err != nil || second {
		t.Fatalf("second = %v, %v; want false, nil (the row is no longer clear)", second, err)
	}
	missing, err := repo.SetServiceStateIfClear(ctx, u.ID+1000, domain.DisabledGeoAutoSuspend, "d", at)
	if err != nil || missing {
		t.Fatalf("missing row = %v, %v; want false, nil", missing, err)
	}
}

// nullableServiceReasonRow is users with service_disabled_reason relaxed to
// NULL-able, the shape a hand-edited database or a column created before its
// NOT NULL constraint can have. AlterColumn rebuilds the column from it.
type nullableServiceReasonRow struct {
	ID                    int64
	ServiceDisabledReason *string `gorm:"size:32"`
}

func (nullableServiceReasonRow) TableName() string { return "users" }

// NULL is "no reason" too. The shipped column is NOT NULL with an empty
// default, so this needs a relaxed column on an isolated database; a CAS that
// matched only the empty string would read such a row as held forever and
// never suspend it — the safe direction, but a silent gap.
func TestSetServiceStateIfClear_TreatsNullAsClear(t *testing.T) {
	db, err := openIsolatedTestDB(t)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	if err := EnsureSchema(db); err != nil {
		t.Fatalf("schema: %v", err)
	}
	repo := NewRepos(db).User
	ctx := context.Background()
	u := createServiceStateUser(t, repo, 1)
	if err := db.Migrator().AlterColumn(&nullableServiceReasonRow{}, "ServiceDisabledReason"); err != nil {
		t.Fatalf("relax service_disabled_reason: %v", err)
	}
	if err := db.Exec("UPDATE users SET service_disabled_reason = NULL WHERE id = ?", u.ID).Error; err != nil {
		t.Fatalf("set NULL: %v", err)
	}

	wrote, err := repo.SetServiceStateIfClear(ctx, u.ID, domain.DisabledGeoAutoSuspend, "d", time.Now())
	if err != nil || !wrote {
		t.Fatalf("NULL reason = %v, %v; want true, nil", wrote, err)
	}
	got, err := repo.GetByID(ctx, u.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.ServiceDisabledReason != domain.DisabledGeoAutoSuspend {
		t.Fatalf("reason = %q, want geo_auto", got.ServiceDisabledReason)
	}
}

// An empty reason would make the write a no-op on a clear row, and MySQL
// would then report 0 changed rows for a CAS that "won" — so it is refused up
// front, on both predicates.
func TestSetServiceStateIfClear_RejectsAnEmptyReason(t *testing.T) {
	repo, _ := serviceStateFixture(t)
	ctx := context.Background()
	u := createServiceStateUser(t, repo, 1)

	if _, err := repo.SetServiceStateIfClear(ctx, u.ID, domain.DisabledNone, "d", time.Now()); !errors.Is(err, domain.ErrValidation) {
		t.Fatalf("SetServiceStateIfClear(\"\") err = %v, want ErrValidation", err)
	}
	if _, err := repo.ClearServiceStateIfReason(ctx, u.ID, domain.DisabledNone); !errors.Is(err, domain.ErrValidation) {
		t.Fatalf("ClearServiceStateIfReason(\"\") err = %v, want ErrValidation", err)
	}
}

// The lift clears only its own reason. A geo_anomaly row (a person's call)
// and a row an admin re-suspended manually are not the detector's to lift.
func TestClearServiceStateIfReason_ClearsOnlyThatReason(t *testing.T) {
	repo, _ := serviceStateFixture(t)
	ctx := context.Background()
	auto := createServiceStateUser(t, repo, 1)
	human := createServiceStateUser(t, repo, 2)
	at := time.Date(2026, 9, 24, 12, 0, 0, 0, time.UTC)
	if err := repo.UpdateServiceState(ctx, auto.ID, domain.DisabledGeoAutoSuspend, "auto", &at); err != nil {
		t.Fatalf("seed auto: %v", err)
	}
	if err := repo.UpdateServiceState(ctx, human.ID, domain.DisabledGeoAnomaly, "human", &at); err != nil {
		t.Fatalf("seed human: %v", err)
	}

	wrote, err := repo.ClearServiceStateIfReason(ctx, human.ID, domain.DisabledGeoAutoSuspend)
	if err != nil || wrote {
		t.Fatalf("geo_anomaly row = %v, %v; want false, nil", wrote, err)
	}
	wrote, err = repo.ClearServiceStateIfReason(ctx, auto.ID, domain.DisabledGeoAutoSuspend)
	if err != nil || !wrote {
		t.Fatalf("geo_auto row = %v, %v; want true, nil", wrote, err)
	}

	got, err := repo.GetByID(ctx, auto.ID)
	if err != nil {
		t.Fatalf("get auto: %v", err)
	}
	if got.ServiceDisabledReason != domain.DisabledNone || got.ServiceDisableDetail != "" || got.ServiceDisabledAt != nil {
		t.Fatalf("auto row = %q/%q/%v, want cleared", got.ServiceDisabledReason, got.ServiceDisableDetail, got.ServiceDisabledAt)
	}
	got, err = repo.GetByID(ctx, human.ID)
	if err != nil {
		t.Fatalf("get human: %v", err)
	}
	if got.ServiceDisabledReason != domain.DisabledGeoAnomaly || got.ServiceDisableDetail != "human" || got.ServiceDisabledAt == nil {
		t.Fatalf("human row = %q/%q/%v, want geo_anomaly/human unchanged",
			got.ServiceDisabledReason, got.ServiceDisableDetail, got.ServiceDisabledAt)
	}
	again, err := repo.ClearServiceStateIfReason(ctx, auto.ID, domain.DisabledGeoAutoSuspend)
	if err != nil || again {
		t.Fatalf("second clear = %v, %v; want false, nil", again, err)
	}
}

// The scheduled and the manual poll can apply the same ban at once. Exactly
// one caller may win, or the user gets two mails and two audit rows for one
// suspension. Meaningful on Postgres and MySQL, where the two statements can
// really run side by side; SQLite serialises them through its one connection.
func TestSetServiceStateIfClear_ConcurrentCallersWinOnce(t *testing.T) {
	repo, _ := serviceStateFixture(t)
	ctx := context.Background()
	u := createServiceStateUser(t, repo, 1)

	const callers = 2
	var (
		wg    sync.WaitGroup
		start = make(chan struct{})
		wins  atomic.Int32
		errs  = make(chan error, callers)
	)
	for i := range callers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			wrote, err := repo.SetServiceStateIfClear(ctx, u.ID, domain.DisabledGeoAutoSuspend, fmt.Sprintf("caller %d", i), time.Now())
			if err != nil {
				errs <- err
				return
			}
			if wrote {
				wins.Add(1)
			}
		}()
	}
	close(start)
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatalf("caller: %v", err)
	}
	if got := wins.Load(); got != 1 {
		t.Fatalf("winners = %d, want exactly 1", got)
	}
}
