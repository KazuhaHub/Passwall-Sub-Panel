package clientplan

import (
	"encoding/base64"
	"testing"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/pkg/crypto"
)

const testUUID = "a265b1ec-cd81-43e7-8239-09f322ef22d6"

var testRules = domain.EmailRules{Domain: "psp.local"}

func direct(nodeID int64, p domain.Protocol) LineCred {
	return LineCred{Line: domain.DirectLine(nodeID), ListenerNodeID: nodeID, Protocol: p}
}

func relay(nodeID int64, id string, p domain.Protocol) LineCred {
	return LineCred{Line: domain.LineKey{NodeID: nodeID, LineID: id}, ListenerNodeID: nodeID, Protocol: p}
}

// One row per line, each on the line's listener. The direct line keeps the
// user's UUID (subscriptions of direct-only users do not change); a relay line
// gets its derived identity, pinned here as a golden.
func TestBuild_OneRowPerLine(t *testing.T) {
	got := Build(42, testUUID, 10, testRules, []LineCred{
		direct(7, domain.ProtoVLESS), relay(7, "r1", domain.ProtoVLESS), direct(8, domain.ProtoTrojan),
	}, nil, nil)
	if len(got) != 3 {
		t.Fatalf("rows = %d, want 3", len(got))
	}
	want := []struct {
		line  domain.LineKey
		uuid  string
		email string
	}{
		{domain.DirectLine(7), testUUID, "u42-l9719921a84@psp.local"},
		{domain.LineKey{NodeID: 7, LineID: "r1"}, "c4b0e7f9-05eb-40ce-a204-5a443f2ef4e3", "u42-l4163395866@psp.local"},
		{domain.DirectLine(8), testUUID, ""},
	}
	for i, w := range want {
		c := got[i].Client
		if c.NodeID != w.line.NodeID || c.LineID != w.line.LineID || c.UUID != w.uuid || c.ID != 0 {
			t.Fatalf("row %d = %+v, want line %+v uuid %s", i, c, w.line, w.uuid)
		}
		if w.email != "" && c.Email != w.email {
			t.Fatalf("row %d email = %s, want %s", i, c.Email, w.email)
		}
		if len(got[i].Inbounds) != 1 || got[i].Inbounds[0].NodeID != w.line.NodeID {
			t.Fatalf("row %d attachments = %+v, want only node %d", i, got[i].Inbounds, w.line.NodeID)
		}
	}
	if got[2].Client.Password != testUUID {
		t.Fatalf("trojan direct password = %s, want the raw UUID", got[2].Client.Password)
	}
}

// A line served by a listener of its own is attached there, and its identity
// is still derived from the line.
func TestBuild_LineOnItsOwnListener(t *testing.T) {
	lc := relay(7, "t1", domain.ProtoVLESS)
	lc.ListenerNodeID = 99
	got := Build(42, testUUID, 10, testRules, []LineCred{lc}, nil, nil)
	if got[0].Inbounds[0].NodeID != 99 || got[0].Client.NodeID != 7 || got[0].Client.LineID != "t1" {
		t.Fatalf("row = %+v / %+v", got[0].Client, got[0].Inbounds)
	}
	if got[0].Client.UUID != domain.DeriveLineUUID(testUUID, lc.Line, 1) {
		t.Fatalf("uuid = %s", got[0].Client.UUID)
	}
}

// SS-2022 takes its PSK from the ROW's UUID, so a relay line's key differs
// from the direct line's; flow follows the node.
func TestBuild_PasswordAndFlowPerRow(t *testing.T) {
	ss := LineCred{Line: domain.DirectLine(3), ListenerNodeID: 3, Protocol: domain.ProtoSS2022, SSMethod: "2022-blake3-aes-128-gcm"}
	ssRelay := ss
	ssRelay.Line = domain.LineKey{NodeID: 3, LineID: "r"}
	vision := LineCred{Line: domain.DirectLine(4), ListenerNodeID: 4, Protocol: domain.ProtoVLESS, Flow: flowVision}
	got := Build(42, testUUID, 10, testRules, []LineCred{ss, ssRelay, vision}, nil, nil)
	if got[0].Client.CredClass != pwClass128 || got[0].Client.Password != passwordForClass(pwClass128, testUUID) {
		t.Fatalf("direct ss row = %+v", got[0].Client)
	}
	assertPSKLen(t, got[0].Client.Password, 16)
	if got[1].Client.Password != passwordForClass(pwClass128, got[1].Client.UUID) || got[1].Client.Password == got[0].Client.Password {
		t.Fatalf("relay ss password must derive from its own UUID: %+v", got[1].Client)
	}
	if got[2].Inbounds[0].FlowOverride != flowVision || got[2].Client.CredClass != pwClassDefault {
		t.Fatalf("vision row = %+v %+v", got[2].Client, got[2].Inbounds)
	}
	if crypto.NewProxyPassword(testUUID) == "" {
		t.Fatal("crypto sanity")
	}
}

// Existing rows are kept by their line with their ID and email; a legacy row
// becomes the direct line of the lowest-ID node.
func TestBuild_KeepsRowsByLineAndAdoptsLegacy(t *testing.T) {
	existing := []ExistingClient{
		{ClientID: 50, Email: "u42@psp.local"}, // legacy shared row
		{ClientID: 61, Line: domain.LineKey{NodeID: 9, LineID: "r1"}, Email: "u42-old@psp.local"},
	}
	got := Build(42, testUUID, 10, testRules, []LineCred{
		direct(9, domain.ProtoVLESS), relay(9, "r1", domain.ProtoVLESS), direct(4, domain.ProtoVLESS),
	}, existing, nil)
	byLine := map[domain.LineKey]domain.PSPClient{}
	for _, d := range got {
		byLine[domain.LineKey{NodeID: d.Client.NodeID, LineID: d.Client.LineID}] = d.Client
	}
	if c := byLine[domain.DirectLine(4)]; c.ID != 50 || c.Email != "u42@psp.local" {
		t.Fatalf("legacy row should be node 4's direct line: %+v", c)
	}
	if c := byLine[domain.LineKey{NodeID: 9, LineID: "r1"}]; c.ID != 61 || c.Email != "u42-old@psp.local" {
		t.Fatalf("relay row not kept: %+v", c)
	}
	if c := byLine[domain.DirectLine(9)]; c.ID != 0 {
		t.Fatalf("node 9 direct should be new: %+v", c)
	}
}

// A derived identity that collides on its listener is re-derived; the row's
// own current UUID never counts as a collision.
func TestBuild_ReDerivesOnListenerCollision(t *testing.T) {
	line := domain.LineKey{NodeID: 7, LineID: "r1"}
	first := domain.DeriveLineUUID(testUUID, line, 1)
	taken := func(listener int64, uuid string, except int64) bool {
		return listener == 7 && domain.LineUUIDKey(uuid) == domain.LineUUIDKey(first) && except != 61
	}
	got := Build(42, testUUID, 10, testRules, []LineCred{relay(7, "r1", domain.ProtoVLESS)}, nil, taken)
	if got[0].Client.UUID != "d94e0af3-a2e2-49c4-a3b7-50ef6ad8c3ec" {
		t.Fatalf("re-derived uuid = %s, want attempt 2", got[0].Client.UUID)
	}
	kept := Build(42, testUUID, 10, testRules, []LineCred{relay(7, "r1", domain.ProtoVLESS)},
		[]ExistingClient{{ClientID: 61, Line: line, UUID: first}}, taken)
	if kept[0].Client.UUID != first {
		t.Fatalf("the row holding the identity lost it: %s", kept[0].Client.UUID)
	}
	// A row on attempt 2 keeps it after attempt 1 frees up: identities do not
	// move on their own.
	second := domain.DeriveLineUUID(testUUID, line, 2)
	stays := Build(42, testUUID, 10, testRules, []LineCred{relay(7, "r1", domain.ProtoVLESS)},
		[]ExistingClient{{ClientID: 62, Line: line, UUID: second}}, func(int64, string, int64) bool { return false })
	if stays[0].Client.UUID != second {
		t.Fatalf("kept identity moved back to attempt 1: %s", stays[0].Client.UUID)
	}
}

func TestIsSharedClientEmail(t *testing.T) {
	cases := []struct {
		email  string
		userID int64
		want   bool
	}{
		{"u18@psp.local", 18, true},              // bare / merged
		{"u18-kf2d62608@psp.local", 18, true},    // content-hash
		{"u18-c1@psp.local", 18, true},           // retired SS-2022-128 literal
		{"u18-c12@example.com", 18, true},        // -c{digits}, any domain
		{"u18-n5@psp.local", 18, false},          // legacy PER-NODE fallback — must NOT match
		{"u18-nodes@psp.local", 18, false},       // -n… is never a shared client
		{"u1@psp.local", 18, false},              // different user
		{"u180@psp.local", 18, false},            // u18 is NOT a prefix of u180
		{"u18-kf2d6260@psp.local", 18, false},    // 7 hex, not 8
		{"u18-kf2d62608g@psp.local", 18, false},  // 9 chars / non-hex
		{"Kazuha Home 0", 18, false},             // operator client, no @ / no scheme
		{"u18", 18, false},                       // no @
		{"u18-k49f8cae4@psp.local", 14, false},   // belongs to u18, reconciling u14
		{"u18-l4163395866@psp.local", 18, true},  // per-line (4.3)
		{"u18-l416339586@psp.local", 18, false},  // 9 hex
		{"u18-l416339586g@psp.local", 18, false}, // non-hex
	}
	for _, c := range cases {
		if got := IsSharedClientEmail(c.email, c.userID); got != c.want {
			t.Errorf("IsSharedClientEmail(%q, %d) = %v, want %v", c.email, c.userID, got, c.want)
		}
	}
}

// A new row records its line's factor, so a delta booked after the line is
// deleted keeps the line's price; a kept row is left to the poll.
func TestBuild_NewRowRecordsLineFactor(t *testing.T) {
	free := 0
	lc := relay(7, "r1", domain.ProtoVLESS)
	lc.FactorMilli = &free
	got := Build(42, testUUID, 10, testRules, []LineCred{lc, direct(8, domain.ProtoVLESS)}, nil, nil)
	if got[0].Client.LastFactor() != 0 || got[1].Client.LastFactorMilli != nil {
		t.Fatalf("rows = %+v / %+v", got[0].Client, got[1].Client)
	}
	kept := Build(42, testUUID, 10, testRules, []LineCred{lc}, []ExistingClient{{ClientID: 3, Line: lc.Line}}, nil)
	if kept[0].Client.LastFactorMilli != nil {
		t.Fatal("a kept row's booked factor was overwritten by the planner")
	}
}

func TestBuild_EmptyLinesYieldNoClients(t *testing.T) {
	if got := Build(42, testUUID, 10, testRules, nil, nil, nil); got != nil {
		t.Fatalf("want nil, got %+v", got)
	}
}

func TestLineCredsFromNodes(t *testing.T) {
	nodes := []*domain.Node{
		{ID: 1, DesiredProtocol: "vless", Relays: []domain.RelayLine{
			{ID: "a", Enabled: true}, {ID: "off", Enabled: false}, {ID: "", Enabled: true},
		}},
		{ID: 2, Kind: domain.NodeKindSeparator, DesiredProtocol: "vless"},
		{ID: 3, DesiredProtocol: ""},
	}
	got := LineCredsFromNodes(nodes)
	if len(got) != 2 || got[0].Line != domain.DirectLine(1) || got[1].Line != (domain.LineKey{NodeID: 1, LineID: "a"}) ||
		got[1].ListenerNodeID != 1 {
		t.Fatalf("lines = %+v", got)
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
		{"vless", &domain.Node{ID: 1, DesiredProtocol: "vless", Flow: "xtls-rprx-vision"}, domain.ProtoVLESS, "", "xtls-rprx-vision"},
		{"trojan", &domain.Node{ID: 2, DesiredProtocol: "trojan"}, domain.ProtoTrojan, "", ""},
		{"ss2022-256", &domain.Node{ID: 3, DesiredProtocol: "shadowsocks", InboundSettings: `{"method":"2022-blake3-aes-256-gcm"}`}, domain.ProtoSS2022, "2022-blake3-aes-256-gcm", ""},
		{"ss2022-128", &domain.Node{ID: 4, DesiredProtocol: "shadowsocks", InboundSettings: `{"method":"2022-blake3-aes-128-gcm"}`}, domain.ProtoSS2022, "2022-blake3-aes-128-gcm", ""},
		{"plain-ss", &domain.Node{ID: 5, DesiredProtocol: "shadowsocks", InboundSettings: `{"method":"aes-256-gcm"}`}, domain.ProtoSS, "aes-256-gcm", ""},
		// Uncaptured shadowsocks (no settings) → classified as plain SS (documented caveat).
		{"ss-uncaptured", &domain.Node{ID: 6, DesiredProtocol: "shadowsocks"}, domain.ProtoSS, "", ""},
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
		{ID: 1, DesiredProtocol: "vless"},
		{ID: 2, Kind: domain.NodeKindSeparator, DesiredProtocol: "vless"}, // separator → skipped
		{ID: 3, DesiredProtocol: ""},                                      // unknown protocol → skipped
		nil,                                                               // nil → skipped
		{ID: 5, DesiredProtocol: "trojan"},
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
