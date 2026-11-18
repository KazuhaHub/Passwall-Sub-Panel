package render

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/url"
	"strings"
	"testing"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

func cdnInbound(proto, stream string) *ports.Inbound {
	return &ports.Inbound{ID: 1, Port: 8443, Protocol: proto, Settings: `{"clients":[]}`, StreamSettings: stream}
}

func cdnLine(address string) *domain.RelayLine {
	return &domain.RelayLine{ID: "c1", Name: "CF", Mode: domain.RelayModeCDN, Domain: "cdn.example.com",
		Address: address, Port: 443, SNI: "cdn.example.com", Host: "cdn.example.com", Enabled: true}
}

const (
	cdnWS   = `{"network":"ws","security":"tls","tlsSettings":{"serverName":"origin.example.com","allowInsecure":true,"alpn":["http/1.1"],"settings":{"fingerprint":"chrome"}},"wsSettings":{"path":"/ws","headers":{"Host":"origin.example.com"}}}`
	cdnGRPC = `{"network":"grpc","security":"tls","tlsSettings":{"serverName":"origin.example.com","settings":{"fingerprint":"firefox"}},"grpcSettings":{"serviceName":"svc"}}`
	cdnHU   = `{"network":"httpupgrade","security":"tls","tlsSettings":{"serverName":"origin.example.com","settings":{"fingerprint":"chrome"}},"httpupgradeSettings":{"path":"/hu","host":"origin.example.com"}}`
)

func TestCDNLine_MihomoAllProtocolsAndTransports(t *testing.T) {
	n := &domain.Node{ID: 1, DisplayName: "L", ServerAddress: "origin.example.com"}
	u := &domain.User{UUID: "uuid-1"}
	for _, proto := range []string{"vless", "vmess", "trojan"} {
		for _, tc := range []struct{ name, stream string }{{"ws", cdnWS}, {"grpc", cdnGRPC}, {"httpupgrade", cdnHU}} {
			for _, addr := range []string{"104.16.1.1", "cdn.example.com"} {
				got, err := emitProxy("L CF", n, u, cdnInbound(proto, tc.stream), "", "", cdnLine(addr), false)
				if err != nil || got == nil {
					t.Fatalf("%s/%s: %v %v", proto, tc.name, got, err)
				}
				if got["server"] != addr || got["port"] != 443 {
					t.Errorf("%s/%s: endpoint %v:%v", proto, tc.name, got["server"], got["port"])
				}
				sni := got["servername"]
				if proto == "trojan" {
					sni = got["sni"]
				}
				if sni != "cdn.example.com" {
					t.Errorf("%s/%s: sni = %v", proto, tc.name, sni)
				}
				if got["skip-cert-verify"] != false {
					t.Errorf("%s/%s: skip-cert-verify = %v, a CDN line always verifies", proto, tc.name, got["skip-cert-verify"])
				}
				if got["client-fingerprint"] == nil || got["client-fingerprint"] == "" {
					t.Errorf("%s/%s: the landing's TLS fingerprint is missing", proto, tc.name)
				}
				switch tc.name {
				case "ws":
					o := got["ws-opts"].(map[string]any)
					if o["path"] != "/ws" || o["headers"].(map[string]string)["Host"] != "cdn.example.com" || got["network"] != "ws" {
						t.Errorf("%s/ws: %v %v", proto, got["network"], o)
					}
				case "grpc":
					if got["network"] != "grpc" || got["grpc-opts"].(map[string]any)["grpc-service-name"] != "svc" {
						t.Errorf("%s/grpc: %v %v", proto, got["network"], got["grpc-opts"])
					}
				case "httpupgrade":
					o := got["ws-opts"].(map[string]any)
					if got["network"] != "ws" || o["v2ray-http-upgrade"] != true || o["path"] != "/hu" || o["headers"].(map[string]string)["Host"] != "cdn.example.com" {
						t.Errorf("%s/httpupgrade: %v %v", proto, got["network"], o)
					}
				}
			}
		}
	}
}

func TestCDNLine_SingBox(t *testing.T) {
	n := &domain.Node{ID: 1, DisplayName: "L", ServerAddress: "origin.example.com"}
	u := &domain.User{UUID: "uuid-1"}
	for _, proto := range []string{"vless", "vmess", "trojan"} {
		for _, tc := range []struct{ name, stream string }{{"ws", cdnWS}, {"grpc", cdnGRPC}, {"httpupgrade", cdnHU}} {
			got, err := emitSingBoxOutbound("L CF", n, u, cdnInbound(proto, tc.stream), "", "", cdnLine("104.16.1.1"))
			if err != nil || got == nil {
				t.Fatalf("%s/%s: %v %v", proto, tc.name, got, err)
			}
			if got["server"] != "104.16.1.1" || got["server_port"] != 443 {
				t.Errorf("%s/%s: endpoint %v:%v", proto, tc.name, got["server"], got["server_port"])
			}
			tls := got["tls"].(map[string]any)
			if tls["server_name"] != "cdn.example.com" || tls["insecure"] != false {
				t.Errorf("%s/%s: tls = %v", proto, tc.name, tls)
			}
			if tls["utls"] == nil {
				t.Errorf("%s/%s: the landing's fingerprint is missing", proto, tc.name)
			}
			tr := got["transport"].(map[string]any)
			switch tc.name {
			case "ws":
				if tr["type"] != "ws" || tr["path"] != "/ws" || tr["headers"].(map[string]string)["Host"] != "cdn.example.com" {
					t.Errorf("%s/ws: %v", proto, tr)
				}
			case "grpc":
				if tr["type"] != "grpc" || tr["service_name"] != "svc" {
					t.Errorf("%s/grpc: %v", proto, tr)
				}
			case "httpupgrade":
				if tr["type"] != "httpupgrade" || tr["path"] != "/hu" || tr["host"] != "cdn.example.com" {
					t.Errorf("%s/httpupgrade: %v", proto, tr)
				}
			}
		}
	}
}

func TestCDNLine_URIList(t *testing.T) {
	n := &domain.Node{ID: 1, DisplayName: "L", ServerAddress: "origin.example.com"}
	u := &domain.User{UUID: "uuid-1"}
	for _, proto := range []string{"vless", "trojan"} {
		for _, tc := range []struct{ name, stream string }{{"ws", cdnWS}, {"grpc", cdnGRPC}, {"httpupgrade", cdnHU}} {
			raw, err := buildURI("L CF", n, u, cdnInbound(proto, tc.stream), "", "", cdnLine("104.16.1.1"))
			if err != nil {
				t.Fatal(err)
			}
			parsed, err := url.Parse(raw)
			if err != nil {
				t.Fatal(err)
			}
			q := parsed.Query()
			if parsed.Host != "104.16.1.1:443" || q.Get("sni") != "cdn.example.com" || q.Get("fp") == "" || q.Get("allowInsecure") != "" {
				t.Errorf("%s/%s: %s", proto, tc.name, raw)
			}
			switch tc.name {
			case "ws":
				if q.Get("type") != "ws" || q.Get("path") != "/ws" || q.Get("host") != "cdn.example.com" {
					t.Errorf("%s/ws: %s", proto, raw)
				}
			case "grpc":
				if q.Get("type") != "grpc" || q.Get("serviceName") != "svc" || q.Get("authority") != "cdn.example.com" {
					t.Errorf("%s/grpc: %s", proto, raw)
				}
			case "httpupgrade":
				if q.Get("type") != "httpupgrade" || q.Get("path") != "/hu" || q.Get("host") != "cdn.example.com" {
					t.Errorf("%s/httpupgrade: %s", proto, raw)
				}
			}
		}
	}
	// vmess is the base64 JSON form.
	for _, tc := range []struct{ name, stream, net, host string }{{"ws", cdnWS, "ws", "cdn.example.com"}, {"httpupgrade", cdnHU, "httpupgrade", "cdn.example.com"}} {
		raw, err := buildURI("L CF", n, u, cdnInbound("vmess", tc.stream), "", "", cdnLine("cdn.example.com"))
		if err != nil {
			t.Fatal(err)
		}
		body, _ := base64.StdEncoding.DecodeString(strings.TrimPrefix(raw, "vmess://"))
		var cfg map[string]string
		if err := json.Unmarshal(body, &cfg); err != nil {
			t.Fatal(err)
		}
		if cfg["add"] != "cdn.example.com" || cfg["port"] != "443" || cfg["net"] != tc.net || cfg["host"] != tc.host || cfg["sni"] != "cdn.example.com" || cfg["fp"] == "" {
			t.Errorf("vmess/%s: %v", tc.name, cfg)
		}
	}
}

// A CDN line follows its landing: it is expanded like any line, and never needs
// an entry confirmation to be published.
func TestCDNLine_ExpandsWithoutGate(t *testing.T) {
	line := *cdnLine("104.16.1.1")
	if !line.Publishable() || line.Managed() || line.Gated() {
		t.Fatalf("a CDN line is published with its landing: %+v", line)
	}
	n := &domain.Node{ID: 1, DisplayName: "L", DesiredProtocol: "vless", StreamSettings: cdnWS, Relays: []domain.RelayLine{line}}
	got := expandRelays([]renderItem{{name: "L", node: n}})
	if len(got) != 2 || got[1].relay == nil || got[1].name != "L CF" {
		t.Fatalf("expanded = %+v", got)
	}
	off := line
	off.Enabled = false
	n.Relays = []domain.RelayLine{off}
	if got := expandRelays([]renderItem{{name: "L", node: n}}); len(got) != 1 {
		t.Fatalf("a disabled CDN line must not render: %+v", got)
	}
}

// The direct entry of the same landing, and a plain relay line, are untouched
// by the CDN additions: no fingerprint is added and allowInsecure stays.
func TestCDNLine_OtherEntriesUnchanged(t *testing.T) {
	n := &domain.Node{ID: 1, DisplayName: "L", ServerAddress: "origin.example.com"}
	u := &domain.User{UUID: "uuid-1"}
	inb := cdnInbound("vless", cdnWS)
	direct, _ := emitProxy("L", n, u, inb, "", "", nil, false)
	if direct["skip-cert-verify"] != true || direct["client-fingerprint"] != nil || direct["servername"] != "origin.example.com" {
		t.Fatalf("direct entry changed: %v", direct)
	}
	plain := &domain.RelayLine{Address: "gz.relay.cn", Port: 20001, Enabled: true}
	got, _ := emitProxy("L GZ", n, u, inb, "", "", plain, false)
	if got["skip-cert-verify"] != true || got["client-fingerprint"] != nil {
		t.Fatalf("a plain relay line changed: %v", got)
	}
}

func renderCDNLines(t *testing.T, landingEnabled, lineEnabled bool) []map[string]any {
	t.Helper()
	node := vlessRealityNode(true)
	node.Flow, node.DesiredPort, node.Enabled = "", 8443, landingEnabled
	node.StreamSettings = cdnWS
	line := *cdnLine("104.16.1.1")
	line.Enabled = lineEnabled
	node.Relays = []domain.RelayLine{line}
	repo := lineCredentialRepo{
		clients: []*domain.PSPClient{
			{ID: 1, UserID: 5, NodeID: node.ID, LineID: domain.DirectLineID, UUID: "uuid-of-user-5"},
			{ID: 2, UserID: 5, NodeID: node.ID, LineID: "c1", UUID: "cdn-line-uuid"},
		},
		attachments: map[int64][]domain.PSPClientInbound{
			1: {{ClientID: 1, NodeID: node.ID, State: domain.ClientApplyApplied, AppliedUUID: "uuid-of-user-5", AppliedEmail: "u5@x"}},
			2: {{ClientID: 2, NodeID: node.ID, State: domain.ClientApplyApplied, AppliedUUID: "cdn-line-uuid", AppliedEmail: "u5-lc@x"}},
		},
	}
	s := &Service{
		repos: ports.Repos{Settings: fakeSettings{ports.UISettings{EmailDomain: "x"}}, PSPClient: repo, XUIPanel: renderPanelRepo{kind: domain.PanelKindPSP}},
		pool:  panicPool{},
	}
	items := expandRelays([]renderItem{{name: "US-1", node: node}})
	return s.buildProxies(context.Background(), &domain.User{ID: 5, UUID: "uuid-of-user-5"}, items, ports.UISettings{EmailDomain: "x"})
}

// The CDN line renders next to the direct line with its own identity, the
// CDN's address and names, and the landing's path.
func TestACDNLineRendersWithItsOwnIdentity(t *testing.T) {
	out := renderCDNLines(t, true, true)
	if len(out) != 2 {
		t.Fatalf("proxies = %#v", out)
	}
	direct, cdn := out[0], out[1]
	if direct["server"] != "node.example.com" || direct["uuid"] != "uuid-of-user-5" || direct["servername"] != "origin.example.com" {
		t.Fatalf("direct = %#v", direct)
	}
	if cdn["name"] != "US-1 CF" || cdn["server"] != "104.16.1.1" || cdn["port"] != 443 || cdn["uuid"] != "cdn-line-uuid" || cdn["servername"] != "cdn.example.com" {
		t.Fatalf("cdn = %#v", cdn)
	}
	if o := cdn["ws-opts"].(map[string]any); o["path"] != "/ws" {
		t.Fatalf("ws-opts = %#v", o)
	}
}

// A CDN line follows its landing: a disabled landing or a disabled line has no
// entry (the landing itself is filtered before rendering, the line here).
func TestACDNLineIsNotRenderedWhenDisabled(t *testing.T) {
	out := renderCDNLines(t, true, false)
	if len(out) != 1 || out[0]["server"] != "node.example.com" {
		t.Fatalf("a disabled CDN line was rendered: %#v", out)
	}
}

const (
	cdnPlainWS   = `{"network":"ws","security":"none","wsSettings":{"path":"/ws","host":"origin.example.com"}}`
	cdnGRPCMulti = `{"network":"grpc","security":"tls","tlsSettings":{"serverName":"origin.example.com"},"grpcSettings":{"serviceName":"svc","multiMode":true}}`
)

func tlsFlag(v bool) *bool { return &v }

// With a plain origin the client side is the line's own setting: TLS on gives a
// TLS client section (SNI) though the landing has none; off gives none.
func TestCDNLine_ClientTLSIsIndependentOfTheOrigin(t *testing.T) {
	n := &domain.Node{ID: 1, DisplayName: "L", ServerAddress: "origin.example.com"}
	u := &domain.User{UUID: "uuid-1"}
	for _, proto := range []string{"vless", "vmess"} {
		on := cdnLine("104.16.1.1")
		on.ClientTLS = tlsFlag(true)
		got, _ := emitProxy("x", n, u, cdnInbound(proto, cdnPlainWS), "", "", on, false)
		if got["tls"] != true || got["servername"] != "cdn.example.com" || got["skip-cert-verify"] != false {
			t.Errorf("%s client TLS on: %v", proto, got)
		}
		sb, _ := emitSingBoxOutbound("x", n, u, cdnInbound(proto, cdnPlainWS), "", "", on)
		if tls, _ := sb["tls"].(map[string]any); tls == nil || tls["server_name"] != "cdn.example.com" || tls["insecure"] != false {
			t.Errorf("%s sing-box client TLS on: %v", proto, sb["tls"])
		}
		off := cdnLine("104.16.1.1")
		off.Port, off.ClientTLS = 8080, tlsFlag(false)
		got, _ = emitProxy("x", n, u, cdnInbound(proto, cdnWS), "", "", off, false) // TLS origin, plain client
		if got["tls"] == true || got["servername"] != nil || got["client-fingerprint"] != nil {
			t.Errorf("%s client TLS off: %v", proto, got)
		}
		sb, _ = emitSingBoxOutbound("x", n, u, cdnInbound(proto, cdnWS), "", "", off)
		if sb["tls"] != nil || sb["server_port"] != 8080 {
			t.Errorf("%s sing-box client TLS off: %v", proto, sb)
		}
		raw, _ := buildURI("x", n, u, cdnInbound(proto, cdnWS), "", "", off)
		if strings.Contains(raw, "sni") || strings.Contains(raw, "tls") && proto == "vless" && strings.Contains(raw, "security=tls") {
			t.Errorf("%s uri client TLS off: %s", proto, raw)
		}
	}
	// The Host header is read from Xray's wsSettings.host and replaced by the line's.
	direct, _ := emitProxy("x", n, u, cdnInbound("vless", cdnPlainWS), "", "", nil, false)
	if direct["ws-opts"].(map[string]any)["headers"].(map[string]string)["Host"] != "origin.example.com" {
		t.Errorf("wsSettings.host not read: %v", direct["ws-opts"])
	}
}

// A line's Host override is the Host of whichever transport the landing uses,
// on every line that sets one, not only CDN lines.
func TestRelayHostOverrideAppliesToHTTPUpgradeAndGRPCOnAnyLine(t *testing.T) {
	n := &domain.Node{ID: 1, DisplayName: "L", ServerAddress: "origin.example.com"}
	u := &domain.User{UUID: "uuid-1"}
	ext := &domain.RelayLine{Address: "gz.relay.cn", Port: 20001, Host: "front.example.net", Enabled: true}
	got, _ := emitProxy("x", n, u, cdnInbound("vless", cdnHU), "", "", ext, false)
	if o := got["ws-opts"].(map[string]any); o["headers"].(map[string]string)["Host"] != "front.example.net" {
		t.Errorf("httpupgrade host: %v", o)
	}
	sb, _ := emitSingBoxOutbound("x", n, u, cdnInbound("vless", cdnHU), "", "", ext)
	if sb["transport"].(map[string]any)["host"] != "front.example.net" {
		t.Errorf("sing-box httpupgrade host: %v", sb["transport"])
	}
	raw, _ := buildURI("x", n, u, cdnInbound("vless", cdnGRPC), "", "", ext)
	if !strings.Contains(raw, "authority=front.example.net") {
		t.Errorf("grpc authority: %s", raw)
	}
	// And no override leaves the landing's own host.
	plain := &domain.RelayLine{Address: "gz.relay.cn", Enabled: true}
	raw, _ = buildURI("x", n, u, cdnInbound("vless", cdnGRPC), "", "", plain)
	if strings.Contains(raw, "authority") {
		t.Errorf("no override must add no authority: %s", raw)
	}
}

// VMess JSON carries the gRPC authority in host and the landing's gRPC mode.
func TestVMessGRPCAuthorityAndMode(t *testing.T) {
	n := &domain.Node{ID: 1, DisplayName: "L", ServerAddress: "origin.example.com"}
	u := &domain.User{UUID: "uuid-1"}
	decode := func(raw string) map[string]string {
		body, _ := base64.StdEncoding.DecodeString(strings.TrimPrefix(raw, "vmess://"))
		var cfg map[string]string
		_ = json.Unmarshal(body, &cfg)
		return cfg
	}
	raw, _ := buildURI("x", n, u, cdnInbound("vmess", cdnGRPC), "", "", cdnLine("104.16.1.1"))
	if cfg := decode(raw); cfg["net"] != "grpc" || cfg["host"] != "cdn.example.com" || cfg["type"] != "gun" || cfg["path"] != "svc" {
		t.Errorf("gun: %v", cfg)
	}
	raw, _ = buildURI("x", n, u, cdnInbound("vmess", cdnGRPCMulti), "", "", cdnLine("104.16.1.1"))
	if cfg := decode(raw); cfg["type"] != "multi" || cfg["host"] != "cdn.example.com" {
		t.Errorf("multi: %v", cfg)
	}
	// VLESS reports the same mode.
	raw, _ = buildURI("x", n, u, cdnInbound("vless", cdnGRPCMulti), "", "", cdnLine("104.16.1.1"))
	if !strings.Contains(raw, "mode=multi") {
		t.Errorf("vless multi: %s", raw)
	}
}

// A CDN line whose landing no longer suits a CDN is withheld, not rendered broken;
// a landing with unknown transport is not judged; other lines are untouched.
func TestACDNLineIsWithheldWhenItsLandingIsUnfit(t *testing.T) {
	line := *cdnLine("104.16.1.1")
	fwd := domain.RelayLine{ID: "f", Name: "GZ", Address: "gz.relay.cn", Enabled: true}
	for name, tc := range map[string]struct {
		stream string
		want   int
	}{
		"reality": {`{"network":"tcp","security":"reality"}`, 2},
		"ws":      {cdnWS, 3},
		"unknown": {"", 3},
	} {
		n := &domain.Node{ID: 1, DisplayName: "L", DesiredProtocol: "vless", StreamSettings: tc.stream, Relays: []domain.RelayLine{line, fwd}}
		if got := expandRelays([]renderItem{{name: "L", node: n}}); len(got) != tc.want {
			t.Errorf("%s: %d entries, want %d", name, len(got), tc.want)
		}
	}
}

func TestACDNLineWithoutANameIsLabelledByItsDomain(t *testing.T) {
	line := *cdnLine("104.16.1.1")
	line.Name = ""
	n := &domain.Node{ID: 1, DisplayName: "L", DesiredProtocol: "vless", StreamSettings: cdnWS, Relays: []domain.RelayLine{line}}
	got := expandRelays([]renderItem{{name: "L", node: n}})
	if got[1].name != "L cdn.example.com" {
		t.Errorf("name = %q", got[1].name)
	}
}

func TestCDNLine_UsesActualInboundForCompatibility(t *testing.T) {
	n := &domain.Node{ID: 1, DisplayName: "L", ServerAddress: "origin.example.com"}
	u := &domain.User{UUID: "uuid-1"}
	line := cdnLine("104.16.1.1")
	inb := cdnInbound("vless", `{"network":"tcp","security":"reality"}`)
	if got, err := emitProxy("L CF", n, u, inb, "", "", line, false); got != nil || err != nil {
		t.Errorf("mihomo rendered the incompatible live inbound: %v, %v", got, err)
	}
	if got, err := emitSingBoxOutbound("L CF", n, u, inb, "", "", line); got != nil || err != nil {
		t.Errorf("sing-box rendered the incompatible live inbound: %v, %v", got, err)
	}
	if got, err := buildURI("L CF", n, u, inb, "", "", line); got != "" || err != nil {
		t.Errorf("URI rendered the incompatible live inbound: %v, %v", got, err)
	}
}
