package geoip

import (
	"testing"

	authcoregeoip "github.com/KazuhaHub/authcore/geoip"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
)

// The dual-schema mapping and the name/city/region fallbacks used to be tested
// here, against hand-built records. They are authcore's now, and its tests are a
// strict superset of the ones this package had: TestMapRecord_MaxMindSchema,
// TestMapRecord_IPinfoSchema, TestMapRecord_CountryCodeOnlyVariant,
// TestMapRecord_CityAndRegionAsPlainStrings, TestMapRecord_NameFallbackWithoutEnglish
// and TestMapRecord_Nil, plus TestReader_Lookup_EndToEnd and
// TestReader_Info_CountryGranularity against generated .mmdb fixtures. Repeating
// those here would be testing a dependency, not this package.
//
// What is this package's to test is the surface it still owns: the conversion
// into the panel's domain type, the address filtering callers rely on, the error
// and zero-value contract, and nil safety.

func TestToDomain(t *testing.T) {
	got := toDomain(authcoregeoip.Location{
		CountryCode: "HK", Country: "Hong Kong", Region: "Central and Western", City: "Central",
	})
	want := domain.GeoLocation{
		CountryCode: "HK", Country: "Hong Kong", Region: "Central and Western", City: "Central",
	}
	if got != want {
		t.Fatalf("toDomain = %+v, want %+v", got, want)
	}
	// Every field is optional — a country-level database fills two of the four.
	if empty := toDomain(authcoregeoip.Location{}); !empty.Empty() {
		t.Fatalf("toDomain of an empty location = %+v, want the domain zero value", empty)
	}
}

func TestIsResolvable(t *testing.T) {
	cases := map[string]bool{
		"8.8.8.8":              true,
		"1.1.1.1":              true,
		"2001:4860:4860::8888": true,
		"192.168.1.1":          false, // private
		"10.0.0.5":             false, // private
		"127.0.0.1":            false, // loopback
		"::1":                  false, // loopback
		"169.254.1.1":          false, // link-local
		"0.0.0.0":              false, // unspecified
		// Multicast is the one class the implementation this replaced did NOT
		// exclude: 224/4 and ff00::/8 are not link-local, so they passed its
		// checks and reached a lookup that could only return nothing.
		"224.0.0.1": false, // all-hosts multicast
		"239.1.1.1": false, // administratively scoped multicast
		"ff02::1":   false, // IPv6 multicast
		"":          false,
		"not-an-ip": false,
		" 8.8.8.8 ": true, // surrounding space is tolerated, not a different address
	}
	for ip, want := range cases {
		if got := IsResolvable(ip); got != want {
			t.Errorf("IsResolvable(%q) = %v, want %v", ip, got, want)
		}
	}
}

// A nil Reader is what the geo service holds before a database is configured, so
// every method has to answer "nothing loaded" rather than panic.
func TestNilReaderIsSafe(t *testing.T) {
	var r *Reader
	if err := r.Close(); err != nil {
		t.Fatalf("Close on a nil reader: %v", err)
	}
	if info := r.Info(); info != (DBInfo{}) {
		t.Fatalf("Info on a nil reader = %+v, want the zero value", info)
	}
	loc, err := r.Lookup("8.8.8.8")
	if err != nil {
		t.Fatalf("Lookup on a nil reader: %v", err)
	}
	if !loc.Empty() {
		t.Fatalf("Lookup on a nil reader = %+v, want the zero location", loc)
	}
}

func TestOpenMissingFile(t *testing.T) {
	if _, err := Open("/nonexistent/does-not-exist.mmdb"); err == nil {
		t.Fatal("Open of a missing file must return an error, not a reader that answers nothing")
	}
}
