package render

import (
	"strings"
	"testing"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

// wsTLSInbound is a VLESS-over-WS+TLS inbound whose SNI and WS Host both point
// at the landing domain — the fixture for exercising relay SNI/Host overrides.
func wsTLSInbound() *ports.Inbound {
	return &ports.Inbound{
		ID:             1,
		Port:           443,
		Protocol:       "vless",
		Settings:       `{"clients":[]}`,
		StreamSettings: `{"network":"ws","security":"tls","tlsSettings":{"serverName":"land.example.com"},"wsSettings":{"path":"/ws","headers":{"Host":"land.example.com"}}}`,
	}
}

// TestExpandRelays_DirectPlusLines: a node with relay lines expands into the
// direct entry (kept) followed by one entry per ENABLED line, in order. A
// disabled line is skipped; relay-less nodes and separators pass through
// unchanged with a nil relay.
func TestExpandRelays_DirectPlusLines(t *testing.T) {
	n := &domain.Node{ID: 1, DisplayName: "HK", Relays: []domain.RelayLine{
		{Name: "GZ", Address: "gz.relay.cn", Port: 20001, Enabled: true},
		{Name: "OFF", Address: "off.relay.cn", Enabled: false},
		{Name: "SH", Address: "sh.relay.cn", Enabled: true},
	}}
	plain := &domain.Node{ID: 2, DisplayName: "JP"}

	items := []renderItem{
		{isSeparator: true, name: "---- 中转 ----"},
		{name: "HK", node: n},
		{name: "JP", node: plain},
	}
	got := expandRelays(items)

	// separator + (HK direct + HK GZ + HK SH) + JP direct = 5
	if len(got) != 5 {
		t.Fatalf("want 5 items, got %d: %+v", len(got), got)
	}
	if !got[0].isSeparator {
		t.Fatalf("item 0 should be the separator")
	}
	want := []struct {
		name    string
		isRelay bool
		addr    string
	}{
		{"HK", false, ""},
		{"HK GZ", true, "gz.relay.cn"},
		{"HK SH", true, "sh.relay.cn"},
		{"JP", false, ""},
	}
	for i, w := range want {
		it := got[i+1]
		if it.name != w.name {
			t.Errorf("item %d name = %q, want %q", i+1, it.name, w.name)
		}
		if (it.relay != nil) != w.isRelay {
			t.Errorf("item %d isRelay = %v, want %v", i+1, it.relay != nil, w.isRelay)
		}
		if w.isRelay && it.relay.Address != w.addr {
			t.Errorf("item %d relay addr = %q, want %q", i+1, it.relay.Address, w.addr)
		}
	}
}

// TestExpandRelays_HideDirect: HideDirect drops the direct entry only when at
// least one relay is enabled. With no enabled relay the direct entry survives
// so the node can never silently disappear.
func TestExpandRelays_HideDirect(t *testing.T) {
	hidden := &domain.Node{ID: 1, DisplayName: "HK", HideDirect: true, Relays: []domain.RelayLine{
		{Name: "GZ", Address: "gz.relay.cn", Enabled: true},
	}}
	got := expandRelays([]renderItem{{name: "HK", node: hidden}})
	if len(got) != 1 || got[0].relay == nil || got[0].name != "HK GZ" {
		t.Fatalf("HideDirect with an enabled relay should yield only the relay entry, got %+v", got)
	}

	// HideDirect but every relay disabled → keep the direct entry.
	stranded := &domain.Node{ID: 2, DisplayName: "TW", HideDirect: true, Relays: []domain.RelayLine{
		{Name: "X", Address: "x", Enabled: false},
	}}
	got = expandRelays([]renderItem{{name: "TW", node: stranded}})
	if len(got) != 1 || got[0].relay != nil {
		t.Fatalf("HideDirect with no enabled relay should keep the direct entry, got %+v", got)
	}
}

// TestEmitProxy_Relay: the Clash block for a relay variant swaps server+port
// and (for a CDN line) the TLS servername + WS Host, while a plain L4 line
// (no SNI/Host) reuses the landing's TLS/Host and falls back to the inbound
// port when the line omits one.
func TestEmitProxy_Relay(t *testing.T) {
	inb := wsTLSInbound()
	n := &domain.Node{ID: 1, DisplayName: "HK", ServerAddress: "land.example.com"}
	u := &domain.User{UUID: "uuid-1"}

	// Direct: untouched landing endpoint.
	direct, err := emitProxy("HK", n, u, inb, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if direct["server"] != "land.example.com" || direct["port"] != 443 {
		t.Fatalf("direct endpoint = %v:%v", direct["server"], direct["port"])
	}
	if direct["servername"] != "land.example.com" {
		t.Fatalf("direct servername = %v", direct["servername"])
	}

	// CDN line: full override.
	cdn := &domain.RelayLine{Address: "104.16.0.1", Port: 8443, SNI: "cdn.example.com", Host: "cdn.example.com", Enabled: true}
	got, err := emitProxy("HK CDN", n, u, inb, "", cdn)
	if err != nil {
		t.Fatal(err)
	}
	if got["server"] != "104.16.0.1" || got["port"] != 8443 {
		t.Fatalf("relay endpoint = %v:%v, want 104.16.0.1:8443", got["server"], got["port"])
	}
	if got["servername"] != "cdn.example.com" {
		t.Fatalf("relay servername = %v, want cdn.example.com", got["servername"])
	}
	wsHost := got["ws-opts"].(map[string]any)["headers"].(map[string]string)["Host"]
	if wsHost != "cdn.example.com" {
		t.Fatalf("relay ws Host = %q, want cdn.example.com", wsHost)
	}
	// Credentials are the landing's, unchanged.
	if got["uuid"] != "uuid-1" {
		t.Fatalf("relay uuid = %v, want landing's uuid-1", got["uuid"])
	}

	// Plain L4 line: only server changes; port falls back to inbound; SNI/Host kept.
	l4 := &domain.RelayLine{Address: "relay.cn", Enabled: true}
	got, err = emitProxy("HK L4", n, u, inb, "", l4)
	if err != nil {
		t.Fatal(err)
	}
	if got["server"] != "relay.cn" || got["port"] != 443 {
		t.Fatalf("L4 endpoint = %v:%v, want relay.cn:443", got["server"], got["port"])
	}
	if got["servername"] != "land.example.com" {
		t.Fatalf("L4 servername = %v, want landing's land.example.com", got["servername"])
	}
}

// TestBuildURI_Relay: the URI variant carries the relay host:port and, for a
// CDN line, the overridden sni / ws host query params.
func TestBuildURI_Relay(t *testing.T) {
	inb := wsTLSInbound()
	n := &domain.Node{ID: 1, DisplayName: "HK", ServerAddress: "land.example.com"}
	u := &domain.User{UUID: "uuid-1"}

	cdn := &domain.RelayLine{Address: "104.16.0.1", Port: 8443, SNI: "cdn.example.com", Host: "cdn.example.com", Enabled: true}
	uri, err := buildURI("HK CDN", n, u, inb, "", cdn)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(uri, "104.16.0.1:8443") {
		t.Fatalf("uri missing relay endpoint: %s", uri)
	}
	if !strings.Contains(uri, "sni=cdn.example.com") {
		t.Fatalf("uri missing overridden sni: %s", uri)
	}
	if !strings.Contains(uri, "host=cdn.example.com") {
		t.Fatalf("uri missing overridden ws host: %s", uri)
	}
}

// TestEmitSingBoxOutbound_Relay: the sing-box outbound for a relay variant
// swaps server/server_port and the TLS server_name + WS Host.
func TestEmitSingBoxOutbound_Relay(t *testing.T) {
	inb := wsTLSInbound()
	n := &domain.Node{ID: 1, DisplayName: "HK", ServerAddress: "land.example.com"}
	u := &domain.User{UUID: "uuid-1"}

	cdn := &domain.RelayLine{Address: "104.16.0.1", Port: 8443, SNI: "cdn.example.com", Host: "cdn.example.com", Enabled: true}
	got, err := emitSingBoxOutbound("HK CDN", n, u, inb, "", cdn)
	if err != nil {
		t.Fatal(err)
	}
	if got["server"] != "104.16.0.1" || got["server_port"] != 8443 {
		t.Fatalf("relay endpoint = %v:%v", got["server"], got["server_port"])
	}
	tls := got["tls"].(map[string]any)
	if tls["server_name"] != "cdn.example.com" {
		t.Fatalf("relay server_name = %v", tls["server_name"])
	}
	transport := got["transport"].(map[string]any)
	host := transport["headers"].(map[string]string)["Host"]
	if host != "cdn.example.com" {
		t.Fatalf("relay transport Host = %q", host)
	}
}
