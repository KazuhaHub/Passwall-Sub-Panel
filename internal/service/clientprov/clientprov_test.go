package clientprov

import (
	"context"
	"errors"
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

// uniqueLine mirrors the store's unique key uk_psp_client_line_panel: one row
// per (user, server, node, line). Legacy rows (no line) never collide.
func (r *fakePSPClientRepo) uniqueLine(c *domain.PSPClient, exceptID int64) error {
	if _, ok := c.Line(); !ok {
		return nil
	}
	for _, other := range r.clients {
		if other.ID != exceptID && other.UserID == c.UserID && other.PanelID == c.PanelID && other.NodeID == c.NodeID && other.LineID == c.LineID {
			return errors.New("UNIQUE constraint failed: psp_clients.user_id, panel_id, node_id, line_id")
		}
	}
	return nil
}

func (r *fakePSPClientRepo) Create(ctx context.Context, c *domain.PSPClient) (int64, error) {
	if err := r.uniqueLine(c, 0); err != nil {
		return 0, err
	}
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
	if err := r.uniqueLine(c, c.ID); err != nil {
		return err
	}
	ex.Email, ex.CredClass, ex.UUID, ex.Password = c.Email, c.CredClass, c.UUID, c.Password
	if c.NodeID != 0 {
		ex.NodeID, ex.LineID = c.NodeID, c.LineID
	}
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
		stored.LastCounterEpoch = c.LastCounterEpoch
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

func lineRow(t *testing.T, repo *fakePSPClientRepo, line domain.LineKey) *domain.PSPClient {
	t.Helper()
	for _, c := range repo.clients {
		if c.NodeID == line.NodeID && c.LineID == line.LineID {
			return c
		}
	}
	t.Fatalf("no row for line %+v", line)
	return nil
}

func attached(repo *fakePSPClientRepo, c *domain.PSPClient) []int64 {
	var out []int64
	for _, in := range repo.inbounds[c.ID] {
		out = append(out, in.NodeID)
	}
	return out
}

// One row per line, each attached only to its line's listener.
func TestSyncUser_CreatesOneRowPerLine(t *testing.T) {
	repo := newFakeRepo()
	ctx := context.Background()
	nodes := []*domain.Node{
		{ID: 1, PanelID: 10, DesiredProtocol: "vless", Relays: []domain.RelayLine{{ID: "a", Enabled: true}, {ID: "off"}}},
		{ID: 2, PanelID: 10, DesiredProtocol: "trojan"},
		{ID: 3, PanelID: 10, Kind: domain.NodeKindSeparator, DesiredProtocol: "vless"},
	}
	if _, err := New(repo).SyncUser(ctx, 42, "uuid-x", rules, nodes); err != nil {
		t.Fatal(err)
	}
	if len(repo.clients) != 3 {
		t.Fatalf("rows = %d, want direct 1, relay 1/a, direct 2", len(repo.clients))
	}
	d1 := lineRow(t, repo, domain.DirectLine(1))
	r1 := lineRow(t, repo, domain.LineKey{NodeID: 1, LineID: "a"})
	d2 := lineRow(t, repo, domain.DirectLine(2))
	if d1.UUID != "uuid-x" || d2.UUID != "uuid-x" || r1.UUID == "uuid-x" {
		t.Fatalf("direct lines keep the user UUID, relay lines get their own: %s %s %s", d1.UUID, d2.UUID, r1.UUID)
	}
	for _, c := range []*domain.PSPClient{d1, r1, d2} {
		if got := attached(repo, c); len(got) != 1 || got[0] != c.NodeID {
			t.Fatalf("row %+v attached to %v", c, got)
		}
	}
}

// A line the user loses is detached (counters kept, upstream projection
// retired), and comes back on the same row when the line returns.
func TestSyncUser_DroppedLineIsDetachedAndKeepsItsRow(t *testing.T) {
	repo := newFakeRepo()
	svc := New(repo)
	ctx := context.Background()
	withRelay := []*domain.Node{{ID: 1, PanelID: 10, DesiredProtocol: "vless", Relays: []domain.RelayLine{{ID: "a", Enabled: true}}}}
	if _, err := svc.SyncUser(ctx, 42, "uuid-x", rules, withRelay); err != nil {
		t.Fatal(err)
	}
	relay := lineRow(t, repo, domain.LineKey{NodeID: 1, LineID: "a"})
	want := &domain.PSPClient{ID: relay.ID, LifetimeUpBytes: 900, LifetimeDownBytes: 1100, LifetimeTotalBytes: 2000,
		LastRawUpBytes: 90, LastRawDownBytes: 110, LastRawTotalBytes: 200,
		PeriodBaselineUpBytes: 400, PeriodBaselineDownBytes: 500, PeriodBaselineTotalBytes: 900, LastCounterEpoch: 7}
	if err := repo.UpdateCounters(ctx, want); err != nil {
		t.Fatal(err)
	}
	got, err := svc.SyncUserRetirements(ctx, 42, "uuid-x", rules, []*domain.Node{{ID: 1, PanelID: 10, DesiredProtocol: "vless"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Regrouped[10]) != 1 || got.Regrouped[10][0] != relay.Email {
		t.Fatalf("regrouped = %v, want the relay row's email", got.Regrouped)
	}
	kept, err := repo.GetByID(ctx, relay.ID)
	if err != nil {
		t.Fatalf("dropped line's row deleted before its counters were booked: %v", err)
	}
	assertClientBaselines(t, kept, want)
	if len(attached(repo, kept)) != 0 {
		t.Fatal("dropped line's row still attached")
	}
	if _, err := svc.SyncUser(ctx, 42, "uuid-x", rules, withRelay); err != nil {
		t.Fatal(err)
	}
	if back := lineRow(t, repo, domain.LineKey{NodeID: 1, LineID: "a"}); back.ID != relay.ID || len(attached(repo, back)) != 1 {
		t.Fatalf("returning line did not reuse its row: %+v", back)
	}
}

// Losing a whole server detaches its rows and reports them as removed with
// the panel, not regrouped.
func TestSyncUserRetirements_PanelRemovedVersusRegrouped(t *testing.T) {
	repo := newFakeRepo()
	svc := New(repo)
	ctx := context.Background()
	both := []*domain.Node{
		{ID: 1, PanelID: 10, DesiredProtocol: "vless"},
		{ID: 2, PanelID: 10, DesiredProtocol: "vless"},
		{ID: 3, PanelID: 11, DesiredProtocol: "vless"},
	}
	if _, err := svc.SyncUser(ctx, 42, "uuid-x", rules, both); err != nil {
		t.Fatal(err)
	}
	got, err := svc.SyncUserRetirements(ctx, 42, "uuid-x", rules, []*domain.Node{{ID: 2, PanelID: 10, DesiredProtocol: "vless"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(got.PanelRemoved[11]) != 1 || len(got.PanelRemoved[10]) != 0 {
		t.Fatalf("PanelRemoved = %v, want exactly panel 11's client", got.PanelRemoved)
	}
	if len(got.Regrouped[10]) != 1 || len(got.Regrouped[11]) != 0 {
		t.Fatalf("Regrouped = %v, want node 1's direct row on panel 10", got.Regrouped)
	}
	if len(repo.clients) != 3 {
		t.Fatalf("rows = %d, want all three kept until booked", len(repo.clients))
	}
}

// A re-sync never resets usage.
func TestSync_PreservesCountersAcrossResync(t *testing.T) {
	repo := newFakeRepo()
	svc := New(repo)
	ctx := context.Background()
	one := []clientplan.LineCred{{Line: domain.DirectLine(1), ListenerNodeID: 1, Protocol: domain.ProtoVLESS}}
	_, _ = svc.Sync(ctx, 42, "uuid-x", 10, rules, one)
	c := lineRow(t, repo, domain.DirectLine(1))
	_ = repo.UpdateCounters(ctx, &domain.PSPClient{ID: c.ID, LifetimeTotalBytes: 5_000})
	two := append(one, clientplan.LineCred{Line: domain.DirectLine(2), ListenerNodeID: 2, Protocol: domain.ProtoTrojan})
	if _, err := svc.Sync(ctx, 42, "uuid-x", 10, rules, two); err != nil {
		t.Fatal(err)
	}
	if got := lineRow(t, repo, domain.DirectLine(1)); got.ID != c.ID || got.LifetimeTotalBytes != 5_000 {
		t.Fatalf("re-sync clobbered usage: %+v", got)
	}
}

// Re-syncing one panel never touches another's rows.
func TestSync_DoesNotTouchOtherPanels(t *testing.T) {
	repo := newFakeRepo()
	svc := New(repo)
	ctx := context.Background()
	_, _ = svc.Sync(ctx, 42, "uuid-x", 10, rules, []clientplan.LineCred{{Line: domain.DirectLine(1), Protocol: domain.ProtoVLESS}})
	_, _ = svc.Sync(ctx, 42, "uuid-x", 11, rules, []clientplan.LineCred{{Line: domain.DirectLine(9), Protocol: domain.ProtoVLESS}})
	if _, err := svc.Sync(ctx, 42, "uuid-x", 10, rules, []clientplan.LineCred{{Line: domain.DirectLine(1), Protocol: domain.ProtoVLESS}}); err != nil {
		t.Fatal(err)
	}
	if c := lineRow(t, repo, domain.DirectLine(9)); len(attached(repo, c)) != 1 {
		t.Fatal("panel-11 row disturbed by a panel-10 sync")
	}
}

// A legacy shared row (written before 4.3) is adopted as the direct line of
// the lowest-ID node and keeps its ID, email and counters.
func TestSyncUser_AdoptsLegacyRowAsLowestDirectLine(t *testing.T) {
	repo := newFakeRepo()
	ctx := context.Background()
	legacyID, _ := repo.Create(ctx, &domain.PSPClient{UserID: 42, PanelID: 10, Email: "u42@psp.local", UUID: "uuid-x", LifetimeTotalBytes: 777})
	_ = repo.SetInbounds(ctx, legacyID, []domain.PSPClientInbound{{ClientID: legacyID, NodeID: 5}, {ClientID: legacyID, NodeID: 3}})
	nodes := []*domain.Node{{ID: 5, PanelID: 10, DesiredProtocol: "vless"}, {ID: 3, PanelID: 10, DesiredProtocol: "vless"}}
	if _, err := New(repo).SyncUser(ctx, 42, "uuid-x", rules, nodes); err != nil {
		t.Fatal(err)
	}
	adopted := lineRow(t, repo, domain.DirectLine(3))
	if adopted.ID != legacyID || adopted.Email != "u42@psp.local" || adopted.LifetimeTotalBytes != 777 {
		t.Fatalf("legacy row not adopted by node 3's direct line: %+v", adopted)
	}
	if got := attached(repo, adopted); len(got) != 1 || got[0] != 3 {
		t.Fatalf("adopted row attachments = %v", got)
	}
	if other := lineRow(t, repo, domain.DirectLine(5)); other.ID == legacyID {
		t.Fatal("two lines share the legacy row")
	}
}

func assertClientBaselines(t *testing.T, got, want *domain.PSPClient) {
	t.Helper()
	if got.LifetimeUpBytes != want.LifetimeUpBytes || got.LifetimeDownBytes != want.LifetimeDownBytes || got.LifetimeTotalBytes != want.LifetimeTotalBytes ||
		got.LastRawUpBytes != want.LastRawUpBytes || got.LastRawDownBytes != want.LastRawDownBytes || got.LastRawTotalBytes != want.LastRawTotalBytes ||
		got.PeriodBaselineUpBytes != want.PeriodBaselineUpBytes || got.PeriodBaselineDownBytes != want.PeriodBaselineDownBytes || got.PeriodBaselineTotalBytes != want.PeriodBaselineTotalBytes ||
		got.LastCounterEpoch != want.LastCounterEpoch {
		t.Fatalf("counter baselines changed:\n got  %+v\n want %+v", got, want)
	}
}

func tunnelNodes() []*domain.Node {
	child := &domain.Node{ID: 20, PanelID: 11, DesiredProtocol: "vless", Flow: "xtls-rprx-vision", ParentNodeID: 1, ParentLineID: "t"}
	return []*domain.Node{{ID: 1, PanelID: 10, DesiredProtocol: "vless", Relays: []domain.RelayLine{
		{ID: "t", Enabled: true, EntryPanelID: 11, Mode: domain.RelayModeTunnel, TunnelNodeID: 20, TunnelNode: child},
	}}}
}

// A tunnel line's client is a row of the line (the landing's node and line) on
// the entry server, attached to the line's own listener. The landing's server
// holds only the direct line, and the user's other panels are not touched.
func TestSyncUser_ATunnelLineLivesOnTheEntryServersListener(t *testing.T) {
	repo := newFakeRepo()
	if _, err := New(repo).SyncUser(context.Background(), 42, "uuid-x", rules, tunnelNodes()); err != nil {
		t.Fatal(err)
	}
	if len(repo.clients) != 2 {
		t.Fatalf("rows = %d, want the direct line on server 10 and the tunnel line on server 11", len(repo.clients))
	}
	direct := lineRow(t, repo, domain.DirectLine(1))
	tunnel := lineRow(t, repo, domain.LineKey{NodeID: 1, LineID: "t"})
	if direct.PanelID != 10 || tunnel.PanelID != 11 {
		t.Fatalf("panels: direct %d, tunnel %d", direct.PanelID, tunnel.PanelID)
	}
	if got := attached(repo, tunnel); len(got) != 1 || got[0] != 20 {
		t.Fatalf("tunnel row attached to %v, want its listener 20", got)
	}
	if got := attached(repo, direct); len(got) != 1 || got[0] != 1 {
		t.Fatalf("direct row attached to %v", got)
	}
	if want := domain.DeriveLineUUID("uuid-x", domain.LineKey{NodeID: 1, LineID: "t"}, 1); tunnel.UUID != want || tunnel.UUID == "uuid-x" {
		t.Fatalf("tunnel uuid = %s, want the derived %s", tunnel.UUID, want)
	}
}

// When the line goes (removed, switched to a forward), its row is detached and
// keeps its counters like any dropped line, and is reported as regrouped, not as
// a lost server.
func TestSyncUser_ARemovedTunnelLineIsDetachedFromItsListener(t *testing.T) {
	repo := newFakeRepo()
	svc := New(repo)
	ctx := context.Background()
	if _, err := svc.SyncUser(ctx, 42, "uuid-x", rules, tunnelNodes()); err != nil {
		t.Fatal(err)
	}
	tunnel := lineRow(t, repo, domain.LineKey{NodeID: 1, LineID: "t"})
	got, err := svc.SyncUserRetirements(ctx, 42, "uuid-x", rules, []*domain.Node{{ID: 1, PanelID: 10, DesiredProtocol: "vless"}})
	if err != nil {
		t.Fatal(err)
	}
	if kept, gerr := repo.GetByID(ctx, tunnel.ID); gerr != nil || len(attached(repo, kept)) != 0 {
		t.Fatalf("tunnel row after its line went: %+v (%v)", kept, gerr)
	}
	if len(got.PanelRemoved[11]) != 1 {
		t.Fatalf("retired = %+v, want the tunnel row retired from server 11 (the user has nothing left there)", got)
	}
}

// Switching a line forward -> tunnel -> forward never collides with the old
// server's row: the identity moves, and comes back on the row it left.
func TestSyncUser_AModeSwitchMovesTheIdentityBetweenServersAndBack(t *testing.T) {
	repo := newFakeRepo()
	svc := New(repo)
	ctx := context.Background()
	forward := []*domain.Node{{ID: 1, PanelID: 10, DesiredProtocol: "vless", Relays: []domain.RelayLine{{ID: "t", Enabled: true, EntryPanelID: 11, Port: 20001}}}}
	var firstOn10 int64
	for step, nodes := range [][]*domain.Node{forward, tunnelNodes(), forward, tunnelNodes()} {
		if _, err := svc.SyncUser(ctx, 42, "uuid-x", rules, nodes); err != nil {
			t.Fatalf("step %d: %v", step, err)
		}
		if step == 0 {
			firstOn10 = lineRowOn(t, repo, 10, "t").ID
		}
	}
	if got := lineRowOn(t, repo, 10, "t").ID; got != firstOn10 {
		t.Fatalf("the forward row was replaced instead of reused: %d then %d", firstOn10, got)
	}
}

func lineRowOn(t *testing.T, repo *fakePSPClientRepo, panel int64, line string) *domain.PSPClient {
	t.Helper()
	for _, c := range repo.clients {
		if c.PanelID == panel && c.LineID == line {
			return c
		}
	}
	t.Fatalf("no row of line %s on server %d", line, panel)
	return nil
}
