package domain

import (
	"fmt"
	"reflect"
	"strings"
	"testing"
)

// Each case here names the real person it protects. A verdict from this file
// ends up in front of a human deciding whether to suspend somebody's account,
// so "it returns true" is not what any of these assert — they assert which
// user is judged, and why.

func pol(mut func(*GeoAnomalyPolicy)) GeoAnomalyPolicy {
	p := DefaultGeoPolicy()
	if mut != nil {
		mut(&p)
	}
	return p
}

func obs(places []string, placed, unplaced int) GeoObservation {
	return GeoObservation{UserID: 7, Places: places, Placed: placed, Unplaced: unplaced, GeoAvailable: true}
}

// Run a sequence of samples through the state machine, returning every
// verdict. The streak is the whole point of this design, and a test that
// evaluates one sample in isolation cannot see it.
func sequence(p GeoAnomalyPolicy, samples ...GeoObservation) []GeoVerdict {
	var out []GeoVerdict
	streak := GeoStreak{}
	for _, s := range samples {
		v := EvaluateGeo(p, s, streak)
		streak = v.Streak
		out = append(out, v)
	}
	return out
}

func states(vs []GeoVerdict) []GeoState {
	out := make([]GeoState, 0, len(vs))
	for _, v := range vs {
		out = append(out, v.State)
	}
	return out
}

// ---------------------------------------------------------------- hysteresis

// The knob a naive detector omits, and the reason it flaps. One noisy sample
// must not raise an alert an operator will learn to ignore.
func TestEvaluate_OneNoisySampleDoesNotFlag(t *testing.T) {
	got := states(sequence(pol(nil),
		obs([]string{"JP"}, 2, 0),
		obs([]string{"DE", "JP"}, 2, 0), // one blip
		obs([]string{"JP"}, 2, 0),
	))
	want := []GeoState{GeoStateClean, GeoStateSuspect, GeoStateClean}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("states = %v, want %v — a single blip must surface as Suspect and clear", got, want)
	}
}

// Sustained is what flags. Suspect is the visible ramp so the eventual flag
// does not look like it came from nowhere.
func TestEvaluate_SustainedSpreadFlagsAfterThreshold(t *testing.T) {
	two := obs([]string{"DE", "JP"}, 2, 0)
	got := states(sequence(pol(nil), two, two, two, two))
	want := []GeoState{GeoStateSuspect, GeoStateSuspect, GeoStateFlagged, GeoStateFlagged}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("states = %v, want %v (FlagAfterPolls=3)", got, want)
	}
}

// Clearing is deliberately slower than flagging. Otherwise an account steps
// just under the line for one check and sheds the flag.
func TestEvaluate_ClearingIsSlowerThanFlagging(t *testing.T) {
	two := obs([]string{"DE", "JP"}, 2, 0)
	one := obs([]string{"JP"}, 2, 0)
	p := pol(func(p *GeoAnomalyPolicy) { p.FlagAfterPolls = 2; p.ClearAfterPolls = 4 })
	vs := sequence(p, two, two, one, one, one, one)
	got := states(vs)
	want := []GeoState{
		GeoStateSuspect, GeoStateFlagged,
		GeoStateFlagged, GeoStateFlagged, GeoStateFlagged, // latched for 3 clean checks
		GeoStateClean, // the 4th clears it
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("states = %v, want %v", got, want)
	}
}

// The easiest evasion there is: disconnect for a while and come back clean.
// Idle polls must neither accrue nor shed a streak.
func TestEvaluate_IdleDoesNotClearAFlag(t *testing.T) {
	two := obs([]string{"DE", "JP"}, 2, 0)
	idle := GeoObservation{UserID: 7, GeoAvailable: true}
	p := pol(func(p *GeoAnomalyPolicy) { p.FlagAfterPolls = 2; p.ClearAfterPolls = 2 })
	vs := sequence(p, two, two, idle, idle, idle, idle)
	got := states(vs)
	want := []GeoState{
		GeoStateSuspect, GeoStateFlagged,
		GeoStateIdle, GeoStateIdle, GeoStateIdle, GeoStateIdle,
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("states = %v, want %v", got, want)
	}
	// And the flag is still latched underneath, so reconnecting resumes
	// from flagged rather than from a clean slate.
	if !vs[len(vs)-1].Streak.Flagged {
		t.Fatal("idling must not silently drop the latched flag")
	}
}

// ----------------------------------------------------------------- tolerance

// The knob that separates "two countries is over" from "two is fine, three
// is over" without touching anything else.
func TestEvaluate_ToleranceIsANumberNotAHardcodedTwo(t *testing.T) {
	three := obs([]string{"DE", "JP", "US"}, 3, 0)
	two := obs([]string{"DE", "JP"}, 2, 0)
	p := pol(func(p *GeoAnomalyPolicy) { p.MaxPlaces = 2; p.FlagAfterPolls = 1 })

	if got := EvaluateGeo(p, two, GeoStreak{}).State; got != GeoStateClean {
		t.Fatalf("two places with tolerance 2: got %s, want clean", got)
	}
	if got := EvaluateGeo(p, three, GeoStreak{}).State; got != GeoStateFlagged {
		t.Fatalf("three places with tolerance 2: got %s, want flagged", got)
	}
}

// A tolerance of 0 would flag every connected user including one sitting at
// home. A misconfiguration must degrade into silence, not into accusing the
// whole fleet.
func TestEvaluate_ZeroToleranceIsRepairedNotObeyed(t *testing.T) {
	p := pol(func(p *GeoAnomalyPolicy) { p.MaxPlaces = 0; p.FlagAfterPolls = 1 })
	if got := EvaluateGeo(p, obs([]string{"JP"}, 1, 0), GeoStreak{}).State; got != GeoStateClean {
		t.Fatalf("one place must never be an anomaly: got %s", got)
	}
}

// ----------------------------------------------------------------- exemption

// The travelling account, the shared team credential, the operator's own test
// user. Excludable outright rather than by raising everyone's threshold.
func TestEvaluate_AllowAnywhereIsExemptNotClean(t *testing.T) {
	p := pol(func(p *GeoAnomalyPolicy) { p.AllowAnywhere = true })
	v := EvaluateGeo(p, obs([]string{"DE", "JP", "US"}, 3, 0), GeoStreak{Over: 9, Flagged: true})
	if v.State != GeoStateExempt {
		t.Fatalf("state = %s, want exempt — and NOT clean, which would read as evidence", v.State)
	}
	if v.Streak.Flagged || v.Streak.Over != 0 {
		t.Fatal("an exemption resets the streak so removing it later starts from a clean slate")
	}
}

// Off is a deliberate choice and must not be reported as Unknown, which means
// "I tried and could not tell".
func TestEvaluate_ScopeOffReportsDisabledNotUnknown(t *testing.T) {
	p := pol(func(p *GeoAnomalyPolicy) { p.Scope = GeoScopeOff })
	if got := EvaluateGeo(p, obs([]string{"DE", "JP"}, 2, 0), GeoStreak{}).State; got != GeoStateDisabled {
		t.Fatalf("state = %s, want disabled", got)
	}
}

// ------------------------------------------------------------------- unknown

// A stale or partial database must not read as a clean fleet.
func TestEvaluate_TooFewPlaceableAddressesIsUnknown(t *testing.T) {
	p := pol(func(p *GeoAnomalyPolicy) { p.MinPlacedRatio = 0.5; p.FlagAfterPolls = 1 })
	// 1 of 4 placed = 25%, below the 50% required.
	v := EvaluateGeo(p, obs([]string{"JP"}, 1, 3), GeoStreak{})
	if v.State != GeoStateUnknown {
		t.Fatalf("state = %s, want unknown", v.State)
	}
	if !strings.Contains(v.Reason, "1 of 4") {
		t.Fatalf("the reason must show the sample it refused to judge, got %q", v.Reason)
	}
}

func TestEvaluate_GeoUnavailableIsUnknown(t *testing.T) {
	o := obs([]string{"DE", "JP"}, 2, 0)
	o.GeoAvailable = false
	if got := EvaluateGeo(pol(nil), o, GeoStreak{}).State; got != GeoStateUnknown {
		t.Fatalf("state = %s, want unknown", got)
	}
}

// Unknown must not silently drain a latched flag either — the account is not
// cleared by the database breaking.
func TestEvaluate_UnknownDoesNotClearAFlag(t *testing.T) {
	unknown := GeoObservation{UserID: 7, Placed: 0, Unplaced: 3, GeoAvailable: false}
	v := EvaluateGeo(pol(nil), unknown, GeoStreak{Flagged: true, Under: 0})
	if !v.Streak.Flagged {
		t.Fatal("a broken database must not clear an existing flag")
	}
}

// ---------------------------------------------------------------- actionable

// Only Flagged may drive an automatic response. Suspect is deliberately below
// the line, and every other state means the detector cannot judge.
func TestGeoState_OnlyFlaggedIsActionable(t *testing.T) {
	for _, s := range []GeoState{
		GeoStateDisabled, GeoStateExempt, GeoStateUnknown,
		GeoStateIdle, GeoStateClean, GeoStateSuspect,
	} {
		if s.Actionable() {
			t.Fatalf("%s must not be actionable", s)
		}
	}
	if !GeoStateFlagged.Actionable() {
		t.Fatal("flagged must be actionable or the feature does nothing")
	}
}

// A verdict is shown to a human deciding about somebody's account. It has to
// carry the numbers that produced it.
func TestEvaluate_ReasonNamesTheNumbersBehindTheVerdict(t *testing.T) {
	two := obs([]string{"DE", "JP"}, 2, 0)
	p := pol(func(p *GeoAnomalyPolicy) { p.FlagAfterPolls = 2 })
	vs := sequence(p, two, two)
	r := vs[1].Reason
	for _, want := range []string{"DE", "JP", "tolerance is 1"} {
		if !strings.Contains(r, want) {
			t.Fatalf("reason %q must mention %q", r, want)
		}
	}
}

// ------------------------------------------------------------------- tiers

// tierCountries are sorted, so a Places slice cut from them is already in the
// order ObserveGeo produces.
var tierCountries = []string{"CN", "DE", "FR", "JP", "US"}

// tiers is one sample with the given spread: `countries` distinct countries,
// and `regions` / `cities` inside the widest one (CN). Every address placed.
func tiers(countries, regions, cities int) GeoObservation {
	o := GeoObservation{
		UserID:        7,
		GeoAvailable:  true,
		Places:        append([]string(nil), tierCountries[:countries]...),
		RegionSpread:  regions,
		RegionCountry: "CN",
		CitySpread:    cities,
		CityCountry:   "CN",
	}
	for i := 0; i < regions; i++ {
		o.Regions = append(o.Regions, fmt.Sprintf("CN/R%d", i))
	}
	for i := 0; i < cities; i++ {
		o.Cities = append(o.Cities, fmt.Sprintf("CN/C%d", i))
	}
	o.Placed = max(regions, cities, 1) + countries - 1
	return o
}

// observeAt runs one poll's addresses through the real projection, so the
// tests named after a person exercise the same path the poll does.
func observeAt(p GeoAnomalyPolicy, at map[string]GeoLocation) GeoObservation {
	list := make([]string, 0, len(at))
	for ip := range at {
		list = append(list, ip)
	}
	return ObserveGeo(p, ips(list...), lookupOf(at), true)
}

func tiersOf(vs []GeoVerdict) []GeoTier {
	out := make([]GeoTier, 0, len(vs))
	for _, v := range vs {
		out = append(out, v.Streak.Tier)
	}
	return out
}

func repeat(o GeoObservation, n int) []GeoObservation {
	out := make([]GeoObservation, n)
	for i := range out {
		out[i] = o
	}
	return out
}

// Home broadband in Shenzhen, the phone's carrier exit in Guangzhou: two
// cities of one province, every day, for every subscriber with a phone. The
// shipped default judges down to cities and must read this as clean — and
// say that it looked at the cities, not only the country.
func TestEvaluate_HomeAndPhoneInOneProvinceIsClean(t *testing.T) {
	p := DefaultGeoPolicy()
	home := observeAt(p, map[string]GeoLocation{
		"1.1.1.1": geoAt("CN", "Guangdong", "Shenzhen"),
		"2.2.2.2": geoAt("CN", "Guangdong", "Guangzhou"),
	})
	vs := sequence(p, repeat(home, 6)...)
	for i, v := range vs {
		if v.State != GeoStateClean {
			t.Fatalf("poll %d: state = %s (%s), want clean", i+1, v.State, v.Reason)
		}
	}
	for _, want := range []string{"2 city(ies)", "(scope city)"} {
		if !strings.Contains(vs[0].Reason, want) {
			t.Fatalf("reason %q must mention %q", vs[0].Reason, want)
		}
	}
}

// Two provinces at once is the D1 signal: a home router in Guangdong and a
// second device in Hunan. The default flags it at the REGION tier after the
// usual ramp.
func TestEvaluate_TwoProvincesFlagsAtRegionTierByDefault(t *testing.T) {
	p := DefaultGeoPolicy()
	two := observeAt(p, map[string]GeoLocation{
		"1.1.1.1": geoAt("CN", "Guangdong", "Shenzhen"),
		"2.2.2.2": geoAt("CN", "Hunan", "Changsha"),
	})
	vs := sequence(p, two, two, two)
	if got, want := states(vs), []GeoState{GeoStateSuspect, GeoStateSuspect, GeoStateFlagged}; !reflect.DeepEqual(got, want) {
		t.Fatalf("states = %v, want %v", got, want)
	}
	if got := tiersOf(vs); !reflect.DeepEqual(got, []GeoTier{GeoTierRegion, GeoTierRegion, GeoTierRegion}) {
		t.Fatalf("tiers = %v, want region throughout", got)
	}
	if !strings.Contains(vs[2].Reason, "in 2 regions of CN at once") {
		t.Fatalf("reason %q must name the regions of CN", vs[2].Reason)
	}
}

// The city tolerance is 2, not 1: at city scope, two cities is within it.
func TestEvaluate_TwoCitiesIsWithinTheDefaultCityTolerance(t *testing.T) {
	p := pol(func(p *GeoAnomalyPolicy) { p.Scope = GeoScopeCity; p.FlagAfterPolls = 1 })
	o := observeAt(p, map[string]GeoLocation{
		"1.1.1.1": geoAt("CN", "Guangdong", "Shenzhen"),
		"2.2.2.2": geoAt("CN", "Guangdong", "Guangzhou"),
	})
	if v := EvaluateGeo(p, o, GeoStreak{}); v.State != GeoStateClean {
		t.Fatalf("state = %s (%s), want clean — two cities is within the default tolerance", v.State, v.Reason)
	}
}

// And the third city at once is over it — at the city tier, with the reason
// naming the cities and the tolerance.
func TestEvaluate_ThreeCitiesInOneCountryIsOverAtCityTier(t *testing.T) {
	p := DefaultGeoPolicy()
	o := observeAt(p, map[string]GeoLocation{
		"1.1.1.1": geoAt("CN", "Guangdong", "Shenzhen"),
		"2.2.2.2": geoAt("CN", "Guangdong", "Guangzhou"),
		"3.3.3.3": geoAt("CN", "Guangdong", "Dongguan"),
	})
	v := EvaluateGeo(p, o, GeoStreak{})
	if v.State != GeoStateSuspect || v.Streak.Tier != GeoTierCity {
		t.Fatalf("state=%s tier=%q (%s), want suspect at the city tier", v.State, v.Streak.Tier, v.Reason)
	}
	for _, want := range []string{"in 3 cities of CN at once", "CN/Dongguan", "tolerance is 2", "1 of 3 checks so far"} {
		if !strings.Contains(v.Reason, want) {
			t.Fatalf("reason %q must mention %q", v.Reason, want)
		}
	}
}

// Country scope: nine cities in five provinces of one country are nothing,
// a second country is the country tier.
func TestEvaluate_CountryScopeNeverLooksBelowCountry(t *testing.T) {
	p := pol(func(p *GeoAnomalyPolicy) { p.Scope = GeoScopeCountry; p.FlagAfterPolls = 1 })
	if v := EvaluateGeo(p, tiers(1, 5, 9), GeoStreak{}); v.State != GeoStateClean || v.Streak.Tier != GeoTierNone {
		t.Fatalf("one country: state=%s tier=%q, want clean", v.State, v.Streak.Tier)
	}
	if v := EvaluateGeo(p, tiers(2, 5, 9), GeoStreak{}); v.State != GeoStateFlagged || v.Streak.Tier != GeoTierCountry {
		t.Fatalf("two countries: state=%s tier=%q, want flagged at the country tier", v.State, v.Streak.Tier)
	}
}

// Region scope: cities are not judged, regions are.
func TestEvaluate_RegionScopeDoesNotJudgeCities(t *testing.T) {
	p := pol(func(p *GeoAnomalyPolicy) { p.Scope = GeoScopeRegion; p.FlagAfterPolls = 1 })
	if v := EvaluateGeo(p, tiers(1, 1, 9), GeoStreak{}); v.State != GeoStateClean {
		t.Fatalf("nine cities of one region: state=%s (%s), want clean", v.State, v.Reason)
	}
	if v := EvaluateGeo(p, tiers(1, 2, 2), GeoStreak{}); v.State != GeoStateFlagged || v.Streak.Tier != GeoTierRegion {
		t.Fatalf("two regions: state=%s tier=%q, want flagged at the region tier", v.State, v.Streak.Tier)
	}
}

// One number per tier, each compared only against its own count.
func TestEvaluate_EachTierHasItsOwnTolerance(t *testing.T) {
	p := pol(func(p *GeoAnomalyPolicy) {
		p.Scope = GeoScopeCity
		p.MaxPlaces, p.MaxRegions, p.MaxCities = 2, 3, 4
		p.FlagAfterPolls = 1
	})
	cases := []struct {
		o    GeoObservation
		want GeoTier
	}{
		{tiers(2, 3, 4), GeoTierNone},
		{tiers(3, 3, 4), GeoTierCountry},
		{tiers(2, 4, 4), GeoTierRegion},
		{tiers(2, 3, 5), GeoTierCity},
	}
	for _, c := range cases {
		v := EvaluateGeo(p, c.o, GeoStreak{})
		if v.Streak.Tier != c.want {
			t.Fatalf("%d/%d/%d: tier=%q (%s), want %q", len(c.o.Places), c.o.RegionSpread, c.o.CitySpread,
				v.Streak.Tier, v.Reason, c.want)
		}
		if over := c.want != GeoTierNone; over != (v.State == GeoStateFlagged) {
			t.Fatalf("%d/%d/%d: state=%s", len(c.o.Places), c.o.RegionSpread, c.o.CitySpread, v.State)
		}
	}
}

// When several tiers are over at once, the verdict names the COARSEST: two
// countries is the stronger statement than the cities that come with them.
func TestEvaluate_CoarsestOverTierIsReported(t *testing.T) {
	p := pol(func(p *GeoAnomalyPolicy) { p.FlagAfterPolls = 1 })
	for _, c := range []struct {
		o    GeoObservation
		want GeoTier
	}{
		{tiers(2, 2, 3), GeoTierCountry},
		{tiers(1, 2, 3), GeoTierRegion},
		{tiers(1, 1, 3), GeoTierCity},
	} {
		if got := EvaluateGeo(p, c.o, GeoStreak{}).Streak.Tier; got != c.want {
			t.Fatalf("%d/%d/%d: tier=%q, want %q", len(c.o.Places), c.o.RegionSpread, c.o.CitySpread, got, c.want)
		}
	}
}

// One streak across all tiers. A sharer whose second device wanders from
// the next city to the next province to abroad has been over the whole time;
// restarting the ramp at each tier change would let them dodge the flag.
func TestEvaluate_TierChangeDoesNotResetTheStreak(t *testing.T) {
	vs := sequence(pol(nil), tiers(1, 1, 3), tiers(1, 2, 2), tiers(2, 1, 1))
	if got, want := states(vs), []GeoState{GeoStateSuspect, GeoStateSuspect, GeoStateFlagged}; !reflect.DeepEqual(got, want) {
		t.Fatalf("states = %v, want %v", got, want)
	}
	if got, want := tiersOf(vs), []GeoTier{GeoTierCity, GeoTierRegion, GeoTierCountry}; !reflect.DeepEqual(got, want) {
		t.Fatalf("tiers = %v, want %v", got, want)
	}
	if vs[2].Streak.Over != 3 {
		t.Fatalf("over = %d, want 3", vs[2].Streak.Over)
	}
}

// While latched, the verdict still says WHY the account was flagged. v1's
// latched text named neither a place nor a tier, so an operator looking at a
// latched row had nothing to act on.
func TestEvaluate_LatchedFlagNamesItsTier(t *testing.T) {
	p := pol(func(p *GeoAnomalyPolicy) { p.FlagAfterPolls = 1; p.ClearAfterPolls = 3 })
	clean := tiers(1, 1, 1)
	vs := sequence(p, tiers(1, 2, 2), clean, clean, clean)
	if got, want := states(vs), []GeoState{GeoStateFlagged, GeoStateFlagged, GeoStateFlagged, GeoStateClean}; !reflect.DeepEqual(got, want) {
		t.Fatalf("states = %v, want %v", got, want)
	}
	if got, want := tiersOf(vs), []GeoTier{GeoTierRegion, GeoTierRegion, GeoTierRegion, GeoTierNone}; !reflect.DeepEqual(got, want) {
		t.Fatalf("tiers = %v, want %v — kept while latched, cleared with the flag", got, want)
	}
	if want := "within tolerance for 1 of the 3 checks needed to clear; flagged at the region tier"; vs[1].Reason != want {
		t.Fatalf("latched reason = %q, want %q", vs[1].Reason, want)
	}
}

// An address the upstream still remembers but that was not live at poll
// time is not a connection. A poll with only such addresses is idle — and,
// like idle, it neither accrues nor sheds any streak, the ban streak
// included; otherwise a sharer clears a flag by pausing one device.
func TestEvaluate_StaleOnlyPollIsIdleAndDoesNotClearAFlag(t *testing.T) {
	p := pol(func(p *GeoAnomalyPolicy) { p.BanEnabled = true; p.ClearAfterPolls = 1 })
	prev := GeoStreak{Over: 4, Flagged: true, Tier: GeoTierRegion, BanOver: 2}
	v := EvaluateGeo(p, GeoObservation{UserID: 7, GeoAvailable: true, Stale: 5}, prev)
	if v.State != GeoStateIdle {
		t.Fatalf("state = %s, want idle", v.State)
	}
	if v.Streak != prev {
		t.Fatalf("streak = %+v, want frozen at %+v", v.Streak, prev)
	}
	if want := "no concurrent connections; 5 address(es) seen earlier in the upstream window"; v.Reason != want {
		t.Fatalf("reason = %q, want %q", v.Reason, want)
	}
}

// Every live address excluded (a relay, a shared exit, the ignore list) is
// not "nobody connected": somebody is, and PSP chose not to look. Reported as
// Unknown, with what was set aside.
func TestEvaluate_AllExcludedIsUnknownNotIdle(t *testing.T) {
	o := GeoObservation{UserID: 7, GeoAvailable: true, Excluded: GeoExcluded{Shared: 1, Infra: 2}}
	v := EvaluateGeo(pol(nil), o, GeoStreak{})
	if v.State != GeoStateUnknown {
		t.Fatalf("state = %s, want unknown", v.State)
	}
	want := "all 3 concurrent address(es) are excluded (shared 1, listed 0, infrastructure 2, internal 0); no conclusion drawn"
	if v.Reason != want {
		t.Fatalf("reason = %q, want %q", v.Reason, want)
	}
}

// And, like every Unknown, it freezes the streak: routing through a relay
// for a while must not wash out a flag.
func TestEvaluate_AllExcludedDoesNotClearAFlag(t *testing.T) {
	p := pol(func(p *GeoAnomalyPolicy) { p.ClearAfterPolls = 2; p.BanEnabled = true })
	prev := GeoStreak{Under: 1, Flagged: true, Tier: GeoTierCity, BanOver: 3}
	o := GeoObservation{UserID: 7, GeoAvailable: true, Excluded: GeoExcluded{Listed: 2}}
	vs := []GeoVerdict{EvaluateGeo(p, o, prev)}
	vs = append(vs, EvaluateGeo(p, o, vs[0].Streak))
	for i, v := range vs {
		if v.Streak != prev {
			t.Fatalf("sample %d: streak = %+v, want frozen at %+v", i+1, v.Streak, prev)
		}
	}
}

// ------------------------------------------------------------------- ban

// Automatic suspension is off unless an admin turns it on (D3): three
// countries at once, sustained, is a bell entry and nothing more.
func TestEvaluate_BanIsOffByDefault(t *testing.T) {
	for i, v := range sequence(DefaultGeoPolicy(), repeat(tiers(3, 3, 4), 20)...) {
		if v.BanDue || v.Streak.BanOver != 0 {
			t.Fatalf("poll %d: banDue=%v banOver=%d with the ban switched off", i+1, v.BanDue, v.Streak.BanOver)
		}
	}
}

// The ban has its own, looser tolerance (cities 3, so four cities) and its
// own, longer streak (6). On the sixth consecutive sample it is due — and
// the streak is CONSUMED, because the caller suspends, the user goes idle,
// idle freezes the streak, and an unconsumed streak would re-suspend on the
// first over-sample after the lift.
func TestEvaluate_BanDueAfterBanAfterPollsOverTheBanTolerance(t *testing.T) {
	p := pol(func(p *GeoAnomalyPolicy) { p.BanEnabled = true })
	vs := sequence(p, repeat(tiers(1, 1, 4), 6)...)
	for i, v := range vs[:5] {
		if v.BanDue || v.Streak.BanOver != i+1 {
			t.Fatalf("poll %d: banDue=%v banOver=%d, want not due and %d", i+1, v.BanDue, v.Streak.BanOver, i+1)
		}
	}
	last := vs[5]
	if !last.BanDue || last.BanTier != GeoTierCity || last.BanSpread != 4 {
		t.Fatalf("poll 6: banDue=%v tier=%q spread=%d, want due at the city tier with 4", last.BanDue, last.BanTier, last.BanSpread)
	}
	if last.Streak.BanOver != 0 {
		t.Fatalf("banOver = %d after a due ban, want 0 (consumed)", last.Streak.BanOver)
	}
}

// Over the flag tolerance but within the ban tolerance: flagged, in the bell,
// and never suspended however long it lasts.
func TestEvaluate_BetweenFlagAndBanToleranceFlagsButNeverBans(t *testing.T) {
	p := pol(func(p *GeoAnomalyPolicy) { p.BanEnabled = true })
	vs := sequence(p, repeat(tiers(1, 1, 3), 20)...)
	if vs[19].State != GeoStateFlagged {
		t.Fatalf("state = %s, want flagged — three cities is over the flag tolerance", vs[19].State)
	}
	for i, v := range vs {
		if v.BanDue || v.Streak.BanOver != 0 {
			t.Fatalf("poll %d: banDue=%v banOver=%d — three cities is within the ban tolerance", i+1, v.BanDue, v.Streak.BanOver)
		}
	}
}

// Consecutive means consecutive: one sample back under the ban tolerance
// restarts the count.
func TestEvaluate_BanStreakIsConsecutive(t *testing.T) {
	p := pol(func(p *GeoAnomalyPolicy) { p.BanEnabled = true; p.BanAfterPolls = 3; p.FlagAfterPolls = 1 })
	ban, flagOnly := tiers(1, 1, 4), tiers(1, 1, 3)
	vs := sequence(p, ban, ban, flagOnly, ban, ban, ban)
	for i, v := range vs[:5] {
		if v.BanDue {
			t.Fatalf("poll %d: ban due before 3 consecutive samples", i+1)
		}
	}
	if vs[2].Streak.BanOver != 0 {
		t.Fatalf("banOver = %d after a sample under the ban tolerance, want 0", vs[2].Streak.BanOver)
	}
	if !vs[5].BanDue {
		t.Fatal("the third consecutive ban-over sample must make the ban due")
	}
}

// Idle neither builds nor breaks the ban streak, for the same reason it
// cannot clear a flag.
func TestEvaluate_IdleFreezesTheBanStreak(t *testing.T) {
	p := pol(func(p *GeoAnomalyPolicy) { p.BanEnabled = true; p.BanAfterPolls = 3; p.FlagAfterPolls = 1 })
	ban, idle := tiers(1, 1, 4), GeoObservation{UserID: 7, GeoAvailable: true}
	vs := sequence(p, ban, ban, idle, idle, idle, ban)
	for i := 2; i <= 4; i++ {
		if vs[i].Streak.BanOver != 2 {
			t.Fatalf("idle poll %d: banOver = %d, want frozen at 2", i+1, vs[i].Streak.BanOver)
		}
	}
	if !vs[5].BanDue {
		t.Fatal("the third ban-over sample, idle polls between, must make the ban due")
	}
}

// Only a Flagged verdict can be acted on. A ban streak that fills while the
// flag is still ramping waits for the flag.
func TestEvaluate_BanRequiresAFlag(t *testing.T) {
	p := pol(func(p *GeoAnomalyPolicy) { p.BanEnabled = true; p.BanAfterPolls = 1; p.FlagAfterPolls = 3 })
	vs := sequence(p, repeat(tiers(2, 1, 1), 3)...)
	if got, want := states(vs), []GeoState{GeoStateSuspect, GeoStateSuspect, GeoStateFlagged}; !reflect.DeepEqual(got, want) {
		t.Fatalf("states = %v, want %v", got, want)
	}
	if vs[0].BanDue || vs[1].BanDue {
		t.Fatal("a Suspect verdict must never be due a ban")
	}
	if !vs[2].BanDue || vs[2].BanTier != GeoTierCountry {
		t.Fatalf("flagged: banDue=%v tier=%q, want due at the country tier", vs[2].BanDue, vs[2].BanTier)
	}
}

// Exempt and Disabled are "not judged". Anything accrued before must not be
// waiting when judging resumes.
func TestEvaluate_ExemptAndDisabledResetTheBanStreak(t *testing.T) {
	prev := GeoStreak{Over: 2, Flagged: true, Tier: GeoTierCity, BanOver: 5}
	for _, p := range []GeoAnomalyPolicy{
		pol(func(p *GeoAnomalyPolicy) { p.BanEnabled = true; p.AllowAnywhere = true }),
		pol(func(p *GeoAnomalyPolicy) { p.BanEnabled = true; p.Scope = GeoScopeOff }),
	} {
		v := EvaluateGeo(p, tiers(3, 3, 4), prev)
		if v.Streak != (GeoStreak{}) || v.BanDue {
			t.Fatalf("%s: streak=%+v banDue=%v, want a reset streak and no ban", v.State, v.Streak, v.BanDue)
		}
	}
}

// The ban's reason quotes the BAN tolerance and streak, not the flag's: it is
// what the audit row and the admin read when deciding whether the automatic
// suspension was right.
func TestEvaluate_BanReasonNamesTheBanTolerance(t *testing.T) {
	p := pol(func(p *GeoAnomalyPolicy) { p.BanEnabled = true; p.BanAfterPolls = 1; p.FlagAfterPolls = 1 })
	v := EvaluateGeo(p, tiers(1, 1, 4), GeoStreak{})
	want := "in 4 cities of CN at once ([CN/C0 CN/C1 CN/C2 CN/C3]); tolerance is 3, sustained for 1 of 1 checks"
	if v.BanReason != want {
		t.Fatalf("ban reason = %q, want %q", v.BanReason, want)
	}
	if !strings.Contains(v.Reason, "tolerance is 2") {
		t.Fatalf("the flag reason %q must still quote the flag tolerance", v.Reason)
	}
}
