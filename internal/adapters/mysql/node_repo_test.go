package mysql

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
)

func newNodeTestRepo(t *testing.T) (*nodeRepo, context.Context) {
	t.Helper()
	db, err := Open("sqlite", filepath.Join(t.TempDir(), "panel.db"))
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
