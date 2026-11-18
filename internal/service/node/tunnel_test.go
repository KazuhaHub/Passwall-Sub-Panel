package node

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"sync"
	"testing"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/pkg/realitykey"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
	"github.com/KazuhaHub/passwall-sub-panel/internal/service/front"
)

// memNodes is an in-memory node store that behaves like the real one where the
// tunnel code relies on it: ids are assigned, writes are column-scoped, reads
// return copies.
type memNodes struct {
	ports.NodeRepo
	mu    sync.Mutex
	nodes map[int64]*domain.Node
	next  int64
}

func newMemNodes(nodes ...*domain.Node) *memNodes {
	r := &memNodes{nodes: map[int64]*domain.Node{}, next: 100}
	for _, n := range nodes {
		r.nodes[n.ID] = copyNode(n)
	}
	return r
}

func copyNode(n *domain.Node) *domain.Node {
	cp := *n
	cp.Relays = append([]domain.RelayLine(nil), n.Relays...)
	for i := range cp.Relays {
		cp.Relays[i].Tags = append([]string(nil), cp.Relays[i].Tags...)
		cp.Relays[i].TunnelNode = nil
	}
	cp.Tags = append([]string(nil), n.Tags...)
	return &cp
}

func (r *memNodes) Create(_ context.Context, n *domain.Node) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if n.ID == 0 {
		r.next++
		n.ID = r.next
	}
	r.nodes[n.ID] = copyNode(n)
	return nil
}

func (r *memNodes) GetByID(_ context.Context, id int64) (*domain.Node, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if n, ok := r.nodes[id]; ok {
		return copyNode(n), nil
	}
	return nil, domain.ErrNotFound
}

func (r *memNodes) List(context.Context) ([]*domain.Node, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	ids := make([]int64, 0, len(r.nodes))
	for id := range r.nodes {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	out := make([]*domain.Node, 0, len(ids))
	for _, id := range ids {
		out = append(out, copyNode(r.nodes[id]))
	}
	return out, nil
}

func (r *memNodes) UpdateMetadata(_ context.Context, n *domain.Node) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	stored, ok := r.nodes[n.ID]
	if !ok {
		return domain.ErrNotFound
	}
	stored.DisplayName, stored.ServerAddress, stored.Flow, stored.Region = n.DisplayName, n.ServerAddress, n.Flow, n.Region
	stored.Tags, stored.SortOrder, stored.HideDirect, stored.EgressPanelID = append([]string(nil), n.Tags...), n.SortOrder, n.HideDirect, n.EgressPanelID
	stored.Relays = copyNode(n).Relays
	stored.TrafficFactorMilli = n.TrafficFactorMilli
	return nil
}

func (r *memNodes) UpdateEnabled(_ context.Context, id int64, enabled bool) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if n, ok := r.nodes[id]; ok {
		n.Enabled = enabled
		return nil
	}
	return domain.ErrNotFound
}

func (r *memNodes) UpdateInboundConfig(_ context.Context, n *domain.Node) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	stored, ok := r.nodes[n.ID]
	if !ok {
		return domain.ErrNotFound
	}
	stored.InboundListen, stored.InboundRemark, stored.InboundSettings = n.InboundListen, n.InboundRemark, n.InboundSettings
	stored.StreamSettings, stored.Sniffing, stored.DesiredPort, stored.DesiredProtocol = n.StreamSettings, n.Sniffing, n.DesiredPort, n.DesiredProtocol
	stored.ConfigSyncState = n.ConfigSyncState
	return nil
}

func (r *memNodes) Delete(_ context.Context, id int64) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.nodes[id]; !ok {
		return domain.ErrNotFound
	}
	delete(r.nodes, id)
	return nil
}

func (r *memNodes) child(landingID int64, lineID string) *domain.Node {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, n := range r.nodes {
		if n.ParentNodeID == landingID && n.ParentLineID == lineID {
			return copyNode(n)
		}
	}
	return nil
}

func (r *memNodes) children() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	count := 0
	for _, n := range r.nodes {
		if n.IsTunnelChild() {
			count++
		}
	}
	return count
}

// tunnelServerClient is the panel client of a native server: it hands out inbound ids
// and applies nothing itself.
type tunnelServerClient struct {
	ports.PanelClient
	mu   sync.Mutex
	next int
}

func (c *tunnelServerClient) AddInbound(context.Context, ports.InboundSpec) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.next++
	return c.next, nil
}
func (c *tunnelServerClient) ApplyIsAsynchronous() bool                                   { return true }
func (c *tunnelServerClient) UpdateInbound(context.Context, int, ports.InboundSpec) error { return nil }
func (c *tunnelServerClient) DelInbound(context.Context, int) error                       { return nil }
func (c *tunnelServerClient) SetInboundEnable(context.Context, int, bool) error           { return nil }

type tunnelServerPool struct {
	panels  []*domain.Panel
	clients map[int64]*tunnelServerClient
}

func newTunnelServerPool(ids ...int64) *tunnelServerPool {
	p := &tunnelServerPool{clients: map[int64]*tunnelServerClient{}}
	for _, id := range ids {
		p.panels = append(p.panels, &domain.Panel{ID: id, Kind: domain.PanelKindPSP})
		p.clients[id] = &tunnelServerClient{}
	}
	return p
}
func (p *tunnelServerPool) Get(id int64) (ports.PanelClient, error) {
	if c, ok := p.clients[id]; ok {
		return c, nil
	}
	return nil, ports.ErrPanelNotRegistered
}
func (p *tunnelServerPool) List() []*domain.Panel   { return p.panels }
func (p *tunnelServerPool) Add(*domain.Panel) error { return nil }
func (p *tunnelServerPool) Remove(int64) error      { return nil }

type linkRecorder struct {
	pairs, removed [][2]int64
	// plan are the ports PlanLink promises in turn (the last repeats); empty is 31001.
	plan  []int
	calls int
}

func (l *linkRecorder) RemoveLink(_ context.Context, entry, exit int64) error {
	l.removed = append(l.removed, [2]int64{entry, exit})
	return nil
}

// PlanLink promises port 31001 on the exit server to a pair that has no tunnel.
func (l *linkRecorder) PlanLink(context.Context, int64, int64) (int, bool, error) {
	port := 31001
	if len(l.plan) > 0 {
		port = l.plan[min(l.calls, len(l.plan)-1)]
	}
	l.calls++
	return port, false, nil
}

func (l *linkRecorder) PlanLinkPort(ctx context.Context, entry, exit int64, requested int, _ ...int) (int, bool, error) {
	if requested != 0 {
		return requested, false, nil
	}
	return l.PlanLink(ctx, entry, exit)
}

func (l *linkRecorder) EnsureLink(_ context.Context, entry, exit int64) (domain.TransitLink, error) {
	l.pairs = append(l.pairs, [2]int64{entry, exit})
	return domain.TransitLink{}, nil
}

type unclaimRecorder struct {
	InboundCleaner
	unclaimed []int
}

func (c *unclaimRecorder) UnclaimAllForInbound(_ context.Context, _ int64, inboundID int) error {
	c.unclaimed = append(c.unclaimed, inboundID)
	return nil
}
func (c *unclaimRecorder) EnsureInboundDeletable(context.Context, int64, int) error { return nil }

const landingReality = `{"network":"tcp","security":"reality","realitySettings":{"show":false,"xver":0,"target":"www.microsoft.com:443",` +
	`"serverNames":["www.microsoft.com"],"privateKey":"landingPriv","shortIds":["abcd"],"settings":{"publicKey":"landingPub","fingerprint":"firefox","spiderX":"/x"}}}`

type tunnelEnv struct {
	svc      *Service
	repo     *memNodes
	links    *linkRecorder
	cleaner  *unclaimRecorder
	resynced []string
	changed  int
}

// newTunnelEnv has a landing (id 1) on server 7 with a REALITY listener on 443,
// and servers 7, 9 and 10, all native. Server 9 has an inbound on 8443.
func newTunnelEnv(t *testing.T, landing func(*domain.Node)) *tunnelEnv {
	t.Helper()
	l := &domain.Node{ID: 1, PanelID: 7, InboundID: 1, DisplayName: "JP", ServerAddress: "land.example.com", Flow: "xtls-rprx-vision",
		Region: "JP", Tags: []string{"asia"}, DesiredProtocol: "vless", DesiredPort: 443, Enabled: true, Kind: domain.NodeKindReal,
		InboundSettings: `{"decryption":"none","fallbacks":[]}`, StreamSettings: landingReality, ConfigSyncState: domain.ConfigSyncSynced}
	if landing != nil {
		landing(l)
	}
	other := &domain.Node{ID: 2, PanelID: 9, InboundID: 1, DisplayName: "entry inbound", DesiredPort: 8443, Enabled: true, Kind: domain.NodeKindReal}
	env := &tunnelEnv{repo: newMemNodes(l, other), links: &linkRecorder{}, cleaner: &unclaimRecorder{}}
	env.svc = &Service{nodes: env.repo, pool: newTunnelServerPool(7, 9, 10), cleaner: env.cleaner}
	env.svc.SetTunnelLinks(env.links)
	env.svc.SetBackgroundRunner(func(name string, _ func(context.Context)) { env.resynced = append(env.resynced, name) })
	env.svc.SetAllowlistResyncer(func(context.Context, int64) { env.changed++ })
	return env
}

func (e *tunnelEnv) landing(t *testing.T) *domain.Node {
	t.Helper()
	n, err := e.repo.GetByID(context.Background(), 1)
	if err != nil {
		t.Fatal(err)
	}
	return n
}

func tunnelLine(entry int64, port int) domain.RelayLine {
	return domain.RelayLine{Name: "Hong Kong", Address: "hk.example.com", Port: port, Enabled: true, EntryPanelID: entry,
		Mode: domain.RelayModeTunnel, Region: "HK", Tags: []string{}}
}

// addTunnel saves the landing with one new tunnel line and returns its id.
func (e *tunnelEnv) addTunnel(t *testing.T, port int) string {
	t.Helper()
	n := e.landing(t)
	n.Relays = append(n.Relays, tunnelLine(9, port))
	if err := e.svc.UpdateMetadata(context.Background(), n); err != nil {
		t.Fatalf("adding the tunnel line: %v", err)
	}
	saved := e.landing(t)
	return saved.Relays[len(saved.Relays)-1].ID
}

func streamOf(t *testing.T, n *domain.Node) map[string]any {
	t.Helper()
	var stream map[string]any
	if err := json.Unmarshal([]byte(n.StreamSettings), &stream); err != nil {
		t.Fatal(err)
	}
	return stream
}

func realityOf(t *testing.T, n *domain.Node) map[string]any {
	t.Helper()
	reality, _ := streamOf(t, n)["realitySettings"].(map[string]any)
	if reality == nil {
		t.Fatalf("no REALITY settings in %s", n.StreamSettings)
	}
	return reality
}

func asRefusal(t *testing.T, err error) *front.Refusal {
	t.Helper()
	var refusal *front.Refusal
	if !errors.As(err, &refusal) {
		t.Fatalf("err = %v, want a confirmation refusal", err)
	}
	if refusal.Code != front.CodeConfirmRequired || refusal.Params["kind"] != "tunnel" {
		t.Fatalf("refusal = %+v", refusal)
	}
	return refusal
}

func confirmed(ctx context.Context) context.Context {
	return WithTunnelConfirmation(ctx, &TunnelConfirmation{Confirmed: true})
}

// A new tunnel line makes its listener: on the entry server, copied from the
// landing's REALITY listener with every secret and name fresh, leaving through
// the landing's server, with its port, names and keys stored on the listener.
func TestANewTunnelLineMakesItsListener(t *testing.T) {
	env := newTunnelEnv(t, nil)
	lineID := env.addTunnel(t, 0)

	landing := env.landing(t)
	line := landing.Relays[0]
	child := env.repo.child(1, lineID)
	if child == nil || line.TunnelNodeID != child.ID || !line.Tunnel() || line.Port != 0 {
		t.Fatalf("line = %+v child = %+v", line, child)
	}
	if child.PanelID != 9 || child.EgressPanelID != 7 || !child.Enabled || child.Kind != domain.NodeKindReal {
		t.Fatalf("listener = %+v, want a listener on server 9 leaving through server 7", child)
	}
	if child.DesiredProtocol != "vless" || child.Flow != "xtls-rprx-vision" || child.ServerAddress != "hk.example.com" {
		t.Fatalf("listener protocol/flow/address = %s/%s/%s", child.DesiredProtocol, child.Flow, child.ServerAddress)
	}
	if child.DisplayName != "JP · Hong Kong" {
		t.Fatalf("listener name = %q", child.DisplayName)
	}
	if child.DesiredPort < 20000 || child.DesiredPort > 59999 || child.DesiredPort == 8443 {
		t.Fatalf("port = %d, want a free port in the pick range", child.DesiredPort)
	}
	reality, own := realityOf(t, child), realityOf(t, landing)
	if reality["privateKey"] == own["privateKey"] || reality["privateKey"] == "" || reality["privateKey"] == nil {
		t.Fatalf("private key = %v: the landing's key must not be reused", reality["privateKey"])
	}
	settings, _ := reality["settings"].(map[string]any)
	if settings["publicKey"] == "landingPub" || settings["publicKey"] == "" {
		t.Fatalf("public key = %v", settings["publicKey"])
	}
	if settings["fingerprint"] != "firefox" || settings["spiderX"] != "/x" {
		t.Fatalf("the landing's own REALITY options were not copied: %v", settings)
	}
	if ids, _ := reality["shortIds"].([]any); len(ids) != 1 || ids[0] == "abcd" {
		t.Fatalf("short ids = %v", reality["shortIds"])
	}
	if names, _ := reality["serverNames"].([]any); len(names) != 1 || names[0] == "www.microsoft.com" {
		t.Fatalf("server names = %v: the landing's name must not be reused", reality["serverNames"])
	}
	if len(env.links.pairs) != 1 || env.links.pairs[0] != [2]int64{9, 7} {
		t.Fatalf("tunnels made = %v, want 9→7", env.links.pairs)
	}
	if len(env.resynced) != 1 {
		t.Fatalf("members were not re-planned: %v", env.resynced)
	}
	// The listener is no node of its own for anyone: the landing lists it.
	visible, err := env.svc.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, n := range visible {
		if n.IsTunnelChild() {
			t.Fatal("a tunnel listener is in the administrator's node list")
		}
	}
}

func TestATunnelLineOfANonRealityLandingGetsTheDefaultListener(t *testing.T) {
	env := newTunnelEnv(t, func(n *domain.Node) {
		n.DesiredProtocol, n.Flow = "trojan", ""
		n.StreamSettings = `{"network":"tcp","security":"tls","tlsSettings":{"serverName":"land.example.com","certificates":[{"certificateFile":"/c","keyFile":"/k"}]}}`
	})
	lineID := env.addTunnel(t, 0)
	child := env.repo.child(1, lineID)
	if child.DesiredProtocol != "vless" || child.Flow != "xtls-rprx-vision" {
		t.Fatalf("listener = %s/%s, want the default VLESS + REALITY + Vision", child.DesiredProtocol, child.Flow)
	}
	stream := streamOf(t, child)
	if stream["security"] != "reality" || strings.Contains(child.StreamSettings, "/c") {
		t.Fatalf("stream = %s: the landing's certificate must not be carried to the entry server", child.StreamSettings)
	}
	if realityOf(t, child)["privateKey"] == "" {
		t.Fatal("no REALITY key")
	}
}

func TestATunnelLinePortConflictBlocksTheSaveAndNamesTheObject(t *testing.T) {
	env := newTunnelEnv(t, nil)
	n := env.landing(t)
	n.Relays = []domain.RelayLine{tunnelLine(9, 8443)} // the inbound of the entry server's node
	err := env.svc.UpdateMetadata(context.Background(), n)
	if err == nil || !errors.Is(err, domain.ErrValidation) || !strings.Contains(err.Error(), "8443") || !strings.Contains(err.Error(), "entry inbound") {
		t.Fatalf("err = %v, want a validation error naming port 8443 and the node", err)
	}
	if env.repo.children() != 0 {
		t.Fatal("a listener was made for a refused save")
	}
	if got := env.landing(t); len(got.Relays) != 0 {
		t.Fatal("a refused save stored the line")
	}
}

func TestATunnelLineNeedsAnEntryOnAnotherNativeServer(t *testing.T) {
	for name, test := range map[string]struct {
		entry int64
		want  string
	}{
		"the landing's own server": {7, "own server"},
		"an unknown server":        {55, "Passwall Node"},
	} {
		env := newTunnelEnv(t, nil)
		n := env.landing(t)
		n.Relays = []domain.RelayLine{tunnelLine(test.entry, 0)}
		if err := env.svc.UpdateMetadata(context.Background(), n); err == nil || !strings.Contains(err.Error(), test.want) {
			t.Errorf("%s: err = %v, want %q", name, err, test.want)
		}
		if env.repo.children() != 0 {
			t.Errorf("%s: a listener was made", name)
		}
	}
}

// A line's own fields follow it to the listener, and nothing else about the
// listener is touched by a save of the line.
func TestTheListenerFollowsItsLineAndItsLanding(t *testing.T) {
	env := newTunnelEnv(t, nil)
	lineID := env.addTunnel(t, 0)
	before := env.repo.child(1, lineID)

	n := env.landing(t)
	n.Relays[0].Name, n.Relays[0].Address = "Kowloon", "kl.example.com"
	factor := 2500
	n.Relays[0].TrafficFactorMilli = &factor
	if err := env.svc.UpdateMetadata(context.Background(), n); err != nil {
		t.Fatal(err)
	}
	after := env.repo.child(1, lineID)
	if after.DisplayName != "JP · Kowloon" || after.ServerAddress != "kl.example.com" || after.EffectiveTrafficFactor() != 2500 {
		t.Fatalf("listener = %+v, want the line's name, address and factor", after)
	}
	if after.DesiredPort != before.DesiredPort || after.StreamSettings != before.StreamSettings || after.ID != before.ID {
		t.Fatal("a save of the line changed the listener's own settings")
	}
	// Disabling the line disables the listener; enabling the line enables it.
	n = env.landing(t)
	n.Relays[0].Enabled = false
	if err := env.svc.UpdateMetadata(context.Background(), n); err != nil {
		t.Fatal(err)
	}
	if env.repo.child(1, lineID).Enabled {
		t.Fatal("a disabled line left its listener enabled")
	}
	n = env.landing(t)
	n.Relays[0].Enabled = true
	if err := env.svc.UpdateMetadata(context.Background(), n); err != nil {
		t.Fatal(err)
	}
	if !env.repo.child(1, lineID).Enabled {
		t.Fatal("an enabled line left its listener disabled")
	}
}

// Disabling the landing disables its lines' listeners, and enabling it enables
// only those of the lines that are on.
func TestDisablingTheLandingDisablesItsTunnelListeners(t *testing.T) {
	env := newTunnelEnv(t, nil)
	on := env.addTunnel(t, 0)
	n := env.landing(t)
	n.Relays = append(n.Relays, tunnelLine(10, 0))
	n.Relays[1].Name, n.Relays[1].Enabled = "off", false
	if err := env.svc.UpdateMetadata(context.Background(), n); err != nil {
		t.Fatal(err)
	}
	off := env.landing(t).Relays[1].ID
	if env.repo.child(1, off).Enabled {
		t.Fatal("a disabled line's listener is enabled")
	}
	ctx := context.Background()
	if err := env.svc.SetEnabled(ctx, 1, false); err != nil {
		t.Fatal(err)
	}
	if env.repo.child(1, on).Enabled || env.repo.child(1, off).Enabled {
		t.Fatal("a listener survived its landing being disabled")
	}
	if err := env.svc.SetEnabled(ctx, 1, true); err != nil {
		t.Fatal(err)
	}
	if !env.repo.child(1, on).Enabled || env.repo.child(1, off).Enabled {
		t.Fatalf("after enabling the landing: on=%v off=%v, want only the line that is on", env.repo.child(1, on).Enabled, env.repo.child(1, off).Enabled)
	}
}

func TestAListenerIsNotEnabledDisabledOrDeletedOnItsOwn(t *testing.T) {
	env := newTunnelEnv(t, nil)
	lineID := env.addTunnel(t, 0)
	child := env.repo.child(1, lineID)
	ctx := context.Background()
	for name, err := range map[string]error{
		"disable": env.svc.SetEnabled(ctx, child.ID, false),
		"delete":  env.svc.DeleteAndSync(ctx, child.ID),
		"detach":  env.svc.DetachAndSync(ctx, child.ID),
	} {
		if err == nil || !errors.Is(err, domain.ErrValidation) || !strings.Contains(err.Error(), "tunnel line") {
			t.Errorf("%s: err = %v, want a refusal pointing at the line", name, err)
		}
	}
	if env.repo.child(1, lineID) == nil {
		t.Fatal("the listener went")
	}
}

// A change of mode only happens through the confirmed switch.
func TestTheModeOfALineChangesOnlyThroughTheSwitch(t *testing.T) {
	env := newTunnelEnv(t, nil)
	n := env.landing(t)
	n.Relays = []domain.RelayLine{{Name: "fwd", Address: "e.example.com", Port: 20001, Enabled: true, EntryPanelID: 9, Region: "JP", Tags: []string{}}}
	if err := env.svc.UpdateMetadata(context.Background(), n); err != nil {
		t.Fatal(err)
	}
	n = env.landing(t)
	n.Relays[0].Mode = domain.RelayModeTunnel // a save that tries to switch
	err := env.svc.UpdateMetadata(context.Background(), n)
	if err == nil || !errors.Is(err, domain.ErrValidation) || !strings.Contains(err.Error(), "mode switch") {
		t.Fatalf("err = %v, want a refusal that points at the mode switch", err)
	}
	if env.repo.children() != 0 || env.landing(t).Relays[0].Tunnel() {
		t.Fatal("the mode changed without the switch")
	}
	// A save that does not name the mode keeps it.
	n = env.landing(t)
	n.Relays[0].Mode, n.Relays[0].ModeUnresolved = "", true
	n.Relays[0].Name = "renamed"
	if err := env.svc.UpdateMetadata(context.Background(), n); err != nil {
		t.Fatal(err)
	}
	if got := env.landing(t).Relays[0]; got.Name != "renamed" || got.Tunnel() {
		t.Fatalf("line = %+v", got)
	}
}

func TestSwitchingAForwardToATunnelAsksFirstAndListsWhatItDoes(t *testing.T) {
	env := newTunnelEnv(t, nil)
	n := env.landing(t)
	n.Relays = []domain.RelayLine{{Name: "fwd", Address: "e.example.com", Port: 20001, Enabled: true, EntryPanelID: 9, Region: "JP", Tags: []string{}}}
	if err := env.svc.UpdateMetadata(context.Background(), n); err != nil {
		t.Fatal(err)
	}
	lineID := env.landing(t).Relays[0].ID
	ctx := context.Background()

	_, err := env.svc.SwitchLineMode(ctx, 1, lineID, domain.RelayModeTunnel, 0)
	refusal := asRefusal(t, err)
	if refusal.Params["action"] != "switch_mode" || refusal.Params["refresh_needed"] != true {
		t.Fatalf("params = %+v", refusal.Params)
	}
	if creates, _ := refusal.Params["creates"].([]TunnelItem); len(creates) != 1 || creates[0].LineID != lineID {
		t.Fatalf("creates = %+v", refusal.Params["creates"])
	}
	if env.repo.children() != 0 || env.landing(t).Relays[0].Tunnel() {
		t.Fatal("an unconfirmed switch changed something")
	}

	got, err := env.svc.SwitchLineMode(askedAndConfirmed(t, env, ctx, lineID, domain.RelayModeTunnel, 0), 1, lineID, domain.RelayModeTunnel, 0)
	if err != nil {
		t.Fatal(err)
	}
	line := got.Relays[0]
	child := env.repo.child(1, lineID)
	if !line.Tunnel() || line.ID != lineID || line.Name != "fwd" || line.Port != 0 || child == nil || line.TunnelNodeID != child.ID {
		t.Fatalf("line = %+v, child = %+v", line, child)
	}
	// The forward's own port is free for the listener, which the switch replaced.
	if line.Managed() {
		t.Fatal("the line is still a managed forward")
	}
}

func TestSwitchingATunnelBackToAForwardRemovesItsListenerAfterConfirmation(t *testing.T) {
	env := newTunnelEnv(t, nil)
	lineID := env.addTunnel(t, 0)
	child := env.repo.child(1, lineID)
	ctx := context.Background()

	_, err := env.svc.SwitchLineMode(ctx, 1, lineID, "", 20123)
	refusal := asRefusal(t, err)
	removes, _ := refusal.Params["removes"].([]TunnelItem)
	if refusal.Params["action"] != "switch_mode" || len(removes) != 1 || removes[0].NodeID != child.ID ||
		removes[0].Port != child.DesiredPort || removes[0].Name != child.DisplayName {
		t.Fatalf("refusal = %+v: it must name the listener it removes and its port", refusal.Params)
	}
	if !strings.Contains(refusal.Message, child.DisplayName) {
		t.Fatalf("message %q does not name the listener", refusal.Message)
	}
	if env.repo.child(1, lineID) == nil {
		t.Fatal("an unconfirmed switch removed the listener")
	}

	got, err := env.svc.SwitchLineMode(askedAndConfirmed(t, env, ctx, lineID, "", 20123), 1, lineID, "", 20123)
	if err != nil {
		t.Fatal(err)
	}
	line := got.Relays[0]
	if line.Tunnel() || !line.Managed() || line.Port != 20123 || line.TunnelNodeID != 0 || line.ID != lineID {
		t.Fatalf("line = %+v, want a forward on 20123 with the same id", line)
	}
	if env.repo.child(1, lineID) != nil {
		t.Fatal("the listener survived")
	}
	if len(env.cleaner.unclaimed) != 1 {
		t.Fatalf("the listener's ownership was not released: %v", env.cleaner.unclaimed)
	}
}

func TestRemovingATunnelLineRemovesItsListenerAfterConfirmation(t *testing.T) {
	env := newTunnelEnv(t, nil)
	lineID := env.addTunnel(t, 0)
	ctx := context.Background()

	n := env.landing(t)
	n.Relays = nil
	err := env.svc.UpdateMetadata(ctx, n)
	refusal := asRefusal(t, err)
	if refusal.Params["action"] != "remove_line" {
		t.Fatalf("action = %v", refusal.Params["action"])
	}
	if env.repo.child(1, lineID) == nil || len(env.landing(t).Relays) != 1 {
		t.Fatal("an unconfirmed removal changed something")
	}
	n = env.landing(t)
	n.Relays = nil
	if err := env.svc.UpdateMetadata(confirmed(ctx), n); err != nil {
		t.Fatal(err)
	}
	if env.repo.child(1, lineID) != nil || len(env.landing(t).Relays) != 0 {
		t.Fatal("the listener or the line is still there")
	}
}

func TestDeletingTheLandingRemovesItsListenersAfterConfirmation(t *testing.T) {
	env := newTunnelEnv(t, nil)
	lineID := env.addTunnel(t, 0)
	ctx := context.Background()

	err := env.svc.DetachAndSync(ctx, 1)
	refusal := asRefusal(t, err)
	removes, _ := refusal.Params["removes"].([]TunnelItem)
	if refusal.Params["action"] != "delete_node" || len(removes) != 1 || removes[0].Name != "JP · Hong Kong" {
		t.Fatalf("refusal = %+v", refusal.Params)
	}
	if env.repo.child(1, lineID) == nil || env.landing(t) == nil {
		t.Fatal("an unconfirmed delete removed something")
	}
	if err := env.svc.DetachAndSync(confirmed(ctx), 1); err != nil {
		t.Fatal(err)
	}
	if env.repo.children() != 0 {
		t.Fatal("the landing's listeners outlived it")
	}
	if _, err := env.repo.GetByID(ctx, 1); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("landing still there: %v", err)
	}
}

// Deleting through the queued path asks first too, before anything is
// disabled.
func TestAskingToDeleteTheLandingLeavesItEnabledUntilConfirmed(t *testing.T) {
	env := newTunnelEnv(t, nil)
	env.addTunnel(t, 0)
	asRefusal(t, env.svc.DeleteAndSync(context.Background(), 1))
	if !env.landing(t).Enabled {
		t.Fatal("the landing was disabled by a refused delete")
	}
}

func TestEditingTheListenerChecksItsPortAndNamesAndKeepsWhatIsNotSent(t *testing.T) {
	env := newTunnelEnv(t, nil)
	lineID := env.addTunnel(t, 0)
	child := env.repo.child(1, lineID)
	ctx := context.Background()

	err := env.svc.UpdateTunnelListener(ctx, 1, lineID, TunnelListenerEdit{Port: 8443})
	if err == nil || !errors.Is(err, domain.ErrValidation) || !strings.Contains(err.Error(), "entry inbound") {
		t.Fatalf("err = %v, want the port named as taken by the entry inbound", err)
	}
	if err := env.svc.UpdateTunnelListener(ctx, 1, lineID, TunnelListenerEdit{Port: 24444, ServerName: "WWW.Example.COM", ShortID: "AB12"}); err != nil {
		t.Fatal(err)
	}
	got := env.repo.child(1, lineID)
	reality := realityOf(t, got)
	if got.DesiredPort != 24444 {
		t.Fatalf("port = %d", got.DesiredPort)
	}
	if names, _ := reality["serverNames"].([]any); len(names) != 1 || names[0] != "www.example.com" {
		t.Fatalf("server names = %v", reality["serverNames"])
	}
	if ids, _ := reality["shortIds"].([]any); len(ids) != 1 || ids[0] != "ab12" {
		t.Fatalf("short ids = %v", reality["shortIds"])
	}
	if reality["target"] != "www.example.com:443" {
		t.Fatalf("target = %v, want it to follow the name when only the name is given", reality["target"])
	}
	if reality["privateKey"] != realityOf(t, child)["privateKey"] {
		t.Fatal("an edit that did not name the key changed it")
	}
	// It is the new settings that wait to be applied.
	if got.ConfigSyncState == domain.ConfigSyncSynced {
		t.Fatalf("an edited listener reads as applied: %q", got.ConfigSyncState)
	}
}

func TestReCopyingFromTheLandingShowsTheDiffAndKeepsTheListenersOwnSecrets(t *testing.T) {
	env := newTunnelEnv(t, nil)
	lineID := env.addTunnel(t, 0)
	before := env.repo.child(1, lineID)
	ctx := context.Background()

	// The landing changes its flow and fingerprint.
	n := env.landing(t)
	n.Flow = ""
	n.StreamSettings = strings.Replace(n.StreamSettings, `"fingerprint":"firefox"`, `"fingerprint":"safari"`, 1)
	env.repo.nodes[1] = copyNode(n)

	diff, err := env.svc.PreviewTunnelSync(ctx, 1, lineID)
	if err != nil {
		t.Fatal(err)
	}
	fields := map[string]TunnelSyncDiff{}
	for _, d := range diff {
		fields[d.Field] = d
	}
	if fields["flow"].To != "" || fields["flow"].From != "xtls-rprx-vision" || fields["stream_settings"].Field == "" {
		t.Fatalf("diff = %+v", diff)
	}
	for _, d := range diff {
		if strings.Contains(d.From+d.To, "landingPriv") || strings.Contains(d.From+d.To, realityOf(t, before)["privateKey"].(string)) {
			t.Fatalf("the diff carries a private key: %+v", d)
		}
	}
	if env.repo.child(1, lineID).Flow != "xtls-rprx-vision" {
		t.Fatal("the preview changed the listener")
	}
	// Without confirmation it refuses, with the same diff.
	_, err = env.svc.SyncTunnelFromLanding(ctx, 1, lineID)
	refusal := asRefusal(t, err)
	if refusal.Params["action"] != "sync_from_landing" {
		t.Fatalf("params = %+v", refusal.Params)
	}
	if _, err := env.svc.SyncTunnelFromLanding(syncConfirmed(t, env, ctx, lineID), 1, lineID); err != nil {
		t.Fatal(err)
	}
	after := env.repo.child(1, lineID)
	if after.Flow != "" || !strings.Contains(after.StreamSettings, `"fingerprint":"safari"`) {
		t.Fatalf("the landing's settings were not copied: flow %q stream %s", after.Flow, after.StreamSettings)
	}
	if after.DesiredPort != before.DesiredPort || realityOf(t, after)["privateKey"] != realityOf(t, before)["privateKey"] ||
		strings.Contains(after.StreamSettings, "landingPriv") || strings.Contains(after.StreamSettings, "landingPub") ||
		strings.Contains(after.StreamSettings, `"www.microsoft.com"`) {
		t.Fatalf("the re-copy replaced the listener's own port, names or keys: %s", after.StreamSettings)
	}
}

func TestAnUncopyableLandingCannotBeReCopied(t *testing.T) {
	env := newTunnelEnv(t, nil)
	lineID := env.addTunnel(t, 0)
	n := env.landing(t)
	n.DesiredProtocol = "shadowsocks"
	env.repo.nodes[1] = copyNode(n)
	if _, err := env.svc.PreviewTunnelSync(context.Background(), 1, lineID); err == nil || !errors.Is(err, domain.ErrValidation) {
		t.Fatalf("err = %v", err)
	}
}

func TestATunnelLineNeedsTheLandingToExistFirst(t *testing.T) {
	env := newTunnelEnv(t, nil)
	n := &domain.Node{DisplayName: "new", Region: "JP", PanelID: 7, Relays: []domain.RelayLine{tunnelLine(9, 0)}}
	err := env.svc.CreateInbound(context.Background(), n, ports.InboundSpec{Port: 4443, Protocol: "vless"})
	if err == nil || !errors.Is(err, domain.ErrValidation) || !strings.Contains(err.Error(), "tunnel line") {
		t.Fatalf("err = %v", err)
	}
}

// The listener of a tunnel line is an ordinary listener to the shared ports of
// its server: a port a front holds is refused, naming it, until the line is
// given a port of its own or the front is edited.
func TestATunnelListenerOnASharedPortIsHeldToTheFrontsRules(t *testing.T) {
	env := newTunnelEnv(t, nil)
	env.svc.SetFronts(fixedFronts{rows: []domain.TransitFront{{ID: 1, PanelID: 9, Port: 24443,
		Listeners: []domain.FrontListener{{NodeID: 2, LoopbackPort: 30001}}}}}, nil)
	n := env.landing(t)
	n.Relays = []domain.RelayLine{tunnelLine(9, 24443)}
	err := env.svc.UpdateMetadata(context.Background(), n)
	if err == nil || !errors.Is(err, domain.ErrValidation) || !strings.Contains(err.Error(), "shared port") || !strings.Contains(err.Error(), "24443") {
		t.Fatalf("err = %v, want a refusal naming the shared port", err)
	}
	if env.repo.children() != 0 || len(env.landing(t).Relays) != 0 {
		t.Fatal("a refused save left something behind")
	}
	// A loopback port of a fronted listener is a taken port too.
	n = env.landing(t)
	n.Relays = []domain.RelayLine{tunnelLine(9, 30001)}
	if err := env.svc.UpdateMetadata(context.Background(), n); err == nil || !strings.Contains(err.Error(), "loopback") {
		t.Fatalf("err = %v, want the loopback port named", err)
	}
}

// A listener does not read as applied until the entry server confirms its
// settings, so a line is never published on a listener that is not running.
func TestANewListenerWaitsForTheEntryServerToApplyIt(t *testing.T) {
	env := newTunnelEnv(t, nil)
	lineID := env.addTunnel(t, 0)
	if state := env.repo.child(1, lineID).ConfigSyncState; state == domain.ConfigSyncSynced {
		t.Fatalf("a new listener reads as applied: %q", state)
	}
}

// A listener behind a shared port asks before it is disabled, and the question
// is asked before anything is saved: declining leaves the landing and every
// listener as they were.
func TestDisablingATunnelListenerBehindASharedPortAsksBeforeChangingAnything(t *testing.T) {
	env := newTunnelEnv(t, nil)
	lineID := env.addTunnel(t, 0)
	child := env.repo.child(1, lineID)
	names := realityOf(t, child)["serverNames"].([]any)
	fronts := &joinFronts{fixedFronts{rows: []domain.TransitFront{{
		ID: 1, PanelID: 9, Port: child.DesiredPort,
		Listeners: []domain.FrontListener{{NodeID: child.ID, LoopbackPort: 30001}},
		Routes:    []domain.FrontRoute{{ServerNames: []string{names[0].(string)}, Target: domain.FrontTarget{Kind: domain.FrontTargetListener, NodeID: child.ID}}},
		Default:   domain.FrontTarget{Kind: domain.FrontTargetExternal, Host: "www.example.com", Port: 443},
	}}}}
	all, _ := env.repo.List(context.Background())
	env.svc.SetFronts(fronts, front.New(fronts, nodeList(all), panelOK{}, agentOK{}, noForwards{}, nil, nil))
	ctx := context.Background()

	for name, err := range map[string]error{
		"disable the landing": env.svc.SetEnabled(ctx, 1, false),
		"disable the line": func() error {
			n := env.landing(t)
			n.Relays[0].Enabled = false
			return env.svc.UpdateMetadata(ctx, n)
		}(),
	} {
		var refusal *front.Refusal
		if !errors.As(err, &refusal) || refusal.Code != front.CodeConfirmRequired {
			t.Fatalf("%s: err = %v, want the shared port's confirmation", name, err)
		}
		if !env.landing(t).Enabled || !env.landing(t).Relays[0].Enabled || !env.repo.child(1, lineID).Enabled {
			t.Fatalf("%s: a declined confirmation changed something", name)
		}
	}
	confirmedFront := front.WithConfirmation(ctx, &front.Confirmation{Confirmed: true})
	if err := env.svc.SetEnabled(confirmedFront, 1, false); err != nil {
		t.Fatal(err)
	}
	if env.landing(t).Enabled || env.repo.child(1, lineID).Enabled {
		t.Fatal("a confirmed disable left something enabled")
	}
}

// A REALITY private key whose public key is not the one it derives is refused:
// a key pair that does not match gives clients a key that can never connect.
func TestAMismatchedRealityKeyPairIsRefused(t *testing.T) {
	env := newTunnelEnv(t, nil)
	lineID := env.addTunnel(t, 0)
	before := env.repo.child(1, lineID)
	ctx := context.Background()
	priv, pub, err := realitykey.GenerateKeypair()
	if err != nil {
		t.Fatal(err)
	}
	// A new private key beside the old public key.
	if err := env.svc.UpdateTunnelListener(ctx, 1, lineID, TunnelListenerEdit{PrivateKey: priv}); err == nil ||
		!errors.Is(err, domain.ErrValidation) || !strings.Contains(err.Error(), "does not match") {
		t.Fatalf("err = %v", err)
	}
	if err := env.svc.UpdateTunnelListener(ctx, 1, lineID, TunnelListenerEdit{PrivateKey: "not-a-key"}); err == nil {
		t.Fatal("a malformed private key was accepted")
	}
	if env.repo.child(1, lineID).StreamSettings != before.StreamSettings {
		t.Fatal("a refused edit changed the listener")
	}
	if err := env.svc.UpdateTunnelListener(ctx, 1, lineID, TunnelListenerEdit{PrivateKey: priv, PublicKey: pub}); err != nil {
		t.Fatalf("a matching pair was refused: %v", err)
	}
}

// What a confirmation lists is what is then made: the listener's port on the
// entry server and the tunnel's port on the landing's server, and the retry that
// is confirmed makes the listener on exactly the port that was listed.
func TestAConfirmationListsThePortsThatOpenAndThenTheyAreTheOnesMade(t *testing.T) {
	env := newTunnelEnv(t, nil)
	n := env.landing(t)
	n.Relays = []domain.RelayLine{{Name: "fwd", Address: "e.example.com", Port: 20001, Enabled: true, EntryPanelID: 9, Region: "JP", Tags: []string{}}}
	if err := env.svc.UpdateMetadata(context.Background(), n); err != nil {
		t.Fatal(err)
	}
	lineID := env.landing(t).Relays[0].ID
	ctx := context.Background()

	_, err := env.svc.SwitchLineMode(ctx, 1, lineID, domain.RelayModeTunnel, 0)
	creates, _ := asRefusal(t, err).Params["creates"].([]TunnelItem)
	if len(creates) != 1 || creates[0].Port < 20000 || creates[0].EntryPanelID != 9 || creates[0].ExitPanelID != 7 ||
		creates[0].LinkPort != 31001 || !creates[0].LinkNew {
		t.Fatalf("creates = %+v, want the listener's port on server 9 and the tunnel's on server 7", creates)
	}
	// Asked again, the same line is offered the same port.
	_, err = env.svc.SwitchLineMode(ctx, 1, lineID, domain.RelayModeTunnel, 0)
	if again, _ := asRefusal(t, err).Params["creates"].([]TunnelItem); again[0].Port != creates[0].Port {
		t.Fatalf("the preview changed its port: %d then %d", creates[0].Port, again[0].Port)
	}
	if _, err := env.svc.SwitchLineMode(askedAndConfirmed(t, env, ctx, lineID, domain.RelayModeTunnel, 0), 1, lineID, domain.RelayModeTunnel, 0); err != nil {
		t.Fatal(err)
	}
	if got := env.repo.child(1, lineID).DesiredPort; got != creates[0].Port {
		t.Fatalf("made on port %d, but %d was listed", got, creates[0].Port)
	}
}

// A hidden listener whose line is gone can be removed by itself, and one that
// still has a line cannot.
func TestAnOrphanListenerCanBeRemovedAndAnOwnedOneCannot(t *testing.T) {
	env := newTunnelEnv(t, nil)
	owned := env.addTunnel(t, 0)
	ownedChild := env.repo.child(1, owned)
	ctx := context.Background()
	if err := env.svc.DetachAndSync(ctx, ownedChild.ID); err == nil || !strings.Contains(err.Error(), "remove the line") {
		t.Fatalf("err = %v", err)
	}
	// A cut-short delete left the listener behind: its line is not on the landing.
	n := env.landing(t)
	n.Relays = nil
	env.repo.nodes[1] = copyNode(n)
	if err := env.svc.DetachAndSync(ctx, ownedChild.ID); err != nil {
		t.Fatalf("an orphan could not be removed: %v", err)
	}
	if env.repo.children() != 0 {
		t.Fatal("the orphan is still there")
	}
}

// Removal comes before the line is saved, so that a save cut short can leave a
// line without its listener, never a listener without its line.
func TestAFailedSaveAfterRemovingATunnelLineLeavesNoOrphanListener(t *testing.T) {
	env := newTunnelEnv(t, nil)
	env.addTunnel(t, 0)
	failing := &failingMetadata{memNodes: env.repo}
	env.svc.nodes = failing
	n := env.landing(t)
	n.Relays = nil
	failing.fail = true
	if err := env.svc.UpdateMetadata(confirmed(context.Background()), n); err == nil {
		t.Fatal("the save did not fail")
	}
	if env.repo.children() != 0 {
		t.Fatal("a listener was left behind with no line to own it")
	}
	if got := env.landing(t).Relays; len(got) != 1 || got[0].Tunnel() == false {
		t.Fatalf("lines = %+v: the line stays, withheld without its listener, until it is removed again", got)
	}
}

type failingMetadata struct {
	*memNodes
	fail bool
	// only, when set, makes just that node's metadata write fail; otherwise every
	// write of a landing does.
	only int64
}

func (f *failingMetadata) UpdateMetadata(ctx context.Context, n *domain.Node) error {
	if f.fail && ((f.only == 0 && !n.IsTunnelChild()) || (f.only != 0 && n.ID == f.only)) {
		return errors.New("database unavailable")
	}
	return f.memNodes.UpdateMetadata(ctx, n)
}

// A removed listener's own routes by destination free their tunnels like a
// deleted node's do.
func TestRemovingATunnelListenerCleansUpTheTunnelsOnlyItsRoutesUsed(t *testing.T) {
	env := newTunnelEnv(t, nil)
	lineID := env.addTunnel(t, 0)
	child := env.repo.child(1, lineID)
	env.repo.nodes[child.ID].DestRoutes = []domain.DestRoute{{ID: "r1", Target: domain.DestRouteTarget{Kind: domain.DestRouteServer, PanelID: 10}}}
	var cleaned []int64
	env.svc.SetRouteLinks(func(_ context.Context, n *domain.Node) error { cleaned = append(cleaned, n.ID); return nil }, nil)
	n := env.landing(t)
	n.Relays = nil
	if err := env.svc.UpdateMetadata(confirmed(context.Background()), n); err != nil {
		t.Fatal(err)
	}
	if len(cleaned) != 1 || cleaned[0] != child.ID {
		t.Fatalf("route cleanup ran for %v, want the removed listener %d", cleaned, child.ID)
	}
}

const landingWithBinding = `{"network":"tcp","security":"reality","tcpSettings":{"acceptProxyProtocol":true,"header":{"type":"none"}},` +
	`"sockopt":{"acceptProxyProtocol":true},"realitySettings":{"show":false,"xver":1,"target":"www.microsoft.com:443",` +
	`"serverNames":["www.microsoft.com"],"privateKey":"landingPriv","mldsa65Seed":"seed-secret","shortIds":["abcd"],` +
	`"settings":{"publicKey":"landingPub","fingerprint":"firefox","mldsa65Verify":"verify-secret"}}}`

// The listener is the entry's own: the landing's bind address, allocation,
// PROXY protocol, REALITY xver and post-quantum material are not carried over.
func TestTheListenerDoesNotInheritTheLandingsBindingOrSecrets(t *testing.T) {
	env := newTunnelEnv(t, func(n *domain.Node) {
		n.StreamSettings, n.InboundListen, n.Allocate = landingWithBinding, "10.0.0.5", `{"strategy":"always"}`
		n.InboundSettings = `{"decryption":"none","clients":[{"id":"landing-user-id","email":"x"}]}`
	})
	lineID := env.addTunnel(t, 0)
	child := env.repo.child(1, lineID)
	if child.InboundListen != "" || child.Allocate != "" {
		t.Fatalf("listen %q allocate %q: the entry listens on every address by default", child.InboundListen, child.Allocate)
	}
	for _, leaked := range []string{"seed-secret", "verify-secret", "landing-user-id", `"acceptProxyProtocol":true`, `"xver":1`} {
		if strings.Contains(child.StreamSettings+child.InboundSettings, leaked) {
			t.Fatalf("%s was carried to the entry's listener: %s %s", leaked, child.StreamSettings, child.InboundSettings)
		}
	}
}

// A re-copy blanks the same material, and neither its diff nor the audit row
// built from it carries a secret.
func TestReCopyingBlanksTheLandingsSecretsAndNeverShowsOne(t *testing.T) {
	env := newTunnelEnv(t, nil)
	lineID := env.addTunnel(t, 0)
	before := env.repo.child(1, lineID)
	n := env.landing(t)
	n.StreamSettings = landingWithBinding
	n.InboundSettings = `{"decryption":"none","clients":[{"id":"landing-user-id"}]}`
	env.repo.nodes[1] = copyNode(n)
	ctx := context.Background()

	diff, err := env.svc.PreviewTunnelSync(ctx, 1, lineID)
	if err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(diff)
	own := realityOf(t, before)["privateKey"].(string)
	for _, secret := range []string{"landingPriv", "seed-secret", "verify-secret", "landing-user-id", own} {
		if strings.Contains(string(encoded), secret) {
			t.Fatalf("the diff carries %q: %s", secret, encoded)
		}
	}
	if _, err := env.svc.SyncTunnelFromLanding(syncConfirmed(t, env, ctx, lineID), 1, lineID); err != nil {
		t.Fatal(err)
	}
	after := env.repo.child(1, lineID)
	for _, leaked := range []string{"seed-secret", "verify-secret", "landing-user-id", `"acceptProxyProtocol":true`, `"xver":1`} {
		if strings.Contains(after.StreamSettings+after.InboundSettings, leaked) {
			t.Fatalf("%s was copied: %s", leaked, after.StreamSettings)
		}
	}
	if after.InboundListen != before.InboundListen || realityOf(t, after)["privateKey"] != realityOf(t, before)["privateKey"] {
		t.Fatal("the re-copy changed the listener's own address or key")
	}
}

// A listener's factor is its line's: setting the listener's own direct factor is
// refused, and a failure to carry a line's factor to its listener is returned.
func TestALineFactorIsSetOnTheLineNotOnItsListener(t *testing.T) {
	env := newTunnelEnv(t, nil)
	lineID := env.addTunnel(t, 0)
	child := env.repo.child(1, lineID)
	ctx := context.Background()
	_, err := env.svc.SetLineFactors(ctx, 9, []LineFactorChange{{Line: domain.DirectLine(child.ID), FactorMilli: 2000}})
	if err == nil || !errors.Is(err, domain.ErrValidation) || !strings.Contains(err.Error(), "tunnel line") {
		t.Fatalf("err = %v", err)
	}
	applied, err := env.svc.SetLineFactors(ctx, 7, []LineFactorChange{{Line: domain.LineKey{NodeID: 1, LineID: lineID}, FactorMilli: 2500}})
	if err != nil || len(applied) != 1 {
		t.Fatalf("applied %v err %v", applied, err)
	}
	if got := env.repo.child(1, lineID).EffectiveTrafficFactor(); got != 2500 {
		t.Fatalf("the listener's copy = %d, want 2500", got)
	}
	// The copy cannot be written: the save says so.
	env.svc.nodes = &failingMetadata{memNodes: env.repo, fail: true, only: child.ID}
	if _, err := env.svc.SetLineFactors(ctx, 7, []LineFactorChange{{Line: domain.LineKey{NodeID: 1, LineID: lineID}, FactorMilli: 3000}}); err == nil ||
		!strings.Contains(err.Error(), "did not take its line's factor") {
		t.Fatalf("err = %v, want the failure to be reported", err)
	}
}

// racingNodes lets a second save in between the peek a save takes to learn which
// servers to lock and the read it plans from: the landing gains a line.
type racingNodes struct {
	*memNodes
	once sync.Once
}

func (r *racingNodes) GetByID(ctx context.Context, id int64) (*domain.Node, error) {
	n, err := r.memNodes.GetByID(ctx, id)
	if id == 1 && err == nil {
		r.once.Do(func() {
			r.memNodes.mu.Lock()
			r.memNodes.nodes[1].Relays = append(r.memNodes.nodes[1].Relays, domain.RelayLine{ID: "concurrent", Address: "c.example", Enabled: true, Tags: []string{}})
			r.memNodes.mu.Unlock()
		})
	}
	return n, err
}

// A switch is planned from the lines as they are under the locks, so a line
// another save added meanwhile is kept, and no listener is orphaned.
func TestASwitchIsPlannedFromWhatIsStoredUnderTheLocks(t *testing.T) {
	env := newTunnelEnv(t, nil)
	n := env.landing(t)
	n.Relays = []domain.RelayLine{{Name: "fwd", Address: "e.example.com", Port: 20001, Enabled: true, EntryPanelID: 9, Region: "JP", Tags: []string{}}}
	if err := env.svc.UpdateMetadata(context.Background(), n); err != nil {
		t.Fatal(err)
	}
	lineID := env.landing(t).Relays[0].ID
	env.svc.nodes = &racingNodes{memNodes: env.repo}
	got, err := env.svc.SwitchLineMode(askedAndConfirmed(t, env, context.Background(), lineID, domain.RelayModeTunnel, 0), 1, lineID, domain.RelayModeTunnel, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Relays) != 2 || lineByID(got.Relays, "concurrent") == nil || !lineByID(got.Relays, lineID).Tunnel() {
		t.Fatalf("lines = %+v: the concurrent save's line was overwritten", got.Relays)
	}
}

// A listener that cannot be made is never queued: the save fails and nothing is
// stored, so no line can point at a listener that does not exist.
func TestAListenerThatCannotBeMadeIsNotQueuedAndNothingIsSaved(t *testing.T) {
	env := newTunnelEnv(t, nil)
	env.svc.pool = brokenPool{tunnelServerPool: newTunnelServerPool(7, 9, 10)}
	n := env.landing(t)
	n.Relays = append(n.Relays, tunnelLine(9, 0))
	err := env.svc.UpdateMetadata(context.Background(), n)
	if err == nil || !strings.Contains(err.Error(), "addInbound") {
		t.Fatalf("err = %v, want the failure of the panel", err)
	}
	if env.repo.children() != 0 || len(env.landing(t).Relays) != 0 {
		t.Fatal("a failed listener left something stored")
	}
}

type brokenPool struct{ *tunnelServerPool }

func (p brokenPool) Get(id int64) (ports.PanelClient, error) {
	c, err := p.tunnelServerPool.Get(id)
	if err != nil {
		return nil, err
	}
	return brokenClient{c}, nil
}

type brokenClient struct{ ports.PanelClient }

func (brokenClient) AddInbound(context.Context, ports.InboundSpec) (int, error) {
	return 0, errors.New("the server is unreachable")
}
func (brokenClient) ApplyIsAsynchronous() bool { return true }

// Back to a forward on the listener's own port: the listener being dropped does
// not hold it against the forward, and the confirmation lists the entry port the
// forward opens.
func TestSwitchingBackMayTakeTheListenersOwnPortAndListsIt(t *testing.T) {
	env := newTunnelEnv(t, nil)
	lineID := env.addTunnel(t, 0)
	port := env.repo.child(1, lineID).DesiredPort
	ctx := context.Background()
	_, err := env.svc.SwitchLineMode(ctx, 1, lineID, "", port)
	creates, _ := asRefusal(t, err).Params["creates"].([]TunnelItem)
	if len(creates) != 1 || creates[0].Kind != "forward" || creates[0].ForwardPort != port || creates[0].EntryPanelID != 9 {
		t.Fatalf("creates = %+v, want the forward port opening on the entry server", creates)
	}
	got, err := env.svc.SwitchLineMode(askedAndConfirmed(t, env, ctx, lineID, "", port), 1, lineID, "", port)
	if err != nil {
		t.Fatalf("the listener's own port was held against its replacement: %v", err)
	}
	if got.Relays[0].Port != port || got.Relays[0].Tunnel() {
		t.Fatalf("line = %+v", got.Relays[0])
	}
}

// A new tunnel line that is off gets a listener that is off before the line is
// saved, so nothing fails after the save.
func TestANewDisabledTunnelLineGetsADisabledListener(t *testing.T) {
	env := newTunnelEnv(t, nil)
	n := env.landing(t)
	line := tunnelLine(9, 0)
	line.Enabled = false
	n.Relays = []domain.RelayLine{line}
	if err := env.svc.UpdateMetadata(context.Background(), n); err != nil {
		t.Fatal(err)
	}
	if child := env.repo.child(1, env.landing(t).Relays[0].ID); child == nil || child.Enabled {
		t.Fatalf("listener = %+v, want one that is off", child)
	}
}

// If the port a confirmation listed is not the one planned when it is confirmed,
// the save asks again with the new ports instead of making the tunnel on them.
func TestAChangedPortIsAskedAgainNeverMadeBehindTheConfirmation(t *testing.T) {
	env := newTunnelEnv(t, nil)
	env.links.plan = []int{31001, 31002}
	n := env.landing(t)
	n.Relays = []domain.RelayLine{{Name: "fwd", Address: "e.example.com", Port: 20001, Enabled: true, EntryPanelID: 9, Region: "JP", Tags: []string{}}}
	if err := env.svc.UpdateMetadata(context.Background(), n); err != nil {
		t.Fatal(err)
	}
	lineID := env.landing(t).Relays[0].ID
	ctx := context.Background()
	_, err := env.svc.SwitchLineMode(ctx, 1, lineID, domain.RelayModeTunnel, 0)
	firstRefusal := asRefusal(t, err)
	if first, _ := firstRefusal.Params["creates"].([]TunnelItem); first[0].LinkPort != 31001 {
		t.Fatalf("first listing = %+v", first)
	}
	// Confirmed with the first listing, but the tunnel's port was taken meanwhile
	// and is planned as 31002: asked again with the new ports, nothing made.
	stale := WithTunnelConfirmation(ctx, &TunnelConfirmation{Confirmed: true, Plan: firstRefusal.Params["plan"].(string)})
	_, err = env.svc.SwitchLineMode(stale, 1, lineID, domain.RelayModeTunnel, 0)
	secondRefusal := asRefusal(t, err)
	if again, _ := secondRefusal.Params["creates"].([]TunnelItem); again[0].LinkPort != 31002 {
		t.Fatalf("second listing = %+v, want the new port", again)
	}
	if env.repo.children() != 0 {
		t.Fatal("a listener was made behind a stale confirmation")
	}
	fresh := WithTunnelConfirmation(ctx, &TunnelConfirmation{Confirmed: true, Plan: secondRefusal.Params["plan"].(string)})
	if _, err := env.svc.SwitchLineMode(fresh, 1, lineID, domain.RelayModeTunnel, 0); err != nil {
		t.Fatalf("the re-confirmed switch failed: %v", err)
	}
}

// A tunnel that a failed save made goes with it when nothing else uses the pair.
func TestAFailedSaveDropsTheTunnelItMade(t *testing.T) {
	env := newTunnelEnv(t, nil)
	env.svc.nodes = &failingMetadata{memNodes: env.repo, fail: true}
	n := env.landing(t)
	n.Relays = append(n.Relays, tunnelLine(9, 0))
	if err := env.svc.UpdateMetadata(context.Background(), n); err == nil {
		t.Fatal("the save did not fail")
	}
	if len(env.links.removed) != 1 || env.links.removed[0] != [2]int64{9, 7} {
		t.Fatalf("tunnels removed = %v, want 9→7", env.links.removed)
	}
	if env.repo.children() != 0 {
		t.Fatal("a listener was left")
	}
}

// askedAndConfirmed asks for a switch and returns a context confirming exactly
// what was listed, as the editor does by echoing params.plan.
func askedAndConfirmed(t *testing.T, env *tunnelEnv, ctx context.Context, lineID, mode string, port int) context.Context {
	t.Helper()
	_, err := env.svc.SwitchLineMode(ctx, 1, lineID, mode, port)
	refusal := asRefusal(t, err)
	return WithTunnelConfirmation(ctx, &TunnelConfirmation{Confirmed: true, Plan: refusal.Params["plan"].(string)})
}

// Back to a forward with no port named (the editor's path): the forward takes
// the listener's own port, the confirmation lists it, and the confirmed save
// makes exactly that one, however many times it is asked.
func TestSwitchingBackWithNoPortNamedPlansTheListenersPortEveryTime(t *testing.T) {
	env := newTunnelEnv(t, nil)
	lineID := env.addTunnel(t, 0)
	port := env.repo.child(1, lineID).DesiredPort
	ctx := context.Background()
	var plan string
	for i := 0; i < 5; i++ {
		_, err := env.svc.SwitchLineMode(ctx, 1, lineID, "", 0)
		refusal := asRefusal(t, err)
		creates, _ := refusal.Params["creates"].([]TunnelItem)
		if len(creates) != 1 || creates[0].ForwardPort != port {
			t.Fatalf("listing %d = %+v, want forward port %d", i, creates, port)
		}
		plan = refusal.Params["plan"].(string)
	}
	confirmedCtx := WithTunnelConfirmation(ctx, &TunnelConfirmation{Confirmed: true, Plan: plan})
	got, err := env.svc.SwitchLineMode(confirmedCtx, 1, lineID, "", 0)
	if err != nil || got.Relays[0].Port != port {
		t.Fatalf("made %+v (%v), want a forward on %d", got.Relays, err, port)
	}
}

// A confirmation made without the listing it answers, or with another one, is not
// carried out: it is asked again with the fresh listing.
func TestAConfirmationMustEchoTheListingItAnswers(t *testing.T) {
	env := newTunnelEnv(t, nil)
	n := env.landing(t)
	n.Relays = []domain.RelayLine{{Name: "fwd", Address: "e.example.com", Port: 20001, Enabled: true, EntryPanelID: 9, Region: "JP", Tags: []string{}}}
	if err := env.svc.UpdateMetadata(context.Background(), n); err != nil {
		t.Fatal(err)
	}
	lineID := env.landing(t).Relays[0].ID
	for name, plan := range map[string]string{"none": "", "another": "tunnel/x:1/0@9,0@7,false;"} {
		ctx := WithTunnelConfirmation(context.Background(), &TunnelConfirmation{Confirmed: true, Plan: plan})
		_, err := env.svc.SwitchLineMode(ctx, 1, lineID, domain.RelayModeTunnel, 0)
		asRefusal(t, err)
		if env.repo.children() != 0 {
			t.Fatalf("%s: a listener was made on a confirmation that did not match", name)
		}
	}
}

// lateTunnelNodes lets a second save in between a save's peek and its locked
// read: the landing gains a tunnel line, with its listener already stored.
type lateTunnelNodes struct {
	*memNodes
	once sync.Once
}

func (r *lateTunnelNodes) GetByID(ctx context.Context, id int64) (*domain.Node, error) {
	n, err := r.memNodes.GetByID(ctx, id)
	if id == 1 && err == nil {
		r.once.Do(func() {
			r.memNodes.mu.Lock()
			defer r.memNodes.mu.Unlock()
			r.memNodes.nodes[150] = &domain.Node{ID: 150, PanelID: 9, InboundID: 9, DisplayName: "JP · late", Enabled: true, Kind: domain.NodeKindReal,
				DesiredProtocol: "vless", DesiredPort: 24999, ParentNodeID: 1, ParentLineID: "late", EgressPanelID: 7}
			r.memNodes.nodes[1].Relays = append(r.memNodes.nodes[1].Relays, domain.RelayLine{ID: "late", Name: "late", Address: "l.example", Enabled: true,
				EntryPanelID: 9, Mode: domain.RelayModeTunnel, TunnelNodeID: 150, Tags: []string{}})
		})
	}
	return n, err
}

// A tunnel line another save added between the read and the lock is followed by
// a disable of its landing, and cascaded by its delete or detach.
func TestATunnelLineAddedBetweenTheReadAndTheLockIsFollowedAndCascaded(t *testing.T) {
	ctx := context.Background()
	t.Run("disable follows it", func(t *testing.T) {
		env := newTunnelEnv(t, nil)
		env.svc.nodes = &lateTunnelNodes{memNodes: env.repo}
		if err := env.svc.SetEnabled(ctx, 1, false); err != nil {
			t.Fatal(err)
		}
		if late := env.repo.child(1, "late"); late == nil || late.Enabled {
			t.Fatalf("the late line's listener = %+v, want it disabled with its landing", late)
		}
	})
	t.Run("detach cascades to it", func(t *testing.T) {
		env := newTunnelEnv(t, nil)
		env.svc.nodes = &lateTunnelNodes{memNodes: env.repo}
		if err := env.svc.DetachAndSync(confirmed(ctx), 1); err != nil {
			t.Fatal(err)
		}
		if env.repo.children() != 0 {
			t.Fatal("the late line's listener outlived its landing")
		}
	})
	t.Run("delete asks about it", func(t *testing.T) {
		env := newTunnelEnv(t, nil)
		env.svc.nodes = &lateTunnelNodes{memNodes: env.repo}
		refusal := asRefusal(t, env.svc.DeleteAndSync(ctx, 1))
		removes, _ := refusal.Params["removes"].([]TunnelItem)
		if len(removes) != 1 || removes[0].NodeID != 150 {
			t.Fatalf("removes = %+v, want the late listener named", removes)
		}
		if !env.repo.nodes[1].Enabled {
			t.Fatal("a refused delete disabled the landing")
		}
	})
}

// storeThenFail stores a node and then reports a failure, as a create that dies
// after its row is written does.
type storeThenFail struct{ *memNodes }

func (r storeThenFail) Create(ctx context.Context, n *domain.Node) error {
	if err := r.memNodes.Create(ctx, n); err != nil {
		return err
	}
	return errors.New("the database went away after the write")
}

// A listener whose creation fails after its row was stored is removed again: no
// orphan is left, and the line is not saved.
func TestAListenerThatFailsAfterItsRowWasStoredIsRemoved(t *testing.T) {
	env := newTunnelEnv(t, nil)
	env.svc.nodes = storeThenFail{env.repo}
	n := env.landing(t)
	n.Relays = append(n.Relays, tunnelLine(9, 0))
	if err := env.svc.UpdateMetadata(context.Background(), n); err == nil {
		t.Fatal("the save did not fail")
	}
	if env.repo.children() != 0 {
		t.Fatal("the stored row of a failed listener was left behind")
	}
	if len(env.landing(t).Relays) != 0 {
		t.Fatal("a line was saved that points at a listener that failed")
	}
}

// The 409 of a sync carries the redacted diff, so the admin sees what would
// change before it is applied.
func TestTheSyncConfirmationCarriesTheRedactedDiff(t *testing.T) {
	env := newTunnelEnv(t, nil)
	lineID := env.addTunnel(t, 0)
	n := env.landing(t)
	n.StreamSettings = landingWithBinding
	n.Flow = ""
	env.repo.nodes[1] = copyNode(n)
	_, err := env.svc.SyncTunnelFromLanding(context.Background(), 1, lineID)
	refusal := asRefusal(t, err)
	diff, _ := refusal.Params["diff"].([]TunnelSyncDiff)
	if len(diff) == 0 || refusal.Params["action"] != "sync_from_landing" {
		t.Fatalf("params = %+v", refusal.Params)
	}
	encoded, _ := json.Marshal(diff)
	for _, secret := range []string{"landingPriv", "seed-secret", "verify-secret"} {
		if strings.Contains(string(encoded), secret) {
			t.Fatalf("the diff carries %q", secret)
		}
	}
}

// syncConfirmed asks for a sync and returns a context confirming exactly the diff
// that was shown, as the editor does by echoing params.plan.
func syncConfirmed(t *testing.T, env *tunnelEnv, ctx context.Context, lineID string) context.Context {
	t.Helper()
	_, err := env.svc.SyncTunnelFromLanding(ctx, 1, lineID)
	refusal := asRefusal(t, err)
	return WithTunnelConfirmation(ctx, &TunnelConfirmation{Confirmed: true, Plan: refusal.Params["plan"].(string)})
}

// A sync is applied only if the diff the admin confirmed is still the diff: when
// the landing changed meanwhile, it asks again with the new one.
func TestASyncConfirmedAgainstAChangedLandingIsAskedAgain(t *testing.T) {
	env := newTunnelEnv(t, nil)
	lineID := env.addTunnel(t, 0)
	ctx := context.Background()
	n := env.landing(t)
	n.Flow = ""
	env.repo.nodes[1] = copyNode(n)
	confirmedFirst := syncConfirmed(t, env, ctx, lineID)
	// The landing changes again before the confirmation arrives.
	n = env.landing(t)
	n.StreamSettings = strings.Replace(n.StreamSettings, `"fingerprint":"firefox"`, `"fingerprint":"safari"`, 1)
	env.repo.nodes[1] = copyNode(n)
	_, err := env.svc.SyncTunnelFromLanding(confirmedFirst, 1, lineID)
	again := asRefusal(t, err)
	if env.repo.child(1, lineID).Flow == "" {
		t.Fatal("the stale confirmation was applied")
	}
	if diff, _ := again.Params["diff"].([]TunnelSyncDiff); len(diff) < 2 {
		t.Fatalf("fresh diff = %+v, want the flow and the fingerprint", diff)
	}
	// Confirmed with the fresh plan it is applied.
	fresh := WithTunnelConfirmation(ctx, &TunnelConfirmation{Confirmed: true, Plan: again.Params["plan"].(string)})
	if _, err := env.svc.SyncTunnelFromLanding(fresh, 1, lineID); err != nil {
		t.Fatal(err)
	}
	if env.repo.child(1, lineID).Flow != "" {
		t.Fatal("the confirmed sync was not applied")
	}
}

// A landing's fallbacks point at its own local ports and a VLESS-encryption
// decryption string holds its server key: neither belongs on the entry server,
// on a new listener or on a re-copy, and a diff never shows the string.
func TestTheListenerDoesNotInheritFallbacksOrADecryptionKey(t *testing.T) {
	const landingSettings = `{"decryption":"mlkem768x25519plus.native.600s.server-key-secret","fallbacks":[{"dest":8001}],"clients":[{"id":"landing-user-id"}]}`
	env := newTunnelEnv(t, func(n *domain.Node) {
		n.StreamSettings = landingWithBinding
		n.InboundSettings = landingSettings
	})
	lineID := env.addTunnel(t, 0)
	check := func(where string, n *domain.Node) {
		var settings map[string]any
		if err := json.Unmarshal([]byte(n.InboundSettings), &settings); err != nil {
			t.Fatalf("%s: %v", where, err)
		}
		if _, has := settings["fallbacks"]; has {
			t.Fatalf("%s: the landing's fallbacks were copied: %s", where, n.InboundSettings)
		}
		if settings["decryption"] != "none" || strings.Contains(n.InboundSettings, "server-key-secret") {
			t.Fatalf("%s: the landing's decryption was copied: %s", where, n.InboundSettings)
		}
	}
	check("new listener", env.repo.child(1, lineID))

	ctx := context.Background()
	if _, err := env.svc.SyncTunnelFromLanding(syncConfirmed(t, env, ctx, lineID), 1, lineID); err != nil {
		t.Fatal(err)
	}
	check("re-copy", env.repo.child(1, lineID))

	if out := redactSecrets(landingSettings); strings.Contains(out, "server-key-secret") {
		t.Fatalf("redactSecrets left the decryption key: %s", out)
	}
}
