package domain

import (
	"reflect"
	"testing"
)

// A fresh install has empty settings. Reading them literally gives MaxPlaces 0
// and FlagAfterPolls 0 — which flags EVERY connected user on their first poll,
// including one sitting at home. Zero means "never configured", and this is
// the single place that distinction is made.
func TestGeoPolicyFromSettings_UnsetFallsBackToTheDefaultNotToZero(t *testing.T) {
	got := GeoPolicyFromSettings(GeoPolicySettings{})
	want := DefaultGeoPolicy()
	if got.MaxPlaces != want.MaxPlaces || got.FlagAfterPolls != want.FlagAfterPolls ||
		got.ClearAfterPolls != want.ClearAfterPolls || got.Scope != want.Scope ||
		got.MinPlacedRatio != want.MinPlacedRatio {
		t.Fatalf("empty settings = %+v, want the shipped default %+v", got, want)
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
func TestGeoPolicyFromSettings_ScopeIsCaseAndSpaceTolerant(t *testing.T) {
	for _, in := range []string{"City", " CITY ", "city"} {
		if got := GeoPolicyFromSettings(GeoPolicySettings{Scope: in}); got.Scope != GeoScopeCity {
			t.Fatalf("scope %q resolved to %s, want city", in, got.Scope)
		}
	}
}

// An unrecognised scope falls back to the documented default rather than to
// something permissive-by-accident.
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

// Co-travel sets are typed as "JP,TW". Country codes are conventionally
// upper and placeOf emits them that way, so a set typed lowercase that
// silently never matched would be indistinguishable from one that was
// ignored — and an admin would have no way to tell which.
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
