package domain

import (
	"reflect"
	"testing"
)

// A fresh install has empty settings. Reading them literally gives MaxPlaces 0
// and FlagAfterPolls 0 — which flags EVERY connected user on their first poll,
// including one sitting at home. Zero means "never configured", and this is
// the single place that distinction is made.
//
// Compared field by field over the WHOLE policy, so a knob added later that
// forgets its "0 means default" guard fails here rather than shipping as a
// zero tolerance or a zero-minute ban.
func TestGeoPolicyFromSettings_UnsetFallsBackToTheDefaultNotToZero(t *testing.T) {
	got := GeoPolicyFromSettings(GeoPolicySettings{})
	want := DefaultGeoPolicy()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("empty settings = %+v\nwant the shipped default %+v", got, want)
	}
}

// And a configured value must actually take effect, or "tunable" is a claim
// rather than a fact.
func TestGeoPolicyFromSettings_ConfiguredValuesApply(t *testing.T) {
	got := GeoPolicyFromSettings(GeoPolicySettings{
		Scope: "city", MaxPlaces: 4, FlagAfterPolls: 7, ClearAfterPolls: 9, MinPlacedRatio: 0.9,
	})
	if got.Scope != GeoScopeCity || got.MaxPlaces != 4 || got.FlagAfterPolls != 7 ||
		got.ClearAfterPolls != 9 || got.MinPlacedRatio != 0.9 {
		t.Fatalf("configured policy did not apply: %+v", got)
	}
}

// Scope is typed by a human into a form. Accepting mixed case and stray
// whitespace is the difference between a working setting and one that
// silently reverts to the default with no way to tell.
//
// Region, not city: city is the default, so a city input would pass even if
// the parse ignored it entirely.
func TestGeoPolicyFromSettings_ScopeIsCaseAndSpaceTolerant(t *testing.T) {
	for _, in := range []string{"Region", " REGION ", "region"} {
		if got := GeoPolicyFromSettings(GeoPolicySettings{Scope: in}); got.Scope != GeoScopeRegion {
			t.Fatalf("scope %q resolved to %s, want region", in, got.Scope)
		}
	}
}

// An unrecognised scope — a typo, or a value from some other build — falls
// back to COUNTRY, the coarsest tier, and deliberately not to the default:
// the default is the finest tier, and a misconfiguration must never make a
// policy judge more finely than the admin meant.
func TestGeoPolicyFromSettings_UnknownScopeFallsBackToCountry(t *testing.T) {
	if got := GeoPolicyFromSettings(GeoPolicySettings{Scope: "continent"}); got.Scope != GeoScopeCountry {
		t.Fatalf("scope = %s, want country", got.Scope)
	}
}

// "off" is a real, valid choice and must survive — it is how an admin turns
// detection off for a group without deleting the rest of the policy.
func TestGeoPolicyFromSettings_OffIsAValidChoice(t *testing.T) {
	if got := GeoPolicyFromSettings(GeoPolicySettings{Scope: "off"}); got.Scope != GeoScopeOff {
		t.Fatalf("scope = %s, want off", got.Scope)
	}
}

// AllowAnywhere has no "unset": false IS the default, so a group setting it
// true must carry through verbatim rather than being treated as a zero.
func TestGeoPolicyFromSettings_AllowAnywhereCarriesThrough(t *testing.T) {
	if !GeoPolicyFromSettings(GeoPolicySettings{AllowAnywhere: true}).AllowAnywhere {
		t.Fatal("a group must be able to exempt its members")
	}
	if GeoPolicyFromSettings(GeoPolicySettings{}).AllowAnywhere {
		t.Fatal("exemption must not be the default")
	}
}

// Co-travel sets are typed as "JP,TW". Country codes are compared
// upper-cased, so a set typed lowercase that silently never matched would be
// indistinguishable from one that was ignored — and an admin would have no
// way to tell which.
func TestGeoPolicyFromSettings_CoTravelIsParsedAndUppercased(t *testing.T) {
	got := GeoPolicyFromSettings(GeoPolicySettings{CoTravel: " jp , tw \ndE,at,ch"})
	want := [][]string{{"JP", "TW"}, {"DE", "AT", "CH"}}
	if !reflect.DeepEqual(got.CoTravel, want) {
		t.Fatalf("co-travel = %v, want %v", got.CoTravel, want)
	}
}

// And the parsed set must actually fold, end to end — the parse is only
// worth anything if the policy it produces behaves.
func TestGeoPolicyFromSettings_ParsedCoTravelActuallyFolds(t *testing.T) {
	p := GeoPolicyFromSettings(GeoPolicySettings{CoTravel: "jp,tw"})
	o := ObserveGeo(p, ips("1.1.1.1", "2.2.2.2"),
		lookupOf(map[string]GeoLocation{
			"1.1.1.1": geoAt("JP", "", "Tokyo"),
			"2.2.2.2": geoAt("TW", "", "Taipei"),
		}), true)
	if len(o.Places) != 1 {
		t.Fatalf("places = %v; a co-travel set typed in lowercase must still fold", o.Places)
	}
}

// Empty and whitespace-only entries produce no set rather than an empty one
// that would reach the fold and index past the end.
func TestGeoPolicyFromSettings_BlankCoTravelEntriesAreDropped(t *testing.T) {
	got := GeoPolicyFromSettings(GeoPolicySettings{CoTravel: "\n  \n , , "})
	if len(got.CoTravel) != 0 {
		t.Fatalf("co-travel = %v, want none", got.CoTravel)
	}
}

// Out-of-range stored values are repaired toward NOT accusing.
func TestGeoPolicyFromSettings_OutOfRangeRatioIsClamped(t *testing.T) {
	if got := GeoPolicyFromSettings(GeoPolicySettings{MinPlacedRatio: 5}); got.MinPlacedRatio > 1 {
		t.Fatalf("ratio = %v, want clamped to 1", got.MinPlacedRatio)
	}
}

// ---------------------------------------------------------------- v2 tiers

// The shipped default judges down to the city tier (D1): countries 1,
// regions 1, cities 2. City is safe as a default only because each tier has
// its own tolerance — home broadband and a phone's carrier exit are two
// cities of one province, which the city tolerance of 2 absorbs.
func TestGeoPolicyFromSettings_DefaultScopeIsCity(t *testing.T) {
	d := DefaultGeoPolicy()
	if d.Scope != GeoScopeCity || d.MaxPlaces != 1 || d.MaxRegions != 1 || d.MaxCities != 2 {
		t.Fatalf("default = scope %s, tolerances %d/%d/%d; want city, 1/1/2",
			d.Scope, d.MaxPlaces, d.MaxRegions, d.MaxCities)
	}
	if got := GeoPolicyFromSettings(GeoPolicySettings{}).Scope; got != GeoScopeCity {
		t.Fatalf("unset scope = %s, want city", got)
	}
}

// An empty (or blank) stored scope is "never configured", which is the
// default — not the unknown-value fallback.
func TestGeoPolicyFromSettings_EmptyScopeIsTheDefault(t *testing.T) {
	for _, in := range []string{"", "   ", "\t"} {
		if got := GeoPolicyFromSettings(GeoPolicySettings{Scope: in}).Scope; got != DefaultGeoPolicy().Scope {
			t.Fatalf("scope %q = %s, want the default %s", in, got, DefaultGeoPolicy().Scope)
		}
	}
}

func TestGeoPolicyFromSettings_TierTolerancesApply(t *testing.T) {
	got := GeoPolicyFromSettings(GeoPolicySettings{MaxPlaces: 2, MaxRegions: 4, MaxCities: 6})
	if got.MaxPlaces != 2 || got.MaxRegions != 4 || got.MaxCities != 6 {
		t.Fatalf("tolerances = %d/%d/%d, want 2/4/6", got.MaxPlaces, got.MaxRegions, got.MaxCities)
	}
}

func TestGeoPolicyFromSettings_BanSettingsApply(t *testing.T) {
	got := GeoPolicyFromSettings(GeoPolicySettings{
		BanEnabled:      true,
		BanMaxCountries: 2, BanMaxRegions: 3, BanMaxCities: 4,
		BanAfterPolls: 8, BanDurationMinutes: 90,
	})
	if !got.BanEnabled || got.BanMaxCountries != 2 || got.BanMaxRegions != 3 || got.BanMaxCities != 4 ||
		got.BanAfterPolls != 8 || got.BanDurationMinutes != 90 {
		t.Fatalf("ban settings did not apply: %+v", got)
	}
	// Unset ban numbers are the shipped defaults (1/2/3, 6 polls, 60 min),
	// never zero: a zero BanAfterPolls would suspend on one sample.
	d := GeoPolicyFromSettings(GeoPolicySettings{BanEnabled: true})
	if d.BanMaxCountries != 1 || d.BanMaxRegions != 2 || d.BanMaxCities != 3 ||
		d.BanAfterPolls != 6 || d.BanDurationMinutes != 60 {
		t.Fatalf("unset ban numbers = %+v, want the defaults 1/2/3, 6, 60", d)
	}
}

// Automatic suspension is off unless a scope turns it on (D3). There is no
// "unset" for the switch: false is the default.
func TestGeoPolicyFromSettings_BanIsOffUnlessEnabled(t *testing.T) {
	if DefaultGeoPolicy().BanEnabled || GeoPolicyFromSettings(GeoPolicySettings{}).BanEnabled {
		t.Fatal("automatic suspension must be off by default")
	}
	if !GeoPolicyFromSettings(GeoPolicySettings{BanEnabled: true}).BanEnabled {
		t.Fatal("an explicit ban_enabled must carry through")
	}
}

// A ban tolerance stricter than the flag tolerance would suspend someone the
// flag does not even consider over. It is raised to the flag tolerance.
func TestGeoPolicyFromSettings_BanToleranceNeverBelowFlagTolerance(t *testing.T) {
	got := GeoPolicyFromSettings(GeoPolicySettings{
		MaxPlaces: 3, MaxRegions: 4, MaxCities: 5,
		BanMaxCountries: 1, BanMaxRegions: 2, BanMaxCities: 3,
	})
	if got.BanMaxCountries != 3 || got.BanMaxRegions != 4 || got.BanMaxCities != 5 {
		t.Fatalf("ban tolerances = %d/%d/%d, want raised to the flag ones 3/4/5",
			got.BanMaxCountries, got.BanMaxRegions, got.BanMaxCities)
	}
}

// A suspension must stay reversible: at most a week, however long the stored
// value is.
func TestGeoPolicyFromSettings_BanDurationIsClamped(t *testing.T) {
	got := GeoPolicyFromSettings(GeoPolicySettings{BanDurationMinutes: 999999})
	if got.BanDurationMinutes != GeoBanMaxDurationMinutes {
		t.Fatalf("ban duration = %d, want clamped to %d", got.BanDurationMinutes, GeoBanMaxDurationMinutes)
	}
	if got := GeoPolicyFromSettings(GeoPolicySettings{BanDurationMinutes: -5}); got.BanDurationMinutes != 60 {
		t.Fatalf("negative ban duration = %d, want the 60-minute default", got.BanDurationMinutes)
	}
}

// Co-travel folds COUNTRIES only. A token like "JP/Kanto" — the shape v1's
// region scope suggested — could never match a country code, so it used to
// sit in the set looking like a rule while doing nothing. It is dropped, and
// a set left empty by that is dropped with it.
func TestGeoPolicyFromSettings_RegionCoTravelTokenIsDroppedNotSilentlyIgnored(t *testing.T) {
	got := GeoPolicyFromSettings(GeoPolicySettings{CoTravel: "JP/Kanto, JP/Kansai\ncn/guangdong,hk\njp,tw"})
	want := [][]string{{"HK"}, {"JP", "TW"}}
	if !reflect.DeepEqual(got.CoTravel, want) {
		t.Fatalf("co-travel = %v, want %v", got.CoTravel, want)
	}
}

// The set and the database may disagree on case in either direction; the
// fold must not care.
func TestGeoPolicyFromSettings_CoTravelIsCaseInsensitive(t *testing.T) {
	p := GeoPolicyFromSettings(GeoPolicySettings{CoTravel: "Jp,tW"})
	o := ObserveGeo(p, ips("1.1.1.1", "2.2.2.2"),
		lookupOf(map[string]GeoLocation{
			"1.1.1.1": geoAt("jp", "", "Tokyo"),
			"2.2.2.2": geoAt("TW", "", "Taipei"),
		}), true)
	if !reflect.DeepEqual(o.Places, []string{"JP"}) {
		t.Fatalf("places = %v, want [JP]", o.Places)
	}
}
