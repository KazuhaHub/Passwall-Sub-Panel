package sqlstore

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

func retiredSyncTaskTestRepo(t *testing.T) *syncTaskRepo {
	t.Helper()
	db, err := openTestDB(t)
	if err != nil {
		t.Fatalf("open test database: %v", err)
	}
	if err := EnsureSchema(db); err != nil {
		t.Fatalf("ensure schema: %v", err)
	}
	return &syncTaskRepo{db: db}
}

func createRetiredSyncTaskFixture(t *testing.T, repo *syncTaskRepo, status domain.SyncTaskStatus, targetID int64) *domain.SyncTask {
	t.Helper()
	finishedAt := time.Date(2026, 9, 12, 11, 0, 0, 0, time.UTC)
	task := &domain.SyncTask{
		Type: domain.SyncTaskNodeUpdate, Status: status, TargetType: "node", TargetID: targetID,
		Summary: "old server configuration", Payload: `{"previous_backend":"3xui"}`,
		LastError: "retired by offline server conversion", Attempts: 8,
		NextRunAt: time.Unix(1, 0).UTC(),
	}
	if status != domain.SyncTaskPending && status != domain.SyncTaskRunning {
		task.FinishedAt = &finishedAt
	}
	if err := repo.Create(context.Background(), task); err != nil {
		t.Fatalf("create %s task: %v", status, err)
	}
	return task
}

// Unlike a canceled task, a retired task no longer refers to the current
// backend. Late runner callbacks and an explicit admin Retry must not revive it
// or erase the conversion reason, attempts, payload, or finished timestamp.
func TestSyncTaskRetiredCannotBeResurrected(t *testing.T) {
	repo := retiredSyncTaskTestRepo(t)
	ctx := context.Background()
	task := createRetiredSyncTaskFixture(t, repo, domain.SyncTaskRetired, 71)
	before, err := repo.GetByID(ctx, task.ID)
	if err != nil {
		t.Fatal(err)
	}
	checks := []struct {
		name string
		run  func(*testing.T) error
	}{
		{"claim", func(t *testing.T) error {
			claimed, err := repo.MarkRunning(ctx, task.ID)
			if claimed {
				t.Fatal("retired task was claimed")
			}
			return err
		}},
		{"late success", func(*testing.T) error { return repo.MarkSucceeded(ctx, task.ID) }},
		{"late retry", func(*testing.T) error {
			return repo.MarkRetry(ctx, task.ID, "late network failure", time.Now().Add(time.Minute))
		}},
		{"cancel", func(*testing.T) error { return repo.Cancel(ctx, task.ID) }},
		{"startup reset", func(*testing.T) error { return repo.ResetRunning(ctx) }},
		{"admin retry", func(t *testing.T) error {
			err := repo.RetryNow(ctx, task.ID)
			if !errors.Is(err, domain.ErrConflict) {
				t.Fatalf("RetryNow(retired) = %v, want ErrConflict", err)
			}
			return nil
		}},
	}
	for _, check := range checks {
		t.Run(check.name, func(t *testing.T) {
			if err := check.run(t); err != nil {
				t.Fatal(err)
			}
			after, err := repo.GetByID(ctx, task.ID)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(after, before) {
				t.Fatalf("%s changed retired task: before=%+v after=%+v", check.name, before, after)
			}
		})
	}
}

func TestSyncTaskRetiredDoesNotBlockCurrentWork(t *testing.T) {
	repo := retiredSyncTaskTestRepo(t)
	ctx := context.Background()
	retired := createRetiredSyncTaskFixture(t, repo, domain.SyncTaskRetired, 72)
	if _, err := repo.GetActiveByTarget(ctx, retired.Type, retired.TargetType, retired.TargetID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("GetActiveByTarget(retired) = %v, want ErrNotFound", err)
	}
	active, err := repo.HasActiveByTargetAny(ctx, []domain.SyncTaskType{retired.Type}, retired.TargetType, retired.TargetID)
	if err != nil || active {
		t.Fatalf("HasActiveByTargetAny(retired) = %v, %v, want false, nil", active, err)
	}
	due, err := repo.ListDue(ctx, time.Now(), 20)
	if err != nil || len(due) != 0 {
		t.Fatalf("ListDue(retired) = %v, %v, want empty", due, err)
	}
	status := domain.SyncTaskRetired
	listed, total, err := repo.List(ctx, ports.SyncTaskFilter{Status: &status})
	if err != nil || total != 1 || len(listed) != 1 || listed[0].ID != retired.ID {
		t.Fatalf("retired history not visible: items=%v total=%d err=%v", listed, total, err)
	}
	current := createRetiredSyncTaskFixture(t, repo, domain.SyncTaskPending, retired.TargetID)
	got, err := repo.GetActiveByTarget(ctx, current.Type, current.TargetType, current.TargetID)
	if err != nil || got.ID != current.ID {
		t.Fatalf("current pending task hidden by retired history: got=%v err=%v", got, err)
	}
}

func TestSyncTaskRetiredGuardKeepsCanceledCallbacksNoOp(t *testing.T) {
	repo := retiredSyncTaskTestRepo(t)
	ctx := context.Background()
	task := createRetiredSyncTaskFixture(t, repo, domain.SyncTaskCanceled, 73)
	before, err := repo.GetByID(ctx, task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.MarkSucceeded(ctx, task.ID); err != nil {
		t.Fatal(err)
	}
	if err := repo.MarkRetry(ctx, task.ID, "late callback", time.Now()); err != nil {
		t.Fatal(err)
	}
	after, err := repo.GetByID(ctx, task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(after, before) {
		t.Fatalf("adding retired guard weakened canceled guard: before=%+v after=%+v", before, after)
	}
}

// Retirement is terminal, not a third active state. The operator's existing
// clear-finished action must remove retired history while preserving live work.
// A held task ID must also remain absent after late callbacks following purge.
func TestSyncTaskDeleteFinishedIncludesRetired(t *testing.T) {
	repo := retiredSyncTaskTestRepo(t)
	ctx := context.Background()
	var finished, active []*domain.SyncTask
	for i, status := range []domain.SyncTaskStatus{
		domain.SyncTaskRetired, domain.SyncTaskCanceled, domain.SyncTaskSucceeded,
		domain.SyncTaskPending, domain.SyncTaskRunning,
	} {
		task := createRetiredSyncTaskFixture(t, repo, status, int64(80+i))
		if status == domain.SyncTaskPending || status == domain.SyncTaskRunning {
			active = append(active, task)
		} else {
			finished = append(finished, task)
		}
	}
	deleted, err := repo.DeleteFinished(ctx)
	if err != nil || deleted != int64(len(finished)) {
		t.Fatalf("DeleteFinished = %d, %v, want %d, nil", deleted, err, len(finished))
	}
	for _, task := range finished {
		if _, err := repo.GetByID(ctx, task.ID); !errors.Is(err, domain.ErrNotFound) {
			t.Fatalf("finished task %d still present: %v", task.ID, err)
		}
	}
	for _, task := range active {
		got, err := repo.GetByID(ctx, task.ID)
		if err != nil || got.Status != task.Status {
			t.Fatalf("active task %d changed: got=%v err=%v", task.ID, got, err)
		}
	}
	heldID := finished[0].ID
	if claimed, err := repo.MarkRunning(ctx, heldID); err != nil || claimed {
		t.Fatalf("claim purged task = %v, %v, want false, nil", claimed, err)
	}
	if err := repo.MarkSucceeded(ctx, heldID); err != nil {
		t.Fatal(err)
	}
	if err := repo.MarkRetry(ctx, heldID, "late result", time.Now()); err != nil {
		t.Fatal(err)
	}
	// Missing-ID error behavior is separate from the retired-state contract;
	// regardless of that error, RetryNow must never insert a replacement row.
	_ = repo.RetryNow(ctx, heldID)
	if _, err := repo.GetByID(ctx, heldID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("purged task resurrected by a late callback: %v", err)
	}
}

func TestSyncTaskRetiredGuardPreservesOrdinaryLifecycle(t *testing.T) {
	repo := retiredSyncTaskTestRepo(t)
	ctx := context.Background()
	task := createRetiredSyncTaskFixture(t, repo, domain.SyncTaskCanceled, 91)
	if err := repo.RetryNow(ctx, task.ID); err != nil {
		t.Fatalf("ordinary canceled task cannot be retried: %v", err)
	}
	if claimed, err := repo.MarkRunning(ctx, task.ID); err != nil || !claimed {
		t.Fatalf("claim ordinary retry = %v, %v, want true, nil", claimed, err)
	}
	if err := repo.MarkRetry(ctx, task.ID, "temporary panel outage", time.Now().Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	got, err := repo.GetByID(ctx, task.ID)
	if err != nil || got.Status != domain.SyncTaskPending || got.Attempts != task.Attempts+1 {
		t.Fatalf("ordinary MarkRetry changed semantics: got=%v err=%v", got, err)
	}
	if claimed, err := repo.MarkRunning(ctx, task.ID); err != nil || !claimed {
		t.Fatalf("reclaim ordinary retry = %v, %v", claimed, err)
	}
	if err := repo.MarkSucceeded(ctx, task.ID); err != nil {
		t.Fatal(err)
	}
	got, err = repo.GetByID(ctx, task.ID)
	if err != nil || got.Status != domain.SyncTaskSucceeded || got.FinishedAt == nil {
		t.Fatalf("ordinary success changed semantics: got=%v err=%v", got, err)
	}
	running := createRetiredSyncTaskFixture(t, repo, domain.SyncTaskRunning, 92)
	if err := repo.ResetRunning(ctx); err != nil {
		t.Fatal(err)
	}
	got, err = repo.GetByID(ctx, running.ID)
	if err != nil || got.Status != domain.SyncTaskPending {
		t.Fatalf("startup no longer recovers ordinary running task: got=%v err=%v", got, err)
	}
}
