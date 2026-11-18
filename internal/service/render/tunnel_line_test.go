package render

import (
	"context"
	"strings"
	"testing"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

// tunnelNodeRepo answers the one lookup the credential resolution makes: the
// hidden listener a client of a tunnel line is attached to.
type tunnelNodeRepo struct {
	ports.NodeRepo
	nodes map[int64]*domain.Node
}

func (r tunnelNodeRepo) GetByID(_ context.Context, id int64) (*domain.Node, error) {
	if n, ok := r.nodes[id]; ok {
		return n, nil
	}
	return nil, domain.ErrNotFound
}

// tunnelListener is the hidden listener of the line "hk" of vlessRealityNode: on
// server 9, with REALITY secrets and names of its own.
func tunnelListener() *domain.Node {
	child := vlessRealityNode(true)
	child.ID, child.PanelID, child.InboundID, child.DesiredPort = 20, 9, 1, 24443
	child.DisplayName = "US-1 · hk"
	child.ServerAddress = "hk.example.com"
	child.ParentNodeID, child.ParentLineID, child.EgressPanelID = 7, "hk", 1
	child.StreamSettings = `{"network":"tcp","security":"reality",` +
		`"realitySettings":{"serverNames":["www.asus.com"],"shortIds":["ef01"],` +
		`"privateKey":"childPriv","settings":{"publicKey":"childPubKey","fingerprint":"chrome"}}}`
	return child
}

func renderTunnelLine(t *testing.T, applied, ready bool) []map[string]any {
	t.Helper()
	landing := vlessRealityNode(true)
	child := tunnelListener()
	landing.Relays = []domain.RelayLine{{
		ID: "hk", Name: "Hong Kong", Address: "hk.example.com", Enabled: true, EntryPanelID: 9,
		Mode: domain.RelayModeTunnel, TunnelNodeID: child.ID, TunnelNode: child, ManagedReady: ready,
		Region: "HK", Tags: []string{"tunnel"},
	}}
	attachment := domain.PSPClientInbound{ClientID: 2, NodeID: child.ID, State: domain.ClientApplyPending}
	if applied {
		attachment.AppliedUUID, attachment.AppliedEmail, attachment.State = "tunnel-line-uuid", "u5-lt@x", domain.ClientApplyApplied
	}
	repo := lineCredentialRepo{
		clients: []*domain.PSPClient{
			{ID: 1, UserID: 5, PanelID: 1, NodeID: landing.ID, LineID: domain.DirectLineID, UUID: "uuid-of-user-5"},
			// The row is the landing's line "hk", and lives on server 9, attached
			// to the line's own listener.
			{ID: 2, UserID: 5, PanelID: 9, NodeID: landing.ID, LineID: "hk", UUID: "tunnel-line-uuid"},
		},
		attachments: map[int64][]domain.PSPClientInbound{
			1: {{ClientID: 1, NodeID: landing.ID, State: domain.ClientApplyApplied, AppliedUUID: "uuid-of-user-5", AppliedEmail: "u5@x"}},
			2: {attachment},
		},
	}
	s := &Service{
		repos: ports.Repos{
			Settings:  fakeSettings{ports.UISettings{EmailDomain: "x"}},
			PSPClient: repo,
			Node:      tunnelNodeRepo{nodes: map[int64]*domain.Node{child.ID: child}},
			XUIPanel:  renderPanelRepo{kind: domain.PanelKindPSP},
		},
		pool: panicPool{},
	}
	items := expandRelays([]renderItem{{name: "US-1", node: landing}})
	return s.buildProxies(context.Background(), &domain.User{ID: 5, UUID: "uuid-of-user-5"}, items, ports.UISettings{EmailDomain: "x"})
}

// A tunnel line is a line of its landing: it keeps the landing's name and the
// line's label, but what the client dials is the line's own listener on the
// entry server, with the listener's own REALITY names and keys and the identity
// of the user's client for that line.
func TestATunnelLineRendersAsALineOfItsLandingWithItsListenersSettings(t *testing.T) {
	out := renderTunnelLine(t, true, true)
	if len(out) != 2 {
		t.Fatalf("proxies = %#v, want the direct line and the tunnel line", out)
	}
	direct, tunnel := out[0], out[1]
	if direct["server"] != "node.example.com" || direct["servername"] != "www.microsoft.com" {
		t.Fatalf("direct line = %#v", direct)
	}
	if tunnel["name"] != "US-1 Hong Kong" {
		t.Fatalf("name = %v, want the landing's name and the line's", tunnel["name"])
	}
	if tunnel["server"] != "hk.example.com" || tunnel["port"] != 24443 {
		t.Fatalf("endpoint = %v:%v, want the line's address and its listener's port", tunnel["server"], tunnel["port"])
	}
	if tunnel["uuid"] != "tunnel-line-uuid" {
		t.Fatalf("uuid = %v, want the line's own identity", tunnel["uuid"])
	}
	if tunnel["servername"] != "www.asus.com" {
		t.Fatalf("servername = %v, want the listener's own REALITY name, not the landing's", tunnel["servername"])
	}
	opts, _ := tunnel["reality-opts"].(map[string]any)
	if opts["public-key"] != "childPubKey" || opts["short-id"] != "ef01" {
		t.Fatalf("reality-opts = %#v, want the listener's own key and short id", opts)
	}
	if tunnel["flow"] != "xtls-rprx-vision" {
		t.Fatalf("flow = %v", tunnel["flow"])
	}
}

// The line is published only while its listener and link run, and only with its
// own identity applied: a listener that does not know the direct identity has
// nothing to fall back to.
func TestATunnelLineIsWithheldUntilReadyAndNeverFallsBackToTheDirectIdentity(t *testing.T) {
	if out := renderTunnelLine(t, true, false); len(out) != 1 || out[0]["server"] != "node.example.com" {
		t.Fatalf("a line whose listener or link is not running was rendered: %#v", out)
	}
	out := renderTunnelLine(t, false, true)
	if len(out) != 1 || out[0]["server"] != "node.example.com" {
		t.Fatalf("a tunnel line without its own applied identity was rendered (with the direct identity?): %#v", out)
	}
}

func TestTunnelLinesRenderInEveryClientFormat(t *testing.T) {
	// The sing-box and URI-list paths dial the same listener.
	landing := vlessRealityNode(true)
	child := tunnelListener()
	landing.Relays = []domain.RelayLine{{ID: "hk", Name: "HK", Address: "hk.example.com", Enabled: true, EntryPanelID: 9,
		Mode: domain.RelayModeTunnel, TunnelNodeID: child.ID, TunnelNode: child, ManagedReady: true, Tags: []string{}}}
	item := renderItem{name: "US-1 HK", node: landing, relay: &landing.Relays[0]}
	if item.listener() != child {
		t.Fatal("the entry does not dial its listener")
	}
	inb := inboundFromNode(item.listener())
	uri, err := buildURI(item.name, item.listener(), &domain.User{UUID: "tunnel-line-uuid"}, inb, "u5-lt@x", "", item.relay)
	if err != nil || uri == "" {
		t.Fatalf("uri = %q (%v)", uri, err)
	}
	for _, want := range []string{"hk.example.com:24443", "tunnel-line-uuid", "www.asus.com", "childPubKey"} {
		if !strings.Contains(uri, want) {
			t.Errorf("uri %q lacks %q", uri, want)
		}
	}
	block, err := emitSingBoxOutbound(item.name, item.listener(), &domain.User{UUID: "tunnel-line-uuid"}, inb, "u5-lt@x", "", item.relay)
	if err != nil || block["server"] != "hk.example.com" || block["server_port"] != 24443 {
		t.Fatalf("sing-box outbound = %#v (%v)", block, err)
	}
}
