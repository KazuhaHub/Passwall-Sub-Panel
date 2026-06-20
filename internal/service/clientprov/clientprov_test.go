package clientprov

import (
	"context"
	"testing"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/pkg/clientplan"
)

// fakePSPClientRepo is a minimal in-memory ports.PSPClientRepo for the
// provisioner tests. Keyed by (panelID, email) like the real unique index.
type fakePSPClientRepo struct {
	nextID   int64
	clients  map[string]*domain.PSPClient            // key: panel|email
	inbounds map[int64][]domain.PSPClientInbound      // clientID -> attachments
}

func newFakeRepo() *fakePSPClientRepo {
	return &fakePSPClientRepo{clients: map[string]*domain.PSPClient{}, inbounds: map[int64][]domain.PSPClientInbound{}}
}

func key(panelID int64, email string) string { return email + "@@" + itoa(panelID) }
func itoa(n int64) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	if neg {
		b = append([]byte{'-'}, b...)
	}
	return string(b)
}

func (r *fakePSPClientRepo) Upsert(ctx context.Context, c *domain.PSPClient) (int64, error) {
	k := key(c.PanelID, c.Email)
	if ex, ok := r.clients[k]; ok {
		// identity/credential update only — preserve counters (real-repo contract)
		ex.UserID, ex.CredClass, ex.UUID, ex.Password = c.UserID, c.CredClass, c.UUID, c.Password
		return ex.ID, nil
	}
	r.nextID++
	cp := *c
	cp.ID = r.nextID
	r.clients[k] = &cp
	return cp.ID, nil
}
func (r *fakePSPClientRepo) GetByID(ctx context.Context, id int64) (*domain.PSPClient, error) {
	for _, c := range r.clients {
		if c.ID == id {
			cp := *c
			return &cp, nil
		}
	}
	return nil, domain.ErrNotFound
}
func (r *fakePSPClientRepo) GetByEmail(ctx context.Context, panelID int64, email string) (*domain.PSPClient, error) {
	if c, ok := r.clients[key(panelID, email)]; ok {
		cp := *c
		return &cp, nil
	}
	return nil, domain.ErrNotFound
}
func (r *fakePSPClientRepo) ListByUser(ctx context.Context, userID int64) ([]*domain.PSPClient, error) {
	var out []*domain.PSPClient
	for _, c := range r.clients {
		if c.UserID == userID {
			cp := *c
			out = append(out, &cp)
		}
	}
	return out, nil
}
func (r *fakePSPClientRepo) DeleteByEmail(ctx context.Context, panelID int64, email string) error {
	k := key(panelID, email)
	if c, ok := r.clients[k]; ok {
		delete(r.inbounds, c.ID)
		delete(r.clients, k)
	}
	return nil
}
func (r *fakePSPClientRepo) SetInbounds(ctx context.Context, clientID int64, inbounds []domain.PSPClientInbound) error {
	r.inbounds[clientID] = append([]domain.PSPClientInbound(nil), inbounds...)
	return nil
}
func (r *fakePSPClientRepo) ListInbounds(ctx context.Context, clientID int64) ([]domain.PSPClientInbound, error) {
	return r.inbounds[clientID], nil
}
func (r *fakePSPClientRepo) UpdateCounters(ctx context.Context, c *domain.PSPClient) error {
	if ex, _ := r.GetByID(ctx, c.ID); ex != nil {
		for _, stored := range r.clients {
			if stored.ID == c.ID {
				stored.LifetimeTotalBytes = c.LifetimeTotalBytes
			}
		}
	}
	return nil
}
func (r *fakePSPClientRepo) BatchUpdateCounters(ctx context.Context, items []*domain.PSPClient) error {
	for _, c := range items {
		_ = r.UpdateCounters(ctx, c)
	}
	return nil
}

var rules = domain.EmailRules{Domain: "psp.local"}

func TestSync_CreatesSharedClientAndAttachments(t *testing.T) {
	repo := newFakeRepo()
	svc := New(repo)
	nodes := []clientplan.NodeCred{
		{NodeID: 1, Protocol: domain.ProtoVLESS, Flow: "xtls-rprx-vision"},
		{NodeID: 2, Protocol: domain.ProtoTrojan},
	}
	if err := svc.Sync(context.Background(), 42, "uuid-x", 10, rules, nodes); err != nil {
		t.Fatal(err)
	}
	c, err := repo.GetByEmail(context.Background(), 10, "u42@psp.local")
	if err != nil {
		t.Fatalf("shared client not created: %v", err)
	}
	inbs, _ := repo.ListInbounds(context.Background(), c.ID)
	if len(inbs) != 2 {
		t.Fatalf("attachments = %d, want 2", len(inbs))
	}
}

func TestSync_PrunesClientWhenAccessRevoked(t *testing.T) {
	repo := newFakeRepo()
	svc := New(repo)
	ctx := context.Background()
	// Initially the user has access to a node on panel 10.
	_ = svc.Sync(ctx, 42, "uuid-x", 10, rules, []clientplan.NodeCred{{NodeID: 1, Protocol: domain.ProtoVLESS}})
	if _, err := repo.GetByEmail(ctx, 10, "u42@psp.local"); err != nil {
		t.Fatalf("precondition: client should exist: %v", err)
	}
	// Access revoked (no nodes) → the panel's client is pruned.
	if err := svc.Sync(ctx, 42, "uuid-x", 10, rules, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.GetByEmail(ctx, 10, "u42@psp.local"); err == nil {
		t.Fatal("client should have been pruned after access revoked")
	}
}

func TestSync_DoesNotTouchOtherPanels(t *testing.T) {
	repo := newFakeRepo()
	svc := New(repo)
	ctx := context.Background()
	_ = svc.Sync(ctx, 42, "uuid-x", 10, rules, []clientplan.NodeCred{{NodeID: 1, Protocol: domain.ProtoVLESS}})
	_ = svc.Sync(ctx, 42, "uuid-x", 11, rules, []clientplan.NodeCred{{NodeID: 9, Protocol: domain.ProtoVLESS}})
	// Re-syncing panel 10 must not disturb the panel-11 client.
	if err := svc.Sync(ctx, 42, "uuid-x", 10, rules, []clientplan.NodeCred{{NodeID: 1, Protocol: domain.ProtoVLESS}}); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.GetByEmail(ctx, 11, "u42@psp.local"); err != nil {
		t.Fatalf("panel-11 client must survive a panel-10 sync: %v", err)
	}
	list, _ := repo.ListByUser(ctx, 42)
	if len(list) != 2 {
		t.Fatalf("want 2 clients (one per panel), got %d", len(list))
	}
}

func TestSyncUser_AcrossPanelsAndPrunesLostServer(t *testing.T) {
	repo := newFakeRepo()
	svc := New(repo)
	ctx := context.Background()

	// User reachable on two servers (panels 10 and 11).
	nodes := []*domain.Node{
		{ID: 1, PanelID: 10, Protocol: "vless"},
		{ID: 2, PanelID: 10, Protocol: "trojan"},
		{ID: 3, PanelID: 11, Protocol: "vless"},
	}
	if err := svc.SyncUser(ctx, 42, "uuid-x", rules, nodes); err != nil {
		t.Fatal(err)
	}
	if list, _ := repo.ListByUser(ctx, 42); len(list) != 2 {
		t.Fatalf("want 2 clients (one per server), got %d", len(list))
	}

	// User loses all access to panel 11 → its client must be pruned even though
	// no node references panel 11 anymore.
	if err := svc.SyncUser(ctx, 42, "uuid-x", rules, []*domain.Node{
		{ID: 1, PanelID: 10, Protocol: "vless"},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.GetByEmail(ctx, 11, "u42@psp.local"); err == nil {
		t.Fatal("panel-11 client should have been pruned after losing access")
	}
	if _, err := repo.GetByEmail(ctx, 10, "u42@psp.local"); err != nil {
		t.Fatalf("panel-10 client should remain: %v", err)
	}
}

func TestSyncUser_SkipsSeparators(t *testing.T) {
	repo := newFakeRepo()
	svc := New(repo)
	ctx := context.Background()
	nodes := []*domain.Node{
		{ID: 1, PanelID: 10, Protocol: "vless"},
		{ID: 2, PanelID: 10, Kind: domain.NodeKindSeparator, Protocol: "vless"},
	}
	if err := svc.SyncUser(ctx, 42, "uuid-x", rules, nodes); err != nil {
		t.Fatal(err)
	}
	c, err := repo.GetByEmail(ctx, 10, "u42@psp.local")
	if err != nil {
		t.Fatal(err)
	}
	inbs, _ := repo.ListInbounds(ctx, c.ID)
	if len(inbs) != 1 || inbs[0].NodeID != 1 {
		t.Fatalf("separator must be excluded from attachments: %+v", inbs)
	}
}

func TestSync_PreservesCountersAcrossResync(t *testing.T) {
	repo := newFakeRepo()
	svc := New(repo)
	ctx := context.Background()
	_ = svc.Sync(ctx, 42, "uuid-x", 10, rules, []clientplan.NodeCred{{NodeID: 1, Protocol: domain.ProtoVLESS}})
	c, _ := repo.GetByEmail(ctx, 10, "u42@psp.local")
	// Simulate the poll advancing the counter.
	_ = repo.UpdateCounters(ctx, &domain.PSPClient{ID: c.ID, LifetimeTotalBytes: 5_000})
	// A re-sync (e.g. group membership change) must NOT reset usage.
	if err := svc.Sync(ctx, 42, "uuid-x", 10, rules, []clientplan.NodeCred{
		{NodeID: 1, Protocol: domain.ProtoVLESS}, {NodeID: 2, Protocol: domain.ProtoTrojan},
	}); err != nil {
		t.Fatal(err)
	}
	got, _ := repo.GetByEmail(ctx, 10, "u42@psp.local")
	if got.LifetimeTotalBytes != 5_000 {
		t.Fatalf("re-sync clobbered usage: got %d, want preserved 5000", got.LifetimeTotalBytes)
	}
}
