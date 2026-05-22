package node

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

// UpdateInboundConfig write-through integration. The field-level mapping
// (clients[] stripping, snapshot capture) is unit-tested in the inboundcfg
// package; here we verify the service persists locally before pushing.

type captureNodeRepo struct {
	fakeNodeRepo
	node *domain.Node
	// Separate counters per method so a test catches regressions where the
	// service accidentally calls the full-row Save path instead of the
	// column-scoped snapshot writer (or vice versa). updateCfg is the
	// snapshot path; update is everything else.
	updateCfg *domain.Node
	update    *domain.Node
}

func (r *captureNodeRepo) GetByID(_ context.Context, _ int64) (*domain.Node, error) {
	if r.node == nil {
		return nil, domain.ErrNotFound
	}
	cp := *r.node
	return &cp, nil
}
func (r *captureNodeRepo) Update(_ context.Context, n *domain.Node) error {
	r.update = n
	return nil
}
func (r *captureNodeRepo) UpdateInboundConfig(_ context.Context, n *domain.Node) error {
	r.updateCfg = n
	return nil
}

type stubXUIClient struct {
	ports.XUIClient
	updated *ports.InboundSpec
	getResp *ports.Inbound
}

func (c *stubXUIClient) UpdateInbound(_ context.Context, _ int, spec ports.InboundSpec) error {
	c.updated = &spec
	return nil
}

func (c *stubXUIClient) GetInbound(_ context.Context, _ int) (*ports.Inbound, error) {
	return c.getResp, nil
}

type stubXUIPool struct {
	c   ports.XUIClient
	err error
}

func (p stubXUIPool) Get(int64) (ports.XUIClient, error) { return p.c, p.err }
func (stubXUIPool) List() []*domain.XUIPanel             { return nil }
func (stubXUIPool) Add(*domain.XUIPanel) error           { return nil }
func (stubXUIPool) Remove(int64) error                   { return nil }

func updateSpec() ports.InboundSpec {
	return ports.InboundSpec{
		Protocol:       "vless",
		Port:           443,
		Settings:       `{"decryption":"none","clients":[{"id":"x","email":"e"}]}`,
		StreamSettings: `{"network":"ws","security":"tls"}`,
	}
}

func TestUpdateInboundConfig_WriteThrough_PushOK(t *testing.T) {
	repo := &captureNodeRepo{node: &domain.Node{ID: 1, PanelID: 1, InboundID: 3}}
	client := &stubXUIClient{}
	svc := &Service{nodes: repo, pool: stubXUIPool{c: client}}

	if err := svc.UpdateInboundConfig(context.Background(), 1, updateSpec()); err != nil {
		t.Fatalf("UpdateInboundConfig = %v, want nil", err)
	}
	// Snapshot writes MUST go through the column-scoped UpdateInboundConfig,
	// not the full-row Save. Calling Update here would race against the
	// health pass / traffic poll on shared columns.
	if repo.update != nil {
		t.Fatalf("snapshot write must NOT call Update (full-row Save races with health writer)")
	}
	if repo.updateCfg == nil {
		t.Fatalf("config not persisted locally (write-through missing)")
	}
	if repo.updateCfg.StreamSettings != `{"network":"ws","security":"tls"}` {
		t.Fatalf("stream settings not stored: %+v", repo.updateCfg)
	}
	if strings.Contains(repo.updateCfg.InboundSettings, "clients") {
		t.Fatalf("stored settings must drop clients[]: %s", repo.updateCfg.InboundSettings)
	}
	if repo.updateCfg.ConfigSyncedAt == nil {
		t.Fatalf("ConfigSyncedAt should be set after write-through")
	}
	if client.updated == nil {
		t.Fatalf("config not pushed to 3X-UI")
	}
}

// Push fails (panel unreachable) but the local snapshot must still be written —
// local-first means render reflects the new config even while 3X-UI is down.
func TestUpdateInboundConfig_PushFails_StillStoredLocally(t *testing.T) {
	repo := &captureNodeRepo{node: &domain.Node{ID: 1, PanelID: 1, InboundID: 3}}
	svc := &Service{nodes: repo, pool: stubXUIPool{err: errPanelDown{}}}

	if err := svc.UpdateInboundConfig(context.Background(), 1, updateSpec()); err != nil {
		t.Fatalf("UpdateInboundConfig = %v, want nil (push failure is enqueued, not returned)", err)
	}
	if repo.updateCfg == nil || repo.updateCfg.StreamSettings == "" {
		t.Fatalf("config must be persisted locally even when the push fails")
	}
}

type errPanelDown struct{}

func (errPanelDown) Error() string { return "panel unreachable" }

// ---- GetInboundConfig reads the local snapshot (v3.5 source-of-truth) ----

// A captured node's edit dialog must read the local snapshot, never live 3X-UI,
// so the form, render and reconcile all agree. The pool errors here to prove it
// is never consulted.
func TestGetInboundConfig_LocalSnapshot(t *testing.T) {
	now := time.Now()
	repo := &captureNodeRepo{node: &domain.Node{
		ID: 1, PanelID: 1, InboundID: 3,
		Protocol:        "vless",
		Port:            443,
		StreamSettings:  `{"network":"ws"}`,
		InboundSettings: `{"decryption":"none"}`,
		ConfigSyncedAt:  &now,
		ConfigSyncState: "synced",
	}}
	svc := &Service{nodes: repo, pool: stubXUIPool{err: errPanelDown{}}}

	inb, err := svc.GetInboundConfig(context.Background(), 1)
	if err != nil {
		t.Fatalf("GetInboundConfig (local) = %v, want nil (must not hit the pool)", err)
	}
	if inb.Protocol != "vless" || inb.Port != 443 || inb.StreamSettings != `{"network":"ws"}` {
		t.Fatalf("expected the local snapshot, got %+v", inb)
	}
}

// A never-captured node (pre-v3.5 / freshly imported before backfill) falls
// back to a live fetch.
func TestGetInboundConfig_FallbackLiveWhenUncaptured(t *testing.T) {
	repo := &captureNodeRepo{node: &domain.Node{ID: 1, PanelID: 1, InboundID: 3}} // ConfigSyncedAt nil
	client := &stubXUIClient{getResp: &ports.Inbound{Protocol: "trojan", Port: 8443}}
	svc := &Service{nodes: repo, pool: stubXUIPool{c: client}}

	inb, err := svc.GetInboundConfig(context.Background(), 1)
	if err != nil {
		t.Fatalf("GetInboundConfig (fallback) = %v, want nil", err)
	}
	if inb.Protocol != "trojan" || inb.Port != 8443 {
		t.Fatalf("un-captured node must live-fetch; got %+v", inb)
	}
}

// ---- Orphan-recovery for CreateInbound (lost AddInbound response) ----

// listInboundsClient returns the configured list from ListInbounds; AddInbound
// is irrelevant here because tryAdoptOrphan only inspects the live list.
type listInboundsClient struct {
	ports.XUIClient
	live []ports.Inbound
}

func (c *listInboundsClient) ListInbounds(context.Context) ([]ports.Inbound, error) {
	return c.live, nil
}

// orphanRecoveryRepo lets a test seed which (panel_id, inbound_id) pairs are
// already owned by some other node — so tryAdoptOrphan's "don't double-adopt"
// guard can be exercised.
type orphanRecoveryRepo struct {
	fakeNodeRepo
	owned map[int64]map[int]bool // panelID -> inboundID -> "owned"
}

func (r *orphanRecoveryRepo) GetByPanelInbound(_ context.Context, panelID int64, inboundID int) (*domain.Node, error) {
	if r.owned[panelID][inboundID] {
		return &domain.Node{ID: 99, PanelID: panelID, InboundID: inboundID}, nil
	}
	return nil, domain.ErrNotFound
}

func TestTryAdoptOrphan_AdoptsExactMatchWhenUnowned(t *testing.T) {
	live := []ports.Inbound{{
		ID: 5, Port: 443, Protocol: "vless", Listen: "0.0.0.0",
		StreamSettings: `{"network":"tcp","security":"reality"}`,
	}}
	client := &listInboundsClient{live: live}
	repo := &orphanRecoveryRepo{}
	svc := &Service{nodes: repo, pool: stubXUIPool{c: client}}

	got, err := svc.tryAdoptOrphan(context.Background(), client, 1, ports.InboundSpec{
		Port: 443, Protocol: "vless", Listen: "0.0.0.0",
	})
	if err != nil {
		t.Fatalf("tryAdoptOrphan = %v, want adopted match", err)
	}
	if got == nil || got.ID != 5 {
		t.Fatalf("expected adoption of inbound 5, got %+v", got)
	}
}

func TestTryAdoptOrphan_RefusesIfAlreadyOwnedByAnotherNode(t *testing.T) {
	live := []ports.Inbound{{
		ID: 5, Port: 443, Protocol: "vless", Listen: "0.0.0.0",
	}}
	client := &listInboundsClient{live: live}
	repo := &orphanRecoveryRepo{owned: map[int64]map[int]bool{1: {5: true}}}
	svc := &Service{nodes: repo, pool: stubXUIPool{c: client}}

	got, err := svc.tryAdoptOrphan(context.Background(), client, 1, ports.InboundSpec{
		Port: 443, Protocol: "vless", Listen: "0.0.0.0",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != nil {
		t.Fatalf("must NOT adopt an already-owned inbound; got %+v", got)
	}
}

func TestTryAdoptOrphan_RefusesOnProtocolMismatch(t *testing.T) {
	// Same port as our spec, but different protocol — that's a real conflict,
	// not a lost-response recovery. Don't adopt.
	live := []ports.Inbound{{
		ID: 5, Port: 443, Protocol: "trojan", Listen: "0.0.0.0",
	}}
	client := &listInboundsClient{live: live}
	repo := &orphanRecoveryRepo{}
	svc := &Service{nodes: repo, pool: stubXUIPool{c: client}}

	got, err := svc.tryAdoptOrphan(context.Background(), client, 1, ports.InboundSpec{
		Port: 443, Protocol: "vless", Listen: "0.0.0.0",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != nil {
		t.Fatalf("must NOT adopt across protocol mismatch; got %+v", got)
	}
}
