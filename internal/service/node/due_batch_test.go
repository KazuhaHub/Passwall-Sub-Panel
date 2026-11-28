package node

import (
	"context"
	"testing"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
)

// ProcessDueBatch reports how many tasks it ran and how many did not succeed.
func TestProcessDueBatchCountsRunAndFailed(t *testing.T) {
	node := &domain.Node{ID: 7, PanelID: 1, InboundID: 11, ConfigSyncState: domain.ConfigSyncPending}
	tasks := &dueTasks{task: &domain.SyncTask{ID: 1, Type: domain.SyncTaskNodeUpdate, TargetID: 7}}
	// An unreachable pool makes runNodeTask fail the way a dead panel does.
	svc := &Service{nodes: &stateRepo{node: node}, tasks: tasks, pool: unreachablePool{}}

	ran, failed, err := svc.ProcessDueBatch(context.Background(), 10)
	if err != nil {
		t.Fatal(err)
	}
	if ran != 1 || failed != 1 {
		t.Fatalf("ran=%d failed=%d, want 1 and 1", ran, failed)
	}
}
