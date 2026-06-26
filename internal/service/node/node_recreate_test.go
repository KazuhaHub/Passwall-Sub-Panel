package node

import (
	"context"
	"testing"
	"time"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

// --- minimal fakes (embed the interface, override only what RecreateInbound uses) ---

type recreateNodeRepo struct {
	ports.NodeRepo
	node    *domain.Node
	updated *domain.Node
}

func (r *recreateNodeRepo) GetByID(_ context.Context, id int64) (*domain.Node, error) {
	if r.node != nil && r.node.ID == id {
		cp := *r.node
		return &cp, nil
	}
	return nil, domain.ErrNotFound
}
func (r *recreateNodeRepo) Update(_ context.Context, n *domain.Node) error {
	cp := *n
	r.updated = &cp
	return nil
}

type recreateClient struct {
	ports.XUIClient
	inbounds    map[int]*ports.Inbound
	addedSpec   ports.InboundSpec
	nextID      int
	deleted     []int
	updatedID   int
	updatedSpec ports.InboundSpec
}

func (c *recreateClient) UpdateInbound(_ context.Context, id int, spec ports.InboundSpec) error {
	c.updatedID = id
	c.updatedSpec = spec
	return nil
}

func (c *recreateClient) GetInbound(_ context.Context, id int) (*ports.Inbound, error) {
	if inb, ok := c.inbounds[id]; ok {
		return inb, nil
	}
	return nil, domain.ErrNotFound
}
func (c *recreateClient) AddInbound(_ context.Context, spec ports.InboundSpec) (int, error) {
	c.addedSpec = spec
	id := c.nextID
	if c.inbounds == nil {
		c.inbounds = map[int]*ports.Inbound{}
	}
	c.inbounds[id] = &ports.Inbound{ID: id, Protocol: spec.Protocol, Port: spec.Port, Settings: spec.Settings}
	return id, nil
}
func (c *recreateClient) DelInbound(_ context.Context, id int) error {
	c.deleted = append(c.deleted, id)
	return nil
}

type recreatePool struct{ c ports.XUIClient }

func (p recreatePool) Get(int64) (ports.XUIClient, error) { return p.c, nil }
func (recreatePool) List() []*domain.XUIPanel             { return nil }
func (recreatePool) Add(*domain.XUIPanel) error           { return nil }
func (recreatePool) Remove(int64) error                   { return nil }

type recreateGroups struct{ ports.GroupRepo }

func (recreateGroups) List(context.Context) ([]*domain.Group, error) { return nil, nil }

type recreateGroupsList struct {
	ports.GroupRepo
	groups []*domain.Group
}

func (g recreateGroupsList) List(context.Context) ([]*domain.Group, error) { return g.groups, nil }

type recreateUsers struct {
	ports.UserRepo
	byGroup map[int64][]*domain.User
}

func (u recreateUsers) ListByGroup(_ context.Context, gid int64) ([]*domain.User, error) {
	return u.byGroup[gid], nil
}

type recreateResyncer struct {
	ids        []int64
	bulkWarmed []int64
}

func (r *recreateResyncer) ResyncMembershipOrEnqueue(_ context.Context, id int64, _ string) error {
	r.ids = append(r.ids, id)
	return nil
}
func (r *recreateResyncer) BulkProvisionNodeMembers(_ context.Context, _ *domain.Node, userIDs []int64) error {
	r.bulkWarmed = append(r.bulkWarmed, userIDs...)
	return nil
}

// provisionNodeMembers (run in the background by recreate) re-provisions every
// ENABLED member of a matching group, skipping disabled members. This is what gives
// recreate its "build inbound AND push clients" one-step behaviour.
func TestProvisionNodeMembers(t *testing.T) {
	resync := &recreateResyncer{}
	svc := &Service{
		resyncer: resync,
		groups:   recreateGroupsList{groups: []*domain.Group{{ID: 7, TagFilter: domain.TagFilter{All: true}}}},
		users: recreateUsers{byGroup: map[int64][]*domain.User{
			7: {{ID: 100, Enabled: true}, {ID: 101, Enabled: false}, {ID: 100, Enabled: true}},
		}},
	}
	svc.provisionNodeMembers(context.Background(), &domain.Node{ID: 1, DisplayName: "TW"})

	// Only the enabled member (100) is resynced, exactly once; disabled (101) skipped.
	if len(resync.ids) != 1 || resync.ids[0] != 100 {
		t.Fatalf("want a single resync for enabled member 100, got %v", resync.ids)
	}
	// The bulk warm-up runs first over the same enabled member set (1 Xray restart).
	if len(resync.bulkWarmed) != 1 || resync.bulkWarmed[0] != 100 {
		t.Fatalf("want bulk warm over [100], got %v", resync.bulkWarmed)
	}
}

// When a tracked dispatcher is wired, the background provision must route through
// it (so App.Shutdown drains it) — not an untracked safego.Go. Pins the GoTracked
// convention for node.Service's handler-spawned work.
func TestProvisionNodeMembersInBackground_UsesTrackedRunner(t *testing.T) {
	var gotName string
	ran := false
	svc := &Service{} // nil resyncer → provisionNodeMembers is a safe no-op inside fn
	svc.SetBackgroundRunner(func(name string, fn func(ctx context.Context)) {
		gotName = name
		fn(context.Background()) // run synchronously for the assertion
		ran = true
	})
	svc.provisionNodeMembersInBackground(&domain.Node{ID: 1})
	if !ran {
		t.Fatal("background provision must route through the wired dispatcher, not an untracked goroutine")
	}
	if gotName != "node.recreate-provision-members" {
		t.Fatalf("dispatcher task name = %q, want node.recreate-provision-members", gotName)
	}
}

// A nil resyncer (not wired) makes member provisioning a safe no-op.
func TestProvisionNodeMembers_NilResyncerNoop(t *testing.T) {
	svc := &Service{groups: recreateGroupsList{groups: []*domain.Group{{ID: 7, TagFilter: domain.TagFilter{All: true}}}}}
	svc.provisionNodeMembers(context.Background(), &domain.Node{ID: 1}) // must not panic
}

// RecreateInboundOnServer rebuilds a node's inbound from PSP's captured snapshot
// onto its (repointed/empty) panel, then relinks the node to the new inbound ID.
func TestRecreateInboundOnServer(t *testing.T) {
	now := time.Now()
	node := &domain.Node{
		ID: 1, PanelID: 10, InboundID: 5, Enabled: true,
		Protocol: "vless", Port: 443,
		InboundRemark:   "TW Static",
		InboundSettings: `{"clients":[]}`,
		StreamSettings:  `{"network":"tcp","security":"reality"}`,
		ConfigSyncedAt:  &now, // HasLocalConfig → true
		ConfigSyncState: "synced",
	}
	// Empty server: the node's old inbound id (5) is absent; AddInbound returns 12.
	cli := &recreateClient{inbounds: map[int]*ports.Inbound{}, nextID: 12}
	repo := &recreateNodeRepo{node: node}
	svc := &Service{nodes: repo, pool: recreatePool{c: cli}, groups: recreateGroups{}}

	if err := svc.RecreateInboundOnServer(context.Background(), 1); err != nil {
		t.Fatalf("RecreateInboundOnServer: %v", err)
	}
	// AddInbound got the node's captured config, enabled.
	if cli.addedSpec.Protocol != "vless" || cli.addedSpec.Port != 443 ||
		cli.addedSpec.StreamSettings != node.StreamSettings || !cli.addedSpec.Enable {
		t.Fatalf("AddInbound spec mismatch: %+v", cli.addedSpec)
	}
	// Node relinked to the newly-created inbound id.
	if repo.updated == nil || repo.updated.InboundID != 12 {
		t.Fatalf("node must be relinked to inbound 12, got %+v", repo.updated)
	}
	if len(cli.deleted) != 0 {
		t.Fatalf("no rollback expected on success, got deletes %v", cli.deleted)
	}
}

// Recreate is IDEMPOTENT: when the inbound already EXISTS it does NOT create a
// duplicate and does NOT error — it just re-provisions clients (so re-clicking pushes
// clients). It only rejects when the inbound is MISSING and there's no captured config.
func TestRecreateInboundOnServer_Guards(t *testing.T) {
	now := time.Now()
	base := func() *domain.Node {
		return &domain.Node{ID: 1, PanelID: 10, InboundID: 5, Protocol: "vless", Port: 443,
			InboundSettings: "{}", ConfigSyncedAt: &now, ConfigSyncState: "synced"}
	}
	// Inbound already present (with a captured snapshot) → idempotent: no error,
	// NO new inbound created, but the snapshot is RE-PUSHED via UpdateInbound to
	// heal an inbound created before the clients[] fix (clients-less SS → un-addable).
	cli := &recreateClient{inbounds: map[int]*ports.Inbound{5: {ID: 5}}, nextID: 12}
	svc := &Service{nodes: &recreateNodeRepo{node: base()}, pool: recreatePool{c: cli}, groups: recreateGroups{}}
	if err := svc.RecreateInboundOnServer(context.Background(), 1); err != nil {
		t.Fatalf("inbound already present must be a no-op re-provision, got err %v", err)
	}
	if cli.addedSpec.Protocol != "" {
		t.Fatalf("must NOT create an inbound when one already exists, got AddInbound spec %+v", cli.addedSpec)
	}
	if cli.updatedID != 5 {
		t.Fatalf("an existing inbound must be healed via UpdateInbound, got updatedID=%d", cli.updatedID)
	}
	// Missing inbound + no captured config → reject (nothing to recreate from).
	n := base()
	n.ConfigSyncedAt = nil
	cli2 := &recreateClient{inbounds: map[int]*ports.Inbound{}, nextID: 12}
	svc2 := &Service{nodes: &recreateNodeRepo{node: n}, pool: recreatePool{c: cli2}, groups: recreateGroups{}}
	if err := svc2.RecreateInboundOnServer(context.Background(), 1); err == nil {
		t.Fatal("must reject when the inbound is missing and there's no captured config")
	}
}
