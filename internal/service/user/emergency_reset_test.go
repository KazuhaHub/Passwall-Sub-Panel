package user

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/KazuhaHub/passwall-sub-panel/internal/adapters/sqlstore"
	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
)

// TestResetEmergencyUsagePersists pins the admin "reset emergency-access
// count" action against the repository the way production sees it. It used to
// zero the three emergency fields and save them with Update, which omits every
// emergency column (pollOwnedColumns): the API answered 204, the row kept its
// used count, window and baseline, and the user was still "out of uses". The
// suite never noticed because memoryUserRepo.Update stored the whole struct;
// it now keeps the omitted fields like production does, so routing the reset
// back through Update fails every case below.
//
// The push half: an emergency window is part of what the panel-side client is
// told (expiry = MAX(ExpireAt, EmergencyUntil), plus the enable bit and quota
// floor while it is active), so ending one in PSP's row alone would leave the
// upstream client serving the revoked window. A count-only reset changes
// nothing pushed and must not fan out (the batch reset runs it per user).
func TestResetEmergencyUsagePersists(t *testing.T) {
	now := time.Now()
	future := now.Add(2 * time.Hour)
	past := now.Add(-time.Hour)
	for _, tc := range []struct {
		name      string
		user      domain.User
		wantPush  bool
		wantWrite bool
	}{
		{
			name: "active window",
			user: domain.User{
				ServiceDisabledReason: domain.DisabledTrafficExceeded, ServiceDisableDetail: "emergency access active",
				EmergencyUsedCount: 2, EmergencyUntil: &future, EmergencyBaselineBytes: 7 << 30,
			},
			wantPush: true, wantWrite: true,
		},
		{
			name:     "expired window",
			user:     domain.User{EmergencyUsedCount: 1, EmergencyUntil: &past, EmergencyBaselineBytes: 3 << 30},
			wantPush: true, wantWrite: true,
		},
		{
			name:     "count only",
			user:     domain.User{EmergencyUsedCount: 3},
			wantPush: false, wantWrite: true,
		},
		{
			name:     "nothing to reset",
			user:     domain.User{},
			wantPush: false, wantWrite: false,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			u := tc.user
			u.ID, u.UPN, u.Role, u.Enabled = 7, "em@example.test", domain.RoleUser, true
			life := &fakeSharedLife{}
			tasks := &recordingTaskRepo{}
			svc := migratedSvc(&u, life, tasks)
			repo := svc.users.(*memoryUserRepo)

			if err := svc.ResetEmergencyUsage(context.Background(), 7); err != nil {
				t.Fatalf("ResetEmergencyUsage: %v", err)
			}
			got := repo.byID[7]
			if got.EmergencyUsedCount != 0 || got.EmergencyUntil != nil || got.EmergencyBaselineBytes != 0 {
				t.Fatalf("reset did not persist: used=%d until=%v baseline=%d",
					got.EmergencyUsedCount, got.EmergencyUntil, got.EmergencyBaselineBytes)
			}
			if got.ServiceDisabledReason != tc.user.ServiceDisabledReason || got.ServiceDisableDetail != tc.user.ServiceDisableDetail {
				t.Fatalf("reset changed the service state to %q/%q; it owns only the emergency record",
					got.ServiceDisabledReason, got.ServiceDisableDetail)
			}
			if wrote := repo.resetEmergencyCalls > 0; wrote != tc.wantWrite {
				t.Fatalf("emergency reset writes = %d, want a write: %v", repo.resetEmergencyCalls, tc.wantWrite)
			}
			if !tc.wantPush {
				if len(life.calls) != 0 {
					t.Fatalf("nothing pushed changed, yet the lifecycle was pushed: %+v", life.calls)
				}
				return
			}
			if len(life.calls) != 1 {
				t.Fatalf("lifecycle pushes = %d, want 1 so the panel drops the window", len(life.calls))
			}
			if c := life.calls[0]; c.userID != 7 || c.want.ExpiryTime != 0 {
				t.Fatalf("pushed %+v, want user 7 with no expiry left by the cleared window", c)
			}
			if tc.user.ServiceDisabledReason == domain.DisabledTrafficExceeded && life.calls[0].want.Enable {
				t.Fatal("pushed enable=true for a traffic-suspended user whose window was just revoked")
			}
			if got := pushConfigTasks(tasks); len(got) != 0 {
				t.Fatalf("a successful push must not enqueue a retry, got: %+v", got)
			}
		})
	}
}

// A failed push after the reset must leave a durable SyncTaskUserPushConfig,
// the contract every other state change that reaches the panel keeps
// (unqueued.go): the reset is already committed, so a nil without a queued
// retry would leave the upstream client serving a window PSP no longer has.
// When the queue is down too, the caller must hear it (errUnqueuedPush) — the
// admin repeating the reset is then the only thing that converges the panel.
func TestResetEmergencyUsage_PushFailureEnqueuesRetry(t *testing.T) {
	for _, tc := range []struct {
		name      string
		queueDown bool
	}{
		{"retry queued", false},
		{"queue down too", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			until := time.Now().Add(time.Hour)
			u := &domain.User{ID: 8, UPN: "em-fail@example.test", Role: domain.RoleUser, Enabled: true,
				EmergencyUsedCount: 1, EmergencyUntil: &until, EmergencyBaselineBytes: 1 << 30}
			tasks := &recordingTaskRepo{}
			life := &failingSharedLife{fail: true}
			svc := migratedSvc(u, life, tasks)
			if tc.queueDown {
				svc.tasks = &refusingTaskRepo{}
			}

			err := svc.ResetEmergencyUsage(context.Background(), 8)
			if life.calls != 1 {
				t.Fatalf("lifecycle push attempts = %d, want 1", life.calls)
			}
			if got := svc.users.(*memoryUserRepo).byID[8]; got.EmergencyUntil != nil || got.EmergencyUsedCount != 0 {
				t.Fatalf("the reset must stay committed when the push fails: until=%v used=%d", got.EmergencyUntil, got.EmergencyUsedCount)
			}
			if tc.queueDown {
				if err == nil {
					t.Fatal("push AND enqueue failed, yet the reset returned nil: nothing will converge the panel")
				}
				return
			}
			if err != nil {
				t.Fatalf("ResetEmergencyUsage should absorb the push failure into a retry task, got: %v", err)
			}
			if got := pushConfigTasks(tasks); len(got) != 1 || got[0].TargetID != 8 {
				t.Fatalf("a failed push must enqueue SyncTaskUserPushConfig for user 8; created tasks: %+v", tasks.created)
			}
		})
	}
}

// refusingTaskRepo is a sync-task queue that cannot take a new task.
type refusingTaskRepo struct{ recordingTaskRepo }

func (*refusingTaskRepo) Create(context.Context, *domain.SyncTask) error {
	return errors.New("sync_tasks: database is locked")
}

// The reset reads and writes the emergency record under emergencyMu, like
// UseEmergencyAccess and the traffic poll's ClearEmergencyAccess. Without it a
// grant could land between the reset's read and its write and be wiped unseen,
// or read the pre-reset count and write it back over the reset.
func TestResetEmergencyUsage_WaitsForTheEmergencyLock(t *testing.T) {
	until := time.Now().Add(time.Hour)
	u := &domain.User{ID: 9, UPN: "em-lock@example.test", Role: domain.RoleUser, Enabled: true,
		EmergencyUsedCount: 1, EmergencyUntil: &until}
	svc := migratedSvc(u, &fakeSharedLife{}, &recordingTaskRepo{})

	held, release := make(chan struct{}), make(chan struct{})
	unlock := sync.OnceFunc(func() { close(release) })
	t.Cleanup(unlock) // a failed assertion below must not strand the holder
	go svc.WithEmergencyLock(func() {
		close(held)
		<-release
	})
	<-held
	done := make(chan error, 1)
	go func() { done <- svc.ResetEmergencyUsage(context.Background(), 9) }()
	select {
	case err := <-done:
		t.Fatalf("reset returned (%v) while the emergency lock was held", err)
	case <-time.After(50 * time.Millisecond):
	}
	unlock()
	if err := <-done; err != nil {
		t.Fatalf("ResetEmergencyUsage: %v", err)
	}
	if got := svc.users.(*memoryUserRepo).byID[9]; got.EmergencyUsedCount != 0 || got.EmergencyUntil != nil {
		t.Fatalf("reset did not persist after the lock was released: used=%d until=%v", got.EmergencyUsedCount, got.EmergencyUntil)
	}
}

// The same reset against the real sqlstore repository, so the regression is
// pinned by the database itself and not only by how faithfully
// memoryUserRepo mirrors Update's omitted columns. Before the fix this exact
// sequence left used=2, the window and the 7 GiB baseline in the row.
func TestResetEmergencyUsagePersistsOnARealDatabase(t *testing.T) {
	db, err := sqlstore.Open("sqlite", filepath.Join(t.TempDir(), "emergency-reset.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if sqlDB, _ := db.DB(); sqlDB != nil {
			_ = sqlDB.Close()
		}
	})
	if err := sqlstore.EnsureSchema(db); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	users := sqlstore.NewRepos(db).User
	u := &domain.User{UPN: "em-db@example.test", PasswordHash: "h", Role: domain.RoleUser,
		SubToken: "sub-em-db", UUID: "00000000-0000-0000-0000-0000000000e9", GroupID: 1,
		TrafficResetPeriod: domain.ResetMonthly, Enabled: true}
	if err := users.Create(ctx, u); err != nil {
		t.Fatal(err)
	}
	if err := users.GrantEmergencyAccess(ctx, u.ID, time.Now().UTC().Add(time.Hour), 2, 7<<30); err != nil {
		t.Fatal(err)
	}
	svc := &Service{users: users, ownership: emptyOwnershipRepo{}, tasks: &recordingTaskRepo{}, settings: bfSettings{}}

	if err := svc.ResetEmergencyUsage(ctx, u.ID); err != nil {
		t.Fatalf("ResetEmergencyUsage: %v", err)
	}
	got, err := users.GetByID(ctx, u.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.EmergencyUsedCount != 0 || got.EmergencyUntil != nil || got.EmergencyBaselineBytes != 0 {
		t.Fatalf("reset did not persist: used=%d until=%v baseline=%d",
			got.EmergencyUsedCount, got.EmergencyUntil, got.EmergencyBaselineBytes)
	}
}
