package user

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

// dueTaskRepo is the sync-task queue as ProcessDueTasks drives it: it hands
// out a fixed batch of due tasks, lets every claim succeed, and records which
// terminal transition each task took.
type dueTaskRepo struct {
	ports.SyncTaskRepo
	due       []*domain.SyncTask
	succeeded []int64
	retried   map[int64]string // task id -> last error recorded with the retry
	canceled  []int64
}

func (r *dueTaskRepo) ListDue(context.Context, time.Time, int) ([]*domain.SyncTask, error) {
	return r.due, nil
}

func (r *dueTaskRepo) MarkRunning(context.Context, int64) (bool, error) { return true, nil }

func (r *dueTaskRepo) MarkSucceeded(_ context.Context, id int64) error {
	r.succeeded = append(r.succeeded, id)
	return nil
}

func (r *dueTaskRepo) MarkRetry(_ context.Context, id int64, lastError string, _ time.Time) error {
	if r.retried == nil {
		r.retried = map[int64]string{}
	}
	r.retried[id] = lastError
	return nil
}

func (r *dueTaskRepo) Cancel(_ context.Context, id int64) error {
	r.canceled = append(r.canceled, id)
	return nil
}

// failingProvisionMigrator fails ProvisionUser the way sharedclient does when
// the post-create read-back cannot reach the panel: the adapter's error is
// wrapped with %w, so its sentinel chain survives up to the task runner.
type failingProvisionMigrator struct {
	resyncMigrator
	err error
}

func (m *failingProvisionMigrator) ProvisionUser(_ context.Context, id int64) error {
	m.provisioned = append(m.provisioned, id)
	return fmt.Errorf("confirm shared client u%d@psp: %w", id, m.err)
}

// A native node's "no reading" sentinels all wrap domain.ErrNotFound, which
// the user-task runner used to read as "the user was deleted". A resync or
// migration that failed ONLY because the agent was offline (or its full
// report missing or stale) was therefore marked SUCCEEDED and never retried:
// the user's shared client stayed unprovisioned, or held a stale lifecycle,
// until the next heal sweep happened to repair it. A native agent on a box
// that restarts makes this the common case, not an edge one.
//
// The task must stay pending and be retried while the user row still exists.
func TestProcessDueTasks_NativeSnapshotUnavailableIsRetried(t *testing.T) {
	sentinels := []error{
		ports.ErrNativePanelAgentOffline,
		ports.ErrNativePanelSnapshotMissing,
		ports.ErrNativePanelSnapshotStale,
	}
	for _, typ := range []domain.SyncTaskType{domain.SyncTaskUserResync, domain.SyncTaskUserMigrate} {
		for _, cause := range sentinels {
			t.Run(fmt.Sprintf("%s/provision/%s", typ, cause), func(t *testing.T) {
				svc, tasks := nativeTaskService(typ)
				svc.SetSharedMigrator(&failingProvisionMigrator{err: cause})
				svc.SetSharedLifecycleSyncer(&failingSharedLife{})

				assertRetriedWith(t, svc, tasks, cause)
			})
			t.Run(fmt.Sprintf("%s/lifecycle/%s", typ, cause), func(t *testing.T) {
				// The lifecycle push's read-back fails the same way
				// (sharedclient.SyncLifecycle wraps GetClient's error).
				svc, tasks := nativeTaskService(typ)
				svc.SetSharedMigrator(&resyncMigrator{})
				svc.SetSharedLifecycleSyncer(&erroringSharedLife{
					err: fmt.Errorf("confirm shared client u7@psp credentials: %w", cause),
				})

				assertRetriedWith(t, svc, tasks, cause)
			})
		}
	}
}

// The other half of the contract, which the fix must not break: a resync or
// migration task whose user has since been deleted has nothing left to do and
// completes instead of burning ~100 retries.
func TestProcessDueTasks_DeletedUserResyncCompletes(t *testing.T) {
	for _, typ := range []domain.SyncTaskType{domain.SyncTaskUserResync, domain.SyncTaskUserMigrate} {
		t.Run(string(typ), func(t *testing.T) {
			tasks := &dueTaskRepo{due: []*domain.SyncTask{{ID: 1, Type: typ, TargetType: "user", TargetID: 999}}}
			svc := &Service{
				users:    &memoryUserRepo{byID: map[int64]*domain.User{}}, // user 999 absent
				groups:   &bfGroupRepo{g: &domain.Group{ID: 1}},
				selector: bfSelector{},
				settings: bfSettings{},
				tasks:    tasks,
			}
			mig := &resyncMigrator{}
			svc.SetSharedMigrator(mig)
			svc.SetSharedLifecycleSyncer(&failingSharedLife{})

			if err := svc.ProcessDueTasks(context.Background(), 20); err != nil {
				t.Fatalf("ProcessDueTasks: %v", err)
			}
			if len(tasks.succeeded) != 1 || tasks.succeeded[0] != 1 {
				t.Fatalf("a task for a deleted user must complete; succeeded=%v retried=%v", tasks.succeeded, tasks.retried)
			}
			if len(tasks.retried) != 0 || len(tasks.canceled) != 0 {
				t.Fatalf("a task for a deleted user must not be retried or cancelled; retried=%v canceled=%v", tasks.retried, tasks.canceled)
			}
			if len(mig.provisioned) != 0 {
				t.Fatalf("nothing may be provisioned for a deleted user: %v", mig.provisioned)
			}
		})
	}
}

// erroringSharedLife fails every lifecycle push with a fixed error.
type erroringSharedLife struct{ err error }

func (f *erroringSharedLife) SyncUserLifecycle(context.Context, int64, domain.UserLifecycle) error {
	return f.err
}

// nativeTaskService wires one due task of typ for an existing user 7 on a
// group that selects one node on panel 1.
func nativeTaskService(typ domain.SyncTaskType) (*Service, *dueTaskRepo) {
	tasks := &dueTaskRepo{due: []*domain.SyncTask{{ID: 1, Type: typ, TargetType: "user", TargetID: 7}}}
	svc := &Service{
		users:    &memoryUserRepo{byID: map[int64]*domain.User{7: {ID: 7, UPN: "u7@example.com", Enabled: true, GroupID: 1}}},
		groups:   &bfGroupRepo{g: &domain.Group{ID: 1}},
		selector: bfSelector{nodes: []*domain.Node{{ID: 10, PanelID: 1, DesiredProtocol: "vless"}}},
		settings: bfSettings{},
		tasks:    tasks,
	}
	return svc, tasks
}

func assertRetriedWith(t *testing.T, svc *Service, tasks *dueTaskRepo, cause error) {
	t.Helper()
	if err := svc.ProcessDueTasks(context.Background(), 20); err != nil {
		t.Fatalf("ProcessDueTasks: %v", err)
	}
	if len(tasks.succeeded) != 0 {
		t.Fatalf("a resync that failed because the native snapshot was unavailable was marked SUCCEEDED (%v); it must stay pending for retry", tasks.succeeded)
	}
	lastErr, ok := tasks.retried[1]
	if !ok {
		t.Fatalf("task was not scheduled for retry; retried=%v canceled=%v", tasks.retried, tasks.canceled)
	}
	// The admin's Sync Tasks view shows this string: it has to name the real
	// cause, not a generic failure.
	if !strings.Contains(lastErr, cause.Error()) {
		t.Fatalf("retry error %q does not carry the native cause %q", lastErr, cause.Error())
	}
}
