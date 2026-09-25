package domain

import (
	"encoding/json"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"
)

func geoAt(cc, region, city string) GeoLocation {
	return GeoLocation{CountryCode: cc, Country: cc, Region: region, City: city}
}

func lookupOf(m map[string]GeoLocation) GeoLookup {
	return func(ips []string) map[string]GeoLocation {
		out := map[string]GeoLocation{}
		for _, ip := range ips {
			if g, ok := m[ip]; ok {
				out[ip] = g
			}
		}
		return out
	}
}

// ips runs the addresses through the same hygiene step the poll uses, with
// every exclusion off, so a projection test sees exactly the sources the
// poll would hand ObserveGeo — and nothing is silently dropped.
func ips(list ...string) UserAddresses {
	return ClassifyAddresses(map[int64]UserLiveIPs{7: {UserID: 7, IPs: list, Fresh: list}}, AddressExclusions{})[7]
}

// ---------------------------------------------------------------- granularity

// A country-scoped policy judges countries and nothing else. The observation
// still carries the finer spread — it is evidence an operator can read — but
// the verdict must not be moved by it: a commute, a carrier NAT pool and a
// mobile handoff all move a user between cities every day.
func TestObserve_CountryScopeIgnoresCityMovement(t *testing.T) {
	p := pol(func(p *GeoAnomalyPolicy) { p.Scope = GeoScopeCountry; p.FlagAfterPolls = 1 })
	o := ObserveGeo(p, ips("1.1.1.1", "1.1.1.2"),
		lookupOf(map[string]GeoLocation{
			"1.1.1.1": geoAt("JP", "Kanto", "Tokyo"),
			"1.1.1.2": geoAt("JP", "Kansai", "Osaka"),
		}), true)
	if !reflect.DeepEqual(o.Places, []string{"JP"}) {
		t.Fatalf("places = %v, want [JP] — Places is countries only", o.Places)
	}
	if o.CitySpread != 2 {
		t.Fatalf("city spread = %d, want 2 — the observation records it at every scope", o.CitySpread)
	}
	if v := EvaluateGeo(p, o, GeoStreak{}); v.State != GeoStateClean {
		t.Fatalf("state = %s (%s), want clean — country scope must not judge the cities", v.State, v.Reason)
	}
}

// Same input, different verdict, purely from the knob: three cities of one
// region are over the city tolerance (2) at city scope and invisible at
// country scope.
func TestObserve_CityScopeSeesWhatCountryScopeIgnores(t *testing.T) {
	in := ips("1.1.1.1", "1.1.1.2", "1.1.1.3")
	lk := lookupOf(map[string]GeoLocation{
		"1.1.1.1": geoAt("JP", "Kanto", "Tokyo"),
		"1.1.1.2": geoAt("JP", "Kanto", "Yokohama"),
		"1.1.1.3": geoAt("JP", "Kanto", "Chiba"),
	})
	city := pol(func(p *GeoAnomalyPolicy) { p.Scope = GeoScopeCity; p.FlagAfterPolls = 1 })
	o := ObserveGeo(city, in, lk, true)
	if o.CitySpread != 3 || o.RegionSpread != 1 {
		t.Fatalf("spread = %d regions / %d cities, want 1 / 3", o.RegionSpread, o.CitySpread)
	}
	v := EvaluateGeo(city, o, GeoStreak{})
	if v.State != GeoStateFlagged || v.Streak.Tier != GeoTierCity {
		t.Fatalf("city scope: state=%s tier=%q, want flagged at the city tier (%s)", v.State, v.Streak.Tier, v.Reason)
	}
	country := pol(func(p *GeoAnomalyPolicy) { p.Scope = GeoScopeCountry; p.FlagAfterPolls = 1 })
	if v := EvaluateGeo(country, ObserveGeo(country, in, lk, true), GeoStreak{}); v.State != GeoStateClean {
		t.Fatalf("country scope: state=%s, want clean", v.State)
	}
}

// Region scope judges countries and regions, not cities: two addresses in
// Kanto and one in Kansai are two regions, and the region tier is what the
// verdict names.
func TestObserve_RegionScopeSitsBetween(t *testing.T) {
	p := pol(func(p *GeoAnomalyPolicy) { p.Scope = GeoScopeRegion; p.FlagAfterPolls = 1 })
	o := ObserveGeo(p, ips("1.1.1.1", "1.1.1.2", "1.1.1.3"),
		lookupOf(map[string]GeoLocation{
			"1.1.1.1": geoAt("JP", "Kanto", "Tokyo"),
			"1.1.1.2": geoAt("JP", "Kanto", "Yokohama"), // same region
			"1.1.1.3": geoAt("JP", "Kansai", "Osaka"),
		}), true)
	if !reflect.DeepEqual(o.Regions, []string{"JP/Kansai", "JP/Kanto"}) {
		t.Fatalf("regions = %v, want the two regions", o.Regions)
	}
	if v := EvaluateGeo(p, o, GeoStreak{}); v.Streak.Tier != GeoTierRegion {
		t.Fatalf("tier = %q (%s), want region", v.Streak.Tier, v.Reason)
	}
}

// A row that resolves a country but no city still says which COUNTRY the
// address is in, so it counts there. What it must never do is become a city
// of its own: a coarse database would otherwise manufacture the city spread.
// (v1 dropped such a row as unplaced under a city policy, which also threw
// away the country it did know.)
func TestObserve_CoarseRowCountsForItsCountryNeverAsACity(t *testing.T) {
	p := pol(func(p *GeoAnomalyPolicy) { p.Scope = GeoScopeCity })
	o := ObserveGeo(p, ips("1.1.1.1", "1.1.1.2", "2.2.2.2"),
		lookupOf(map[string]GeoLocation{
			"1.1.1.1": geoAt("JP", "Kanto", "Tokyo"),
			"1.1.1.2": geoAt("JP", "", ""), // country only
			"2.2.2.2": geoAt("DE", "", ""), // country only
		}), true)
	if o.Placed != 3 || o.Unplaced != 0 {
		t.Fatalf("placed=%d unplaced=%d, want 3 / 0 — a known country is a placed address", o.Placed, o.Unplaced)
	}
	if !reflect.DeepEqual(o.Places, []string{"DE", "JP"}) {
		t.Fatalf("places = %v, want [DE JP]", o.Places)
	}
	if o.CitySpread != 1 || o.CityKnown != 1 || o.RegionKnown != 1 {
		t.Fatalf("cities=%d cityKnown=%d regionKnown=%d, want 1/1/1 — a coarse row is no city",
			o.CitySpread, o.CityKnown, o.RegionKnown)
	}
}

// The spread is measured inside ONE country — the widest — never summed
// across countries: two cities in Japan and two in Germany are two
// countries (the country tier's business) and two cities, not four.
func TestObserve_WidestCountryIsJudgedNotTheSum(t *testing.T) {
	o := ObserveGeo(pol(nil), ips("1.1.1.1", "1.1.1.2", "2.2.2.1", "2.2.2.2"),
		lookupOf(map[string]GeoLocation{
			"1.1.1.1": geoAt("JP", "Kanto", "Tokyo"),
			"1.1.1.2": geoAt("JP", "Kansai", "Osaka"),
			"2.2.2.1": geoAt("DE", "Berlin", "Berlin"),
			"2.2.2.2": geoAt("DE", "Bavaria", "Munich"),
		}), true)
	if o.CitySpread != 2 || o.RegionSpread != 2 {
		t.Fatalf("spread = %d regions / %d cities, want 2 / 2 (per country, not summed)", o.RegionSpread, o.CitySpread)
	}
	// A tie goes to the smallest code, so the named country is stable from
	// poll to poll.
	if o.CityCountry != "DE" || o.RegionCountry != "DE" {
		t.Fatalf("widest = %q / %q, want DE on a tie", o.RegionCountry, o.CityCountry)
	}
	if !reflect.DeepEqual(o.Cities, []string{"DE/Berlin", "DE/Munich"}) {
		t.Fatalf("cities = %v", o.Cities)
	}
}

// Spots are what an operator reads to see where somebody is. They are
// bounded, ordered most-occupied first, and carry no address: evidence is
// stored and shown, and an address in it would outlive the connection.
func TestObserve_SpotsAreCappedSortedAndAddressFree(t *testing.T) {
	at := map[string]GeoLocation{
		"9.9.9.1": geoAt("CN", "Guangdong", "Shenzhen"),
		"9.9.9.2": geoAt("CN", "Guangdong", "Shenzhen"),
		"9.9.9.3": geoAt("CN", "Guangdong", "Shenzhen"),
	}
	var list []string
	for ip := range at {
		list = append(list, ip)
	}
	for i := 0; i < 13; i++ {
		ip := "8.8.8." + strconv.Itoa(i+1)
		at[ip] = geoAt("JP", "R"+strconv.Itoa(i), "C"+strconv.Itoa(i))
		list = append(list, ip)
	}
	o := ObserveGeo(pol(nil), ips(list...), lookupOf(at), true)
	if len(o.Spots) != GeoEvidenceMaxSpots {
		t.Fatalf("spots = %d, want capped at %d", len(o.Spots), GeoEvidenceMaxSpots)
	}
	if o.Spots[0] != (GeoSpot{CC: "CN", Region: "Guangdong", City: "Shenzhen", N: 3}) {
		t.Fatalf("first spot = %+v, want the most-occupied one", o.Spots[0])
	}
	// Then by cc, region, city: JP/R0/C0, JP/R1/C1, JP/R10/C10, ...
	if o.Spots[1].Region != "R0" || o.Spots[2].Region != "R1" || o.Spots[3].Region != "R10" {
		t.Fatalf("spots not ordered by cc/region/city: %+v", o.Spots[1:4])
	}
	raw, _ := json.Marshal(o.Spots)
	for _, ip := range list {
		if strings.Contains(string(raw), ip) {
			t.Fatalf("spots carry the address %s: %s", ip, raw)
		}
	}
}

// Country codes are compared, folded and counted upper-cased: a database
// that returns "jp" must neither dodge a co-travel set typed as "JP" nor
// count as a second country next to a "JP" row.
func TestObserve_CountryCodesAreUppercasedBeforeCoTravel(t *testing.T) {
	p := pol(func(p *GeoAnomalyPolicy) { p.CoTravel = [][]string{{"JP", "TW"}} })
	o := ObserveGeo(p, ips("1.1.1.1", "2.2.2.2", "3.3.3.3"),
		lookupOf(map[string]GeoLocation{
			"1.1.1.1": geoAt("jp", "", "Tokyo"),
			"2.2.2.2": geoAt(" tw ", "", "Taipei"),
			"3.3.3.3": geoAt("JP", "", "Osaka"),
		}), true)
	if !reflect.DeepEqual(o.Places, []string{"JP"}) {
		t.Fatalf("places = %v, want [JP]", o.Places)
	}
}

// Without a country code nothing below it is a place: "Springfield" is not a
// location until you know which country.
func TestObserve_CityWithoutCountryIsUnplaced(t *testing.T) {
	o := ObserveGeo(DefaultGeoPolicy(), ips("1.1.1.1"),
		lookupOf(map[string]GeoLocation{"1.1.1.1": {City: "Springfield"}}), true)
	if o.Placed != 0 || o.Unplaced != 1 || len(o.Places) != 0 {
		t.Fatalf("placed=%d unplaced=%d places=%v", o.Placed, o.Unplaced, o.Places)
	}
}

// ---------------------------------------------------------------- co-travel

// The border commuter, or a user with a genuine dual presence. Naming the
// pair is more honest than raising their tolerance, because it stays
// specific.
func TestObserve_CoTravelSetFoldsAPairIntoOnePlace(t *testing.T) {
	p := pol(func(p *GeoAnomalyPolicy) { p.CoTravel = [][]string{{"JP", "TW"}} })
	o := ObserveGeo(p, ips("1.1.1.1", "2.2.2.2"),
		lookupOf(map[string]GeoLocation{
			"1.1.1.1": geoAt("JP", "", "Tokyo"),
			"2.2.2.2": geoAt("TW", "", "Taipei"),
		}), true)
	if len(o.Places) != 1 {
		t.Fatalf("places = %v, want the pair folded to one", o.Places)
	}
}

// And it stays specific: a third country outside the set still counts, so the
// exemption cannot be used as a blanket tolerance increase.
func TestObserve_CoTravelDoesNotExcuseAThirdPlace(t *testing.T) {
	p := pol(func(p *GeoAnomalyPolicy) { p.CoTravel = [][]string{{"JP", "TW"}} })
	o := ObserveGeo(p, ips("1.1.1.1", "2.2.2.2", "3.3.3.3"),
		lookupOf(map[string]GeoLocation{
			"1.1.1.1": geoAt("JP", "", "Tokyo"),
			"2.2.2.2": geoAt("TW", "", "Taipei"),
			"3.3.3.3": geoAt("DE", "", "Berlin"),
		}), true)
	if len(o.Places) != 2 {
		t.Fatalf("places = %v, want 2 (the folded pair plus DE)", o.Places)
	}
	if EvaluateGeo(pol(func(q *GeoAnomalyPolicy) {
		q.CoTravel = p.CoTravel
		q.FlagAfterPolls = 1
	}), o, GeoStreak{}).State != GeoStateFlagged {
		t.Fatal("a place outside the co-travel set must still be able to flag")
	}
}

// A one-member set excuses nothing.
//
// This pins the PROPERTY, not a line. No guard delivers it: folding a
// singleton maps its only place to itself, so it is already the identity.
// Verified by mutation — loosening the emptiness check to also process
// singletons leaves this green, which is correct rather than a gap. Recorded
// here so a later reader does not add a guard believing this test demands one.
func TestObserve_SingletonCoTravelSetIsIgnored(t *testing.T) {
	p := pol(func(p *GeoAnomalyPolicy) { p.CoTravel = [][]string{{"JP"}} })
	o := ObserveGeo(p, ips("1.1.1.1", "2.2.2.2"),
		lookupOf(map[string]GeoLocation{
			"1.1.1.1": geoAt("JP", "", "Tokyo"),
			"2.2.2.2": geoAt("DE", "", "Berlin"),
		}), true)
	if len(o.Places) != 2 {
		t.Fatalf("places = %v, want 2 — a singleton set excuses nothing", o.Places)
	}
}

// ---------------------------------------------------------------- sanitising

// Every repair goes toward NOT accusing. A misconfiguration — a typo, a
// cleared field, an older schema — must degrade into silence, never into a
// detector that flags or suspends a whole fleet.
func TestSanitized_RepairsTowardNotAccusing(t *testing.T) {
	got := GeoAnomalyPolicy{
		Scope:     "continent",
		MaxPlaces: 0, MaxRegions: -1, MaxCities: 0,
		FlagAfterPolls: 0, ClearAfterPolls: -2,
		MinPlacedRatio: 9,
		// Ban tolerances BELOW the flag ones would let a sample that is not
		// even over the flag line count toward a suspension.
		BanMaxCountries: 0, BanMaxRegions: 0, BanMaxCities: 1,
		BanAfterPolls:      0,
		BanDurationMinutes: GeoBanMaxDurationMinutes + 1,
	}.sanitized()
	want := GeoAnomalyPolicy{
		// An unknown scope judges the coarsest tier, never the finest.
		Scope:     GeoScopeCountry,
		MaxPlaces: 1, MaxRegions: 1, MaxCities: 1,
		FlagAfterPolls: 1, ClearAfterPolls: 1,
		MinPlacedRatio:  1,
		BanMaxCountries: 1, BanMaxRegions: 1, BanMaxCities: 1,
		// Deliberately the shipped default (6), not 1: a zeroed field must
		// not turn one sample into a suspension.
		BanAfterPolls:      6,
		BanDurationMinutes: GeoBanMaxDurationMinutes,
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("sanitized = %+v\nwant        %+v", got, want)
	}

	// Ban tolerances are raised to the flag ones tier by tier, so being over
	// a ban tolerance always implies being over the flag tolerance.
	got = GeoAnomalyPolicy{
		Scope: GeoScopeCity, MaxPlaces: 2, MaxRegions: 3, MaxCities: 5,
		BanMaxCountries: 1, BanMaxRegions: 4, BanMaxCities: 2,
		BanDurationMinutes: -5,
	}.sanitized()
	if got.BanMaxCountries != 2 || got.BanMaxRegions != 4 || got.BanMaxCities != 5 {
		t.Fatalf("ban tolerances = %d/%d/%d, want 2/4/5", got.BanMaxCountries, got.BanMaxRegions, got.BanMaxCities)
	}
	if got.BanDurationMinutes != 1 {
		t.Fatalf("ban duration = %d, want clamped up to 1 minute", got.BanDurationMinutes)
	}
}

// The tolerances a verdict is judged against, per tier, and the ban's time
// box as a duration — read from the sanitised policy so a caller holding a
// raw one cannot schedule a zero-length or unbounded suspension.
func TestGeoPolicy_TolerancesAndDuration(t *testing.T) {
	p := GeoAnomalyPolicy{
		Scope: GeoScopeCity, MaxPlaces: 2, MaxRegions: 3, MaxCities: 4,
		BanMaxCountries: 5, BanMaxRegions: 6, BanMaxCities: 7, BanDurationMinutes: 90,
	}
	if got := p.FlagTolerances(); got != (GeoTolerances{Countries: 2, Regions: 3, Cities: 4}) {
		t.Fatalf("flag tolerances = %+v", got)
	}
	if got := p.BanTolerances(); got != (GeoTolerances{Countries: 5, Regions: 6, Cities: 7}) {
		t.Fatalf("ban tolerances = %+v", got)
	}
	if got := p.BanDuration(); got != 90*time.Minute {
		t.Fatalf("ban duration = %v, want 90m", got)
	}
	p.BanDurationMinutes = 0
	if got := p.BanDuration(); got != time.Minute {
		t.Fatalf("ban duration of an unsanitised zero = %v, want the 1-minute floor", got)
	}
	p.BanDurationMinutes = 1 << 30
	if got := p.BanDuration(); got != GeoBanMaxDurationMinutes*time.Minute {
		t.Fatalf("ban duration = %v, want capped at %d minutes", got, GeoBanMaxDurationMinutes)
	}
}

// Scope off short-circuits the projection too, so no lookup is performed for
// a principal nobody is judging.
func TestObserve_ScopeOffPlacesNothing(t *testing.T) {
	called := false
	lk := GeoLookup(func([]string) map[string]GeoLocation {
		called = true
		return nil
	})
	o := ObserveGeo(pol(func(p *GeoAnomalyPolicy) { p.Scope = GeoScopeOff }), ips("1.1.1.1"), lk, true)
	if called {
		t.Fatal("scope off must not query the geo database at all")
	}
	if o.Unplaced != 1 || len(o.Places) != 0 {
		t.Fatalf("unplaced=%d places=%v", o.Unplaced, o.Places)
	}
}

// The one thing the emptiness check actually averts: an empty set would index
// past the end when picking a representative.
func TestObserve_EmptyCoTravelSetDoesNotPanic(t *testing.T) {
	p := pol(func(p *GeoAnomalyPolicy) { p.CoTravel = [][]string{{}, {"  ", ""}, {"JP", "TW"}} })
	o := ObserveGeo(p, ips("1.1.1.1", "2.2.2.2"),
		lookupOf(map[string]GeoLocation{
			"1.1.1.1": geoAt("JP", "", "Tokyo"),
			"2.2.2.2": geoAt("TW", "", "Taipei"),
		}), true)
	if len(o.Places) != 1 {
		t.Fatalf("places = %v; empty sets must be skipped and the real pair still folded", o.Places)
	}
}
