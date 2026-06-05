package health

import (
	"context"
	"errors"
	"strconv"
	"testing"
	"time"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

// --- pure logic: isUDPProtocol ----------------------------------------------

func TestIsUDPProtocol(t *testing.T) {
	for _, p := range []string{"hysteria2", "Hysteria2", "hy2", "hysteria"} {
		if !isUDPProtocol(p) {
			t.Fatalf("isUDPProtocol(%q) = false, want true", p)
		}
	}
	for _, p := range []string{"vless", "vmess", "trojan", "shadowsocks", ""} {
		if isUDPProtocol(p) {
			t.Fatalf("isUDPProtocol(%q) = true, want false", p)
		}
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
func (r *fakeNodeRepo) ListPaged(ctx context.Context, _ ports.Pagination) ([]*domain.Node, int64, error) {
	return r.nodes, int64(len(r.nodes)), nil
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
func (r *fakeNodeRepo) UpdateMetadata(ctx context.Context, n *domain.Node) error {
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
func (r *fakeNodeRepo) BatchUpdateTrafficCounters(ctx context.Context, nodes []*domain.Node) error {
	return nil
}
func (r *fakeNodeRepo) UpdateInboundConfig(ctx context.Context, n *domain.Node) error   { return nil }
func (r *fakeNodeRepo) UpdateEnabled(ctx context.Context, id int64, enabled bool) error { return nil }
func (r *fakeNodeRepo) UpdateCertBinding(_ context.Context, _ int64, _ domain.CertSource, _ int64) error {
	return nil
}
func (r *fakeNodeRepo) ListByCertID(_ context.Context, _ int64) ([]*domain.Node, error) { return nil, nil }
func (r *fakeNodeRepo) Create(ctx context.Context, n *domain.Node) error                { return nil }
func (r *fakeNodeRepo) Delete(ctx context.Context, id int64) error                      { return nil }
func (r *fakeNodeRepo) BatchUpdateSortOrder(ctx context.Context, updates []ports.NodeSortUpdate) error {
	return nil
}

// newWithProbe builds a Service with an injected probe that records the
// (network, host, port) it was called with and returns the given error.
//
// v3.5: Service no longer takes a pool — port/protocol come from the Node row
// itself (written by the inbound write-through paths and aligned by reconcile
// axis A), so tests just set them on the node directly.
func newWithProbe(repo ports.NodeRepo, ret error) (*Service, *[]string) {
	calls := &[]string{}
	s := New(repo)
	s.probe = func(_ context.Context, network, host string, port int) error {
		*calls = append(*calls, network+" "+host+":"+strconv.Itoa(port))
		return ret
	}
	return s, calls
}

func TestCheckOnce_PortOpen_Up(t *testing.T) {
	repo := &fakeNodeRepo{nodes: []*domain.Node{
		{ID: 1, PanelID: 10, InboundID: 1, Enabled: true, ServerAddress: "a.example", Port: 443, Protocol: "vless"},
	}}
	s, calls := newWithProbe(repo, nil)
	if err := s.CheckOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(repo.updates) != 1 || repo.updates[0].HealthState != domain.NodeHealthOK {
		t.Fatalf("want one OK update, got %+v", repo.updates)
	}
	if repo.updates[0].HealthCheckedAt == nil {
		t.Fatal("HealthCheckedAt must be stamped every pass")
	}
	if repo.updates[0].Port != 443 {
		t.Fatalf("port not preserved through persist: got %d, want 443", repo.updates[0].Port)
	}
	if len(*calls) != 1 || (*calls)[0] != "tcp a.example:443" {
		t.Fatalf("probe calls = %v, want one tcp a.example:443", *calls)
	}
}

func TestCheckOnce_PortClosed_Down(t *testing.T) {
	repo := &fakeNodeRepo{nodes: []*domain.Node{
		{ID: 1, PanelID: 10, InboundID: 1, Enabled: true, ServerAddress: "a.example", Port: 443, Protocol: "vless"},
	}}
	s, _ := newWithProbe(repo, errors.New("connection refused"))
	if err := s.CheckOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(repo.updates) != 1 || repo.updates[0].HealthState != domain.NodeHealthUnreachable {
		t.Fatalf("want one unreachable update, got %+v", repo.updates)
	}
}

func TestCheckOnce_UDPProtocolProbedWithUDP(t *testing.T) {
	repo := &fakeNodeRepo{nodes: []*domain.Node{
		{ID: 1, PanelID: 10, InboundID: 1, Enabled: true, ServerAddress: "h.example", Port: 8443, Protocol: "hysteria2"},
	}}
	s, calls := newWithProbe(repo, nil)
	if err := s.CheckOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(*calls) != 1 || (*calls)[0] != "udp h.example:8443" {
		t.Fatalf("hysteria2 must be probed over udp; calls = %v", *calls)
	}
}

// Pre-v3.5 row (or freshly imported node before reconcile backfills) with no
// captured port: must not probe, surfaces as Unreachable so the UI doesn't
// show a stale green dot. The "panel_unreachable" attribution and the
// "inbound_missing" detection both moved out of health in v3.5 — health is now
// pure data-plane reachability, and 3X-UI inbound existence is reconcile §9.4.3
// #6's job.
func TestCheckOnce_NoPortInRow_Unreachable(t *testing.T) {
	repo := &fakeNodeRepo{nodes: []*domain.Node{
		{ID: 1, PanelID: 10, InboundID: 1, Enabled: true, ServerAddress: "a.example"}, // Port 0
	}}
	s, calls := newWithProbe(repo, nil)
	if err := s.CheckOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(*calls) != 0 {
		t.Fatalf("must not probe without a known port; calls = %v", *calls)
	}
	if len(repo.updates) != 1 || repo.updates[0].HealthState != domain.NodeHealthUnreachable {
		t.Fatalf("want unreachable, got %+v", repo.updates)
	}
}

func TestCheckOnce_DisabledNodesSkipped(t *testing.T) {
	repo := &fakeNodeRepo{nodes: []*domain.Node{
		{ID: 1, PanelID: 10, InboundID: 1, Enabled: false, Port: 443, Protocol: "vless", HealthState: domain.NodeHealthOK},
	}}
	s, _ := newWithProbe(repo, nil)
	if err := s.CheckOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(repo.updates) != 0 {
		t.Fatalf("disabled node was probed (%d updates); admin took it out of rotation deliberately", len(repo.updates))
	}
}

// Sanity: Loop respects ctx cancellation promptly.
func TestLoopReturnsOnCancel(t *testing.T) {
	repo := &fakeNodeRepo{}
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // already done before Loop starts
	done := make(chan struct{})
	go func() {
		New(repo).Loop(ctx, 1*time.Hour)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Loop did not return after ctx cancellation")
	}
}
