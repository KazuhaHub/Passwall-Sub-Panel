package sqlstore

import (
	"context"
	"testing"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
)

// A hit's link id resolves to its two servers in one read; an unknown id is
// absent, and nothing is returned for ids not asked for.
func TestTransitLinkEnds_ResolvesIDsInOneRead(t *testing.T) {
	db, err := openTestDB(t)
	if err != nil {
		t.Fatal(err)
	}
	if err := ensureTestSchema(db); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	repo := &transitLinkRepo{db: db}
	var ids []int64
	for _, pair := range [][2]int64{{1, 2}, {1, 3}, {4, 2}} {
		row := transitLinkRow{EntryPanelID: pair[0], ExitPanelID: pair[1], Protocol: "ss2022", Port: 1, SSMethod: "m", SSKey: "k"}
		if err := db.Create(&row).Error; err != nil {
			t.Fatal(err)
		}
		ids = append(ids, row.ID)
	}
	got, err := repo.LinkEnds(ctx, []int64{ids[0], ids[2], 9999})
	if err != nil {
		t.Fatal(err)
	}
	want := map[int64]domain.TransitLinkEnds{ids[0]: {EntryPanelID: 1, ExitPanelID: 2}, ids[2]: {EntryPanelID: 4, ExitPanelID: 2}}
	if len(got) != 2 || got[ids[0]] != want[ids[0]] || got[ids[2]] != want[ids[2]] {
		t.Fatalf("LinkEnds = %v, want %v", got, want)
	}
	if empty, err := repo.LinkEnds(ctx, nil); err != nil || len(empty) != 0 {
		t.Fatalf("no ids = %v, %v", empty, err)
	}
}
