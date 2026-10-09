package app

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/pkg/metrics"
)

func TestBuildHourlyCleanupPrunesExpiredDestinationExemptions(t *testing.T) {
	f := buildDestinationPolicyFixture(t)
	a := f.a
	now := time.Now().UTC()
	until := now.Add(-time.Minute)
	later := now.Add(time.Hour)
	for index, expiry := range []*time.Time{&until, nil, &later} {
		user := *f.user
		if index > 0 {
			user.ID = 0
			user.UPN = fmt.Sprintf("expiry-%d@example.test", index)
			user.Email, user.SSOSubject = user.UPN, user.UPN
			user.UUID = fmt.Sprintf("11111111-1111-4111-8111-%012d", index)
			user.SubToken = fmt.Sprintf("expiry-fixture-sub-token-%d", index)
			if err := a.repos.User.Create(t.Context(), &user); err != nil {
				t.Fatal(err)
			}
		}
		ex := domain.DestExemption{UserID: user.ID, CreatedBy: f.user.ID, Reason: "Cleanup fixture", ExpiresAt: expiry}
		if err := a.destDefinitions.SaveExemption(t.Context(), &ex, true, now); err != nil {
			t.Fatal(err)
		}
	}
	before, err := a.destDefinitions.State(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	// Use the production hourly loop with the actual Build-owned SQL store.
	// Its startup pass avoids an hour-long test while preserving real wiring.
	worker := &App{destDefinitions: a.destDefinitions, operationGate: a.operationGate}
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() { defer close(done); worker.runAuditCleanupLoop(ctx) }()
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(3 * time.Second):
			t.Error("hourly destination cleanup did not join on cancellation")
		}
	})
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		rows, err := a.destDefinitions.ListExemptions(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		if len(rows) == 2 {
			for _, row := range rows {
				if row.UserID == f.user.ID {
					t.Fatal("cleanup removed an unexpired exemption")
				}
			}
			after, err := a.destDefinitions.State(t.Context())
			if err != nil || after.Generation != before.Generation+1 {
				t.Fatal("expiry cleanup did not advance one durable definition generation")
			}
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("production hourly cleanup left the expired destination exemption stored")
}

func TestHourlyDestinationCleanupCancellationWhileBackendSwitchOwnsGate(t *testing.T) {
	f := buildDestinationPolicyFixture(t)
	a := f.a
	now := time.Now().UTC()
	expiry := now.Add(-time.Minute)
	ex := domain.DestExemption{UserID: f.user.ID, CreatedBy: f.user.ID, Reason: "Gate fixture", ExpiresAt: &expiry}
	if err := a.destDefinitions.SaveExemption(t.Context(), &ex, true, now); err != nil {
		t.Fatal(err)
	}
	entered, release, ownerDone := make(chan struct{}), make(chan struct{}), make(chan error, 1)
	go func() {
		ownerDone <- a.operationGate.Exclusive(t.Context(), func(context.Context) error {
			close(entered)
			<-release
			return nil
		})
	}()
	<-entered
	t.Cleanup(func() {
		close(release)
		if err := <-ownerDone; err != nil {
			t.Error(err)
		}
	})
	worker := &App{destDefinitions: a.destDefinitions, operationGate: a.operationGate}
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() { defer close(done); worker.runAuditCleanupLoop(ctx) }()
	time.Sleep(50 * time.Millisecond)
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("cleanup ignored cancellation while waiting for backend admission")
	}
	rows, err := a.destDefinitions.ListExemptions(t.Context())
	state, stateErr := a.destDefinitions.State(t.Context())
	if err != nil || stateErr != nil || len(rows) != 1 || state.Generation != 1 {
		t.Fatal("cleanup wrote while backend-switch admission was held")
	}
}

func TestDestinationExpiryCleanupCountsOnlyCommittedDeletesAndRetriesFailure(t *testing.T) {
	f := buildDestinationPolicyFixture(t)
	a := f.a
	now := time.Now().UTC()
	expiry := now.Add(-time.Minute)
	ex := domain.DestExemption{UserID: f.user.ID, CreatedBy: f.user.ID, Reason: "Rollback fixture", ExpiresAt: &expiry}
	if err := a.destDefinitions.SaveExemption(t.Context(), &ex, true, now); err != nil {
		t.Fatal(err)
	}
	counter := metrics.DestPrunedRowsTotal.With(metrics.DestPruneExemptions)
	before := counter.Value()
	if _, err := a.database.ExecContext(t.Context(), "CREATE TRIGGER expiry_cleanup_failure BEFORE UPDATE OF generation ON dest_policy_state BEGIN SELECT RAISE(ABORT, 'expiry-cleanup-private-marker'); END"); err != nil {
		t.Fatal(err)
	}
	a.pruneDestExemptions(t.Context())
	rows, err := a.destDefinitions.ListExemptions(t.Context())
	state, stateErr := a.destDefinitions.State(t.Context())
	if err != nil || stateErr != nil || len(rows) != 1 || state.Generation != 1 || counter.Value() != before {
		t.Fatal("failed expiry cleanup lost rows, generation or counted uncommitted deletes")
	}
	if _, err := a.database.ExecContext(t.Context(), "DROP TRIGGER expiry_cleanup_failure"); err != nil {
		t.Fatal(err)
	}
	a.pruneDestExemptions(t.Context())
	a.pruneDestExemptions(t.Context())
	rows, err = a.destDefinitions.ListExemptions(t.Context())
	state, stateErr = a.destDefinitions.State(t.Context())
	if err != nil || stateErr != nil || len(rows) != 0 || state.Generation != 2 || counter.Value() != before+1 {
		t.Fatal("expiry retry or no-op pass changed generation/counter incorrectly")
	}
}
