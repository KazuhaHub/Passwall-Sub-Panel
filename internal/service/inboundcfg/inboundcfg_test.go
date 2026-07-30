package inboundcfg

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

func TestHasLocalConfig(t *testing.T) {
	now := time.Now()
	cases := []struct {
		name string
		node *domain.Node
		want bool
	}{
		{"nil node", nil, false},
		{"never captured", &domain.Node{}, false},
		{"captured, synced", &domain.Node{ConfigSyncedAt: &now, ConfigSyncState: "synced"}, true},
		{"captured, empty state", &domain.Node{ConfigSyncedAt: &now, ConfigSyncState: ""}, true},
		{"captured but gated off", &domain.Node{ConfigSyncedAt: &now, ConfigSyncState: "drift"}, false},
	}
	for _, c := range cases {
		if got := HasLocalConfig(c.node); got != c.want {
			t.Fatalf("%s: HasLocalConfig = %v, want %v", c.name, got, c.want)
		}
	}
}

func TestStripClients(t *testing.T) {
	// SS-2022: method + server PSK live alongside clients[] and MUST survive.
	in := `{"method":"2022-blake3-aes-256-gcm","password":"server-psk","clients":[{"email":"u1-n3@d","password":"upsk"}]}`
	var m map[string]any
	if err := json.Unmarshal([]byte(StripClients(in)), &m); err != nil {
		t.Fatalf("result not valid JSON: %v", err)
	}
	if _, ok := m["clients"]; ok {
		t.Fatalf("clients[] should be stripped")
	}
	if m["method"] != "2022-blake3-aes-256-gcm" || m["password"] != "server-psk" {
		t.Fatalf("protocol-level fields lost: %v", m)
	}
	for _, verbatim := range []string{`{"decryption":"none"}`, "not json", ""} {
		if StripClients(verbatim) != verbatim {
			t.Fatalf("input without clients should pass through verbatim: %q", verbatim)
		}
	}
}

func TestApplySpec(t *testing.T) {
	n := &domain.Node{ID: 1}
	spec := ports.InboundSpec{
		Protocol:       "VLESS", // stored lowercase
		Port:           443,
		Listen:         "0.0.0.0",
		Remark:         "us-reality",
		Settings:       `{"decryption":"none","clients":[{"id":"x"}]}`,
		StreamSettings: `{"network":"ws"}`,
		Sniffing:       `{"enabled":true}`,
		Allocate:       `{"strategy":"always"}`,
		ExpiryTime:     12345,
	}
	ApplySpec(n, spec)

	if n.Protocol != "vless" || n.Port != 443 || n.InboundListen != "0.0.0.0" || n.InboundRemark != "us-reality" {
		t.Fatalf("top-level mismatch: %+v", n)
	}
	if n.StreamSettings != spec.StreamSettings || n.Sniffing != spec.Sniffing || n.Allocate != spec.Allocate || n.InboundExpiryTime != 12345 {
		t.Fatalf("stream/sniffing/allocate/expiry not stored verbatim")
	}
	if strings.Contains(n.InboundSettings, "clients") {
		t.Fatalf("clients[] must be stripped: %s", n.InboundSettings)
	}
	if n.ConfigSyncedAt == nil || n.ConfigSyncState != "synced" {
		t.Fatalf("snapshot should be marked synced: %+v", n)
	}
}

func TestCaptureAndRoundTrip(t *testing.T) {
	inb := &ports.Inbound{
		Protocol:       "shadowsocks",
		Port:           8388,
		Listen:         "127.0.0.1",
		Remark:         "ss",
		Settings:       `{"method":"aes-128-gcm","clients":[{"email":"e"}]}`,
		StreamSettings: `{"network":"tcp"}`,
	}
	n := &domain.Node{ID: 1, Enabled: true}
	Capture(n, inb)
	if n.Protocol != "shadowsocks" || n.Port != 8388 || n.InboundListen != "127.0.0.1" {
		t.Fatalf("capture top-level mismatch: %+v", n)
	}
	if strings.Contains(n.InboundSettings, "clients") || !strings.Contains(n.InboundSettings, "aes-128-gcm") {
		t.Fatalf("ss method must survive, clients stripped: %s", n.InboundSettings)
	}

	// SpecFromNode is the inverse used by the reconcile push; it must round-trip
	// the stored fields (clients[] absent — UpdateInbound re-merges live ones).
	spec := SpecFromNode(n)
	if spec.Protocol != "shadowsocks" || spec.Port != 8388 || spec.Listen != "127.0.0.1" || !spec.Enable {
		t.Fatalf("SpecFromNode mismatch: %+v", spec)
	}
	if spec.StreamSettings != `{"network":"tcp"}` {
		t.Fatalf("SpecFromNode stream mismatch: %+v", spec)
	}
}

// TestCaptureEmptySettingsStoresValidJSON guards against the v3.5 client-wipe
// bug: a node captured from an inbound with blank settings used to land in the
// DB with InboundSettings="". Subsequent reconcile would see drift against a
// non-empty live, push "", and the RMW guard's empty-input shortcut would let
// the empty push reach 3X-UI — wiping every live client. Both ends are now
// hardened; this test pins the snapshot side.
func TestCaptureEmptySettingsStoresValidJSON(t *testing.T) {
	for _, raw := range []string{"", "   ", "\n"} {
		n := &domain.Node{ID: 1}
		Capture(n, &ports.Inbound{Protocol: "vless", Port: 443, Settings: raw})
		if strings.TrimSpace(n.InboundSettings) == "" {
			t.Fatalf("blank live settings %q must not produce blank snapshot", raw)
		}
		var m map[string]any
		if err := json.Unmarshal([]byte(n.InboundSettings), &m); err != nil {
			t.Fatalf("snapshot must be valid JSON; got %q (%v)", n.InboundSettings, err)
		}
	}

	// ApplySpec (admin write-through) has the same guarantee.
	n := &domain.Node{ID: 1}
	ApplySpec(n, ports.InboundSpec{Protocol: "vless", Port: 443, Settings: ""})
	if strings.TrimSpace(n.InboundSettings) == "" {
		t.Fatalf("ApplySpec with blank settings must normalise to {}")
	}
}

func TestInSync(t *testing.T) {
	live := &ports.Inbound{
		Port:           443,
		Protocol:       "vless",
		Listen:         "",
		StreamSettings: `{"network":"ws","security":"tls"}`,
		// live carries clients[]; stored does not — must NOT register as drift.
		Settings: `{"decryption":"none","clients":[{"id":"x","email":"e"}]}`,
	}
	n := &domain.Node{
		Port:            443,
		Protocol:        "vless",
		StreamSettings:  `{"security":"tls","network":"ws"}`, // key order differs → still in sync
		InboundSettings: `{"decryption":"none"}`,
	}
	if !InSync(n, live) {
		t.Fatalf("expected in-sync despite clients[] and key-order differences")
	}

	// A real config change (security flipped) must register as drift.
	drift := *n
	drift.StreamSettings = `{"network":"ws","security":"reality"}`
	if InSync(&drift, live) {
		t.Fatalf("expected drift when stream security differs")
	}

	// Port change is drift.
	drift2 := *n
	drift2.Port = 8443
	if InSync(&drift2, live) {
		t.Fatalf("expected drift when port differs")
	}

	// Remark is NOT enforced (cosmetic): a remark-only difference must read as
	// in-sync, so reconcile never reverts an admin's direct 3X-UI rename.
	noDrift := *n
	noDrift.InboundRemark = "psp-managed"
	liveRenamed := *live
	liveRenamed.Remark = "manual-rename"
	if !InSync(&noDrift, &liveRenamed) {
		t.Fatalf("remark-only difference must NOT register as drift")
	}
}

// TestJSONEqualEmptyEquivalence locks the #4 insurance: every "effectively
// empty" JSON form compares equal, so a snapshot normalised to "{}" never
// perpetually drifts against a live "" / null the way a naive string/parse
// compare would.
func TestJSONEqualEmptyEquivalence(t *testing.T) {
	empties := []string{"", "   ", "null", "{}", "[]"}
	for _, a := range empties {
		for _, b := range empties {
			if !jsonEqual(a, b) {
				t.Fatalf("jsonEqual(%q, %q) = false, want true (empty-equivalent)", a, b)
			}
		}
	}
	if jsonEqual(`{"network":"ws"}`, "{}") {
		t.Fatalf("non-empty object must differ from empty")
	}
	if !jsonEqual(`{"a":1,"b":2}`, `{"b":2,"a":1}`) {
		t.Fatalf("key order must not matter")
	}
	if jsonEqual(`{bad`, `{}`) {
		t.Fatalf("unparseable input must not equal empty")
	}
}

// TestSpecFromNodeStripsRealityFinalmaskTCP covers the 3X-UI 3.6.0 upgrade
// hazard: 3.6.0 ships a boot seeder (InboundRealityFinalmaskTcpStrip) that
// deletes finalmask.tcp from every stored REALITY inbound, because that combo
// panics Xray-core on the first connection (XTLS/Xray-core#6453). A PSP snapshot
// captured BEFORE the upgrade still carries the key, so InSync reports drift and
// reconcile reverse-pushes it — which 3X-UI then rejects forever
// (validateFinalMaskRealityCombo), pinning the node at config_sync_state=pending.
//
// Stripping it here, on the push spec, makes the push succeed; reconcile's
// post-push re-capture then converges the snapshot and the drift is gone for
// good, with no admin action. PSP never authors this combination itself (the SPA
// emits finalmask only for hysteria2, with an empty tcp array), so this only
// affects inbounds PSP adopted from an operator.
func TestSpecFromNodeStripsRealityFinalmaskTCP(t *testing.T) {
	cases := []struct {
		name          string
		stream        string
		wantHasTCP    bool
		wantHasUDP    bool
		wantHasFinal  bool
		wantUnchanged bool
	}{
		{
			name:         "reality + finalmask.tcp only — whole finalmask object goes",
			stream:       `{"security":"reality","network":"tcp","finalmask":{"tcp":[{"type":"xmc"}]}}`,
			wantHasTCP:   false,
			wantHasFinal: false,
		},
		{
			name:         "reality + both — tcp stripped, udp preserved",
			stream:       `{"security":"reality","finalmask":{"tcp":[{"type":"xmc"}],"udp":[{"type":"salamander"}]}}`,
			wantHasTCP:   false,
			wantHasUDP:   true,
			wantHasFinal: true,
		},
		{
			name:         "reality + finalmask.udp only — untouched (Hy2 obfs is PSP's own)",
			stream:       `{"security":"reality","finalmask":{"udp":[{"type":"salamander"}]}}`,
			wantHasUDP:   true,
			wantHasFinal: true,
		},
		{
			name:         "reality + EMPTY tcp array — no rewrite (mirrors upstream's len>0 guard)",
			stream:       `{"security":"reality","finalmask":{"tcp":[],"udp":[]}}`,
			wantHasTCP:   true,
			wantHasUDP:   true,
			wantHasFinal: true,
		},
		{
			name:       "tls + finalmask.tcp — NOT ours to touch, 3X-UI accepts it",
			stream:     `{"security":"tls","finalmask":{"tcp":[{"type":"xmc"}]}}`,
			wantHasTCP: true, wantHasUDP: false, wantHasFinal: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			n := &domain.Node{StreamSettings: tc.stream}
			got := SpecFromNode(n).StreamSettings

			var stream map[string]any
			if err := json.Unmarshal([]byte(got), &stream); err != nil {
				t.Fatalf("result is not valid JSON: %v (got %q)", err, got)
			}
			// Everything outside finalmask must survive verbatim.
			if _, ok := stream["security"]; !ok {
				t.Fatalf("security key was lost: %q", got)
			}
			fm, hasFinal := stream["finalmask"].(map[string]any)
			if hasFinal != tc.wantHasFinal {
				t.Fatalf("finalmask present = %v, want %v (got %q)", hasFinal, tc.wantHasFinal, got)
			}
			if !hasFinal {
				return
			}
			if _, ok := fm["tcp"]; ok != tc.wantHasTCP {
				t.Errorf("finalmask.tcp present = %v, want %v (got %q)", ok, tc.wantHasTCP, got)
			}
			if _, ok := fm["udp"]; ok != tc.wantHasUDP {
				t.Errorf("finalmask.udp present = %v, want %v (got %q)", ok, tc.wantHasUDP, got)
			}
		})
	}
}

// TestSpecFromNodePassesThroughUnparseableStream guards the degenerate inputs:
// the strip must never turn a stream string PSP could previously push into
// something different (or into "null"). Anything it cannot parse is forwarded
// byte-for-byte, so a stream shape we've never seen can still round-trip.
func TestSpecFromNodePassesThroughUnparseableStream(t *testing.T) {
	for _, s := range []string{"", "   ", "not json", `{"security":"reality"`, `[1,2,3]`, "null"} {
		n := &domain.Node{StreamSettings: s}
		if got := SpecFromNode(n).StreamSettings; got != s {
			t.Errorf("SpecFromNode(%q).StreamSettings = %q, want it passed through unchanged", s, got)
		}
	}
}
