package render

import (
	"reflect"
	"testing"
)

// TestEmitHysteria2 verifies the mihomo proxy block for a Hysteria 2 inbound.
// Mihomo (and Clash.Meta) expect:
//
//	type: hysteria2, server, port, password, sni, alpn, skip-cert-verify,
//	      obfs, obfs-password
//
// up/down are documented in mihomo's reference but most servers fall back
// to whatever the server announces — we still emit them when the admin
// configured server-side bandwidth limits so clients honour the cap.
func TestEmitHysteria2(t *testing.T) {
	base := map[string]any{
		"name":   "US-1",
		"server": "node.example.com",
		"port":   8443,
		"udp":    true,
	}
	got := emitHysteria2(base, "secret-pwd", hysteria2Opts{
		SNI:          "node.example.com",
		ObfsType:     "salamander",
		ObfsPassword: "obfs-secret",
		ALPN:         []string{"h3"},
		Insecure:     false,
	})
	want := map[string]any{
		"name":             "US-1",
		"server":           "node.example.com",
		"port":             8443,
		"udp":              true,
		"type":             "hysteria2",
		"password":         "secret-pwd",
		"sni":              "node.example.com",
		"alpn":             []string{"h3"},
		"skip-cert-verify": false,
		"obfs":             "salamander",
		"obfs-password":    "obfs-secret",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %#v, want %#v", got, want)
	}
}

// TestEmitHysteria2_NoObfs_Insecure: obfs keys must be ABSENT when the
// admin didn't configure obfuscation. skip-cert-verify=true mirrors the
// admin's "allow insecure" toggle.
func TestEmitHysteria2_NoObfs_Insecure(t *testing.T) {
	base := map[string]any{
		"name": "X", "server": "1.2.3.4", "port": 443, "udp": true,
	}
	got := emitHysteria2(base, "p", hysteria2Opts{Insecure: true})
	if _, ok := got["obfs"]; ok {
		t.Fatalf("obfs key should be absent: %#v", got)
	}
	if got["skip-cert-verify"] != true {
		t.Fatalf("skip-cert-verify = %v, want true", got["skip-cert-verify"])
	}
}

// TestEmitVLESS_FlowVerbatim pins the unified flow contract: the renderer
// emits exactly the node's stored flow and never substitutes a default.
// A REALITY inbound with an empty flow must NOT gain xtls-rprx-vision —
// vision only works over raw TCP and must match the server, so guessing it
// would break ws/grpc or pure-reality clients. (Clash, sing-box and the URI
// builder all follow this rule.)
func TestEmitVLESS_FlowVerbatim(t *testing.T) {
	reality := xuiStreamSettings{Network: "tcp", Security: "reality", RealitySettings: &xuiRealitySettings{}}

	// Empty flow on a REALITY inbound → no "flow" key at all.
	got := emitVLESS(map[string]any{"name": "n"}, "uuid-1", reality, "")
	if v, ok := got["flow"]; ok {
		t.Fatalf("empty flow must not be defaulted, got flow=%v", v)
	}

	// Explicit flow → emitted verbatim.
	got = emitVLESS(map[string]any{"name": "n"}, "uuid-1", reality, "xtls-rprx-vision")
	if got["flow"] != "xtls-rprx-vision" {
		t.Fatalf("flow = %v, want xtls-rprx-vision", got["flow"])
	}

	// Flow is honored regardless of security (e.g. vision over plain TLS).
	tls := xuiStreamSettings{Network: "tcp", Security: "tls", TLSSettings: &xuiTLSSettings{ServerName: "x"}}
	got = emitVLESS(map[string]any{"name": "n"}, "uuid-1", tls, "xtls-rprx-vision-udp443")
	if got["flow"] != "xtls-rprx-vision-udp443" {
		t.Fatalf("flow = %v, want xtls-rprx-vision-udp443", got["flow"])
	}
}

// TestEmitTLS_AllowInsecure pins that tlsSettings.allowInsecure flows into
// the client config's skip-cert-verify (Clash) for VLESS, VMess and Trojan.
func TestEmitTLS_AllowInsecure(t *testing.T) {
	tls := xuiStreamSettings{Network: "tcp", Security: "tls",
		TLSSettings: &xuiTLSSettings{ServerName: "x", AllowInsecure: true}}

	if got := emitVLESS(map[string]any{"name": "n"}, "uuid", tls, ""); got["skip-cert-verify"] != true {
		t.Fatalf("vless skip-cert-verify = %v, want true", got["skip-cert-verify"])
	}
	if got := emitVMess(map[string]any{"name": "n"}, "uuid", tls); got["skip-cert-verify"] != true {
		t.Fatalf("vmess skip-cert-verify = %v, want true", got["skip-cert-verify"])
	}
	if got := emitTrojan(map[string]any{"name": "n"}, "pw", tls); got["skip-cert-verify"] != true {
		t.Fatalf("trojan skip-cert-verify = %v, want true", got["skip-cert-verify"])
	}

	// Default (allowInsecure absent) stays false.
	safe := xuiStreamSettings{Network: "tcp", Security: "tls", TLSSettings: &xuiTLSSettings{ServerName: "x"}}
	if got := emitVLESS(map[string]any{"name": "n"}, "uuid", safe, ""); got["skip-cert-verify"] != false {
		t.Fatalf("vless skip-cert-verify = %v, want false", got["skip-cert-verify"])
	}
}

// TestParseHysteria2Opts maps 3X-UI's actual inbound JSON onto the
// shared hysteria2Opts struct. As documented in
// frontend/src/models/inbound.js, 3X-UI stores salamander obfs under
// streamSettings.finalmask.udp[{type:"salamander", settings:{password}}],
// NOT under settings.obfs. SNI/ALPN remain on tlsSettings.
func TestParseHysteria2Opts(t *testing.T) {
	settingsJSON := `{"version":2,"clients":[]}`
	streamJSON := `{
		"security":"tls",
		"tlsSettings":{"serverName":"node.example.com","alpn":["h3"]},
		"finalmask":{
			"tcp":[],
			"udp":[{"type":"salamander","settings":{"password":"obfs-pwd"}}]
		}
	}`
	got := parseHysteria2Opts(settingsJSON, streamJSON)
	if got.ObfsType != "salamander" {
		t.Fatalf("obfs type = %q", got.ObfsType)
	}
	if got.ObfsPassword != "obfs-pwd" {
		t.Fatalf("obfs password = %q", got.ObfsPassword)
	}
	if got.SNI != "node.example.com" {
		t.Fatalf("sni = %q", got.SNI)
	}
	if !reflect.DeepEqual(got.ALPN, []string{"h3"}) {
		t.Fatalf("alpn = %#v", got.ALPN)
	}
}

// TestParseHysteria2Opts_NoObfs: when finalmask has no salamander entry,
// downstream builders must see empty ObfsType so the obfs keys are
// omitted in the rendered URI / outbound (clients reject `obfs=` with
// empty password).
func TestParseHysteria2Opts_NoObfs(t *testing.T) {
	got := parseHysteria2Opts(`{}`, `{"tlsSettings":{"serverName":"x"}}`)
	if got.ObfsType != "" || got.ObfsPassword != "" {
		t.Fatalf("expected empty obfs, got %+v", got)
	}
}

