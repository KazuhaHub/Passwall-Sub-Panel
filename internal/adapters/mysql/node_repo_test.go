package mysql

import (
	"context"
	"path/filepath"
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
