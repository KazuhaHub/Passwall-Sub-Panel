package domain

import (
	"math"
	"sort"
)

// GeoPoint is where a location database placed one source, and how sure it
// was. In memory only: no persisted or served type holds one
// (TestNoStoredTypeHoldsACoordinate). GeoEvidence stores the rounded
// kilometres drawn from points, never the points, because a coordinate kept
// beside an account turns a verdict into a map of where the subscriber
// connects from.
//
// Comparable on purpose: ObserveGeo counts sources per point in a map keyed
// by it, so two sources the database placed identically are one point.
type GeoPoint struct {
	Lat, Lon float64
	RadiusKm int // 0 = the database gave none
}

// Point is g's coordinates. ok is false for (0, 0), a non-finite value, or a
// latitude outside [-90, 90] or longitude outside [-180, 180]. authcore
// already guarantees pair-or-none; this repeats the checks because fakes and
// future adapters build GeoLocation too. A negative radius reads as 0.
//
// (0, 0) is authcore's "none", so a network a database truly placed in the
// Gulf of Guinea is treated as unplaced too. That costs nothing a proxy
// panel's users would notice, and the alternative — every record without
// coordinates sitting at one shared point — would pair every such source
// with every real one.
func (g GeoLocation) Point() (GeoPoint, bool) {
	lat, lon := g.Latitude, g.Longitude
	// Written as "not inside" so a NaN, which fails every comparison, is
	// rejected rather than admitted. ±Inf is outside the ranges anyway.
	if !(lat >= -90 && lat <= 90) || !(lon >= -180 && lon <= 180) || (lat == 0 && lon == 0) {
		return GeoPoint{}, false
	}
	return GeoPoint{Lat: lat, Lon: lon, RadiusKm: max(g.AccuracyRadiusKm, 0)}, true
}

const (
	earthRadiusKm = 6371.0 // mean radius; the sphere is within ~0.5% of the ellipsoid, far inside any accuracy radius
	// GeoSpreadMaxPoints bounds the distinct points paired per user per poll:
	// pairing is quadratic, and 256 points is 32,640 distance computations.
	// No real account comes near it; the cap exists so a pathological one
	// cannot make the poll's cost unbounded.
	GeoSpreadMaxPoints = 256
)

// HaversineKm is the great-circle distance between a and b on a sphere of
// earthRadiusKm, ignoring both radii. The half-angle term is clamped to
// [0, 1]: for antipodal pairs floating-point rounding can leave it a hair
// above 1 (1 + 2⁻⁵² for (−88.5, −178)–(88.5, 2)), and should the square root
// keep that excess the arcsine is NaN — a distance that compares false with
// everything and would silently drop out of the maximum. sin²(Δλ/2) is
// periodic, so a pair across the antimeridian needs no special case.
func HaversineKm(a, b GeoPoint) float64 {
	const rad = math.Pi / 180
	sinLat := math.Sin((b.Lat - a.Lat) * rad / 2)
	sinLon := math.Sin((b.Lon - a.Lon) * rad / 2)
	h := sinLat*sinLat + math.Cos(a.Lat*rad)*math.Cos(b.Lat*rad)*sinLon*sinLon
	h = min(max(h, 0), 1)
	return 2 * earthRadiusKm * math.Asin(math.Sqrt(h))
}

// EffectiveKm is how far apart two sources must at least be, given how sure
// the database was of each: max(0, HaversineKm − a.RadiusKm − b.RadiusKm).
// Two centroids of vague records are not two places; only the distance that
// survives both radii is evidence of any. A missing radius (0) subtracts
// nothing, so with a database that gives none (DB-IP) the figure is the raw
// centroid distance and can overstate; that is tolerable only because
// nothing judges it.
func EffectiveKm(a, b GeoPoint) float64 {
	return max(0, HaversineKm(a, b)-float64(a.RadiusKm)-float64(b.RadiusKm))
}

// MaxSpreadKm is the largest EffectiveKm over every pair of distinct points,
// rounded to 10 km (roundKm10). counts maps a point to its number of sources.
// Beyond GeoSpreadMaxPoints, only the most-occupied points are paired (ties by
// Lat, then Lon, then RadiusKm, ascending), so the result is a deterministic
// lower bound. 0 with fewer than two distinct points.
//
// The maximum is taken AFTER the radii, not by picking the farthest pair of
// centroids and subtracting afterwards: the farthest centroids are often the
// vaguest records, and the figure is meant to be the most distance the data
// actually supports. The cap errs toward understating, which is the right
// side for a number that is observation only; and the full order of the
// ranking is what makes the same sources give the same figure every poll,
// whatever order the map yields them in.
func MaxSpreadKm(counts map[GeoPoint]int) int {
	if len(counts) < 2 {
		return 0
	}
	pts := make([]GeoPoint, 0, len(counts))
	for p := range counts {
		pts = append(pts, p)
	}
	if len(pts) > GeoSpreadMaxPoints {
		sort.Slice(pts, func(i, j int) bool {
			a, b := pts[i], pts[j]
			if counts[a] != counts[b] {
				return counts[a] > counts[b]
			}
			if a.Lat != b.Lat {
				return a.Lat < b.Lat
			}
			if a.Lon != b.Lon {
				return a.Lon < b.Lon
			}
			return a.RadiusKm < b.RadiusKm
		})
		pts = pts[:GeoSpreadMaxPoints]
	}
	best := 0.0
	for i := range pts {
		for j := i + 1; j < len(pts); j++ {
			best = max(best, EffectiveKm(pts[i], pts[j]))
		}
	}
	// Rounding is monotonic, so rounding the maximum once equals the
	// maximum of the rounded distances.
	return roundKm10(best)
}

// roundKm10 rounds a non-negative distance to the nearest 10 km, half up
// (math.Round rounds half away from zero, which is up for d >= 0). The stored
// figure lies between two database centroids; a kilometre-exact number would
// claim a precision the data does not have.
func roundKm10(d float64) int { return int(math.Round(d/10)) * 10 }
