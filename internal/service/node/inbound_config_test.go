package node

import (
	"context"
	"errors"
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
	created   *domain.Node
}

func (r *captureNodeRepo) Create(_ context.Context, n *domain.Node) error {
	r.created = n
	if n.ID == 0 {
		n.ID = 1 // mimic the DB assigning an autoincrement id
	}
	return nil
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
func (r *captureNodeRepo) UpdateEnabled(_ context.Context, _ int64, _ bool) error { return nil }

type stubXUIClient struct {
	ports.XUIClient
	updated *ports.InboundSpec
	getResp *ports.Inbound
	addID   int
	added   *ports.InboundSpec
}

func (c *stubXUIClient) UpdateInbound(_ context.Context, _ int, spec ports.InboundSpec) error {
	c.updated = &spec
	return nil
}

func (c *stubXUIClient) GetInbound(_ context.Context, _ int) (*ports.Inbound, error) {
	return c.getResp, nil
}

func (c *stubXUIClient) AddInbound(_ context.Context, spec ports.InboundSpec) (int, error) {
	c.added = &spec
	return c.addID, nil
}

// minimal deps so CreateInbound / ImportExisting can run syncExistingUsersToNode
// without panicking; an empty group list makes the per-group loop a no-op.
type emptyGroups struct{ ports.GroupRepo }

func (emptyGroups) List(context.Context) ([]*domain.Group, error) { return nil, nil }

type settingsStub struct{}

func (settingsStub) Load(_ context.Context, _ ports.UISettings) (ports.UISettings, error) {
	return ports.UISettings{EmailDomain: "kazuha.org"}, nil
}
func (settingsStub) Save(_ context.Context, _ ports.UISettings) error { return nil }

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
// And the state column flips to "pending" so the UI / dashboard can show
// "PSP wants this config but couldn't deliver it" instead of misleadingly
// reading "synced" (which was the bug before this assertion landed).
func TestUpdateInboundConfig_PushFails_StillStoredLocally(t *testing.T) {
	repo := &captureNodeRepo{node: &domain.Node{ID: 1, PanelID: 1, InboundID: 3}}
	svc := &Service{nodes: repo, pool: stubXUIPool{err: errPanelDown{}}}

	if err := svc.UpdateInboundConfig(context.Background(), 1, updateSpec()); err != nil {
		t.Fatalf("UpdateInboundConfig = %v, want nil (push failure is enqueued, not returned)", err)
	}
	if repo.updateCfg == nil || repo.updateCfg.StreamSettings == "" {
		t.Fatalf("config must be persisted locally even when the push fails")
	}
	if repo.updateCfg.ConfigSyncState != "pending" {
		t.Fatalf("push fail must mark ConfigSyncState=pending, got %q", repo.updateCfg.ConfigSyncState)
	}
}

// The SyncTaskNodeUpdate retry path must flip the snapshot back to "synced"
// when the deferred push finally lands — otherwise the UI shows pending
// forever even after 3X-UI is reconciled.
func TestRunNodeTask_UpdateSuccessClearsPending(t *testing.T) {
	now := time.Now()
	repo := &captureNodeRepo{node: &domain.Node{
		ID: 1, PanelID: 1, InboundID: 3,
		Protocol:        "vless",
		Port:            443,
		StreamSettings:  `{"network":"ws"}`,
		InboundSettings: `{"decryption":"none"}`,
		ConfigSyncedAt:  &now,
		ConfigSyncState: "pending", // left over from the original push-fail
	}}
	client := &stubXUIClient{}
	svc := &Service{nodes: repo, pool: stubXUIPool{c: client}}

	if err := svc.runNodeTask(context.Background(), &domain.SyncTask{Type: domain.SyncTaskNodeUpdate, TargetID: 1}); err != nil {
		t.Fatalf("runNodeTask = %v, want nil", err)
	}
	if client.updated == nil {
		t.Fatalf("retry must push to 3X-UI")
	}
	if repo.updateCfg == nil || repo.updateCfg.ConfigSyncState != "synced" {
		t.Fatalf("retry success must flip ConfigSyncState back to synced, got %+v", repo.updateCfg)
	}
}

type errPanelDown struct{}

func (errPanelDown) Error() string { return "panel unreachable" }

// ---- Create / Import write-through: config captured into the node row ----

// CreateInbound must persist the just-pushed config into the local snapshot so
// the node renders without a live fetch from its first subscription.
func TestCreateInbound_WriteThrough(t *testing.T) {
	repo := &captureNodeRepo{}
	// getResp feeds inspectInbound (protocol detection) inside the post-create
	// user sync; addID is the inbound id 3X-UI "returns".
	client := &stubXUIClient{addID: 7, getResp: &ports.Inbound{Protocol: "vless", Settings: `{"decryption":"none"}`}}
	svc := &Service{
		nodes:    repo,
		pool:     stubXUIPool{c: client},
		groups:   emptyGroups{},
		settings: settingsStub{},
	}
	n := &domain.Node{DisplayName: "US-1", Region: "us", PanelID: 1}
	spec := ports.InboundSpec{
		Protocol:       "vless",
		Port:           443,
		StreamSettings: `{"network":"ws","security":"tls"}`,
		Settings:       `{"decryption":"none","clients":[{"id":"x","email":"e"}]}`,
	}
	if err := svc.CreateInbound(context.Background(), n, spec); err != nil {
		t.Fatalf("CreateInbound = %v, want nil", err)
	}
	if repo.created == nil {
		t.Fatalf("node not created")
	}
	got := repo.created
	if got.InboundID != 7 {
		t.Fatalf("inbound id from AddInbound not recorded: %+v", got)
	}
	if got.ConfigSyncedAt == nil {
		t.Fatalf("config snapshot not captured on create (ConfigSyncedAt nil)")
	}
	if got.StreamSettings != `{"network":"ws","security":"tls"}` || got.Port != 443 {
		t.Fatalf("create did not store the inbound config: %+v", got)
	}
	if strings.Contains(got.InboundSettings, "clients") {
		t.Fatalf("stored settings must drop clients[]: %s", got.InboundSettings)
	}
}

// CreateInbound must return at once even when the post-create user-sync would
// otherwise block on N sequential 3X-UI round-trips. We prove it: pin
// groups.List inside the user-sync to a channel and verify CreateInbound
// returns BEFORE we release it — the sync has to be off the request thread.
func TestCreateInbound_SyncRunsInBackground(t *testing.T) {
	repo := &captureNodeRepo{}
	client := &stubXUIClient{addID: 7, getResp: &ports.Inbound{Protocol: "vless", Settings: `{"decryption":"none"}`}}
	syncReached := make(chan struct{}, 1)
	release := make(chan struct{})
	svc := &Service{
		nodes: repo,
		pool:  stubXUIPool{c: client},
		groups: blockingGroups{
			called:  syncReached,
			release: release,
		},
		settings: settingsStub{},
	}
	n := &domain.Node{DisplayName: "X", Region: "us", PanelID: 1}
	spec := ports.InboundSpec{Protocol: "vless", Port: 443, StreamSettings: `{"network":"ws"}`}

	done := make(chan error, 1)
	go func() { done <- svc.CreateInbound(context.Background(), n, spec) }()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("CreateInbound = %v, want nil", err)
		}
	case <-time.After(500 * time.Millisecond):
		close(release)
		t.Fatal("CreateInbound blocked waiting on user-sync; sync must run off the request thread")
	}

	// And the background sync did kick off.
	select {
	case <-syncReached:
	case <-time.After(2 * time.Second):
		close(release)
		t.Fatal("background user-sync never reached groups.List")
	}
	close(release)
}

// blockingGroups gates the post-create user-sync inside groups.List so the
// async test can prove CreateInbound returns without waiting.
type blockingGroups struct {
	ports.GroupRepo
	called  chan<- struct{}
	release <-chan struct{}
}

func (b blockingGroups) List(ctx context.Context) ([]*domain.Group, error) {
	select {
	case b.called <- struct{}{}:
	default:
	}
	select {
	case <-b.release:
	case <-ctx.Done():
	}
	return nil, nil
}

// ImportExisting = take ownership: capture the live inbound's config into the
// node so render reads it locally and reconcile can keep 3X-UI aligned.
func TestImportExisting_TakesOwnership(t *testing.T) {
	repo := &captureNodeRepo{}
	live := &ports.Inbound{
		Protocol:       "shadowsocks",
		Port:           8388,
		Listen:         "127.0.0.1",
		StreamSettings: `{"network":"tcp"}`,
		Settings:       `{"method":"aes-128-gcm","clients":[{"email":"old-friend"}]}`,
	}
	client := &stubXUIClient{getResp: live}
	svc := &Service{
		nodes:    repo,
		pool:     stubXUIPool{c: client},
		groups:   emptyGroups{},
		settings: settingsStub{},
	}
	n := &domain.Node{DisplayName: "imported", Region: "us", PanelID: 1, InboundID: 3}
	if err := svc.ImportExisting(context.Background(), n); err != nil {
		t.Fatalf("ImportExisting = %v, want nil", err)
	}
	if repo.created == nil {
		t.Fatalf("node not created on import")
	}
	got := repo.created
	if got.ConfigSyncedAt == nil {
		t.Fatalf("import must capture the live config (ConfigSyncedAt nil)")
	}
	if got.Protocol != "shadowsocks" || got.Port != 8388 || got.InboundListen != "127.0.0.1" {
		t.Fatalf("import did not capture live config: %+v", got)
	}
	if strings.Contains(got.InboundSettings, "clients") {
		t.Fatalf("captured settings must drop clients[]: %s", got.InboundSettings)
	}
	if !strings.Contains(got.InboundSettings, "aes-128-gcm") {
		t.Fatalf("ss method must survive the clients strip: %s", got.InboundSettings)
	}
}

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

// slimInboundsClient serves the live inbound list via ListInboundsSlim — the
// endpoint tryAdoptOrphan must use, since it only matches on port/protocol/listen
// and Capture strips clients[] anyway. ListInbounds is wired to FAIL so a
// regression back to the heavy full-list pull (every inbound with full
// settings.clients[]) is caught here instead of silently wasting transfer.
type slimInboundsClient struct {
	ports.XUIClient
	live []ports.Inbound
}

func (c *slimInboundsClient) ListInboundsSlim(context.Context) ([]ports.Inbound, error) {
	return c.live, nil
}

func (c *slimInboundsClient) ListInbounds(context.Context) ([]ports.Inbound, error) {
	return nil, errors.New("tryAdoptOrphan must use ListInboundsSlim, not ListInbounds (avoids pulling every client)")
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
	client := &slimInboundsClient{live: live}
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
	client := &slimInboundsClient{live: live}
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
	client := &slimInboundsClient{live: live}
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
