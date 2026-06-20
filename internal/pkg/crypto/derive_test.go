package crypto

import (
	"encoding/base64"
	"testing"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
)

// TestNewProxyPassword pins the v3.9.0 stored-password invariants: it is a
// valid 32-byte SS-2022 PSK (base64 of SHA-256), it equals the SS-2022-256
// derivation (so migration keeps such clients' passwords stable), it is
// deterministic, and it differs per UUID.
func TestNewProxyPassword(t *testing.T) {
	const uuid = "a265b1ec-cd81-43e7-8239-09f322ef22d6"
	got := NewProxyPassword(uuid)

	// 32 raw bytes once base64-decoded → a valid aes-256-gcm / chacha20 PSK.
	raw, err := base64.StdEncoding.DecodeString(got)
	if err != nil {
		t.Fatalf("not valid base64: %v", err)
	}
	if len(raw) != 32 {
		t.Fatalf("PSK length = %d bytes, want 32", len(raw))
	}

	// Must match the SS-2022-256 branch of DeriveProxyPassword so an existing
	// SS-2022-256 client keeps the same password across the v3.9.0 migration.
	if want := DeriveProxyPassword(uuid, domain.ProtoSS2022, "2022-blake3-aes-256-gcm"); got != want {
		t.Fatalf("NewProxyPassword=%q != SS-2022-256 derivation=%q", got, want)
	}

	// Deterministic + UUID-sensitive.
	if NewProxyPassword(uuid) != got {
		t.Fatal("not deterministic")
	}
	if NewProxyPassword("different-uuid") == got {
		t.Fatal("must differ per UUID")
	}
}

// TestDetectProtocol_All covers every protocol family the panel renders
// subscriptions for. Keeps the mapping pinned so a renaming somewhere in
// 3X-UI's wire protocol can't silently change the dispatch result.
func TestDetectProtocol_All(t *testing.T) {
	cases := []struct {
		name, in, method string
		want             domain.Protocol
	}{
		{"vless", "vless", "", domain.ProtoVLESS},
		{"vmess", "vmess", "", domain.ProtoVMess},
		{"trojan", "trojan", "", domain.ProtoTrojan},
		{"ss-legacy", "shadowsocks", "aes-256-gcm", domain.ProtoSS},
		{"ss2022", "shadowsocks", "2022-blake3-aes-128-gcm", domain.ProtoSS2022},
		{"hysteria2", "hysteria2", "", domain.ProtoHysteria2},
		{"case-insensitive", "VLESS", "", domain.ProtoVLESS},
		{"unknown", "dokodemo-door", "", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := DetectProtocol(tc.in, tc.method)
			if got != tc.want {
				t.Fatalf("DetectProtocol(%q,%q) = %q, want %q", tc.in, tc.method, got, tc.want)
			}
		})
	}
}

// TestDeriveProxyPassword_SS2022KeyLength pins the SIP022 PSK byte length per
// cipher: aes-128-gcm needs a 16-byte key, aes-256-gcm / chacha20-poly1305
// need 32. The PSK is base64(SHA-256(uuid)) truncated to that length; sending
// the wrong length makes Xray reject the client ("bad key length, required 16").
func TestDeriveProxyPassword_SS2022KeyLength(t *testing.T) {
	const uuid = "11111111-2222-3333-4444-555555555555"
	cases := []struct {
		name, method string
		wantBytes    int
	}{
		{"aes-128-gcm → 16 bytes", "2022-blake3-aes-128-gcm", 16},
		{"aes-256-gcm → 32 bytes", "2022-blake3-aes-256-gcm", 32},
		{"chacha20 → 32 bytes", "2022-blake3-chacha20-poly1305", 32},
		{"empty method falls back to 32", "", 32},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			psk := DeriveProxyPassword(uuid, domain.ProtoSS2022, tc.method)
			raw, err := base64.StdEncoding.DecodeString(psk)
			if err != nil {
				t.Fatalf("PSK %q is not valid base64: %v", psk, err)
			}
			if len(raw) != tc.wantBytes {
				t.Fatalf("method %q: decoded PSK is %d bytes, want %d", tc.method, len(raw), tc.wantBytes)
			}
		})
	}
}

// TestDeriveProxyPassword_NonSS2022 confirms ssMethod is ignored for protocols
// whose credential is the raw UUID (VLESS/VMess/Trojan/SS-legacy).
func TestDeriveProxyPassword_NonSS2022(t *testing.T) {
	const uuid = "abc-uuid"
	for _, p := range []domain.Protocol{domain.ProtoVLESS, domain.ProtoVMess, domain.ProtoTrojan, domain.ProtoSS} {
		if got := DeriveProxyPassword(uuid, p, "2022-blake3-aes-128-gcm"); got != uuid {
			t.Fatalf("protocol %q: got %q, want raw uuid %q", p, got, uuid)
		}
	}
}
