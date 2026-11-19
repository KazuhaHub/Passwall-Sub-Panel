package sqlstore

import (
	"context"
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
