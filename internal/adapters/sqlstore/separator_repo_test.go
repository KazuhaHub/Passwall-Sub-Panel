package sqlstore

import (
	"context"
	"testing"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
)

// TestSeparatorRepoCreate_RepeatsForReportedBug reproduces the
// production "POST /api/admin/nodes/separator → 500" path. Goal: catch
// any GORM/sqlite-side incompat with our jsonInt64s + boolean defaults
// at insert time.
func TestSeparatorRepoCreate_RepeatsForReportedBug(t *testing.T) {
	db, err := openTestDB(t)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	// Windows can't unlink the .db file while the sqlite handle is
	// still open — t.TempDir's auto-cleanup would otherwise fail.
	sqlDB, _ := db.DB()
	t.Cleanup(func() { _ = sqlDB.Close() })
	if err := EnsureSchema(db); err != nil {
		t.Fatalf("schema: %v", err)
	}
	repo := &separatorRepo{db: db}
	ctx := context.Background()

	// Mirror the four shapes the React form can produce under the
	// rc.4 (mode + node_ids) schema.
	cases := []struct {
		name string
		e    *domain.SeparatorEntry
	}{
		{"global / no node ids", &domain.SeparatorEntry{
			DisplayName: "----- TW -----", Enabled: true,
			Mode: domain.SeparatorModeGlobal, SortOrder: 10,
		}},
		{"node_bound / two ids", &domain.SeparatorEntry{
			DisplayName: "----- Taiwan -----", Enabled: true,
			Mode: domain.SeparatorModeNodeBound, NodeIDs: []int64{1, 2}, SortOrder: 20,
		}},
		{"node_bound / empty ids slice", &domain.SeparatorEntry{
			DisplayName: "----- empty -----", Enabled: true,
			Mode: domain.SeparatorModeNodeBound, NodeIDs: []int64{}, SortOrder: 30,
		}},
		{"node_bound / nil ids slice", &domain.SeparatorEntry{
			DisplayName: "----- nil -----", Enabled: true,
			Mode: domain.SeparatorModeNodeBound, NodeIDs: nil, SortOrder: 40,
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := repo.Create(ctx, tc.e); err != nil {
				t.Fatalf("Create: %v", err)
			}
			if tc.e.ID == 0 {
				t.Fatal("Create did not assign ID")
			}
		})
	}

	got, err := repo.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != len(cases) {
		t.Fatalf("List returned %d rows, want %d", len(got), len(cases))
	}
}

// An edit must not destroy created_at.
//
// The admin edit path builds its SeparatorEntry from the request DTO, which
// carries no created_at, and Save is a full-row write — so every edit stamped
// the column with Go's zero time and the row came back claiming to have been
// created in year 1. Nothing failed and nothing warned. Found while
// reproducing the "disabled separator still renders" report: the disable
// worked, and this was sitting underneath it.
//
// Disabling is the case asserted because that is what was reported, and it is
// also the one where the two bugs met: a write whose visible effect was
// delayed AND whose invisible effect was destructive.
func TestSeparatorRepoUpdate_PreservesCreatedAt(t *testing.T) {
	db, err := openTestDB(t)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	sqlDB, _ := db.DB()
	t.Cleanup(func() { _ = sqlDB.Close() })
	if err := EnsureSchema(db); err != nil {
		t.Fatalf("schema: %v", err)
	}
	repo := &separatorRepo{db: db}
	ctx := context.Background()

	e := &domain.SeparatorEntry{DisplayName: "---- TW ----", Enabled: true, Mode: domain.SeparatorModeGlobal}
	if err := repo.Create(ctx, e); err != nil {
		t.Fatalf("create: %v", err)
	}
	created, err := repo.GetByID(ctx, e.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if created.CreatedAt.IsZero() {
		t.Fatal("create did not stamp created_at, so this test cannot prove anything")
	}

	// Exactly what the handler builds: no CreatedAt, because the request has
	// no such field.
	if err := repo.Update(ctx, &domain.SeparatorEntry{
		ID: e.ID, DisplayName: "---- TW ----", Enabled: false, Mode: domain.SeparatorModeGlobal,
	}); err != nil {
		t.Fatalf("update: %v", err)
	}

	after, err := repo.GetByID(ctx, e.ID)
	if err != nil {
		t.Fatalf("get after update: %v", err)
	}
	if !after.CreatedAt.Equal(created.CreatedAt) {
		t.Fatalf("created_at = %v, want %v — an edit destroyed it", after.CreatedAt, created.CreatedAt)
	}
	// The edit itself still has to land, or the fix would be preserving the
	// timestamp by not writing anything at all.
	if after.Enabled {
		t.Fatal("the disable did not persist")
	}
}
