package node

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/KazuhaHub/passwall-sub-panel/internal/adapters/sqlstore"
	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

// A reorder commits without holding any server lock, so a metadata save that
// loaded its node earlier can write back the position it read. These tests
// commit a reorder between the read and the write and check the order survives:
// sort_order belongs to the reorder writers, and no metadata save writes it.

type racingNodeRepo struct {
	ports.NodeRepo
	// getsLeft counts GetByID calls until fire runs once, after the call returns.
	getsLeft int
	fire     func()
}

func (r *racingNodeRepo) GetByID(ctx context.Context, id int64) (*domain.Node, error) {
	n, err := r.NodeRepo.GetByID(ctx, id)
	if r.fire != nil {
		if r.getsLeft--; r.getsLeft == 0 {
			fire := r.fire
			r.fire = nil
			fire()
		}
	}
	return n, err
}

func newSortOrderRaceFixture(t *testing.T) (*Service, *racingNodeRepo, ports.Repos, *domain.Node) {
	t.Helper()
	sqlstore.ConfigureSecretKey("test-only-sort-order-race-key")
	t.Cleanup(func() { sqlstore.ConfigureSecretKey("") })
	db, err := sqlstore.Open("sqlite", filepath.Join(t.TempDir(), "race.db"))
	if err != nil {
		t.Fatal(err)
	}
	if err := sqlstore.EnsureSchema(db); err != nil {
		t.Fatal(err)
	}
	repos := sqlstore.NewRepos(db)
	n := &domain.Node{PanelID: 1, InboundID: 1, DisplayName: "A", Region: "JP", DesiredProtocol: "vless", DesiredPort: 443, Enabled: true,
		SortOrder: 10, Relays: []domain.RelayLine{{ID: "ext", Name: "ext", Address: "e.example", Enabled: true}}}
	if err := repos.Node.Create(t.Context(), n); err != nil {
		t.Fatal(err)
	}
	racing := &racingNodeRepo{NodeRepo: repos.Node}
	svc := New(racing, repos.Separator, nil, nil, repos.SyncTask, repos.Group, repos.User)
	svc.SetSubscriptionInvalidator(func() {})
	return svc, racing, repos, n
}

func moveTo(t *testing.T, repos ports.Repos, id int64, order int) {
	t.Helper()
	if err := repos.Node.ApplySubscriptionOrder(t.Context(), ports.SubscriptionOrderWrite{Nodes: []ports.NodeSortUpdate{{NodeID: id, SortOrder: order}}}); err != nil {
		t.Fatal(err)
	}
}

// The HTTP edit path: the handler fills the node with what it read before the
// server locks, then the service saves it. A reorder that lands in between must
// win.
func TestMetadataSaveFromStaleCopyKeepsTheOrderedPosition(t *testing.T) {
	svc, _, repos, n := newSortOrderRaceFixture(t)
	stale, err := repos.Node.GetByID(t.Context(), n.ID)
	if err != nil {
		t.Fatal(err)
	}
	moveTo(t, repos, n.ID, 40)
	stale.DisplayName = "A renamed"
	if err := svc.UpdateMetadata(t.Context(), stale); err != nil {
		t.Fatal(err)
	}
	got, _ := repos.Node.GetByID(t.Context(), n.ID)
	if got.DisplayName != "A renamed" || got.SortOrder != 40 {
		t.Fatalf("name=%q sort_order=%d, want the rename and the ordered 40", got.DisplayName, got.SortOrder)
	}
}

// An internal caller that loads under the server lock and writes back what it
// loaded (adding a source address to a line): a reorder committing between its
// read and its write must also win.
func TestInternalMetadataWriterKeepsTheOrderedPosition(t *testing.T) {
	svc, racing, repos, n := newSortOrderRaceFixture(t)
	racing.getsLeft, racing.fire = 2, func() { moveTo(t, repos, n.ID, 40) } // after lockAndLoad's second read
	if _, err := svc.AddLineSourceCIDR(t.Context(), n.ID, "ext", "192.0.2.0/24"); err != nil {
		t.Fatal(err)
	}
	got, _ := repos.Node.GetByID(t.Context(), n.ID)
	if len(got.Relays[0].SourceCIDRs) != 1 {
		t.Fatalf("the save did not apply: %+v", got.Relays[0])
	}
	if got.SortOrder != 40 {
		t.Fatalf("sort_order = %d, want the ordered 40", got.SortOrder)
	}
}
