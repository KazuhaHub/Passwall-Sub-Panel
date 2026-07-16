package sqlstore

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
)

func newNodeTestRepo(t *testing.T) (*nodeRepo, context.Context) {
	t.Helper()
	db, err := openTestDB(t)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	// Windows can't unlink the .db file while the sqlite handle is open —
	// t.TempDir's auto-cleanup would otherwise fail.
	sqlDB, _ := db.DB()
	t.Cleanup(func() { _ = sqlDB.Close() })
	if err := EnsureSchema(db); err != nil {
		t.Fatalf("schema: %v", err)
	}
	return &nodeRepo{db: db}, context.Background()
}

// TestNodeRepo_UpdateMetadataPreservesPollColumns pins the column-scoped
// node-edit write: changing identity fields must NOT roll back poll-owned
// columns (traffic counters, health) to the stale snapshot the edit dialog
// loaded. A full-row Save would zero them — this is the regression guard.
func TestNodeRepo_UpdateMetadataPreservesPollColumns(t *testing.T) {
	repo, ctx := newNodeTestRepo(t)

	n := &domain.Node{PanelID: 1, InboundID: 2, DisplayName: "Old", Region: "TW", Tags: []string{"a"}}
	if err := repo.Create(ctx, n); err != nil {
		t.Fatalf("create: %v", err)
	}
	// Poll/health writers advance these out-of-band.
	n.LifetimeUpBytes, n.LifetimeDownBytes, n.LifetimeTotalBytes = 100, 200, 300
	n.LastTrafficTotalBytes = 300
	if err := repo.UpdateTrafficCounters(ctx, n); err != nil {
		t.Fatalf("counters: %v", err)
	}
	n.HealthState, n.HealthDetail = "healthy", "ok"
	n.RelayHealth = []domain.RelayHealth{{Index: 0, Address: "old.relay", Port: 443, State: domain.NodeHealthOK}}
	if err := repo.UpdateHealth(ctx, n); err != nil {
		t.Fatalf("health: %v", err)
	}

	// Admin edits the name from a stale dialog snapshot that carries ZERO
	// counters/health (as the edit form would).
	edit := &domain.Node{
		ID: n.ID, PanelID: 1, InboundID: 2,
		DisplayName: "New", ServerAddress: "n.example.com", Region: "HK", Tags: []string{"b", "c"}, SortOrder: 7,
		Relays: []domain.RelayLine{{Name: "new", Address: "new.relay", Port: 8443, Enabled: true}}, ShowRelayStatus: true,
		// Lifetime*/Health* deliberately zero — must not be persisted.
	}
	if err := repo.UpdateMetadata(ctx, edit); err != nil {
		t.Fatalf("update metadata: %v", err)
	}

	got, err := repo.GetByID(ctx, n.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	// Identity fields updated.
	if got.DisplayName != "New" || got.Region != "HK" || got.ServerAddress != "n.example.com" || got.SortOrder != 7 {
		t.Errorf("identity not updated: %+v", got)
	}
	if len(got.Tags) != 2 || got.Tags[0] != "b" {
		t.Errorf("tags not updated: %v", got.Tags)
	}
	// Poll-owned columns preserved (NOT clobbered to the edit's zeros).
	if got.LifetimeTotalBytes != 300 || got.LastTrafficTotalBytes != 300 {
		t.Errorf("traffic counters clobbered: lifetime=%d last=%d, want 300/300", got.LifetimeTotalBytes, got.LastTrafficTotalBytes)
	}
	if len(got.RelayHealth) != 1 || got.RelayHealth[0].Address != "old.relay" {
		t.Errorf("relay health clobbered: %#v", got.RelayHealth)
	}
	if len(got.Relays) != 1 || got.Relays[0].Address != "new.relay" || !got.ShowRelayStatus {
		t.Errorf("relay metadata not updated: relays=%#v show=%v", got.Relays, got.ShowRelayStatus)
	}
	if got.HealthState != "healthy" {
		t.Errorf("health clobbered: %q, want healthy", got.HealthState)
	}
}

// TestNodeRepo_ProtocolRoundTrips verifies the protocol column is migrated
// and survives Create → GetByID, and that Update can change it. This is the
// value the UI relies on to gate VLESS-only fields (e.g. Flow) without a
// live 3X-UI fetch.
func TestNodeRepo_ProtocolRoundTrips(t *testing.T) {
	repo, ctx := newNodeTestRepo(t)

	n := &domain.Node{
		PanelID:       1,
		InboundID:     2,
		DisplayName:   "SS-TCP",
		ServerAddress: "node.example.com",
		Protocol:      "shadowsocks",
		Region:        "TW",
	}
	if err := repo.Create(ctx, n); err != nil {
		t.Fatalf("create: %v", err)
	}
	got, err := repo.GetByID(ctx, n.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Protocol != "shadowsocks" {
		t.Fatalf("protocol = %q, want shadowsocks", got.Protocol)
	}

	// Backfill / change path (mirrors UpdateInboundConfig switching protocol).
	got.Protocol = "vless"
	if err := repo.Update(ctx, got); err != nil {
		t.Fatalf("update: %v", err)
	}
	again, err := repo.GetByID(ctx, n.ID)
	if err != nil {
		t.Fatalf("get after update: %v", err)
	}
	if again.Protocol != "vless" {
		t.Fatalf("protocol after update = %q, want vless", again.Protocol)
	}
}

// TestNodeRepo_CreateAppendsToBottom pins the "new nodes default to the bottom"
// rule: Create with sort_order <= 0 assigns max(sort_order)+10, while an
// explicit positive value is kept verbatim.
func TestNodeRepo_CreateAppendsToBottom(t *testing.T) {
	repo, ctx := newNodeTestRepo(t)
	inb := 0
	mk := func(name string, sort int) *domain.Node {
		inb++ // distinct inbound_id per node — (panel_id, inbound_id) is unique
		return &domain.Node{PanelID: 1, InboundID: inb, DisplayName: name, ServerAddress: "x", Region: "X", SortOrder: sort}
	}

	// First node into an empty table: 0 + 10 = 10.
	a := mk("a", 0)
	if err := repo.Create(ctx, a); err != nil {
		t.Fatalf("create a: %v", err)
	}
	if a.SortOrder != 10 {
		t.Fatalf("first auto node sort_order = %d, want 10", a.SortOrder)
	}
	// Second auto node: max(10)+10 = 20.
	b := mk("b", 0)
	if err := repo.Create(ctx, b); err != nil {
		t.Fatalf("create b: %v", err)
	}
	if b.SortOrder != 20 {
		t.Fatalf("second auto node sort_order = %d, want 20", b.SortOrder)
	}
	// Explicit value is respected (not overridden).
	c := mk("c", 5)
	if err := repo.Create(ctx, c); err != nil {
		t.Fatalf("create c: %v", err)
	}
	if c.SortOrder != 5 {
		t.Fatalf("explicit sort_order = %d, want 5 (kept)", c.SortOrder)
	}
	// Next auto node appends after the current max (20): 30.
	d := mk("d", 0)
	if err := repo.Create(ctx, d); err != nil {
		t.Fatalf("create d: %v", err)
	}
	if d.SortOrder != 30 {
		t.Fatalf("auto node after explicit = %d, want 30 (max 20 + 10)", d.SortOrder)
	}
}

// TestNodeRepo_RelaysRoundTrip pins the v3.8.0 transit-line persistence: a
// node's Relays slice + HideDirect flag survive a write→read→update cycle
// across every dialect (the JSON column is the Postgres-strict path — text,
// not an inferred composite/array). A relay-less node reads back with no
// relays and HideDirect false.
func TestNodeRepo_RelaysRoundTrip(t *testing.T) {
	repo, ctx := newNodeTestRepo(t)

	n := &domain.Node{
		PanelID:         1,
		InboundID:       2,
		DisplayName:     "HK",
		ServerAddress:   "land.example.com",
		Protocol:        "vless",
		Region:          "HK",
		HideDirect:      true,
		ShowRelayStatus: true,
		Relays: []domain.RelayLine{
			{Name: "广州移动中转", Address: "gz.relay.cn", Port: 20001, Enabled: true},
			{Name: "CDN优选", Address: "104.16.0.1", Port: 8443, SNI: "cdn.example.com", Host: "cdn.example.com", Enabled: false},
		},
	}
	if err := repo.Create(ctx, n); err != nil {
		t.Fatalf("create: %v", err)
	}
	got, err := repo.GetByID(ctx, n.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if !got.HideDirect {
		t.Fatalf("HideDirect = false, want true")
	}
	if !got.ShowRelayStatus {
		t.Fatal("ShowRelayStatus = false, want true")
	}
	if !reflect.DeepEqual(got.Relays, n.Relays) {
		t.Fatalf("relays round-trip mismatch:\n got %#v\nwant %#v", got.Relays, n.Relays)
	}

	// Update path: drop one line, flip a toggle.
	got.Relays = []domain.RelayLine{{Name: "上海电信", Address: "sh.relay.cn", Port: 30001, Enabled: true}}
	got.HideDirect = false
	got.ShowRelayStatus = false
	if err := repo.Update(ctx, got); err != nil {
		t.Fatalf("update: %v", err)
	}
	again, err := repo.GetByID(ctx, n.ID)
	if err != nil {
		t.Fatalf("get after update: %v", err)
	}
	if again.HideDirect {
		t.Fatalf("HideDirect after update = true, want false")
	}
	if again.ShowRelayStatus {
		t.Fatal("ShowRelayStatus after update = true, want false")
	}
	if !reflect.DeepEqual(again.Relays, got.Relays) {
		t.Fatalf("relays after update mismatch:\n got %#v\nwant %#v", again.Relays, got.Relays)
	}

	// Relay-less node: nil relays, HideDirect false.
	plain := &domain.Node{PanelID: 1, InboundID: 3, DisplayName: "JP", ServerAddress: "jp.example.com", Region: "JP"}
	if err := repo.Create(ctx, plain); err != nil {
		t.Fatalf("create plain: %v", err)
	}
	gotPlain, err := repo.GetByID(ctx, plain.ID)
	if err != nil {
		t.Fatalf("get plain: %v", err)
	}
	if len(gotPlain.Relays) != 0 || gotPlain.HideDirect {
		t.Fatalf("relay-less node = %#v, want no relays + HideDirect false", gotPlain)
	}
}

// TestNodeRepo_InboundSecretsRoundTripEncrypted verifies the v3.5 trust
// boundary: server-identity material (SS-2022 server PSK in inbound_settings,
// Reality privateKey / inline TLS keys in stream_settings) is stored
// AES-GCM-encrypted at rest and transparently decrypted on read. The check
// is two-pronged: (1) the value round-trips through domain (write → read =
// equal), (2) the underlying TEXT column carries the enc:v1: prefix so a
// dump of the DB never exposes the secret in plaintext.
func TestNodeRepo_InboundSecretsRoundTripEncrypted(t *testing.T) {
	// 32 random bytes; deterministic for the test but treated like a real key.
	ConfigureSecretKey("test-key-material-do-not-use-in-prod")
	t.Cleanup(func() { ConfigureSecretKey("") })

	repo, ctx := newNodeTestRepo(t)

	const ssPSK = "server-psk-must-not-leak"
	const realityPriv = "reality-private-key-must-not-leak"
	const inboundSettings = `{"method":"2022-blake3-aes-256-gcm","password":"` + ssPSK + `"}`
	const streamSettings = `{"network":"tcp","security":"reality","realitySettings":{"privateKey":"` + realityPriv + `"}}`

	n := &domain.Node{
		PanelID: 1, InboundID: 2, DisplayName: "ss2022", Region: "TW",
		Protocol: "shadowsocks", Port: 8388,
		InboundSettings: inboundSettings,
		StreamSettings:  streamSettings,
	}
	if err := repo.Create(ctx, n); err != nil {
		t.Fatalf("create: %v", err)
	}

	// (1) Round-trip: GetByID returns the plaintext values.
	got, err := repo.GetByID(ctx, n.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.InboundSettings != inboundSettings {
		t.Fatalf("inbound_settings did not round-trip: got %q", got.InboundSettings)
	}
	if got.StreamSettings != streamSettings {
		t.Fatalf("stream_settings did not round-trip: got %q", got.StreamSettings)
	}

	// (2) At-rest check: read the raw columns and verify they're prefixed
	// with the enc:v1: marker — the plaintext secret must not appear.
	var raw struct {
		InboundSettings string `gorm:"column:inbound_settings"`
		StreamSettings  string `gorm:"column:stream_settings"`
	}
	if err := repo.db.Table("nodes").Where("id = ?", n.ID).First(&raw).Error; err != nil {
		t.Fatalf("read raw row: %v", err)
	}
	if !strings.HasPrefix(raw.InboundSettings, secretPrefix) {
		t.Fatalf("inbound_settings stored unencrypted (prefix=%q): %q", secretPrefix, raw.InboundSettings)
	}
	if !strings.HasPrefix(raw.StreamSettings, secretPrefix) {
		t.Fatalf("stream_settings stored unencrypted: %q", raw.StreamSettings)
	}
	if strings.Contains(raw.InboundSettings, ssPSK) {
		t.Fatalf("SS-2022 server PSK leaked plaintext into stored row")
	}
	if strings.Contains(raw.StreamSettings, realityPriv) {
		t.Fatalf("Reality privateKey leaked plaintext into stored row")
	}

	// UpdateInboundConfig (column-scoped writer) must produce the same
	// encrypted-at-rest result — write a fresh secret and re-verify.
	got.InboundSettings = `{"method":"aes-128-gcm","password":"rotated-psk"}`
	if err := repo.UpdateInboundConfig(ctx, got); err != nil {
		t.Fatalf("UpdateInboundConfig: %v", err)
	}
	if err := repo.db.Table("nodes").Where("id = ?", n.ID).First(&raw).Error; err != nil {
		t.Fatalf("read raw row after update: %v", err)
	}
	if !strings.HasPrefix(raw.InboundSettings, secretPrefix) {
		t.Fatalf("UpdateInboundConfig wrote plaintext: %q", raw.InboundSettings)
	}
	if strings.Contains(raw.InboundSettings, "rotated-psk") {
		t.Fatalf("rotated PSK leaked plaintext after UpdateInboundConfig")
	}
}

// TestNodeRepo_InboundSecretsLegacyPlaintextStillReads verifies the soft
// migration story: rows written before v3.5 (without enc:v1: prefix) keep
// reading back unchanged. New writes always encrypt, old reads passthrough.
func TestNodeRepo_InboundSecretsLegacyPlaintextStillReads(t *testing.T) {
	ConfigureSecretKey("test-key-material-do-not-use-in-prod")
	t.Cleanup(func() { ConfigureSecretKey("") })

	repo, ctx := newNodeTestRepo(t)

	// Simulate a pre-v3.5 row by writing the columns directly (no encryption).
	row := &nodeRow{
		PanelID: 1, InboundID: 9, DisplayName: "legacy-ss", Region: "JP",
		Protocol:        "shadowsocks",
		InboundSettings: `{"method":"aes-128-gcm","password":"old-plain-psk"}`,
		StreamSettings:  `{"network":"tcp"}`,
	}
	if err := repo.db.Create(row).Error; err != nil {
		t.Fatalf("seed plaintext row: %v", err)
	}

	got, err := repo.GetByID(ctx, row.ID)
	if err != nil {
		t.Fatalf("get legacy: %v", err)
	}
	if !strings.Contains(got.InboundSettings, "old-plain-psk") {
		t.Fatalf("legacy plaintext row must read back unchanged, got %q", got.InboundSettings)
	}
}

// TestNodeRepo_ProtocolEmptyForLegacyRows documents the agreed fallback:
// a node written without a protocol (e.g. imported before the column
// existed) reads back as empty, which the UI treats as "unknown" and keeps
// the Flow field visible until the row self-heals.
func TestNodeRepo_ProtocolEmptyForLegacyRows(t *testing.T) {
	repo, ctx := newNodeTestRepo(t)

	n := &domain.Node{
		PanelID:       3,
		InboundID:     4,
		DisplayName:   "legacy",
		ServerAddress: "1.2.3.4",
		Region:        "CA",
	}
	if err := repo.Create(ctx, n); err != nil {
		t.Fatalf("create: %v", err)
	}
	got, err := repo.GetByID(ctx, n.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Protocol != "" {
		t.Fatalf("protocol = %q, want empty", got.Protocol)
	}
}
