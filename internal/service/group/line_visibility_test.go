package group

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
)

func filterGroup(id int64, mode string, conds ...string) *domain.Group {
	return &domain.Group{ID: id, Name: "g", TagFilter: domain.TagFilter{Mode: mode, Tags: conds}}
}

func lineNode() *domain.Node {
	return &domain.Node{
		ID: 1, PanelID: 1, Enabled: true, ServerAddress: "land.example", Region: "HK", Tags: []string{"premium", "server:a"},
		Relays: []domain.RelayLine{
			{ID: "gz", Address: "gz.example", Enabled: true, Region: "CN", Tags: []string{"premium", "cmcc"}},
			{ID: "sh", Address: "sh.example", Enabled: true, Region: "HK", Tags: []string{"premium", "server:a"}},
		},
		RelayHealth: []domain.RelayHealth{{Index: 0, Address: "gz.example", State: "up"}, {Index: 1, Address: "sh.example", State: "down"}},
		RouteState: domain.NodeRouteState{Status: "applied", Lines: []domain.VlessRouteLine{
			{ID: "via-gz", EntryRelayID: "gz", OutboundTag: "direct", Enabled: true, Region: "HK", Tags: []string{"premium"}},
			{ID: "us", OutboundTag: "direct", Enabled: true, Region: "US", Tags: []string{"premium", "stream"}},
			{ID: "old", OutboundTag: "direct", Deleted: true, Region: "ZZ"},
		}},
	}
}

func lineIDs[T any](lines []T, id func(T) string) []string {
	out := []string{}
	for _, line := range lines {
		out = append(out, id(line))
	}
	return out
}

func relayIDs(n *domain.Node) []string {
	return lineIDs(n.Relays, func(l domain.RelayLine) string { return l.ID })
}

func routeIDs(n *domain.Node) []string {
	return lineIDs(n.RouteState.Lines, func(l domain.VlessRouteLine) string { return l.ID })
}

// The tag_filter is evaluated on the line's own labels with the node's exact
// semantics: region case-insensitive, tags and custom keys exact, all/any.
func TestLineMatchesUsesNodeFilterSemanticsOnLineLabels(t *testing.T) {
	n := lineNode()
	cases := []struct {
		name   string
		filter domain.TagFilter
		region string
		tags   []string
		want   bool
	}{
		{"all filter", domain.TagFilter{All: true}, "", nil, true},
		{"empty all-mode is vacuous", domain.TagFilter{}, "", nil, true},
		{"empty any-mode matches nothing", domain.TagFilter{Mode: "any"}, "HK", []string{"x"}, false},
		{"region is case-insensitive", domain.TagFilter{Tags: []string{"region:hk"}}, "HK", nil, true},
		{"region reads the line", domain.TagFilter{Tags: []string{"region:HK"}}, "CN", nil, false},
		{"bare tag", domain.TagFilter{Tags: []string{"cmcc"}}, "", []string{"cmcc"}, true},
		{"tag: prefix with spaces", domain.TagFilter{Tags: []string{"tag: cmcc"}}, "", []string{"cmcc"}, true},
		{"tags are exact", domain.TagFilter{Tags: []string{"tag:CMCC"}}, "", []string{"cmcc"}, false},
		{"custom key is a literal tag", domain.TagFilter{Tags: []string{"server:a"}}, "", []string{"server:a"}, true},
		{"all mode needs every condition", domain.TagFilter{Tags: []string{"region:CN", "tag:stream"}}, "CN", []string{"cmcc"}, false},
		{"any mode needs one", domain.TagFilter{Mode: "any", Tags: []string{"region:US", "tag:cmcc"}}, "CN", []string{"cmcc"}, true},
		{"node: keeps the node address", domain.TagFilter{Tags: []string{"node:land.example"}}, "CN", nil, true},
	}
	for _, c := range cases {
		if got := lineMatches(n, c.region, c.tags, c.filter); got != c.want {
			t.Errorf("%s: got %v want %v", c.name, got, c.want)
		}
	}
}

func TestVisibleLinesNarrowsByLineLabelsAndEntry(t *testing.T) {
	n := lineNode()
	// region:HK keeps the HK relay, drops the CN relay, and with it the route
	// line entering through that relay even though the route itself is HK.
	got := VisibleLines(filterGroup(1, "", "region:HK"), []*domain.Node{n})[0]
	if want := []string{"sh"}; !reflect.DeepEqual(relayIDs(got), want) {
		t.Fatalf("relays %v want %v", relayIDs(got), want)
	}
	if want := []string{"old"}; !reflect.DeepEqual(routeIDs(got), want) {
		t.Fatalf("routes %v want %v (deleted lines are kept, hidden entry hides its route)", routeIDs(got), want)
	}
	if want := []domain.RelayHealth{{Index: 0, Address: "sh.example", State: "down"}}; !reflect.DeepEqual(got.RelayHealth, want) {
		t.Fatalf("relay health not re-indexed: %+v", got.RelayHealth)
	}
	// The shared node is untouched.
	if len(n.Relays) != 2 || len(n.RouteState.Lines) != 3 || len(n.RelayHealth) != 2 || n.RelayHealth[1].Index != 1 {
		t.Fatal("VisibleLines modified the cached node")
	}
}

func TestVisibleLinesEntryVisibleKeepsRoute(t *testing.T) {
	n := lineNode()
	got := VisibleLines(filterGroup(1, "", "tag:premium"), []*domain.Node{n})[0]
	if got != n {
		t.Fatal("a node with every line visible must be passed through, not copied")
	}
	got = VisibleLines(filterGroup(1, "any", "region:CN", "region:HK"), []*domain.Node{n})[0]
	if want := []string{"via-gz", "old"}; !reflect.DeepEqual(routeIDs(got), want) {
		t.Fatalf("routes %v want %v", routeIDs(got), want)
	}
}

// Invariant 1: a line can only narrow. A node the group does not pass never
// reaches VisibleLines (NodesFor drops it); SubscriptionNodesFor composes both.
func TestSubscriptionNodesForOnlyNarrows(t *testing.T) {
	n := lineNode()
	other := &domain.Node{ID: 2, Enabled: true, Region: "JP", Relays: []domain.RelayLine{{ID: "x", Region: "HK", Tags: []string{"premium"}}}}
	svc := New(&eligGroups{}, &eligNodes{nodes: []*domain.Node{n, other}}, nil)
	nodes, err := svc.SubscriptionNodesFor(context.Background(), filterGroup(1, "", "region:HK"))
	if err != nil {
		t.Fatal(err)
	}
	if len(nodes) != 1 || nodes[0].ID != 1 {
		t.Fatalf("a line matching the group admitted its node: %+v", nodes)
	}
	if want := []string{"sh"}; !reflect.DeepEqual(relayIDs(nodes[0]), want) {
		t.Fatalf("relays %v want %v", relayIDs(nodes[0]), want)
	}
}

func TestPreviewLineVisibilityReportsPerLineGroups(t *testing.T) {
	n := lineNode()
	n.RouteState.Lines = n.RouteState.Lines[:2] // a draft has no deleted lines
	hk := filterGroup(1, "", "region:HK")
	stream := filterGroup(2, "any", "tag:stream", "node:land.example")
	jp := filterGroup(3, "", "region:JP")
	svc := New(&eligGroups{groups: []*domain.Group{hk, stream, jp}}, &eligNodes{}, nil)
	got, err := svc.PreviewLineVisibility(context.Background(), n)
	if err != nil {
		t.Fatal(err)
	}
	if want := []int64{1, 2}; !reflect.DeepEqual(got.NodeGroups, want) {
		t.Fatalf("node groups %v want %v", got.NodeGroups, want)
	}
	if want := [][]int64{{2}, {1, 2}}; !reflect.DeepEqual(got.Relays, want) {
		t.Fatalf("relays %v want %v", got.Relays, want)
	}
	if want := [][]int64{{2}, {2}}; !reflect.DeepEqual(got.Routes, want) {
		t.Fatalf("routes %v want %v", got.Routes, want)
	}
	if len(got.Groups) != 3 {
		t.Fatal("every group must be listed for the editor to name")
	}
}

func TestPreviewLineVisibilityEmptyIsNotUnknown(t *testing.T) {
	n := lineNode()
	n.RouteState.Lines = nil
	svc := New(&eligGroups{groups: []*domain.Group{filterGroup(1, "", "region:SG")}}, &eligNodes{}, nil)
	got, err := svc.PreviewLineVisibility(context.Background(), n)
	if err != nil || got.NodeGroups == nil || len(got.NodeGroups) != 0 || got.Relays[0] == nil || len(got.Relays[0]) != 0 {
		t.Fatalf("no visible group must be an empty, non-nil answer: %+v %v", got, err)
	}
	boom := errors.New("db down")
	svc = New(&eligGroups{err: boom}, &eligNodes{}, nil)
	if _, err := svc.PreviewLineVisibility(context.Background(), n); !errors.Is(err, boom) {
		t.Fatalf("a failed read must be an error, never an answer: %v", err)
	}
}

func TestPreviewLineVisibilityPropagatesEligibilityErrors(t *testing.T) {
	e := newEligEnv(t)
	e.groups.groups = []*domain.Group{allowlistGroup(domain.DestStateOn)}
	e.panels.err = errors.New("db down")
	if _, err := e.svc.PreviewLineVisibility(context.Background(), e.node); err == nil {
		t.Fatal("an allowlist eligibility read failure must surface as an error")
	}
}
