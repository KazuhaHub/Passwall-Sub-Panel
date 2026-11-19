package sqlstore

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

func seedPositionedNodes(t *testing.T) (*nodeRepo, context.Context, []*domain.Node) {
	t.Helper()
	repo, ctx := newNodeTestRepo(t)
	var nodes []*domain.Node
	for i, name := range []string{"A", "B"} {
		n := &domain.Node{PanelID: 1, InboundID: i + 1, DisplayName: name, Enabled: true,
			Relays: []domain.RelayLine{
				{ID: name + "-r1", Name: "r1", Address: "r1.example", Enabled: true},
				{ID: name + "-r2", Name: "r2", Address: "r2.example", Enabled: true},
			}}
		if err := repo.Create(ctx, n); err != nil {
			t.Fatal(err)
		}
		nodes = append(nodes, n)
	}
	return repo, ctx, nodes
}

func putPosition(t *testing.T, repo *nodeRepo, nodeID int64, lineID string, pos int) {
	t.Helper()
	if err := repo.db.Create(&subscriptionLinePositionRow{NodeID: nodeID, LineID: lineID, SortOrder: pos}).Error; err != nil {
		t.Fatal(err)
	}
}

func relayPosition(n *domain.Node, id string) *int {
	for _, r := range n.Relays {
		if r.ID == id {
			return r.Position
		}
	}
	panic("no such relay " + id)
}

// The table is created by the same AutoMigrate every other table goes through,
// takes a composite key and rejects a second row for the same line.
func TestLinePositionsAutoMigrateAndRoundTrip(t *testing.T) {
	repo, _, nodes := seedPositionedNodes(t)
	if !repo.db.Migrator().HasTable("subscription_line_positions") {
		t.Fatal("subscription_line_positions was not created by EnsureSchema")
	}
	putPosition(t, repo, nodes[0].ID, "A-r1", 25)
	var got subscriptionLinePositionRow
	if err := repo.db.First(&got, "node_id = ? AND line_id = ?", nodes[0].ID, "A-r1").Error; err != nil || got.SortOrder != 25 {
		t.Fatalf("round trip: %+v err=%v", got, err)
	}
	if err := repo.db.Create(&subscriptionLinePositionRow{NodeID: nodes[0].ID, LineID: "A-r1", SortOrder: 1}).Error; err == nil {
		t.Fatal("a line must have at most one position row")
	}
	// The same line ID under another node is a different line.
	putPosition(t, repo, nodes[1].ID, "A-r1", 30)
}

// Every read path that feeds render or the admin list attaches positions, and
// only to the lines that have one.
func TestNodeReadsAttachPositions(t *testing.T) {
	repo, ctx, nodes := seedPositionedNodes(t)
	putPosition(t, repo, nodes[0].ID, "A-r2", 15)
	putPosition(t, repo, nodes[1].ID, "B-r1", 0)  // zero is a valid position, not "unset"
	putPosition(t, repo, nodes[1].ID, "gone", 99) // orphan: no such line, must be ignored

	check := func(name string, list []*domain.Node) {
		t.Helper()
		byName := map[string]*domain.Node{}
		for _, n := range list {
			byName[n.DisplayName] = n
		}
		a, b := byName["A"], byName["B"]
		if a == nil || b == nil {
			t.Fatalf("%s: missing nodes", name)
		}
		if p := relayPosition(a, "A-r1"); p != nil {
			t.Errorf("%s: unpositioned line has position %d", name, *p)
		}
		if p := relayPosition(a, "A-r2"); p == nil || *p != 15 {
			t.Errorf("%s: A-r2 position = %v, want 15", name, p)
		}
		if p := relayPosition(b, "B-r1"); p == nil || *p != 0 {
			t.Errorf("%s: B-r1 position = %v, want 0", name, p)
		}
	}
	enabled, err := repo.ListEnabled(ctx)
	if err != nil {
		t.Fatal(err)
	}
	check("ListEnabled", enabled)
	all, err := repo.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	check("List", all)
	paged, _, err := repo.ListPaged(ctx, ports.Pagination{Page: 1, PageSize: 50})
	if err != nil {
		t.Fatal(err)
	}
	check("ListPaged", paged)
	one, err := repo.GetByID(ctx, nodes[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	if p := relayPosition(one, "A-r2"); p == nil || *p != 15 {
		t.Errorf("GetByID: A-r2 position = %v, want 15", p)
	}
}

func allPositions(t *testing.T, repo *nodeRepo) map[string]int {
	t.Helper()
	var rows []subscriptionLinePositionRow
	if err := repo.db.Find(&rows).Error; err != nil {
		t.Fatal(err)
	}
	out := map[string]int{}
	for _, r := range rows {
		out[fmt.Sprintf("%d/%s", r.NodeID, r.LineID)] = r.SortOrder
	}
	return out
}

func seedSeparator(t *testing.T, repo *nodeRepo, order int) *domain.SeparatorEntry {
	t.Helper()
	sep := &domain.SeparatorEntry{DisplayName: "----", Enabled: true, Mode: domain.SeparatorModeGlobal, SortOrder: order}
	if err := (&separatorRepo{db: repo.db}).Create(context.Background(), sep); err != nil {
		t.Fatal(err)
	}
	return sep
}

// One call moves nodes, separators and lines together and replaces the whole
// set of line rows: a line missing from the write loses its old position.
func TestApplySubscriptionOrderWritesNodesSeparatorsAndLines(t *testing.T) {
	repo, ctx, nodes := seedPositionedNodes(t)
	sep := seedSeparator(t, repo, 5)
	putPosition(t, repo, nodes[0].ID, "A-r1", 999) // replaced
	putPosition(t, repo, nodes[1].ID, "B-r2", 888) // absent from the write: dropped

	err := repo.ApplySubscriptionOrder(ctx, ports.SubscriptionOrderWrite{
		Nodes:      []ports.NodeSortUpdate{{NodeID: nodes[0].ID, SortOrder: 20}, {NodeID: nodes[1].ID, SortOrder: 40}},
		Separators: []ports.SeparatorSortUpdate{{SeparatorID: sep.ID, SortOrder: 10}},
		Lines: []ports.LinePositionWrite{
			{NodeID: nodes[1].ID, LineID: "B-r1", SortOrder: 30},
			{NodeID: nodes[0].ID, LineID: "A-r1", SortOrder: 50},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	a, _ := repo.GetByID(ctx, nodes[0].ID)
	b, _ := repo.GetByID(ctx, nodes[1].ID)
	if a.SortOrder != 20 || b.SortOrder != 40 {
		t.Fatalf("node orders = %d,%d", a.SortOrder, b.SortOrder)
	}
	got, _ := (&separatorRepo{db: repo.db}).GetByID(ctx, sep.ID)
	if got.SortOrder != 10 || !got.Enabled || got.DisplayName != "----" {
		t.Fatalf("separator = %+v (only sort_order may change)", got)
	}
	want := map[string]int{fmt.Sprintf("%d/B-r1", nodes[1].ID): 30, fmt.Sprintf("%d/A-r1", nodes[0].ID): 50}
	if !reflect.DeepEqual(allPositions(t, repo), want) {
		t.Fatalf("positions = %v, want %v", allPositions(t, repo), want)
	}
	// Column-scoped: nothing else on the node moved.
	if len(a.Relays) != 2 || a.DisplayName != "A" || !a.Enabled {
		t.Fatalf("node row clobbered: %+v", a)
	}
}

// A failure on the last insert leaves everything as it was: the node and
// separator updates and the delete of the old rows roll back with it.
func TestApplySubscriptionOrderIsAtomic(t *testing.T) {
	repo, ctx, nodes := seedPositionedNodes(t)
	sep := seedSeparator(t, repo, 5)
	putPosition(t, repo, nodes[0].ID, "A-r1", 77)
	before := allPositions(t, repo)

	err := repo.ApplySubscriptionOrder(ctx, ports.SubscriptionOrderWrite{
		Nodes:      []ports.NodeSortUpdate{{NodeID: nodes[0].ID, SortOrder: 20}},
		Separators: []ports.SeparatorSortUpdate{{SeparatorID: sep.ID, SortOrder: 10}},
		Lines: []ports.LinePositionWrite{
			{NodeID: nodes[0].ID, LineID: "A-r2", SortOrder: 30},
			{NodeID: nodes[0].ID, LineID: "A-r2", SortOrder: 31}, // duplicate key: the last insert fails
		},
	})
	if err == nil {
		t.Fatal("expected the duplicate to fail the write")
	}
	a, _ := repo.GetByID(ctx, nodes[0].ID)
	got, _ := (&separatorRepo{db: repo.db}).GetByID(ctx, sep.ID)
	if a.SortOrder == 20 || got.SortOrder != 5 {
		t.Fatalf("partial write survived: node=%d separator=%d", a.SortOrder, got.SortOrder)
	}
	if !reflect.DeepEqual(allPositions(t, repo), before) {
		t.Fatalf("positions changed by a failed write: %v", allPositions(t, repo))
	}
}

// The direct line is the node row: a position for it would be a second copy.
func TestApplySubscriptionOrderRefusesDirectAndEmptyLineIDs(t *testing.T) {
	repo, ctx, nodes := seedPositionedNodes(t)
	for _, id := range []string{domain.DirectLineID, ""} {
		err := repo.ApplySubscriptionOrder(ctx, ports.SubscriptionOrderWrite{
			Lines: []ports.LinePositionWrite{{NodeID: nodes[0].ID, LineID: id, SortOrder: 10}},
		})
		if !errors.Is(err, domain.ErrValidation) {
			t.Fatalf("line %q: err = %v, want ErrValidation", id, err)
		}
	}
	if len(allPositions(t, repo)) != 0 {
		t.Fatal("a refused write left rows behind")
	}
}

func TestNodeDeleteRemovesLinePositions(t *testing.T) {
	repo, ctx, nodes := seedPositionedNodes(t)
	putPosition(t, repo, nodes[0].ID, "A-r1", 10)
	putPosition(t, repo, nodes[1].ID, "B-r1", 20)
	if err := repo.Delete(ctx, nodes[0].ID); err != nil {
		t.Fatal(err)
	}
	if got, want := allPositions(t, repo), map[string]int{fmt.Sprintf("%d/B-r1", nodes[1].ID): 20}; !reflect.DeepEqual(got, want) {
		t.Fatalf("positions = %v, want %v", got, want)
	}
}

// A line removed in a metadata save takes its position with it; the ones that
// remain keep theirs, and the write does not touch positions otherwise.
func TestUpdateMetadataPrunesRemovedLinePositions(t *testing.T) {
	repo, ctx, nodes := seedPositionedNodes(t)
	putPosition(t, repo, nodes[0].ID, "A-r1", 10)
	putPosition(t, repo, nodes[0].ID, "A-r2", 20)
	putPosition(t, repo, nodes[1].ID, "B-r1", 30)

	edit := *nodes[0]
	edit.Relays = []domain.RelayLine{nodes[0].Relays[1]} // A-r1 removed
	if err := repo.UpdateMetadata(ctx, &edit); err != nil {
		t.Fatal(err)
	}
	want := map[string]int{fmt.Sprintf("%d/A-r2", nodes[0].ID): 20, fmt.Sprintf("%d/B-r1", nodes[1].ID): 30}
	if got := allPositions(t, repo); !reflect.DeepEqual(got, want) {
		t.Fatalf("positions = %v, want %v", got, want)
	}

	edit.Relays = nil // every line removed
	if err := repo.UpdateMetadata(ctx, &edit); err != nil {
		t.Fatal(err)
	}
	if got, want := allPositions(t, repo), map[string]int{fmt.Sprintf("%d/B-r1", nodes[1].ID): 30}; !reflect.DeepEqual(got, want) {
		t.Fatalf("positions = %v, want %v", got, want)
	}
}

// A save that edits a line in place (the ID is kept, the way a mode switch or a
// rename does) keeps its position.
func TestUpdateMetadataKeepsPositionOfEditedLine(t *testing.T) {
	repo, ctx, nodes := seedPositionedNodes(t)
	putPosition(t, repo, nodes[0].ID, "A-r1", 10)
	edit := *nodes[0]
	edit.Relays = []domain.RelayLine{nodes[0].Relays[0], nodes[0].Relays[1]}
	edit.Relays[0].Name, edit.Relays[0].Mode = "renamed", domain.RelayModeTunnel
	if err := repo.UpdateMetadata(ctx, &edit); err != nil {
		t.Fatal(err)
	}
	got, _ := repo.GetByID(ctx, nodes[0].ID)
	if p := relayPosition(got, "A-r1"); p == nil || *p != 10 {
		t.Fatalf("position = %v, want 10", p)
	}
	// The position never leaks into the stored relays JSON.
	var raw string
	if err := repo.db.Raw("SELECT relays FROM nodes WHERE id = ?", nodes[0].ID).Scan(&raw).Error; err != nil {
		t.Fatal(err)
	}
	if strings.Contains(strings.ToLower(raw), "position") {
		t.Fatalf("position serialised into relays: %s", raw)
	}
}
