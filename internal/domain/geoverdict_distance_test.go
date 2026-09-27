package domain

import (
	"fmt"
	"reflect"
	"testing"
)

// ---------------------------------------------------------------- region codes

// Each spot carries its region's ISO code, normalized, so the admin UI can
// name a province in the reader's language. The code belongs to the REGION:
// a source whose record gave none (Guangzhou here) shows the code its
// region's other sources gave, and a lower-case code reads as the canonical
// upper-case one.
func TestObserveGeo_SpotsCarryTheRegionCode(t *testing.T) {
	o := observeAt(DefaultGeoPolicy(), map[string]GeoLocation{
		"1.1.1.1": geoAtPoint("CN", "Guangdong", "Shenzhen", "GD", GeoPoint{}),
		"1.1.1.2": geoAtPoint("CN", "Guangdong", "Guangzhou", "", GeoPoint{}),
		"1.1.1.3": geoAtPoint("CN", "Hunan", "Changsha", "hn", GeoPoint{}),
	})
	want := []GeoSpot{
		{CC: "CN", Region: "Guangdong", RC: "GD", City: "Guangzhou", N: 1},
		{CC: "CN", Region: "Guangdong", RC: "GD", City: "Shenzhen", N: 1},
		{CC: "CN", Region: "Hunan", RC: "HN", City: "Changsha", N: 1},
	}
	if !reflect.DeepEqual(o.Spots, want) {
		t.Fatalf("spots = %+v\nwant    %+v", o.Spots, want)
	}
}

// The code is a display attribute and never a key. Grouping, every count and
// the verdict are drawn from (country, region, city) exactly as before, so a
// database that gives codes on some records and not others — or two codes
// for one region, which is a database bug — cannot split a province into
// two, add a spot, or move a verdict.
//
// Five users, one per verdict the concurrent-location check can reach from
// real sources, each with its first source duplicated on a second address so
// that one region always holds two sources whose codes can disagree. Each is
// observed three ways: without codes, with the duplicated region's two
// sources disagreeing (and every other source coded), and with codes on only
// some sources (one of them malformed). The observations must be identical
// once the display code is set aside, and so must the verdicts.
func TestObserveGeo_RegionCodeNeverChangesGroupingOrVerdict(t *testing.T) {
	type src struct{ cc, region, city string }
	cases := []struct {
		name string
		srcs []src // srcs[0] is also placed on a second address
		prev GeoStreak
		code GeoReasonCode
		tier GeoTier
	}{
		{"two countries", []src{{"DE", "Berlin", "Berlin"}, {"JP", "Tokyo", "Tokyo"}},
			GeoStreak{}, GeoWhySuspect, GeoTierCountry},
		{"three cities of one region", []src{{"JP", "Kanto", "Tokyo"}, {"JP", "Kanto", "Yokohama"}, {"JP", "Kanto", "Chiba"}},
			GeoStreak{}, GeoWhySuspect, GeoTierCity},
		{"two provinces, sustained", []src{{"CN", "Guangdong", "Shenzhen"}, {"CN", "Hunan", "Changsha"}},
			GeoStreak{Over: 2}, GeoWhyFlaggedSustained, GeoTierRegion},
		{"one province, latched", []src{{"CN", "Guangdong", "Shenzhen"}},
			GeoStreak{Flagged: true, Tier: GeoTierRegion}, GeoWhyFlaggedClearing, GeoTierRegion},
		{"two cities of one region", []src{{"JP", "Kanto", "Tokyo"}, {"JP", "Kanto", "Yokohama"}},
			GeoStreak{}, GeoWhyCleanWithin, GeoTierNone},
	}
	// code gives the code for source i; i == -1 is srcs[0]'s duplicate.
	variants := []struct {
		name string
		code func(i int) string
	}{
		{"no codes", func(int) string { return "" }},
		{"conflicting codes", func(i int) string {
			switch i {
			case 0:
				return "GD"
			case -1:
				return "GX"
			}
			return "hn"
		}},
		{"codes on some sources", func(i int) string {
			switch i {
			case 0:
				return "GD"
			case 1:
				return "CN-GD" // malformed: normalizes to ""
			}
			return ""
		}},
	}
	p := DefaultGeoPolicy()
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			observe := func(code func(int) string) GeoObservation {
				at := map[string]GeoLocation{}
				list := []string{}
				add := func(ip string, s src, rc string) {
					at[ip] = geoAtPoint(s.cc, s.region, s.city, rc, GeoPoint{})
					list = append(list, ip)
				}
				for i, s := range c.srcs {
					add(fmt.Sprintf("1.1.1.%d", i+1), s, code(i))
				}
				add("1.1.2.1", c.srcs[0], code(-1))
				o := ObserveGeo(p, ips(list...), lookupOf(at), true)
				for i := range o.Spots {
					o.Spots[i].RC = ""
				}
				return o
			}
			base := observe(variants[0].code)
			baseV := EvaluateGeo(p, base, c.prev)
			if baseV.Why.Code != c.code || baseV.Why.Tier != c.tier {
				t.Fatalf("baseline verdict = %s at %q (%s), want %s at %q", baseV.Why.Code, baseV.Why.Tier, baseV.Reason, c.code, c.tier)
			}
			for _, v := range variants[1:] {
				o := observe(v.code)
				if !reflect.DeepEqual(o, base) {
					t.Errorf("%s: observation = %+v\nwithout codes it is %+v", v.name, o, base)
				}
				if got := EvaluateGeo(p, o, c.prev); !reflect.DeepEqual(got, baseV) {
					t.Errorf("%s: verdict = %+v\nwithout codes it is %+v", v.name, got, baseV)
				}
			}
		})
	}
}

// No region, no code. A record that names a country but no region (or only a
// city) carries whatever code the database attached, and that code describes
// nothing the spot shows: the spot has no region for it to name.
func TestObserveGeo_CountryOnlySourceHasNoRegionCode(t *testing.T) {
	o := observeAt(DefaultGeoPolicy(), map[string]GeoLocation{
		"1.1.1.1": geoAtPoint("CN", "", "", "GD", GeoPoint{}),
		"1.1.1.2": geoAtPoint("CN", "", "Changsha", "HN", GeoPoint{}),
	})
	if len(o.Spots) != 2 {
		t.Fatalf("spots = %+v, want the country-only and the city-only spot", o.Spots)
	}
	for _, s := range o.Spots {
		if s.RC != "" {
			t.Errorf("spot %+v carries a code with no region to name", s)
		}
	}
}

// ---------------------------------------------------------------- distance

// The farthest pair of the user's concurrent sources: Beijing–Guangzhou
// (1888.59 km) among Beijing, Shanghai and Guangzhou, all exact.
func TestObserveGeo_MaxKmIsTheFarthestConcurrentPair(t *testing.T) {
	o := observeAt(DefaultGeoPolicy(), map[string]GeoLocation{
		"1.1.1.1": geoAtPoint("CN", "Beijing", "Beijing", "BJ", ptBJ),
		"1.1.1.2": geoAtPoint("CN", "Shanghai", "Shanghai", "SH", ptSH),
		"1.1.1.3": geoAtPoint("CN", "Guangdong", "Guangzhou", "GD", ptGZ),
	})
	if o.MaxKm != 1890 || o.CoordSources != 3 {
		t.Fatalf("MaxKm = %d from %d sources, want 1890 from 3", o.MaxKm, o.CoordSources)
	}
}

// A record resolved no finer than its country sits at the country's centroid
// (MaxMind's CN records at 35°N 105°E), so pairing it with a real city reads a
// user in Shenzhen as 1,520 km away from themselves. It is left out, as
// ObserveGeo already leaves it out of the region and city tiers. A record
// that names a city but no region is resolved below the country and does
// count: Shenzhen–Changsha is 642.06 km, less 20 + 20 of radius.
func TestObserveGeo_MaxKmCountsSourcesBelowTheCountryOnly(t *testing.T) {
	at := map[string]GeoLocation{
		"1.1.1.1": geoAtPoint("CN", "Guangdong", "Shenzhen", "GD", ptSZ.r(20)),
		"1.1.1.2": geoAtPoint("CN", "", "", "", ptCN.r(100)),
	}
	if o := observeAt(DefaultGeoPolicy(), at); o.MaxKm != 0 || o.CoordSources != 1 {
		t.Fatalf("with a country-only record: MaxKm = %d from %d sources, want 0 from 1", o.MaxKm, o.CoordSources)
	}
	at["1.1.1.3"] = geoAtPoint("CN", "", "Changsha", "", ptCS.r(20))
	if o := observeAt(DefaultGeoPolicy(), at); o.MaxKm != 600 || o.CoordSources != 2 {
		t.Fatalf("with a city-only record: MaxKm = %d from %d sources, want 600 from 2", o.MaxKm, o.CoordSources)
	}
}

// A source the database could not give a country is unplaced: it counts
// toward nothing below the country, and toward no distance either, whatever
// coordinates came with it.
func TestObserveGeo_MaxKmLeavesUnplacedSourcesOut(t *testing.T) {
	o := observeAt(DefaultGeoPolicy(), map[string]GeoLocation{
		"1.1.1.1": geoAtPoint("CN", "Beijing", "Beijing", "BJ", ptBJ),
		"1.1.1.2": geoAtPoint("CN", "Shanghai", "Shanghai", "SH", ptSH),
		"1.1.1.3": geoAtPoint("", "Xinjiang", "Urumqi", "XJ", ptUR),
	})
	if o.Unplaced != 1 {
		t.Fatalf("unplaced = %d, want 1", o.Unplaced)
	}
	if o.MaxKm != 1070 || o.CoordSources != 2 {
		t.Fatalf("MaxKm = %d from %d sources, want 1070 from 2 (Beijing–Shanghai)", o.MaxKm, o.CoordSources)
	}
}

// The distance is evidence and nothing else. A future change that let it into
// the tiers would turn every honest user on a mobile gateway 1,000 km from
// home into an accused one, and it would do so silently: nothing in the
// reason would change its wording. Every branch EvaluateGeo can take must
// give the same verdict at 0 km and at half the Earth.
func TestEvaluateGeo_IgnoresTheDistance(t *testing.T) {
	for _, f := range reasonFixtures() {
		t.Run(f.name, func(t *testing.T) {
			near, far := f.o, f.o
			near.MaxKm, near.CoordSources = 0, 0
			far.MaxKm, far.CoordSources = 20020, 9
			a, b := EvaluateGeo(f.p, near, f.prev), EvaluateGeo(f.p, far, f.prev)
			if !reflect.DeepEqual(a, b) {
				t.Fatalf("verdict at 0 km = %+v\nat 20020 km   = %+v", a, b)
			}
		})
	}
}
