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
	confirm       []int // inboundIDs GetClient reports the client attached to AFTER an add/attach
	preExist      []int // if non-nil, GetClient reports this BEFORE any add (pre-existing client); nil = absent
	added         bool
	attachedTo    []int             // inboundIDs passed to AttachClient (the existing-client path)
	liveClients   map[string][]int  // orphan-reconcile tests: panel-wide email -> inbounds
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
	c.added = true
	c.addedInbounds = append([]int(nil), inboundIDs...)
	c.addedSpec = spec
	return nil
}

// GetClient models 3X-UI: a client does not exist until added (returns nil), then
// reports `confirm`. preExist simulates a client already present before provision.
// When liveClients is set (orphan-reconcile tests), it is the source of truth.
func (c *fakeXUI) GetClient(_ context.Context, email string) (*ports.ClientDetail, error) {
	if c.liveClients != nil {
		if inbs, ok := c.liveClients[email]; ok {
			return &ports.ClientDetail{Email: email, InboundIDs: inbs}, nil
		}
		return nil, nil
	}
	if !c.added {
		if c.preExist == nil {
			return nil, nil
		}
		return &ports.ClientDetail{InboundIDs: c.preExist}, nil
	}
	return &ports.ClientDetail{InboundIDs: c.confirm}, nil
}

// ListClientInbounds returns the panel-wide live client→inbounds map (orphan tests).
func (c *fakeXUI) ListClientInbounds(context.Context) (map[string][]int, error) {
	return c.liveClients, nil
}
// AttachClient is the existing-client path: idempotent attach of the desired
// inbounds. Sets `added` so the read-back GetClient reports `confirm`.
func (c *fakeXUI) AttachClient(_ context.Context, _ string, inboundIDs []int) error {
	c.added = true
	c.attachedTo = append([]int(nil), inboundIDs...)
	return nil
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

// BulkDelByEmail is the panel-wide batch delete the legacy cleanup now uses (one
// call per panel). Record each email so len(deleted) still reflects client count.
func (c *fakeXUI) BulkDelByEmail(_ context.Context, emails []string) (int, error) {
	for _, e := range emails {
		c.deleted = append(c.deleted, deletedClient{email: e})
	}
	return len(emails), nil
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

// No-op-skip: when the live client is ALREADY attached to exactly the desired
// inbound set, ProvisionClient must NOT call AddClientToInbounds (which restarts
// Xray) — it just re-marks the attachments provisioned. This restores the legacy
// per-node clientUnchanged behaviour so a steady-state resync costs 0 restarts.
func TestProvisionClient_SkipsRedundantReattach(t *testing.T) {
	clients := &fakeClients{attachments: []domain.PSPClientInbound{
		{ClientID: 1, NodeID: 11, FlowOverride: "xtls-rprx-vision"},
		{ClientID: 1, NodeID: 12, FlowOverride: "xtls-rprx-vision"},
	}}
	nodes := fakeNodes{byID: map[int64]*domain.Node{
		11: {ID: 11, PanelID: 10, InboundID: 101},
		12: {ID: 12, PanelID: 10, InboundID: 102},
	}}
	// Client already present on EXACTLY the desired inbounds (101,102) before provision.
	xui := &fakeXUI{preExist: []int{101, 102}}
	svc := New(clients, fakePool{c: xui}, nodes)

	c := &domain.PSPClient{ID: 1, PanelID: 10, Email: "u1@psp.local", UUID: "uuid-x", Password: "pw-x"}
	res, err := svc.ProvisionClient(context.Background(), c)
	if err != nil {
		t.Fatal(err)
	}
	if len(xui.addedInbounds) != 0 || res.Created {
		t.Fatalf("must skip the re-add when already attached: added=%v created=%v", xui.addedInbounds, res.Created)
	}
	if res.Provisioned != 2 || !clients.provisioned[11] || !clients.provisioned[12] {
		t.Fatalf("both nodes must still be marked provisioned: res=%+v marks=%v", res, clients.provisioned)
	}
}

// ReconcileOrphans deletes a user's stale shared clients (pre-merge per-class
// clients the merge re-keyed) but NEVER the legacy per-node fallback, an operator
// client, or another user's client — and only when the desired client covers the
// orphan's inbounds.
func TestReconcileOrphans_DeletesStaleSharedOnly(t *testing.T) {
	clients := &fakeClients{byUser: []*domain.PSPClient{
		{ID: 1, UserID: 18, PanelID: 10, Email: "u18@psp.local", UUID: "uuid-18"},
	}}
	xui := &fakeXUI{liveClients: map[string][]int{
		"u18@psp.local":           {1, 2}, // desired merged — covers both inbounds
		"u18-kf2d62608@psp.local": {1},    // stale shared orphan (covered) → delete
		"u18-k49f8cae4@psp.local": {2},    // stale shared orphan (covered) → delete
		"u18-n7@psp.local":        {1},    // legacy per-node fallback → KEEP
		"Kazuha Home 0":           {1},    // operator client → KEEP
		"u1@psp.local":            {1},    // different user → KEEP
	}}
	svc := New(clients, fakePool{c: xui}, fakeNodes{})
	if err := svc.ReconcileOrphans(context.Background(), 18); err != nil {
		t.Fatal(err)
	}
	deleted := map[string]bool{}
	for _, d := range xui.deleted {
		deleted[d.email] = true
	}
	for _, want := range []string{"u18-kf2d62608@psp.local", "u18-k49f8cae4@psp.local"} {
		if !deleted[want] {
			t.Errorf("stale shared orphan %q must be deleted (deleted=%v)", want, xui.deleted)
		}
	}
	for _, keep := range []string{"u18@psp.local", "u18-n7@psp.local", "Kazuha Home 0", "u1@psp.local"} {
		if deleted[keep] {
			t.Errorf("%q must NOT be deleted", keep)
		}
	}
}

// Coverage gate (availability): if the live desired client does NOT cover an
// inbound the orphan serves, the orphan is kept — deleting it would drop that
// inbound's only client.
func TestReconcileOrphans_KeepsOrphanWhenReplacementUncovered(t *testing.T) {
	clients := &fakeClients{byUser: []*domain.PSPClient{
		{ID: 1, UserID: 18, PanelID: 10, Email: "u18@psp.local", UUID: "uuid-18"},
	}}
	xui := &fakeXUI{liveClients: map[string][]int{
		"u18@psp.local":           {1}, // merged attached only to inbound 1 (partial)
		"u18-k49f8cae4@psp.local": {2}, // orphan serves inbound 2 — NOT covered → KEEP
	}}
	svc := New(clients, fakePool{c: xui}, fakeNodes{})
	if err := svc.ReconcileOrphans(context.Background(), 18); err != nil {
		t.Fatal(err)
	}
	for _, d := range xui.deleted {
		if d.email == "u18-k49f8cae4@psp.local" {
			t.Fatal("must not delete an orphan whose inbound the replacement doesn't cover")
		}
	}
}

// Gate: if a desired client isn't live at all (provisioning failed), the panel is
// skipped entirely — nothing is deleted.
func TestReconcileOrphans_SkipsPanelWhenDesiredAbsent(t *testing.T) {
	clients := &fakeClients{byUser: []*domain.PSPClient{
		{ID: 1, UserID: 18, PanelID: 10, Email: "u18@psp.local", UUID: "uuid-18"},
	}}
	xui := &fakeXUI{liveClients: map[string][]int{
		// u18@ absent → replacement not up; the per-class orphan must survive.
		"u18-kf2d62608@psp.local": {1},
	}}
	svc := New(clients, fakePool{c: xui}, fakeNodes{})
	if err := svc.ReconcileOrphans(context.Background(), 18); err != nil {
		t.Fatal(err)
	}
	if len(xui.deleted) != 0 {
		t.Fatalf("desired client absent → no deletes, got %v", xui.deleted)
	}
}

// The v3.9.0 merge steady state: the merged email REUSES an existing client's
// email (e.g. the VLESS-vision per-class email when the panel has no SS-2022) and
// now needs MORE inbounds than that client currently has. ProvisionClient must use
// the idempotent AttachClient — NOT AddClientToInbounds, which 3X-UI rejects with
// "email already in use" on the inbound the client is already on (verified live on
// 3.4.0; this was the China Shanghai heal failure).
func TestProvisionClient_AttachesWhenEmailAlreadyExists(t *testing.T) {
	clients := &fakeClients{attachments: []domain.PSPClientInbound{
		{ClientID: 1, NodeID: 11, FlowOverride: "xtls-rprx-vision"},
		{ClientID: 1, NodeID: 12, FlowOverride: "xtls-rprx-vision"},
	}}
	nodes := fakeNodes{byID: map[int64]*domain.Node{
		11: {ID: 11, PanelID: 10, InboundID: 101},
		12: {ID: 12, PanelID: 10, InboundID: 102},
	}}
	// Client already exists on 101 (a per-class client being merged); desired is
	// 101+102. 3X-UI confirms both after the idempotent attach.
	xui := &fakeXUI{preExist: []int{101}, confirm: []int{101, 102}}
	svc := New(clients, fakePool{c: xui}, nodes)

	res, err := svc.ProvisionClient(context.Background(),
		&domain.PSPClient{ID: 1, PanelID: 10, Email: "u1-kf2d62608@psp.local", UUID: "uuid-x"})
	if err != nil {
		t.Fatal(err)
	}
	if len(xui.addedInbounds) != 0 {
		t.Fatalf("must NOT call AddClientToInbounds on an existing email (got %v)", xui.addedInbounds)
	}
	if len(xui.attachedTo) != 2 || xui.attachedTo[0] != 101 || xui.attachedTo[1] != 102 {
		t.Fatalf("must AttachClient to the full desired set [101 102], got %v", xui.attachedTo)
	}
	if !res.Created || res.Provisioned != 2 {
		t.Fatalf("result = %+v, want created + 2 provisioned", res)
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
