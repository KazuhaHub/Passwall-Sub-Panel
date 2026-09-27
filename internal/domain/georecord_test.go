package domain

import (
	"encoding/json"
	"net/netip"
	"reflect"
	"strings"
	"testing"
)

// Evidence is stored with the verdict and shown to admins. It says where a
// user is and how the sample was built, and it must never say FROM WHICH
// ADDRESS: an address in a stored row outlives the connection it described
// and turns a location summary into a tracking log.
//
// The same holds for the Why the evidence now carries: it records a policy and
// a branch, and nothing in it may parse as an address or a network, whatever
// field a later change adds to it.
func TestGeoEvidenceFrom_CarriesNoAddresses(t *testing.T) {
	list := []string{"203.0.113.7", "198.51.100.9", "2001:db8:1:2::5"}
	p := DefaultGeoPolicy()
	o := ObserveGeo(p, ips(list...), lookupOf(map[string]GeoLocation{
		"203.0.113.7":     geoAt("CN", "Guangdong", "Shenzhen"),
		"198.51.100.9":    geoAt("CN", "Hunan", "Changsha"),
		"2001:db8:1:2::5": geoAt("JP", "Kanto", "Tokyo"),
	}), true)
	raw, err := json.Marshal(GeoEvidenceFrom(o, EvaluateGeo(p, o, GeoStreak{}).Why))
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	// Not vacuous: the places themselves must be there, and so must the why.
	for _, want := range []string{"Shenzhen", "Changsha", "Tokyo", `"why":{"code":"suspect"`} {
		if !strings.Contains(string(raw), want) {
			t.Fatalf("evidence %s lacks %q", raw, want)
		}
	}
	for _, needle := range append(list, "203.0.113", "198.51.100", "2001:db8") {
		if strings.Contains(string(raw), needle) {
			t.Fatalf("evidence carries %q: %s", needle, raw)
		}
	}
	var tree any
	if err := json.Unmarshal(raw, &tree); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	walkJSONStrings(tree, func(s string) {
		if _, err := netip.ParseAddr(s); err == nil {
			t.Fatalf("evidence carries the address %q: %s", s, raw)
		}
		if _, err := netip.ParsePrefix(s); err == nil {
			t.Fatalf("evidence carries the network %q: %s", s, raw)
		}
	})
}

// walkJSONStrings calls fn with every string in a decoded JSON value, object
// keys included, however deeply nested.
func walkJSONStrings(v any, fn func(string)) {
	switch x := v.(type) {
	case string:
		fn(x)
	case []any:
		for _, e := range x {
			walkJSONStrings(e, fn)
		}
	case map[string]any:
		for k, e := range x {
			fn(k)
			walkJSONStrings(e, fn)
		}
	}
}

// Version 2, so a reader can tell evidence that records its Why from v1
// evidence (none) and from a legacy row (no evidence, which reads as 0).
func TestGeoEvidenceFrom_VersionIsSet(t *testing.T) {
	if got := GeoEvidenceFrom(GeoObservation{}, GeoWhy{}).V; got != 2 {
		t.Fatalf("v = %d, want 2", got)
	}
}

// The why travels with the evidence, whole. A zero Why (no branch named) is
// left out rather than stored as a code of "" — a reader would otherwise have
// to tell an explanation from an empty object.
func TestGeoEvidenceFrom_CarriesTheWhy(t *testing.T) {
	why := GeoWhy{Code: GeoWhySuspect, Tier: GeoTierRegion, Scope: GeoScopeCity,
		Tol: GeoTolerances{Countries: 1, Regions: 3, Cities: 2}, FlagAfter: 3, ClearAfter: 6, MinPlacedRatio: 0.5}
	e := GeoEvidenceFrom(GeoObservation{}, why)
	if e.Why == nil || *e.Why != why || e.V != 2 {
		t.Fatalf("evidence = v%d why %+v, want v2 carrying %+v", e.V, e.Why, why)
	}
	if e := GeoEvidenceFrom(GeoObservation{}, GeoWhy{}); e.Why != nil {
		t.Fatalf("why = %+v for a zero GeoWhy, want nil", e.Why)
	}
}

// Spots serialise as [] even for an idle user, so a reader that iterates
// them never meets null.
func TestGeoEvidenceFrom_SpotsNeverNil(t *testing.T) {
	e := GeoEvidenceFrom(GeoObservation{}, GeoWhy{})
	if e.Spots == nil {
		t.Fatal("spots is nil")
	}
	raw, _ := json.Marshal(e)
	if !strings.Contains(string(raw), `"spots":[]`) {
		t.Fatalf("evidence = %s, want \"spots\":[]", raw)
	}
}

// Every number the verdict was drawn from reaches the evidence, under the
// names the admin API and the SPA read.
func TestGeoEvidenceFrom_MapsTheObservation(t *testing.T) {
	o := GeoObservation{
		Places: []string{"CN", "JP"}, Placed: 5, Unplaced: 1,
		RegionKnown: 4, CityKnown: 3,
		RegionSpread: 2, RegionCountry: "CN", CitySpread: 3, CityCountry: "JP",
		Excluded: GeoExcluded{Shared: 1, Listed: 2, Infra: 3, Internal: 4},
		Stale:    7, Networks: 6,
		Spots: []GeoSpot{{CC: "CN", Region: "Guangdong", City: "Shenzhen", N: 2}},
	}
	want := GeoEvidence{
		V:        2,
		Spots:    []GeoSpot{{CC: "CN", Region: "Guangdong", City: "Shenzhen", N: 2}},
		Excluded: GeoExcluded{Shared: 1, Listed: 2, Infra: 3, Internal: 4},
		Stale:    7,
		Coverage: GeoCoverage{Placed: 5, Unplaced: 1, RegionKnown: 4, CityKnown: 3},
		Networks: 6,
		Spread:   GeoSpread{Countries: 2, Regions: 2, RegionCountry: "CN", Cities: 3, CityCountry: "JP"},
	}
	got := GeoEvidenceFrom(o, GeoWhy{})
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("evidence = %+v\nwant       %+v", got, want)
	}
	// A copy, not an alias: the stored evidence must not change if the
	// observation's slice is reused.
	o.Spots[0].N = 99
	if got.Spots[0].N != 2 {
		t.Fatal("evidence aliases the observation's spots")
	}
}
