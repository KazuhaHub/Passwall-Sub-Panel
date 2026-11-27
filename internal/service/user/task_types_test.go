package user

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

// typedDueRepo behaves like the real repo: it applies the type filter and the
// limit to a mixed queue, earliest first, and records every type set it was
// asked for.
type typedDueRepo struct {
	ports.SyncTaskRepo
	queue     []*domain.SyncTask // already in next_run_at order
	asked     [][]domain.SyncTaskType
	succeeded []int64
}

func (r *typedDueRepo) ListDue(_ context.Context, _ time.Time, types []domain.SyncTaskType, limit int) ([]*domain.SyncTask, error) {
	r.asked = append(r.asked, types)
	var out []*domain.SyncTask
	for _, t := range r.queue {
		if slices.Contains(types, t.Type) && len(out) < limit {
			out = append(out, t)
		}
	}
	return out, nil
}
func (r *typedDueRepo) MarkRunning(context.Context, int64) (bool, error) { return true, nil }
func (r *typedDueRepo) MarkSucceeded(_ context.Context, id int64) error {
	r.succeeded = append(r.succeeded, id)
	return nil
}

// 30 due mail rows ahead of one user task used to fill the 20-row window, so
// the user processor saw only mail and skipped all of it, every 30s, until the
// hourly mail loop drained the queue.
func TestProcessDueTasks_NotStarvedByForeignTypes(t *testing.T) {
	repo := &typedDueRepo{}
	for i := 1; i <= 30; i++ {
		repo.queue = append(repo.queue, &domain.SyncTask{ID: int64(i), Type: domain.SyncTaskMailNotify})
	}
	repo.queue = append(repo.queue, &domain.SyncTask{ID: 100, Type: domain.SyncTaskUserResync, TargetType: "user", TargetID: 999})
	svc := &Service{
		users:    &memoryUserRepo{byID: map[int64]*domain.User{}}, // user 999 absent: resync completes
		groups:   &bfGroupRepo{g: &domain.Group{ID: 1}},
		selector: bfSelector{},
		settings: bfSettings{},
		tasks:    repo,
	}
	svc.SetSharedMigrator(&resyncMigrator{})
	svc.SetSharedLifecycleSyncer(&failingSharedLife{})

	if err := svc.ProcessDueTasks(context.Background(), 20); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(repo.succeeded, []int64{100}) {
		t.Fatalf("succeeded = %v, want only the user task 100 on the first pass", repo.succeeded)
	}
	if len(repo.asked) != 1 || !slices.Equal(repo.asked[0], OwnedTaskTypes()) {
		t.Fatalf("ListDue asked for %v, want exactly the user types %v", repo.asked, OwnedTaskTypes())
	}
}
