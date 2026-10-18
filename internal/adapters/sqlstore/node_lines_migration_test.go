package sqlstore

import (
	"testing"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
)

func TestHistoricalRelayIDsArePersistedAndIdempotent(t *testing.T) {
	r, ctx := newNodeTestRepo(t)
	n := &domain.Node{PanelID: 1, InboundID: 1, DisplayName: "legacy"}
	if err := r.Create(ctx, n); err != nil {
		t.Fatal(err)
	}
	legacy := `[{"Name":"relay","Address":"relay.example","Port":443,"Enabled":true}]`
	if err := r.db.Table("nodes").Where("id = ?", n.ID).Update("relays", legacy).Error; err != nil {
		t.Fatal(err)
	}
	if err := backfillRelayLineIDs(r.db); err != nil {
		t.Fatal(err)
	}
	first, err := r.GetByID(ctx, n.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Relays) != 1 || first.Relays[0].ID == "" || first.Relays[0].Address != "relay.example" {
		t.Fatalf("bad migration: %+v", first.Relays)
	}
	if err := backfillRelayLineIDs(r.db); err != nil {
		t.Fatal(err)
	}
	second, err := r.GetByID(ctx, n.ID)
	if err != nil {
		t.Fatal(err)
	}
	if first.Relays[0].ID != second.Relays[0].ID {
		t.Fatal("migration changed a published ID")
	}
}
