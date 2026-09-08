package node

import (
	"context"
	"errors"
	"strings"
	"testing"

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
