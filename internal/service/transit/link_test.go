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
	entry, err := service.LinksFor(t.Context(), 9, map[int]bool{443: true})
	if err != nil || len(entry.Out) != 1 {
		t.Fatalf("entry plan = %+v, %v", entry, err)
	}
	exit, err := service.LinksFor(t.Context(), 3, map[int]bool{443: true})
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
