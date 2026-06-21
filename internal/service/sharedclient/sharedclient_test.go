package sharedclient

import (
	"context"
	"errors"
	"testing"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

// --- embedded-interface fakes: override only what ProvisionClient calls ---

type fakeClients struct {
	ports.PSPClientRepo
	attachments []domain.PSPClientInbound
	provisioned map[int64]bool      // nodeID -> provisioned
	byUser      []*domain.PSPClient // ListByUser result (cleanup tests)
}

func (f *fakeClients) ListInbounds(context.Context, int64) ([]domain.PSPClientInbound, error) {
	// Overlay MarkInboundProvisioned updates so ListInbounds reflects what
	// ProvisionClient just confirmed (without mutating the seed slice).
	out := make([]domain.PSPClientInbound, len(f.attachments))
	copy(out, f.attachments)
	for i := range out {
		if f.provisioned[out[i].NodeID] {
			out[i].Provisioned = true
		}
	}
	return out, nil
}
func (f *fakeClients) ListByUser(context.Context, int64) ([]*domain.PSPClient, error) {
	return f.byUser, nil
}
func (f *fakeClients) MarkInboundProvisioned(_ context.Context, _ int64, nodeID int64, p bool) error {
	if f.provisioned == nil {
		f.provisioned = map[int64]bool{}
	}
	f.provisioned[nodeID] = p
	return nil
}

type fakeNodes struct {
	ports.NodeRepo
	byID map[int64]*domain.Node
}

func (f fakeNodes) GetByID(_ context.Context, id int64) (*domain.Node, error) {
	if n, ok := f.byID[id]; ok {
		return n, nil
	}
	return nil, domain.ErrNotFound
}

type fakeXUI struct {
	ports.XUIClient
	addedInbounds []int
	addedSpec     ports.ClientSpec
	confirm       []int // inboundIDs GetClient reports the client attached to
	updatedSpec   ports.ClientSpec
	updateCalls   int
	deleted       []deletedClient
	detached      []int
	failAdd       bool
}

var errFakeAdd = errors.New("fake add failure")

func (c *fakeXUI) AddClientToInbounds(_ context.Context, inboundIDs []int, spec ports.ClientSpec) error {
	if c.failAdd {
		return errFakeAdd
	}
	c.addedInbounds = append([]int(nil), inboundIDs...)
	c.addedSpec = spec
	return nil
}
func (c *fakeXUI) GetClient(context.Context, string) (*ports.ClientDetail, error) {
	return &ports.ClientDetail{InboundIDs: c.confirm}, nil
}
func (c *fakeXUI) DetachClient(_ context.Context, _ string, inboundIDs []int) error {
	c.detached = append(c.detached, inboundIDs...)
	return nil
}
func (c *fakeXUI) UpdateClient(_ context.Context, _ int, _ string, spec ports.ClientSpec) error {
	c.updatedSpec = spec
	c.updateCalls++
	return nil
}

type deletedClient struct {
	inbound int
	email   string
}

func (c *fakeXUI) DelClientByEmail(_ context.Context, inboundID int, email string) error {
	c.deleted = append(c.deleted, deletedClient{inboundID, email})
	return nil
}

type fakeOwnership struct {
	ports.OwnershipRepo
	entries   []*domain.XUIClientEntry
	removedID []int64
}

func (o *fakeOwnership) ListByUser(context.Context, int64) ([]*domain.XUIClientEntry, error) {
	return o.entries, nil
}
func (o *fakeOwnership) Remove(_ context.Context, id int64) error {
	o.removedID = append(o.removedID, id)
	return nil
}

type fakePool struct {
	ports.XUIPool
	c ports.XUIClient
}

func (p fakePool) Get(int64) (ports.XUIClient, error) { return p.c, nil }

func TestProvisionClient_CreatesAndMarksConfirmed(t *testing.T) {
	clients := &fakeClients{attachments: []domain.PSPClientInbound{
		{ClientID: 1, NodeID: 11, FlowOverride: "xtls-rprx-vision"},
		{ClientID: 1, NodeID: 12, FlowOverride: "xtls-rprx-vision"},
	}}
	nodes := fakeNodes{byID: map[int64]*domain.Node{
		11: {ID: 11, PanelID: 10, InboundID: 101},
		12: {ID: 12, PanelID: 10, InboundID: 102},
	}}
	xui := &fakeXUI{confirm: []int{101, 102}} // 3X-UI confirms both
	svc := New(clients, fakePool{c: xui}, nodes)

	c := &domain.PSPClient{ID: 1, PanelID: 10, Email: "u1@psp.local", UUID: "uuid-x", Password: "pw-x"}
	res, err := svc.ProvisionClient(context.Background(), c)
	if err != nil {
		t.Fatal(err)
	}
	// One AddClientToInbounds over BOTH inbounds (single Xray restart).
	if len(xui.addedInbounds) != 2 || xui.addedInbounds[0] != 101 || xui.addedInbounds[1] != 102 {
		t.Fatalf("added inbounds = %v, want [101 102]", xui.addedInbounds)
	}
	// Spec carries stored creds + flow for multi-protocol projection.
	if xui.addedSpec.ID != "uuid-x" || xui.addedSpec.Password != "pw-x" || xui.addedSpec.Auth != "uuid-x" ||
		xui.addedSpec.Flow != "xtls-rprx-vision" || !xui.addedSpec.Enable {
		t.Fatalf("spec = %+v", xui.addedSpec)
	}
	if !res.Created || res.Provisioned != 2 {
		t.Fatalf("result = %+v, want created + 2 provisioned", res)
	}
	if !clients.provisioned[11] || !clients.provisioned[12] {
		t.Fatalf("both nodes should be marked provisioned: %v", clients.provisioned)
	}
}

// Full reconcile: if 3X-UI reports the shared client attached to an inbound that
// is no longer in the desired set (a node left the group), ProvisionClient must
// DETACH it — not just attach the desired ones.
func TestProvisionClient_DetachesStaleInbound(t *testing.T) {
	clients := &fakeClients{attachments: []domain.PSPClientInbound{
		{ClientID: 1, NodeID: 11}, // desired → inbound 101
	}}
	nodes := fakeNodes{byID: map[int64]*domain.Node{11: {ID: 11, PanelID: 10, InboundID: 101}}}
	// 3X-UI says the client is on 101 (desired) AND 102 (stale — node removed).
	xui := &fakeXUI{confirm: []int{101, 102}}
	svc := New(clients, fakePool{c: xui}, nodes)

	if _, err := svc.ProvisionClient(context.Background(), &domain.PSPClient{ID: 1, PanelID: 10, Email: "u1@psp.local"}); err != nil {
		t.Fatal(err)
	}
	if len(xui.detached) != 1 || xui.detached[0] != 102 {
		t.Fatalf("stale inbound 102 must be detached, got %v", xui.detached)
	}
}

func TestProvisionClient_MarksOnlyConfirmed(t *testing.T) {
	clients := &fakeClients{attachments: []domain.PSPClientInbound{
		{ClientID: 1, NodeID: 11},
		{ClientID: 1, NodeID: 12},
	}}
	nodes := fakeNodes{byID: map[int64]*domain.Node{
		11: {ID: 11, PanelID: 10, InboundID: 101},
		12: {ID: 12, PanelID: 10, InboundID: 102},
	}}
	// 3X-UI confirms only inbound 101 (102's attach silently failed).
	xui := &fakeXUI{confirm: []int{101}}
	svc := New(clients, fakePool{c: xui}, nodes)

	res, err := svc.ProvisionClient(context.Background(), &domain.PSPClient{ID: 1, PanelID: 10, Email: "u1@psp.local"})
	if err != nil {
		t.Fatal(err)
	}
	if res.Provisioned != 1 || !clients.provisioned[11] || clients.provisioned[12] {
		t.Fatalf("only node 11 should be provisioned (12 unconfirmed): res=%+v marks=%v", res, clients.provisioned)
	}
}

func TestSyncLifecycle_PushesEnableExpiryQuotaWithCredsAndFlow(t *testing.T) {
	clients := &fakeClients{attachments: []domain.PSPClientInbound{
		{ClientID: 1, NodeID: 11, FlowOverride: "xtls-rprx-vision", Provisioned: true},
	}}
	xui := &fakeXUI{}
	svc := New(clients, fakePool{c: xui}, fakeNodes{})

	c := &domain.PSPClient{ID: 1, PanelID: 10, Email: "u1@psp.local", UUID: "uuid-x", Password: "pw-x"}
	// disabled, with an expiry + a quota floor
	if err := svc.SyncLifecycle(context.Background(), c, false, 1893456000000, 5<<30); err != nil {
		t.Fatal(err)
	}
	if xui.updateCalls != 1 {
		t.Fatalf("update calls = %d, want 1", xui.updateCalls)
	}
	s := xui.updatedSpec
	if s.Enable || s.ExpiryTime != 1893456000000 || s.TotalGB != 5<<30 {
		t.Fatalf("lifecycle fields not pushed: %+v", s)
	}
	// UpdateClient is full-replace, so creds + flow must ride along unchanged.
	if s.Email != "u1@psp.local" || s.ID != "uuid-x" || s.Password != "pw-x" || s.Auth != "uuid-x" ||
		s.Flow != "xtls-rprx-vision" {
		t.Fatalf("creds/flow not preserved on lifecycle push: %+v", s)
	}
}

func TestSyncLifecycle_NoAttachmentsSkips(t *testing.T) {
	clients := &fakeClients{attachments: nil}
	xui := &fakeXUI{}
	svc := New(clients, fakePool{c: xui}, fakeNodes{})
	if err := svc.SyncLifecycle(context.Background(), &domain.PSPClient{ID: 1, PanelID: 10}, true, 0, 0); err != nil {
		t.Fatal(err)
	}
	if xui.updateCalls != 0 {
		t.Fatalf("a client with no attachments must not be pushed (calls=%d)", xui.updateCalls)
	}
}

// Until the cutover provisions the shared client in 3X-UI, a lifecycle push would
// hit a non-existent email and fail noisily — so an UN-provisioned client (the
// default on every install) must be skipped entirely, no 3X-UI call.
func TestSyncLifecycle_UnprovisionedSkips(t *testing.T) {
	clients := &fakeClients{attachments: []domain.PSPClientInbound{
		{ClientID: 1, NodeID: 11}, // Provisioned: false (dual-write wrote the row; reconcile hasn't run)
	}}
	xui := &fakeXUI{}
	svc := New(clients, fakePool{c: xui}, fakeNodes{})
	if err := svc.SyncLifecycle(context.Background(), &domain.PSPClient{ID: 1, PanelID: 10, Email: "u1@psp.local"}, false, 0, 0); err != nil {
		t.Fatal(err)
	}
	if xui.updateCalls != 0 {
		t.Fatalf("an un-provisioned shared client must not be pushed to 3X-UI (calls=%d)", xui.updateCalls)
	}
}

// The full per-user migration: provision the shared client(s) in 3X-UI, confirm,
// then delete the now-covered legacy per-node clients + ownership rows.
func TestMigrateUser_ProvisionsThenDeletesLegacy(t *testing.T) {
	clients := &fakeClients{
		byUser: []*domain.PSPClient{{ID: 1, UserID: 7, PanelID: 10, Email: "u7@psp.local", UUID: "uuid-7"}},
		attachments: []domain.PSPClientInbound{
			{ClientID: 1, NodeID: 11},
			{ClientID: 1, NodeID: 12},
		},
	}
	nodes := fakeNodes{byID: map[int64]*domain.Node{
		11: {ID: 11, PanelID: 10, InboundID: 101},
		12: {ID: 12, PanelID: 10, InboundID: 102},
	}}
	own := &fakeOwnership{entries: []*domain.XUIClientEntry{
		{ID: 501, PanelID: 10, InboundID: 101, ClientEmail: "u7-n11@psp.local"},
		{ID: 502, PanelID: 10, InboundID: 102, ClientEmail: "u7-n12@psp.local"},
	}}
	xui := &fakeXUI{confirm: []int{101, 102}} // 3X-UI confirms both attaches
	svc := New(clients, fakePool{c: xui}, nodes)
	svc.SetOwnershipRepo(own)

	res, err := svc.MigrateUser(context.Background(), 7)
	if err != nil {
		t.Fatal(err)
	}
	if res.Provisioned != 2 || res.Deleted != 2 {
		t.Fatalf("result = %+v, want 2 provisioned + 2 deleted", res)
	}
	if len(xui.addedInbounds) != 2 {
		t.Fatalf("shared client should attach to both inbounds: %v", xui.addedInbounds)
	}
	if len(xui.deleted) != 2 {
		t.Fatalf("both legacy per-node clients should be deleted: %+v", xui.deleted)
	}
	if len(own.removedID) != 2 {
		t.Fatalf("both ownership rows should be removed: %v", own.removedID)
	}
}

// Failure-safe: if provisioning the shared client fails, the legacy per-node
// clients must be LEFT INTACT (the user keeps working; the task retries).
func TestMigrateUser_ProvisionFailureKeepsLegacy(t *testing.T) {
	clients := &fakeClients{
		byUser:      []*domain.PSPClient{{ID: 1, UserID: 7, PanelID: 10, Email: "u7@psp.local"}},
		attachments: []domain.PSPClientInbound{{ClientID: 1, NodeID: 11}},
	}
	nodes := fakeNodes{byID: map[int64]*domain.Node{11: {ID: 11, PanelID: 10, InboundID: 101}}}
	own := &fakeOwnership{entries: []*domain.XUIClientEntry{
		{ID: 501, PanelID: 10, InboundID: 101, ClientEmail: "u7-n11@psp.local"},
	}}
	xui := &fakeXUI{failAdd: true} // provisioning fails
	svc := New(clients, fakePool{c: xui}, nodes)
	svc.SetOwnershipRepo(own)

	if _, err := svc.MigrateUser(context.Background(), 7); err == nil {
		t.Fatal("MigrateUser must return an error when provisioning fails")
	}
	if len(xui.deleted) != 0 || len(own.removedID) != 0 {
		t.Fatal("a failed provision must NOT delete any legacy per-node client")
	}
}

func TestProvisionClient_SkipsUnresolvableNode(t *testing.T) {
	clients := &fakeClients{attachments: []domain.PSPClientInbound{
		{ClientID: 1, NodeID: 11},
		{ClientID: 1, NodeID: 99}, // not in nodes repo
	}}
	nodes := fakeNodes{byID: map[int64]*domain.Node{11: {ID: 11, PanelID: 10, InboundID: 101}}}
	xui := &fakeXUI{confirm: []int{101}}
	svc := New(clients, fakePool{c: xui}, nodes)

	res, err := svc.ProvisionClient(context.Background(), &domain.PSPClient{ID: 1, PanelID: 10, Email: "u1@psp.local"})
	if err != nil {
		t.Fatal(err)
	}
	if res.Skipped != 1 || res.Provisioned != 1 {
		t.Fatalf("result = %+v, want 1 skipped + 1 provisioned", res)
	}
	if len(xui.addedInbounds) != 1 || xui.addedInbounds[0] != 101 {
		t.Fatalf("only the resolvable inbound should be added: %v", xui.addedInbounds)
	}
}
