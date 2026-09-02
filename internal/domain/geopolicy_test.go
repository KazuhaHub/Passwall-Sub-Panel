package domain

import (
	"reflect"
	"testing"
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

func ips(list ...string) UserLiveIPs { return UserLiveIPs{UserID: 7, IPs: list} }

// ---------------------------------------------------------------- granularity

// The default, and the reason it is the default: a commute, a carrier NAT
// pool and a mobile handoff all move a user between cities every day. Firing
// on that is how a detector becomes noise.
func TestObserve_CountryScopeIgnoresCityMovement(t *testing.T) {
	o := ObserveGeo(DefaultGeoPolicy(), ips("1.1.1.1", "1.1.1.2"),
		lookupOf(map[string]GeoLocation{
			"1.1.1.1": geoAt("JP", "Kanto", "Tokyo"),
			"1.1.1.2": geoAt("JP", "Kansai", "Osaka"),
		}), true)
	if !reflect.DeepEqual(o.Places, []string{"JP"}) {
		t.Fatalf("places = %v, want [JP] — two cities in one country is one place", o.Places)
	}
}

// Available for a deployment that knows its population is local, and only
// then. Same input, different verdict, purely from the knob.
func TestObserve_CityScopeSeesWhatCountryScopeIgnores(t *testing.T) {
	in := ips("1.1.1.1", "1.1.1.2")
	lk := lookupOf(map[string]GeoLocation{
		"1.1.1.1": geoAt("JP", "Kanto", "Tokyo"),
		"1.1.1.2": geoAt("JP", "Kansai", "Osaka"),
	})
	city := ObserveGeo(pol(func(p *GeoAnomalyPolicy) { p.Scope = GeoScopeCity }), in, lk, true)
	if len(city.Places) != 2 {
		t.Fatalf("city scope places = %v, want 2", city.Places)
	}
	country := ObserveGeo(DefaultGeoPolicy(), in, lk, true)
	if len(country.Places) != 1 {
		t.Fatalf("country scope places = %v, want 1", country.Places)
	}
}

func TestObserve_RegionScopeSitsBetween(t *testing.T) {
	o := ObserveGeo(pol(func(p *GeoAnomalyPolicy) { p.Scope = GeoScopeRegion }),
		ips("1.1.1.1", "1.1.1.2", "1.1.1.3"),
		lookupOf(map[string]GeoLocation{
			"1.1.1.1": geoAt("JP", "Kanto", "Tokyo"),
			"1.1.1.2": geoAt("JP", "Kanto", "Yokohama"), // same region
			"1.1.1.3": geoAt("JP", "Kansai", "Osaka"),
		}), true)
	if !reflect.DeepEqual(o.Places, []string{"JP/Kansai", "JP/Kanto"}) {
		t.Fatalf("places = %v, want the two regions", o.Places)
	}
}

// A database row that resolves a country but no city cannot answer a
// city-scoped policy. It must count as unplaced rather than becoming a place
// of its own — otherwise a coarse database manufactures the signal.
func TestObserve_CoarseRowIsUnplacedNotAPlaceOfItsOwn(t *testing.T) {
	o := ObserveGeo(pol(func(p *GeoAnomalyPolicy) { p.Scope = GeoScopeCity }),
		ips("1.1.1.1", "1.1.1.2"),
		lookupOf(map[string]GeoLocation{
			"1.1.1.1": geoAt("JP", "Kanto", "Tokyo"),
			"1.1.1.2": geoAt("JP", "", ""), // country only
		}), true)
	if len(o.Places) != 1 || o.Unplaced != 1 {
		t.Fatalf("places=%v placed=%d unplaced=%d, want 1 place and 1 unplaced", o.Places, o.Placed, o.Unplaced)
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

// ---------------------------------------------------------------- inheritance

// Field-wise layering, the same model the traffic and connection limits use.
// A user who may travel anywhere must not have to restate the scope and the
// hysteresis to say so.
func TestResolveGeoPolicy_UserOverridesOneFieldWithoutRestatingTheRest(t *testing.T) {
	base := DefaultGeoPolicy()
	yes := true
	got := ResolveGeoPolicy(base, GeoPolicyOverrides{}, GeoPolicyOverrides{AllowAnywhere: &yes})
	if !got.AllowAnywhere {
		t.Fatal("the user's override did not apply")
	}
	if got.Scope != base.Scope || got.FlagAfterPolls != base.FlagAfterPolls || got.ClearAfterPolls != base.ClearAfterPolls {
		t.Fatalf("untouched fields drifted: %+v vs base %+v", got, base)
	}
}

func TestResolveGeoPolicy_UserBeatsGroupBeatsDefault(t *testing.T) {
	city, region := GeoScopeCity, GeoScopeRegion
	three, five := 3, 5
	got := ResolveGeoPolicy(DefaultGeoPolicy(),
		GeoPolicyOverrides{Scope: &region, MaxPlaces: &three},
		GeoPolicyOverrides{Scope: &city},
	)
	if got.Scope != GeoScopeCity {
		t.Fatalf("scope = %s, want the user's city", got.Scope)
	}
	if got.MaxPlaces != 3 {
		t.Fatalf("maxPlaces = %d, want the group's 3 to survive an unrelated user override", got.MaxPlaces)
	}
	_ = five
}

// A group can be MORE permissive than the default, not only stricter — the
// point of inheritance is a policy, not a floor.
func TestResolveGeoPolicy_GroupMayLoosen(t *testing.T) {
	yes := true
	got := ResolveGeoPolicy(DefaultGeoPolicy(), GeoPolicyOverrides{AllowAnywhere: &yes}, GeoPolicyOverrides{})
	if !got.AllowAnywhere {
		t.Fatal("a group must be able to exempt its members")
	}
}

// An unrecognised scope must never behave like a permissive default silently;
// it is repaired to the documented default so the behaviour is knowable.
func TestResolveGeoPolicy_UnknownScopeIsRepaired(t *testing.T) {
	bad := GeoScope("continent")
	got := ResolveGeoPolicy(DefaultGeoPolicy(), GeoPolicyOverrides{}, GeoPolicyOverrides{Scope: &bad})
	if got.Scope != GeoScopeCountry {
		t.Fatalf("scope = %s, want repaired to country", got.Scope)
	}
}

// Out-of-range tolerances are repaired toward silence, never toward accusing.
func TestResolveGeoPolicy_OutOfRangeValuesDegradeSafely(t *testing.T) {
	zero, neg := 0, -3
	ratio := 9.0
	got := ResolveGeoPolicy(DefaultGeoPolicy(), GeoPolicyOverrides{},
		GeoPolicyOverrides{MaxPlaces: &zero, FlagAfterPolls: &neg, MinPlacedRatio: &ratio})
	if got.MaxPlaces < 1 || got.FlagAfterPolls < 1 || got.MinPlacedRatio > 1 {
		t.Fatalf("unusable policy was not repaired: %+v", got)
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
