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
