package sqlstore

import (
	"context"
	"errors"
	"testing"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
)

func newPSPClientTestRepo(t *testing.T) (interface {
	Upsert(context.Context, *domain.PSPClient) (int64, error)
	GetByID(context.Context, int64) (*domain.PSPClient, error)
	GetByEmail(context.Context, int64, string) (*domain.PSPClient, error)
	ListByUser(context.Context, int64) ([]*domain.PSPClient, error)
	DeleteByEmail(context.Context, int64, string) error
	SetInbounds(context.Context, int64, []domain.PSPClientInbound) error
	ListInbounds(context.Context, int64) ([]domain.PSPClientInbound, error)
	UpdateCounters(context.Context, *domain.PSPClient) error
	BatchUpdateCounters(context.Context, []*domain.PSPClient) error
}, context.Context) {
	t.Helper()
	db, err := openTestDB(t)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	if err := EnsureSchema(db); err != nil {
		t.Fatalf("schema: %v", err)
	}
	t.Cleanup(func() {
		if sqlDB, _ := db.DB(); sqlDB != nil {
			_ = sqlDB.Close()
		}
	})
	return NewRepos(db).PSPClient, context.Background()
}

func TestPSPClientUpsertIsKeyedByPanelAndEmail(t *testing.T) {
	repo, ctx := newPSPClientTestRepo(t)

	id1, err := repo.Upsert(ctx, &domain.PSPClient{
		UserID: 1, PanelID: 10, Email: "u1@psp.local", UUID: "uuid-1", Password: "pw-1",
		LifetimeTotalBytes: 100,
	})
	if err != nil {
		t.Fatal(err)
	}
	// Same (panel, email) → UPDATE in place, same ID. Identity/credentials are
	// replaced; the poll-owned counters are PRESERVED (the incoming 250 is
	// ignored — Upsert never touches counters).
	id2, err := repo.Upsert(ctx, &domain.PSPClient{
		UserID: 1, PanelID: 10, Email: "u1@psp.local", UUID: "uuid-1b", Password: "pw-2",
		LifetimeTotalBytes: 250,
	})
	if err != nil {
		t.Fatal(err)
	}
	if id1 != id2 {
		t.Fatalf("upsert on same (panel,email) made a new row: %d vs %d", id1, id2)
	}
	got, err := repo.GetByEmail(ctx, 10, "u1@psp.local")
	if err != nil {
		t.Fatal(err)
	}
	if got.UUID != "uuid-1b" || got.Password != "pw-2" {
		t.Fatalf("upsert did not update identity/credentials: %+v", got)
	}
	if got.LifetimeTotalBytes != 100 {
		t.Fatalf("upsert clobbered poll-owned counters: got %d, want preserved 100", got.LifetimeTotalBytes)
	}

	// Same email on a DIFFERENT panel is a distinct client (per-server identity).
	id3, err := repo.Upsert(ctx, &domain.PSPClient{UserID: 1, PanelID: 11, Email: "u1@psp.local", UUID: "x"})
	if err != nil {
		t.Fatal(err)
	}
	if id3 == id1 {
		t.Fatal("same email on a different panel must be a separate client")
	}
}

func TestPSPClientGetByEmailNotFound(t *testing.T) {
	repo, ctx := newPSPClientTestRepo(t)
	_, err := repo.GetByEmail(ctx, 10, "missing@psp.local")
	if !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
}

func TestPSPClientSetInboundsReplacesAttachmentSet(t *testing.T) {
	repo, ctx := newPSPClientTestRepo(t)
	id, err := repo.Upsert(ctx, &domain.PSPClient{UserID: 1, PanelID: 10, Email: "u1@psp.local"})
	if err != nil {
		t.Fatal(err)
	}
	// Attach to nodes 2 and 3.
	if err := repo.SetInbounds(ctx, id, []domain.PSPClientInbound{
		{ClientID: id, NodeID: 2, FlowOverride: "xtls-rprx-vision"},
		{ClientID: id, NodeID: 3},
	}); err != nil {
		t.Fatal(err)
	}
	got, err := repo.ListInbounds(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].NodeID != 2 || got[0].FlowOverride != "xtls-rprx-vision" || got[1].NodeID != 3 {
		t.Fatalf("attachment set = %+v", got)
	}
	// Replace with just node 3 → set fully replaced, no node 2 left.
	if err := repo.SetInbounds(ctx, id, []domain.PSPClientInbound{{ClientID: id, NodeID: 3}}); err != nil {
		t.Fatal(err)
	}
	got, _ = repo.ListInbounds(ctx, id)
	if len(got) != 1 || got[0].NodeID != 3 {
		t.Fatalf("after replace, attachment set = %+v, want only node 3", got)
	}
}

func TestPSPClientDeleteCascadesInbounds(t *testing.T) {
	repo, ctx := newPSPClientTestRepo(t)
	id, _ := repo.Upsert(ctx, &domain.PSPClient{UserID: 1, PanelID: 10, Email: "u1@psp.local"})
	_ = repo.SetInbounds(ctx, id, []domain.PSPClientInbound{{ClientID: id, NodeID: 2}})
	if err := repo.DeleteByEmail(ctx, 10, "u1@psp.local"); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.GetByEmail(ctx, 10, "u1@psp.local"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("client should be gone, got %v", err)
	}
	if got, _ := repo.ListInbounds(ctx, id); len(got) != 0 {
		t.Fatalf("attachment rows should cascade-delete, got %+v", got)
	}
	// Delete of a missing client is idempotent.
	if err := repo.DeleteByEmail(ctx, 10, "u1@psp.local"); err != nil {
		t.Fatalf("idempotent delete errored: %v", err)
	}
}

func TestPSPClientUpdateCountersIsColumnScoped(t *testing.T) {
	repo, ctx := newPSPClientTestRepo(t)
	id, _ := repo.Upsert(ctx, &domain.PSPClient{
		UserID: 1, PanelID: 10, Email: "u1@psp.local", UUID: "uuid-keep", Password: "pw-keep",
	})
	// Counter-only update must NOT clobber identity/credential columns.
	if err := repo.UpdateCounters(ctx, &domain.PSPClient{
		ID: id, LifetimeTotalBytes: 999, LifetimeUpBytes: 400, LifetimeDownBytes: 599,
		LastRawTotalBytes: 999, PeriodBaselineTotalBytes: 100,
	}); err != nil {
		t.Fatal(err)
	}
	got, _ := repo.GetByID(ctx, id)
	if got.UUID != "uuid-keep" || got.Password != "pw-keep" {
		t.Fatalf("UpdateCounters clobbered credentials: %+v", got)
	}
	if got.LifetimeTotalBytes != 999 || got.PeriodBaselineTotalBytes != 100 {
		t.Fatalf("counters not persisted: %+v", got)
	}
}

func TestPSPClientListByUserAndPeriodUsage(t *testing.T) {
	repo, ctx := newPSPClientTestRepo(t)
	// One user, two servers (panels) → two clients = per-user-per-server usage.
	_, _ = repo.Upsert(ctx, &domain.PSPClient{UserID: 7, PanelID: 10, Email: "u7@psp.local",
		LifetimeTotalBytes: 1000, PeriodBaselineTotalBytes: 200})
	_, _ = repo.Upsert(ctx, &domain.PSPClient{UserID: 7, PanelID: 11, Email: "u7@psp.local",
		LifetimeTotalBytes: 500, PeriodBaselineTotalBytes: 0})
	list, err := repo.ListByUser(ctx, 7)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 2 {
		t.Fatalf("want 2 per-server clients for user 7, got %d", len(list))
	}
	// Per-server period usage is directly available from each client.
	var totalPeriod int64
	for _, c := range list {
		totalPeriod += c.PeriodUsedTotal()
	}
	if totalPeriod != 800+500 { // (1000-200) + (500-0)
		t.Fatalf("summed per-server period usage = %d, want 1300", totalPeriod)
	}
}
