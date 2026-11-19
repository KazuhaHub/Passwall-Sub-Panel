package node

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

// orderFakeRepo is a small in-memory node + separator store that applies an
// ApplySubscriptionOrder write the way the SQL repo does, so a test can write
// and then read back.
type orderFakeRepo struct {
	ports.NodeRepo
	nodes []*domain.Node
	seps  []*domain.SeparatorEntry
	err   error
	calls int
}

func (r *orderFakeRepo) List(context.Context) ([]*domain.Node, error) {
	out := make([]*domain.Node, len(r.nodes))
	for i, n := range r.nodes {
		c := *n
		c.Relays = append([]domain.RelayLine(nil), n.Relays...)
		out[i] = &c
	}
	return out, nil
}

type orderSepView struct {
	ports.SeparatorRepo
	r *orderFakeRepo
}

func (v orderSepView) List(context.Context) ([]*domain.SeparatorEntry, error) {
	return append([]*domain.SeparatorEntry(nil), v.r.seps...), nil
}

func (r *orderFakeRepo) ApplySubscriptionOrder(_ context.Context, w ports.SubscriptionOrderWrite) error {
	r.calls++
	if r.err != nil {
		return r.err
	}
	for _, u := range w.Nodes {
		for _, n := range r.nodes {
			if n.ID == u.NodeID {
				n.SortOrder = u.SortOrder
			}
		}
	}
	for _, u := range w.Separators {
		for _, s := range r.seps {
			if s.ID == u.SeparatorID {
				s.SortOrder = u.SortOrder
			}
		}
	}
	for _, n := range r.nodes {
		for i := range n.Relays {
			n.Relays[i].Position = nil
		}
	}
	for _, l := range w.Lines {
		for _, n := range r.nodes {
			if n.ID != l.NodeID {
				continue
			}
			for i := range n.Relays {
				if n.Relays[i].ID == l.LineID {
					p := l.SortOrder
					n.Relays[i].Position = &p
				}
			}
		}
	}
	return nil
}

func orderFixture() (*Service, *orderFakeRepo) {
	repo := &orderFakeRepo{
		nodes: []*domain.Node{
			{ID: 3, DisplayName: "HK", Region: "HK", SortOrder: 20, Enabled: true,
				Relays: []domain.RelayLine{{ID: "r-b", Name: "B", Address: "b.example", Enabled: true}}},
			{ID: 1, DisplayName: "JP", Region: "JP", SortOrder: 10, Enabled: true, HideDirect: true,
				Relays: []domain.RelayLine{{ID: "r-a", Name: "A", Address: "a.example", Enabled: true}, {ID: "r-c", Name: "C", Address: "c.example"}}},
			{ID: 9, DisplayName: "tunnel listener", ParentNodeID: 1, ParentLineID: "r-a", SortOrder: 5, Enabled: true},
			{ID: 2, DisplayName: "US", Region: "US", SortOrder: 20, Enabled: true},
		},
		seps: []*domain.SeparatorEntry{{ID: 3, DisplayName: "---- x ----", SortOrder: 20, Enabled: true, Mode: domain.SeparatorModeGlobal}},
	}
	return &Service{nodes: repo, separators: orderSepView{r: repo}}, repo
}

func refs(rows []OrderRow) []OrderRef {
	out := make([]OrderRef, len(rows))
	for i, r := range rows {
		out[i] = r.Ref
	}
	return out
}

func direct(nodeID int64) OrderRef {
	return OrderRef{Kind: OrderKindLine, NodeID: nodeID, LineID: domain.DirectLineID}
}
func lineRef(nodeID int64, id string) OrderRef {
	return OrderRef{Kind: OrderKindLine, NodeID: nodeID, LineID: id}
}
func sepRef(id int64) OrderRef { return OrderRef{Kind: OrderKindSeparator, ID: id} }

// The list is the unfiltered subscription order: sort_order first, a separator
// above a node it ties with, equal nodes by region then ID, every line right
// after its landing until it is positioned. Hidden tunnel listeners are not
// rows, and a hidden direct entry still is one.
func TestSubscriptionOrderListsTheGlobalOrder(t *testing.T) {
	svc, _ := orderFixture()
	got, err := svc.SubscriptionOrder(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	want := []OrderRef{
		direct(1), lineRef(1, "r-a"), lineRef(1, "r-c"), // sort_order 10
		sepRef(3),                    // ties at 20: the separator first
		direct(3), lineRef(3, "r-b"), // then HK before US on the region tie
		direct(2),
	}
	if !reflect.DeepEqual(refs(got.Rows), want) {
		t.Fatalf("got  %+v\nwant %+v", refs(got.Rows), want)
	}
	if got.Version == "" {
		t.Fatal("no version")
	}
	// Facts the admin list shows.
	byRef := map[OrderRef]OrderRow{}
	for _, r := range got.Rows {
		byRef[r.Ref] = r
	}
	if r := byRef[direct(1)]; r.Withheld != "hidden_direct" || r.Published {
		t.Errorf("hidden direct row: %+v", r)
	}
	if r := byRef[lineRef(1, "r-c")]; r.Withheld != "disabled" || r.Positioned || r.Position != 10 {
		t.Errorf("disabled unpositioned line: %+v", r)
	}
	if r := byRef[lineRef(3, "r-b")]; !r.Published || r.Position != 20 {
		t.Errorf("published line: %+v", r)
	}
}

// A positioned line floats exactly where the renderer would put it.
func TestSubscriptionOrderPlacesPositionedLines(t *testing.T) {
	svc, repo := orderFixture()
	p := 15
	repo.nodes[0].Relays[0].Position = &p // HK's line to 15: between JP's lines (10) and the 20s
	got, _ := svc.SubscriptionOrder(context.Background())
	want := []OrderRef{direct(1), lineRef(1, "r-a"), lineRef(1, "r-c"), lineRef(3, "r-b"), sepRef(3), direct(3), direct(2)}
	if !reflect.DeepEqual(refs(got.Rows), want) {
		t.Fatalf("got  %+v\nwant %+v", refs(got.Rows), want)
	}
	if r := got.Rows[3]; !r.Positioned || r.Position != 15 {
		t.Fatalf("row = %+v", r)
	}
}

func TestApplySubscriptionOrderWritesEveryRowInTenSteps(t *testing.T) {
	svc, _ := orderFixture()
	cur, _ := svc.SubscriptionOrder(context.Background())
	// Reverse everything.
	items := refs(cur.Rows)
	for i, j := 0, len(items)-1; i < j; i, j = i+1, j-1 {
		items[i], items[j] = items[j], items[i]
	}
	version, err := svc.ApplySubscriptionOrder(context.Background(), cur.Version, items)
	if err != nil {
		t.Fatal(err)
	}
	after, _ := svc.SubscriptionOrder(context.Background())
	if after.Version != version || version == cur.Version {
		t.Fatalf("version %q -> %q (returned %q)", cur.Version, after.Version, version)
	}
	if !reflect.DeepEqual(refs(after.Rows), items) {
		t.Fatalf("read-back order differs from the written one:\n got  %+v\nwant %+v", refs(after.Rows), items)
	}
	// Every row got an explicit position (i+1)*10, every relay line included.
	for i, r := range after.Rows {
		if r.Position != (i+1)*10 {
			t.Errorf("row %d (%+v) position %d, want %d", i, r.Ref, r.Position, (i+1)*10)
		}
		if r.Ref.Kind == OrderKindLine && r.Ref.LineID != domain.DirectLineID && !r.Positioned {
			t.Errorf("relay %+v left unpositioned", r.Ref)
		}
	}
}

func TestApplySubscriptionOrderRejectsUnknownDuplicateIncompleteStale(t *testing.T) {
	ctx := context.Background()
	svc, repo := orderFixture()
	cur, _ := svc.SubscriptionOrder(ctx)
	full := refs(cur.Rows)
	without := func(i int) []OrderRef { return append(append([]OrderRef(nil), full[:i]...), full[i+1:]...) }
	with := func(extra OrderRef) []OrderRef { return append(append([]OrderRef(nil), full...), extra) }

	cases := []struct {
		name    string
		version string
		items   []OrderRef
		wantErr error // ErrValidation or ErrConflict
		stale   bool
	}{
		{"unknown kind", cur.Version, []OrderRef{{Kind: "node", ID: 1}}, domain.ErrValidation, false},
		{"separator id zero", cur.Version, []OrderRef{sepRef(0)}, domain.ErrValidation, false},
		{"line without id", cur.Version, []OrderRef{{Kind: OrderKindLine, NodeID: 1}}, domain.ErrValidation, false},
		{"duplicate ref", cur.Version, append(append([]OrderRef(nil), full...), full[0]), domain.ErrValidation, false},
		{"missing base version", "", full, domain.ErrValidation, false},
		{"empty list", cur.Version, nil, domain.ErrValidation, false},
		{"stale version", "0000", full, domain.ErrConflict, true},
		{"row missing", cur.Version, without(2), domain.ErrConflict, true},
		{"unknown line", cur.Version, with(lineRef(1, "nope")), domain.ErrConflict, true},
		{"unknown node", cur.Version, with(direct(77)), domain.ErrConflict, true},
		{"tunnel listener is not a row", cur.Version, with(direct(9)), domain.ErrConflict, true},
		{"unknown separator", cur.Version, with(sepRef(99)), domain.ErrConflict, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			before := repo.calls
			_, err := svc.ApplySubscriptionOrder(ctx, tc.version, tc.items)
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("err = %v, want %v", err, tc.wantErr)
			}
			var stale *OrderStaleError
			if errors.As(err, &stale) != tc.stale {
				t.Fatalf("stale error = %v, want %v", errors.As(err, &stale), tc.stale)
			}
			if stale != nil && (stale.Current == nil || stale.Current.Version != cur.Version) {
				t.Fatalf("stale error must carry the current order: %+v", stale.Current)
			}
			if repo.calls != before {
				t.Fatal("a refused write reached the repo")
			}
		})
	}
}

func TestApplySubscriptionOrderRejectsOversizedLists(t *testing.T) {
	svc, _ := orderFixture()
	cur, _ := svc.SubscriptionOrder(context.Background())
	items := make([]OrderRef, MaxOrderItems+1)
	for i := range items {
		items[i] = sepRef(int64(i + 1))
	}
	if _, err := svc.ApplySubscriptionOrder(context.Background(), cur.Version, items); !errors.Is(err, domain.ErrValidation) {
		t.Fatalf("err = %v", err)
	}
}

// Wiring the invalidator wraps the repo, so a reorder through the service
// drops the group node cache and the render cache exactly once.
func TestApplySubscriptionOrderInvalidatesCaches(t *testing.T) {
	svc, repo := orderFixture()
	dropped := 0
	svc.SetSubscriptionInvalidator(func() { dropped++ })
	cur, _ := svc.SubscriptionOrder(context.Background())
	if _, err := svc.ApplySubscriptionOrder(context.Background(), cur.Version, refs(cur.Rows)); err != nil {
		t.Fatal(err)
	}
	if dropped != 1 {
		t.Fatalf("invalidations = %d, want 1", dropped)
	}
	repo.err = errors.New("boom")
	cur, _ = svc.SubscriptionOrder(context.Background())
	if _, err := svc.ApplySubscriptionOrder(context.Background(), cur.Version, refs(cur.Rows)); err == nil {
		t.Fatal("expected the repo error")
	}
	if dropped != 1 {
		t.Fatalf("a failed write invalidated: %d", dropped)
	}
}

// The version moves when anything the list shows moves, and only then.
func TestSubscriptionOrderVersionTracksTheOrder(t *testing.T) {
	ctx := context.Background()
	svc, repo := orderFixture()
	a, _ := svc.SubscriptionOrder(ctx)
	b, _ := svc.SubscriptionOrder(ctx)
	if a.Version != b.Version {
		t.Fatal("version is not stable for an unchanged state")
	}
	repo.nodes[3].DisplayName = "renamed" // not part of the order
	if c, _ := svc.SubscriptionOrder(ctx); c.Version != a.Version {
		t.Fatal("a rename changed the order version")
	}
	repo.nodes[0].Relays = append(repo.nodes[0].Relays, domain.RelayLine{ID: "new", Address: "n.example"})
	if c, _ := svc.SubscriptionOrder(ctx); c.Version == a.Version {
		t.Fatal("a new line did not change the order version")
	}
}
