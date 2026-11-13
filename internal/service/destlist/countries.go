package destlist

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/netip"
	"os"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/oschwald/maxminddb-golang"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
)

// ErrGeoIPUnavailable means a country cannot be expanded because there is no
// usable GeoIP database. A rule that needs the expansion is SKIPPED and the
// status says so; it is never treated as compiled, because an unexpanded
// "deny CN" that looks active is the silent fail-open this exists to prevent.
// It classifies as ErrUnavailable (HTTP 503).
var ErrGeoIPUnavailable = fmt.Errorf("%w: geoip database unavailable", domain.ErrUnavailable)

// MMDBLocator names the GeoIP database in use. The geo service implements it,
// so a country is expanded from the very file the risk center places addresses
// with — one database, never two that could disagree.
type MMDBLocator interface {
	// ActiveMMDBPath is the absolute path of the active .mmdb, "" when there
	// is none.
	ActiveMMDBPath(ctx context.Context) string
}

// maxCachedExpansions bounds the cache: one entry per distinct country set in
// use, and the sets in use are the handful of source rules an admin has made.
const maxCachedExpansions = 32

// CountryExpander turns ISO country codes into the merged, canonical address
// ranges a GeoIP database maps to them, caching the result by the database's
// digest and the country set. Safe for concurrent use; expansions are
// serialised (one walk of a database at a time is plenty).
type CountryExpander struct {
	locator MMDBLocator

	mu        sync.Mutex
	hash      fileHash
	cache     map[string][]string
	order     []string
	expansion int
}

type fileHash struct {
	path string
	size int64
	mod  time.Time
	sum  string
}

// NewCountryExpander returns an expander reading the database loc names. A nil
// locator is "no database".
func NewCountryExpander(loc MMDBLocator) *CountryExpander {
	return &CountryExpander{locator: loc, cache: map[string][]string{}}
}

// Expansions is how many full walks of a database this expander has made, for
// tests of the cache.
func (e *CountryExpander) Expansions() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.expansion
}

func (e *CountryExpander) path(ctx context.Context) (string, error) {
	if e == nil || e.locator == nil {
		return "", ErrGeoIPUnavailable
	}
	p := e.locator.ActiveMMDBPath(ctx)
	if p == "" {
		return "", ErrGeoIPUnavailable
	}
	return p, nil
}

// digestLocked hashes the active database, remembering the answer for as long
// as the file's path, size and modification time do not change: hashing tens
// of megabytes at every compile would be wasted work, and a file that changed
// in any of those three has to be re-read anyway.
func (e *CountryExpander) digestLocked(path string) (string, error) {
	fi, err := os.Stat(path)
	if err != nil || fi.IsDir() {
		return "", fmt.Errorf("%w: %v", ErrGeoIPUnavailable, err)
	}
	if e.hash.path == path && e.hash.size == fi.Size() && e.hash.mod.Equal(fi.ModTime()) && e.hash.sum != "" {
		return e.hash.sum, nil
	}
	f, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("%w: %v", ErrGeoIPUnavailable, err)
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", fmt.Errorf("%w: %v", ErrGeoIPUnavailable, err)
	}
	e.hash = fileHash{path: path, size: fi.Size(), mod: fi.ModTime(), sum: hex.EncodeToString(h.Sum(nil))}
	return e.hash.sum, nil
}

// ActivePath is the path of the database in use, "" when there is none. A
// caller that must tell "no database" from "a database that cannot be read"
// asks here first.
func (e *CountryExpander) ActivePath(ctx context.Context) string {
	if e == nil || e.locator == nil {
		return ""
	}
	return e.locator.ActiveMMDBPath(ctx)
}

// DatabaseSHA256 is the digest of the active database: the half of the cache
// key that says "this data", and what a change detector compares to learn that
// a database update changed what a country means.
func (e *CountryExpander) DatabaseSHA256(ctx context.Context) (string, error) {
	path, err := e.path(ctx)
	if err != nil {
		return "", err
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.digestLocked(path)
}

// CIDRs returns the canonical ranges of every given country, merged together
// and sorted bytewise (the order the wire policy validates). Codes are
// case-insensitive and repeats ignored; a code that is not two letters is a
// validation error; a country the database does not hold contributes nothing.
// With no database the error is ErrGeoIPUnavailable. The slice is the
// caller's own.
func (e *CountryExpander) CIDRs(ctx context.Context, countries []string) ([]string, error) {
	set, ok := domain.NormalizeDestCountries(countries)
	if !ok {
		return nil, fmt.Errorf("%w: countries must be ISO 3166-1 alpha-2 codes", domain.ErrValidation)
	}
	if len(set) == 0 {
		return nil, nil
	}
	path, err := e.path(ctx)
	if err != nil {
		return nil, err
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	sum, err := e.digestLocked(path)
	if err != nil {
		return nil, err
	}
	key := sum + "|" + strings.Join(set, ",")
	if hit, ok := e.cache[key]; ok {
		return slices.Clone(hit), nil
	}
	out, err := expandCountries(path, set)
	if err != nil {
		return nil, err
	}
	e.expansion++
	if len(e.order) >= maxCachedExpansions {
		delete(e.cache, e.order[0])
		e.order = e.order[1:]
	}
	e.cache[key] = out
	e.order = append(e.order, key)
	return slices.Clone(out), nil
}

// expandCountries walks every network of the database at path and collects
// those whose country is in set.
//
// The record is read as MaxMind's schema first (country.iso_code), which is
// what GeoLite2, DB-IP and the like use; the first record that does not fit
// (ipinfo Lite keeps the NAME in country and the code in country_code) restarts
// the walk with the flat reader. SkipAliasedNetworks, or an IPv6 database would
// yield its IPv4 subtree several times over.
func expandCountries(path string, set []string) ([]string, error) {
	r, err := maxminddb.Open(path)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrGeoIPUnavailable, err)
	}
	defer r.Close()
	want := make(map[string]bool, len(set))
	for _, c := range set {
		want[c] = true
	}
	for _, flat := range []bool{false, true} {
		picked, retry, err := walkNetworks(r, want, flat)
		if err != nil {
			return nil, fmt.Errorf("%w: %v", ErrGeoIPUnavailable, err)
		}
		if retry {
			continue
		}
		merged := MergePrefixes(picked)
		out := make([]string, 0, len(merged))
		for _, p := range merged {
			out = append(out, p.String())
		}
		slices.Sort(out)
		return out, nil
	}
	return nil, fmt.Errorf("%w: the database has no country records this panel can read", ErrGeoIPUnavailable)
}

type maxmindCountry struct {
	Country struct {
		ISOCode string `maxminddb:"iso_code"`
	} `maxminddb:"country"`
	CountryCode string `maxminddb:"country_code"`
}

type flatCountry struct {
	CountryCode string `maxminddb:"country_code"`
}

func walkNetworks(r *maxminddb.Reader, want map[string]bool, flat bool) (picked []netip.Prefix, retry bool, err error) {
	nets := r.Networks(maxminddb.SkipAliasedNetworks)
	first := true
	for nets.Next() {
		var code string
		var ipnet interface {
			String() string
		}
		if flat {
			var rec flatCountry
			n, derr := nets.Network(&rec)
			if derr != nil {
				return nil, false, derr
			}
			code, ipnet = rec.CountryCode, n
		} else {
			var rec maxmindCountry
			n, derr := nets.Network(&rec)
			if derr != nil {
				if first {
					return nil, true, nil
				}
				return nil, false, derr
			}
			code = rec.Country.ISOCode
			if code == "" {
				code = rec.CountryCode
			}
			ipnet = n
		}
		first = false
		if !want[strings.ToUpper(code)] {
			continue
		}
		p, perr := netip.ParsePrefix(ipnet.String())
		if perr != nil {
			continue
		}
		if p.Addr().Is4In6() && p.Bits() >= 96 {
			p = netip.PrefixFrom(p.Addr().Unmap(), p.Bits()-96)
		}
		picked = append(picked, p)
	}
	if err := nets.Err(); err != nil {
		return nil, false, err
	}
	return picked, false, nil
}

// MergePrefixes returns the smallest canonical set covering exactly the same
// addresses as in: masked, sorted by address, overlaps and contained prefixes
// removed, and every pair of sibling prefixes (the two halves of one larger
// prefix) joined, repeatedly. Sorted by address (IPv4 before IPv6); callers
// that need the wire's bytewise order sort the strings.
func MergePrefixes(in []netip.Prefix) []netip.Prefix {
	if len(in) == 0 {
		return nil
	}
	ps := make([]netip.Prefix, 0, len(in))
	for _, p := range in {
		ps = append(ps, p.Masked())
	}
	slices.SortFunc(ps, func(a, b netip.Prefix) int {
		if c := a.Addr().Compare(b.Addr()); c != 0 {
			return c
		}
		return a.Bits() - b.Bits() // the larger prefix first
	})
	var out []netip.Prefix
	for _, p := range ps {
		if n := len(out); n > 0 && out[n-1].Bits() <= p.Bits() && out[n-1].Contains(p.Addr()) {
			continue
		}
		out = append(out, p)
		// Siblings are adjacent in address order, so joining only ever has to
		// look at the top of the stack; a join can make the new top a sibling
		// of the one beneath it, hence the loop.
		for n := len(out); n >= 2; n = len(out) {
			a, b := out[n-2], out[n-1]
			if !siblings(a, b) {
				break
			}
			out = append(out[:n-2], netip.PrefixFrom(a.Addr(), a.Bits()-1))
		}
	}
	return out
}

// siblings reports whether a and b are the two halves of one prefix: the same
// length, a the lower half (aligned to the parent), b starting right after a.
func siblings(a, b netip.Prefix) bool {
	if a.Bits() != b.Bits() || a.Bits() == 0 || a.Addr().BitLen() != b.Addr().BitLen() {
		return false
	}
	parent := netip.PrefixFrom(a.Addr(), a.Bits()-1)
	if parent.Masked().Addr() != a.Addr() {
		return false
	}
	return lastAddr(a).Next() == b.Addr()
}

// lastAddr is the highest address of p.
func lastAddr(p netip.Prefix) netip.Addr {
	b := p.Addr().AsSlice()
	for i := p.Bits(); i < len(b)*8; i++ {
		b[i/8] |= 1 << (7 - i%8)
	}
	a, _ := netip.AddrFromSlice(b)
	return a
}
