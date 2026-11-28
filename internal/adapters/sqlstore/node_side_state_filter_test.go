package sqlstore

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"strconv"
	"testing"
	"time"

	"gorm.io/gorm"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

// sideStateFixture is a fleet with state on every side table a node read
// attaches: routes per node, a link per (entry, exit) pair and probe rows per
// server. Nodes 1..nodes sit on panels 1..panels round-robin; every node past
// the first panel tunnels to the next panel, so links and probes exist for
// pairs the node under test knows nothing about.
type sideStateFixture struct {
	db    *gorm.DB
	repos ports.Repos
	ids   []int64
}

func newSideStateFixture(t *testing.T, nodes, panels int) *sideStateFixture {
	t.Helper()
	db, err := openIsolatedTestDB(t)
	if err != nil {
		t.Fatal(err)
	}
	if err := EnsureSchema(db); err != nil {
		t.Fatal(err)
	}
	repos := NewRepos(db)
	ctx := t.Context()
	fx := &sideStateFixture{db: db, repos: repos}
	fresh := time.Now().Add(time.Hour)
	for i := 1; i <= nodes; i++ {
		panel := int64(i%panels + 1)
		n := &domain.Node{PanelID: panel, InboundID: i, DisplayName: fmt.Sprintf("n%d", i), DesiredProtocol: "vless",
			DesiredPort: 7000 + i, Region: "JP", Enabled: true, EgressPanelID: int64((i+1)%panels + 1)}
		if err := repos.Node.Create(ctx, n); err != nil {
			t.Fatal(err)
		}
		fx.ids = append(fx.ids, n.ID)
		if err := repos.TransitRoutes.Replace(ctx, n.ID, sampleRoutes()); err != nil {
			t.Fatal(err)
		}
		if i%2 == 0 {
			if _, err := repos.TransitRoutes.RecordSent(ctx, n.ID, "d", "", time.Now().UTC()); err != nil {
				t.Fatal(err)
			}
		}
	}
	// One link per ordered pair, ready for the listeners of every odd node.
	for entry := int64(1); entry <= int64(panels); entry++ {
		for exit := int64(1); exit <= int64(panels); exit++ {
			if entry == exit {
				continue
			}
			link := transitLinkRow{EntryPanelID: entry, ExitPanelID: exit, Protocol: "ss2022", Port: 1, SSMethod: "m", SSKey: "k",
				InSent: "a", InApplied: "a", OutSent: "b", OutApplied: "b"}
			for _, id := range fx.ids {
				if id%2 == 1 {
					link.OutSentBound = append(link.OutSentBound, "lst_"+strconv.FormatInt(id, 10))
				}
			}
			if err := db.Create(&link).Error; err != nil {
				t.Fatal(err)
			}
			state := domain.ProbeOK
			if (entry+exit)%2 == 0 {
				state = domain.ProbeDown
			}
			row := probeRow(entry, "lnk_"+strconv.FormatInt(link.ID, 10), domain.ProbeKindE2E, state, true, fresh)
			if _, _, err := (&transitProbeRepo{db: db}).Apply(ctx, row.PanelID, row.ObjectKey, row.Kind,
				func(*domain.ProbeState) (domain.ProbeState, bool, error) { return row, true, nil }); err != nil {
				t.Fatal(err)
			}
		}
	}
	return fx
}

// loadRaw reads node rows without any attach, so a test can hand attachNodeState
// exactly the nodes it names.
func (fx *sideStateFixture) loadRaw(t *testing.T, ids ...int64) []*domain.Node {
	t.Helper()
	var rows []nodeRow
	if err := fx.db.Where("id IN ?", ids).Order("id").Find(&rows).Error; err != nil {
		t.Fatal(err)
	}
	out := make([]*domain.Node, len(rows))
	for i := range rows {
		n, err := rows[i].toDomain()
		if err != nil {
			t.Fatal(err)
		}
		out[i] = n
	}
	return out
}

// stateOf is everything the three attach functions fill on a node.
type attachedState struct {
	Routes                    []domain.DestRoute
	Sent, Applied, Withheld   string
	EgressReady, EgressWithld bool
	EgressProbe               *domain.ProbeQuality
}

func stateOf(n *domain.Node) attachedState {
	return attachedState{n.DestRoutes, n.RoutesSent, n.RoutesApplied, n.RoutesWithheld, n.EgressReady, n.EgressWithheld, n.EgressProbe}
}

// Attaching state to a few nodes of a database that holds many more must fill
// them exactly as attaching to the whole fleet does.
func TestAttachNodeStateOfASubsetMatchesTheWholeFleet(t *testing.T) {
	fx := newSideStateFixture(t, 24, 4)
	ctx := t.Context()

	all := fx.loadRaw(t, fx.ids...)
	if err := attachNodeState(ctx, fx.db, all); err != nil {
		t.Fatal(err)
	}
	want := map[int64]attachedState{}
	sawReady, sawNotReady, sawProbe, sawRoutes := false, false, false, false
	for _, n := range all {
		want[n.ID] = stateOf(n)
		sawReady = sawReady || n.EgressReady
		sawNotReady = sawNotReady || !n.EgressReady
		sawProbe = sawProbe || n.EgressProbe != nil
		sawRoutes = sawRoutes || len(n.DestRoutes) > 0
	}
	if !sawReady || !sawNotReady || !sawProbe || !sawRoutes {
		t.Fatalf("the fixture must exercise ready, not-ready, probed and routed nodes: %v %v %v %v", sawReady, sawNotReady, sawProbe, sawRoutes)
	}

	subsets := [][]int64{{fx.ids[0]}, {fx.ids[1]}, {fx.ids[2], fx.ids[7], fx.ids[20]}, fx.ids[5:9]}
	for _, subset := range subsets {
		nodes := fx.loadRaw(t, subset...)
		if err := attachNodeState(ctx, fx.db, nodes); err != nil {
			t.Fatal(err)
		}
		for _, n := range nodes {
			if got := stateOf(n); !reflect.DeepEqual(got, want[n.ID]) {
				t.Errorf("node %d attached alone differs from the fleet attach:\n got %+v\nwant %+v", n.ID, got, want[n.ID])
			}
		}
	}

	// The single-node readers go through the same path.
	for _, id := range []int64{fx.ids[3], fx.ids[10]} {
		byID, err := fx.repos.Node.GetByID(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		byInbound, err := fx.repos.Node.GetByPanelInbound(ctx, byID.PanelID, byID.InboundID)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(stateOf(byID), want[id]) || !reflect.DeepEqual(stateOf(byInbound), want[id]) {
			t.Errorf("node %d: GetByID %+v / GetByPanelInbound %+v, want %+v", id, stateOf(byID), stateOf(byInbound), want[id])
		}
	}
}

// A node asked about more ids than one IN list carries is still attached whole.
func TestAttachNodeStateAcrossMoreThanOneChunk(t *testing.T) {
	fx := newSideStateFixture(t, 3, 3)
	ctx := t.Context()
	// Pad the list with ids that match nothing, past idReadChunk, ahead of the
	// real nodes, so the real ones land in a later chunk.
	nodes := fx.loadRaw(t, fx.ids...)
	var padding []*domain.Node
	for i := 0; i < idReadChunk+10; i++ {
		padding = append(padding, &domain.Node{ID: int64(1_000_000 + i), PanelID: 1})
	}
	reference := fx.loadRaw(t, fx.ids...)
	if err := attachNodeState(ctx, fx.db, reference); err != nil {
		t.Fatal(err)
	}
	if err := attachNodeState(ctx, fx.db, append(padding, nodes...)); err != nil {
		t.Fatal(err)
	}
	for i, n := range nodes {
		if !reflect.DeepEqual(stateOf(n), stateOf(reference[i])) {
			t.Errorf("node %d past the first chunk: %+v, want %+v", n.ID, stateOf(n), stateOf(reference[i]))
		}
	}
}

// Managed lines read their forward's probe by the entry server, which is not the
// node's own panel; the filtered read must still find it.
func TestAttachProbeStateKeepsAForwardOnAnotherServer(t *testing.T) {
	db, err := openTestDB(t)
	if err != nil {
		t.Fatal(err)
	}
	if err := ensureTestSchema(db); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	repo := &transitProbeRepo{db: db}
	fresh := time.Now().Add(time.Hour)
	for _, row := range []domain.ProbeState{
		probeRow(8, "fwd_3", domain.ProbeKindForward, domain.ProbeDown, true, fresh),
		probeRow(5, "fwd_3", domain.ProbeKindForward, domain.ProbeOK, false, fresh), // same key, another server
		probeRow(77, "fwd_3", domain.ProbeKindForward, domain.ProbeOK, false, fresh),
	} {
		row := row
		if _, _, err := repo.Apply(ctx, row.PanelID, row.ObjectKey, row.Kind,
			func(*domain.ProbeState) (domain.ProbeState, bool, error) { return row, true, nil }); err != nil {
			t.Fatal(err)
		}
	}
	node := &domain.Node{ID: 1, PanelID: 5, Relays: []domain.RelayLine{{ID: "a", EntryPanelID: 8, Enabled: true}}}
	if err := attachProbeState(ctx, db, []*domain.Node{node}, map[probeLineRef]int64{{1, "a"}: 3}); err != nil {
		t.Fatal(err)
	}
	if line := node.Relays[0]; line.Probe == nil || line.Probe.State != domain.ProbeDown {
		t.Fatalf("a forward on the entry server lost its probe: %+v", line.Probe)
	}
}

// rowCounter counts the rows each query hands back, per table, and the
// statements run, so a read's cost is a number and not a duration.
type rowCounter struct {
	rows       map[string]int64
	statements int
}

func countQueries(t *testing.T, db *gorm.DB) *rowCounter {
	t.Helper()
	c := &rowCounter{rows: map[string]int64{}}
	name := "test:count_rows_" + t.Name()
	if err := db.Callback().Query().After("gorm:query").Register(name, func(tx *gorm.DB) {
		c.statements++
		c.rows[tx.Statement.Table] += tx.RowsAffected
	}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Callback().Query().Remove(name) })
	return c
}

func (c *rowCounter) String() string {
	keys := make([]string, 0, len(c.rows))
	for k := range c.rows {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	s := fmt.Sprintf("%d statements", c.statements)
	for _, k := range keys {
		s += fmt.Sprintf(", %s=%d", k, c.rows[k])
	}
	return s
}

// Reading one node must read that node's side rows, not the fleet's.
func TestGetByIDReadsOnlyItsOwnSideRows(t *testing.T) {
	const nodes, panels = 60, 6
	fx := newSideStateFixture(t, nodes, panels)
	ctx := t.Context()
	counter := countQueries(t, fx.db)

	node, err := fx.repos.Node.GetByID(ctx, fx.ids[10])
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("GetByID with %d nodes / %d panels / %d links: %s", nodes, panels, panels*(panels-1), counter)
	if len(node.DestRoutes) == 0 {
		t.Fatal("the node lost its routes")
	}
	if got := counter.rows["transit_routes"]; got != 1 {
		t.Errorf("transit_routes rows read = %d, want 1 (the node's own)", got)
	}
	// One entry panel, one exit panel: exactly the one link the node uses.
	if got := counter.rows["transit_links"]; got > 2 {
		t.Errorf("transit_links rows read = %d, want the node's own pair only", got)
	}
	// Probe rows of the node's own server, not of every server.
	if got, all := counter.rows["transit_probe_state"], int64(panels*(panels-1)); got >= all {
		t.Errorf("transit_probe_state rows read = %d of %d, want only the node's server's", got, all)
	}
}

// The poll's lookup reads the node row and nothing else, and the node it gets
// carries the same counter columns as the full read.
func TestGetByPanelInboundBareReadsOnlyTheNodeRow(t *testing.T) {
	fx := newSideStateFixture(t, 12, 4)
	ctx := t.Context()
	full, err := fx.repos.Node.GetByID(ctx, fx.ids[5])
	if err != nil {
		t.Fatal(err)
	}
	reader, ok := fx.repos.Node.(ports.NodeBareReader)
	if !ok {
		t.Fatal("the SQL node repo must offer the light read")
	}
	counter := countQueries(t, fx.db)
	bare, err := reader.GetByPanelInboundBare(ctx, full.PanelID, full.InboundID)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("GetByPanelInboundBare: %s", counter)
	if counter.statements != 1 || counter.rows["nodes"] != 1 || len(counter.rows) != 1 {
		t.Errorf("light read ran %s, want one statement on nodes", counter)
	}
	if bare.ID != full.ID || bare.LifetimeTotalBytes != full.LifetimeTotalBytes || bare.LastInboundUpBytes != full.LastInboundUpBytes ||
		bare.LastInboundSeeded != full.LastInboundSeeded || bare.LastInboundCounterEpoch != full.LastInboundCounterEpoch {
		t.Errorf("counter columns differ: bare %+v, full %+v", bare, full)
	}
	if _, err := reader.GetByPanelInboundBare(ctx, 9999, 1); err == nil || !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("an unknown inbound = %v, want ErrNotFound", err)
	}
}

// ListByPanelBare is the nodes of one server, in List's order, read with one
// statement on the nodes table: no other server's rows and none of the side
// state List attaches to every node.
func TestListByPanelBareReadsOnlyThePanelsNodeRows(t *testing.T) {
	fx := newSideStateFixture(t, 12, 4)
	ctx := t.Context()
	all, err := fx.repos.Node.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	lister, ok := fx.repos.Node.(ports.NodePanelBareLister)
	if !ok {
		t.Fatal("the SQL node repo must offer the light panel read")
	}
	const panelID = int64(2)
	counter := countQueries(t, fx.db)
	bare, err := lister.ListByPanelBare(ctx, panelID)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("ListByPanelBare: %s", counter)
	var want []*domain.Node
	for _, node := range all {
		if node.PanelID == panelID {
			want = append(want, node)
		}
	}
	if len(want) != 3 {
		t.Fatalf("fixture has %d nodes on panel %d, want 3", len(want), panelID)
	}
	if counter.statements != 1 || counter.rows["nodes"] != int64(len(want)) || len(counter.rows) != 1 {
		t.Errorf("light read ran %s, want one statement returning the panel's %d nodes", counter, len(want))
	}
	if len(bare) != len(want) {
		t.Fatalf("nodes = %d, want %d", len(bare), len(want))
	}
	for i := range want {
		if bare[i].ID != want[i].ID || bare[i].PanelID != panelID || bare[i].StreamSettings != want[i].StreamSettings ||
			bare[i].InboundSettings != want[i].InboundSettings || bare[i].DesiredPort != want[i].DesiredPort ||
			bare[i].ConfigSyncState != want[i].ConfigSyncState {
			t.Errorf("node %d: bare %+v, full %+v", i, bare[i], want[i])
		}
	}
	if none, err := lister.ListByPanelBare(ctx, 9999); err != nil || len(none) != 0 {
		t.Errorf("an unknown panel = (%v, %v), want no nodes", none, err)
	}
}
