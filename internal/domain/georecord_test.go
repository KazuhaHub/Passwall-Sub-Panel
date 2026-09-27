package domain

import (
	"encoding/json"
	"net/netip"
	"reflect"
	"strconv"
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

// Version 3, so a reader can tell evidence that may record a distance and
// region codes from v2 evidence (which never measured a distance, so its
// missing max_km means "not recorded" rather than "none measured"), from v1
// evidence (no Why) and from a legacy row (no evidence, which reads as 0).
func TestGeoEvidenceFrom_VersionIsSet(t *testing.T) {
	if got := GeoEvidenceFrom(GeoObservation{}, GeoWhy{}).V; got != 3 {
		t.Fatalf("v = %d, want 3", got)
	}
}

// The why travels with the evidence, whole. A zero Why (no branch named) is
// left out rather than stored as a code of "" — a reader would otherwise have
// to tell an explanation from an empty object.
func TestGeoEvidenceFrom_CarriesTheWhy(t *testing.T) {
	why := GeoWhy{Code: GeoWhySuspect, Tier: GeoTierRegion, Scope: GeoScopeCity,
		Tol: GeoTolerances{Countries: 1, Regions: 3, Cities: 2}, FlagAfter: 3, ClearAfter: 6, MinPlacedRatio: 0.5}
	e := GeoEvidenceFrom(GeoObservation{}, why)
	if e.Why == nil || *e.Why != why || e.V != 3 {
		t.Fatalf("evidence = v%d why %+v, want v3 carrying %+v", e.V, e.Why, why)
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
		Spots: []GeoSpot{{CC: "CN", Region: "Guangdong", RC: "GD", City: "Shenzhen", N: 2}},
		MaxKm: 540, CoordSources: 3,
	}
	want := GeoEvidence{
		V:        3,
		Spots:    []GeoSpot{{CC: "CN", Region: "Guangdong", RC: "GD", City: "Shenzhen", N: 2}},
		Excluded: GeoExcluded{Shared: 1, Listed: 2, Infra: 3, Internal: 4},
		Stale:    7,
		Coverage: GeoCoverage{Placed: 5, Unplaced: 1, RegionKnown: 4, CityKnown: 3},
		Networks: 6,
		Spread:   GeoSpread{Countries: 2, Regions: 2, RegionCountry: "CN", Cities: 3, CityCountry: "JP", MaxKm: 540},
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

// The poll's path, end to end: sources with region codes and coordinates go
// through ObserveGeo and come out of GeoEvidenceFrom as v3 evidence carrying
// the rounded distance and each spot's code. Shenzhen–Changsha is 642.06 km;
// less 20 + 20 of radius that is 602.06, stored as 600.
func TestGeoEvidenceFrom_CarriesTheDistanceAndCodes(t *testing.T) {
	p := DefaultGeoPolicy()
	o := observeAt(p, map[string]GeoLocation{
		"1.1.1.1": geoAtPoint("CN", "Guangdong", "Shenzhen", "GD", ptSZ.r(20)),
		"1.1.1.2": geoAtPoint("CN", "Hunan", "Changsha", "HN", ptCS.r(20)),
	})
	e := GeoEvidenceFrom(o, EvaluateGeo(p, o, GeoStreak{}).Why)
	if e.V != 3 {
		t.Fatalf("v = %d, want 3", e.V)
	}
	if e.Spread.MaxKm != 600 {
		t.Fatalf("spread.max_km = %d, want 600", e.Spread.MaxKm)
	}
	want := []GeoSpot{
		{CC: "CN", Region: "Guangdong", RC: "GD", City: "Shenzhen", N: 1},
		{CC: "CN", Region: "Hunan", RC: "HN", City: "Changsha", N: 1},
	}
	if !reflect.DeepEqual(e.Spots, want) {
		t.Fatalf("spots = %+v\nwant    %+v", e.Spots, want)
	}
}

// The admin view's JSON for evidence without a code or a distance is the v2
// shape: no "rc" on a spot the database gave no code, and no "max_km" when
// none was measured. An empty code serialised as "" would make the SPA look
// up a province named nothing, and a 0 distance would read as "the same
// place" — which is exactly what 0 does not mean.
func TestGeoEvidence_EmptyCodeAndDistanceAreOmitted(t *testing.T) {
	bare, err := json.Marshal(GeoEvidence{
		V:     GeoEvidenceVersion,
		Spots: []GeoSpot{{CC: "CN", Region: "Guangdong", City: "Shenzhen", N: 1}},
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	for _, key := range []string{`"rc"`, `"max_km"`} {
		if strings.Contains(string(bare), key) {
			t.Errorf("evidence without a code or distance carries %s: %s", key, bare)
		}
	}
	// Not vacuous: set, both keys are there.
	full, err := json.Marshal(GeoEvidence{
		V:      GeoEvidenceVersion,
		Spots:  []GeoSpot{{CC: "CN", Region: "Guangdong", RC: "GD", City: "Shenzhen", N: 1}},
		Spread: GeoSpread{MaxKm: 540},
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	for _, want := range []string{`"region":"Guangdong","rc":"GD","city":"Shenzhen"`, `"max_km":540`} {
		if !strings.Contains(string(full), want) {
			t.Errorf("evidence %s lacks %s", full, want)
		}
	}
}

// The coordinates that produced the distance never reach the stored JSON, in
// any form: not the full value, not a 6-character prefix a rounding field
// would leave, and no key named for one. TestNoStoredTypeHoldsACoordinate pins
// the types; this pins the bytes the poll actually writes.
func TestGeoEvidence_CarriesNoCoordinates(t *testing.T) {
	sz := GeoPoint{Lat: 22.543123, Lon: 114.057868, RadiusKm: 20}
	cs := GeoPoint{Lat: 28.228271, Lon: 112.938813, RadiusKm: 20}
	p := DefaultGeoPolicy()
	o := observeAt(p, map[string]GeoLocation{
		"1.1.1.1": geoAtPoint("CN", "Guangdong", "Shenzhen", "GD", sz),
		"1.1.1.2": geoAtPoint("CN", "Hunan", "Changsha", "HN", cs),
	})
	raw, err := json.Marshal(GeoEvidenceFrom(o, EvaluateGeo(p, o, GeoStreak{}).Why))
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	// Not vacuous: the places the coordinates belong to are there.
	for _, want := range []string{"Shenzhen", "Changsha"} {
		if !strings.Contains(string(raw), want) {
			t.Fatalf("evidence %s lacks %q", raw, want)
		}
	}
	for _, v := range []float64{sz.Lat, sz.Lon, cs.Lat, cs.Lon} {
		s := strconv.FormatFloat(v, 'f', -1, 64)
		for _, needle := range []string{s, s[:6]} {
			if strings.Contains(string(raw), needle) {
				t.Fatalf("evidence carries the coordinate %q: %s", needle, raw)
			}
		}
	}
	for _, key := range []string{"latitude", "longitude", "accuracy"} {
		if strings.Contains(strings.ToLower(string(raw)), key) {
			t.Fatalf("evidence carries %q: %s", key, raw)
		}
	}
}
