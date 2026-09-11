package sqlstore

import (
	"context"
	"errors"
	"testing"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
)

func newPSPClientTestRepo(t *testing.T) (interface {
	Create(context.Context, *domain.PSPClient) (int64, error)
	UpdateDefinition(context.Context, *domain.PSPClient) error
	UpdateDesiredLifecycleByUser(context.Context, int64, domain.UserLifecycle) error
	GetByID(context.Context, int64) (*domain.PSPClient, error)
	GetByEmail(context.Context, int64, string) (*domain.PSPClient, error)
	ListByUser(context.Context, int64) ([]*domain.PSPClient, error)
	DeleteByID(context.Context, int64) error
	SetInbounds(context.Context, int64, []domain.PSPClientInbound) error
	ListInbounds(context.Context, int64) ([]domain.PSPClientInbound, error)
	UpdateInboundState(context.Context, domain.PSPClientInbound) error
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

func TestPSPClientDefinitionUpdateIsKeyedOnlyByStableID(t *testing.T) {
	repo, ctx := newPSPClientTestRepo(t)

	id1, err := repo.Create(ctx, &domain.PSPClient{
		UserID: 1, PanelID: 10, Email: "u1@psp.local", UUID: "uuid-1", Password: "pw-1",
		LifetimeTotalBytes: 100,
		LastRawUpBytes:     40, LastRawDownBytes: 60, LastRawTotalBytes: 100,
	})
	if err != nil {
		t.Fatal(err)
	}
	// Email changes, but an explicit stable ID updates the same row. Definition
	// fields are replaced while poll-owned counters and baselines are preserved.
	err = repo.UpdateDefinition(ctx, &domain.PSPClient{
		ID: id1, UserID: 1, PanelID: 10, Email: "u1@new.example", UUID: "uuid-1b", Password: "pw-2",
		LifetimeTotalBytes: 250,
	})
	if err != nil {
		t.Fatal(err)
	}
	got, err := repo.GetByEmail(ctx, 10, "u1@new.example")
	if err != nil {
		t.Fatal(err)
	}
	if got.UUID != "uuid-1b" || got.Password != "pw-2" {
		t.Fatalf("definition update did not update credentials: %+v", got)
	}
	if got.LifetimeTotalBytes != 100 {
		t.Fatalf("definition update clobbered poll-owned counters: got %d, want preserved 100", got.LifetimeTotalBytes)
	}
	if got.LastRawUpBytes != 40 || got.LastRawDownBytes != 60 || got.LastRawTotalBytes != 100 {
		t.Fatalf("definition update clobbered LastRaw baseline: %+v", got)
	}

	// Same email on a DIFFERENT panel is a distinct client (per-server identity).
	id3, err := repo.Create(ctx, &domain.PSPClient{UserID: 1, PanelID: 11, Email: "u1@new.example", UUID: "x"})
	if err != nil {
		t.Fatal(err)
	}
	if id3 == id1 {
		t.Fatal("same email on a different panel must be a separate client")
	}
}

func TestPSPClientDesiredLifecycleHasOneColumnScopedWriter(t *testing.T) {
	repo, ctx := newPSPClientTestRepo(t)
	id, err := repo.Create(ctx, &domain.PSPClient{
		UserID: 7, PanelID: 10, Email: "u7@psp.local", UUID: "uuid-keep",
		LifetimeTotalBytes: 999, LastRawTotalBytes: 111,
	})
	if err != nil {
		t.Fatal(err)
	}
	want := domain.UserLifecycle{
		Enable: true, ExpiryTime: 1893456000000, QuotaHeadroom: 1234,
		IPLimit: 3, DeviceLimit: 4,
	}
	if err := repo.UpdateDesiredLifecycleByUser(ctx, 7, want); err != nil {
		t.Fatal(err)
	}
	got, err := repo.GetByID(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if !got.DesiredMinted || got.DesiredLifecycle() != want {
		t.Fatalf("desired lifecycle = %+v (minted=%v), want %+v", got.DesiredLifecycle(), got.DesiredMinted, want)
	}
	if got.Email != "u7@psp.local" || got.UUID != "uuid-keep" ||
		got.LifetimeTotalBytes != 999 || got.LastRawTotalBytes != 111 {
		t.Fatalf("lifecycle writer clobbered another owner's columns: %+v", got)
	}

	// Identity re-planning owns different columns and must not erase intent.
	got.Email = "u7@new.example"
	got.UUID = "uuid-new"
	if err := repo.UpdateDefinition(ctx, got); err != nil {
		t.Fatal(err)
	}
	after, err := repo.GetByID(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if !after.DesiredMinted || after.DesiredLifecycle() != want {
		t.Fatalf("definition writer clobbered desired lifecycle: %+v", after)
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
	id, err := repo.Create(ctx, &domain.PSPClient{UserID: 1, PanelID: 10, Email: "u1@psp.local"})
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

func TestPSPClientSetInboundsPreservesApplyState(t *testing.T) {
	repo, ctx := newPSPClientTestRepo(t)
	id, _ := repo.Create(ctx, &domain.PSPClient{UserID: 1, PanelID: 10, Email: "u1@psp.local"})
	if err := repo.SetInbounds(ctx, id, []domain.PSPClientInbound{
		{ClientID: id, NodeID: 2}, {ClientID: id, NodeID: 3},
	}); err != nil {
		t.Fatal(err)
	}
	// reconcile confirms node 2 attached in 3X-UI.
	if err := repo.UpdateInboundState(ctx, domain.PSPClientInbound{
		ClientID: id, NodeID: 2, State: domain.ClientApplyApplied, AppliedVersion: 7,
		AppliedEmail: "u1@psp.local", AppliedUUID: "old-uuid", AppliedPassword: "old-password",
	}); err != nil {
		t.Fatal(err)
	}

	// A dual-write re-syncs the SAME node set (additive diff, flow change on 2).
	// node 2's applied state must SURVIVE; its flow updates; node 3 stays pending.
	if err := repo.SetInbounds(ctx, id, []domain.PSPClientInbound{
		{ClientID: id, NodeID: 2, FlowOverride: "xtls-rprx-vision"}, {ClientID: id, NodeID: 3},
	}); err != nil {
		t.Fatal(err)
	}
	by := func() map[int64]domain.PSPClientInbound {
		got, _ := repo.ListInbounds(ctx, id)
		m := map[int64]domain.PSPClientInbound{}
		for _, in := range got {
			m[in.NodeID] = in
		}
		return m
	}
	m := by()
	if !m[2].Applied() || m[2].AppliedVersion != 7 {
		t.Fatal("node 2 applied state/version must survive an additive re-sync")
	}
	if m[2].AppliedEmail != "u1@psp.local" || m[2].AppliedUUID != "old-uuid" || m[2].AppliedPassword != "old-password" {
		t.Fatalf("node 2 applied credential snapshot did not survive: %+v", m[2])
	}
	if m[2].FlowOverride != "xtls-rprx-vision" {
		t.Fatalf("flow should update on the surviving row, got %q", m[2].FlowOverride)
	}
	if m[3].State != domain.ClientApplyPending || m[3].FirstFailedAt == nil {
		t.Fatal("new node 3 must be pending with an observable start time")
	}
	if err := repo.UpdateInboundState(ctx, domain.PSPClientInbound{
		ClientID: id, NodeID: 2, State: domain.ClientApplyPending, AppliedVersion: 8,
	}); err != nil {
		t.Fatal(err)
	}
	pendingRotation := by()[2]
	if pendingRotation.AppliedVersion != 7 || pendingRotation.AppliedUUID != "old-uuid" || pendingRotation.AppliedPassword != "old-password" {
		t.Fatalf("pending newer roster erased last applied credential: %+v", pendingRotation)
	}

	// Remove node 2, then re-add it → it comes back pending (fresh attachment).
	if err := repo.SetInbounds(ctx, id, []domain.PSPClientInbound{{ClientID: id, NodeID: 3}}); err != nil {
		t.Fatal(err)
	}
	if err := repo.SetInbounds(ctx, id, []domain.PSPClientInbound{
		{ClientID: id, NodeID: 2}, {ClientID: id, NodeID: 3},
	}); err != nil {
		t.Fatal(err)
	}
	if got := by()[2]; got.State != domain.ClientApplyPending || got.FirstFailedAt == nil {
		t.Fatal("re-added node 2 must be pending with a fresh failure clock")
	}
}

func TestPSPClientDeleteCascadesInbounds(t *testing.T) {
	repo, ctx := newPSPClientTestRepo(t)
	id, _ := repo.Create(ctx, &domain.PSPClient{UserID: 1, PanelID: 10, Email: "u1@psp.local"})
	_ = repo.SetInbounds(ctx, id, []domain.PSPClientInbound{{ClientID: id, NodeID: 2}})
	if err := repo.DeleteByID(ctx, id); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.GetByEmail(ctx, 10, "u1@psp.local"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("client should be gone, got %v", err)
	}
	if got, _ := repo.ListInbounds(ctx, id); len(got) != 0 {
		t.Fatalf("attachment rows should cascade-delete, got %+v", got)
	}
	// Delete of a missing client is idempotent.
	if err := repo.DeleteByID(ctx, id); err != nil {
		t.Fatalf("idempotent delete errored: %v", err)
	}
}

func TestPSPClientUpdateCountersIsColumnScoped(t *testing.T) {
	repo, ctx := newPSPClientTestRepo(t)
	id, _ := repo.Create(ctx, &domain.PSPClient{
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
	_, _ = repo.Create(ctx, &domain.PSPClient{UserID: 7, PanelID: 10, Email: "u7@psp.local",
		LifetimeTotalBytes: 1000, PeriodBaselineTotalBytes: 200})
	_, _ = repo.Create(ctx, &domain.PSPClient{UserID: 7, PanelID: 11, Email: "u7@psp.local",
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
