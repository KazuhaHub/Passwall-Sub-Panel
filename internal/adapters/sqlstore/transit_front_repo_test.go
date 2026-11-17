package sqlstore

import (
	"errors"
	"testing"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
)

func sampleFront(panelID int64, port int) domain.TransitFront {
	return domain.TransitFront{
		PanelID: panelID, Port: port,
		Listeners: []domain.FrontListener{{NodeID: 4, LoopbackPort: 30001}},
		Routes: []domain.FrontRoute{
			{ServerNames: []string{"a.example.com", "b.example.com"}, Target: domain.FrontTarget{Kind: domain.FrontTargetListener, NodeID: 4}},
			{ServerNames: []string{"land.example.com"}, Target: domain.FrontTarget{Kind: domain.FrontTargetForward, NodeID: 5, LineID: "x"}},
		},
		Default: domain.FrontTarget{Kind: domain.FrontTargetExternal, Host: "www.example.com", Port: 443},
	}
}

func TestTransitFrontRepoRoundTripsAndKeepsOneFrontPerPort(t *testing.T) {
	db, err := openIsolatedTestDB(t)
	if err != nil {
		t.Fatal(err)
	}
	if err := EnsureSchema(db); err != nil {
		t.Fatal(err)
	}
	repo := NewRepos(db).TransitFronts
	ctx := t.Context()

	created, err := repo.Create(ctx, sampleFront(7, 443))
	if err != nil || created.ID == 0 {
		t.Fatalf("create: %+v %v", created, err)
	}
	if _, err := repo.Create(ctx, sampleFront(7, 443)); !errors.Is(err, domain.ErrAlreadyExists) {
		t.Fatalf("a second front on one port: %v", err)
	}
	if _, err := repo.Create(ctx, sampleFront(8, 443)); err != nil {
		t.Fatalf("the same port on another server: %v", err)
	}
	other, err := repo.Create(ctx, sampleFront(7, 8443))
	if err != nil {
		t.Fatal(err)
	}

	got, err := repo.Get(ctx, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.PanelID != 7 || got.Port != 443 || len(got.Routes) != 2 || got.Routes[1].Target.LineID != "x" ||
		got.Listeners[0].LoopbackPort != 30001 || got.Default.Host != "www.example.com" || got.Routes[0].ServerNames[1] != "b.example.com" {
		t.Fatalf("round trip = %+v", got)
	}
	list, err := repo.ListByPanel(ctx, 7)
	if err != nil || len(list) != 2 || list[0].Port != 443 || list[1].Port != 8443 {
		t.Fatalf("list = %+v %v", list, err)
	}
	all, err := repo.List(ctx)
	if err != nil || len(all) != 3 {
		t.Fatalf("all = %d %v", len(all), err)
	}

	// Editing the port onto another front's port is refused; any free port works.
	moved := got
	moved.Port = 8443
	if _, err := repo.Update(ctx, moved); !errors.Is(err, domain.ErrAlreadyExists) {
		t.Fatalf("update onto a taken port: %v", err)
	}
	moved.Port, moved.Default.Port = 2053, 8443
	updated, err := repo.Update(ctx, moved)
	if err != nil || updated.Port != 2053 || updated.Default.Port != 8443 {
		t.Fatalf("update = %+v %v", updated, err)
	}
	if _, err := repo.Update(ctx, domain.TransitFront{ID: 999, PanelID: 7, Port: 1}); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("update of a missing front: %v", err)
	}

	// A front is deleted only through its own server.
	if ok, err := repo.Delete(ctx, 8, other.ID); err != nil || ok {
		t.Fatalf("delete through another server: %v %v", ok, err)
	}
	if ok, err := repo.Delete(ctx, 7, other.ID); err != nil || !ok {
		t.Fatalf("delete: %v %v", ok, err)
	}
	if _, err := repo.Get(ctx, other.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("get after delete: %v", err)
	}
}

// A database written before the fronts existed gains the table on the next
// start and keeps everything else; running the upgrade again changes nothing.
func TestTransitFrontsTableIsAddedToAnOlderDatabase(t *testing.T) {
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
	if err := db.Migrator().DropTable(&transitFrontRow{}); err != nil {
		t.Fatal(err)
	}
	if db.Migrator().HasTable(&transitFrontRow{}) {
		t.Fatal("the table was not dropped")
	}
	for round := 0; round < 2; round++ {
		if err := EnsureSchema(db); err != nil {
			t.Fatalf("EnsureSchema round %d: %v", round, err)
		}
		if !db.Migrator().HasTable(&transitFrontRow{}) {
			t.Fatalf("round %d: no transit_fronts table", round)
		}
	}
	if _, err := NewRepos(db).TransitFronts.Create(t.Context(), sampleFront(1, 443)); err != nil {
		t.Fatalf("create after upgrade: %v", err)
	}
	if got, err := NewRepos(db).Node.GetByID(t.Context(), landing.ID); err != nil || got.DisplayName != "JP" {
		t.Fatalf("existing data after upgrade: %+v %v", got, err)
	}
}
