package node

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

type retiredGuardTasks struct {
	ports.SyncTaskRepo
	stored *domain.SyncTask
	err    error
	gets   int
}

func (r *retiredGuardTasks) GetByID(_ context.Context, id int64) (*domain.SyncTask, error) {
	r.gets++
	if r.err != nil {
		return nil, r.err
	}
	if r.stored == nil || r.stored.ID != id {
		return nil, domain.ErrNotFound
	}
	copy := *r.stored
	return &copy, nil
}

type retiredGuardNodes struct {
	ports.NodeRepo
	gets int
}

func (r *retiredGuardNodes) GetByID(context.Context, int64) (*domain.Node, error) {
	r.gets++
	return &domain.Node{ID: 31, PanelID: 41, InboundID: 51}, nil
}

type retiredGuardPool struct {
	ports.XUIPool
	gets int
	err  error
}

func (p *retiredGuardPool) Get(int64) (ports.XUIClient, error) {
	p.gets++
	return nil, p.err
}

// node_create has no existing Node row and branches before the normal node
// lookup, so every mutation type must pass the retirement check first.
func TestRunNodeTaskRetiredNeverTouchesBackend(t *testing.T) {
	for _, typ := range []domain.SyncTaskType{
		domain.SyncTaskNodeCreate, domain.SyncTaskNodeDelete,
		domain.SyncTaskNodeSetEnabled, domain.SyncTaskNodeUpdate,
	} {
		for _, scenario := range []struct {
			name       string
			heldStatus domain.SyncTaskStatus
			stored     bool
			lookupErr  error
		}{
			{"explicit retired", domain.SyncTaskRetired, false, errors.New("lookup must not run")},
			{"held pending before conversion", domain.SyncTaskPending, true, nil},
			{"held running before conversion", domain.SyncTaskRunning, true, nil},
			{"held pointer after clear finished", domain.SyncTaskPending, false, fmt.Errorf("purged: %w", domain.ErrNotFound)},
		} {
			t.Run(string(typ)+"/"+scenario.name, func(t *testing.T) {
				held := &domain.SyncTask{
					ID: 11, Type: typ, Status: scenario.heldStatus, TargetType: "node", TargetID: 31,
					// Valid old-server payload: malformed JSON must not accidentally
					// be the reason a create task avoids the backend.
					Payload: `{"node":{"panel_id":41,"port":443},"spec":{"port":443}}`,
				}
				tasks := &retiredGuardTasks{err: scenario.lookupErr}
				if scenario.stored {
					stored := *held
					stored.Status = domain.SyncTaskRetired
					tasks.stored = &stored
				}
				nodes := &retiredGuardNodes{}
				pool := &retiredGuardPool{err: errors.New("backend should not be accessed")}
				svc := &Service{tasks: tasks, nodes: nodes, pool: pool}
				if err := svc.runNodeTask(context.Background(), held); err != nil {
					t.Fatalf("retired or purged task = %v, want safe no-op", err)
				}
				if nodes.gets != 0 || pool.gets != 0 {
					t.Fatalf("old task reached current backend: node reads=%d pool reads=%d", nodes.gets, pool.gets)
				}
				wantGets := 1
				if scenario.heldStatus == domain.SyncTaskRetired {
					wantGets = 0
				}
				if tasks.gets != wantGets {
					t.Fatalf("persistent task lookups=%d, want %d", tasks.gets, wantGets)
				}
			})
		}
	}
}

func TestRunNodeTaskRetirementLookupFailsClosed(t *testing.T) {
	lookupErr := errors.New("task database unavailable")
	tasks := &retiredGuardTasks{err: lookupErr}
	nodes := &retiredGuardNodes{}
	pool := &retiredGuardPool{err: errors.New("unexpected backend access")}
	svc := &Service{tasks: tasks, nodes: nodes, pool: pool}
	err := svc.runNodeTask(context.Background(), &domain.SyncTask{
		ID: 11, Type: domain.SyncTaskNodeCreate, Status: domain.SyncTaskPending,
		Payload: `{"node":{"panel_id":41},"spec":{"port":443}}`,
	})
	if !errors.Is(err, lookupErr) {
		t.Fatalf("lookup failure = %v, want original error", err)
	}
	if tasks.gets != 1 || nodes.gets != 0 || pool.gets != 0 {
		t.Fatalf("lookup failure accessed backend: task=%d node=%d pool=%d", tasks.gets, nodes.gets, pool.gets)
	}
}

func TestRunNodeTaskRetiredGuardAllowsCurrentTask(t *testing.T) {
	for _, persistent := range []bool{false, true} {
		t.Run(fmt.Sprintf("persistent=%v", persistent), func(t *testing.T) {
			held := &domain.SyncTask{Type: domain.SyncTaskNodeUpdate, Status: domain.SyncTaskRunning, TargetID: 31}
			tasks := &retiredGuardTasks{err: errors.New("ID-zero direct call must not look up task")}
			if persistent {
				held.ID = 11
				stored := *held
				tasks.stored, tasks.err = &stored, nil
			}
			nodes := &retiredGuardNodes{}
			backendErr := errors.New("current backend unavailable")
			pool := &retiredGuardPool{err: backendErr}
			svc := &Service{tasks: tasks, nodes: nodes, pool: pool}
			if err := svc.runNodeTask(context.Background(), held); !errors.Is(err, backendErr) {
				t.Fatalf("current task = %v, want backend error", err)
			}
			wantGets := 0
			if persistent {
				wantGets = 1
			}
			if tasks.gets != wantGets || nodes.gets != 1 || pool.gets != 1 {
				t.Fatalf("current task did not execute normally: task=%d node=%d pool=%d", tasks.gets, nodes.gets, pool.gets)
			}
		})
	}
}

func TestRunNodeTaskExplicitRetiredWithoutRepo(t *testing.T) {
	nodes := &retiredGuardNodes{}
	pool := &retiredGuardPool{err: errors.New("unexpected backend access")}
	svc := &Service{nodes: nodes, pool: pool}
	if err := svc.runNodeTask(context.Background(), &domain.SyncTask{Type: domain.SyncTaskNodeCreate, Status: domain.SyncTaskRetired}); err != nil {
		t.Fatal(err)
	}
	if nodes.gets != 0 || pool.gets != 0 {
		t.Fatalf("explicit retirement without repo reached backend: node=%d pool=%d", nodes.gets, pool.gets)
	}
}
