package user

import (
	"context"
	"errors"
	"testing"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
)

type resyncMigrator struct {
	provisioned   []int64
	deletedLegacy []int64
	reconciled    []int64
}

func (m *resyncMigrator) ProvisionUser(_ context.Context, id int64) error {
	m.provisioned = append(m.provisioned, id)
	return nil
}
func (m *resyncMigrator) DeleteLegacyForUser(_ context.Context, id int64) error {
	m.deletedLegacy = append(m.deletedLegacy, id)
	return nil
}
func (m *resyncMigrator) ReconcileOrphans(_ context.Context, id int64) error {
	m.reconciled = append(m.reconciled, id)
	return nil
}

type failingSharedLife struct {
	fail  bool
	calls int
}

func (f *failingSharedLife) SyncUserLifecycle(context.Context, int64, bool, int64, int64) error {
	f.calls++
	if f.fail {
		return errors.New("updateClient timeout")
	}
	return nil
}

// Audit regression (final review, confirmed HIGH): the shared client is created at
// the spec default Enable:true, and the lifecycle push is the ONLY thing that
// corrects it to the user's real (e.g. disabled) state. If that push FAILS, the
// legacy per-node fallback — which holds the correct disabled state — must NOT be
// deleted, and ResyncMembership must return an error so the sync-task retries.
// Otherwise a disabled user is left with a fully-enabled shared client and no
// fallback (reopening the audit-#1 enforcement bypass on the failure window).
func TestResyncMembership_LifecycleFailureKeepsLegacy(t *testing.T) {
	disabled := &domain.User{ID: 7, Enabled: false, GroupID: 1}
	svc := &Service{
		users:    &memoryUserRepo{byID: map[int64]*domain.User{7: disabled}},
		groups:   &bfGroupRepo{g: &domain.Group{ID: 1}},
		selector: bfSelector{nodes: []*domain.Node{{ID: 10, PanelID: 1, Protocol: "vless"}}},
		settings: bfSettings{},
	}
	mig := &resyncMigrator{}
	life := &failingSharedLife{fail: true}
	svc.SetSharedMigrator(mig)
	svc.SetSharedLifecycleSyncer(life)

	err := svc.ResyncMembership(context.Background(), 7)
	if err == nil {
		t.Fatal("ResyncMembership must error when the lifecycle push fails (so the task retries)")
	}
	if len(mig.provisioned) != 1 {
		t.Fatalf("shared client should still be provisioned: %v", mig.provisioned)
	}
	if len(mig.deletedLegacy) != 0 {
		t.Fatalf("legacy fallback MUST be kept on lifecycle-push failure, but DeleteLegacyForUser ran: %v", mig.deletedLegacy)
	}
}

// Happy path: lifecycle push succeeds -> the legacy fallback IS deleted.
func TestResyncMembership_LifecycleSuccessDeletesLegacy(t *testing.T) {
	u := &domain.User{ID: 8, Enabled: true, GroupID: 1}
	svc := &Service{
		users:    &memoryUserRepo{byID: map[int64]*domain.User{8: u}},
		groups:   &bfGroupRepo{g: &domain.Group{ID: 1}},
		selector: bfSelector{nodes: []*domain.Node{{ID: 10, PanelID: 1, Protocol: "vless"}}},
		settings: bfSettings{},
	}
	mig := &resyncMigrator{}
	life := &failingSharedLife{fail: false}
	svc.SetSharedMigrator(mig)
	svc.SetSharedLifecycleSyncer(life)

	if err := svc.ResyncMembership(context.Background(), 8); err != nil {
		t.Fatalf("happy path should succeed: %v", err)
	}
	if len(mig.deletedLegacy) != 1 {
		t.Fatalf("legacy should be deleted when the lifecycle push succeeds: %v", mig.deletedLegacy)
	}
}
