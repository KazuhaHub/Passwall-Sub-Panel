package user

import (
	"context"
	"errors"
	"testing"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

// retirementPSP is a psp_client provisioner that reports its retirements by
// kind, as clientprov does.
type retirementPSP struct{ retired domain.RetiredClients }

func (p *retirementPSP) SyncUser(context.Context, int64, string, domain.EmailRules, []*domain.Node) (map[int64][]string, error) {
	return p.retired.All(), nil
}

func (p *retirementPSP) SyncUserRetirements(context.Context, int64, string, domain.EmailRules, []*domain.Node) (domain.RetiredClients, error) {
	return p.retired, nil
}

// legacyPSP reports retirements the old way, in one map.
type legacyPSP struct{ retired map[int64][]string }

func (p *legacyPSP) SyncUser(context.Context, int64, string, domain.EmailRules, []*domain.Node) (map[int64][]string, error) {
	return p.retired, nil
}

type deletePool struct {
	deleted  map[int64][]string
	offline  map[int64]bool
	deleteBy error
}

type deleteClient struct {
	ports.XUIClient
	pool  *deletePool
	panel int64
}

func (p *deletePool) Get(id int64) (ports.XUIClient, error) {
	if p.offline[id] {
		return nil, errors.New("panel offline")
	}
	return deleteClient{pool: p, panel: id}, nil
}
func (*deletePool) List() []*domain.XUIPanel   { return nil }
func (*deletePool) Add(*domain.XUIPanel) error { return nil }
func (*deletePool) Remove(int64) error         { return nil }

func (c deleteClient) DelClientByEmail(_ context.Context, email string) error {
	if c.pool.deleteBy != nil {
		return c.pool.deleteBy
	}
	if c.pool.deleted == nil {
		c.pool.deleted = map[int64][]string{}
	}
	c.pool.deleted[c.panel] = append(c.pool.deleted[c.panel], email)
	return nil
}

// offlineProvisionMigrator is a shared migrator whose provision fails, as it
// does while any one of the user's panels is down.
type offlineProvisionMigrator struct{ *resyncMigrator }

func (offlineProvisionMigrator) ProvisionUser(context.Context, int64) error {
	return errors.New("a native node is offline")
}

func newRetirementService(psp PSPClientProvisioner, mig SharedMigrator, life SharedLifecycleSyncer) (*Service, *deletePool) {
	u := &domain.User{ID: 7, Enabled: true, GroupID: 1}
	pool := &deletePool{}
	svc := &Service{
		users:    &memoryUserRepo{byID: map[int64]*domain.User{7: u}},
		groups:   &bfGroupRepo{g: &domain.Group{ID: 1}},
		selector: bfSelector{nodes: []*domain.Node{{ID: 10, PanelID: 1, DesiredProtocol: "vless"}}},
		settings: bfSettings{},
		pool:     pool,
	}
	svc.SetPSPProvisioner(psp)
	svc.SetSharedMigrator(mig)
	svc.SetSharedLifecycleSyncer(life)
	return svc, pool
}

// R5: when a group becomes an allowlist the members lose their client on a
// panel that cannot enforce it, and that must not wait for the user's other
// panels: the provision fails whenever one of them (say, a native node) is
// offline, and with it the old gate the 3X-UI client survived, so the
// allowlist could be walked around with the old subscription.
func TestResyncMembership_WholePanelRemovalDeletesDespiteProvisionFailure(t *testing.T) {
	psp := &retirementPSP{retired: domain.RetiredClients{PanelRemoved: map[int64][]string{9: {"u7@psp.local"}}}}
	svc, pool := newRetirementService(psp, offlineProvisionMigrator{&resyncMigrator{}}, &failingSharedLife{})

	err := svc.ResyncMembership(context.Background(), 7)
	if err == nil {
		t.Fatal("the failed provision must still be returned so the task retries")
	}
	if got := pool.deleted[9]; len(got) != 1 || got[0] != "u7@psp.local" {
		t.Fatalf("the client on the removed panel was not deleted: %v", pool.deleted)
	}
}

func TestResyncMembership_WholePanelRemovalDeletesDespiteLifecycleFailure(t *testing.T) {
	psp := &retirementPSP{retired: domain.RetiredClients{PanelRemoved: map[int64][]string{9: {"u7@psp.local"}}}}
	svc, pool := newRetirementService(psp, &resyncMigrator{}, &failingSharedLife{fail: true})
	if err := svc.ResyncMembership(context.Background(), 7); err == nil {
		t.Fatal("a lifecycle failure must be returned")
	}
	if len(pool.deleted[9]) != 1 {
		t.Fatalf("the client on the removed panel was not deleted: %v", pool.deleted)
	}
}

// A client replaced on the SAME panel is different: deleting it before its
// replacement is live cuts the user off, so it keeps its provision gate.
func TestResyncMembership_RegroupedClientKeptWhenProvisionFails(t *testing.T) {
	psp := &retirementPSP{retired: domain.RetiredClients{Regrouped: map[int64][]string{9: {"u7@psp.local"}}}}
	svc, pool := newRetirementService(psp, offlineProvisionMigrator{&resyncMigrator{}}, &failingSharedLife{})

	if err := svc.ResyncMembership(context.Background(), 7); err == nil {
		t.Fatal("the failed provision must be returned")
	}
	if len(pool.deleted) != 0 {
		t.Fatalf("a regrouped client was deleted before its replacement was live: %v", pool.deleted)
	}
}

func TestResyncMembership_RegroupedClientDeletedOnceProvisioned(t *testing.T) {
	psp := &retirementPSP{retired: domain.RetiredClients{
		Regrouped:    map[int64][]string{9: {"u7-k1@psp.local"}},
		PanelRemoved: map[int64][]string{8: {"u7@psp.local"}},
	}}
	svc, pool := newRetirementService(psp, &resyncMigrator{}, &failingSharedLife{})
	if err := svc.ResyncMembership(context.Background(), 7); err != nil {
		t.Fatal(err)
	}
	if len(pool.deleted[9]) != 1 || len(pool.deleted[8]) != 1 {
		t.Fatalf("both kinds must be deleted when everything succeeds: %v", pool.deleted)
	}
}

// A provisioner that does not say which kind keeps the old behaviour: every
// retirement waits for the provision.
func TestResyncMembership_UndifferentiatedRetirementsKeepTheProvisionGate(t *testing.T) {
	psp := &legacyPSP{retired: map[int64][]string{9: {"u7@psp.local"}}}
	svc, pool := newRetirementService(psp, offlineProvisionMigrator{&resyncMigrator{}}, &failingSharedLife{})
	_ = svc.ResyncMembership(context.Background(), 7)
	if len(pool.deleted) != 0 {
		t.Fatalf("deleted %v without knowing the kind", pool.deleted)
	}
}

// A delete that fails is returned, so the user_resync task retries it on the
// task cadence and the heal sweep, and an unreachable removed panel the same:
// today it is only logged and the stray client lives until something else
// happens to resync the user.
func TestResyncMembership_FailedDeletionsAreReturned(t *testing.T) {
	for name, tc := range map[string]struct {
		retired domain.RetiredClients
		arrange func(p *deletePool)
	}{
		"delete fails, whole panel": {
			retired: domain.RetiredClients{PanelRemoved: map[int64][]string{9: {"u7@psp.local"}}},
			arrange: func(p *deletePool) { p.deleteBy = errors.New("panel refused the delete") },
		},
		"removed panel unreachable": {
			retired: domain.RetiredClients{PanelRemoved: map[int64][]string{9: {"u7@psp.local"}}},
			arrange: func(p *deletePool) { p.offline = map[int64]bool{9: true} },
		},
		"delete fails, regrouped": {
			retired: domain.RetiredClients{Regrouped: map[int64][]string{9: {"u7-k1@psp.local"}}},
			arrange: func(p *deletePool) { p.deleteBy = errors.New("panel refused the delete") },
		},
	} {
		t.Run(name, func(t *testing.T) {
			svc, pool := newRetirementService(&retirementPSP{retired: tc.retired}, &resyncMigrator{}, &failingSharedLife{})
			tc.arrange(pool)
			if err := svc.ResyncMembership(context.Background(), 7); err == nil {
				t.Fatal("a stray client that could not be removed must make the resync fail")
			}
		})
	}
}

// The retained empty row of a removed panel is listed as retired on every
// resync, so after the first delete the client is already gone and the panel
// says so ("client not found"). That is the goal reached, not a failure: it
// must not turn every later resync of the user into an error.
func TestResyncMembership_AnAlreadyAbsentClientIsNotAFailure(t *testing.T) {
	for _, gone := range []error{
		errors.New("POST /panel/api/clients/del/u7%40psp.local: client not found"),
		domain.ErrNotFound,
	} {
		psp := &retirementPSP{retired: domain.RetiredClients{PanelRemoved: map[int64][]string{9: {"u7@psp.local"}}}}
		svc, pool := newRetirementService(psp, &resyncMigrator{}, &failingSharedLife{})
		pool.deleteBy = gone
		if err := svc.ResyncMembership(context.Background(), 7); err != nil {
			t.Fatalf("a client the panel no longer has failed the resync: %v", err)
		}
	}
}
