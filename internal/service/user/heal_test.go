package user

import (
	"context"
	"errors"
	"sort"
	"sync"
	"testing"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
)

// healMigrator is a thread-safe SharedMigrator fake (HealSharedClients provisions
// from concurrent workers). failOn forces ProvisionUser to error for one user.
type healMigrator struct {
	mu          sync.Mutex
	provisioned []int64
	failOn      int64
}

func (m *healMigrator) ProvisionUser(_ context.Context, userID int64) error {
	if userID == m.failOn {
		return errors.New("provision boom")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.provisioned = append(m.provisioned, userID)
	return nil
}
func (m *healMigrator) DeleteLegacyForUser(context.Context, int64) error { return nil }

// HealSharedClients must walk every user, skip pending-delete ones, and run
// provision + lifecycle on the rest. A per-user provision failure is logged and
// skipped (best-effort) — it returns the first error but still heals the others.
func TestHealSharedClients(t *testing.T) {
	users := []*domain.User{
		{ID: 1, Enabled: true},
		{ID: 2, Enabled: false, AutoDisabledReason: domain.DisabledPendingDelete}, // skipped
		{ID: 3, Enabled: true},
	}
	mig := &healMigrator{}
	life := &fakeSharedLife{}
	// HealSharedClients now runs a full ResyncMembership per user, so wire the deps
	// it reaches before ProvisionUser: a group, a (node-less) selector, the psp.
	svc := &Service{
		users:    &bfUserRepo{users: users},
		settings: bfSettings{},
		groups:   &bfGroupRepo{g: &domain.Group{ID: 0}},
		selector: bfSelector{},
		psp:      &bfPSP{},
	}
	svc.SetSharedMigrator(mig)
	svc.SetSharedLifecycleSyncer(life)

	healed, err := svc.HealSharedClients(context.Background())
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if healed != 2 {
		t.Fatalf("healed = %d, want 2 (pending-delete user skipped)", healed)
	}
	sort.Slice(mig.provisioned, func(i, j int) bool { return mig.provisioned[i] < mig.provisioned[j] })
	if len(mig.provisioned) != 2 || mig.provisioned[0] != 1 || mig.provisioned[1] != 3 {
		t.Fatalf("provisioned = %v, want [1 3]", mig.provisioned)
	}
	if len(life.calls) != 2 {
		t.Fatalf("lifecycle pushes = %d, want 2 (one per healed user)", len(life.calls))
	}
}

func TestHealSharedClients_NilMigratorNoop(t *testing.T) {
	svc := &Service{users: &bfUserRepo{users: []*domain.User{{ID: 1, Enabled: true}}}, settings: bfSettings{}}
	if n, err := svc.HealSharedClients(context.Background()); n != 0 || err != nil {
		t.Fatalf("nil migrator must be a no-op, got healed=%d err=%v", n, err)
	}
}

// A provision failure for one user must not abort the sweep: the other users
// still heal, and the first error is surfaced.
func TestHealSharedClients_BestEffortOnError(t *testing.T) {
	users := []*domain.User{{ID: 1, Enabled: true}, {ID: 2, Enabled: true}}
	mig := &healMigrator{failOn: 1}
	life := &fakeSharedLife{}
	// HealSharedClients now runs a full ResyncMembership per user, so wire the deps
	// it reaches before ProvisionUser: a group, a (node-less) selector, the psp.
	svc := &Service{
		users:    &bfUserRepo{users: users},
		settings: bfSettings{},
		groups:   &bfGroupRepo{g: &domain.Group{ID: 0}},
		selector: bfSelector{},
		psp:      &bfPSP{},
	}
	svc.SetSharedMigrator(mig)
	svc.SetSharedLifecycleSyncer(life)

	healed, err := svc.HealSharedClients(context.Background())
	if err == nil {
		t.Fatal("want the first provision error surfaced")
	}
	if healed != 1 || len(mig.provisioned) != 1 || mig.provisioned[0] != 2 {
		t.Fatalf("only user 2 should heal: healed=%d provisioned=%v", healed, mig.provisioned)
	}
}
