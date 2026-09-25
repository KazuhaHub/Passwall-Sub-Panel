package domain

import (
	"errors"
	"fmt"
	"net/netip"
	"reflect"
	"strings"
	"testing"
)

// Address hygiene sits between "which addresses did the upstream report" and
// "which places is this person in". Every mistake here that KEEPS an address
// it should not is a potential accusation; every mistake that drops one is a
// missed sharer. The tests name which direction they guard.

// ---- SourceKey -------------------------------------------------------------

// A phone rotates IPv6 privacy addresses inside its /64 every few hours.
// Keyed per address, one handset would read as a crowd.
func TestSourceKey_IPv6CollapsesToSlash64(t *testing.T) {
	key, addr, ok := SourceKey(" 2001:db8:1:2:aaaa:bbbb:cccc:dddd ")
	if !ok || key != "2001:db8:1:2::/64" {
		t.Fatalf("SourceKey = %q ok=%v, want 2001:db8:1:2::/64 true", key, ok)
	}
	if addr != netip.MustParseAddr("2001:db8:1:2:aaaa:bbbb:cccc:dddd") {
		t.Fatalf("addr = %v, want the member address itself, not the network", addr)
	}
}

// An IPv4 client reaching a dual-stack listener is reported as ::ffff:a.b.c.d.
// Left mapped, it would key as an IPv6 /64 and never match the same client
// seen as plain IPv4 on another node.
func TestSourceKey_IPv4MappedIsIPv4(t *testing.T) {
	key, addr, ok := SourceKey("::ffff:1.2.3.4")
	if !ok || key != "1.2.3.4" || !addr.Is4() {
		t.Fatalf("SourceKey = %q %v ok=%v, want 1.2.3.4 as a plain IPv4", key, addr, ok)
	}
}

// Something PSP cannot parse is still a connection. Dropping it would make
// the user look quieter than they are; keeping it verbatim keeps it counted.
func TestSourceKey_UnparseableKeptVerbatim(t *testing.T) {
	key, addr, ok := SourceKey("  not-an-address ")
	if ok || key != "not-an-address" || addr.IsValid() {
		t.Fatalf("SourceKey = %q %v ok=%v, want the trimmed raw string, no address, ok=false", key, addr, ok)
	}
}

// A zone names an interface on the reporting host, not a place. Keeping it
// would split one source into as many keys as the host has interfaces.
func TestSourceKey_ZoneIsDropped(t *testing.T) {
	key, addr, ok := SourceKey("fe80::1%eth0")
	if !ok || key != "fe80::/64" || addr.Zone() != "" {
		t.Fatalf("SourceKey = %q zone=%q ok=%v, want fe80::/64 with no zone", key, addr.Zone(), ok)
	}
}

// ---- FreshLiveIPs ----------------------------------------------------------

func sighting(ip, node string, at int64) LiveIPSighting {
	return LiveIPSighting{IP: ip, Node: node, SeenAt: at}
}

// detailPanel builds a panel as a detail reader would deliver it: ByEmail is
// the flattened window, Sightings the same answer with node and time kept.
// ByEmail is built here rather than through LiveIPsOf so these tests do not
// depend on the function another test pins.
func detailPanel(id int64, s map[string][]LiveIPSighting) PanelLiveIPs {
	by := map[string][]string{}
	for email, list := range s {
		for _, x := range list {
			by[email] = append(by[email], x.IP)
		}
	}
	return PanelLiveIPs{PanelID: id, ByEmail: by, Sightings: s}
}

// freshOf returns panel id's Fresh map from FreshLiveIPs' output. A panel
// missing from the output is a failure of its own: every input panel must
// come back, or its users silently drop out of the aggregate.
func freshOf(t *testing.T, out []PanelLiveIPs, id int64) map[string][]string {
	t.Helper()
	for _, p := range out {
		if p.PanelID == id {
			return p.Fresh
		}
	}
	t.Fatalf("panel %d missing from FreshLiveIPs output %+v", id, out)
	return nil
}

// The upstream keeps an address for 30 minutes after its stream closed. Only
// the addresses its node still restamps are live; the rest are where the
// user WAS, and reading them as "at once" is how one commute becomes two
// cities.
func TestFresh_KeepsWithin120sOfTheNodesNewest(t *testing.T) {
	out, _ := FreshLiveIPs([]PanelLiveIPs{detailPanel(1, map[string][]LiveIPSighting{
		"u7@x": {sighting("1.1.1.1", "n1", 1000), sighting("2.2.2.2", "n1", 900), sighting("3.3.3.3", "n1", 850)},
	})}, nil)
	got := freshOf(t, out, 1)["u7@x"]
	want := []string{"1.1.1.1", "2.2.2.2"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("fresh = %v, want %v — 850 is 150s behind the node's newest scan and is where the user was, not is", got, want)
	}
}

// Each node scans on its own. Judging a quiet node against a busy sibling's
// clock would read every address on the quiet one as stale.
func TestFresh_JudgedPerNode(t *testing.T) {
	out, _ := FreshLiveIPs([]PanelLiveIPs{detailPanel(1, map[string][]LiveIPSighting{
		"u7@x": {sighting("1.1.1.1", "a", 1000), sighting("2.2.2.2", "b", 500), sighting("3.3.3.3", "b", 300)},
	})}, nil)
	got := freshOf(t, out, 1)["u7@x"]
	want := []string{"1.1.1.1", "2.2.2.2"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("fresh = %v, want %v — 2.2.2.2 is node b's newest and live by b's clock", got, want)
	}
}

// A node that stopped rescanning (nobody streaming) serves the same frozen
// batch poll after poll. Its newest timestamp did not move, so nothing on it
// is live — otherwise yesterday's commute is replayed as evidence forever.
func TestFresh_ReferenceThatDidNotAdvanceIsAllStale(t *testing.T) {
	prev := map[NodeRef]int64{{PanelID: 1, Node: "n1"}: 1000}
	out, next := FreshLiveIPs([]PanelLiveIPs{detailPanel(1, map[string][]LiveIPSighting{
		"u7@x": {sighting("1.1.1.1", "n1", 1000), sighting("2.2.2.2", "n1", 990)},
	})}, prev)
	fresh := freshOf(t, out, 1)
	if fresh == nil || len(fresh) != 0 {
		t.Fatalf("fresh = %#v, want a non-nil empty map: computed, and nobody is live", fresh)
	}
	if next[NodeRef{PanelID: 1, Node: "n1"}] != 1000 {
		t.Fatalf("next = %v, want the node's reference carried at 1000", next)
	}
}

// After a restart PSP has no memory of any node's clock. It cannot tell a
// frozen batch from a live one, so it trusts the data once — the v1
// behaviour — rather than calling a whole fleet idle.
func TestFresh_FirstPollAfterRestartTrusts(t *testing.T) {
	out, next := FreshLiveIPs([]PanelLiveIPs{detailPanel(1, map[string][]LiveIPSighting{
		"u7@x": {sighting("1.1.1.1", "n1", 1000), sighting("2.2.2.2", "n1", 950)},
	})}, map[NodeRef]int64{})
	got := freshOf(t, out, 1)["u7@x"]
	if !reflect.DeepEqual(got, []string{"1.1.1.1", "2.2.2.2"}) {
		t.Fatalf("fresh = %v, want both addresses trusted on the first poll", got)
	}
	if next[NodeRef{PanelID: 1, Node: "n1"}] != 1000 {
		t.Fatalf("next = %v, want the reference recorded for the next poll", next)
	}
}

// A sighting without a timestamp cannot be judged, so it is read the way v1
// read everything: live. Even on a node whose reference did not advance.
func TestFresh_MissingTimestampIsFresh(t *testing.T) {
	prev := map[NodeRef]int64{{PanelID: 1, Node: "n1"}: 1000}
	out, _ := FreshLiveIPs([]PanelLiveIPs{detailPanel(1, map[string][]LiveIPSighting{
		"u7@x": {sighting("1.1.1.1", "n1", 1000), sighting("2.2.2.2", "n1", 0)},
	})}, prev)
	got := freshOf(t, out, 1)["u7@x"]
	if !reflect.DeepEqual(got, []string{"2.2.2.2"}) {
		t.Fatalf("fresh = %v, want only the untimed address", got)
	}
}

// A plain reader (a PSP-native node) has no timestamps at all. Its whole
// window is what it reports as live, exactly as before.
func TestFresh_PlainReaderPanelIsAllFresh(t *testing.T) {
	by := map[string][]string{"u7@x": {"1.1.1.1", "2.2.2.2"}}
	out, next := FreshLiveIPs([]PanelLiveIPs{{PanelID: 1, ByEmail: by}}, nil)
	if got := freshOf(t, out, 1); !reflect.DeepEqual(got, by) {
		t.Fatalf("fresh = %v, want the whole window %v", got, by)
	}
	if len(next) != 0 {
		t.Fatalf("next = %v, want no reference from a panel with no clock", next)
	}
}

// An unread panel stays unread: no Fresh to aggregate, and no reference
// taken from whatever partial answer came with the error.
func TestFresh_UnreadPanelPassesThrough(t *testing.T) {
	readErr := errors.New("timeout")
	p := detailPanel(2, map[string][]LiveIPSighting{"u7@x": {sighting("1.1.1.1", "n1", 1000)}})
	p.Err = readErr
	out, next := FreshLiveIPs([]PanelLiveIPs{p}, nil)
	if len(out) != 1 || out[0].PanelID != 2 || !errors.Is(out[0].Err, readErr) {
		t.Fatalf("out = %+v, want the unread panel returned with its error intact", out)
	}
	if out[0].Fresh != nil {
		t.Fatalf("fresh = %v, want nil for a panel that could not be read", out[0].Fresh)
	}
	if len(next) != 0 {
		t.Fatalf("next = %v, want no reference from an unread panel", next)
	}
}

// Only a node that actually has a clock contributes a reference. Recording
// 0 for an untimed node would make the NEXT poll read as "first after
// restart" for it forever, which is harmless, but a stored 0 would also be
// indistinguishable from a real clock at the epoch.
func TestFresh_NextRefsCarryOnlyTimestampedNodes(t *testing.T) {
	_, next := FreshLiveIPs([]PanelLiveIPs{detailPanel(1, map[string][]LiveIPSighting{
		"u7@x": {sighting("1.1.1.1", "a", 1000), sighting("2.2.2.2", "b", 0)},
	})}, nil)
	want := map[NodeRef]int64{{PanelID: 1, Node: "a"}: 1000}
	if !reflect.DeepEqual(next, want) {
		t.Fatalf("next = %v, want %v", next, want)
	}
}

// The node's clock is the node's, whoever the clients belong to. A client
// PSP does not own (hand-made on the panel) still proves the node rescanned,
// and ignoring it would read the owned client's live address as frozen.
func TestFresh_UnownedEmailsAdvanceTheReference(t *testing.T) {
	prev := map[NodeRef]int64{{PanelID: 1, Node: "n1"}: 1000}
	out, next := FreshLiveIPs([]PanelLiveIPs{detailPanel(1, map[string][]LiveIPSighting{
		"u7@x":          {sighting("1.1.1.1", "n1", 1000)},
		"hand-made@pnl": {sighting("9.9.9.9", "n1", 1010)},
	})}, prev)
	if got := freshOf(t, out, 1)["u7@x"]; !reflect.DeepEqual(got, []string{"1.1.1.1"}) {
		t.Fatalf("fresh = %v, want u7's address live: the node's reference advanced to 1010", got)
	}
	if next[NodeRef{PanelID: 1, Node: "n1"}] != 1010 {
		t.Fatalf("next = %v, want 1010", next)
	}
}

// (guard) Pure: the caller's panels and reference map are not written. The
// service holds the reference map under a lock and merges the result itself.
func TestFresh_DoesNotMutateItsInput(t *testing.T) {
	panels := []PanelLiveIPs{detailPanel(1, map[string][]LiveIPSighting{"u7@x": {sighting("1.1.1.1", "n1", 1000)}})}
	prev := map[NodeRef]int64{{PanelID: 1, Node: "n1"}: 900}
	FreshLiveIPs(panels, prev)
	if panels[0].Fresh != nil {
		t.Fatal("the input panel was written; FreshLiveIPs must return copies")
	}
	if prev[NodeRef{PanelID: 1, Node: "n1"}] != 900 || len(prev) != 1 {
		t.Fatalf("prev = %v, want it untouched", prev)
	}
}

// ---- ClassifyAddresses -----------------------------------------------------

func live(uid int64, fresh ...string) UserLiveIPs {
	return UserLiveIPs{UserID: uid, IPs: fresh, Fresh: fresh}
}

func keysOf(a UserAddresses) []string {
	out := []string{}
	for _, s := range a.Kept {
		out = append(out, s.Key)
	}
	return out
}

func sharedRule() AddressExclusions { return AddressExclusions{SharedMinUsers: SharedExitMinUsers} }

// Three accounts on one source at once is a campus, a carrier NAT or an
// office — not a place any one of them is in.
func TestClassify_ThreeUsersOnOneKeyIsShared(t *testing.T) {
	got := ClassifyAddresses(map[int64]UserLiveIPs{
		1: live(1, "8.8.8.8", "9.9.9.9"),
		2: live(2, "8.8.8.8"),
		3: live(3, "8.8.8.8"),
	}, sharedRule())
	for _, uid := range []int64{1, 2, 3} {
		if got[uid].Excluded.Shared != 1 {
			t.Fatalf("user %d: shared = %d, want 1", uid, got[uid].Excluded.Shared)
		}
	}
	if k := keysOf(got[1]); !reflect.DeepEqual(k, []string{"9.9.9.9"}) {
		t.Fatalf("user 1 kept %v, want only the unshared address", k)
	}
}

// Two accounts on one address is a household — a couple on one wifi. That
// is a real place for both of them and must stay evidence.
func TestClassify_TwoUsersOnOneKeyIsNotShared(t *testing.T) {
	got := ClassifyAddresses(map[int64]UserLiveIPs{
		1: live(1, "8.8.8.8"),
		2: live(2, "8.8.8.8"),
	}, sharedRule())
	for _, uid := range []int64{1, 2} {
		if k := keysOf(got[uid]); !reflect.DeepEqual(k, []string{"8.8.8.8"}) || got[uid].Excluded.Shared != 0 {
			t.Fatalf("user %d kept %v shared=%d, want the address kept", uid, k, got[uid].Excluded.Shared)
		}
	}
}

// One phone, several privacy addresses in its /64: one source.
func TestClassify_OnePhonesPrivacyAddressesAreOneSource(t *testing.T) {
	got := ClassifyAddresses(map[int64]UserLiveIPs{
		1: live(1, "2001:db8:1:2::a", "2001:db8:1:2::b", "2001:db8:1:2:ffff::1", "2001:db8:1:3::1"),
	}, AddressExclusions{})
	want := []string{"2001:db8:1:2::/64", "2001:db8:1:3::/64"}
	if k := keysOf(got[1]); !reflect.DeepEqual(k, want) {
		t.Fatalf("kept %v, want %v", k, want)
	}
}

// 100.64.0.0/10 is carrier-grade NAT: strangers behind one carrier gateway
// share it, and no geo database can place it.
func TestClassify_CGNATSharedSpaceIsInternal(t *testing.T) {
	got := ClassifyAddresses(map[int64]UserLiveIPs{
		1: live(1, "100.64.1.2", "100.127.255.1", "100.128.0.1"),
	}, AddressExclusions{Internal: true})
	if got[1].Excluded.Internal != 2 {
		t.Fatalf("internal = %d, want the two addresses inside 100.64.0.0/10", got[1].Excluded.Internal)
	}
	if k := keysOf(got[1]); !reflect.DeepEqual(k, []string{"100.128.0.1"}) {
		t.Fatalf("kept %v, want 100.128.0.1 — just outside the /10 is ordinary public space", k)
	}
}

func TestClassify_PrivateLoopbackLinkLocalAreInternal(t *testing.T) {
	internal := []string{
		"10.1.2.3", "172.16.5.4", "192.168.1.1", // private
		"127.0.0.1", "::1", // loopback
		"169.254.1.1", "fe80::1", // link-local unicast
		"224.0.0.251", "ff02::1", // link-local multicast
		"0.0.0.0",      // unspecified
		"fd12:3456::1", // unique local
	}
	got := ClassifyAddresses(map[int64]UserLiveIPs{1: live(1, append(internal, "203.0.113.1")...)},
		AddressExclusions{Internal: true})
	if got[1].Excluded.Internal != len(internal) {
		t.Fatalf("internal = %d, want %d", got[1].Excluded.Internal, len(internal))
	}
	if k := keysOf(got[1]); !reflect.DeepEqual(k, []string{"203.0.113.1"}) {
		t.Fatalf("kept %v, want only the public address", k)
	}
}

// Off means off: the internal rule is a choice the caller makes.
func TestClassify_InternalOnlyWhenAsked(t *testing.T) {
	got := ClassifyAddresses(map[int64]UserLiveIPs{1: live(1, "10.0.0.1")}, AddressExclusions{})
	if k := keysOf(got[1]); !reflect.DeepEqual(k, []string{"10.0.0.1"}) || got[1].Excluded.Internal != 0 {
		t.Fatalf("kept %v internal=%d, want the address kept when Internal is off", k, got[1].Excluded.Internal)
	}
}

// Precedence is fixed so the counts an admin reads are stable: a private
// address the admin also listed reports as internal, which is the more
// fundamental reason.
func TestClassify_InternalWinsOverListed(t *testing.T) {
	list, _ := ParseGeoIgnoreList("10.0.0.0/8")
	got := ClassifyAddresses(map[int64]UserLiveIPs{1: live(1, "10.0.0.1")},
		AddressExclusions{Internal: true, Ignore: list})
	if e := got[1].Excluded; e.Internal != 1 || e.Listed != 0 {
		t.Fatalf("excluded = %+v, want internal 1, listed 0", e)
	}
}

func TestClassify_ListedWinsOverInfra(t *testing.T) {
	list, _ := ParseGeoIgnoreList("203.0.113.5")
	got := ClassifyAddresses(map[int64]UserLiveIPs{1: live(1, "203.0.113.5")},
		AddressExclusions{Ignore: list, Infra: func(netip.Addr) bool { return true }})
	if e := got[1].Excluded; e.Listed != 1 || e.Infra != 0 {
		t.Fatalf("excluded = %+v, want listed 1, infra 0", e)
	}
}

// Three users arriving through one relay are not a shared exit, they are a
// relay — and "infrastructure" is the reason that tells the admin why.
func TestClassify_InfraWinsOverShared(t *testing.T) {
	relay := netip.MustParseAddr("203.0.113.9")
	got := ClassifyAddresses(map[int64]UserLiveIPs{
		1: live(1, "203.0.113.9"), 2: live(2, "203.0.113.9"), 3: live(3, "203.0.113.9"),
	}, AddressExclusions{SharedMinUsers: SharedExitMinUsers, Infra: func(a netip.Addr) bool { return a == relay }})
	for _, uid := range []int64{1, 2, 3} {
		if e := got[uid].Excluded; e.Infra != 1 || e.Shared != 0 {
			t.Fatalf("user %d excluded = %+v, want infra 1, shared 0", uid, e)
		}
	}
}

// The address asked of the geo database must be stable from poll to poll,
// or one phone's source could hop between two database rows.
func TestClassify_LookupIPIsTheSmallestMember(t *testing.T) {
	got := ClassifyAddresses(map[int64]UserLiveIPs{
		1: live(1, "2001:db8::9", "2001:db8::3", "203.0.113.7"),
	}, AddressExclusions{})
	want := []SourceAddr{
		{Key: "2001:db8::/64", LookupIP: "2001:db8::3", Network: "2001:db8::/48"},
		{Key: "203.0.113.7", LookupIP: "203.0.113.7", Network: "203.0.113.0/24"},
	}
	if !reflect.DeepEqual(got[1].Kept, want) {
		t.Fatalf("kept = %+v, want %+v", got[1].Kept, want)
	}
}

// An address PSP cannot parse cannot be excluded by any address rule, so it
// is kept and asked about verbatim; dropping it would under-count the user.
func TestClassify_UnparseableAddressIsKeptVerbatim(t *testing.T) {
	got := ClassifyAddresses(map[int64]UserLiveIPs{1: live(1, "garbage")}, AddressExclusions{Internal: true})
	want := []SourceAddr{{Key: "garbage", LookupIP: "garbage", Network: ""}}
	if !reflect.DeepEqual(got[1].Kept, want) {
		t.Fatalf("kept = %+v, want %+v", got[1].Kept, want)
	}
}

// What the window remembers but is not live is reported, not judged: the
// admin sees "21 seen earlier" instead of wondering where they went.
func TestClassify_StaleIsWindowMinusFresh(t *testing.T) {
	u := UserLiveIPs{UserID: 1,
		IPs:   []string{"1.1.1.1", "2.2.2.2", "3.3.3.3", "4.4.4.4", "5.5.5.5"},
		Fresh: []string{"1.1.1.1", "2.2.2.2"}}
	got := ClassifyAddresses(map[int64]UserLiveIPs{1: u}, AddressExclusions{})
	if got[1].Stale != 3 {
		t.Fatalf("stale = %d, want 3", got[1].Stale)
	}
}

// An idle user still gets a row: the caller must tell "idle" from "not
// looked at", exactly as the aggregate does.
func TestClassify_EveryUserGetsARow(t *testing.T) {
	got := ClassifyAddresses(map[int64]UserLiveIPs{
		1: live(1),
		2: {UserID: 2},
		3: live(3, "1.1.1.1"),
	}, sharedRule())
	for _, uid := range []int64{1, 2, 3} {
		if r, ok := got[uid]; !ok || r.UserID != uid {
			t.Fatalf("user %d: row = %+v present=%v, want a row", uid, r, ok)
		}
	}
}

func TestGeoExcluded_TotalSumsEveryReason(t *testing.T) {
	if n := (GeoExcluded{Shared: 1, Listed: 2, Infra: 3, Internal: 4}).Total(); n != 10 {
		t.Fatalf("Total = %d, want 10", n)
	}
}

// ---- ParseGeoIgnoreList ----------------------------------------------------

func TestParseGeoIgnoreList_AcceptsIPsCIDRsCommasNewlinesAndComments(t *testing.T) {
	l, err := ParseGeoIgnoreList("203.0.113.5, 198.51.100.0/24 # office\n\n# a whole-line comment 192.0.2.1\r\n2001:db8::/32\t,  ,")
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	for _, in := range []string{"203.0.113.5", "198.51.100.77", "2001:db8:ffff::1"} {
		if !l.Contains(netip.MustParseAddr(in)) {
			t.Errorf("%s should be listed", in)
		}
	}
	for _, out := range []string{"203.0.113.6", "192.0.2.1", "2001:db9::1"} {
		if l.Contains(netip.MustParseAddr(out)) {
			t.Errorf("%s should not be listed (a commented-out entry is not an entry)", out)
		}
	}
}

// A typo fails OPEN — the address the admin meant to exclude keeps accusing
// somebody — so every bad entry is named, and the good ones still apply.
func TestParseGeoIgnoreList_ReportsEveryBadEntry(t *testing.T) {
	l, err := ParseGeoIgnoreList("1.2.3.999\n10.0.0.0/33, 203.0.113.5")
	if !errors.Is(err, ErrValidation) {
		t.Fatalf("err = %v, want ErrValidation", err)
	}
	for _, bad := range []string{"1.2.3.999", "10.0.0.0/33"} {
		if !strings.Contains(err.Error(), bad) {
			t.Errorf("error %q does not name %s", err, bad)
		}
	}
	if !l.Contains(netip.MustParseAddr("203.0.113.5")) {
		t.Fatal("the valid entry must survive a bad neighbour")
	}
}

func TestParseGeoIgnoreList_RejectsMoreThan256(t *testing.T) {
	var b strings.Builder
	for i := 0; i < GeoIgnoreListMaxEntries+1; i++ {
		fmt.Fprintf(&b, "10.%d.%d.1\n", i/256, i%256)
	}
	l, err := ParseGeoIgnoreList(b.String())
	if !errors.Is(err, ErrValidation) || !strings.Contains(err.Error(), "257") {
		t.Fatalf("err = %v, want ErrValidation naming the count 257", err)
	}
	if len(l.prefixes) != GeoIgnoreListMaxEntries+1 {
		t.Fatalf("carried %d entries, want every valid one (%d)", len(l.prefixes), GeoIgnoreListMaxEntries+1)
	}
}

func TestParseGeoIgnoreList_MasksHostBits(t *testing.T) {
	l, err := ParseGeoIgnoreList("10.1.2.3/8, 2001:db8::1/32, 192.0.2.7")
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	want := []netip.Prefix{
		netip.MustParsePrefix("10.0.0.0/8"),
		netip.MustParsePrefix("2001:db8::/32"),
		netip.MustParsePrefix("192.0.2.7/32"),
	}
	if !reflect.DeepEqual(l.prefixes, want) {
		t.Fatalf("prefixes = %v, want %v", l.prefixes, want)
	}
}

// The addresses being checked are unmapped, so a mapped entry left as IPv6
// would never match anything — a silent fail-open.
func TestParseGeoIgnoreList_MappedPrefixBecomesIPv4(t *testing.T) {
	l, err := ParseGeoIgnoreList("::ffff:198.51.100.0/120, ::ffff:203.0.113.5")
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	want := []netip.Prefix{netip.MustParsePrefix("198.51.100.0/24"), netip.MustParsePrefix("203.0.113.5/32")}
	if !reflect.DeepEqual(l.prefixes, want) {
		t.Fatalf("prefixes = %v, want %v", l.prefixes, want)
	}
	if !l.Contains(netip.MustParseAddr("198.51.100.9")) {
		t.Fatal("a plain IPv4 inside the mapped prefix must match")
	}
	// Fewer than 96 bits reaches outside the mapped range: there is no
	// IPv4 prefix it could mean.
	if _, err := ParseGeoIgnoreList("::ffff:0.0.0.0/90"); !errors.Is(err, ErrValidation) {
		t.Fatalf("err = %v, want a mapped prefix shorter than /96 rejected", err)
	}
}

func TestParseGeoIgnoreList_EmptyIsEmptyNotAnError(t *testing.T) {
	for _, raw := range []string{"", "  \n# only a comment\n , "} {
		l, err := ParseGeoIgnoreList(raw)
		if err != nil || len(l.prefixes) != 0 {
			t.Fatalf("ParseGeoIgnoreList(%q) = %v, %v; want empty and nil", raw, l.prefixes, err)
		}
	}
}
