package user

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

// fakeSyncTaskRepo records the arguments a status read passes down and returns
// canned rows, so the tests can pin both the query contract and the shaping.
type fakeSyncTaskRepo struct {
	ports.SyncTaskRepo
	active   []*domain.SyncTask
	terminal []*domain.SyncTask
	err      error

	gotActiveTypes  []domain.SyncTaskType
	gotActiveTarget string
	gotActiveID     int64
	gotActiveLimit  int
	gotTermLimit    int
}

func (r *fakeSyncTaskRepo) ListActiveByTarget(_ context.Context, types []domain.SyncTaskType, targetType string, targetID int64, limit int) ([]*domain.SyncTask, error) {
	r.gotActiveTypes, r.gotActiveTarget, r.gotActiveID, r.gotActiveLimit = types, targetType, targetID, limit
	if r.err != nil {
		return nil, r.err
	}
	return r.active, nil
}

func (r *fakeSyncTaskRepo) ListTerminalByTarget(_ context.Context, _ []domain.SyncTaskType, _ string, _ int64, limit int) ([]*domain.SyncTask, error) {
	r.gotTermLimit = limit
	if r.err != nil {
		return nil, r.err
	}
	return r.terminal, nil
}

func syncStatusService(tasks ports.SyncTaskRepo, users ...*domain.User) *Service {
	return &Service{users: &bfUserRepo{users: users}, tasks: tasks}
}

func activeUserTask(id int64) *domain.SyncTask {
	return &domain.SyncTask{
		ID: id, Type: domain.SyncTaskUserResync, Status: domain.SyncTaskPending,
		TargetType: "user", TargetID: 7,
		Summary: "resync after group change", Payload: `{"uuid":"secret-uuid"}`,
		LastError: "dial tcp 10.0.0.1: refused",
		Attempts:  3, NextRunAt: time.Unix(1, 0).UTC(),
	}
}

func TestSyncStatusNoActiveTasksNeverClaimsSynced(t *testing.T) {
	repo := &fakeSyncTaskRepo{}
	svc := syncStatusService(repo, &domain.User{ID: 7})

	got, err := svc.SyncStatus(context.Background(), 7)
	if err != nil {
		t.Fatalf("sync status: %v", err)
	}
	if got.State != SyncStatusNoActiveTasks {
		t.Fatalf("want %q, got %q", SyncStatusNoActiveTasks, got.State)
	}
	// The whole point of the contract: no active task is an observation about
	// local work, not a statement that upstream has the config.
	if strings.Contains(strings.ToLower(string(got.State)), "sync") && got.State != SyncStatusNoActiveTasks {
		t.Fatalf("state must not be spelled as a sync verdict, got %q", got.State)
	}
	if got.HistoryScope != SyncStatusHistoryScopeRetained {
		t.Fatalf("want history scope %q, got %q", SyncStatusHistoryScopeRetained, got.HistoryScope)
	}
}

func TestSyncStatusReportsActiveTasks(t *testing.T) {
	task := activeUserTask(41)
	repo := &fakeSyncTaskRepo{active: []*domain.SyncTask{task}}
	svc := syncStatusService(repo, &domain.User{ID: 7})

	got, err := svc.SyncStatus(context.Background(), 7)
	if err != nil {
		t.Fatalf("sync status: %v", err)
	}
	if got.State != SyncStatusActiveTasks {
		t.Fatalf("want %q, got %q", SyncStatusActiveTasks, got.State)
	}
	if len(got.ActiveTasks) != 1 || got.ActiveTasks[0].ID != 41 {
		t.Fatalf("want the active task, got %+v", got.ActiveTasks)
	}
	if !got.TargetExists {
		t.Fatal("the user row exists, so target_exists must be true")
	}
}

// The query must be jointly bounded by target kind, target id and an explicit
// type set — a bare target_id would collide across resource kinds.
func TestSyncStatusQueriesOneKindAndAFixedTypeSet(t *testing.T) {
	repo := &fakeSyncTaskRepo{}
	svc := syncStatusService(repo, &domain.User{ID: 7})

	if _, err := svc.SyncStatus(context.Background(), 7); err != nil {
		t.Fatalf("sync status: %v", err)
	}
	if repo.gotActiveTarget != "user" || repo.gotActiveID != 7 {
		t.Fatalf("want target user/7, got %s/%d", repo.gotActiveTarget, repo.gotActiveID)
	}
	want := map[domain.SyncTaskType]bool{
		domain.SyncTaskUserDelete:     true,
		domain.SyncTaskUserResync:     true,
		domain.SyncTaskUserPushConfig: true,
		domain.SyncTaskUserMigrate:    true,
	}
	if len(repo.gotActiveTypes) != len(want) {
		t.Fatalf("want %d covered types, got %v", len(want), repo.gotActiveTypes)
	}
	for _, ty := range repo.gotActiveTypes {
		if !want[ty] {
			t.Fatalf("unexpected task type in the query: %s", ty)
		}
	}
	// Both reads must be bounded.
	if repo.gotActiveLimit <= 0 || repo.gotTermLimit <= 0 {
		t.Fatalf("both reads need a limit, got %d / %d", repo.gotActiveLimit, repo.gotTermLimit)
	}
}

// A store that cannot answer must not be reported as "nothing pending" — that is
// exactly the HasPendingSync behaviour this contract exists to avoid.
func TestSyncStatusStoreFailureIsAnErrorNotAnEmptyAnswer(t *testing.T) {
	svc := syncStatusService(&fakeSyncTaskRepo{err: errors.New("db down")}, &domain.User{ID: 7})

	got, err := svc.SyncStatus(context.Background(), 7)
	if err == nil {
		t.Fatalf("want an error, got status %+v", got)
	}
	if !errors.Is(err, domain.ErrUnavailable) {
		t.Fatalf("want ErrUnavailable, got %v", err)
	}
}

func TestSyncStatusUnwiredTaskRepoIsUnavailable(t *testing.T) {
	svc := syncStatusService(nil, &domain.User{ID: 7})

	if _, err := svc.SyncStatus(context.Background(), 7); !errors.Is(err, domain.ErrUnavailable) {
		t.Fatalf("want ErrUnavailable, got %v", err)
	}
}

// The task view is a deliberate subset: task payloads and raw upstream errors
// can carry credentials and endpoints.
func TestSyncStatusTaskViewOmitsSensitiveFields(t *testing.T) {
	repo := &fakeSyncTaskRepo{active: []*domain.SyncTask{activeUserTask(41)}}
	svc := syncStatusService(repo, &domain.User{ID: 7})

	got, err := svc.SyncStatus(context.Background(), 7)
	if err != nil {
		t.Fatalf("sync status: %v", err)
	}
	view := got.ActiveTasks[0]
	if !view.HasError {
		t.Fatal("a task carrying a last_error must report has_error")
	}
	if view.Attempts != 3 || view.Type != domain.SyncTaskUserResync {
		t.Fatalf("want attempts/type preserved, got %d/%s", view.Attempts, view.Type)
	}
}

// An admin may look at a target that no longer exists to see what happened to
// its retained tasks. That must be a successful answer with target_exists=false,
// not a 404 — and it must not be mistaken for "this user is fine".
func TestSyncStatusMissingTargetStillReportsRetainedTasks(t *testing.T) {
	repo := &fakeSyncTaskRepo{terminal: []*domain.SyncTask{{ID: 9, Type: domain.SyncTaskUserDelete, Status: domain.SyncTaskSucceeded}}}
	svc := syncStatusService(repo)

	got, err := svc.SyncStatus(context.Background(), 404)
	if err != nil {
		t.Fatalf("a missing target must still answer: %v", err)
	}
	if got.TargetExists {
		t.Fatal("target_exists must be false for a missing user")
	}
	if len(got.RecentTerminalTasks) != 1 {
		t.Fatalf("want retained terminal tasks, got %d", len(got.RecentTerminalTasks))
	}
}
