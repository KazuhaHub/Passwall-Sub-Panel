package sqlstore

import (
	"context"
	"testing"
	"time"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
)

func targetScopeTestRepo(t *testing.T) *syncTaskRepo {
	t.Helper()
	db, err := openTestDB(t)
	if err != nil {
		t.Fatalf("open test database: %v", err)
	}
	if err := ensureTestSchema(db); err != nil {
		t.Fatalf("ensure schema: %v", err)
	}
	return &syncTaskRepo{db: db}
}

func seedTargetScopeTask(t *testing.T, repo *syncTaskRepo, typ domain.SyncTaskType, targetType string, targetID int64, status domain.SyncTaskStatus, updated time.Time) *domain.SyncTask {
	t.Helper()
	task := &domain.SyncTask{
		Type: typ, Status: status, TargetType: targetType, TargetID: targetID,
		Summary: "target-scope fixture", Payload: `{}`,
		NextRunAt: time.Unix(1, 0).UTC(), UpdatedAt: updated,
	}
	if status != domain.SyncTaskPending && status != domain.SyncTaskRunning {
		finished := updated
		task.FinishedAt = &finished
	}
	if err := repo.Create(context.Background(), task); err != nil {
		t.Fatalf("create %s task: %v", typ, err)
	}
	return task
}

// The four user task types the sync-status contract covers.
var contractUserTypes = []domain.SyncTaskType{
	domain.SyncTaskUserDelete,
	domain.SyncTaskUserResync,
	domain.SyncTaskUserPushConfig,
	domain.SyncTaskUserMigrate,
}

// A sync-status read must never mix another resource kind into a user's tasks.
// Target IDs are per-kind integers, so user 7 and node 7 collide numerically —
// filtering on target_id alone would hand one resource's tasks to another.
func TestSyncTaskListActiveByTargetSeparatesResourceKinds(t *testing.T) {
	repo := targetScopeTestRepo(t)
	ctx := context.Background()
	now := time.Date(2026, 9, 18, 12, 0, 0, 0, time.UTC)

	userTask := seedTargetScopeTask(t, repo, domain.SyncTaskUserResync, "user", 7, domain.SyncTaskPending, now)
	seedTargetScopeTask(t, repo, domain.SyncTaskNodeUpdate, "node", 7, domain.SyncTaskPending, now)

	got, err := repo.ListActiveByTarget(ctx, contractUserTypes, "user", 7, 20)
	if err != nil {
		t.Fatalf("list active by target: %v", err)
	}
	if len(got) != 1 || got[0].ID != userTask.ID {
		t.Fatalf("want only the user task for user 7, got %d tasks", len(got))
	}
}

// The contract names an explicit set of task types. A type outside that set must
// not appear, or "no active tasks" would silently mean "no tasks of the types
// someone remembered to check".
func TestSyncTaskListActiveByTargetHonoursTheTypeSet(t *testing.T) {
	repo := targetScopeTestRepo(t)
	ctx := context.Background()
	now := time.Date(2026, 9, 18, 12, 0, 0, 0, time.UTC)

	covered := seedTargetScopeTask(t, repo, domain.SyncTaskUserMigrate, "user", 11, domain.SyncTaskPending, now)
	seedTargetScopeTask(t, repo, domain.SyncTaskMailNotify, "user", 11, domain.SyncTaskPending, now)

	got, err := repo.ListActiveByTarget(ctx, contractUserTypes, "user", 11, 20)
	if err != nil {
		t.Fatalf("list active by target: %v", err)
	}
	if len(got) != 1 || got[0].ID != covered.ID {
		t.Fatalf("want only the user_migrate task, got %d tasks", len(got))
	}
}

// Only pending/running are active. A finished task must never be reported as
// something still working.
func TestSyncTaskListActiveByTargetExcludesTerminalStates(t *testing.T) {
	repo := targetScopeTestRepo(t)
	ctx := context.Background()
	now := time.Date(2026, 9, 18, 12, 0, 0, 0, time.UTC)

	seedTargetScopeTask(t, repo, domain.SyncTaskUserResync, "user", 3, domain.SyncTaskSucceeded, now)
	seedTargetScopeTask(t, repo, domain.SyncTaskUserResync, "user", 3, domain.SyncTaskCanceled, now)
	seedTargetScopeTask(t, repo, domain.SyncTaskUserResync, "user", 3, domain.SyncTaskRunning, now)

	got, err := repo.ListActiveByTarget(ctx, contractUserTypes, "user", 3, 20)
	if err != nil {
		t.Fatalf("list active by target: %v", err)
	}
	if len(got) != 1 || got[0].Status != domain.SyncTaskRunning {
		t.Fatalf("want only the running task, got %d tasks", len(got))
	}
}

// The limit is part of the contract: a target with a long retry history must not
// be able to make one status read unbounded.
func TestSyncTaskListActiveByTargetRespectsLimitNewestFirst(t *testing.T) {
	repo := targetScopeTestRepo(t)
	ctx := context.Background()
	base := time.Date(2026, 9, 18, 12, 0, 0, 0, time.UTC)

	for i := 0; i < 5; i++ {
		seedTargetScopeTask(t, repo, domain.SyncTaskUserResync, "user", 21, domain.SyncTaskPending, base.Add(time.Duration(i)*time.Minute))
	}

	got, err := repo.ListActiveByTarget(ctx, contractUserTypes, "user", 21, 2)
	if err != nil {
		t.Fatalf("list active by target: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("want 2 tasks for limit=2, got %d", len(got))
	}
	if !got[0].UpdatedAt.After(got[1].UpdatedAt) {
		t.Fatalf("want newest first, got %s before %s", got[0].UpdatedAt, got[1].UpdatedAt)
	}
}

// Retained terminal tasks are what the UI shows as "recent results"; they are a
// different query from the active one and must exclude anything still running.
func TestSyncTaskListTerminalByTargetReturnsOnlyFinished(t *testing.T) {
	repo := targetScopeTestRepo(t)
	ctx := context.Background()
	base := time.Date(2026, 9, 18, 12, 0, 0, 0, time.UTC)

	seedTargetScopeTask(t, repo, domain.SyncTaskUserResync, "user", 5, domain.SyncTaskPending, base)
	done := seedTargetScopeTask(t, repo, domain.SyncTaskUserResync, "user", 5, domain.SyncTaskSucceeded, base.Add(time.Minute))
	seedTargetScopeTask(t, repo, domain.SyncTaskUserResync, "user", 5, domain.SyncTaskCanceled, base.Add(2*time.Minute))

	got, err := repo.ListTerminalByTarget(ctx, contractUserTypes, "user", 5, 10)
	if err != nil {
		t.Fatalf("list terminal by target: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("want 2 terminal tasks, got %d", len(got))
	}
	// Newest finished first.
	if got[0].Status != domain.SyncTaskCanceled || got[1].ID != done.ID {
		t.Fatalf("want newest finished first, got %v then %v", got[0].Status, got[1].Status)
	}
}
