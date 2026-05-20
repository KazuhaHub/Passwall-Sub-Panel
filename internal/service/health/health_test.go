package health

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

// --- pure logic: decideHealth -----------------------------------------------

func TestDecideHealth_OK(t *testing.T) {
	st, detail := decideHealth(1, map[int]ports.Inbound{
		1: {ID: 1, Enable: true},
	})
	if st != domain.NodeHealthOK {
		t.Fatalf("state = %q, want ok", st)
	}
	if detail != "" {
		t.Fatalf("detail = %q, want empty when healthy", detail)
	}
}

func TestDecideHealth_InboundDisabled(t *testing.T) {
	st, detail := decideHealth(2, map[int]ports.Inbound{
		2: {ID: 2, Enable: false},
	})
	if st != domain.NodeHealthInboundDisabled {
		t.Fatalf("state = %q, want inbound_disabled", st)
	}
	if detail == "" {
		t.Fatal("detail must explain why")
	}
}

func TestDecideHealth_InboundMissing(t *testing.T) {
	st, detail := decideHealth(99, map[int]ports.Inbound{
		1: {ID: 1, Enable: true},
	})
	if st != domain.NodeHealthInboundMissing {
		t.Fatalf("state = %q, want inbound_missing", st)
	}
	if detail == "" {
		t.Fatal("detail must mention the inbound id")
	}
}

// --- integration: CheckOnce orchestration ----------------------------------

type fakeNodeRepo struct {
	nodes   []*domain.Node
	updates []*domain.Node
}

func (r *fakeNodeRepo) List(ctx context.Context) ([]*domain.Node, error) {
	return r.nodes, nil
}
func (r *fakeNodeRepo) ListEnabled(ctx context.Context) ([]*domain.Node, error) {
	out := []*domain.Node{}
	for _, n := range r.nodes {
		if n.Enabled {
			out = append(out, n)
		}
	}
	return out, nil
}
func (r *fakeNodeRepo) GetByID(ctx context.Context, id int64) (*domain.Node, error) {
	for _, n := range r.nodes {
		if n.ID == id {
			return n, nil
		}
	}
	return nil, domain.ErrNotFound
}
func (r *fakeNodeRepo) GetByPanelInbound(ctx context.Context, panelID int64, inboundID int) (*domain.Node, error) {
	return nil, domain.ErrNotFound
}
func (r *fakeNodeRepo) Update(ctx context.Context, n *domain.Node) error {
	cp := *n
	r.updates = append(r.updates, &cp)
	return nil
}
func (r *fakeNodeRepo) UpdateHealth(ctx context.Context, n *domain.Node) error {
	cp := *n
	r.updates = append(r.updates, &cp)
	return nil
}
func (r *fakeNodeRepo) UpdateTrafficCounters(ctx context.Context, n *domain.Node) error { return nil }
func (r *fakeNodeRepo) Create(ctx context.Context, n *domain.Node) error                       { return nil }
func (r *fakeNodeRepo) Delete(ctx context.Context, id int64) error                             { return nil }
func (r *fakeNodeRepo) BatchUpdateSortOrder(ctx context.Context, updates []ports.NodeSortUpdate) error {
	return nil
}

type fakeXUIClient struct {
	inbounds []ports.Inbound
	err      error
}

func (c *fakeXUIClient) ListInbounds(ctx context.Context) ([]ports.Inbound, error) {
	return c.inbounds, c.err
}
func (c *fakeXUIClient) GetInbound(ctx context.Context, id int) (*ports.Inbound, error) {
	return nil, domain.ErrNotFound
}
func (c *fakeXUIClient) AddInbound(ctx context.Context, spec ports.InboundSpec) (int, error) {
	return 0, nil
}
func (c *fakeXUIClient) UpdateInbound(ctx context.Context, id int, spec ports.InboundSpec) error {
	return nil
}
func (c *fakeXUIClient) DelInbound(ctx context.Context, id int) error                         { return nil }
func (c *fakeXUIClient) SetInboundEnable(ctx context.Context, id int, enable bool) error      { return nil }
func (c *fakeXUIClient) AddClient(ctx context.Context, id int, spec ports.ClientSpec) error   { return nil }
func (c *fakeXUIClient) UpdateClient(ctx context.Context, id int, uuid string, s ports.ClientSpec) error {
	return nil
}
func (c *fakeXUIClient) DelClient(ctx context.Context, id int, uuid string) error           { return nil }
func (c *fakeXUIClient) DelClientByEmail(ctx context.Context, id int, email string) error   { return nil }
func (c *fakeXUIClient) CopyClients(ctx context.Context, src, dst int, emails []string) error {
	return nil
}
func (c *fakeXUIClient) GetClientTraffic(ctx context.Context, email string) ([]ports.ClientTraffic, error) {
	return nil, nil
}
func (c *fakeXUIClient) GetInboundTraffics(ctx context.Context, id int) ([]ports.ClientTraffic, error) {
	return nil, nil
}
func (c *fakeXUIClient) ResetClientTraffic(ctx context.Context, id int, email string) error {
	return nil
}
func (c *fakeXUIClient) GetInboundClients(ctx context.Context, id int) ([]ports.ClientDetail, error) {
	return nil, nil
}

type fakePool struct {
	clients map[int64]ports.XUIClient
	err     map[int64]error
}

func (p *fakePool) Get(panelID int64) (ports.XUIClient, error) {
	if e, ok := p.err[panelID]; ok {
		return nil, e
	}
	c, ok := p.clients[panelID]
	if !ok {
		return nil, domain.ErrNotFound
	}
	return c, nil
}
func (p *fakePool) List() []*domain.XUIPanel       { return nil }
func (p *fakePool) Add(panel *domain.XUIPanel) error { return nil }
func (p *fakePool) Remove(panelID int64) error      { return nil }

func TestCheckOnce_HappyPath(t *testing.T) {
	repo := &fakeNodeRepo{nodes: []*domain.Node{
		{ID: 1, PanelID: 10, InboundID: 1, Enabled: true},
		{ID: 2, PanelID: 10, InboundID: 2, Enabled: true},
	}}
	pool := &fakePool{clients: map[int64]ports.XUIClient{
		10: &fakeXUIClient{inbounds: []ports.Inbound{
			{ID: 1, Enable: true},
			{ID: 2, Enable: true},
		}},
	}}
	if err := New(repo, pool).CheckOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(repo.updates) != 2 {
		t.Fatalf("updates = %d, want 2", len(repo.updates))
	}
	for _, u := range repo.updates {
		if u.HealthState != domain.NodeHealthOK {
			t.Fatalf("node %d state = %q, want ok", u.ID, u.HealthState)
		}
		if u.HealthCheckedAt == nil {
			t.Fatalf("node %d missing HealthCheckedAt", u.ID)
		}
	}
}

func TestCheckOnce_PanelUnreachableMarksAllNodes(t *testing.T) {
	repo := &fakeNodeRepo{nodes: []*domain.Node{
		{ID: 1, PanelID: 10, InboundID: 1, Enabled: true},
		{ID: 2, PanelID: 10, InboundID: 2, Enabled: true},
	}}
	// Panel exists but ListInbounds errors out (network / auth).
	pool := &fakePool{clients: map[int64]ports.XUIClient{
		10: &fakeXUIClient{err: errors.New("connection refused")},
	}}
	if err := New(repo, pool).CheckOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	for _, u := range repo.updates {
		if u.HealthState != domain.NodeHealthPanelUnreachable {
			t.Fatalf("node %d state = %q, want panel_unreachable", u.ID, u.HealthState)
		}
		if u.HealthDetail == "" {
			t.Fatal("detail should include the underlying error")
		}
	}
}

func TestCheckOnce_DisabledNodesSkipped(t *testing.T) {
	repo := &fakeNodeRepo{nodes: []*domain.Node{
		{ID: 1, PanelID: 10, InboundID: 1, Enabled: false, HealthState: domain.NodeHealthOK},
	}}
	pool := &fakePool{}
	if err := New(repo, pool).CheckOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(repo.updates) != 0 {
		t.Fatalf("disabled node was probed (%d updates); admin took it out of rotation deliberately", len(repo.updates))
	}
}

func TestCheckOnce_SkipsUpdateWhenStateUnchanged(t *testing.T) {
	// Node is already marked OK with the same (empty) detail; CheckOnce
	// must not write to the DB on every tick when nothing changed.
	repo := &fakeNodeRepo{nodes: []*domain.Node{
		{ID: 1, PanelID: 10, InboundID: 1, Enabled: true,
			HealthState: domain.NodeHealthOK, HealthDetail: ""},
	}}
	pool := &fakePool{clients: map[int64]ports.XUIClient{
		10: &fakeXUIClient{inbounds: []ports.Inbound{{ID: 1, Enable: true}}},
	}}
	if err := New(repo, pool).CheckOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(repo.updates) != 0 {
		t.Fatalf("update fired when state didn't change (%d writes)", len(repo.updates))
	}
}

func TestCheckOnce_InboundMissingAndDisabledMixed(t *testing.T) {
	repo := &fakeNodeRepo{nodes: []*domain.Node{
		{ID: 1, PanelID: 10, InboundID: 1, Enabled: true}, // OK
		{ID: 2, PanelID: 10, InboundID: 2, Enabled: true}, // disabled inbound
		{ID: 3, PanelID: 10, InboundID: 9, Enabled: true}, // missing
	}}
	pool := &fakePool{clients: map[int64]ports.XUIClient{
		10: &fakeXUIClient{inbounds: []ports.Inbound{
			{ID: 1, Enable: true},
			{ID: 2, Enable: false},
		}},
	}}
	if err := New(repo, pool).CheckOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	got := map[int64]domain.NodeHealthState{}
	for _, u := range repo.updates {
		got[u.ID] = u.HealthState
	}
	want := map[int64]domain.NodeHealthState{
		1: domain.NodeHealthOK,
		2: domain.NodeHealthInboundDisabled,
		3: domain.NodeHealthInboundMissing,
	}
	for id, w := range want {
		if got[id] != w {
			t.Fatalf("node %d: got %q, want %q", id, got[id], w)
		}
	}
}

// Sanity: Loop respects ctx cancellation promptly.
func TestLoopReturnsOnCancel(t *testing.T) {
	repo := &fakeNodeRepo{}
	pool := &fakePool{}
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // already done before Loop starts
	done := make(chan struct{})
	go func() {
		New(repo, pool).Loop(ctx, 1*time.Hour)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Loop did not return after ctx cancellation")
	}
}
