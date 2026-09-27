package domain

import (
	"fmt"
	"reflect"
	"sort"
	"strings"
	"testing"
	"unicode/utf8"
)

// The live-connection snapshot is what the risk center's 实时连接 tab shows:
// every live source of every account, on the panel and node that reported
// it. It is a display of what the detector judged, so it is built from the
// same fresh sightings and the same exclusion decisions — never a second
// reading of the panels.

func timedPanel(id int64, s map[string][]LiveIPSighting) PanelLiveIPs {
	return PanelLiveIPs{PanelID: id, Sightings: s, FreshSightings: s}
}

// One connection per (account, panel, node, source): a /64's rotating
// privacy addresses on one node are one connection shown at its smallest
// member, with the node's newest sighting of any of them; the same /64 on a
// second node, or the same address on a second panel, is a second one. A
// client PSP does not own and a panel that was not read contribute nothing.
func TestCollectLiveConnections_KeepsPanelNodeAndSource(t *testing.T) {
	panels := []PanelLiveIPs{
		timedPanel(1, map[string][]LiveIPSighting{
			"u7@a": {
				{IP: "2001:db8:1:2::9", Node: "n1", SeenAt: 1000},
				{IP: "2001:db8:1:2::5", Node: "n1", SeenAt: 990},
				{IP: "2001:db8:1:2::7", Node: "n2", SeenAt: 500},
				{IP: "::ffff:203.0.113.7", Node: "n1", SeenAt: 995},
			},
			"hand-made@a": {{IP: "9.9.9.9", Node: "n1", SeenAt: 1000}},
		}),
		timedPanel(2, map[string][]LiveIPSighting{"u7@b": {{IP: "203.0.113.7", Node: "n1", SeenAt: 42}}}),
		{PanelID: 3, Err: fmt.Errorf("timeout"), // a partial answer that came with the error
			ByEmail: map[string][]string{"u7@c": {"8.8.8.8"}}, Fresh: map[string][]string{"u7@c": {"8.8.8.8"}}},
	}
	own := owners(1, "u7@a", 7, 2, "u7@b", 7, 3, "u7@c", 7)

	got, truncated := CollectLiveConnections(panels, own, map[int64]UserAddresses{7: {UserID: 7}}, LiveConnMaxPerUser)

	want := []LiveConnection{
		{UserID: 7, PanelID: 1, Node: "n1", SourceKey: "2001:db8:1:2::/64", IP: "2001:db8:1:2::5", SeenAt: 1000},
		{UserID: 7, PanelID: 1, Node: "n1", SourceKey: "203.0.113.7", IP: "203.0.113.7", SeenAt: 995},
		{UserID: 7, PanelID: 1, Node: "n2", SourceKey: "2001:db8:1:2::/64", IP: "2001:db8:1:2::7", SeenAt: 500},
		{UserID: 7, PanelID: 2, Node: "n1", SourceKey: "203.0.113.7", IP: "203.0.113.7", SeenAt: 42},
	}
	if !reflect.DeepEqual(got, want) || truncated != 0 {
		t.Fatalf("connections = %+v (truncated %d),\nwant %+v (truncated 0)", got, truncated, want)
	}
}

// Each connection carries the rule that set its source aside, from the same
// ClassifyAddresses decision the verdict was judged on, keyed by the source.
// A kept source reads "".
func TestCollectLiveConnections_CarriesTheExclusionReason(t *testing.T) {
	panels := []PanelLiveIPs{{PanelID: 1, Fresh: map[string][]string{
		"u7@a": {"10.0.0.1", "192.0.2.50", "8.8.8.8", "2001:db8::5"},
	}}}
	addrs := map[int64]UserAddresses{7: {UserID: 7, ExcludedBy: map[string]string{
		"10.0.0.1":      AddressExcludedInternal,
		"192.0.2.50":    AddressExcludedShared,
		"2001:db8::/64": AddressExcludedListed,
	}}}

	got, _ := CollectLiveConnections(panels, owners(1, "u7@a", 7), addrs, LiveConnMaxPerUser)

	reasons := map[string]string{}
	for _, c := range got {
		reasons[c.SourceKey] = c.Exclusion
	}
	want := map[string]string{
		"10.0.0.1":      AddressExcludedInternal,
		"192.0.2.50":    AddressExcludedShared,
		"2001:db8::/64": AddressExcludedListed,
		"8.8.8.8":       "",
	}
	if !reflect.DeepEqual(reasons, want) {
		t.Fatalf("exclusions = %v, want %v", reasons, want)
	}
}

// The per-account cap bounds memory for an account behind a pathological
// NAT pool, and what it cuts must be the least informative part: the
// sources the detector judged come first, the excluded ones fill what is
// left. Ten excluded sources that sort BEFORE sixty kept ones are the case
// that tells "judged first" from "first by key". Other accounts are not
// touched, and every cut connection is counted.
func TestCollectLiveConnections_CapsPerUserJudgedFirst(t *testing.T) {
	var ips []string
	excluded := map[string]string{}
	for i := 1; i <= 10; i++ {
		ip := fmt.Sprintf("1.0.0.%d", i)
		ips = append(ips, ip)
		excluded[ip] = AddressExcludedShared
	}
	for i := 1; i <= 60; i++ {
		ips = append(ips, fmt.Sprintf("9.0.0.%d", i))
	}
	panels := []PanelLiveIPs{{PanelID: 1, Fresh: map[string][]string{
		"u7@a": ips,
		"u8@a": {"5.5.5.1", "5.5.5.2", "5.5.5.3"},
	}}}
	addrs := map[int64]UserAddresses{7: {UserID: 7, ExcludedBy: excluded}, 8: {UserID: 8}}

	got, truncated := CollectLiveConnections(panels, owners(1, "u7@a", 7, 1, "u8@a", 8), addrs, LiveConnMaxPerUser)

	var kept, ex, other int
	for _, c := range got {
		switch {
		case c.UserID == 8:
			other++
		case c.Exclusion == "":
			kept++
		default:
			ex++
		}
	}
	if kept != 60 || ex != 4 || other != 3 || truncated != 6 {
		t.Fatalf("kept %d, excluded %d, other account %d, truncated %d; want 60, 4, 3, 6", kept, ex, other, truncated)
	}
}

// A plain reader (a PSP-native node) has no node layer and no timestamps:
// its connections carry neither, rather than an invented one. A panel whose
// freshness was never computed falls back to its whole window, as the
// aggregator does.
func TestCollectLiveConnections_PlainReaderHasNoNode(t *testing.T) {
	panels := []PanelLiveIPs{
		{PanelID: 1, ByEmail: map[string][]string{"u7@a": {"1.1.1.1", "2.2.2.2"}}, Fresh: map[string][]string{"u7@a": {"1.1.1.1"}}},
		{PanelID: 2, ByEmail: map[string][]string{"u7@b": {"3.3.3.3"}}},
	}

	got, _ := CollectLiveConnections(panels, owners(1, "u7@a", 7, 2, "u7@b", 7), map[int64]UserAddresses{}, LiveConnMaxPerUser)

	want := []LiveConnection{
		{UserID: 7, PanelID: 1, SourceKey: "1.1.1.1", IP: "1.1.1.1"},
		{UserID: 7, PanelID: 2, SourceKey: "3.3.3.3", IP: "3.3.3.3"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("connections = %+v, want %+v — no node, no timestamp, only the live addresses", got, want)
	}
}

// The snapshot is paged by account and read by an admin comparing two
// refreshes, so its order must not depend on Go's randomised map order.
func TestCollectLiveConnections_IsSorted(t *testing.T) {
	build := func() []PanelLiveIPs {
		return []PanelLiveIPs{
			timedPanel(2, map[string][]LiveIPSighting{
				"u9@b": {{IP: "4.4.4.4", Node: "z", SeenAt: 1}, {IP: "4.4.4.5", Node: "a", SeenAt: 1}},
				"u7@b": {{IP: "3.3.3.3", Node: "m", SeenAt: 1}},
			}),
			timedPanel(1, map[string][]LiveIPSighting{
				"u9@a": {{IP: "6.6.6.6", Node: "b", SeenAt: 1}, {IP: "5.5.5.5", Node: "b", SeenAt: 1}},
				"u7@a": {{IP: "2.2.2.2", Node: "a", SeenAt: 1}, {IP: "1.1.1.1", Node: "c", SeenAt: 1}},
			}),
		}
	}
	own := owners(1, "u7@a", 7, 2, "u7@b", 7, 1, "u9@a", 9, 2, "u9@b", 9)
	first, _ := CollectLiveConnections(build(), own, nil, LiveConnMaxPerUser)
	less := func(a, b LiveConnection) bool {
		if a.UserID != b.UserID {
			return a.UserID < b.UserID
		}
		if a.PanelID != b.PanelID {
			return a.PanelID < b.PanelID
		}
		if a.Node != b.Node {
			return a.Node < b.Node
		}
		return a.SourceKey < b.SourceKey
	}
	if len(first) != 7 || !sort.SliceIsSorted(first, func(i, j int) bool { return less(first[i], first[j]) }) {
		t.Fatalf("connections = %+v, want 7 sorted by (user, panel, node, source)", first)
	}
	for i := 0; i < 20; i++ {
		if again, _ := CollectLiveConnections(build(), own, nil, LiveConnMaxPerUser); !reflect.DeepEqual(again, first) {
			t.Fatalf("run %d = %+v, want the same order as the first %+v", i, again, first)
		}
	}
}

// Node guids and unparseable addresses come from the upstream verbatim and
// are bounded by nothing PSP owns, while each is shown and stored in a
// fixed-width column. Each is cut to its width on a character boundary —
// never mid-rune, which some drivers reject and every reader renders as a
// replacement character.
func TestCollectLiveConnections_CutsOverlongValues(t *testing.T) {
	node := strings.Repeat("é", 40) // 80 bytes
	raw := strings.Repeat("地", 30)  // 90 bytes, unparseable
	panels := []PanelLiveIPs{timedPanel(1, map[string][]LiveIPSighting{"u7@a": {{IP: raw, Node: node, SeenAt: 1}}})}

	got, _ := CollectLiveConnections(panels, owners(1, "u7@a", 7), nil, LiveConnMaxPerUser)

	if len(got) != 1 {
		t.Fatalf("connections = %+v, want 1", got)
	}
	c := got[0]
	for _, f := range []struct {
		name, v, full string
		max           int
	}{
		{"Node", c.Node, node, LiveConnNodeMaxBytes},
		{"SourceKey", c.SourceKey, raw, LiveConnKeyMaxBytes},
		{"IP", c.IP, raw, LiveConnIPMaxBytes},
	} {
		if len(f.v) > f.max || !utf8.ValidString(f.v) || !strings.HasPrefix(f.full, f.v) || len(f.v) < f.max-3 {
			t.Errorf("%s = %q (%d bytes), want the longest whole-character prefix within %d bytes", f.name, f.v, len(f.v), f.max)
		}
	}

	// Two overlong sources that share their first 64 bytes are one
	// connection once cut, as they would be one stored row. If either was
	// judged, the connection reads as judged — whichever the map order
	// happened to visit first.
	other := strings.Repeat("地", 22) + "zz" // 68 bytes, same cut as raw
	panels = []PanelLiveIPs{{PanelID: 1, Fresh: map[string][]string{"u7@a": {raw, other}}}}
	addrs := map[int64]UserAddresses{7: {UserID: 7, ExcludedBy: map[string]string{raw: AddressExcludedShared}}}
	for i := 0; i < 20; i++ {
		got, _ = CollectLiveConnections(panels, owners(1, "u7@a", 7), addrs, LiveConnMaxPerUser)
		if len(got) != 1 || got[0].Exclusion != "" {
			t.Fatalf("merged by the cut: %+v, want one connection read as judged", got)
		}
	}
}

// A place is shown and stored in fixed-width columns: each name is cut to
// its width on a character boundary, the region code is normalised the one
// way every other reader normalises it, and the coordinates never leave the
// GeoLocation (ConnPlace has nowhere to put them).
func TestConnPlaceOf_NormalizesTheRegionCodeAndCutsEachField(t *testing.T) {
	long := strings.Repeat("市", 60) // 180 bytes
	got := ConnPlaceOf(GeoLocation{
		CountryCode: "CN", Country: "China", Region: long, RegionCode: " gd ", City: long,
		Latitude: 22.5, Longitude: 114.1, AccuracyRadiusKm: 20,
	})
	if got.CountryCode != "CN" || got.Country != "China" || got.RegionCode != "GD" {
		t.Fatalf("place = %+v, want CN / China / GD", got)
	}
	for _, f := range []struct {
		name, v string
		max     int
	}{{"Region", got.Region, ConnPlaceRegionMaxBytes}, {"City", got.City, ConnPlaceCityMaxBytes}} {
		if len(f.v) > f.max || !utf8.ValidString(f.v) || !strings.HasPrefix(long, f.v) || len(f.v) < f.max-3 {
			t.Errorf("%s = %d bytes, want the longest whole-character prefix within %d", f.name, len(f.v), f.max)
		}
	}
	if bad := ConnPlaceOf(GeoLocation{RegionCode: "GD-1"}); bad.RegionCode != "" {
		t.Fatalf("RegionCode %q kept, want an invalid code dropped", bad.RegionCode)
	}
}
