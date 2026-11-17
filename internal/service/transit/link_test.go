package transit

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

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

	entry, err := service.LinksFor(ctx, 9, map[int]bool{443: true, 8443: true}, false)
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

	exit, err := service.LinksFor(ctx, 3, map[int]bool{443: true}, false)
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
	again, err := service.LinksFor(ctx, 9, map[int]bool{443: true, 8443: true}, false)
	if err != nil || again.Changed {
		t.Fatalf("an unchanged round reported a change: %+v, %v", again, err)
	}

	// A listener moving onto the link's port withholds the incoming half,
	// which is recorded so the bound node is withdrawn.
	blocked, err := service.LinksFor(ctx, 3, map[int]bool{443: true, in.Port: true}, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(blocked.In) != 0 || !blocked.Changed {
		t.Fatalf("a colliding incoming link was sent: %+v", blocked)
	}

	// Without an address for the exit, the entry gets nothing to dial.
	nowhere := New(nodes, newFakeRepo()).WithLinks(repos.TransitLinks, fakeAgents{})
	lost, err := nowhere.LinksFor(ctx, 9, map[int]bool{}, false)
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
	plan, err := service.LinksFor(ctx, 9, map[int]bool{}, false)
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

// ---- link counters ----

type linkFixture struct {
	service   *Service
	repos     ports.Repos
	entrySent *nodeprotocol.Transit
	exitSent  *nodeprotocol.Transit
	key       nodeprotocol.LinkKey
	id        int64
}

// newLinkFixture creates the link between entry server 9 and exit server 3 and
// the two configs the panel would have sent them.
func newLinkFixture(t *testing.T) linkFixture {
	t.Helper()
	repos := linkStore(t)
	bound := landing(1, 9, "entry.example", 443)
	bound.EgressPanelID = 3
	exitListener := landing(3, 3, "exit.example", 443)
	service := New(fakeNodes{bound, exitListener}, newFakeRepo()).WithLinks(repos.TransitLinks, fakeAgents{3: "198.51.100.7"})
	entry, err := service.LinksFor(t.Context(), 9, map[int]bool{443: true}, false)
	if err != nil || len(entry.Out) != 1 {
		t.Fatalf("entry plan = %+v, %v", entry, err)
	}
	exit, err := service.LinksFor(t.Context(), 3, map[int]bool{443: true}, false)
	if err != nil || len(exit.In) != 1 {
		t.Fatalf("exit plan = %+v, %v", exit, err)
	}
	id, err := entry.Out[0].Key.RowID()
	if err != nil {
		t.Fatal(err)
	}
	return linkFixture{
		service: service, repos: repos, key: entry.Out[0].Key, id: id,
		entrySent: &nodeprotocol.Transit{LinksOut: entry.Out}, exitSent: &nodeprotocol.Transit{LinksIn: exit.In},
	}
}

func (f linkFixture) totals(t *testing.T) domain.TransitLink {
	t.Helper()
	links, err := f.repos.TransitLinks.ListLinks(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	for _, link := range links {
		if link.ID == f.id {
			return link
		}
	}
	t.Fatalf("link %d is gone", f.id)
	return domain.TransitLink{}
}

func counter(key nodeprotocol.LinkKey, up, down int64, epoch uint64) nodeprotocol.LinkCounters {
	return nodeprotocol.LinkCounters{Key: key, Present: true, UpBytes: up, DownBytes: down, CounterEpoch: epoch}
}

// The server that holds the incoming half (the exit) books on In, the one that
// holds the outgoing half (the entry) on Out, each only for what it was sent.
func TestRecordLinkCountersBooksOnTheSideTheReporterHolds(t *testing.T) {
	f := newLinkFixture(t)
	ctx := t.Context()
	if err := f.service.RecordLinkCounters(ctx, f.exitSent, []nodeprotocol.LinkCounters{counter(f.key, 11, 22, 1)}); err != nil {
		t.Fatal(err)
	}
	if got := f.totals(t); got.InUpBytes != 11 || got.InDownBytes != 22 || got.OutUpBytes != 0 || got.OutDownBytes != 0 {
		t.Fatalf("after the exit's report: in %d/%d out %d/%d", got.InUpBytes, got.InDownBytes, got.OutUpBytes, got.OutDownBytes)
	}
	if err := f.service.RecordLinkCounters(ctx, f.entrySent, []nodeprotocol.LinkCounters{counter(f.key, 33, 44, 1)}); err != nil {
		t.Fatal(err)
	}
	if got := f.totals(t); got.InUpBytes != 11 || got.InDownBytes != 22 || got.OutUpBytes != 33 || got.OutDownBytes != 44 {
		t.Fatalf("after the entry's report: in %d/%d out %d/%d", got.InUpBytes, got.InDownBytes, got.OutUpBytes, got.OutDownBytes)
	}
}

func TestRecordLinkCountersIgnoresWhatWasNotSentOrIsNotRunning(t *testing.T) {
	f := newLinkFixture(t)
	ctx := t.Context()
	err := f.service.RecordLinkCounters(ctx, f.exitSent, []nodeprotocol.LinkCounters{
		counter(nodeprotocol.NewLinkKey(f.id+100), 999, 999, 1),                     // a key this server was not sent
		{Key: f.key, Present: false, UpBytes: 999, DownBytes: 999, CounterEpoch: 1}, // not running
	})
	if err != nil {
		t.Fatal(err)
	}
	// A server whose config carried no link at all cannot book one either.
	if err := f.service.RecordLinkCounters(ctx, &nodeprotocol.Transit{}, []nodeprotocol.LinkCounters{counter(f.key, 5, 5, 1)}); err != nil {
		t.Fatal(err)
	}
	if err := f.service.RecordLinkCounters(ctx, nil, []nodeprotocol.LinkCounters{counter(f.key, 5, 5, 1)}); err != nil {
		t.Fatal(err)
	}
	if got := f.totals(t); got.InUpBytes+got.InDownBytes+got.OutUpBytes+got.OutDownBytes != 0 {
		t.Fatalf("booked %+v", got)
	}
}

// Within one counter epoch a sample books what grew; a new epoch (the core
// restarted) books the whole value; a replay books nothing.
func TestRecordLinkCountersSameEpochBooksTheDeltaNewEpochTheWhole(t *testing.T) {
	f := newLinkFixture(t)
	for _, sample := range []struct {
		up, down int64
		epoch    uint64
	}{
		{100, 1000, 1}, // first sample of an epoch counts whole
		{150, 1500, 1}, // +50 / +500
		{150, 1500, 1}, // replay: nothing
		{20, 30, 2},    // new epoch: the whole value
	} {
		if err := f.service.RecordLinkCounters(t.Context(), f.entrySent, []nodeprotocol.LinkCounters{counter(f.key, sample.up, sample.down, sample.epoch)}); err != nil {
			t.Fatal(err)
		}
	}
	if got := f.totals(t); got.OutUpBytes != 100+50+20 || got.OutDownBytes != 1000+500+30 {
		t.Fatalf("totals = %d / %d, want 170 / 1530", got.OutUpBytes, got.OutDownBytes)
	}
}

// Both ends meter the same traffic, so the administrator sees the entry's
// figure, and the exit's only while the entry has reported none.
func TestLinkViewPrefersTheEntryTotalsAndFallsBackToTheExit(t *testing.T) {
	f := newLinkFixture(t)
	ctx := t.Context()
	view := func() LinkView {
		t.Helper()
		v, err := f.service.Link(ctx, 9, 3)
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
	if v := view(); v.UpBytes != 0 || v.DownBytes != 0 {
		t.Fatalf("an unmetered link shows %d/%d", v.UpBytes, v.DownBytes)
	}
	if err := f.service.RecordLinkCounters(ctx, f.exitSent, []nodeprotocol.LinkCounters{counter(f.key, 7, 8, 1)}); err != nil {
		t.Fatal(err)
	}
	if v := view(); v.UpBytes != 7 || v.DownBytes != 8 {
		t.Fatalf("with only the exit's totals the view shows %d/%d, want 7/8", v.UpBytes, v.DownBytes)
	}
	if err := f.service.RecordLinkCounters(ctx, f.entrySent, []nodeprotocol.LinkCounters{counter(f.key, 70, 80, 1)}); err != nil {
		t.Fatal(err)
	}
	if v := view(); v.UpBytes != 70 || v.DownBytes != 80 {
		t.Fatalf("with both the view shows %d/%d, want the entry's 70/80", v.UpBytes, v.DownBytes)
	}
}

// ---- routes ----

func routedNode() *domain.Node {
	n := landing(1, 9, "entry.example", 443)
	n.DestRoutes = []domain.DestRoute{
		{ID: "r1", Target: domain.DestRouteTarget{Kind: domain.DestRouteServer, PanelID: 3}, Domains: []string{"domain:a.com"}},
		{ID: "r2", Target: domain.DestRouteTarget{Kind: domain.DestRouteLocal}, Domains: []string{"domain:b.com"}},
	}
	return n
}

// A route's exit gets a link made the way an egress link is, even though no node
// is bound to it: the entry holds its outgoing half only when it declared
// routes, the exit holds the incoming half either way, and nothing is bound.
func TestLinksForMakesTheTunnelOfARoutedExit(t *testing.T) {
	repos := linkStore(t)
	service := New(fakeNodes{routedNode(), landing(3, 3, "exit.example", 443)}, newFakeRepo()).
		WithLinks(repos.TransitLinks, fakeAgents{3: "198.51.100.7"})
	ctx := context.Background()

	without, err := service.LinksFor(ctx, 9, map[int]bool{443: true}, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(without.Out) != 0 || len(without.RouteOut) != 0 || len(without.RouteKeys) != 0 {
		t.Fatalf("an entry that did not declare routes got %+v", without)
	}
	exit, err := service.LinksFor(ctx, 3, map[int]bool{443: true}, false)
	if err != nil || len(exit.In) != 1 {
		t.Fatalf("exit plan = %+v, %v", exit, err)
	}

	entry, err := service.LinksFor(ctx, 9, map[int]bool{443: true}, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(entry.Out) != 1 || len(entry.Egress) != 0 || len(entry.EgressExit) != 0 {
		t.Fatalf("entry plan = %+v", entry)
	}
	key := entry.RouteOut[3]
	if key == "" || key != entry.Out[0].Key || key != exit.In[0].Key || entry.RouteKeys[3] != key {
		t.Fatalf("route links %+v do not reach the exit's %+v", entry.RouteKeys, exit.In[0].Key)
	}
	// A route that leaves locally needs no link, and the link is the same one
	// each time it is asked for.
	again, _ := service.LinksFor(ctx, 9, map[int]bool{443: true}, true)
	if len(again.Out) != 1 || again.RouteOut[3] != key {
		t.Fatalf("second plan = %+v", again)
	}

	// The link the route needs is the link the default exit uses when the node's
	// own exit is the same server: one link, with the node bound to it.
	routed := routedNode()
	routed.EgressPanelID = 3
	service = New(fakeNodes{routed, landing(3, 3, "exit.example", 443)}, newFakeRepo()).WithLinks(repos.TransitLinks, fakeAgents{3: "198.51.100.7"})
	both, err := service.LinksFor(ctx, 9, map[int]bool{443: true}, true)
	if err != nil || len(both.Out) != 1 || len(both.Egress) != 1 || both.RouteOut[3] != both.Out[0].Key {
		t.Fatalf("plan with the default exit through the same server = %+v, %v", both, err)
	}
}

// A disabled node's routes make no link, and a route to the node's own server
// is not a tunnel.
func TestLinksForIgnoresRoutesThatAreNotInForce(t *testing.T) {
	repos := linkStore(t)
	off := routedNode()
	off.Enabled = false
	self := landing(2, 9, "entry.example", 8443)
	self.DestRoutes = []domain.DestRoute{{ID: "r1", Target: domain.DestRouteTarget{Kind: domain.DestRouteServer, PanelID: 9}, Domains: []string{"domain:a.com"}}}
	service := New(fakeNodes{off, self, landing(3, 3, "exit.example", 443)}, newFakeRepo()).WithLinks(repos.TransitLinks, fakeAgents{3: "198.51.100.7"})
	for _, panel := range []int64{9, 3} {
		plan, err := service.LinksFor(context.Background(), panel, map[int]bool{}, true)
		if err != nil || len(plan.In)+len(plan.Out) != 0 {
			t.Fatalf("panel %d plan = %+v, %v", panel, plan, err)
		}
	}
}

// EnsureLink makes the link a route is saved against, so the administrator can
// see it, and returns the same one again.
func TestEnsureLinkMakesTheLinkOnceAndLinkRowReadsIt(t *testing.T) {
	repos := linkStore(t)
	service := New(fakeNodes{landing(3, 3, "exit.example", 443)}, newFakeRepo()).WithLinks(repos.TransitLinks, fakeAgents{3: "198.51.100.7"})
	ctx := context.Background()
	if _, err := service.LinkRow(ctx, 9, 3); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("a link that does not exist: %v", err)
	}
	first, err := service.EnsureLink(ctx, 9, 3)
	if err != nil || first.ID == 0 || first.Port < 20000 {
		t.Fatalf("ensure = %+v, %v", first, err)
	}
	again, err := service.EnsureLink(ctx, 9, 3)
	if err != nil || again.ID != first.ID || again.SSKey != first.SSKey {
		t.Fatalf("a second ensure = %+v, %v", again, err)
	}
	if row, err := service.LinkRow(ctx, 9, 3); err != nil || row.ID != first.ID || row.Ready() {
		t.Fatalf("link row = %+v, %v", row, err)
	}
	if _, err := service.EnsureLink(ctx, 9, 9); !errors.Is(err, domain.ErrValidation) {
		t.Fatalf("a link to itself: %v", err)
	}
}

// A route to a server that no longer exists makes no tunnel: its listener is
// withheld by the routing plan, and nothing is created or kept for the exit.
func TestLinksForMakesNoTunnelToADeletedServer(t *testing.T) {
	repos := linkStore(t)
	service := New(fakeNodes{routedNode(), landing(3, 3, "exit.example", 443)}, newFakeRepo()).
		WithLinks(repos.TransitLinks, fakeAgents{3: "198.51.100.7"}).
		WithPanelCheck(func(_ context.Context, id int64) bool { return id == 3 })
	for _, panel := range []int64{9, 3} {
		plan, err := service.LinksFor(context.Background(), panel, map[int]bool{}, true)
		if err != nil || len(plan.In)+len(plan.Out) != 0 || len(plan.RouteKeys) != 0 {
			t.Fatalf("panel %d plan = %+v, %v", panel, plan, err)
		}
	}
	if links, _ := repos.TransitLinks.ListLinks(context.Background()); len(links) != 0 {
		t.Fatalf("links = %+v", links)
	}
}

// flipNodes shows the nodes as a sync read them (a route to the exit) the first
// time, and as they are a moment later (a save has dropped the route).
type flipNodes struct {
	first, later []*domain.Node
	calls        int
}

func (f *flipNodes) List(context.Context) ([]*domain.Node, error) {
	f.calls++
	if f.calls == 1 {
		return f.first, nil
	}
	return f.later, nil
}

// A sync that read the nodes just before a save dropped the only route to an
// exit, and the tunnel with it, must not make the tunnel again.
func TestLinksForDoesNotResurrectATunnelThatWasJustDropped(t *testing.T) {
	repos := linkStore(t)
	var locked []int64
	nodes := &flipNodes{
		first: []*domain.Node{routedNode(), landing(3, 3, "exit.example", 443)},
		later: []*domain.Node{landing(1, 9, "entry.example", 443), landing(3, 3, "exit.example", 443)},
	}
	service := New(nodes, newFakeRepo()).WithLinks(repos.TransitLinks, fakeAgents{3: "198.51.100.7"}).
		WithServerLock(func(panel int64) func() { locked = append(locked, panel); return func() {} })
	plan, err := service.LinksFor(context.Background(), 9, map[int]bool{}, true)
	if err != nil || len(plan.Out) != 0 || len(plan.RouteKeys) != 0 {
		t.Fatalf("plan = %+v, %v", plan, err)
	}
	if links, _ := repos.TransitLinks.ListLinks(context.Background()); len(links) != 0 {
		t.Fatalf("the dropped tunnel came back: %+v", links)
	}
	if len(locked) != 2 || locked[0] != 3 || locked[1] != 9 {
		t.Fatalf("locks = %v, want both servers, lower first", locked)
	}
	// Still used: it is made.
	nodes = &flipNodes{first: []*domain.Node{routedNode(), landing(3, 3, "exit.example", 443)}, later: []*domain.Node{routedNode(), landing(3, 3, "exit.example", 443)}}
	service = New(nodes, newFakeRepo()).WithLinks(repos.TransitLinks, fakeAgents{3: "198.51.100.7"}).WithServerLock(func(int64) func() { return func() {} })
	if plan, err = service.LinksFor(context.Background(), 9, map[int]bool{}, true); err != nil || len(plan.Out) != 1 {
		t.Fatalf("a tunnel that is still used: %+v, %v", plan, err)
	}
}

// The listener of a tunnel line is an ordinary listener whose default exit is
// the landing's server: the tunnel from its server to that one is made for it,
// the entry binds exactly that listener to it, and the landing's server holds
// the exit half. Nothing is said about the landing's own listener.
func TestLinksForBindsATunnelLinesListenerToTheLandingsServer(t *testing.T) {
	repos := linkStore(t)
	landingNode := landing(1, 3, "land.example", 443)
	child := landing(2, 9, "entry.example", 24443)
	child.ParentNodeID, child.ParentLineID, child.EgressPanelID = 1, "line", 3
	service := New(fakeNodes{landingNode, child}, newFakeRepo()).WithLinks(repos.TransitLinks, fakeAgents{3: "198.51.100.7"})
	ctx := context.Background()

	entry, err := service.LinksFor(ctx, 9, map[int]bool{24443: true}, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(entry.Out) != 1 || len(entry.Egress) != 1 || entry.Egress[0].Listener != nodeprotocol.NewListenerKey(2) {
		t.Fatalf("entry plan = %+v, want the listener of the line bound to the tunnel", entry)
	}
	if entry.EgressExit[2] != 3 {
		t.Fatalf("exits = %v", entry.EgressExit)
	}
	exit, err := service.LinksFor(ctx, 3, map[int]bool{443: true}, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(exit.In) != 1 || len(exit.Egress) != 0 {
		t.Fatalf("exit plan = %+v", exit)
	}
	// A disabled line's listener has no tunnel.
	child.Enabled = false
	if plan, err := service.LinksFor(ctx, 9, map[int]bool{24443: true}, false); err != nil || len(plan.Egress) != 0 {
		t.Fatalf("a disabled listener is bound: %+v (%v)", plan, err)
	}
}

// PlanLink's promise is kept while it is fresh and honoured by every other
// link's allocation, expires, and is not honoured by a link made after the port
// was taken: that is refused, never moved quietly.
func TestPlanLinkReservationsExpireAreHonouredAndNeverMovedQuietly(t *testing.T) {
	repos := linkStore(t)
	exitNode := landing(1, 3, "exit.example", 443)
	service := New(fakeNodes{exitNode, landing(2, 9, "entry.example", 8443)}, newFakeRepo()).WithLinks(repos.TransitLinks, fakeAgents{3: "198.51.100.7"})
	clock := time.Unix(1_700_000_000, 0)
	service.now = func() time.Time { return clock }
	ctx := context.Background()

	port, exists, err := service.PlanLink(ctx, 9, 3)
	if err != nil || exists {
		t.Fatalf("PlanLink = %d %v %v", port, exists, err)
	}
	if again, _, _ := service.PlanLink(ctx, 9, 3); again != port {
		t.Fatalf("the same pair was promised %d then %d", port, again)
	}
	// Another entry planning a link to the same exit never gets the promised port.
	for i := 0; i < 200; i++ {
		if other, _, _ := service.PlanLink(ctx, 10, 3); other == port {
			t.Fatal("a live reservation was given to another link")
		}
		service.takeReservation([2]int64{10, 3})
	}
	// Taken before the link is made (a shared port of the exit): refused.
	service.WithPortCheck(func(_ context.Context, panel int64, p int) bool { return panel == 3 && p == port })
	if _, err := service.EnsureLink(ctx, 9, 3); !errors.Is(err, ErrReservedPortTaken) {
		t.Fatalf("err = %v, want the promised port reported taken", err)
	}
	// An expired promise is forgotten, and a link made without one takes a free port.
	service.WithPortCheck(nil)
	first, _, _ := service.PlanLink(ctx, 9, 3)
	clock = clock.Add(11 * time.Minute)
	live := service.liveReservations(3)
	if live[first] || len(live) != 0 {
		t.Fatalf("an expired reservation is still live: %v", live)
	}
	link, err := service.EnsureLink(ctx, 9, 3)
	if err != nil || link.Port < 20000 {
		t.Fatalf("link = %+v err = %v", link, err)
	}
	// A planned port is the one the link is then made with.
	service.takeReservation([2]int64{9, 3})
	planned, _, _ := service.PlanLink(ctx, 11, 3)
	made, err := service.EnsureLink(ctx, 11, 3)
	if err != nil || made.Port != planned {
		t.Fatalf("made on %d (%v), planned %d", made.Port, err, planned)
	}
}
