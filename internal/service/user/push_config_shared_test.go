package user

import (
	"context"
	"testing"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
)

// migratedSvc builds the steady-state post-migration shape: the legacy
// user_xui_clients table is gone, so ListByUser answers empty and the SHARED
// client is the only thing a config push can reach.
func migratedSvc(u *domain.User, life SharedLifecycleSyncer, tasks *recordingTaskRepo) *Service {
	svc := &Service{
		users:     &memoryUserRepo{byID: map[int64]*domain.User{u.ID: u}},
		ownership: emptyOwnershipRepo{},
		tasks:     tasks,
		settings:  bfSettings{},
	}
	svc.SetSharedLifecycleSyncer(life)
	return svc
}

func pushConfigTasks(r *recordingTaskRepo) []*domain.SyncTask {
	var out []*domain.SyncTask
	for _, t := range r.created {
		if t.Type == domain.SyncTaskUserPushConfig {
			out = append(out, t)
		}
	}
	return out
}

// The enforcement-critical case. SetEnabledAndSync is the chokepoint admin
// disable AND quota/expiry auto-disable funnel through, and its only durable
// recovery from a failed push is the SyncTaskUserPushConfig it enqueues when
// pushClientConfigToAll returns non-nil.
//
// On a fully-migrated install the shared push is the ONLY write that function
// makes. So if its error is dropped, the function returns nil for a push that
// never reached 3X-UI, the enqueue is skipped, and a user PSP believes it has
// disabled stays live on the panel indefinitely — with nothing queued, nothing
// counted, and only a log line to show for it.
//
// This is the same failure ResyncMembership is already guarded against in
// resync_lifecycle_test.go (the audit-#1 bypass); this path had no such guard.
func TestSetEnabledAndSync_SharedPushFailureEnqueuesRetry(t *testing.T) {
	u := &domain.User{ID: 42, UPN: "u@example.com", Role: domain.RoleUser, Enabled: true}
	tasks := &recordingTaskRepo{}
	life := &failingSharedLife{fail: true}
	svc := migratedSvc(u, life, tasks)

	if err := svc.SetEnabledAndSync(context.Background(), 42, false, domain.DisabledManual, "admin disabled"); err != nil {
		t.Fatalf("SetEnabledAndSync should absorb the push failure into a retry task, got: %v", err)
	}
	if life.calls != 1 {
		t.Fatalf("the shared lifecycle push must be attempted exactly once, got %d", life.calls)
	}
	if got := pushConfigTasks(tasks); len(got) != 1 {
		t.Fatalf("a failed shared-client push must enqueue SyncTaskUserPushConfig so the disable is retried; created tasks: %+v", tasks.created)
	}
	if stored := svc.users.(*memoryUserRepo).byID[42]; stored.Enabled {
		t.Fatal("PSP's own record must still show the user disabled")
	}
}

// The other half of the contract: a push that SUCCEEDS must not enqueue
// anything. Without this, the test above would also pass against a version
// that enqueued a retry unconditionally — which would queue a task on every
// admin disable and every traffic-poll floor refresh.
func TestSetEnabledAndSync_SharedPushSuccessEnqueuesNothing(t *testing.T) {
	u := &domain.User{ID: 43, UPN: "ok@example.com", Role: domain.RoleUser, Enabled: true}
	tasks := &recordingTaskRepo{}
	life := &failingSharedLife{fail: false}
	svc := migratedSvc(u, life, tasks)

	if err := svc.SetEnabledAndSync(context.Background(), 43, false, domain.DisabledManual, "admin disabled"); err != nil {
		t.Fatalf("happy path should succeed: %v", err)
	}
	if got := pushConfigTasks(tasks); len(got) != 0 {
		t.Fatalf("a successful push must not enqueue a retry, got: %+v", got)
	}
}

// PushClientConfig is what the traffic poll calls per user to refresh the
// panel-side quota floor. Its error is what increments PushConfigErrorTotal,
// so a swallowed shared failure also made that counter read zero while the
// floor silently went stale — the metric an operator would check to find
// exactly this problem.
func TestPushClientConfig_SurfacesSharedFailure(t *testing.T) {
	u := &domain.User{ID: 44, UPN: "poll@example.com", Role: domain.RoleUser, Enabled: true}
	svc := migratedSvc(u, &failingSharedLife{fail: true}, &recordingTaskRepo{})

	if err := svc.PushClientConfig(context.Background(), 44); err == nil {
		t.Fatal("PushClientConfig must report a failed shared-client push, not return nil")
	}
}
