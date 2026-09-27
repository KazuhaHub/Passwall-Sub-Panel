package domain

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

// Evidence is stored with the verdict and shown to admins. It says where a
// user is and how the sample was built, and it must never say FROM WHICH
// ADDRESS: an address in a stored row outlives the connection it described
// and turns a location summary into a tracking log.
func TestGeoEvidenceFrom_CarriesNoAddresses(t *testing.T) {
	list := []string{"203.0.113.7", "198.51.100.9", "2001:db8:1:2::5"}
	o := ObserveGeo(DefaultGeoPolicy(), ips(list...), lookupOf(map[string]GeoLocation{
		"203.0.113.7":     geoAt("CN", "Guangdong", "Shenzhen"),
		"198.51.100.9":    geoAt("CN", "Hunan", "Changsha"),
		"2001:db8:1:2::5": geoAt("JP", "Kanto", "Tokyo"),
	}), true)
	raw, err := json.Marshal(GeoEvidenceFrom(o))
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	// Not vacuous: the places themselves must be there.
	for _, want := range []string{"Shenzhen", "Changsha", "Tokyo"} {
		if !strings.Contains(string(raw), want) {
			t.Fatalf("evidence %s lacks the place %q", raw, want)
		}
	}
	for _, needle := range append(list, "203.0.113", "198.51.100", "2001:db8") {
		if strings.Contains(string(raw), needle) {
			t.Fatalf("evidence carries %q: %s", needle, raw)
		}
	}
}

// Version 1, so a reader can tell evidence written by this code from a
// legacy row (no evidence, which reads as version 0).
func TestGeoEvidenceFrom_VersionIsSet(t *testing.T) {
	if got := GeoEvidenceFrom(GeoObservation{}).V; got != 1 {
		t.Fatalf("v = %d, want 1", got)
	}
}

// Spots serialise as [] even for an idle user, so a reader that iterates
// them never meets null.
func TestGeoEvidenceFrom_SpotsNeverNil(t *testing.T) {
	e := GeoEvidenceFrom(GeoObservation{})
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
		V:        1,
		Spots:    []GeoSpot{{CC: "CN", Region: "Guangdong", City: "Shenzhen", N: 2}},
		Excluded: GeoExcluded{Shared: 1, Listed: 2, Infra: 3, Internal: 4},
		Stale:    7,
		Coverage: GeoCoverage{Placed: 5, Unplaced: 1, RegionKnown: 4, CityKnown: 3},
		Networks: 6,
		Spread:   GeoSpread{Countries: 2, Regions: 2, RegionCountry: "CN", Cities: 3, CityCountry: "JP"},
	}
	got := GeoEvidenceFrom(o)
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
