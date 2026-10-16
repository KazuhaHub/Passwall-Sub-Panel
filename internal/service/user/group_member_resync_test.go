package user

import (
	"context"
	"errors"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

type groupResyncMembersRepo struct {
	ports.UserRepo
	read func(context.Context, int64) ([]*domain.User, error)
	get  func(context.Context, int64) (*domain.User, error)
}

func (r groupResyncMembersRepo) GetByID(ctx context.Context, id int64) (*domain.User, error) {
	return r.get(ctx, id)
}

func (r groupResyncMembersRepo) ListByGroup(ctx context.Context, groupID int64) ([]*domain.User, error) {
	return r.read(ctx, groupID)
}

func awaitGroupResync(t *testing.T, ready <-chan struct{}) {
	t.Helper()
	select {
	case <-ready:
	case <-time.After(5 * time.Second):
		t.Fatal("group membership work did not reach its controlled boundary")
	}
}

func TestGroupMemberResyncMergesQueuedChangesAndRereadsAfterInFlightFailure(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	started, release, done := make(chan struct{}), make(chan struct{}), make(chan struct{})
	var version atomic.Int64
	version.Store(1)
	var calls atomic.Int64
	var observed []int64
	service := New(groupResyncMembersRepo{read: func(got context.Context, groupID int64) ([]*domain.User, error) {
		if got != ctx || groupID != 7 {
			t.Error("group resync lost its tracked context or current group")
		}
		observed = append(observed, version.Load())
		if calls.Add(1) == 1 {
			close(started)
			select {
			case <-release:
			case <-got.Done():
			}
			return nil, errors.New("owned first membership read failure")
		}
		return nil, nil
	}}, nil, nil, nil, nil, nil, nil, nil)
	queued := make(chan func(context.Context), 256)
	service.SetBackgroundRunner(func(name string, work func(context.Context)) {
		if name != "user.resync-group-members" {
			t.Error("group work bypassed its tracked dispatcher")
		}
		queued <- work
	})
	notify := func() {
		var writers sync.WaitGroup
		start, finished := make(chan struct{}), make(chan struct{})
		for range 100 {
			writers.Add(1)
			go func() {
				defer writers.Done()
				<-start
				service.ResyncGroupMembersInBackground(7)
			}()
		}
		close(start)
		go func() { writers.Wait(); close(finished) }()
		awaitGroupResync(t, finished)
	}
	notify()
	if len(queued) != 1 || calls.Load() != 0 {
		t.Fatal("queued changes started duplicate group work or read before dispatch")
	}
	version.Store(2)
	work := <-queued
	go func() { defer close(done); work(ctx) }()
	awaitGroupResync(t, started)
	version.Store(3)
	notify()
	if len(queued) != 0 {
		t.Fatal("in-flight changes launched overlapping full member passes")
	}
	close(release)
	awaitGroupResync(t, done)
	if !reflect.DeepEqual(observed, []int64{2, 3}) {
		t.Fatalf("current membership versions = %v; final change was lost or a queued version was retained", observed)
	}
	version.Store(4)
	service.ResyncGroupMembersInBackground(7)
	if len(queued) != 1 {
		t.Fatal("completed work suppressed a subsequent group change")
	}
	(<-queued)(ctx)
	if !reflect.DeepEqual(observed, []int64{2, 3, 4}) {
		t.Fatal("a completed group result was cached")
	}
}

type groupResyncTasksRepo struct {
	ports.SyncTaskRepo
	tasks []*domain.SyncTask
}

func (*groupResyncTasksRepo) GetActiveByTarget(context.Context, domain.SyncTaskType, string, int64) (*domain.SyncTask, error) {
	return nil, domain.ErrNotFound
}

func (r *groupResyncTasksRepo) Create(_ context.Context, task *domain.SyncTask) error {
	r.tasks = append(r.tasks, task)
	return nil
}

func TestGroupMemberResyncStillAttemptsEveryMemberAndQueuesIndividualFailures(t *testing.T) {
	var attempted []int64
	repo := groupResyncMembersRepo{
		read: func(context.Context, int64) ([]*domain.User, error) {
			return []*domain.User{{ID: 1, UPN: "one"}, {ID: 2, UPN: "two"}, {ID: 3, UPN: "three"}}, nil
		},
		get: func(_ context.Context, id int64) (*domain.User, error) {
			attempted = append(attempted, id)
			return nil, errors.New("owned member synchronization failure")
		},
	}
	tasks := &groupResyncTasksRepo{}
	service := New(repo, nil, nil, tasks, nil, nil, nil, nil)
	service.SetBackgroundRunner(func(_ string, work func(context.Context)) { work(t.Context()) })
	service.ResyncGroupMembersInBackground(7)
	if !reflect.DeepEqual(attempted, []int64{1, 2, 3}) || len(tasks.tasks) != 3 {
		t.Fatal("a member failure aborted the group or bypassed its durable retry")
	}
	for i, task := range tasks.tasks {
		if task.TargetID != int64(i+1) || task.TargetType != "user" || task.Type != domain.SyncTaskUserResync || task.Status != domain.SyncTaskPending {
			t.Fatal("coalesced group work changed an individual failure's durable task")
		}
	}
}

func TestGroupMemberResyncKeepsIndependentGroupsAndReleasesCancelledWork(t *testing.T) {
	var queue groupMemberResyncs
	queued := make(chan func(context.Context), 4)
	dispatch := func(work func(context.Context)) { queued <- work }
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	started, done := make(chan struct{}), make(chan struct{})
	var calls atomic.Int64
	queue.schedule(7, dispatch, func(ctx context.Context) {
		calls.Add(1)
		close(started)
		<-ctx.Done()
	})
	first := <-queued
	go func() { defer close(done); first(ctx) }()
	awaitGroupResync(t, started)
	queue.schedule(7, dispatch, func(context.Context) { t.Error("overlapping work replaced the current pass") })
	independent := false
	queue.schedule(8, dispatch, func(context.Context) { independent = true })
	if len(queued) != 1 {
		t.Fatal("one group blocked independent group dispatch or duplicated its own work")
	}
	(<-queued)(t.Context())
	if !independent {
		t.Fatal("an active group prevented another group's work")
	}
	cancel()
	awaitGroupResync(t, done)
	if calls.Load() != 1 {
		t.Fatal("cancelled work repeated a dirty pass under a dead context")
	}
	current := false
	queue.schedule(7, dispatch, func(context.Context) { current = true })
	if len(queued) != 1 {
		t.Fatal("cancelled group remained permanently scheduled")
	}
	(<-queued)(t.Context())
	if !current || len(queue.active) != 0 {
		t.Fatal("fresh group work failed or completed entries were retained")
	}
}

func TestGroupMemberResyncReleasesPanickedWorkForNextChange(t *testing.T) {
	var queue groupMemberResyncs
	var queued func(context.Context)
	dispatch := func(work func(context.Context)) { queued = work }
	queue.schedule(7, dispatch, func(context.Context) { panic("owned group membership failure") })
	func() {
		defer func() {
			if recover() != "owned group membership failure" {
				t.Error("group resync swallowed or changed the background panic")
			}
		}()
		queued(t.Context())
	}()
	called := false
	queue.schedule(7, dispatch, func(context.Context) { called = true })
	queued(t.Context())
	if !called || len(queue.active) != 0 {
		t.Fatal("recovered background failure suppressed the next group change")
	}
}
