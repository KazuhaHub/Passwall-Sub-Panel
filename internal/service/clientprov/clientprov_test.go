package clientprov

import (
	"context"
	"testing"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/pkg/clientplan"
)

// fakePSPClientRepo is a minimal in-memory ports.PSPClientRepo for the
// provisioner tests. It is keyed only by the database-minted stable ID; email
// is deliberately just a mutable field.
type fakePSPClientRepo struct {
	nextID   int64
	clients  map[int64]*domain.PSPClient
	inbounds map[int64][]domain.PSPClientInbound // clientID -> attachments
}

func newFakeRepo() *fakePSPClientRepo {
	return &fakePSPClientRepo{clients: map[int64]*domain.PSPClient{}, inbounds: map[int64][]domain.PSPClientInbound{}}
}

func (r *fakePSPClientRepo) Create(ctx context.Context, c *domain.PSPClient) (int64, error) {
	r.nextID++
	cp := *c
	cp.ID = r.nextID
	r.clients[cp.ID] = &cp
	return cp.ID, nil
}
func (r *fakePSPClientRepo) UpdateDefinition(ctx context.Context, c *domain.PSPClient) error {
	ex, ok := r.clients[c.ID]
	if !ok || ex.UserID != c.UserID || ex.PanelID != c.PanelID {
		return domain.ErrNotFound
	}
	ex.Email, ex.CredClass, ex.UUID, ex.Password = c.Email, c.CredClass, c.UUID, c.Password
	return nil
}
func (r *fakePSPClientRepo) UpdateDesiredLifecycleByUser(_ context.Context, userID int64, lifecycle domain.UserLifecycle) error {
	for _, c := range r.clients {
		if c.UserID == userID {
			c.SetDesiredLifecycle(lifecycle)
		}
	}
	return nil
}
func (r *fakePSPClientRepo) GetByID(ctx context.Context, id int64) (*domain.PSPClient, error) {
	if c, ok := r.clients[id]; ok {
		cp := *c
		return &cp, nil
	}
	return nil, domain.ErrNotFound
}
func (r *fakePSPClientRepo) GetByEmail(ctx context.Context, panelID int64, email string) (*domain.PSPClient, error) {
	for _, c := range r.clients {
		if c.PanelID == panelID && c.Email == email {
			cp := *c
			return &cp, nil
		}
	}
	return nil, domain.ErrNotFound
}
func (r *fakePSPClientRepo) ListAll(ctx context.Context) ([]*domain.PSPClient, error) {
	var out []*domain.PSPClient
	for _, c := range r.clients {
		cp := *c
		out = append(out, &cp)
	}
	return out, nil
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
func (r *fakePSPClientRepo) DeleteByID(ctx context.Context, id int64) error {
	delete(r.inbounds, id)
	delete(r.clients, id)
	return nil
}
func (r *fakePSPClientRepo) SetInbounds(ctx context.Context, clientID int64, inbounds []domain.PSPClientInbound) error {
	r.inbounds[clientID] = append([]domain.PSPClientInbound(nil), inbounds...)
	return nil
}
func (r *fakePSPClientRepo) ListInbounds(ctx context.Context, clientID int64) ([]domain.PSPClientInbound, error) {
	return r.inbounds[clientID], nil
}
func (r *fakePSPClientRepo) UpdateInboundState(ctx context.Context, inbound domain.PSPClientInbound) error {
	for i := range r.inbounds[inbound.ClientID] {
		if r.inbounds[inbound.ClientID][i].NodeID == inbound.NodeID {
			r.inbounds[inbound.ClientID][i].State = inbound.State
			r.inbounds[inbound.ClientID][i].AppliedVersion = inbound.AppliedVersion
			r.inbounds[inbound.ClientID][i].FirstFailedAt = inbound.FirstFailedAt
		}
	}
	return nil
}
func (r *fakePSPClientRepo) UpdateCounters(ctx context.Context, c *domain.PSPClient) error {
	if stored, ok := r.clients[c.ID]; ok {
		stored.LifetimeUpBytes = c.LifetimeUpBytes
		stored.LifetimeDownBytes = c.LifetimeDownBytes
		stored.LifetimeTotalBytes = c.LifetimeTotalBytes
		stored.LastRawUpBytes = c.LastRawUpBytes
		stored.LastRawDownBytes = c.LastRawDownBytes
		stored.LastRawTotalBytes = c.LastRawTotalBytes
		stored.PeriodBaselineUpBytes = c.PeriodBaselineUpBytes
		stored.PeriodBaselineDownBytes = c.PeriodBaselineDownBytes
		stored.PeriodBaselineTotalBytes = c.PeriodBaselineTotalBytes
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
	// Both default-class (no flow), so they share ONE client with two
	// attachments. (Flow-based splitting is covered in clientplan's tests.)
	nodes := []clientplan.NodeCred{
		{NodeID: 1, Protocol: domain.ProtoVLESS},
		{NodeID: 2, Protocol: domain.ProtoTrojan},
	}
	if _, err := svc.Sync(context.Background(), 42, "uuid-x", 10, rules, nodes); err != nil {
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
	_, _ = svc.Sync(ctx, 42, "uuid-x", 10, rules, []clientplan.NodeCred{{NodeID: 1, Protocol: domain.ProtoVLESS}})
	if _, err := repo.GetByEmail(ctx, 10, "u42@psp.local"); err != nil {
		t.Fatalf("precondition: client should exist: %v", err)
	}
	// Access revoked (no nodes) → the panel's client is pruned.
	if _, err := svc.Sync(ctx, 42, "uuid-x", 10, rules, nil); err != nil {
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
	_, _ = svc.Sync(ctx, 42, "uuid-x", 10, rules, []clientplan.NodeCred{{NodeID: 1, Protocol: domain.ProtoVLESS}})
	_, _ = svc.Sync(ctx, 42, "uuid-x", 11, rules, []clientplan.NodeCred{{NodeID: 9, Protocol: domain.ProtoVLESS}})
	// Re-syncing panel 10 must not disturb the panel-11 client.
	if _, err := svc.Sync(ctx, 42, "uuid-x", 10, rules, []clientplan.NodeCred{{NodeID: 1, Protocol: domain.ProtoVLESS}}); err != nil {
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
		{ID: 1, PanelID: 10, DesiredProtocol: "vless"},
		{ID: 2, PanelID: 10, DesiredProtocol: "trojan"},
		{ID: 3, PanelID: 11, DesiredProtocol: "vless"},
	}
	if _, err := svc.SyncUser(ctx, 42, "uuid-x", rules, nodes); err != nil {
		t.Fatal(err)
	}
	if list, _ := repo.ListByUser(ctx, 42); len(list) != 2 {
		t.Fatalf("want 2 clients (one per server), got %d", len(list))
	}

	// User loses all access to panel 11 → its client must be pruned even though
	// no node references panel 11 anymore.
	if _, err := svc.SyncUser(ctx, 42, "uuid-x", rules, []*domain.Node{
		{ID: 1, PanelID: 10, DesiredProtocol: "vless"},
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
		{ID: 1, PanelID: 10, DesiredProtocol: "vless"},
		{ID: 2, PanelID: 10, Kind: domain.NodeKindSeparator, DesiredProtocol: "vless"},
	}
	if _, err := svc.SyncUser(ctx, 42, "uuid-x", rules, nodes); err != nil {
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
	_, _ = svc.Sync(ctx, 42, "uuid-x", 10, rules, []clientplan.NodeCred{{NodeID: 1, Protocol: domain.ProtoVLESS}})
	c, _ := repo.GetByEmail(ctx, 10, "u42@psp.local")
	// Simulate the poll advancing the counter.
	_ = repo.UpdateCounters(ctx, &domain.PSPClient{ID: c.ID, LifetimeTotalBytes: 5_000})
	// A re-sync (e.g. group membership change) must NOT reset usage.
	if _, err := svc.Sync(ctx, 42, "uuid-x", 10, rules, []clientplan.NodeCred{
		{NodeID: 1, Protocol: domain.ProtoVLESS}, {NodeID: 2, Protocol: domain.ProtoTrojan},
	}); err != nil {
		t.Fatal(err)
	}
	got, _ := repo.GetByEmail(ctx, 10, "u42@psp.local")
	if got.LifetimeTotalBytes != 5_000 {
		t.Fatalf("re-sync clobbered usage: got %d, want preserved 5000", got.LifetimeTotalBytes)
	}
}

func TestSync_PreservesStableRowAndBaselinesAcrossPartitionBoundary(t *testing.T) {
	repo := newFakeRepo()
	svc := New(repo)
	ctx := context.Background()
	ss2022 := clientplan.NodeCred{NodeID: 1, Protocol: domain.ProtoSS2022, SSMethod: "2022-blake3-aes-256-gcm"}
	plainSS := clientplan.NodeCred{NodeID: 2, Protocol: domain.ProtoSS, SSMethod: "aes-256-gcm"}

	if _, err := svc.Sync(ctx, 42, "uuid-x", 10, rules, []clientplan.NodeCred{ss2022}); err != nil {
		t.Fatal(err)
	}
	before, err := repo.GetByEmail(ctx, 10, "u42@psp.local")
	if err != nil {
		t.Fatal(err)
	}
	wantID := before.ID
	wantCounters := &domain.PSPClient{
		ID:              wantID,
		LifetimeUpBytes: 900, LifetimeDownBytes: 1100, LifetimeTotalBytes: 2000,
		LastRawUpBytes: 90, LastRawDownBytes: 110, LastRawTotalBytes: 200,
		PeriodBaselineUpBytes: 400, PeriodBaselineDownBytes: 500, PeriodBaselineTotalBytes: 900,
	}
	if err := repo.UpdateCounters(ctx, wantCounters); err != nil {
		t.Fatal(err)
	}

	// 1→2: the SS-2022 row's email gains a partition suffix, but the stable
	// ID and every counter baseline stay on the row that still serves node 1.
	if _, err := svc.Sync(ctx, 42, "uuid-x", 10, rules, []clientplan.NodeCred{ss2022, plainSS}); err != nil {
		t.Fatal(err)
	}
	afterSplit, err := repo.GetByID(ctx, wantID)
	if err != nil {
		t.Fatal(err)
	}
	if afterSplit.Email == "u42@psp.local" {
		t.Fatalf("test did not cross 1→2 email boundary: %+v", afterSplit)
	}
	assertClientBaselines(t, afterSplit, wantCounters)
	inbounds, _ := repo.ListInbounds(ctx, wantID)
	if len(inbounds) != 1 || inbounds[0].NodeID != 1 {
		t.Fatalf("stable row followed the wrong partition: %+v", inbounds)
	}

	// 2→1: removing the conflicting node returns the same row to the bare
	// email; it must not delete/recreate it on the reverse boundary either.
	if _, err := svc.Sync(ctx, 42, "uuid-x", 10, rules, []clientplan.NodeCred{ss2022}); err != nil {
		t.Fatal(err)
	}
	afterMerge, err := repo.GetByEmail(ctx, 10, "u42@psp.local")
	if err != nil {
		t.Fatal(err)
	}
	if afterMerge.ID != wantID {
		t.Fatalf("2→1 replaced client row %d with %d", wantID, afterMerge.ID)
	}
	assertClientBaselines(t, afterMerge, wantCounters)
}

func TestSync_PreservesStableRowAndBaselinesAcrossDomainChange(t *testing.T) {
	repo := newFakeRepo()
	svc := New(repo)
	ctx := context.Background()
	nodes := []clientplan.NodeCred{
		{NodeID: 1, Protocol: domain.ProtoSS2022, SSMethod: "2022-blake3-aes-256-gcm"},
		{NodeID: 2, Protocol: domain.ProtoSS, SSMethod: "aes-256-gcm"},
	}
	if _, err := svc.Sync(ctx, 42, "uuid-x", 10, rules, nodes); err != nil {
		t.Fatal(err)
	}
	before, _ := repo.ListByUser(ctx, 42)
	if len(before) != 2 {
		t.Fatalf("precondition: want two clients, got %+v", before)
	}
	wantByID := make(map[int64]*domain.PSPClient, len(before))
	for i, c := range before {
		n := int64(i + 1)
		want := &domain.PSPClient{
			ID:              c.ID,
			LifetimeUpBytes: 10 * n, LifetimeDownBytes: 20 * n, LifetimeTotalBytes: 30 * n,
			LastRawUpBytes: 4 * n, LastRawDownBytes: 5 * n, LastRawTotalBytes: 9 * n,
			PeriodBaselineUpBytes: n, PeriodBaselineDownBytes: 2 * n, PeriodBaselineTotalBytes: 3 * n,
		}
		if err := repo.UpdateCounters(ctx, want); err != nil {
			t.Fatal(err)
		}
		wantByID[c.ID] = want
	}

	if _, err := svc.Sync(ctx, 42, "uuid-x", 10, domain.EmailRules{Domain: "new.example"}, nodes); err != nil {
		t.Fatal(err)
	}
	after, _ := repo.ListByUser(ctx, 42)
	if len(after) != 2 {
		t.Fatalf("domain change replaced the two-row plan: %+v", after)
	}
	for _, c := range after {
		want, ok := wantByID[c.ID]
		if !ok {
			t.Fatalf("domain change minted an unexpected client ID %d", c.ID)
		}
		if c.Email[len(c.Email)-len("new.example"):] != "new.example" {
			t.Fatalf("client %d email did not change domains: %q", c.ID, c.Email)
		}
		assertClientBaselines(t, c, want)
	}
}

func assertClientBaselines(t *testing.T, got, want *domain.PSPClient) {
	t.Helper()
	if got.LifetimeUpBytes != want.LifetimeUpBytes || got.LifetimeDownBytes != want.LifetimeDownBytes || got.LifetimeTotalBytes != want.LifetimeTotalBytes ||
		got.LastRawUpBytes != want.LastRawUpBytes || got.LastRawDownBytes != want.LastRawDownBytes || got.LastRawTotalBytes != want.LastRawTotalBytes ||
		got.PeriodBaselineUpBytes != want.PeriodBaselineUpBytes || got.PeriodBaselineDownBytes != want.PeriodBaselineDownBytes || got.PeriodBaselineTotalBytes != want.PeriodBaselineTotalBytes {
		t.Fatalf("counter baselines changed:\n got  %+v\n want %+v", got, want)
	}
}
