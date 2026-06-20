package clientplan

import (
	"encoding/base64"
	"testing"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/pkg/crypto"
)

const testUUID = "a265b1ec-cd81-43e7-8239-09f322ef22d6"

var testRules = domain.EmailRules{Domain: "psp.local"}

// The common case: a mix of VLESS / Trojan / SS / SS-2022-256 / Hysteria2 nodes
// collapses to ONE shared client, attached to all of them, with the 32-byte
// stored password.
func TestBuild_MixedProtocolsCollapseToOneClient(t *testing.T) {
	nodes := []NodeCred{
		{NodeID: 1, Protocol: domain.ProtoVLESS, Flow: "xtls-rprx-vision"},
		{NodeID: 2, Protocol: domain.ProtoTrojan},
		{NodeID: 3, Protocol: domain.ProtoSS},
		{NodeID: 4, Protocol: domain.ProtoSS2022, SSMethod: "2022-blake3-aes-256-gcm"},
		{NodeID: 5, Protocol: domain.ProtoHysteria2},
	}
	got := Build(42, testUUID, 10, testRules, nodes)
	if len(got) != 1 {
		t.Fatalf("want 1 shared client, got %d", len(got))
	}
	c := got[0]
	if c.Client.CredClass != 0 || c.Client.Email != "u42@psp.local" {
		t.Fatalf("client identity = %+v", c.Client)
	}
	if c.Client.UUID != testUUID {
		t.Fatalf("uuid = %q, want user uuid", c.Client.UUID)
	}
	if c.Client.Password != crypto.NewProxyPassword(testUUID) {
		t.Fatalf("password is not the 32-byte stored value")
	}
	if len(c.Inbounds) != 5 {
		t.Fatalf("attachment set size = %d, want 5", len(c.Inbounds))
	}
	// VLESS flow rides through as a per-attachment override.
	if c.Inbounds[0].NodeID != 1 || c.Inbounds[0].FlowOverride != "xtls-rprx-vision" {
		t.Fatalf("flow override not carried: %+v", c.Inbounds[0])
	}
}

// SS-2022-128 cannot share the password field with the 32-byte protocols, so a
// user with both gets exactly two clients (credClass 0 and 1), partitioned
// correctly, each with the right-length PSK.
func TestBuild_SS2022_128_SplitsIntoSecondClient(t *testing.T) {
	nodes := []NodeCred{
		{NodeID: 1, Protocol: domain.ProtoVLESS},
		{NodeID: 2, Protocol: domain.ProtoSS2022, SSMethod: "2022-blake3-aes-256-gcm"}, // 32B → class 0
		{NodeID: 3, Protocol: domain.ProtoSS2022, SSMethod: "2022-blake3-aes-128-gcm"}, // 16B → class 1
	}
	got := Build(42, testUUID, 10, testRules, nodes)
	if len(got) != 2 {
		t.Fatalf("want 2 clients (128 split), got %d", len(got))
	}
	// Ordered by CredClass ascending.
	c0, c1 := got[0], got[1]
	if c0.Client.CredClass != 0 || c1.Client.CredClass != 1 {
		t.Fatalf("classes = %d, %d", c0.Client.CredClass, c1.Client.CredClass)
	}
	if c0.Client.Email != "u42@psp.local" || c1.Client.Email != "u42-c1@psp.local" {
		t.Fatalf("emails = %q, %q", c0.Client.Email, c1.Client.Email)
	}
	// class 0 holds the VLESS + SS-2022-256 nodes; class 1 holds only the 128 node.
	if len(c0.Inbounds) != 2 || len(c1.Inbounds) != 1 || c1.Inbounds[0].NodeID != 3 {
		t.Fatalf("partition wrong: c0=%+v c1=%+v", c0.Inbounds, c1.Inbounds)
	}
	// The 128 client's password decodes to 16 bytes; the default client's to 32.
	assertPSKLen(t, c0.Client.Password, 32)
	assertPSKLen(t, c1.Client.Password, 16)
	// Both clients share the user's UUID (VLESS/VMess id).
	if c0.Client.UUID != testUUID || c1.Client.UUID != testUUID {
		t.Fatal("both clients must carry the user's UUID")
	}
}

// A user with only SS-2022-128 nodes gets a single class-0... no: it's the 128
// class. Verify the lone-128 case still produces one (class-1) client, since
// there is nothing in the default class.
func TestBuild_OnlySS2022_128(t *testing.T) {
	nodes := []NodeCred{
		{NodeID: 7, Protocol: domain.ProtoSS2022, SSMethod: "2022-blake3-aes-128-gcm"},
	}
	got := Build(42, testUUID, 10, testRules, nodes)
	if len(got) != 1 || got[0].Client.CredClass != 1 {
		t.Fatalf("want a single class-1 client, got %+v", got)
	}
	assertPSKLen(t, got[0].Client.Password, 16)
}

func TestBuild_EmptyNodesYieldsNoClients(t *testing.T) {
	if got := Build(42, testUUID, 10, testRules, nil); got != nil {
		t.Fatalf("empty nodes should yield no clients, got %+v", got)
	}
}

func TestBuild_Deterministic(t *testing.T) {
	nodes := []NodeCred{
		{NodeID: 1, Protocol: domain.ProtoVLESS},
		{NodeID: 2, Protocol: domain.ProtoSS2022, SSMethod: "2022-blake3-aes-128-gcm"},
	}
	a := Build(42, testUUID, 10, testRules, nodes)
	b := Build(42, testUUID, 10, testRules, nodes)
	if len(a) != len(b) {
		t.Fatalf("non-deterministic length")
	}
	for i := range a {
		if a[i].Client.Email != b[i].Client.Email || a[i].Client.Password != b[i].Client.Password {
			t.Fatalf("non-deterministic at %d", i)
		}
	}
}

func TestNodeCredFromNode(t *testing.T) {
	cases := []struct {
		name      string
		node      *domain.Node
		wantProto domain.Protocol
		wantSS    string
		wantFlow  string
	}{
		{"vless", &domain.Node{ID: 1, Protocol: "vless", Flow: "xtls-rprx-vision"}, domain.ProtoVLESS, "", "xtls-rprx-vision"},
		{"trojan", &domain.Node{ID: 2, Protocol: "trojan"}, domain.ProtoTrojan, "", ""},
		{"ss2022-256", &domain.Node{ID: 3, Protocol: "shadowsocks", InboundSettings: `{"method":"2022-blake3-aes-256-gcm"}`}, domain.ProtoSS2022, "2022-blake3-aes-256-gcm", ""},
		{"ss2022-128", &domain.Node{ID: 4, Protocol: "shadowsocks", InboundSettings: `{"method":"2022-blake3-aes-128-gcm"}`}, domain.ProtoSS2022, "2022-blake3-aes-128-gcm", ""},
		{"plain-ss", &domain.Node{ID: 5, Protocol: "shadowsocks", InboundSettings: `{"method":"aes-256-gcm"}`}, domain.ProtoSS, "aes-256-gcm", ""},
		// Uncaptured shadowsocks (no settings) → classified as plain SS (documented caveat).
		{"ss-uncaptured", &domain.Node{ID: 6, Protocol: "shadowsocks"}, domain.ProtoSS, "", ""},
	}
	for _, tc := range cases {
		got := NodeCredFromNode(tc.node)
		if got.Protocol != tc.wantProto || got.SSMethod != tc.wantSS || got.Flow != tc.wantFlow {
			t.Errorf("%s: got %+v, want proto=%s ss=%q flow=%q", tc.name, got, tc.wantProto, tc.wantSS, tc.wantFlow)
		}
		if got.NodeID != tc.node.ID {
			t.Errorf("%s: NodeID = %d, want %d", tc.name, got.NodeID, tc.node.ID)
		}
	}
}

func TestNodeCredsFromNodes_SkipsSeparatorsAndUnknown(t *testing.T) {
	nodes := []*domain.Node{
		{ID: 1, Protocol: "vless"},
		{ID: 2, Kind: domain.NodeKindSeparator, Protocol: "vless"}, // separator → skipped
		{ID: 3, Protocol: ""},                                      // unknown protocol → skipped
		nil,                                                        // nil → skipped
		{ID: 5, Protocol: "trojan"},
	}
	got := NodeCredsFromNodes(nodes)
	if len(got) != 2 {
		t.Fatalf("want 2 creds (vless + trojan), got %d: %+v", len(got), got)
	}
	if got[0].NodeID != 1 || got[1].NodeID != 5 {
		t.Fatalf("wrong nodes kept: %+v", got)
	}
}

func assertPSKLen(t *testing.T, password string, wantBytes int) {
	t.Helper()
	raw, err := base64.StdEncoding.DecodeString(password)
	if err != nil {
		t.Fatalf("password not base64: %v", err)
	}
	if len(raw) != wantBytes {
		t.Fatalf("PSK = %d bytes, want %d", len(raw), wantBytes)
	}
}
