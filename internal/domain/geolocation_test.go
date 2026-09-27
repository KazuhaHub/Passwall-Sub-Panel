package domain

import (
	"encoding/json"
	"reflect"
	"testing"
)

// The sub-log, audit and auth-event admin views serve a GeoLocation verbatim
// as their "region" object. The region code and the coordinates were added to
// the type for in-memory judging, and neither may reach those views: a code
// would change their bytes for every city database, and a coordinate served
// beside an address is a precise map of where the subscriber connects from.
//
// The byte comparison pins today's four keys, their order and their
// non-omitempty empties. The reflection pass pins the rule rather than today's
// field list, so a field added later without `json:"-"` fails here instead of
// quietly widening three admin payloads.
func TestGeoLocation_JSONIsTheFourNamesOnly(t *testing.T) {
	full := GeoLocation{
		CountryCode: "CN", Country: "China", Region: "Guangdong", City: "Shenzhen",
		RegionCode: "GD", Latitude: 22.5431, Longitude: 114.0579, AccuracyRadiusKm: 20,
	}
	got, err := json.Marshal(full)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	const want = `{"country_code":"CN","country":"China","region":"Guangdong","city":"Shenzhen"}`
	if string(got) != want {
		t.Fatalf("json.Marshal(GeoLocation) =\n  %s\nwant exactly the four names\n  %s", got, want)
	}
	// An unresolved place still serialises its four keys: the views have
	// always sent them, empty, and a reader may test for the key.
	zero, err := json.Marshal(GeoLocation{})
	if err != nil {
		t.Fatalf("marshal zero: %v", err)
	}
	if string(zero) != `{"country_code":"","country":"","region":"","city":""}` {
		t.Fatalf("json.Marshal(GeoLocation{}) = %s, want the four keys, empty", zero)
	}

	names := map[string]string{
		"CountryCode": "country_code",
		"Country":     "country",
		"Region":      "region",
		"City":        "city",
	}
	typ := reflect.TypeFor[GeoLocation]()
	for i := range typ.NumField() {
		f := typ.Field(i)
		tag := f.Tag.Get("json")
		if name, ok := names[f.Name]; ok {
			if tag != name {
				t.Errorf("GeoLocation.%s has json tag %q, want %q", f.Name, tag, name)
			}
			continue
		}
		if tag != "-" {
			t.Errorf("GeoLocation.%s has json tag %q, want \"-\": only the four names may reach the per-event views", f.Name, tag)
		}
	}
}

// Empty decides whether a lookup produced anything worth keeping — the geo
// service drops Empty results before any caller sees them. It mirrors
// authcore's Location.Empty, which reads the four names only, so that a record
// carrying a code or coordinates but no name stays "nothing resolved" as it
// was before those fields existed. Reading a coordinate here would admit
// nameless places into the geo judgment as unplaced sources.
func TestGeoLocation_EmptyIgnoresCodeAndCoordinates(t *testing.T) {
	g := GeoLocation{RegionCode: "GD", Latitude: 22.5431, Longitude: 114.0579, AccuracyRadiusKm: 20}
	if !g.Empty() {
		t.Fatalf("%+v reports non-empty; Empty must read the four names only", g)
	}
	if (GeoLocation{City: "Shenzhen"}).Empty() {
		t.Fatal("a location with a city name must not report Empty")
	}
}
