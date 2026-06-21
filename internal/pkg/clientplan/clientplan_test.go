package clientplan

import (
	"encoding/base64"
	"testing"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/pkg/crypto"
)

const testUUID = "a265b1ec-cd81-43e7-8239-09f322ef22d6"

var testRules = domain.EmailRules{Domain: "psp.local"}

// The common case: VLESS (no flow) / Trojan / SS / Hysteria2 collapse to ONE
// shared client whose password is the RAW UUID — byte-identical to the legacy
// per-node derivation, so the migration is silent.
func TestBuild_MixedProtocolsCollapseToOneClient(t *testing.T) {
	nodes := []NodeCred{
		{NodeID: 1, Protocol: domain.ProtoVLESS}, // no flow → default class
		{NodeID: 2, Protocol: domain.ProtoTrojan},
		{NodeID: 3, Protocol: domain.ProtoSS},
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
	if c.Client.Password != testUUID {
		t.Fatalf("default-class password must be the raw UUID (silent), got %q", c.Client.Password)
	}
	if len(c.Inbounds) != 4 {
		t.Fatalf("attachment set size = %d, want 4", len(c.Inbounds))
	}
}

// HOLE #8: a client carries a single flow and 3X-UI has no per-inbound
// flowOverride API, so VLESS nodes with DIFFERENT flow can't share a client. A
// user with a VLESS-vision node + (VLESS-noflow + Trojan) gets TWO clients: the
// default (u{uid}@) and a flow-split one (stable -k{hash} email). Both keep the
// raw-UUID password — flow never changes the credential.
func TestBuild_VLESSFlowSplitsClient(t *testing.T) {
	nodes := []NodeCred{
		{NodeID: 1, Protocol: domain.ProtoVLESS, Flow: "xtls-rprx-vision"}, // vision → its own client
		{NodeID: 2, Protocol: domain.ProtoVLESS},                           // no flow → default
		{NodeID: 3, Protocol: domain.ProtoTrojan},                          // no flow → default
	}
	got := Build(42, testUUID, 10, testRules, nodes)
	if len(got) != 2 {
		t.Fatalf("want 2 clients (flow split), got %d", len(got))
	}
	// Sorted by (pwClass, flow): default (flow "") first, vision second.
	def, vis := got[0], got[1]
	if def.Client.Email != "u42@psp.local" {
		t.Fatalf("default client email = %q, want u42@psp.local", def.Client.Email)
	}
	if len(def.Inbounds) != 2 || def.Inbounds[0].NodeID != 2 || def.Inbounds[1].NodeID != 3 {
		t.Fatalf("default client should hold the no-flow nodes 2,3: %+v", def.Inbounds)
	}
	if vis.Client.CredClass != 0 {
		t.Fatalf("flow split keeps pwClass 0, got %d", vis.Client.CredClass)
	}
	if vis.Client.Email == def.Client.Email || vis.Client.Email[:5] != "u42-k" {
		t.Fatalf("flow-split client needs a distinct -k email: %q", vis.Client.Email)
	}
	if len(vis.Inbounds) != 1 || vis.Inbounds[0].NodeID != 1 || vis.Inbounds[0].FlowOverride != "xtls-rprx-vision" {
		t.Fatalf("vision client should hold node 1 with vision flow: %+v", vis.Inbounds)
	}
	if def.Client.Password != testUUID || vis.Client.Password != testUUID {
		t.Fatal("flow split must keep the raw-UUID password (silent)")
	}
}

// SS-2022 needs a real PSK, not the raw UUID, so each key length splits off its
// own client. A user with VLESS + SS-2022-256 + SS-2022-128 gets THREE clients:
// default (uuid), pwClass256 (32B PSK), pwClass128 (16B PSK).
func TestBuild_SS2022SplitsByKeyLength(t *testing.T) {
	nodes := []NodeCred{
		{NodeID: 1, Protocol: domain.ProtoVLESS},
		{NodeID: 2, Protocol: domain.ProtoSS2022, SSMethod: "2022-blake3-aes-256-gcm"}, // 32B → pwClass256
		{NodeID: 3, Protocol: domain.ProtoSS2022, SSMethod: "2022-blake3-aes-128-gcm"}, // 16B → pwClass128
	}
	got := Build(42, testUUID, 10, testRules, nodes)
	if len(got) != 3 {
		t.Fatalf("want 3 clients, got %d", len(got))
	}
	// Sorted by pwClass: 0 (uuid), 1 (256), 2 (128).
	def, ss256, ss128 := got[0], got[1], got[2]
	if def.Client.CredClass != 0 || def.Client.Password != testUUID {
		t.Fatalf("default class must be raw-UUID: %+v", def.Client)
	}
	if def.Client.Email != "u42@psp.local" || len(def.Inbounds) != 1 || def.Inbounds[0].NodeID != 1 {
		t.Fatalf("default should hold VLESS node 1: %+v", def)
	}
	if ss256.Client.CredClass != 1 || ss256.Client.Password != crypto.NewProxyPassword(testUUID) {
		t.Fatalf("ss256 class wrong: %+v", ss256.Client)
	}
	assertPSKLen(t, ss256.Client.Password, 32)
	if len(ss256.Inbounds) != 1 || ss256.Inbounds[0].NodeID != 2 {
		t.Fatalf("ss256 should hold node 2: %+v", ss256.Inbounds)
	}
	if ss128.Client.CredClass != 2 {
		t.Fatalf("ss128 class wrong: %+v", ss128.Client)
	}
	assertPSKLen(t, ss128.Client.Password, 16)
	if len(ss128.Inbounds) != 1 || ss128.Inbounds[0].NodeID != 3 {
		t.Fatalf("ss128 should hold node 3: %+v", ss128.Inbounds)
	}
	// The two SS-2022 clients get distinct hash emails, never the default u42@.
	if ss256.Client.Email == "u42@psp.local" || ss128.Client.Email == "u42@psp.local" {
		t.Fatal("SS-2022 clients must not reuse the default email")
	}
	if ss256.Client.Email == ss128.Client.Email {
		t.Fatal("256 and 128 clients must have distinct emails")
	}
}

// A user with only SS-2022-128 nodes gets a single pwClass128 (CredClass 2)
// client, since there is nothing in the default class.
func TestBuild_OnlySS2022_128(t *testing.T) {
	nodes := []NodeCred{
		{NodeID: 7, Protocol: domain.ProtoSS2022, SSMethod: "2022-blake3-aes-128-gcm"},
	}
	got := Build(42, testUUID, 10, testRules, nodes)
	if len(got) != 1 || got[0].Client.CredClass != 2 {
		t.Fatalf("want a single pwClass128 (CredClass 2) client, got %+v", got)
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
