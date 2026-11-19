package render

import (
	"reflect"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

func intp(v int) *int { return &v }

// line returns the relay with the given ID of the fleet's node.
func line(r *lineOrderRender, nodeID int64, id string) *domain.RelayLine {
	for _, n := range r.nodes {
		if n.ID != nodeID {
			continue
		}
		for i := range n.Relays {
			if n.Relays[i].ID == id {
				return &n.Relays[i]
			}
		}
	}
	panic("no such line " + id)
}

func eachFormat(t *testing.T, fn func(t *testing.T, ct domain.ClientType)) {
	t.Helper()
	for _, ct := range allClientTypes {
		t.Run(string(ct), func(t *testing.T) { fn(t, ct) })
	}
}

// The baseline order of the awkward fleet, which the golden pins byte for byte.
var awkwardBaseline = []string{
	"---- early ----", "A", "A a1", "A a2", "C c1", "---- positional ----", "---- tied with D ----",
	"D", "D d1", "D d2", "E", "F", "F f1",
}

func TestRelayLinePositionOrdersEveryFormat(t *testing.T) {
	eachFormat(t, func(t *testing.T, ct domain.ClientType) {
		r := awkwardFleet(t)
		if got := renderedOrder(t, r, ct); !reflect.DeepEqual(got, awkwardBaseline) {
			t.Fatalf("baseline moved: %q", got)
		}
		// D's second line dragged up between A's lines and C's: it sits at 15 on
		// the shared scale, so it goes after the last entry at or below 15 (A a2).
		line(r, 9, "d2").Position = intp(15)
		want := []string{
			"---- early ----", "A", "A a1", "A a2", "D d2", "C c1", "---- positional ----", "---- tied with D ----",
			"D", "D d1", "E", "F", "F f1",
		}
		if got := renderedOrder(t, r, ct); !reflect.DeepEqual(got, want) {
			t.Fatalf("got  %q\nwant %q", got, want)
		}
	})
}

func TestPlaceLinesFloatingAboveOwnLanding(t *testing.T) {
	eachFormat(t, func(t *testing.T, ct domain.ClientType) {
		r := awkwardFleet(t)
		line(r, 9, "d1").Position = intp(3) // below every key: the very first entry
		got := renderedOrder(t, r, ct)
		if got[0] != "D d1" || len(got) != len(awkwardBaseline) {
			t.Fatalf("got %q", got)
		}
	})
}

// A line without a stored position still sits under its landing even while a
// sibling of the same node was dragged away.
func TestPlaceLinesUnsetAttachesAfterLanding(t *testing.T) {
	eachFormat(t, func(t *testing.T, ct domain.ClientType) {
		r := awkwardFleet(t)
		line(r, 9, "d2").Position = intp(15)
		got := renderedOrder(t, r, ct)
		for i, name := range got {
			if name == "D d1" && got[i-2] != "---- tied with D ----" && got[i-1] != "D" {
				t.Fatalf("D d1 detached from D: %q", got)
			}
		}
	})
}

// A hidden direct entry has no row of its own in a subscription; its remaining
// lines keep their place, positioned or not.
func TestPlaceLinesHiddenDirectLandingKeepsItsLines(t *testing.T) {
	eachFormat(t, func(t *testing.T, ct domain.ClientType) {
		r := awkwardFleet(t) // node 8 (C) hides its direct entry
		line(r, 8, "c1").Position = intp(20)
		if got := renderedOrder(t, r, ct); !reflect.DeepEqual(got, awkwardBaseline) {
			t.Fatalf("got %q", got)
		}
		line(r, 8, "c1").Position = intp(45) // dragged to the bottom
		got := renderedOrder(t, r, ct)
		if got[len(got)-1] != "C c1" {
			t.Fatalf("got %q", got)
		}
	})
}

// Positional group separators are placed by index in the pre-expansion list and
// have no key: a line lands relative to the keyed entries around them.
func TestPlaceLinesRelativeToPositionalSeparator(t *testing.T) {
	eachFormat(t, func(t *testing.T, ct domain.ClientType) {
		r := awkwardFleet(t)
		line(r, 7, "a2").Position = intp(22) // after C c1 (20), still above the separator
		got := renderedOrder(t, r, ct)
		want := []string{
			"---- early ----", "A", "A a1", "C c1", "A a2", "---- positional ----", "---- tied with D ----",
			"D", "D d1", "D d2", "E", "F", "F f1",
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("got  %q\nwant %q", got, want)
		}
	})
}

// A line whose landing is not published takes its position with it.
func TestFilteredLineKeepsOthersInOrder(t *testing.T) {
	eachFormat(t, func(t *testing.T, ct domain.ClientType) {
		r := awkwardFleet(t)
		for _, n := range r.nodes {
			for i := range n.Relays {
				n.Relays[i].Tags = []string{"keep"}
			}
		}
		line(r, 9, "d2").Tags = []string{"drop"}
		line(r, 9, "d2").Position = intp(15)
		r.group.group.TagFilter = domain.TagFilter{Tags: []string{"keep"}}
		got := renderedOrder(t, r, ct)
		want := []string{
			"---- early ----", "A", "A a1", "A a2", "C c1", "---- positional ----", "---- tied with D ----",
			"D", "D d1", "E", "F", "F f1",
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("got  %q\nwant %q", got, want)
		}
	})
}

// An explicit position never brings back a line that is not publishable.
func TestUnpublishableFloatingLineOmitted(t *testing.T) {
	eachFormat(t, func(t *testing.T, ct domain.ClientType) {
		r := awkwardFleet(t)
		line(r, 9, "d2").Enabled = false
		line(r, 9, "d2").Position = intp(15)
		want := []string{
			"---- early ----", "A", "A a1", "A a2", "C c1", "---- positional ----", "---- tied with D ----",
			"D", "D d1", "E", "F", "F f1",
		}
		if got := renderedOrder(t, r, ct); !reflect.DeepEqual(got, want) {
			t.Fatalf("got  %q\nwant %q", got, want)
		}
	})
}

// Proxy-group member lists come from the same items, so "all nodes" follows the
// new order. Subscribers see this change; the test pins it.
func TestProxyGroupAllFollowsLineOrder(t *testing.T) {
	r := awkwardFleet(t)
	line(r, 9, "d2").Position = intp(15)
	out, err := r.RenderForUser(t.Context(), r.user, domain.ClientMihomo)
	if err != nil {
		t.Fatal(err)
	}
	var config struct {
		Groups []struct {
			Name    string   `yaml:"name"`
			Proxies []string `yaml:"proxies"`
		} `yaml:"proxy-groups"`
	}
	if err := yaml.Unmarshal(out.Body, &config); err != nil {
		t.Fatal(err)
	}
	var members []string
	for _, g := range config.Groups {
		if g.Name == "国外媒体" {
			members = g.Proxies
		}
	}
	// Separator entries are members too (the group lists every emitted entry).
	want := []string{
		"---- early ----", "A", "A a1", "A a2", "D d2", "C c1", "---- positional ----", "---- tied with D ----",
		"D", "D d1", "E", "F", "F f1",
	}
	if !reflect.DeepEqual(members, want) {
		t.Fatalf("got  %q\nwant %q", members, want)
	}
}

// A dragged line is flagged by its own region, wherever it ended up.
func TestRegionFlagUnaffectedByPosition(t *testing.T) {
	r := awkwardFleet(t)
	r.Service.repos.Settings = fakeSettings{s: ports.UISettings{SubRegionFlagPrefix: true}}
	line(r, 9, "d2").Position = intp(15) // D's lines are region HK, the entries around it are US/JP
	got := renderedOrder(t, r, domain.ClientMihomo)
	found := false
	for _, name := range got {
		if name == "\U0001F1ED\U0001F1F0 D d2" {
			found = true
		}
	}
	if !found {
		t.Fatalf("moved line lost its own flag: %q", got)
	}
}

// Line display names are allocated by node and line ID, never by render order,
// so a drag cannot rename a line.
func TestLineNamesStableAcrossReorder(t *testing.T) {
	eachFormat(t, func(t *testing.T, ct domain.ClientType) {
		before := append([]string(nil), renderedOrder(t, awkwardFleet(t), ct)...)
		r := awkwardFleet(t)
		line(r, 9, "d2").Position = intp(15)
		line(r, 7, "a1").Position = intp(45)
		after := renderedOrder(t, r, ct)
		if !sameSet(before, after) {
			t.Fatalf("names changed by a drag:\nbefore %q\nafter  %q", before, after)
		}
	})
}

func sameSet(a, b []string) bool {
	count := map[string]int{}
	for _, s := range a {
		count[s]++
	}
	for _, s := range b {
		count[s]--
	}
	for _, n := range count {
		if n != 0 {
			return false
		}
	}
	return true
}
