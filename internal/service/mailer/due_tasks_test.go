package mailer

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

// mailQueueRepo is a typed due queue: ListDue applies the type filter and the
// limit like the real repo, and terminal transitions take a task out of it.
type mailQueueRepo struct {
	ports.SyncTaskRepo
	queue     []*domain.SyncTask
	listCalls int
	askedAll  [][]domain.SyncTaskType
	canceled  int
	retried   int
}

func (r *mailQueueRepo) ListDue(_ context.Context, _ time.Time, types []domain.SyncTaskType, limit int) ([]*domain.SyncTask, error) {
	r.listCalls++
	r.askedAll = append(r.askedAll, types)
	var out []*domain.SyncTask
	for _, t := range r.queue {
		if slices.Contains(types, t.Type) && len(out) < limit {
			out = append(out, t)
		}
	}
	return out, nil
}
func (r *mailQueueRepo) remove(id int64) {
	r.queue = slices.DeleteFunc(r.queue, func(t *domain.SyncTask) bool { return t.ID == id })
}
func (r *mailQueueRepo) MarkRunning(context.Context, int64) (bool, error) { return true, nil }
func (r *mailQueueRepo) MarkSucceeded(_ context.Context, id int64) error {
	r.remove(id)
	return nil
}
func (r *mailQueueRepo) Cancel(_ context.Context, id int64) error {
	r.canceled++
	r.remove(id)
	return nil
}

// MarkRetry reschedules into the future, so the task leaves the due set.
func (r *mailQueueRepo) MarkRetry(_ context.Context, id int64, _ string, _ time.Time) error {
	r.retried++
	r.remove(id)
	return nil
}

// failingUsers makes processMailTask fail with a transient (non-validation)
// error, the way a user-store or SMTP failure does, without any SMTP server.
type failingUsers struct{ ports.UserRepo }

func (failingUsers) GetByID(context.Context, int64) (*domain.User, error) {
	return nil, errors.New("smtp relay down")
}

func mailTasks(n int, payload string) []*domain.SyncTask {
	out := make([]*domain.SyncTask, n)
	for i := range out {
		out[i] = &domain.SyncTask{ID: int64(i + 1), Type: domain.SyncTaskMailNotify, Payload: payload}
	}
	return out
}

// The hourly loop used to take 20 tasks per run. It now drains batches: a
// queue longer than one batch is emptied in one run, up to the per-run cap.
func TestProcessDueMailTasksDrainsPastOneBatch(t *testing.T) {
	// A payload that never parses is cancelled as a permanent failure — no
	// SMTP involved, and it does not count toward the failure streak.
	repo := &mailQueueRepo{queue: mailTasks(65, "not json")}
	svc := &Service{tasks: repo}
	if err := svc.ProcessDueMailTasks(context.Background(), 20); err != nil {
		t.Fatal(err)
	}
	if repo.canceled != 65 || len(repo.queue) != 0 {
		t.Fatalf("processed %d, left %d; want all 65 drained in one run", repo.canceled, len(repo.queue))
	}
	// 20+20+20+5: the short fourth batch ends the run without a fifth query.
	if repo.listCalls != 4 {
		t.Fatalf("ListDue called %d times, want 4", repo.listCalls)
	}
	for _, asked := range repo.askedAll {
		if !slices.Equal(asked, OwnedTaskTypes()) {
			t.Fatalf("asked for %v, want only %v", asked, OwnedTaskTypes())
		}
	}
}

func TestProcessDueMailTasksStopsAtPerRunCap(t *testing.T) {
	repo := &mailQueueRepo{queue: mailTasks(maxMailTasksPerRun+50, "not json")}
	svc := &Service{tasks: repo}
	if err := svc.ProcessDueMailTasks(context.Background(), 20); err != nil {
		t.Fatal(err)
	}
	if repo.canceled != maxMailTasksPerRun {
		t.Fatalf("processed %d in one run, want exactly the cap %d", repo.canceled, maxMailTasksPerRun)
	}
	if len(repo.queue) != 50 {
		t.Fatalf("left %d, want 50 for the next run", len(repo.queue))
	}
}

// An SMTP outage fails every send; the run must give up after a short streak
// instead of dialing a dead relay for the whole queue.
func TestProcessDueMailTasksStopsOnFailureStreak(t *testing.T) {
	repo := &mailQueueRepo{queue: mailTasks(100, `{"user_id":1,"template_kind":"account_disabled"}`)}
	svc := &Service{tasks: repo, users: failingUsers{}}
	if err := svc.ProcessDueMailTasks(context.Background(), 20); err != nil {
		t.Fatal(err)
	}
	if repo.retried != mailFailureStreakLimit {
		t.Fatalf("attempted %d sends before stopping, want %d", repo.retried, mailFailureStreakLimit)
	}
}

// Permanent failures say nothing about the relay and must not trip the stop.
func TestProcessDueMailTasksPermanentFailuresDoNotCountAsOutage(t *testing.T) {
	q := mailTasks(10, "not json")
	repo := &mailQueueRepo{queue: q}
	svc := &Service{tasks: repo, users: failingUsers{}}
	if err := svc.ProcessDueMailTasks(context.Background(), 20); err != nil {
		t.Fatal(err)
	}
	if repo.canceled != 10 {
		t.Fatalf("cancelled %d, want 10", repo.canceled)
	}
}
