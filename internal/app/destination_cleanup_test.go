package app

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/pkg/metrics"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
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

func seedDestinationCleanupRows(t *testing.T, f destinationPolicyFixture) {
	t.Helper()
	now := time.Now().UTC()
	old := now.Add(-31 * 24 * time.Hour).Truncate(time.Hour).UnixMilli()
	hour := now.Truncate(time.Hour).UnixMilli()
	for _, row := range []struct {
		user   int64
		source string
		hour   int64
		dest   string
		port   int
	}{
		{f.user.ID, "p12", old, "expired.test", 443},
		{0, fmt.Sprintf("g%d", f.group.ID), hour, "valid-trial.test", 0},
		{0, "p12", hour, "invalid-anonymous.test", 0},
	} {
		if _, err := f.a.database.ExecContext(t.Context(), "INSERT INTO dest_hits (hour_ms, panel_id, user_id, source, action, dest, port, count, first_at, last_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)", row.hour, f.agent.PanelID, row.user, row.source, "observe", row.dest, row.port, 1, time.UnixMilli(row.hour), time.UnixMilli(row.hour+1)); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := f.a.database.ExecContext(t.Context(), "INSERT INTO dest_audit_batches (agent_id, batch_id, kind, hour_ms, received_at) VALUES (?, ?, ?, ?, ?)", "", "00000000000000000000000000000001", "receiver_loss", hour, now); err != nil {
		t.Fatal(err)
	}
	if _, err := f.a.database.ExecContext(t.Context(), "INSERT INTO dest_audit_batches (agent_id, batch_id, kind, hour_ms, received_at) VALUES (?, ?, ?, ?, ?)", f.agent.AgentID, "00000000000000000000000000000002", "block", old, now.Add(-73*time.Hour)); err != nil {
		t.Fatal(err)
	}
}

func TestBuildPrunesDestRowsThroughHourlyLoopWithoutDeletingTrialOrReceiverMarker(t *testing.T) {
	f := buildDestinationPolicyFixture(t)
	if f.a.destAuditMaintenance == nil {
		t.Fatal("Build did not wire destination audit maintenance")
	}
	seedDestinationCleanupRows(t, f)
	if _, err := f.a.database.ExecContext(t.Context(), "INSERT INTO dest_exemptions (user_id, reason, created_by, created_at) VALUES (?, ?, ?, ?)", 999999, "orphan cleanup fixture", 0, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	worker := &App{destAuditMaintenance: f.a.destAuditMaintenance, destDefinitions: f.a.destDefinitions, operationGate: f.a.operationGate, settings: f.a.settings}
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() { defer close(done); worker.runAuditCleanupLoop(ctx) }()
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(3 * time.Second):
			t.Error("destination cleanup did not stop")
		}
	})
	waitAuditRows(t, f.a, "dest_hits", 1)
	waitAuditRows(t, f.a, "dest_exemptions", 0)
	if auditTableCount(t, f.a, "dest_audit_batches") != 1 {
		t.Fatal("hourly cleanup removed receiver dedup marker or kept expired batch")
	}
	if state, err := f.a.destDefinitions.State(t.Context()); err != nil || state.Generation != 1 {
		t.Fatal("hourly orphan exemption cleanup did not publish its definition change")
	}
}

func TestDestinationCleanupSettingsFailureSkipsTTLButStillRemovesOrphansAndExpiredMarkers(t *testing.T) {
	f := buildDestinationPolicyFixture(t)
	if f.a.destAuditMaintenance == nil {
		t.Fatal("Build did not wire destination audit maintenance")
	}
	seedDestinationCleanupRows(t, f)
	worker := &App{destAuditMaintenance: f.a.destAuditMaintenance, operationGate: f.a.operationGate, settings: retentionSettings{err: errors.New("private settings failure")}}
	worker.pruneDestAudit(t.Context())
	if auditTableCount(t, f.a, "dest_hits") != 2 || auditTableCount(t, f.a, "dest_audit_batches") != 1 {
		t.Fatal("settings outage deleted configured history or skipped orphan/fixed retention cleanup")
	}
}

type destinationCleanupProbe struct {
	settings *domain.DestinationSettings
	calls    int
	err      error
}

func (p *destinationCleanupProbe) PruneDestinationAudit(_ context.Context, _ time.Time, s *domain.DestinationSettings) (domain.DestAuditPruned, error) {
	p.calls++
	p.settings = s
	return domain.DestAuditPruned{Hits: 2, Trial: 3, Usage: 4, Loss: 5, Batches: 6, Budget: 7, Orphans: 8}, p.err
}

func TestDestinationCleanupCountsOnlyCommittedRowsAndHonorsBackendAdmission(t *testing.T) {
	f := buildDestinationPolicyFixture(t)
	p := &destinationCleanupProbe{err: errors.New("private storage failure")}
	a := &App{destAuditMaintenance: p, operationGate: f.a.operationGate, settings: retentionSettings{stored: ports.UISettings{DestHitRetentionDays: 2, DestTrialRetentionDays: 7}}}
	counter := metrics.DestPrunedRowsTotal.With(metrics.DestPruneHits)
	before := counter.Value()
	a.pruneDestAudit(t.Context())
	if p.calls != 1 || counter.Value() != before {
		t.Fatal("failed cleanup counted uncommitted rows or skipped storage")
	}
	p.err = nil
	a.pruneDestAudit(t.Context())
	if p.calls != 2 || p.settings == nil || p.settings.Effective().TrialRetentionDays != 2 || counter.Value() != before+2 {
		t.Fatal("cleanup ignored effective settings or committed row counts")
	}
	if err := a.operationGate.Exclusive(t.Context(), func(context.Context) error {
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		a.pruneDestAudit(ctx)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if p.calls != 2 {
		t.Fatal("cleanup entered storage without backend admission")
	}
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
