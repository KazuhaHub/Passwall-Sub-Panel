package domain

import (
	"encoding/json"
	"fmt"
	"math/rand/v2"
	"net/netip"
	"reflect"
	"sort"
	"strings"
	"testing"
)

// The cast of the sub_spread fixtures: P is the account holder's phone,
// which declares a device id; R is a router at home, known only by its
// client string; F is somebody else's client.
var (
	spreadPhone  = SubIdentity{Key: "d:0a1b2c3d4e5f6071", Kind: "hwid", Label: "iOS 18.1 iPhone15,2", HWID4: "0a1b"}
	spreadRouter = SubIdentity{Key: "u:clash.meta/1.19.2", Kind: "ua", Label: "clash.meta/1.19.2"}
	spreadFriend = SubIdentity{Key: "u:ClashX Pro/1.118.0", Kind: "ua", Label: "ClashX Pro/1.118.0"}
)

// on sets the listed window days (0 = the oldest) in a day mask.
func on(days ...int) uint8 {
	var m uint8
	for _, d := range days {
		m |= 1 << d
	}
	return m
}

// span sets every window day from..to, inclusive.
func span(from, to int) uint8 {
	var m uint8
	for d := from; d <= to; d++ {
		m |= 1 << d
	}
	return m
}

const week = uint8(0x7f) // all seven window days

func seen(id SubIdentity, cc, region string, days uint8) SubPlaceSighting {
	return SubPlaceSighting{Identity: id.Key, CC: cc, Region: region, Days: days}
}

// ua is a client known only by its client string, for fixtures that need
// more clients than the cast.
func ua(name string) SubIdentity {
	return SubIdentity{Key: "u:" + name, Kind: "ua", Label: name}
}

// spreadPolicy is the shipped policy: scope city, region tolerance 1, half
// the sources placed, three days to be established.
func spreadPolicy() SubSpreadPolicy {
	return SubSpreadPolicy{Geo: DefaultGeoPolicy(), MinDays: RiskDefaultMinDays}
}

// spreadInput is a fetched seven-day window in which every source was placed
// down to its province: one source per distinct place, and a description of
// every client the sightings name.
func spreadInput(ids []SubIdentity, sightings ...SubPlaceSighting) SubSpreadInput {
	places := map[string]bool{}
	regions := 0
	for _, s := range sightings {
		k := s.CC + "/" + s.Region
		if !places[k] {
			places[k] = true
			if s.Region != "" {
				regions++
			}
		}
	}
	return SubSpreadInput{
		WindowDays: RiskWindowDays, WindowStart: "2026-09-19", Fetched: true,
		Sources: len(places), Placed: len(places), RegionKnown: regions,
		GeoAvailable: true,
		Sightings:    sightings,
		Identities:   ids,
	}
}

func wantSpread(t *testing.T, v RiskVerdict, state GeoState, code RiskCode) {
	t.Helper()
	if v.State != state || v.Code != code {
		t.Fatalf("verdict = %s/%s, want %s/%s", v.State, v.Code, state, code)
	}
}

func mustSpreadEvidence(t *testing.T, ev *SubSpreadEvidence) *SubSpreadEvidence {
	t.Helper()
	if ev == nil {
		t.Fatal("no evidence for a verdict that judged something")
	}
	return ev
}

func wantGroups(t *testing.T, ev *SubSpreadEvidence, groups, all int) {
	t.Helper()
	ev = mustSpreadEvidence(t, ev)
	if ev.Groups != groups || ev.GroupsAll != all {
		t.Fatalf("groups = %d, groups_all = %d; want %d and %d (provinces %+v)", ev.Groups, ev.GroupsAll, groups, all, ev.Provinces)
	}
}

// subSpreadCase is one named week of the behaviour tests below.
type subSpreadCase struct {
	name string
	in   SubSpreadInput
}

// subSpreadCases are the inputs of the behaviour tests that pin how
// provinces link, group and get judged, kept in one table so a guard can run
// every one of them again with region codes attached
// (TestSubSpread_RegionCodeNeverChangesTheVerdict). Each test reads its own
// week by name and asserts what it always asserted.
func subSpreadCases() []subSpreadCase {
	x, y := ua("x/1"), ua("y/1")
	return []subSpreadCase{
		{"trip", spreadInput([]SubIdentity{spreadPhone, spreadRouter},
			seen(spreadPhone, "CN", "Guangdong", week),
			seen(spreadPhone, "CN", "Hunan", span(2, 4)),
			seen(spreadRouter, "CN", "Guangdong", week),
		)},
		{"friend", spreadInput([]SubIdentity{spreadPhone, spreadFriend},
			seen(spreadPhone, "CN", "Guangdong", week),
			seen(spreadFriend, "CN", "Hunan", week),
		)},
		{"sim-never-on-wifi", spreadInput([]SubIdentity{spreadPhone, spreadRouter},
			seen(spreadRouter, "CN", "Guangdong", week),
			seen(spreadPhone, "CN", "Hunan", week),
		)},
		{"transitive", spreadInput([]SubIdentity{x, y},
			seen(x, "CN", "Anhui", week),
			seen(x, "CN", "Beijing", week),
			seen(y, "CN", "Beijing", week),
			seen(y, "CN", "Chongqing", week),
		)},
		{"link-through-unestablished", spreadInput([]SubIdentity{x, y},
			seen(x, "CN", "Anhui", week),
			seen(x, "CN", "Beijing", on(3)),
			seen(y, "CN", "Beijing", on(3)),
			seen(y, "CN", "Chongqing", week),
		)},
		{"country-tier", spreadInput([]SubIdentity{spreadPhone, spreadRouter},
			seen(spreadPhone, "CN", "Guangdong", week),
			seen(spreadRouter, "JP", "Tokyo", week),
		)},
	}
}

// spreadCase is the named week of subSpreadCases.
func spreadCase(t *testing.T, name string) SubSpreadInput {
	t.Helper()
	for _, c := range subSpreadCases() {
		if c.name == name {
			return c.in
		}
	}
	t.Fatalf("no sub_spread case %q", name)
	return SubSpreadInput{}
}

// A trip: the phone fetched at home in Guangdong all week and in Hunan for
// three days. The provinces are linked by the phone — one person, two places
// on different days — so it is one group, not two.
func TestSubSpread_TripLinkedByThePhoneIsClean(t *testing.T) {
	v, ev := EvaluateSubSpread(spreadPolicy(), spreadCase(t, "trip"))
	wantSpread(t, v, GeoStateClean, RiskCodeWithin)
	wantGroups(t, ev, 1, 1)
	if ev.Country != "CN" {
		t.Fatalf("judged country = %q, want CN", ev.Country)
	}
}

// Somebody else, using the account from another province every day: no
// client was ever seen in both, so the two provinces are two groups — over a
// region tolerance of 1.
func TestSubSpread_FriendInAnotherProvinceIsFlagged(t *testing.T) {
	v, ev := EvaluateSubSpread(spreadPolicy(), spreadCase(t, "friend"))
	wantSpread(t, v, GeoStateFlagged, RiskCodeSpread)
	wantGroups(t, ev, 2, 2)
}

// THE KNOWN FALSE POSITIVE (V3-D3), pinned so it stays a decision rather
// than a surprise: a phone whose SIM is from another province geolocates
// there even at home, and when it never refreshes on the home WiFi nothing
// links it to the router. Guangdong (router) and Hunan (phone) are two
// groups, exactly like a friend. The remedy is the admin's: a group with a
// higher max_regions, or risk.sub_spread_off.
func TestSubSpread_OutOfProvinceSIMNeverOnHomeWiFiIsFlagged(t *testing.T) {
	v, ev := EvaluateSubSpread(spreadPolicy(), spreadCase(t, "sim-never-on-wifi"))
	wantSpread(t, v, GeoStateFlagged, RiskCodeSpread)
	wantGroups(t, ev, 2, 2)
}

// The same household once the phone also refreshes on the home WiFi, on any
// days at all: the phone was seen in both provinces, which links them.
func TestSubSpread_OutOfProvinceSIMAlsoOnHomeWiFiIsClean(t *testing.T) {
	v, ev := EvaluateSubSpread(spreadPolicy(), spreadInput([]SubIdentity{spreadPhone, spreadRouter},
		seen(spreadRouter, "CN", "Guangdong", week),
		seen(spreadPhone, "CN", "Hunan", week),
		seen(spreadPhone, "CN", "Guangdong", on(1, 4)),
	))
	wantSpread(t, v, GeoStateClean, RiskCodeWithin)
	wantGroups(t, ev, 1, 1)
}

// An unlinked province seen on one day is not a habit yet: it counts toward
// groups_all, not groups, and reads suspect — worth a look, not a flag.
func TestSubSpread_OneOffProvinceIsSuspect(t *testing.T) {
	v, ev := EvaluateSubSpread(spreadPolicy(), spreadInput([]SubIdentity{spreadPhone, spreadFriend},
		seen(spreadPhone, "CN", "Guangdong", week),
		seen(spreadFriend, "CN", "Hunan", on(5)),
	))
	wantSpread(t, v, GeoStateSuspect, RiskCodeSpreadBuilding)
	wantGroups(t, ev, 1, 2)
}

// Links chain: X was seen in Anhui and Beijing, Y in Beijing and Chongqing,
// so all three are one group though no client saw Anhui and Chongqing both.
func TestSubSpread_LinkingIsTransitive(t *testing.T) {
	v, ev := EvaluateSubSpread(spreadPolicy(), spreadCase(t, "transitive"))
	wantSpread(t, v, GeoStateClean, RiskCodeWithin)
	wantGroups(t, ev, 1, 1)
	for _, p := range ev.Provinces {
		if p.Group != 1 {
			t.Fatalf("province %s is in group %d, want every province in group 1: %+v", p.Region, p.Group, ev.Provinces)
		}
	}
}

// A link may pass through a province that is not established itself. X
// lives in Anhui and Y in Chongqing, and both fetched once from Beijing on
// the same day: that day links them, and the two established provinces are
// one group. Erring toward silence — a province too brief to count on its
// own can still say two clients belong together.
func TestSubSpread_LinkThroughAnUnestablishedProvinceCounts(t *testing.T) {
	v, ev := EvaluateSubSpread(spreadPolicy(), spreadCase(t, "link-through-unestablished"))
	wantSpread(t, v, GeoStateClean, RiskCodeWithin)
	wantGroups(t, ev, 1, 1)
}

// groups counts the components holding an established province, groups_all
// every component, so groups can never exceed groups_all — the UI would
// otherwise read "3 of 2 groups". And the state is exactly what the two
// counts say against the tolerance. Swept over random weeks.
func TestSubSpread_GroupsNeverExceedGroupsAll(t *testing.T) {
	r := rand.New(rand.NewPCG(8, 2026))
	regions := []string{"Anhui", "Beijing", "Chongqing", "Fujian", "Gansu"}
	ids := []SubIdentity{ua("a/1"), ua("b/1"), ua("c/1"), ua("d/1")}
	for i := range 500 {
		var sightings []SubPlaceSighting
		for _, id := range ids {
			for _, reg := range regions {
				if r.IntN(3) == 0 {
					sightings = append(sightings, seen(id, "CN", reg, uint8(r.IntN(128))|on(r.IntN(7))))
				}
			}
		}
		if len(sightings) == 0 {
			continue
		}
		p := spreadPolicy()
		p.Geo.MaxRegions = 1 + r.IntN(3)
		p.MinDays = 1 + r.IntN(7)
		v, ev := EvaluateSubSpread(p, spreadInput(ids, sightings...))
		ev = mustSpreadEvidence(t, ev)
		if ev.Groups > ev.GroupsAll {
			t.Fatalf("case %d: groups %d > groups_all %d (%+v)", i, ev.Groups, ev.GroupsAll, ev.Provinces)
		}
		want := GeoStateClean
		switch {
		case ev.Groups > ev.Tolerance:
			want = GeoStateFlagged
		case ev.GroupsAll > ev.Tolerance:
			want = GeoStateSuspect
		}
		if v.State != want {
			t.Fatalf("case %d: state %s with groups %d/%d at tolerance %d, want %s", i, v.State, ev.Groups, ev.GroupsAll, ev.Tolerance, want)
		}
	}
}

// THE KNOWN FALSE NEGATIVE, pinned: identity is the device id or, without
// one, the exact client string. Two people on the same app and version are
// one "client", so their provinces link and nothing is flagged.
func TestSubSpread_IdenticalClientStringsLink(t *testing.T) {
	same := ua("clash.meta/1.19.2")
	v, ev := EvaluateSubSpread(spreadPolicy(), spreadInput([]SubIdentity{same},
		seen(same, "CN", "Guangdong", week), // the account holder
		seen(same, "CN", "Hunan", week),     // a friend with the same client
	))
	wantSpread(t, v, GeoStateClean, RiskCodeWithin)
	wantGroups(t, ev, 1, 1)
}

// Countries are never judged here. A fetch routed through the proxy exits at
// the landing abroad, so "China and Japan" is the ordinary picture of a
// relayed user; concurrent use abroad is v2's business. The other country is
// shown as context only.
func TestSubSpread_CountryTierIsNeverJudged(t *testing.T) {
	v, ev := EvaluateSubSpread(spreadPolicy(), spreadCase(t, "country-tier"))
	wantSpread(t, v, GeoStateClean, RiskCodeWithin)
	wantGroups(t, ev, 1, 1)
	if ev.Country != "CN" {
		t.Fatalf("judged country = %q, want CN (a tie goes to the smaller code)", ev.Country)
	}
	if want := []SubForeign{{CC: "JP", Days: week}}; !reflect.DeepEqual(ev.Foreign, want) {
		t.Fatalf("foreign = %+v, want %+v", ev.Foreign, want)
	}
	for _, p := range ev.Provinces {
		if p.CC != "CN" {
			t.Fatalf("a province of %s is listed: provinces are never folded across countries (%+v)", p.CC, ev.Provinces)
		}
	}
}

// One country is judged: the one with the most established provinces, then
// the most provinces, then the smaller code. Three one-day regions in Japan
// (a proxy's rotating egress) must not displace two home provinces in China
// — judged, Japan would read suspect on three unlinked groups.
func TestSubSpread_JudgedCountryHasMostEstablishedProvinces(t *testing.T) {
	x, y, z := ua("x/1"), ua("y/1"), ua("z/1")
	for _, c := range []struct {
		name    string
		in      SubSpreadInput
		country string
		state   GeoState
	}{
		{"most established wins", spreadInput([]SubIdentity{spreadPhone, x, y, z},
			seen(spreadPhone, "CN", "Guangdong", week),
			seen(spreadPhone, "CN", "Hunan", week),
			seen(x, "JP", "Tokyo", on(1)),
			seen(y, "JP", "Osaka", on(2)),
			seen(z, "JP", "Kyoto", on(3)),
		), "CN", GeoStateClean},
		{"then most provinces", spreadInput([]SubIdentity{spreadPhone, x, y},
			seen(spreadPhone, "CN", "Guangdong", week),
			seen(x, "JP", "Tokyo", week),
			seen(y, "JP", "Osaka", on(2)),
		), "JP", GeoStateSuspect},
		{"then the smaller code", spreadInput([]SubIdentity{x, y},
			seen(x, "JP", "Tokyo", week),
			seen(y, "DE", "Berlin", week),
		), "DE", GeoStateClean},
	} {
		v, ev := EvaluateSubSpread(spreadPolicy(), c.in)
		ev = mustSpreadEvidence(t, ev)
		if ev.Country != c.country || v.State != c.state {
			t.Errorf("%s: judged %q as %s, want %q as %s", c.name, ev.Country, v.State, c.country, c.state)
		}
	}
}

// The tolerance is the group's own geo_anomaly.max_regions (V3-D3): a group
// that allows two provinces allows two groups here too.
func TestSubSpread_ToleranceIsTheGroupsMaxRegions(t *testing.T) {
	two := spreadInput([]SubIdentity{spreadPhone, spreadFriend},
		seen(spreadPhone, "CN", "Guangdong", week),
		seen(spreadFriend, "CN", "Hunan", week),
	)
	p := spreadPolicy()
	p.Geo.MaxRegions = 2
	v, ev := EvaluateSubSpread(p, two)
	wantSpread(t, v, GeoStateClean, RiskCodeWithin)
	if ev = mustSpreadEvidence(t, ev); ev.Tolerance != 2 {
		t.Fatalf("tolerance = %d, want the group's max_regions 2", ev.Tolerance)
	}

	third := ua("third/1")
	three := spreadInput([]SubIdentity{spreadPhone, spreadFriend, third}, append(two.Sightings,
		seen(third, "CN", "Sichuan", week))...)
	v, _ = EvaluateSubSpread(p, three)
	wantSpread(t, v, GeoStateFlagged, RiskCodeSpread)

	// An unusable tolerance is repaired the way v2 repairs it (0 → 1), not
	// read as "every second province is too many".
	p.Geo.MaxRegions = 0
	v, ev = EvaluateSubSpread(p, two)
	wantSpread(t, v, GeoStateFlagged, RiskCodeSpread)
	if ev.Tolerance != 1 {
		t.Fatalf("tolerance for a stored 0 = %d, want 1", ev.Tolerance)
	}
}

// flaggedWeek is input that would be flagged if it were judged, for the
// guards that must stop it first.
func flaggedWeek() SubSpreadInput {
	return spreadInput([]SubIdentity{spreadPhone, spreadFriend},
		seen(spreadPhone, "CN", "Guangdong", week),
		seen(spreadFriend, "CN", "Hunan", week),
	)
}

// The switch reads disabled with no evidence: the admin sees it took, and
// nothing from before it outlives it. It beats every other guard.
func TestSubSpread_SignalOffIsDisabled(t *testing.T) {
	p := spreadPolicy()
	p.Off = true
	p.Geo.Scope = GeoScopeOff
	v, ev := EvaluateSubSpread(p, flaggedWeek())
	wantSpread(t, v, GeoStateDisabled, RiskCodeSignalOff)
	if ev != nil {
		t.Fatalf("evidence %+v for a switched-off signal, want none", ev)
	}
}

// A scope of "off" switches location judging off; "country" says the admin
// judges countries only, and this signal is about provinces, so it has
// nothing to judge. Both are disabled, each with its own code.
func TestSubSpread_ScopeCountryAndOffAreDisabled(t *testing.T) {
	for scope, code := range map[GeoScope]RiskCode{GeoScopeOff: RiskCodeScopeOff, GeoScopeCountry: RiskCodeScopeCountry} {
		p := spreadPolicy()
		p.Geo.Scope = scope
		v, ev := EvaluateSubSpread(p, flaggedWeek())
		wantSpread(t, v, GeoStateDisabled, code)
		if ev != nil {
			t.Fatalf("scope %s: evidence %+v, want none", scope, ev)
		}
	}
	// An unrecognised stored scope is judged as "country" by v2 (the
	// coarsest tier accuses least), so here it is disabled too.
	p := spreadPolicy()
	p.Geo.Scope = "bogus"
	v, _ := EvaluateSubSpread(p, flaggedWeek())
	wantSpread(t, v, GeoStateDisabled, RiskCodeScopeCountry)
	// Region scope judges provinces.
	p.Geo.Scope = GeoScopeRegion
	v, _ = EvaluateSubSpread(p, flaggedWeek())
	wantSpread(t, v, GeoStateFlagged, RiskCodeSpread)
}

func TestSubSpread_AllowAnywhereIsExempt(t *testing.T) {
	p := spreadPolicy()
	p.Geo.AllowAnywhere = true
	v, ev := EvaluateSubSpread(p, flaggedWeek())
	wantSpread(t, v, GeoStateExempt, RiskCodeAllowAnywhere)
	if ev != nil {
		t.Fatalf("evidence %+v for an exempt account, want none", ev)
	}
}

// An account an admin trusts is exempt with its own code and no evidence:
// subscription places are a location judgement, and trust is exactly "stop
// judging where this account is". It sits after the two scope guards — a
// group that judges no provinces reads disabled whatever one account's review
// says — and before allow_anywhere, because the per-account decision is the
// more specific one and is what the admin who made it expects to read.
func TestSubSpread_TrustedIsExempt(t *testing.T) {
	p := spreadPolicy()
	p.Geo.Trusted = true
	v, ev := EvaluateSubSpread(p, flaggedWeek())
	wantSpread(t, v, GeoStateExempt, RiskCodeTrusted)
	if ev != nil {
		t.Fatalf("evidence %+v for a trusted account, want none", ev)
	}

	p.Geo.AllowAnywhere = true
	v, _ = EvaluateSubSpread(p, flaggedWeek())
	wantSpread(t, v, GeoStateExempt, RiskCodeTrusted)

	for scope, code := range map[GeoScope]RiskCode{GeoScopeOff: RiskCodeScopeOff, GeoScopeCountry: RiskCodeScopeCountry} {
		p := spreadPolicy()
		p.Geo.Trusted = true
		p.Geo.Scope = scope
		v, _ := EvaluateSubSpread(p, flaggedWeek())
		wantSpread(t, v, GeoStateDisabled, code)
	}
	p = spreadPolicy()
	p.Geo.Trusted = true
	p.Off = true
	v, _ = EvaluateSubSpread(p, flaggedWeek())
	wantSpread(t, v, GeoStateDisabled, RiskCodeSignalOff)
}

func TestSubSpread_NoFetchIsIdle(t *testing.T) {
	v, ev := EvaluateSubSpread(spreadPolicy(), SubSpreadInput{WindowDays: RiskWindowDays, WindowStart: "2026-09-19", GeoAvailable: true})
	wantSpread(t, v, GeoStateIdle, RiskCodeNoFetches)
	if ev != nil {
		t.Fatalf("evidence %+v for an account that fetched nothing, want none", ev)
	}
}

// Sub-log retention shorter than min_days leaves a window in which nothing
// can recur often enough to count: unknown, never clean — "we kept too little
// to say" is not "nothing to see".
func TestSubSpread_RetentionShorterThanMinDaysIsUnknown(t *testing.T) {
	in := flaggedWeek()
	in.WindowDays, in.RetentionDays = 2, 2
	for i := range in.Sightings {
		in.Sightings[i].Days = span(0, 1)
	}
	v, ev := EvaluateSubSpread(spreadPolicy(), in)
	wantSpread(t, v, GeoStateUnknown, RiskCodeRetentionShort)
	ev = mustSpreadEvidence(t, ev)
	if ev.WindowDays != 2 || ev.RetentionDays != 2 || ev.MinDays != 3 {
		t.Fatalf("window %d, retention %d, min_days %d; want 2, 2, 3", ev.WindowDays, ev.RetentionDays, ev.MinDays)
	}
	// Two days of logs are enough when two days is all an admin asks for.
	p := spreadPolicy()
	p.MinDays = 2
	v, _ = EvaluateSubSpread(p, in)
	wantSpread(t, v, GeoStateFlagged, RiskCodeSpread)
}

// Every source was set aside (shared exits, the ignore list, PSP's own
// relays, internal ranges): nothing is left to place, so nothing is judged,
// and the evidence says what was set aside.
func TestSubSpread_AllExcludedIsUnknown(t *testing.T) {
	in := flaggedWeek()
	in.Sources, in.Placed, in.RegionKnown = 0, 0, 0
	in.Sightings = nil
	in.Excluded = GeoExcluded{Shared: 2, Listed: 1, Infra: 3, Internal: 1}
	v, ev := EvaluateSubSpread(spreadPolicy(), in)
	wantSpread(t, v, GeoStateUnknown, RiskCodeAllExcluded)
	if ev = mustSpreadEvidence(t, ev); ev.Excluded != in.Excluded {
		t.Fatalf("excluded = %+v, want %+v", ev.Excluded, in.Excluded)
	}
}

// No geo database: nothing can be placed, which is the absence of evidence,
// not a clean result.
func TestSubSpread_GeoUnavailableIsUnknown(t *testing.T) {
	in := flaggedWeek()
	in.GeoAvailable = false
	v, ev := EvaluateSubSpread(spreadPolicy(), in)
	wantSpread(t, v, GeoStateUnknown, RiskCodeGeoUnavailable)
	mustSpreadEvidence(t, ev)
}

// Fewer placed sources than the group's min_placed_ratio: the provinces seen
// are a sample too thin to say the account is in only them.
func TestSubSpread_LowPlacedIsUnknown(t *testing.T) {
	in := flaggedWeek()
	in.Sources, in.Placed, in.RegionKnown = 5, 2, 2
	v, ev := EvaluateSubSpread(spreadPolicy(), in)
	wantSpread(t, v, GeoStateUnknown, RiskCodeLowPlaced)
	ev = mustSpreadEvidence(t, ev)
	if want := (SubCoverage{Sources: 5, Placed: 2, RegionKnown: 2}); ev.Coverage != want || ev.MinPlacedPct != 50 {
		t.Fatalf("coverage %+v at %d%%, want %+v at 50%%", ev.Coverage, ev.MinPlacedPct, want)
	}
	// Exactly the ratio is enough.
	in.Sources = 4
	v, _ = EvaluateSubSpread(spreadPolicy(), in)
	wantSpread(t, v, GeoStateFlagged, RiskCodeSpread)
}

// A source placed only to its country forms no province (v2's rule: without
// a region there is nothing to tell apart). When no source has a region the
// verdict is unknown, and the countries are shown as context.
func TestSubSpread_CountryOnlyRowsAreUnknownNoRegions(t *testing.T) {
	v, ev := EvaluateSubSpread(spreadPolicy(), spreadInput([]SubIdentity{spreadPhone, spreadRouter},
		seen(spreadPhone, "JP", "", on(1)),
		seen(spreadRouter, "CN", "", week),
	))
	wantSpread(t, v, GeoStateUnknown, RiskCodeNoRegions)
	ev = mustSpreadEvidence(t, ev)
	if want := []SubForeign{{CC: "CN", Days: week}, {CC: "JP", Days: on(1)}}; !reflect.DeepEqual(ev.Foreign, want) {
		t.Fatalf("foreign = %+v, want %+v (most days first)", ev.Foreign, want)
	}
}

// The guards apply in the table's order: each row meets two conditions and
// must answer with the earlier one.
func TestSubSpread_GuardsApplyInOrder(t *testing.T) {
	for _, c := range []struct {
		name  string
		p     func(*SubSpreadPolicy)
		in    func(*SubSpreadInput)
		state GeoState
		code  RiskCode
	}{
		{"scope off before exempt", func(p *SubSpreadPolicy) { p.Geo.Scope = GeoScopeOff; p.Geo.AllowAnywhere = true }, nil, GeoStateDisabled, RiskCodeScopeOff},
		{"exempt before idle", func(p *SubSpreadPolicy) { p.Geo.AllowAnywhere = true }, func(in *SubSpreadInput) { in.Fetched = false }, GeoStateExempt, RiskCodeAllowAnywhere},
		{"idle before retention", nil, func(in *SubSpreadInput) { in.Fetched = false; in.WindowDays = 1 }, GeoStateIdle, RiskCodeNoFetches},
		{"retention before all excluded", nil, func(in *SubSpreadInput) { in.WindowDays = 1; in.Sources = 0 }, GeoStateUnknown, RiskCodeRetentionShort},
		{"all excluded before geo", nil, func(in *SubSpreadInput) { in.Sources = 0; in.GeoAvailable = false }, GeoStateUnknown, RiskCodeAllExcluded},
		{"geo before low placed", nil, func(in *SubSpreadInput) { in.GeoAvailable = false; in.Placed = 0 }, GeoStateUnknown, RiskCodeGeoUnavailable},
		{"low placed before no regions", nil, func(in *SubSpreadInput) {
			in.Placed = 0
			for i := range in.Sightings {
				in.Sightings[i].Region = ""
			}
		}, GeoStateUnknown, RiskCodeLowPlaced},
	} {
		p, in := spreadPolicy(), flaggedWeek()
		if c.p != nil {
			c.p(&p)
		}
		if c.in != nil {
			c.in(&in)
		}
		if v, _ := EvaluateSubSpread(p, in); v.State != c.state || v.Code != c.code {
			t.Errorf("%s: %s/%s, want %s/%s", c.name, v.State, v.Code, c.state, c.code)
		}
	}
}

// Groups are numbered established-first — by each group's smallest
// established province — and then the rest by their smallest province, and
// the provinces are listed in group order. The numbers are what the UI puts
// on each province's chip, so the same week must number the same way.
func TestSubSpread_GroupsAreNumberedEstablishedFirst(t *testing.T) {
	x, y, z := ua("x/1"), ua("y/1"), ua("z/1")
	_, ev := EvaluateSubSpread(spreadPolicy(), spreadInput([]SubIdentity{x, y, z},
		seen(x, "CN", "Zhejiang", week),
		seen(y, "CN", "Anhui", on(2)),
		seen(z, "CN", "Beijing", week),
	))
	ev = mustSpreadEvidence(t, ev)
	var got []string
	for _, p := range ev.Provinces {
		got = append(got, fmt.Sprintf("%d:%s:%v", p.Group, p.Region, p.Established))
	}
	want := []string{"1:Beijing:true", "2:Zhejiang:true", "3:Anhui:false"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("provinces = %v, want %v", got, want)
	}
}

// The evidence is bounded — 12 provinces, 10 clients — and ordered: provinces
// by group, clients by how many listed provinces they link. A client that
// touches only provinces cut from the list is not shown. Every slice is
// present even when empty, so the SPA never meets a null list.
func TestSubSpread_EvidenceIsBoundedAndOrdered(t *testing.T) {
	var ids []SubIdentity
	var sightings []SubPlaceSighting
	for i := range 11 {
		id := ua(fmt.Sprintf("client-%02d", i))
		ids = append(ids, id)
		sightings = append(sightings, seen(id, "CN", fmt.Sprintf("R%02d", i), week))
	}
	// client-00 also links R11 and R12 into its own group; client-11 shares
	// R01 with client-01. 13 provinces, 12 clients, 11 groups.
	sightings = append(sightings,
		seen(ids[0], "CN", "R11", week),
		seen(ids[0], "CN", "R12", week),
	)
	extra := ua("client-11")
	ids = append(ids, extra)
	sightings = append(sightings, seen(extra, "CN", "R01", week))

	v, ev := EvaluateSubSpread(spreadPolicy(), spreadInput(ids, sightings...))
	wantSpread(t, v, GeoStateFlagged, RiskCodeSpread)
	wantGroups(t, ev, 11, 11)
	if len(ev.Provinces) != RiskEvidenceMaxProvinces {
		t.Fatalf("%d provinces listed, want the cap %d", len(ev.Provinces), RiskEvidenceMaxProvinces)
	}
	var regions []string
	for i, p := range ev.Provinces {
		regions = append(regions, p.Region)
		if i > 0 && p.Group < ev.Provinces[i-1].Group {
			t.Fatalf("provinces not in group order: %+v", ev.Provinces)
		}
	}
	// R10 is group 11, the last: it is the one cut.
	wantRegions := []string{"R00", "R11", "R12", "R01", "R02", "R03", "R04", "R05", "R06", "R07", "R08", "R09"}
	if !reflect.DeepEqual(regions, wantRegions) {
		t.Fatalf("provinces = %v, want %v", regions, wantRegions)
	}
	if len(ev.Identities) != RiskEvidenceMaxIdentities {
		t.Fatalf("%d clients listed, want the cap %d", len(ev.Identities), RiskEvidenceMaxIdentities)
	}
	var labels []string
	for i, id := range ev.Identities {
		labels = append(labels, id.Label)
		if id.Provinces == nil {
			t.Fatalf("client %s has a nil province list", id.Label)
		}
		if !sort.IntsAreSorted(id.Provinces) {
			t.Fatalf("client %s's provinces %v are not ascending", id.Label, id.Provinces)
		}
		if i > 0 && len(id.Provinces) > len(ev.Identities[i-1].Provinces) {
			t.Fatalf("clients not ordered by linked provinces: %+v", ev.Identities)
		}
	}
	// client-10 links only R10, which was cut; client-11 is 11th of 11.
	wantLabels := []string{"client-00", "client-01", "client-02", "client-03", "client-04", "client-05", "client-06", "client-07", "client-08", "client-09"}
	if !reflect.DeepEqual(labels, wantLabels) {
		t.Fatalf("clients = %v, want %v", labels, wantLabels)
	}
	if !reflect.DeepEqual(ev.Identities[0].Provinces, []int{0, 1, 2}) {
		t.Fatalf("client-00 links %v, want [0 1 2]", ev.Identities[0].Provinces)
	}
	if ev.Foreign == nil {
		t.Fatal("foreign is nil, want an empty list")
	}

	// Foreign is capped at 6, most days first.
	var abroad []SubPlaceSighting
	abroad = append(abroad, seen(spreadPhone, "CN", "Guangdong", week))
	for i, cc := range []string{"DE", "FR", "GB", "JP", "KR", "SG", "US"} {
		abroad = append(abroad, seen(spreadPhone, cc, "", span(0, i%7)))
	}
	_, ev = EvaluateSubSpread(spreadPolicy(), spreadInput([]SubIdentity{spreadPhone}, abroad...))
	ev = mustSpreadEvidence(t, ev)
	var ccs []string
	for _, f := range ev.Foreign {
		ccs = append(ccs, f.CC)
	}
	if want := []string{"US", "SG", "KR", "JP", "GB", "FR"}; !reflect.DeepEqual(ccs, want) {
		t.Fatalf("foreign = %v, want %v", ccs, want)
	}

	// A verdict that stopped at a guard still has every list.
	in := flaggedWeek()
	in.GeoAvailable = false
	_, ev = EvaluateSubSpread(spreadPolicy(), in)
	if ev = mustSpreadEvidence(t, ev); ev.Provinces == nil || ev.Identities == nil || ev.Foreign == nil {
		t.Fatalf("a guard's evidence has a nil list: %+v", ev)
	}
}

// Clients are listed hwid before ua at equal reach, and each shows its days
// across the whole window, wherever it fetched from.
func TestSubSpread_IdentityEvidence(t *testing.T) {
	_, ev := EvaluateSubSpread(spreadPolicy(), spreadInput([]SubIdentity{spreadRouter, spreadPhone},
		seen(spreadRouter, "CN", "Guangdong", week),
		seen(spreadPhone, "CN", "Guangdong", on(0, 1)),
		seen(spreadPhone, "JP", "Tokyo", on(5)),
	))
	ev = mustSpreadEvidence(t, ev)
	want := []SubIdentityEvidence{
		{Kind: "ua", Label: "clash.meta/1.19.2", Days: week, Provinces: []int{0}},
		{Kind: "hwid", Label: "iOS 18.1 iPhone15,2", HWID4: "0a1b", Days: on(0, 1, 5), Provinces: []int{0}},
	}
	// More days first: the router (7) before the phone (3).
	if !reflect.DeepEqual(ev.Identities, want) {
		t.Fatalf("identities = %+v\nwant %+v", ev.Identities, want)
	}
	_, ev = EvaluateSubSpread(spreadPolicy(), spreadInput([]SubIdentity{spreadRouter, spreadPhone},
		seen(spreadRouter, "CN", "Guangdong", week),
		seen(spreadPhone, "CN", "Guangdong", week),
	))
	if ev = mustSpreadEvidence(t, ev); len(ev.Identities) != 2 || ev.Identities[0].Kind != "hwid" {
		t.Fatalf("identities = %+v, want the hwid client first at equal reach and days", ev.Identities)
	}
}

// The evidence names provinces, countries and client labels — never an
// address, and never a client's full device id or its key: the admin sees a
// four-character prefix, and the key is an in-memory handle only.
func TestSubSpread_EvidenceCarriesNoAddress(t *testing.T) {
	_, ev := EvaluateSubSpread(spreadPolicy(), spreadInput([]SubIdentity{spreadPhone, spreadRouter, spreadFriend},
		seen(spreadPhone, "CN", "Guangdong", week),
		seen(spreadRouter, "CN", "Guangdong", week),
		seen(spreadFriend, "CN", "Hunan", week),
		seen(spreadFriend, "JP", "", on(2)),
	))
	ev = mustSpreadEvidence(t, ev)
	raw, err := json.Marshal(ev)
	if err != nil {
		t.Fatal(err)
	}
	for _, leak := range []string{"0a1b2c3d4e5f6071", spreadPhone.Key, spreadRouter.Key, spreadFriend.Key} {
		if strings.Contains(string(raw), leak) {
			t.Fatalf("evidence %s contains %q", raw, leak)
		}
	}
	var doc any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	strs := 0
	var walk func(any)
	walk = func(v any) {
		switch x := v.(type) {
		case map[string]any:
			for _, e := range x {
				walk(e)
			}
		case []any:
			for _, e := range x {
				walk(e)
			}
		case string:
			strs++
			if _, err := netip.ParseAddr(x); err == nil {
				t.Errorf("evidence carries an address: %q", x)
			}
			if _, err := netip.ParsePrefix(x); err == nil {
				t.Errorf("evidence carries a network: %q", x)
			}
		}
	}
	walk(doc)
	if strs == 0 {
		t.Fatal("walked no strings: the evidence is empty")
	}
}

// The evidence's JSON field names are a wire contract: the SPA reads them
// from rows written by older builds too.
func TestSubSpread_EvidenceShape(t *testing.T) {
	in := flaggedWeek()
	in.RetentionDays = 30
	_, ev := EvaluateSubSpread(spreadPolicy(), in)
	ev = mustSpreadEvidence(t, ev)
	raw, err := json.Marshal(ev)
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]json.RawMessage
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	wantKeys := func(what string, obj map[string]json.RawMessage, keys ...string) {
		t.Helper()
		var got []string
		for k := range obj {
			got = append(got, k)
		}
		sort.Strings(got)
		sort.Strings(keys)
		if !reflect.DeepEqual(got, keys) {
			t.Fatalf("%s keys = %v, want %v", what, got, keys)
		}
	}
	wantKeys("evidence", doc, "v", "window_days", "window_start", "retention_days", "min_days", "min_placed_pct",
		"tolerance", "country", "groups", "groups_all", "provinces", "identities", "foreign", "excluded", "coverage")
	var provinces []map[string]json.RawMessage
	if err := json.Unmarshal(doc["provinces"], &provinces); err != nil || len(provinces) == 0 {
		t.Fatalf("provinces %s: %v", doc["provinces"], err)
	}
	wantKeys("province", provinces[0], "cc", "region", "days", "established", "group")
	var ids []map[string]json.RawMessage
	if err := json.Unmarshal(doc["identities"], &ids); err != nil || len(ids) != 2 {
		t.Fatalf("identities %s: %v", doc["identities"], err)
	}
	for _, id := range ids {
		if string(id["kind"]) == `"hwid"` {
			wantKeys("hwid identity", id, "kind", "label", "hwid4", "days", "provinces")
		} else {
			wantKeys("ua identity", id, "kind", "label", "days", "provinces")
		}
	}
	var coverage map[string]json.RawMessage
	if err := json.Unmarshal(doc["coverage"], &coverage); err != nil {
		t.Fatal(err)
	}
	wantKeys("coverage", coverage, "sources", "placed", "region_known")
	if ev.V != RiskEvidenceVersion || ev.WindowStart != "2026-09-19" || ev.RetentionDays != 30 || ev.MinPlacedPct != 50 {
		t.Fatalf("v %d, window_start %q, retention %d, min_placed_pct %d", ev.V, ev.WindowStart, ev.RetentionDays, ev.MinPlacedPct)
	}
	// Without a retention that shortened anything, the key is left out.
	in.RetentionDays = 0
	_, ev = EvaluateSubSpread(spreadPolicy(), in)
	if raw, _ = json.Marshal(ev); strings.Contains(string(raw), "retention_days") {
		t.Fatalf("retention_days present for retention 0: %s", raw)
	}
}

// A country is established for an account when its fetches came from it on
// min_days days — any province, or none known. These are the countries a
// later panel login is measured against.
func TestEstablishedCountries(t *testing.T) {
	s := []SubPlaceSighting{
		seen(spreadPhone, "CN", "Guangdong", on(0, 1)),
		seen(spreadRouter, "CN", "", on(2)),          // country only still counts
		seen(spreadPhone, "JP", "Tokyo", on(0, 1)),   // two days
		seen(spreadFriend, "US", "California", week), // all week
		seen(spreadFriend, "", "", week),             // not placed
	}
	for _, c := range []struct {
		min  int
		want []string
	}{
		{3, []string{"CN", "US"}},
		{2, []string{"CN", "JP", "US"}},
		{0, []string{"CN", "JP", "US"}}, // clamped to 1
		{9, []string{"US"}},             // clamped to the window
	} {
		if got := EstablishedCountries(s, c.min); !reflect.DeepEqual(got, c.want) {
			t.Errorf("EstablishedCountries(min %d) = %v, want %v", c.min, got, c.want)
		}
	}
	if got := EstablishedCountries(nil, 3); got == nil || len(got) != 0 {
		t.Fatalf("EstablishedCountries(nil) = %#v, want an empty list", got)
	}
}

// spreadFixtures reaches every code sub_spread can return, one fixture each.
func spreadFixtures() []struct {
	p  SubSpreadPolicy
	in SubSpreadInput
} {
	with := func(f func(*SubSpreadPolicy)) SubSpreadPolicy { p := spreadPolicy(); f(&p); return p }
	change := func(f func(*SubSpreadInput)) SubSpreadInput { in := flaggedWeek(); f(&in); return in }
	return []struct {
		p  SubSpreadPolicy
		in SubSpreadInput
	}{
		{with(func(p *SubSpreadPolicy) { p.Off = true }), flaggedWeek()},
		{with(func(p *SubSpreadPolicy) { p.Geo.Scope = GeoScopeOff }), flaggedWeek()},
		{with(func(p *SubSpreadPolicy) { p.Geo.Scope = GeoScopeCountry }), flaggedWeek()},
		{with(func(p *SubSpreadPolicy) { p.Geo.Trusted = true }), flaggedWeek()},
		{with(func(p *SubSpreadPolicy) { p.Geo.AllowAnywhere = true }), flaggedWeek()},
		{spreadPolicy(), change(func(in *SubSpreadInput) { in.Fetched = false })},
		{spreadPolicy(), change(func(in *SubSpreadInput) { in.WindowDays = 2 })},
		{spreadPolicy(), change(func(in *SubSpreadInput) { in.Sources = 0 })},
		{spreadPolicy(), change(func(in *SubSpreadInput) { in.GeoAvailable = false })},
		{spreadPolicy(), change(func(in *SubSpreadInput) { in.Placed = 0 })},
		{spreadPolicy(), change(func(in *SubSpreadInput) {
			for i := range in.Sightings {
				in.Sightings[i].Region = ""
			}
		})},
		{spreadPolicy(), flaggedWeek()},
		{spreadPolicy(), change(func(in *SubSpreadInput) { in.Sightings[1].Days = on(3) })},
		{spreadPolicy(), change(func(in *SubSpreadInput) { in.Sightings = in.Sightings[:1] })},
	}
}

// AllRiskCodes()[sub_spread] is the list the SPA's locale keys are checked
// against, so it must be exactly the codes this evaluator can return.
func TestSubSpread_CodesAreExactlyAllRiskCodes(t *testing.T) {
	reached := map[RiskCode]bool{}
	for _, f := range spreadFixtures() {
		v, _ := EvaluateSubSpread(f.p, f.in)
		reached[v.Code] = true
	}
	listed := map[RiskCode]bool{}
	for _, c := range AllRiskCodes()[RiskKindSubSpread] {
		listed[c] = true
	}
	if !reflect.DeepEqual(reached, listed) {
		t.Fatalf("sub_spread codes reached %v, AllRiskCodes lists %v", reached, listed)
	}
}

// coded is s with the region code a database gave its source.
func coded(s SubPlaceSighting, rc string) SubPlaceSighting {
	s.RC = rc
	return s
}

// A province carries its region's ISO code so the admin UI can name it in
// the reader's language. Sources of one province can disagree — a database
// that codes some records and not others, or spells one code in lower case,
// or gives two — and the province then shows the smallest valid code,
// whatever order its sightings arrive in. A code that is not a plausible
// subdivision code is dropped rather than repaired, and a province with no
// valid code has no "rc" key at all: the UI falls back to the region name.
func TestSubSpread_ProvinceCarriesTheSmallestRegionCode(t *testing.T) {
	x := ua("x/1")
	ids := []SubIdentity{spreadPhone, spreadRouter, spreadFriend, x}
	sightings := []SubPlaceSighting{
		coded(seen(spreadPhone, "CN", "Guangdong", week), ""),
		coded(seen(spreadRouter, "CN", "Guangdong", week), "gd"),
		coded(seen(x, "CN", "Guangdong", on(2)), "GX"),
		coded(seen(spreadFriend, "CN", "Hunan", week), "GUANGDONG"),
	}
	reversed := make([]SubPlaceSighting, len(sightings))
	for i, s := range sightings {
		reversed[len(sightings)-1-i] = s
	}
	for _, order := range [][]SubPlaceSighting{sightings, reversed} {
		_, ev := EvaluateSubSpread(spreadPolicy(), spreadInput(ids, order...))
		ev = mustSpreadEvidence(t, ev)
		rc := map[string]string{}
		for _, p := range ev.Provinces {
			rc[p.Region] = p.RC
		}
		if want := map[string]string{"Guangdong": "GD", "Hunan": ""}; !reflect.DeepEqual(rc, want) {
			t.Fatalf("province codes %v, want %v (provinces %+v)", rc, want, ev.Provinces)
		}
		raw, err := json.Marshal(ev)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(raw), `"region":"Guangdong","rc":"GD"`) {
			t.Fatalf("evidence %s does not read Guangdong as GD", raw)
		}
		var doc struct {
			Provinces []map[string]json.RawMessage `json:"provinces"`
		}
		if err := json.Unmarshal(raw, &doc); err != nil {
			t.Fatal(err)
		}
		for _, p := range doc.Provinces {
			if _, has := p["rc"]; has && string(p["region"]) == `"Hunan"` {
				t.Fatalf("Hunan carries an rc key for an invalid code: %s", raw)
			}
		}
	}
}

// withConflictingCodes is in with a region code on every sighting, chosen so
// codes carry no information a key could use: every province's first
// sighting says "ZZ" (so provinces share a code), its second "aa" (so one
// province has two, one in lower case), its third none, its fourth a numeric
// code, its fifth an invalid one. It also says how many provinces were given
// two different valid codes.
func withConflictingCodes(in SubSpreadInput) (SubSpreadInput, int) {
	codes := []string{"ZZ", "aa", "", "13", "CN-GD"}
	out := in
	out.Sightings = make([]SubPlaceSighting, len(in.Sightings))
	// Its own key, not the evaluator's: the guard must still compile when a
	// mutation changes what the evaluator keys provinces by.
	n := map[[2]string]int{}
	conflicts := 0
	for i, s := range in.Sightings {
		k := [2]string{s.CC, s.Region}
		s.RC = codes[n[k]%len(codes)]
		if n[k] == 1 {
			conflicts++
		}
		n[k]++
		out.Sightings[i] = s
	}
	return out, conflicts
}

// withoutRegionCodes is a copy of ev with every province's code cleared.
func withoutRegionCodes(ev *SubSpreadEvidence) *SubSpreadEvidence {
	if ev == nil {
		return nil
	}
	c := *ev
	c.Provinces = make([]SubProvince, len(ev.Provinces))
	copy(c.Provinces, ev.Provinces)
	for i := range c.Provinces {
		c.Provinces[i].RC = ""
	}
	return &c
}

// THE REGION CODE IS A DISPLAY ATTRIBUTE, NEVER A KEY (B-D3, I5). Provinces
// are keyed by (country, region name) as they always were: a database that
// codes some sources and not others, gives one province two codes, or gives
// two provinces the same one must not split, merge, relink or re-judge
// anything. Every behaviour week above runs with and without codes; the
// verdicts are equal, and so is the evidence once the codes are cleared.
func TestSubSpread_RegionCodeNeverChangesTheVerdict(t *testing.T) {
	conflicts := 0
	for _, c := range subSpreadCases() {
		withCodes, n := withConflictingCodes(c.in)
		conflicts += n
		v0, ev0 := EvaluateSubSpread(spreadPolicy(), c.in)
		v1, ev1 := EvaluateSubSpread(spreadPolicy(), withCodes)
		if v0 != v1 {
			t.Errorf("%s: verdict %+v without codes, %+v with them", c.name, v0, v1)
		}
		if a, b := withoutRegionCodes(ev0), withoutRegionCodes(ev1); !reflect.DeepEqual(a, b) {
			t.Errorf("%s: evidence differs once codes are attached\nwithout %+v\nwith    %+v", c.name, a, b)
		}
	}
	// Not vacuous: some province really was given two codes.
	if conflicts == 0 {
		t.Fatal("no case gave a province two different codes")
	}
}
