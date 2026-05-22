package render

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

// ---- minimal fakes (white-box: same package) ----

type fakeSettings struct{ s ports.UISettings }

func (f fakeSettings) Load(_ context.Context, _ ports.UISettings) (ports.UISettings, error) {
	return f.s, nil
}
func (f fakeSettings) Save(_ context.Context, _ ports.UISettings) error { return nil }

// panicPool fails the test if any pool access happens — used to prove that a
// node with a local config snapshot triggers zero 3X-UI calls.
type panicPool struct{}

func (panicPool) Get(int64) (ports.XUIClient, error) {
	panic("pool.Get must not be called when the node has a local config snapshot")
}
func (panicPool) List() []*domain.XUIPanel   { return nil }
func (panicPool) Add(*domain.XUIPanel) error { return nil }
func (panicPool) Remove(int64) error         { return nil }

// recordingPool records whether Get was called and always reports the panel as
// unreachable — enough to assert the fallback live-fetch path was taken without
// implementing the whole XUIClient surface.
type recordingPool struct{ got atomic.Bool }

func (p *recordingPool) Get(int64) (ports.XUIClient, error) {
	p.got.Store(true)
	return nil, errors.New("panel unreachable")
}
func (p *recordingPool) List() []*domain.XUIPanel   { return nil }
func (p *recordingPool) Add(*domain.XUIPanel) error { return nil }
func (p *recordingPool) Remove(int64) error         { return nil }

// vlessRealityNode returns a node carrying a faithful VLESS+Reality config
// snapshot, as the v3.5 write-through / poll backfill would store it.
func vlessRealityNode(synced bool) *domain.Node {
	n := &domain.Node{
		ID:            7,
		PanelID:       1,
		InboundID:     3,
		DisplayName:   "US-1",
		ServerAddress: "node.example.com",
		Flow:          "xtls-rprx-vision",
		Protocol:      "vless",
		Port:          443,
		Enabled:       true,
		Kind:          domain.NodeKindReal,
		StreamSettings: `{"network":"tcp","security":"reality",` +
			`"realitySettings":{"serverNames":["www.microsoft.com"],"shortIds":["abcd"],` +
			`"privateKey":"aPriv","settings":{"publicKey":"aPubKey","fingerprint":"chrome"}}}`,
		InboundSettings: `{"decryption":"none"}`,
	}
	if synced {
		now := time.Now()
		n.ConfigSyncedAt = &now
		n.ConfigSyncState = "synced"
	}
	return n
}

// TestInboundFromNode verifies the local snapshot maps onto a faithful
// ports.Inbound (the fields render and the reconcile push path consume).
func TestInboundFromNode(t *testing.T) {
	n := vlessRealityNode(true)
	n.InboundListen = "127.0.0.1"
	n.InboundRemark = "vless-reality"
	n.Sniffing = `{"enabled":true}`
	n.Allocate = `{"strategy":"always"}`
	n.InboundExpiryTime = 0

	inb := inboundFromNode(n)

	if inb.ID != 3 || inb.Port != 443 || inb.Protocol != "vless" {
		t.Fatalf("top-level mismatch: %+v", inb)
	}
	if inb.Listen != "127.0.0.1" || inb.Remark != "vless-reality" {
		t.Fatalf("listen/remark mismatch: %+v", inb)
	}
	if inb.Settings != n.InboundSettings || inb.StreamSettings != n.StreamSettings {
		t.Fatalf("settings/stream not carried verbatim")
	}
	if inb.Sniffing != n.Sniffing || inb.Allocate != n.Allocate {
		t.Fatalf("sniffing/allocate not carried verbatim")
	}
	if !inb.Enable {
		t.Fatalf("enable should mirror node.Enabled")
	}
}

// TestBuildProxies_LocalConfig_ZeroFetch is the headline v3.5 guarantee: a node
// with a captured snapshot renders entirely from the DB, never touching the
// 3X-UI pool (panicPool would crash the test if it did). The emitted block must
// reflect the stored VLESS+Reality config.
func TestBuildProxies_LocalConfig_ZeroFetch(t *testing.T) {
	s := &Service{
		repos: ports.Repos{Settings: fakeSettings{ports.UISettings{EmailDomain: "kazuha.org"}}},
		pool:  panicPool{},
	}
	u := &domain.User{ID: 5, UUID: "uuid-of-user-5"}
	items := []renderItem{{name: "US-1", node: vlessRealityNode(true)}}

	out := s.buildProxies(context.Background(), u, items)

	if len(out) != 1 {
		t.Fatalf("want 1 proxy block, got %d: %#v", len(out), out)
	}
	got := out[0]
	if got["type"] != "vless" || got["uuid"] != "uuid-of-user-5" {
		t.Fatalf("vless/uuid mismatch: %#v", got)
	}
	if got["server"] != "node.example.com" || got["port"] != 443 {
		t.Fatalf("server/port mismatch: %#v", got)
	}
	if got["flow"] != "xtls-rprx-vision" || got["servername"] != "www.microsoft.com" {
		t.Fatalf("flow/servername mismatch: %#v", got)
	}
	ro, ok := got["reality-opts"].(map[string]any)
	if !ok || ro["public-key"] != "aPubKey" || ro["short-id"] != "abcd" {
		t.Fatalf("reality-opts mismatch: %#v", got["reality-opts"])
	}
}

// TestResolveInbounds covers the shared local-first + bulk-fallback decision
// used by all three render paths: captured nodes resolve from the local
// snapshot without touching the pool; un-captured ones fall back to a fetch.
func TestResolveInbounds(t *testing.T) {
	// Captured node → served from the local snapshot, pool never touched
	// (panicPool would crash the test on any access).
	s := &Service{pool: panicPool{}}
	got := s.resolveInbounds(context.Background(), []renderItem{{name: "US-1", node: vlessRealityNode(true)}})
	if inb := got[7]; inb == nil || inb.Protocol != "vless" {
		t.Fatalf("captured node should resolve from local snapshot: %#v", got)
	}

	// Un-captured node → fallback fetch; unreachable panel → absent from map.
	pool := &recordingPool{}
	s2 := &Service{pool: pool}
	got2 := s2.resolveInbounds(context.Background(), []renderItem{{name: "US-1", node: vlessRealityNode(false)}})
	if !pool.got.Load() {
		t.Fatalf("un-captured node should trigger a fallback fetch")
	}
	if _, ok := got2[7]; ok {
		t.Fatalf("unreachable panel → node must be absent from the resolved map")
	}
}

// TestBuildProxies_NoLocalConfig_FallsBackToFetch proves the transition-window
// fallback: a node whose snapshot was never captured (ConfigSyncedAt==nil)
// triggers a live pool fetch. Here the panel is unreachable, so the node is
// skipped and the sentinel is injected — but the pool WAS consulted.
func TestBuildProxies_NoLocalConfig_FallsBackToFetch(t *testing.T) {
	pool := &recordingPool{}
	s := &Service{
		repos: ports.Repos{Settings: fakeSettings{ports.UISettings{EmailDomain: "kazuha.org"}}},
		pool:  pool,
	}
	u := &domain.User{ID: 5, UUID: "uuid-of-user-5"}
	items := []renderItem{{name: "US-1", node: vlessRealityNode(false)}}

	out := s.buildProxies(context.Background(), u, items)

	if !pool.got.Load() {
		t.Fatalf("expected a live fetch when ConfigSyncedAt is nil, pool was never consulted")
	}
	// Panel unreachable → real proxy dropped → sentinel keeps the document valid.
	if len(out) != 1 {
		t.Fatalf("want sentinel-only output, got %d blocks: %#v", len(out), out)
	}
}
