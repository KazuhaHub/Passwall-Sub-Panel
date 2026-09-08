package node

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

// failingTasks cannot record anything, so a push that needs a retry has
// nowhere to leave one.
type failingTasks struct{ ports.SyncTaskRepo }

func (failingTasks) GetActiveByTarget(context.Context, domain.SyncTaskType, string, int64) (*domain.SyncTask, error) {
	return nil, domain.ErrNotFound
}
func (failingTasks) Create(context.Context, *domain.SyncTask) error {
	return errors.New("task table unavailable")
}

type unreachablePool struct{ ports.XUIPool }

func (unreachablePool) Get(int64) (ports.XUIClient, error) {
	return nil, errors.New("panel unreachable")
}

// "The push failed, so it was queued" is a real outcome and nil is right for
// it. It is only wrong when the enqueue ITSELF fails — then the inbound is
// still enabled on the panel, nothing is pending to fix it, and the admin has
// been told the node is off.
//
// Both halves are asserted together on purpose: a fix that returns an error
// whenever the push fails would be equally wrong in the other direction,
// turning every transient panel outage into a red banner for work that is
// already durably queued.
func TestSetEnabledDistinguishesQueuedFromUnqueued(t *testing.T) {
	node := func() *getByIDRepo {
		return &getByIDRepo{node: &domain.Node{ID: 7, PanelID: 1, InboundID: 11, Enabled: true}}
	}

	t.Run("panel unreachable but the retry is queued", func(t *testing.T) {
		svc := &Service{nodes: node(), pool: unreachablePool{}, tasks: &recordingTasks{}}
		if err := svc.SetEnabled(context.Background(), 7, false); err != nil {
			t.Fatalf("a queued retry is a real outcome, not a failure: %v", err)
		}
	})

	t.Run("panel unreachable AND the retry cannot be queued", func(t *testing.T) {
		svc := &Service{nodes: node(), pool: unreachablePool{}, tasks: failingTasks{}}
		err := svc.SetEnabled(context.Background(), 7, false)
		if err == nil {
			t.Fatal("nothing reached the panel and nothing is pending, but the caller was told it succeeded")
		}
		// The message has to say what to do next, or it reads as "it broke,
		// leave it alone" — which leaves the inbound enabled.
		if !strings.Contains(err.Error(), "repeat this action") {
			t.Fatalf("error does not tell the operator how to converge: %v", err)
		}
	})

	// The local row is committed either way, so a repeat is safe and is the
	// documented remedy.
	t.Run("the local state is written even when both fail", func(t *testing.T) {
		repo := node()
		svc := &Service{nodes: repo, pool: unreachablePool{}, tasks: failingTasks{}}
		_ = svc.SetEnabled(context.Background(), 7, false)
		if !repo.enabledWritten || repo.enabledValue {
			t.Fatalf("local disable must still be committed: written=%v value=%v",
				repo.enabledWritten, repo.enabledValue)
		}
	})
}

// The entry an admin edit produces is built from a request DTO, so the fields
// the form does not carry arrive as zero. UpdateSeparator reconciles it with
// storage before writing, and the caller gets back what is stored rather than
// what it sent — the response feeds the admin table's local row, so a zero
// created_at there rendered a real row as "created in year 1" even though the
// database was fine. The write was honest and the answer was not.
func TestUpdateSeparatorAnswersFromStorage(t *testing.T) {
	created := time.Date(2026, 9, 8, 7, 0, 0, 0, time.UTC)
	repo := &captureSeparatorRepo{stored: &domain.SeparatorEntry{
		ID: 1, DisplayName: "---- TW ----", SortOrder: 40, Enabled: true, CreatedAt: created,
	}}
	svc := &Service{separators: repo}

	// What admin_node.go hands over: no SortOrder (the dialog dropped the
	// field) and no CreatedAt (the request has no such field).
	e := &domain.SeparatorEntry{ID: 1, DisplayName: "---- TW ----", Enabled: false}
	if err := svc.UpdateSeparator(context.Background(), e); err != nil {
		t.Fatal(err)
	}
	if !e.CreatedAt.Equal(created) {
		t.Fatalf("created_at handed back = %v, want %v — the response would misreport a healthy row", e.CreatedAt, created)
	}
	if e.SortOrder != 40 {
		t.Fatalf("sort_order = %d, want the stored 40 — a zero would clobber the dragged position", e.SortOrder)
	}
	if repo.updated == nil || repo.updated.Enabled {
		t.Fatal("the disable itself must still be written")
	}
}

// Updating a separator that is gone must fail. GORM's Save inserts when the
// primary key is absent, so without the load this silently resurrected a
// deleted row under the requested id.
func TestUpdateSeparatorRejectsAMissingRow(t *testing.T) {
	svc := &Service{separators: &captureSeparatorRepo{}} // stored == nil
	err := svc.UpdateSeparator(context.Background(), &domain.SeparatorEntry{ID: 404, DisplayName: "x"})
	if !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound — otherwise Save recreates the deleted row", err)
	}
}
