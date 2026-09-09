package node

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
)

// stateRepo serves one node and records what the state column is written to.
type stateRepo struct {
	fakeNodeRepo
	node    *domain.Node
	written []string
	err     error
}

func (r *stateRepo) GetByID(context.Context, int64) (*domain.Node, error) {
	if r.err != nil {
		return nil, r.err
	}
	cp := *r.node
	return &cp, nil
}

func (r *stateRepo) UpdateInboundConfig(_ context.Context, n *domain.Node) error {
	r.written = append(r.written, n.ConfigSyncState)
	r.node.ConfigSyncState = n.ConfigSyncState
	return nil
}

// "pending" is a promise: the admin dot reads "配置下发待重试" and the Sync
// Tasks view is where the retry would be. Once the retry is cancelled the
// promise is false, and it used to stand forever — the row stayed pending with
// nothing queued and nothing to notice.
func TestGivingUpMovesTheRowOffPending(t *testing.T) {
	for _, typ := range []domain.SyncTaskType{domain.SyncTaskNodeUpdate, domain.SyncTaskNodeCreate} {
		t.Run(string(typ), func(t *testing.T) {
			repo := &stateRepo{node: &domain.Node{ID: 7, ConfigSyncState: domain.ConfigSyncPending}}
			svc := &Service{nodes: repo}

			svc.markConfigSyncGaveUp(context.Background(),
				&domain.SyncTask{ID: 1, Type: typ, TargetID: 7}, errors.New("panel gone"))

			if len(repo.written) != 1 || repo.written[0] != domain.ConfigSyncFailed {
				t.Fatalf("writes = %v, want one write of %q", repo.written, domain.ConfigSyncFailed)
			}
		})
	}
}

// A task type that does not carry config must not touch the column. Marking a
// failed enable as a CONFIG failure would make the dot mean two things, and
// the operator would go looking for a config problem that does not exist.
func TestGivingUpIgnoresNonConfigTasks(t *testing.T) {
	repo := &stateRepo{node: &domain.Node{ID: 7, ConfigSyncState: domain.ConfigSyncPending}}
	svc := &Service{nodes: repo}

	svc.markConfigSyncGaveUp(context.Background(),
		&domain.SyncTask{ID: 1, Type: domain.SyncTaskNodeSetEnabled, TargetID: 7}, errors.New("x"))

	if len(repo.written) != 0 {
		t.Fatalf("a non-config task wrote the config state: %v", repo.written)
	}
}

// Idempotent: the give-up path can be reached more than once for one node
// (a create task and a later update task can both exhaust), and a second write
// of the same value is pointless traffic against a column three loops contend
// for.
func TestGivingUpIsIdempotent(t *testing.T) {
	repo := &stateRepo{node: &domain.Node{ID: 7, ConfigSyncState: domain.ConfigSyncFailed}}
	svc := &Service{nodes: repo}

	svc.markConfigSyncGaveUp(context.Background(),
		&domain.SyncTask{ID: 1, Type: domain.SyncTaskNodeUpdate, TargetID: 7}, errors.New("x"))

	if len(repo.written) != 0 {
		t.Fatalf("already failed, wrote again: %v", repo.written)
	}
}

// The node may be unreadable — it could have been deleted while its task was
// retrying. That must not panic or stall the rest of the due batch; the task
// was already cancelled by the caller.
func TestGivingUpSurvivesAnUnreadableNode(t *testing.T) {
	repo := &stateRepo{node: &domain.Node{ID: 7}, err: domain.ErrNotFound}
	svc := &Service{nodes: repo}

	svc.markConfigSyncGaveUp(context.Background(),
		&domain.SyncTask{ID: 1, Type: domain.SyncTaskNodeUpdate, TargetID: 7}, errors.New("x"))

	if len(repo.written) != 0 {
		t.Fatalf("wrote despite an unreadable node: %v", repo.written)
	}
}

// dueTasks serves one due task and records the cancellation.
type dueTasks struct {
	ports.SyncTaskRepo
	task      *domain.SyncTask
	cancelled bool
}

func (d *dueTasks) ListDue(context.Context, time.Time, int) ([]*domain.SyncTask, error) {
	return []*domain.SyncTask{d.task}, nil
}
func (d *dueTasks) MarkRunning(context.Context, int64) (bool, error) { return true, nil }
func (d *dueTasks) Cancel(context.Context, int64) error              { d.cancelled = true; return nil }
func (d *dueTasks) MarkRetry(context.Context, int64, string, time.Time) error {
	return nil
}

// The helper being correct is worth nothing if the give-up path does not call
// it. Deleting both call sites left every other test in this file green, which
// is the shape this package has been bitten by before: a correct function
// whose only caller is optional.
//
// Driven through ProcessDueTasks so the assertion covers the real path — a
// task at the retry cap, cancelled, and the node's state moved off pending.
func TestProcessDueTasksMovesTheRowOffPendingWhenItGivesUp(t *testing.T) {
	node := &domain.Node{ID: 7, PanelID: 1, InboundID: 11, ConfigSyncState: domain.ConfigSyncPending}
	repo := &stateRepo{node: node}
	tasks := &dueTasks{task: &domain.SyncTask{
		ID: 1, Type: domain.SyncTaskNodeUpdate, TargetID: 7,
		// One short of the cap, so this attempt is the last one.
		Attempts: maxNodeTaskAttempts - 1,
	}}
	// An unreachable pool makes runNodeTask fail the way a dead panel does.
	svc := &Service{nodes: repo, tasks: tasks, pool: unreachablePool{}}

	if err := svc.ProcessDueTasks(context.Background(), 10); err != nil {
		t.Fatalf("ProcessDueTasks = %v, want nil (a per-task failure is not a batch failure)", err)
	}
	if !tasks.cancelled {
		t.Fatal("the task should have been cancelled at the retry cap")
	}
	if node.ConfigSyncState != domain.ConfigSyncFailed {
		t.Fatalf("state = %q, want %q — the row would keep promising a retry that was cancelled",
			node.ConfigSyncState, domain.ConfigSyncFailed)
	}
}
