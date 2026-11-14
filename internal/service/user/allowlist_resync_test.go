package user

import (
	"context"
	"errors"
	"fmt"
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
	getErr   error
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
	if p.getErr != nil {
		return nil, p.getErr
	}
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

// exclusionSelector is a node selector that also says which panels the
// allowlist excluded (group.Service does).
type exclusionSelector struct {
	bfSelector
	excluded map[int64]bool
}

func (s exclusionSelector) AllowlistExcludedPanels(context.Context, *domain.Group) (map[int64]bool, error) {
	return s.excluded, nil
}

// notRegisteredErr is what the pool answers for a panel that was deleted.
var notRegisteredErr = fmt.Errorf("xui panel id 9 not registered: %w", ports.ErrPanelNotRegistered)

func newRetirementService(psp PSPClientProvisioner, mig SharedMigrator, life SharedLifecycleSyncer, excluded ...int64) (*Service, *deletePool) {
	u := &domain.User{ID: 7, Enabled: true, GroupID: 1}
	pool := &deletePool{}
	excl := map[int64]bool{}
	for _, p := range excluded {
		excl[p] = true
	}
	svc := &Service{
		users:    &memoryUserRepo{byID: map[int64]*domain.User{7: u}},
		groups:   &bfGroupRepo{g: &domain.Group{ID: 1}},
		selector: exclusionSelector{bfSelector: bfSelector{nodes: []*domain.Node{{ID: 10, PanelID: 1, DesiredProtocol: "vless"}}}, excluded: excl},
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
	svc, pool := newRetirementService(psp, offlineProvisionMigrator{&resyncMigrator{}}, &failingSharedLife{}, 9)

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
	svc, pool := newRetirementService(psp, &resyncMigrator{}, &failingSharedLife{fail: true}, 9)
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

// A client on a panel the ALLOWLIST excluded is deleted for good, so a failed
// delete (or an unreachable panel) is returned: the user_resync task and the
// heal sweep retry it, and the stray client is the way round the allowlist.
func TestResyncMembership_FailedDeletionsOnAnExcludedPanelAreReturned(t *testing.T) {
	for name, arrange := range map[string]func(p *deletePool){
		"delete refused":    func(p *deletePool) { p.deleteBy = errors.New("panel refused the delete") },
		"panel unreachable": func(p *deletePool) { p.offline = map[int64]bool{9: true} },
		"a different not-found": func(p *deletePool) {
			p.deleteBy = errors.New("POST /panel/api/clients/del/x: endpoint not found")
		},
	} {
		t.Run(name, func(t *testing.T) {
			psp := &retirementPSP{retired: domain.RetiredClients{PanelRemoved: map[int64][]string{9: {"u7@psp.local"}}}}
			svc, pool := newRetirementService(psp, &resyncMigrator{}, &failingSharedLife{}, 9)
			arrange(pool)
			if err := svc.ResyncMembership(context.Background(), 7); err == nil {
				t.Fatal("a stray client that could not be removed must make the resync fail")
			}
		})
	}
}

// An ordinary move between panels (the tag filter now selects another panel)
// is make-before-break: the old client stays until the new one is live, or the
// user is cut off while the new panel is offline. Only an allowlist exclusion
// skips that wait.
func TestResyncMembership_OrdinaryPanelMoveKeepsMakeBeforeBreak(t *testing.T) {
	psp := &retirementPSP{retired: domain.RetiredClients{PanelRemoved: map[int64][]string{9: {"u7@psp.local"}}}}
	svc, pool := newRetirementService(psp, offlineProvisionMigrator{&resyncMigrator{}}, &failingSharedLife{}) // 9 not excluded
	if err := svc.ResyncMembership(context.Background(), 7); err == nil {
		t.Fatal("the failed provision must be returned")
	}
	if len(pool.deleted) != 0 {
		t.Fatalf("the old panel's client was deleted before the new one was live: %v", pool.deleted)
	}
	svc, pool = newRetirementService(psp, &resyncMigrator{}, &failingSharedLife{})
	if err := svc.ResyncMembership(context.Background(), 7); err != nil {
		t.Fatal(err)
	}
	if len(pool.deleted[9]) != 1 {
		t.Fatalf("once provisioned the old client must go: %v", pool.deleted)
	}
}

// Outside an allowlist exclusion a delete that fails keeps the old behaviour:
// logged, and the retained empty row is tried again on the next resync. Turning
// it into an error would make a user with a row on a long-offline panel fail
// every resync and every heal sweep for ever.
func TestResyncMembership_OtherDeletionFailuresAreOnlyLogged(t *testing.T) {
	for name, retired := range map[string]domain.RetiredClients{
		"removed panel": {PanelRemoved: map[int64][]string{9: {"u7@psp.local"}}},
		"regrouped":     {Regrouped: map[int64][]string{9: {"u7-k1@psp.local"}}},
	} {
		for how, arrange := range map[string]func(p *deletePool){
			"refused":     func(p *deletePool) { p.deleteBy = errors.New("panel refused the delete") },
			"unreachable": func(p *deletePool) { p.offline = map[int64]bool{9: true} },
		} {
			t.Run(name+"/"+how, func(t *testing.T) {
				svc, pool := newRetirementService(&retirementPSP{retired: retired}, &resyncMigrator{}, &failingSharedLife{})
				arrange(pool)
				if err := svc.ResyncMembership(context.Background(), 7); err != nil {
					t.Fatalf("a failed delete outside an allowlist exclusion must not fail the resync: %v", err)
				}
			})
		}
	}
}

// The retained empty row of a removed panel is listed as retired on every
// resync. A client the panel no longer has ("client not found") and a panel that
// is no longer registered at all (deleted: its psp_clients rows stay) are the
// goal reached, not failures, even for an excluded panel.
func TestResyncMembership_AnAlreadyGoneClientOrPanelIsNotAFailure(t *testing.T) {
	retired := map[string]domain.RetiredClients{
		"excluded": {PanelRemoved: map[int64][]string{9: {"u7@psp.local"}}},
		"regroup":  {Regrouped: map[int64][]string{9: {"u7-k1@psp.local"}}},
	}
	for name, rc := range retired {
		for how, arrange := range map[string]func(p *deletePool){
			"client not found":     func(p *deletePool) { p.deleteBy = errors.New("POST /panel/api/clients/del/u7: client not found") },
			"record not found":     func(p *deletePool) { p.deleteBy = errors.New("get: (record not found)") },
			"ErrNotFound":          func(p *deletePool) { p.deleteBy = domain.ErrNotFound },
			"panel not registered": func(p *deletePool) { p.getErr = notRegisteredErr },
		} {
			t.Run(name+"/"+how, func(t *testing.T) {
				svc, pool := newRetirementService(&retirementPSP{retired: rc}, &resyncMigrator{}, &failingSharedLife{}, 9)
				arrange(pool)
				if err := svc.ResyncMembership(context.Background(), 7); err != nil {
					t.Fatalf("failed the resync: %v", err)
				}
			})
		}
	}
}

// invalidatingSelector records the member-count invalidations the group service
// is owed when someone joins or leaves a group.
type invalidatingSelector struct {
	bfSelector
	invalidations int
}

func (s *invalidatingSelector) InvalidateMembers() { s.invalidations++ }

// A member joining or leaving a group changes the count the per-node limit is
// judged by, so the cached count must not outlive it.
func TestMembershipChangesInvalidateTheMemberCount(t *testing.T) {
	sel := &invalidatingSelector{}
	u := &domain.User{ID: 7, Enabled: true, GroupID: 1}
	svc := &Service{
		users:    &memoryUserRepo{byID: map[int64]*domain.User{7: u}},
		groups:   &bfGroupRepo{g: &domain.Group{ID: 2}},
		selector: sel,
		settings: bfSettings{},
	}
	if err := svc.ChangeGroupAndSync(context.Background(), 7, 2); err != nil {
		t.Logf("resync: %v", err) // the resync itself is not what is under test
	}
	if sel.invalidations == 0 {
		t.Fatal("moving a user to another group left the cached member counts standing")
	}
	sel.invalidations = 0
	if err := svc.deleteUser(context.Background(), 7); err != nil {
		t.Fatal(err)
	}
	if sel.invalidations == 0 {
		t.Fatal("deleting a user left the cached member counts standing")
	}
}
