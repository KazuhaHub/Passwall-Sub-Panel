package domain

import (
	"math"
	"reflect"
	"strings"
	"testing"
)

// Where a database would place a network in each of these cities. The
// distances the tests below expect were computed on the same 6371 km sphere
// the implementation uses, and each is quoted beside its use.
var (
	ptBJ = GeoPoint{Lat: 39.9042, Lon: 116.4074} // Beijing
	ptSH = GeoPoint{Lat: 31.2304, Lon: 121.4737} // Shanghai
	ptGZ = GeoPoint{Lat: 23.1291, Lon: 113.2644} // Guangzhou
	ptSZ = GeoPoint{Lat: 22.5431, Lon: 114.0579} // Shenzhen
	ptCS = GeoPoint{Lat: 28.2282, Lon: 112.9388} // Changsha
	ptCN = GeoPoint{Lat: 35, Lon: 105}           // where a country-only CN record sits
	ptUR = GeoPoint{Lat: 43.8256, Lon: 87.6168}  // Ürümqi
	ptF  = GeoPoint{Lat: 39.95, Lon: 116.45}     // beside Beijing, for the cap test
	ptJ  = GeoPoint{Lat: 35.6762, Lon: 139.6503} // Tokyo and Osaka, as the traffic
	ptO  = GeoPoint{Lat: 34.6937, Lon: 135.5023} // poll's tests place them
	ptA1 = GeoPoint{Lat: 0, Lon: 179.5}          // just west of the antimeridian
	ptA2 = GeoPoint{Lat: 0, Lon: -179.5}         // and just east of it
	ptE1 = GeoPoint{Lat: 0, Lon: 10}             // on the equator
	ptE2 = GeoPoint{Lat: 0, Lon: -170}           // and its antipode
	ptN1 = GeoPoint{Lat: -88.5, Lon: -178}       // an antipodal pair whose half-angle
	ptN2 = GeoPoint{Lat: 88.5, Lon: 2}           // term rounds to just above 1
)

// r returns p with an accuracy radius.
func (p GeoPoint) r(km int) GeoPoint {
	p.RadiusKm = km
	return p
}

// geoAtPoint is geoAt plus the database's region code and coordinates: the
// shape a city database with authcore v0.5.0 returns.
func geoAtPoint(cc, region, city, rc string, p GeoPoint) GeoLocation {
	g := geoAt(cc, region, city)
	g.RegionCode = rc
	g.Latitude, g.Longitude, g.AccuracyRadiusKm = p.Lat, p.Lon, p.RadiusKm
	return g
}

// A coordinate is only as good as the database's promise that it is one.
// authcore already returns a pair or none, but fakes and future adapters also
// build GeoLocation, and a (0, 0) "none" or a NaN that reached the distance
// would be read as a place in the Gulf of Guinea or as no distance at all.
func TestGeoLocationPoint(t *testing.T) {
	ok := func(lat, lon float64, r int) GeoLocation {
		return GeoLocation{CountryCode: "CN", Latitude: lat, Longitude: lon, AccuracyRadiusKm: r}
	}
	if p, good := ok(22.5431, 114.0579, 20).Point(); !good || p != (GeoPoint{Lat: 22.5431, Lon: 114.0579, RadiusKm: 20}) {
		t.Fatalf("a real pair = %+v, %v; want (22.5431, 114.0579) r20, true", p, good)
	}
	for _, c := range []struct {
		name     string
		lat, lon float64
	}{
		{"(0, 0) is none", 0, 0},
		{"latitude past the pole", 91, 10},
		{"longitude past the antimeridian", 10, -181},
		{"NaN latitude", math.NaN(), 10},
		{"NaN longitude", 10, math.NaN()},
		{"+Inf latitude", math.Inf(1), 10},
		{"-Inf longitude", 10, math.Inf(-1)},
	} {
		if p, good := ok(c.lat, c.lon, 20).Point(); good {
			t.Errorf("%s: Point() = %+v, true; want false", c.name, p)
		}
	}
	// Only the PAIR (0, 0) means none: a point on the equator or on the prime
	// meridian is a real place, and so are the edges of the valid range.
	for _, c := range [][2]float64{{10, 0}, {0, 10}, {90, 180}, {-90, -180}} {
		if _, good := ok(c[0], c[1], 0).Point(); !good {
			t.Errorf("(%v, %v) must be a point", c[0], c[1])
		}
	}
	// A negative radius is no radius, never a bonus distance.
	if p, good := ok(22.5431, 114.0579, -5).Point(); !good || p.RadiusKm != 0 {
		t.Fatalf("radius -5 = %+v, %v; want radius 0, true", p, good)
	}
}

func near(got, want float64) bool { return math.Abs(got-want) <= 0.01 }

// The great-circle distance on a 6371 km sphere. The antipodal pairs are
// where an unclamped half-angle term can round past 1 and turn the distance
// into NaN, and the antimeridian pair is where a formula on raw longitude
// differences would read two points 111 km apart as nearly 40,000 km.
func TestHaversineKm(t *testing.T) {
	for _, c := range []struct {
		name string
		a, b GeoPoint
		want float64
	}{
		{"Beijing–Shanghai", ptBJ, ptSH, 1067.31},
		{"Guangzhou–Shenzhen", ptGZ, ptSZ, 104.20},
		{"antipodes on the equator", ptE1, ptE2, 20015.09},
		{"antipodes near the poles", ptN1, ptN2, 20015.09},
		{"across the antimeridian", ptA1, ptA2, 111.19},
		{"Tokyo–Osaka", ptJ, ptO, 392.44},
		{"the same point", ptBJ, ptBJ, 0},
	} {
		got := HaversineKm(c.a, c.b)
		if math.IsNaN(got) || !near(got, c.want) {
			t.Errorf("%s: HaversineKm = %v, want %.2f ± 0.01", c.name, got, c.want)
		}
		if back := HaversineKm(c.b, c.a); math.Abs(back-got) > 1e-9 {
			t.Errorf("%s: not symmetric: %v one way, %v the other", c.name, got, back)
		}
	}
}

// The distance after both accuracy radii: how far apart the two networks
// must at least be, given how sure the database was of each. Never negative:
// two overlapping circles are "could be the same place", not a debt.
func TestEffectiveKm(t *testing.T) {
	for _, c := range []struct {
		name string
		a, b GeoPoint
		want float64
	}{
		{"Beijing r100–Shanghai r50", ptBJ.r(100), ptSH.r(50), 917.31},
		{"Guangzhou r50–Shenzhen r50", ptGZ.r(50), ptSZ.r(50), 4.20},
		{"Guangzhou r60–Shenzhen r50 overlap", ptGZ.r(60), ptSZ.r(50), 0},
	} {
		if got := EffectiveKm(c.a, c.b); !near(got, c.want) {
			t.Errorf("%s: EffectiveKm = %v, want %.2f ± 0.01", c.name, got, c.want)
		}
	}
}

// Half-up to the nearest 10 km: the stored number is an estimate between two
// database centroids, and a kilometre-exact figure would claim a precision
// the data does not have.
func TestRoundKm10(t *testing.T) {
	for _, c := range []struct {
		in   float64
		want int
	}{
		{0, 0}, {4.999, 0}, {5, 10}, {14.999, 10}, {15, 20}, {1067.31, 1070}, {20015.09, 20020},
	} {
		if got := roundKm10(c.in); got != c.want {
			t.Errorf("roundKm10(%v) = %d, want %d", c.in, got, c.want)
		}
	}
}

// The farthest pair is chosen AFTER the radii. Beijing–Shenzhen is the
// farthest pair of centroids (1943.14 km), but both of Beijing's and
// Guangzhou's records here are 1000 km vague; the most distance the data
// actually supports is Shanghai–Shenzhen (1213.27 km, both exact). Picking
// the raw farthest pair and subtracting afterwards would report 940.
func TestMaxSpreadKm_FarthestPairAfterTheRadii(t *testing.T) {
	got := MaxSpreadKm(map[GeoPoint]int{ptBJ.r(1000): 1, ptGZ.r(1000): 1, ptSH: 1, ptSZ: 1})
	if got != 1210 {
		t.Fatalf("MaxSpreadKm = %d, want 1210 (Shanghai–Shenzhen after the radii)", got)
	}
	// Fewer than two distinct points is no distance: a point is never
	// paired with itself, however many sources sit on it.
	for name, m := range map[string]map[GeoPoint]int{
		"nil":                  nil,
		"empty":                {},
		"one point, 5 sources": {ptBJ: 5},
	} {
		if got := MaxSpreadKm(m); got != 0 {
			t.Errorf("%s: MaxSpreadKm = %d, want 0", name, got)
		}
	}
}

// Beyond GeoSpreadMaxPoints only the most-occupied points are paired, and
// which ones must not depend on map order: the same sources must give the
// same number every poll.
//
// 257 distinct points: Beijing and Shanghai with 5 sources each, 253 vague
// fillers beside Beijing with 2 each, and two single-source points tying for
// the last of the 256 slots — F beside Beijing and Ürümqi. Ranked by count
// then latitude, F (39.95) takes the slot and Ürümqi (43.83) is cut, so the
// answer is Beijing–Shanghai, 1070. With Ürümqi in it would be Shanghai–Ürümqi
// (3267.66 → 3270). Each call builds a fresh map, so a ranking without the
// latitude tie-break would let Ürümqi in about half the time and fail one of
// twenty calls with probability 1 − 2⁻²⁰.
func TestMaxSpreadKm_CapIsADeterministicLowerBound(t *testing.T) {
	build := func(urumqi int) map[GeoPoint]int {
		m := map[GeoPoint]int{ptBJ: 5, ptSH: 5, ptF.r(1000): 1, ptUR: urumqi}
		for i := 1; i <= 253; i++ {
			m[GeoPoint{Lat: 39.90 + float64(i)*0.0001, Lon: 116.41, RadiusKm: 1000}] = 2
		}
		if len(m) != GeoSpreadMaxPoints+1 {
			t.Fatalf("fixture has %d distinct points, want %d", len(m), GeoSpreadMaxPoints+1)
		}
		return m
	}
	for i := range 20 {
		if got := MaxSpreadKm(build(1)); got != 1070 {
			t.Fatalf("call %d: MaxSpreadKm = %d, want 1070 — the cut point must be chosen by count, then latitude", i, got)
		}
	}
	// The cap keeps the MOST-OCCUPIED points: with three sources Ürümqi
	// outranks every filler and F is the one cut.
	if got := MaxSpreadKm(build(3)); got != 3270 {
		t.Fatalf("MaxSpreadKm with Ürümqi at 3 sources = %d, want 3270", got)
	}
}

// Coordinates live in memory for one judgement and nowhere else. Evidence is
// stored per account and served to admins; a coordinate kept there, beside
// the account it describes, turns a verdict into a map of where a subscriber
// connects from. This pins the rule structurally, for every type that is
// stored or served as evidence, at every depth: no field may hold a GeoPoint
// or a GeoLocation, and no JSON name may read as a coordinate or a radius.
// Kilometres drawn from the points are fine; the points are not.
func TestNoStoredTypeHoldsACoordinate(t *testing.T) {
	banned := []string{"lat", "lon", "accuracy", "radius", "coord"}
	forbidden := map[reflect.Type]bool{reflect.TypeFor[GeoPoint](): true, reflect.TypeFor[GeoLocation](): true}
	seen := map[reflect.Type]bool{}
	var walk func(path string, typ reflect.Type)
	walk = func(path string, typ reflect.Type) {
		if forbidden[typ] {
			t.Errorf("%s holds a %s", path, typ)
			return
		}
		if seen[typ] {
			return
		}
		seen[typ] = true
		switch typ.Kind() {
		case reflect.Pointer, reflect.Slice, reflect.Array:
			walk(path+"[]", typ.Elem())
		case reflect.Map:
			walk(path+"[key]", typ.Key())
			walk(path+"[]", typ.Elem())
		case reflect.Struct:
			for i := range typ.NumField() {
				f := typ.Field(i)
				name, _, _ := strings.Cut(f.Tag.Get("json"), ",")
				for _, b := range banned {
					if strings.Contains(strings.ToLower(name), b) {
						t.Errorf("%s.%s is served as %q, which reads as a coordinate", path, f.Name, name)
					}
				}
				walk(path+"."+f.Name, f.Type)
			}
		}
	}
	for _, root := range []reflect.Type{
		reflect.TypeFor[GeoRecord](),
		reflect.TypeFor[GeoEvidence](),
		reflect.TypeFor[SubSpreadEvidence](),
		reflect.TypeFor[DevicesEvidence](),
		reflect.TypeFor[UsageShiftEvidence](),
		reflect.TypeFor[LoginCountryEvidence](),
		// The live-connection snapshot is served to admins with an address
		// beside every place, and the connection history stores the same
		// shape: a coordinate there is a map pin per subscriber.
		reflect.TypeFor[LiveConnection](),
		reflect.TypeFor[LiveConnSnapshot](),
		// connection_history keeps that shape for up to 90 days.
		reflect.TypeFor[ConnectionRecord](),
		// A flag record keeps a verdict's evidence for months, with no
		// address and so no pin either.
		reflect.TypeFor[FlagRecord](),
		reflect.TypeFor[GeoFlagParams](),
	} {
		walk(root.Name(), root)
	}
	// Not vacuous: the walk reached the nested types a coordinate would
	// most plausibly be added to.
	for _, typ := range []reflect.Type{reflect.TypeFor[GeoSpot](), reflect.TypeFor[GeoSpread](), reflect.TypeFor[SubProvince](), reflect.TypeFor[ConnPlace]()} {
		if !seen[typ] {
			t.Errorf("the walk never reached %s", typ)
		}
	}
}
