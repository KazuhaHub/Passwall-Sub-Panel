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
	inbounds  map[int]*ports.Inbound
	addedSpec ports.InboundSpec
	nextID    int
	deleted   []int
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

type recreateResyncer struct{ ids []int64 }

func (r *recreateResyncer) ResyncMembershipOrEnqueue(_ context.Context, id int64, _ string) error {
	r.ids = append(r.ids, id)
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

// Recreate refuses when the node's inbound already EXISTS on the panel (the action
// is only for a missing inbound) and when there's no captured config to push.
func TestRecreateInboundOnServer_Guards(t *testing.T) {
	now := time.Now()
	base := func() *domain.Node {
		return &domain.Node{ID: 1, PanelID: 10, InboundID: 5, Protocol: "vless", Port: 443,
			InboundSettings: "{}", ConfigSyncedAt: &now, ConfigSyncState: "synced"}
	}
	// Inbound already present → reject.
	cli := &recreateClient{inbounds: map[int]*ports.Inbound{5: {ID: 5}}, nextID: 12}
	svc := &Service{nodes: &recreateNodeRepo{node: base()}, pool: recreatePool{c: cli}, groups: recreateGroups{}}
	if err := svc.RecreateInboundOnServer(context.Background(), 1); err == nil {
		t.Fatal("must reject when the inbound already exists on the panel")
	}
	// No captured config → reject.
	n := base()
	n.ConfigSyncedAt = nil
	cli2 := &recreateClient{inbounds: map[int]*ports.Inbound{}, nextID: 12}
	svc2 := &Service{nodes: &recreateNodeRepo{node: n}, pool: recreatePool{c: cli2}, groups: recreateGroups{}}
	if err := svc2.RecreateInboundOnServer(context.Background(), 1); err == nil {
		t.Fatal("must reject when the node has no captured inbound config")
	}
}
