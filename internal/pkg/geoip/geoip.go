// Package geoip resolves IPs to a domain.GeoLocation using a local
// MaxMind-format (.mmdb) database — fully offline, no external calls, no
// caching needed (the memory-mapped DB IS the lookup).
//
// It is a thin adapter over authcore/geoip. The schema handling — MaxMind /
// GeoLite2's nested objects, ipinfo Lite's flat strings, the country_code-only
// variant, the name/city/region fallbacks — lives there, along with the address
// filtering. This package's job is to keep PSP's own surface stable: the
// package path and the four functions its callers already use, and the
// conversion into the panel's domain type.
//
// Two behaviours are deliberately different from the implementation this
// replaced, both from authcore's wider handling. Multicast addresses are now
// treated as unresolvable rather than looked up, and a record carrying only
// flat name/city/region/country_code values (which the old mapping left empty)
// now fills those fields. Neither affects account decisions; both are display
// values, and both are covered by authcore's own fixtures.
package geoip

import (
	authcoregeoip "github.com/KazuhaHub/authcore/geoip"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
)

// DBInfo describes a loaded database for the admin status view.
type DBInfo struct {
	Type        string // Metadata.DatabaseType, e.g. "GeoLite2-City"
	BuildEpoch  uint   // Unix seconds the DB was built (last update)
	Granularity string // "city" or "country"
}

// Reader wraps an open .mmdb database. Safe for concurrent Lookup (the
// underlying maxminddb.Reader is thread-safe); the geo service owns its
// lifecycle (open/close/reload). A nil *Reader is safe to call every method on,
// answering as "no database loaded", so a caller that has not opened one yet
// does not need a nil check at each call site.
type Reader struct {
	inner *authcoregeoip.Reader
}

// Open opens an .mmdb file.
func Open(path string) (*Reader, error) {
	r, err := authcoregeoip.Open(path)
	if err != nil {
		return nil, err
	}
	return &Reader{inner: r}, nil
}

// Close releases the database.
func (r *Reader) Close() error {
	if r == nil {
		return nil
	}
	return r.inner.Close()
}

// Info returns metadata for the status view.
func (r *Reader) Info() DBInfo {
	if r == nil {
		return DBInfo{}
	}
	info := r.inner.Info()
	return DBInfo{Type: info.Type, BuildEpoch: info.BuildEpoch, Granularity: info.Granularity}
}

// Lookup resolves one IP. A zero GeoLocation with a nil error means "unknown" —
// an unparseable, non-routable or unmapped address — and a non-nil error means
// the database itself could not be read (a corrupt file, for example), not that
// the address was absent.
func (r *Reader) Lookup(ip string) (domain.GeoLocation, error) {
	if r == nil {
		return domain.GeoLocation{}, nil
	}
	loc, err := r.inner.Lookup(ip)
	if err != nil {
		return domain.GeoLocation{}, err
	}
	return toDomain(loc), nil
}

// IsResolvable reports whether ip is a routable address worth looking up.
// Loopback, private, link-local, unspecified and MULTICAST addresses are not in
// any geolocation database, so looking one up would only produce an empty answer
// more slowly.
func IsResolvable(ip string) bool { return authcoregeoip.IsResolvable(ip) }

// toDomain converts a Location into the panel's own type. It is a named
// function rather than four inlined assignments so the conversion is testable on
// its own: the schema work behind it is verified by authcore's fixtures, which
// this package does not re-implement and must not re-verify.
func toDomain(l authcoregeoip.Location) domain.GeoLocation {
	return domain.GeoLocation{
		CountryCode: l.CountryCode,
		Country:     l.Country,
		Region:      l.Region,
		City:        l.City,
	}
}
