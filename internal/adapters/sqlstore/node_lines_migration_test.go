package sqlstore

import (
	"errors"
	"reflect"
	"strings"
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

// ADR 0040 §3: lines written before line labels existed take the node's
// values; a key present with an empty value is an explicit clear and stays.
func TestLineLabelBackfillByKeyPresence(t *testing.T) {
	r, ctx := newNodeTestRepo(t)
	n := &domain.Node{PanelID: 1, InboundID: 1, DisplayName: "legacy", Region: "HK", Tags: []string{"edge", "edge", " raw "}}
	if err := r.Create(ctx, n); err != nil {
		t.Fatal(err)
	}
	relays := `[{"ID":"a","Name":"old","Address":"relay.example","Enabled":true},` +
		`{"ID":"b","Address":"cleared.example","Enabled":true,"Region":"","Tags":[]},` +
		`{"ID":"c","Address":"half.example","Enabled":true,"Region":"JP"}]`
	route := `{"revision":4,"lines":[{"id":"v1","name":"old","outbound_tag":"direct","route":1,"enabled":true},` +
		`{"id":"v2","outbound_tag":"direct","route":2,"enabled":true,"region":"","tags":[]},` +
		`{"id":"v3","outbound_tag":"direct","route":3,"deleted":true}],"status":"applied","binding":"keep"}`
	if err := r.db.Table("nodes").Where("id = ?", n.ID).Updates(map[string]any{"relays": relays, "route_state": route, "route_revision": 4}).Error; err != nil {
		t.Fatal(err)
	}
	if err := backfillLineLabels(r.db); err != nil {
		t.Fatal(err)
	}
	got, err := r.GetByID(ctx, n.ID)
	if err != nil {
		t.Fatal(err)
	}
	nodeTags := []string{"edge", "edge", " raw "} // copied verbatim: output must not change
	if a := got.Relays[0]; a.Region != "HK" || !reflect.DeepEqual(a.Tags, nodeTags) {
		t.Fatalf("legacy relay not backfilled: %+v", a)
	}
	if b := got.Relays[1]; b.Region != "" || len(b.Tags) != 0 {
		t.Fatalf("explicit clear overwritten: %+v", b)
	}
	if c := got.Relays[2]; c.Region != "JP" || !reflect.DeepEqual(c.Tags, nodeTags) {
		t.Fatalf("only the missing key may be filled: %+v", c)
	}
	lines := got.RouteState.Lines
	if lines[0].Region != "HK" || !reflect.DeepEqual(lines[0].Tags, nodeTags) || lines[1].Region != "" || len(lines[1].Tags) != 0 || lines[2].Region != "HK" {
		t.Fatalf("route lines: %+v", lines)
	}
	if got.RouteState.Revision != 4 || got.RouteState.Status != "applied" || got.RouteState.Binding != "keep" {
		t.Fatalf("backfill touched route state metadata: %+v", got.RouteState)
	}
	var stored struct{ Relays, RouteState string }
	if err := r.db.Table("nodes").Select("relays, route_state").Where("id = ?", n.ID).Scan(&stored).Error; err != nil {
		t.Fatal(err)
	}
	// Idempotent: the second start changes nothing, even after the node moves on.
	if err := r.db.Table("nodes").Where("id = ?", n.ID).Update("region", "US").Error; err != nil {
		t.Fatal(err)
	}
	if err := backfillLineLabels(r.db); err != nil {
		t.Fatal(err)
	}
	var again struct{ Relays, RouteState string }
	if err := r.db.Table("nodes").Select("relays, route_state").Where("id = ?", n.ID).Scan(&again).Error; err != nil {
		t.Fatal(err)
	}
	if again != stored {
		t.Fatal("a second backfill rewrote completed lines")
	}
}

// A row an older binary rewrote after a downgrade lost the keys again; the
// next start completes it with the node's value at that time.
func TestLineLabelBackfillCompletesRowsRewrittenAfterDowngrade(t *testing.T) {
	r, ctx := newNodeTestRepo(t)
	n := &domain.Node{PanelID: 1, InboundID: 1, DisplayName: "n", Region: "HK", Relays: []domain.RelayLine{{Address: "relay.example", Enabled: true, Region: "JP", Tags: []string{"x"}}}}
	if err := r.Create(ctx, n); err != nil {
		t.Fatal(err)
	}
	downgraded := `[{"ID":"` + n.Relays[0].ID + `","Address":"relay.example","Enabled":true}]`
	if err := r.db.Table("nodes").Where("id = ?", n.ID).Updates(map[string]any{"relays": downgraded, "region": "SG"}).Error; err != nil {
		t.Fatal(err)
	}
	if err := backfillLineLabels(r.db); err != nil {
		t.Fatal(err)
	}
	got, _ := r.GetByID(ctx, n.ID)
	if got.Relays[0].Region != "SG" || got.Relays[0].Tags == nil {
		t.Fatalf("downgraded row not completed: %+v", got.Relays[0])
	}
}

func TestLineLabelBackfillCompareAndSwapDetectsConcurrentWriter(t *testing.T) {
	r, ctx := newNodeTestRepo(t)
	n := &domain.Node{PanelID: 1, InboundID: 1, DisplayName: "n", Region: "HK"}
	if err := r.Create(ctx, n); err != nil {
		t.Fatal(err)
	}
	if err := casNodeColumn(r.db, n.ID, "relays", `[{"stale":true}]`, jsonRelays{}); err == nil {
		t.Fatal("a CAS against a changed column succeeded")
	}
}

// Storage refuses a line whose omitted fields were never resolved, so a
// forgotten resolution step fails instead of silently clearing labels.
func TestStorageRejectsUnresolvedLineLabels(t *testing.T) {
	r, ctx := newNodeTestRepo(t)
	n := &domain.Node{PanelID: 1, InboundID: 1, DisplayName: "n", Relays: []domain.RelayLine{{Address: "relay.example", RegionUnresolved: true}}}
	if err := r.Create(ctx, n); !errors.Is(err, domain.ErrUnresolvedLineLabels) {
		t.Fatalf("unresolved relay stored: %v", err)
	}
	n.Relays = nil
	if err := r.Create(ctx, n); err != nil {
		t.Fatal(err)
	}
	state := &domain.NodeRouteState{Lines: []domain.VlessRouteLine{{ID: "v", OutboundTag: "direct", TagsUnresolved: true}}}
	if err := r.SaveRouteState(ctx, n.ID, 0, state); !errors.Is(err, domain.ErrUnresolvedLineLabels) {
		t.Fatalf("unresolved route line stored: %v", err)
	}
	stored, err := jsonRelays{{ID: "a", Address: "x"}}.Value()
	if err != nil || !strings.Contains(stored.(string), `"Region":""`) || !strings.Contains(stored.(string), `"Tags":[]`) {
		t.Fatalf("new writes must always carry both keys: %v %v", stored, err)
	}
}
