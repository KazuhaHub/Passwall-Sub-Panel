package inboundcfg

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

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
