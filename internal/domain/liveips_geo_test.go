package domain

import (
	"reflect"
	"testing"
)

func fixedGeo(m map[string]GeoLocation) GeoLookup {
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

func at(cc, city string) GeoLocation {
	return GeoLocation{CountryCode: cc, Country: cc, City: city}
}

func live(uid int64, ips ...string) UserLiveIPs {
	return UserLiveIPs{UserID: uid, IPs: ips}
}

// The signal this feature is built on.
func TestSpread_TwoCountriesAtOnceIsSuspicious(t *testing.T) {
	s := SpreadOf(live(7, "1.1.1.1", "2.2.2.2"),
		fixedGeo(map[string]GeoLocation{"1.1.1.1": at("JP", "Tokyo"), "2.2.2.2": at("DE", "Berlin")}), true)
	if !s.Suspicious() {
		t.Fatalf("two countries at once must flag: %+v", s)
	}
	if !reflect.DeepEqual(s.Countries, []string{"DE", "JP"}) {
		t.Fatalf("countries = %v, want [DE JP] sorted", s.Countries)
	}
}

// The false-positive this design exists to avoid. Several addresses in one
// country is the normal shape of a household, a phone changing networks, or
// a carrier's NAT pool — and it is exactly what a raw IP count punishes.
func TestSpread_ManyIPsOneCountryIsNotSuspicious(t *testing.T) {
	s := SpreadOf(live(7, "1.1.1.1", "1.1.1.2", "1.1.1.3", "1.1.1.4"),
		fixedGeo(map[string]GeoLocation{
			"1.1.1.1": at("JP", "Tokyo"), "1.1.1.2": at("JP", "Osaka"),
			"1.1.1.3": at("JP", "Tokyo"), "1.1.1.4": at("JP", "Nagoya"),
		}), true)
	if s.Suspicious() {
		t.Fatalf("four addresses in one country is ordinary, not a flag: %+v", s)
	}
	if len(s.Cities) != 3 {
		t.Fatalf("cities are still reported for context: got %v", s.Cities)
	}
}

// Multiple cities inside one country must never escalate on their own: a
// commute or a mobile carrier produces it every day.
func TestSpread_CitiesAloneNeverEscalate(t *testing.T) {
	s := SpreadOf(live(7, "1.1.1.1", "1.1.1.2"),
		fixedGeo(map[string]GeoLocation{"1.1.1.1": at("JP", "Tokyo"), "1.1.1.2": at("JP", "Fukuoka")}), true)
	if s.Suspicious() {
		t.Fatal("two cities in one country is not the signal")
	}
}

// The state that must not collapse into "no anomaly". With geo off, the
// honest answer is unknown; a detector that silently stops detecting is the
// same failure as a cap that silently stops capping.
func TestSpread_GeoUnavailableIsUnknownNotClean(t *testing.T) {
	s := SpreadOf(live(7, "1.1.1.1", "2.2.2.2"),
		fixedGeo(map[string]GeoLocation{"1.1.1.1": at("JP", "Tokyo"), "2.2.2.2": at("DE", "Berlin")}), false)
	if s.Suspicious() {
		t.Fatal("no conclusion may be drawn with geo unavailable")
	}
	if s.GeoAvailable {
		t.Fatal("GeoAvailable must stay false so the caller can say why")
	}
	if s.Unlocated != 2 || s.Located != 0 {
		t.Fatalf("with geo off every IP is unlocated: got located=%d unlocated=%d", s.Located, s.Unlocated)
	}
}

// A stale or partial database must not read as a clean fleet. The IPs it
// cannot place stay visible instead of quietly leaving the denominator.
func TestSpread_UnresolvableIPsAreCountedNotDropped(t *testing.T) {
	s := SpreadOf(live(7, "1.1.1.1", "10.0.0.1"),
		fixedGeo(map[string]GeoLocation{"1.1.1.1": at("JP", "Tokyo")}), true)
	if s.Located != 1 || s.Unlocated != 1 {
		t.Fatalf("got located=%d unlocated=%d, want 1/1", s.Located, s.Unlocated)
	}
	if s.Suspicious() {
		t.Fatal("one placed country plus one unknown is not two countries")
	}
}

// A location with a city but no country code cannot contribute to the signal,
// so it must count as unlocated rather than become a country of its own —
// otherwise a partial database manufactures a second "country" and flags an
// innocent user.
func TestSpread_LocationWithoutCountryCodeIsUnlocated(t *testing.T) {
	s := SpreadOf(live(7, "1.1.1.1", "2.2.2.2"),
		fixedGeo(map[string]GeoLocation{
			"1.1.1.1": at("JP", "Tokyo"),
			"2.2.2.2": {City: "Nowhere"},
		}), true)
	if s.Suspicious() {
		t.Fatalf("a countryless location must not become a second country: %+v", s)
	}
	if s.Unlocated != 1 {
		t.Fatalf("unlocated = %d, want 1", s.Unlocated)
	}
}

// An idle user resolves to nothing at all rather than to a flag.
func TestSpread_NoLiveIPsIsNotSuspicious(t *testing.T) {
	s := SpreadOf(live(7), fixedGeo(nil), true)
	if s.Suspicious() || len(s.Countries) != 0 {
		t.Fatalf("idle user: %+v", s)
	}
}

// A nil lookup is a wiring mistake, not evidence of innocence.
func TestSpread_NilLookupDrawsNoConclusion(t *testing.T) {
	s := SpreadOf(live(7, "1.1.1.1", "2.2.2.2"), nil, true)
	if s.Suspicious() {
		t.Fatal("a missing lookup must not read as a clean result")
	}
	if s.Unlocated != 2 {
		t.Fatalf("unlocated = %d, want 2", s.Unlocated)
	}
}

// The guard inside Suspicious itself, reached by constructing the struct
// directly rather than through SpreadOf.
//
// SpreadOf cannot produce this shape — with geo off it returns no countries,
// so the guard is unreachable that way and a mutation removing it survives
// every test above. But LiveIPSpread is exported with exported fields and is
// serialized into the admin API and persisted, so a record read back after
// geo was switched off arrives exactly like this: countries from when the
// database still worked, GeoAvailable now false. Acting on it would accuse
// someone on evidence the system has stopped being able to check.
func TestSpread_StoredCountriesDoNotOutliveGeoAvailability(t *testing.T) {
	stale := LiveIPSpread{
		UserID:       7,
		Countries:    []string{"DE", "JP"},
		GeoAvailable: false,
	}
	if stale.Suspicious() {
		t.Fatal("countries recorded while geo worked must not be acted on after it stopped")
	}

	// And the same value with geo available still flags, so the guard is
	// narrowing the conclusion rather than disabling the feature.
	stale.GeoAvailable = true
	if !stale.Suspicious() {
		t.Fatal("the guard must not suppress a live two-country result")
	}
}
