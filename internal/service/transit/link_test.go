package transit

import (
	"context"
	"path/filepath"
	"testing"

	nodeprotocol "github.com/KazuhaHub/passwall-protocol/protocol"

	"github.com/KazuhaHub/passwall-sub-panel/internal/adapters/sqlstore"
	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

type fakeAgents map[int64]string

func (f fakeAgents) ListByPanelIDs(_ context.Context, panelIDs []int64) ([]*domain.NodeAgent, error) {
	var out []*domain.NodeAgent
	for _, id := range panelIDs {
		if address, ok := f[id]; ok {
			out = append(out, &domain.NodeAgent{PanelID: id, ObservedAddress: address})
		}
	}
	return out, nil
}

func linkStore(t *testing.T) ports.Repos {
	t.Helper()
	sqlstore.ConfigureSecretKey("test-only-transit-link-key")
	t.Cleanup(func() { sqlstore.ConfigureSecretKey("") })
	db, err := sqlstore.Open("sqlite", filepath.Join(t.TempDir(), "links.db"))
	if err != nil {
		t.Fatal(err)
	}
	connection, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = connection.Close() })
	if err := sqlstore.EnsureSchema(db); err != nil {
		t.Fatal(err)
	}
	return sqlstore.NewRepos(db)
}

func TestLinksForGivesEachSideItsHalf(t *testing.T) {
	repos := linkStore(t)
	bound := landing(1, 9, "entry.example", 443)
	bound.EgressPanelID = 3
	other := landing(2, 9, "entry.example", 8443)
	exitListener := landing(3, 3, "exit.example", 443)
	nodes := fakeNodes{bound, other, exitListener}
	service := New(nodes, newFakeRepo()).WithLinks(repos.TransitLinks, fakeAgents{3: "198.51.100.7"})
	ctx := context.Background()

	entry, err := service.LinksFor(ctx, 9, map[int]bool{443: true, 8443: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(entry.Out) != 1 || len(entry.In) != 0 || len(entry.Egress) != 1 || !entry.Changed {
		t.Fatalf("entry plan = %+v", entry)
	}
	out := entry.Out[0]
	if out.Address != "198.51.100.7" || out.Protocol != nodeprotocol.LinkProtocolSS2022 {
		t.Fatalf("outgoing link = %+v", out)
	}
	if entry.Egress[0] != (nodeprotocol.EgressBinding{Listener: nodeprotocol.NewListenerKey(1), Link: out.Key}) {
		t.Fatalf("egress = %+v", entry.Egress)
	}
	if entry.EgressExit[1] != 3 || len(entry.EgressExit) != 1 {
		t.Fatalf("egress exits = %v", entry.EgressExit)
	}

	exit, err := service.LinksFor(ctx, 3, map[int]bool{443: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(exit.In) != 1 || len(exit.Out) != 0 || len(exit.Egress) != 0 {
		t.Fatalf("exit plan = %+v", exit)
	}
	in := exit.In[0]
	if in.Key != out.Key || in.Port != out.Port || in.Port < linkPortLow || in.Port >= linkPortHigh || in.SS.Password != out.SS.Password {
		t.Fatalf("halves do not match: in %+v, out %+v", in, out)
	}

	// A second round sends the same thing and changes nothing.
	again, err := service.LinksFor(ctx, 9, map[int]bool{443: true, 8443: true})
	if err != nil || again.Changed {
		t.Fatalf("an unchanged round reported a change: %+v, %v", again, err)
	}

	// A listener moving onto the link's port withholds the incoming half,
	// which is recorded so the bound node is withdrawn.
	blocked, err := service.LinksFor(ctx, 3, map[int]bool{443: true, in.Port: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(blocked.In) != 0 || !blocked.Changed {
		t.Fatalf("a colliding incoming link was sent: %+v", blocked)
	}

	// Without an address for the exit, the entry gets nothing to dial.
	nowhere := New(nodes, newFakeRepo()).WithLinks(repos.TransitLinks, fakeAgents{})
	lost, err := nowhere.LinksFor(ctx, 9, map[int]bool{})
	if err != nil {
		t.Fatal(err)
	}
	if len(lost.Out) != 0 || len(lost.Egress) != 0 || len(lost.EgressExit) != 0 {
		t.Fatalf("a link without an address was sent: %+v", lost)
	}
}

func TestUpdateLinkSwitchesProtocolAndAddress(t *testing.T) {
	repos := linkStore(t)
	bound := landing(1, 9, "entry.example", 443)
	bound.EgressPanelID = 3
	service := New(fakeNodes{bound}, newFakeRepo()).WithLinks(repos.TransitLinks, fakeAgents{3: "198.51.100.7"})
	ctx := context.Background()

	fresh, err := service.Link(ctx, 9, 3)
	if err != nil || fresh.Exists || fresh.Protocol != nodeprotocol.LinkProtocolSS2022 || fresh.ServerAddress != "198.51.100.7" {
		t.Fatalf("defaults = %+v, %v", fresh, err)
	}
	view, err := service.UpdateLink(ctx, 9, 3, domain.TransitLinkSettings{Protocol: nodeprotocol.LinkProtocolVLESSReality, ExitAddress: " exit.example.com "})
	if err != nil {
		t.Fatal(err)
	}
	if !view.Exists || view.ExitAddress != "exit.example.com" || view.RealityServerName == "" || view.RealityTarget != view.RealityServerName+":443" {
		t.Fatalf("updated = %+v", view)
	}
	plan, err := service.LinksFor(ctx, 9, map[int]bool{})
	if err != nil {
		t.Fatal(err)
	}
	out := plan.Out[0]
	if out.Protocol != nodeprotocol.LinkProtocolVLESSReality || out.Address != "exit.example.com" || out.Reality == nil || out.Reality.ServerName != view.RealityServerName {
		t.Fatalf("outgoing link = %+v", out)
	}
	if _, err := service.UpdateLink(ctx, 9, 3, domain.TransitLinkSettings{Protocol: "wireguard"}); err == nil {
		t.Fatal("an unknown protocol was accepted")
	}
	custom, err := service.UpdateLink(ctx, 9, 3, domain.TransitLinkSettings{Protocol: nodeprotocol.LinkProtocolVLESSReality, RealityServerName: "www.example.org"})
	if err != nil || custom.RealityTarget != "www.example.org:443" {
		t.Fatalf("custom camouflage = %+v, %v", custom, err)
	}
}
