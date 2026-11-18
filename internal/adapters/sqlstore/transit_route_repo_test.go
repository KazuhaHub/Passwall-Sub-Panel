package sqlstore

import (
	"errors"
	"fmt"
	"testing"
	"time"

	"gorm.io/gorm"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
)

// seedNodes creates the nodes the routes are saved for: routes of a node that
// does not exist are refused.
func seedNodes(t *testing.T, db *gorm.DB, ids ...int64) {
	t.Helper()
	repo := NewRepos(db).Node
	for _, id := range ids {
		n := &domain.Node{ID: id, PanelID: 50, InboundID: int(id), DisplayName: fmt.Sprintf("node-%d", id), DesiredProtocol: "vless", DesiredPort: 6000 + int(id), Enabled: true, Region: "JP"}
		if err := repo.Create(t.Context(), n); err != nil {
			t.Fatal(err)
		}
		if n.ID != id {
			t.Fatalf("node %d was created as %d", id, n.ID)
		}
	}
}

func sampleRoutes() []domain.DestRoute {
	return []domain.DestRoute{
		{ID: "r1", Name: "streaming", Target: domain.DestRouteTarget{Kind: domain.DestRouteServer, PanelID: 9},
			Domains: []string{"domain:example.com"}, Ports: "443"},
		{ID: "r3", Target: domain.DestRouteTarget{Kind: domain.DestRouteLocal}, CIDRs: []string{"10.0.0.0/8"}, Network: "tcp"},
	}
}

func TestTransitRouteRepoRoundTripsAndKeepsTheDeliveryDigests(t *testing.T) {
	db, err := openIsolatedTestDB(t)
	if err != nil {
		t.Fatal(err)
	}
	if err := EnsureSchema(db); err != nil {
		t.Fatal(err)
	}
	repos := NewRepos(db)
	repo := repos.TransitRoutes
	seedNodes(t, db, 6, 7, 8)
	ctx := t.Context()
	now := time.Now().UTC().Truncate(time.Second)

	if got, err := repo.Get(ctx, 7); err != nil || len(got.Routes) != 0 || got.NodeID != 7 {
		t.Fatalf("a node without routes is an empty value: %+v %v", got, err)
	}
	// Nothing is recorded for a node that has no routes.
	if moved, err := repo.RecordSent(ctx, 7, "abc", "", now); err != nil || moved {
		t.Fatalf("sent on a node without routes: %v %v", moved, err)
	}
	if err := repo.Replace(ctx, 7, sampleRoutes()); err != nil {
		t.Fatal(err)
	}
	got, err := repo.Get(ctx, 7)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Routes) != 2 || got.Routes[0].Name != "streaming" || got.Routes[0].Target.PanelID != 9 ||
		got.Routes[1].Target.Kind != domain.DestRouteLocal || got.Routes[1].CIDRs[0] != "10.0.0.0/8" || got.Routes[1].Network != "tcp" {
		t.Fatalf("round trip = %+v", got.Routes)
	}

	// Sent, then confirmed; a repeat writes nothing.
	if moved, err := repo.RecordSent(ctx, 7, "d1", "", now); err != nil || !moved {
		t.Fatalf("record sent: %v %v", moved, err)
	}
	if moved, err := repo.RecordSent(ctx, 7, "d1", "", now); err != nil || moved {
		t.Fatalf("a repeated digest: %v %v", moved, err)
	}
	if moved, err := repo.SetApplied(ctx, 7, "d1", now); err != nil || !moved {
		t.Fatalf("set applied: %v %v", moved, err)
	}
	running, err := repo.Get(ctx, 7)
	if err != nil || !running.Running() || running.SentAt == nil || running.AppliedAt == nil {
		t.Fatalf("running = %+v %v", running, err)
	}

	// Replacing the routes keeps the digests: the next sync moves "sent", and a
	// confirmation of the old content then no longer matches it.
	edited := sampleRoutes()[:1]
	if err := repo.Replace(ctx, 7, edited); err != nil {
		t.Fatal(err)
	}
	got, _ = repo.Get(ctx, 7)
	if len(got.Routes) != 1 || got.Sent != "d1" || got.Applied != "d1" {
		t.Fatalf("after replace = %+v", got)
	}
	if moved, err := repo.RecordSent(ctx, 7, "d2", "", now); err != nil || !moved {
		t.Fatalf("the new content: %v %v", moved, err)
	}
	if got, _ = repo.Get(ctx, 7); got.Running() {
		t.Fatalf("an older confirmation counts for newer content: %+v", got)
	}

	// Routes that are not being sent are not running, and an old confirmation
	// does not come back when they are sent again.
	if _, err := repo.SetApplied(ctx, 7, "d2", now); err != nil {
		t.Fatal(err)
	}
	if moved, err := repo.RecordSent(ctx, 7, "", "", now); err != nil || !moved {
		t.Fatalf("withdraw: %v %v", moved, err)
	}
	if got, _ = repo.Get(ctx, 7); got.Sent != "" || got.Applied != "" || got.SentAt != nil || got.Running() {
		t.Fatalf("after withdrawing = %+v", got)
	}

	// Why a node is withheld is stored beside the digest, and is a change by itself.
	if moved, err := repo.RecordSent(ctx, 7, "", "no_capability", now); err != nil || !moved {
		t.Fatalf("withhold: %v %v", moved, err)
	}
	if moved, err := repo.RecordSent(ctx, 7, "", "no_capability", now); err != nil || moved {
		t.Fatalf("a repeated reason: %v %v", moved, err)
	}
	if got, _ = repo.Get(ctx, 7); got.Withheld != "no_capability" {
		t.Fatalf("withheld = %q", got.Withheld)
	}
	node := &domain.Node{ID: 7}
	if err := attachRouteState(ctx, db, []*domain.Node{node}); err != nil || node.RoutesWithheld != "no_capability" || node.EgressPublishable() {
		t.Fatalf("a withheld node is published: %+v %v", node, err)
	}

	// A node with no routes left is not listed; the list is by node.
	if err := repo.Replace(ctx, 8, nil); err != nil {
		t.Fatal(err)
	}
	if err := repo.Replace(ctx, 6, sampleRoutes()); err != nil {
		t.Fatal(err)
	}
	all, err := repo.List(ctx)
	if err != nil || len(all) != 2 || all[0].NodeID != 6 || all[1].NodeID != 7 {
		t.Fatalf("list = %+v %v", all, err)
	}
}

// Loading nodes attaches their routes and delivery state, and deleting a node
// takes its routes with it.
func TestNodesCarryTheirRoutesAndDeleteThem(t *testing.T) {
	db, err := openIsolatedTestDB(t)
	if err != nil {
		t.Fatal(err)
	}
	if err := EnsureSchema(db); err != nil {
		t.Fatal(err)
	}
	repos := NewRepos(db)
	ctx := t.Context()
	routed := &domain.Node{PanelID: 1, InboundID: 2, DisplayName: "routed", DesiredProtocol: "vless", DesiredPort: 443, Region: "JP", Enabled: true}
	plain := &domain.Node{PanelID: 1, InboundID: 3, DisplayName: "plain", DesiredProtocol: "vless", DesiredPort: 444, Region: "JP", Enabled: true}
	for _, n := range []*domain.Node{routed, plain} {
		if err := repos.Node.Create(ctx, n); err != nil {
			t.Fatal(err)
		}
	}
	if err := repos.TransitRoutes.Replace(ctx, routed.ID, sampleRoutes()); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	if _, err := repos.TransitRoutes.RecordSent(ctx, routed.ID, "d", "", now); err != nil {
		t.Fatal(err)
	}
	if _, err := repos.TransitRoutes.SetApplied(ctx, routed.ID, "d", now); err != nil {
		t.Fatal(err)
	}

	got, err := repos.Node.GetByID(ctx, routed.ID)
	if err != nil || len(got.DestRoutes) != 2 || !got.RoutesRunning() {
		t.Fatalf("routed node = %+v %v", got, err)
	}
	snapshot, err := repos.NativeDesired.Load(ctx, 1)
	if err != nil || len(snapshot.Nodes) != 2 || len(snapshot.Nodes[0].DestRoutes) != 2 || len(snapshot.Nodes[1].DestRoutes) != 0 {
		t.Fatalf("desired snapshot = %+v %v", snapshot, err)
	}
	// A whole-row write of the node (an import, a poll) does not touch them.
	got.DisplayName = "renamed"
	if err := repos.Node.Update(ctx, got); err != nil {
		t.Fatal(err)
	}
	if again, err := repos.Node.GetByID(ctx, routed.ID); err != nil || len(again.DestRoutes) != 2 {
		t.Fatalf("after a whole-row save = %+v %v", again, err)
	}
	if err := repos.Node.Delete(ctx, routed.ID); err != nil {
		t.Fatal(err)
	}
	if stored, err := repos.TransitRoutes.Get(ctx, routed.ID); err != nil || len(stored.Routes) != 0 {
		t.Fatalf("routes of a deleted node = %+v %v", stored, err)
	}
}

// A database written before routes existed gains the table on the next start
// and keeps everything else; running the upgrade again changes nothing.
func TestTransitRoutesTableIsAddedToAnOlderDatabase(t *testing.T) {
	db, err := openIsolatedTestDB(t)
	if err != nil {
		t.Fatal(err)
	}
	if err := EnsureSchema(db); err != nil {
		t.Fatal(err)
	}
	repos := NewRepos(db)
	landing := &domain.Node{PanelID: 1, InboundID: 2, DisplayName: "JP", DesiredProtocol: "vless", DesiredPort: 443, Region: "JP", Enabled: true}
	if err := repos.Node.Create(t.Context(), landing); err != nil {
		t.Fatal(err)
	}
	if err := db.Migrator().DropTable(&transitRouteRow{}); err != nil {
		t.Fatal(err)
	}
	if db.Migrator().HasTable(&transitRouteRow{}) {
		t.Fatal("the table was not dropped")
	}
	for round := 0; round < 2; round++ {
		if err := EnsureSchema(db); err != nil {
			t.Fatalf("EnsureSchema round %d: %v", round, err)
		}
		if !db.Migrator().HasTable(&transitRouteRow{}) {
			t.Fatalf("round %d: no transit_routes table", round)
		}
	}
	// A node that never had routes loads as it did, and one can be given some.
	got, err := NewRepos(db).Node.GetByID(t.Context(), landing.ID)
	if err != nil || got.DisplayName != "JP" || len(got.DestRoutes) != 0 || got.RoutesSent != "" {
		t.Fatalf("existing data after upgrade: %+v %v", got, err)
	}
	if err := NewRepos(db).TransitRoutes.Replace(t.Context(), landing.ID, sampleRoutes()); err != nil {
		t.Fatalf("replace after upgrade: %v", err)
	}
}

// A list a node's routes name is held like one a policy names: it cannot be
// deleted while they do, the check is made under the same lock the route write
// takes, and a route cannot be saved naming a list that is gone.
func TestRoutesHoldTheListsTheyName(t *testing.T) {
	repos, db, ctx := newDestRepos(t)
	list := &domain.DestList{Name: "streaming", Kind: domain.DestListCustom, Side: domain.DestSideDest, Entries: []byte("domain:a.com"), EntryCount: 1}
	if err := repos.Lists.Create(ctx, list); err != nil {
		t.Fatal(err)
	}
	routes := NewRepos(db).TransitRoutes
	seedNodes(t, db, 6, 7, 8)
	named := []domain.DestRoute{{ID: "r1", Target: domain.DestRouteTarget{Kind: domain.DestRouteLocal}, ListIDs: []int64{list.ID}}}
	if err := routes.Replace(ctx, 7, named); err != nil {
		t.Fatal(err)
	}
	using, err := repos.Lists.DeleteUnused(ctx, list.ID)
	if err != nil || len(using.NodeIDs) != 1 || using.NodeIDs[0] != 7 {
		t.Fatalf("delete of a list a route holds: %+v %v", using, err)
	}
	if _, err := repos.Lists.GetByID(ctx, list.ID); err != nil {
		t.Fatalf("the list was deleted: %v", err)
	}
	if err := routes.Replace(ctx, 7, nil); err != nil {
		t.Fatal(err)
	}
	if using, err = repos.Lists.DeleteUnused(ctx, list.ID); err != nil || using.Any() {
		t.Fatalf("delete after the route let go: %+v %v", using, err)
	}
	if err := routes.Replace(ctx, 7, named); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("routes naming a deleted list: %v", err)
	}
}

// The last good expansion of a list is kept whole (tens of thousands of entries
// are past MySQL's TEXT), is replaced only by a different one, and is forgotten
// on request.
func TestTransitRouteRepoKeepsTheLastGoodListWhole(t *testing.T) {
	repos, db, ctx := newDestRepos(t)
	list := &domain.DestList{Name: "big", Kind: domain.DestListCustom, Side: domain.DestSideDest, Entries: []byte("domain:a.com"), EntryCount: 1}
	if err := repos.Lists.Create(ctx, list); err != nil {
		t.Fatal(err)
	}
	repo := NewRepos(db).TransitRoutes
	seedNodes(t, db, 6, 7, 8)
	if _, _, found, err := repo.GoodList(ctx, list.ID); err != nil || found {
		t.Fatalf("nothing kept yet: %v %v", found, err)
	}
	// Nothing is kept for a list no route names: a save that raced a route's
	// release of it must not bring content back.
	if err := repo.SaveGoodList(ctx, list.ID, "d0", []string{"domain:a.com"}, nil); err != nil {
		t.Fatal(err)
	}
	if _, _, found, _ := repo.GoodList(ctx, list.ID); found {
		t.Fatal("content was kept for a list no route names")
	}
	if err := repo.Replace(ctx, 7, []domain.DestRoute{{ID: "r1", Target: domain.DestRouteTarget{Kind: domain.DestRouteLocal}, ListIDs: []int64{list.ID}}}); err != nil {
		t.Fatal(err)
	}
	var domains []string
	for i := 0; i < 40000; i++ {
		domains = append(domains, fmt.Sprintf("domain:host%05d.example.com", i))
	}
	cidrs := []string{"10.0.0.0/8", "198.51.100.0/24"}
	if err := repo.SaveGoodList(ctx, list.ID, "d1", domains, cidrs); err != nil {
		t.Fatal(err)
	}
	got, gotCIDRs, found, err := repo.GoodList(ctx, list.ID)
	if err != nil || !found || len(got) != 40000 || got[39999] != domains[39999] || len(gotCIDRs) != 2 || gotCIDRs[1] != cidrs[1] {
		t.Fatalf("round trip: %d %v %v %v", len(got), gotCIDRs, found, err)
	}
	if err := repo.SaveGoodList(ctx, list.ID, "d1", []string{"domain:other.com"}, nil); err != nil {
		t.Fatal(err)
	}
	if got, _, _, _ = repo.GoodList(ctx, list.ID); len(got) != 40000 {
		t.Fatal("a save with the same digest rewrote the list")
	}
	if err := repo.SaveGoodList(ctx, list.ID, "d2", []string{"domain:other.com"}, nil); err != nil {
		t.Fatal(err)
	}
	if got, gotCIDRs, _, _ = repo.GoodList(ctx, list.ID); len(got) != 1 || len(gotCIDRs) != 0 {
		t.Fatalf("a different expansion: %v %v", got, gotCIDRs)
	}
	if err := repo.ForgetGoodList(ctx, list.ID); err != nil {
		t.Fatal(err)
	}
	if _, _, found, _ = repo.GoodList(ctx, list.ID); found {
		t.Fatal("forgotten but kept")
	}
}

// A route with thousands of typed entries is past MySQL's 64 KiB TEXT: it is
// stored whole, on every dialect.
func TestTransitRoutesStoreARouteOfFiveThousandEntries(t *testing.T) {
	db, err := openIsolatedTestDB(t)
	if err != nil {
		t.Fatal(err)
	}
	if err := EnsureSchema(db); err != nil {
		t.Fatal(err)
	}
	repo := NewRepos(db).TransitRoutes
	seedNodes(t, db, 6, 7, 8)
	var typed []string
	for i := 0; i < 5000; i++ {
		typed = append(typed, fmt.Sprintf("domain:host%05d.example.com", i))
	}
	route := domain.DestRoute{ID: "r1", Target: domain.DestRouteTarget{Kind: domain.DestRouteLocal}, Domains: typed}
	if err := domain.ValidateDestRoutes([]domain.DestRoute{route}); err != nil {
		t.Fatal(err)
	}
	if err := repo.Replace(t.Context(), 7, []domain.DestRoute{route}); err != nil {
		t.Fatalf("a 5000-entry route: %v", err)
	}
	got, err := repo.Get(t.Context(), 7)
	if err != nil || len(got.Routes) != 1 || len(got.Routes[0].Domains) != 5000 || got.Routes[0].Domains[4999] != typed[4999] {
		t.Fatalf("round trip: %d %v", len(got.Routes), err)
	}
}

// legacyRoutes is the table a 4.3 beta created: the routes in a TEXT column named
// spec.
type legacyRoutes struct {
	NodeID    int64  `gorm:"primaryKey;autoIncrement:false"`
	Spec      string `gorm:"type:text;not null"`
	Sent      string `gorm:"size:64;not null;default:''"`
	Applied   string `gorm:"size:64;not null;default:''"`
	Withheld  string `gorm:"size:32;not null;default:''"`
	SentAt    *time.Time
	AppliedAt *time.Time
	UpdatedAt time.Time
}

func (legacyRoutes) TableName() string { return "transit_routes" }

// A database a 4.3 beta already created the routes table in (with spec) gains the
// byte column, keeps every node's routes and delivery digests, and loses the old
// column; a second start changes nothing.
func TestARoutesTableOfABetaIsCarriedOverToTheByteColumn(t *testing.T) {
	db, err := openIsolatedTestDB(t)
	if err != nil {
		t.Fatal(err)
	}
	if err := EnsureSchema(db); err != nil {
		t.Fatal(err)
	}
	if err := db.Migrator().DropTable(&transitRouteRow{}); err != nil {
		t.Fatal(err)
	}
	if err := db.Migrator().CreateTable(&legacyRoutes{}); err != nil {
		t.Fatal(err)
	}
	spec := `[{"id":"r1","target":{"kind":"local"},"domains":["domain:a.com"]}]`
	if err := db.Create(&legacyRoutes{NodeID: 7, Spec: spec, Sent: "dd", Applied: "dd", UpdatedAt: time.Now().UTC()}).Error; err != nil {
		t.Fatal(err)
	}
	for round := 0; round < 2; round++ {
		if err := EnsureSchema(db); err != nil {
			t.Fatalf("EnsureSchema round %d: %v", round, err)
		}
		if db.Migrator().HasColumn(&retiredRouteSpec{}, "spec") {
			t.Fatalf("round %d: the old column is still there", round)
		}
		got, err := NewRepos(db).TransitRoutes.Get(t.Context(), 7)
		if err != nil || len(got.Routes) != 1 || got.Routes[0].Domains[0] != "domain:a.com" || !got.Running() {
			t.Fatalf("round %d: %+v %v", round, got, err)
		}
	}
}

// The kept content of a list goes when no route names the list any more, and
// with a list that is deleted, so it cannot come back stale.
func TestKeptListContentGoesWhenNoRouteNamesTheList(t *testing.T) {
	repos, db, ctx := newDestRepos(t)
	list := &domain.DestList{Name: "streaming", Kind: domain.DestListCustom, Side: domain.DestSideDest, Entries: []byte("domain:a.com"), EntryCount: 1}
	if err := repos.Lists.Create(ctx, list); err != nil {
		t.Fatal(err)
	}
	routes := NewRepos(db).TransitRoutes
	seedNodes(t, db, 6, 7, 8)
	named := []domain.DestRoute{{ID: "r1", Target: domain.DestRouteTarget{Kind: domain.DestRouteLocal}, ListIDs: []int64{list.ID}}}
	if err := routes.Replace(ctx, 7, named); err != nil {
		t.Fatal(err)
	}
	if err := routes.SaveGoodList(ctx, list.ID, "d", []string{"domain:a.com"}, nil); err != nil {
		t.Fatal(err)
	}
	if err := routes.Replace(ctx, 7, named); err != nil {
		t.Fatal(err)
	}
	if _, _, found, _ := routes.GoodList(ctx, list.ID); !found {
		t.Fatal("a list a route still names lost its content")
	}
	// Another node's routes name it too: one letting go is not enough.
	if err := routes.Replace(ctx, 8, named); err != nil {
		t.Fatal(err)
	}
	if err := routes.Replace(ctx, 7, nil); err != nil {
		t.Fatal(err)
	}
	if _, _, found, _ := routes.GoodList(ctx, list.ID); !found {
		t.Fatal("the content went while another node's route names the list")
	}
	if err := deleteNodeRoutesTx(db, 8); err != nil {
		t.Fatal(err)
	}
	if _, _, found, _ := routes.GoodList(ctx, list.ID); found {
		t.Fatal("the content stayed after no route names the list")
	}
}

// Routes cannot be saved for a node that no longer exists: a save that raced the
// node's deletion would leave a row nothing removes (and that blocks deleting the
// lists it names). Clearing is always allowed, and the check is made under the
// lock a node's deletion also takes.
func TestRoutesAreRefusedForANodeThatIsGone(t *testing.T) {
	repos, db, ctx := newDestRepos(t)
	routes := NewRepos(db).TransitRoutes
	seedNodes(t, db, 7)
	list := &domain.DestList{Name: "streaming", Kind: domain.DestListCustom, Side: domain.DestSideDest, Entries: []byte("domain:a.com"), EntryCount: 1}
	if err := repos.Lists.Create(ctx, list); err != nil {
		t.Fatal(err)
	}
	named := []domain.DestRoute{{ID: "r1", Target: domain.DestRouteTarget{Kind: domain.DestRouteLocal}, ListIDs: []int64{list.ID}}}
	if err := routes.Replace(ctx, 7, named); err != nil {
		t.Fatal(err)
	}
	if err := NewRepos(db).Node.Delete(ctx, 7); err != nil {
		t.Fatal(err)
	}
	if err := routes.Replace(ctx, 7, named); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("routes for a deleted node: %v", err)
	}
	if got, err := routes.Get(ctx, 7); err != nil || len(got.Routes) != 0 {
		t.Fatalf("an orphan row was left: %+v %v", got, err)
	}
	if using, err := repos.Lists.DeleteUnused(ctx, list.ID); err != nil || using.Any() {
		t.Fatalf("the list is held by a node that is gone: %+v %v", using, err)
	}
	if err := routes.Replace(ctx, 7, nil); err != nil {
		t.Fatalf("clearing the routes of a gone node: %v", err)
	}
	if err := routes.Replace(ctx, 999, named[:0]); err != nil {
		t.Fatalf("an empty list for a node that never existed: %v", err)
	}
}
