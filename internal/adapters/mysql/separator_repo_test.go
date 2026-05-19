package mysql

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
)

// TestSeparatorRepoCreate_RepeatsForReportedBug reproduces the
// production "POST /api/admin/nodes/separator → 500" path. Goal: catch
// any GORM/sqlite-side incompat with our jsonInt64s + boolean defaults
// at insert time.
func TestSeparatorRepoCreate_RepeatsForReportedBug(t *testing.T) {
	db, err := Open("sqlite", filepath.Join(t.TempDir(), "panel.db"))
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

	// Mirror the JSON the React form sends:
	//   { display_name, show_in_all_groups: false, group_ids: [..], enabled: true }
	cases := []struct {
		name string
		e    *domain.SeparatorEntry
	}{
		{"all-groups true / no ids", &domain.SeparatorEntry{
			DisplayName: "----- TW -----", Enabled: true, ShowInAllGroups: true, SortOrder: 10,
		}},
		{"all-groups false / two ids", &domain.SeparatorEntry{
			DisplayName: "----- Taiwan -----", Enabled: true, ShowInAllGroups: false,
			GroupIDs: []int64{1, 2}, SortOrder: 20,
		}},
		{"empty ids slice", &domain.SeparatorEntry{
			DisplayName: "----- empty -----", Enabled: true, ShowInAllGroups: false,
			GroupIDs: []int64{}, SortOrder: 30,
		}},
		{"nil ids slice", &domain.SeparatorEntry{
			DisplayName: "----- nil -----", Enabled: true, ShowInAllGroups: false,
			GroupIDs: nil, SortOrder: 40,
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
