package user

import (
	"context"
	"testing"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
)

// ProcessDueBatch reports how many tasks it ran and how many did not succeed.
// Rows of another processor's type are not counted: the loop takes another
// batch only when this processor's own work filled the window.
func TestProcessDueBatchCountsRunAndFailed(t *testing.T) {
	repo := &typedDueRepo{}
	for i := 1; i <= 3; i++ {
		repo.queue = append(repo.queue, &domain.SyncTask{ID: int64(i), Type: domain.SyncTaskUserResync, TargetType: "user", TargetID: 990 + int64(i)})
	}
	svc := &Service{
		users:    &memoryUserRepo{byID: map[int64]*domain.User{}}, // users absent: each resync completes
		groups:   &bfGroupRepo{g: &domain.Group{ID: 1}},
		selector: bfSelector{},
		settings: bfSettings{},
		tasks:    repo,
	}
	svc.SetSharedMigrator(&resyncMigrator{})
	svc.SetSharedLifecycleSyncer(&failingSharedLife{})

	ran, failed, err := svc.ProcessDueBatch(context.Background(), 2)
	if err != nil {
		t.Fatal(err)
	}
	if ran != 2 || failed != 0 {
		t.Fatalf("ran=%d failed=%d, want 2 and 0", ran, failed)
	}
}
