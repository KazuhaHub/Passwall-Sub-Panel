package domain

import "sort"

// Why location and not a bigger number.
//
// A raw count of concurrent IPs is a poor sharing signal and a good way to
// punish the wrong people: one household behind CGNAT reads as many IPs, a
// phone moving between wifi and cellular reads as two, and a mobile user on a
// train reads as a stream of them. Raising the cap to spare those users
// raises it for a sharer too, so the knob has no setting that separates them.
//
// Concurrent presence in two COUNTRIES separates them. It is rare by
// accident and routine when an account is shared, and it does not scale with
// how mobile a legitimate user is. The node sees the client's source address
// BEFORE the proxy, so this is the person's real location, not the node's.
//
// It is a signal, not a verdict. Split tunnelling, a corporate VPN, a chained
// proxy, and a family member travelling all produce it honestly. Everything
// built on this must be able to say "suspected", and the automatic actions
// must be reversible. See docs/connection-limits.md §12.

// LiveIPSpread is where one user's concurrent connections are, geographically.
type LiveIPSpread struct {
	UserID int64
	// Countries are the distinct ISO codes among this user's live IPs,
	// sorted. Length >= 2 is the signal.
	Countries []string
	// Cities are the distinct "CC/City" pairs, sorted. A weaker signal kept
	// separate: two cities in one country is common and normal (a commute, a
	// mobile carrier's NAT pools), so it is reported but never escalated on
	// its own.
	Cities []string
	// Located and Unlocated split the user's live IPs by whether geo could
	// place them. Unlocated is not zero-risk and not risk — it is absence of
	// evidence, and it has to stay visible or a fleet with a stale database
	// silently reads as "nobody is sharing".
	Located   int
	Unlocated int
	// GeoAvailable is false when geo lookup is off or unusable. Then every
	// IP is Unlocated for a reason that has nothing to do with the user, and
	// no detection conclusion may be drawn at all.
	GeoAvailable bool
}

// Suspicious reports concurrent presence in two or more countries.
//
// False when geo is unavailable, deliberately. With no database the honest
// answer is "unknown", and returning false makes the caller inert rather than
// making it accuse everyone or no one. The caller must surface GeoAvailable
// separately; a detector that silently stops detecting is the same failure
// shape as a cap that silently stops capping.
func (s LiveIPSpread) Suspicious() bool {
	return s.GeoAvailable && len(s.Countries) >= 2
}

// GeoLookup resolves IPs to locations. Narrow on purpose so the domain does
// not depend on the geo service, and so a test can supply a fixed map.
// Implementations return no entry for an IP they cannot place.
type GeoLookup func(ips []string) map[string]GeoLocation

// SpreadOf classifies one user's live IPs by location.
//
// geoAvailable is passed in rather than inferred from an empty result: "the
// database is off" and "every IP in this sample is unresolvable" are
// different facts that happen to look identical here, and conflating them
// would let a broken geo database read as a clean fleet.
func SpreadOf(u UserLiveIPs, lookup GeoLookup, geoAvailable bool) LiveIPSpread {
	out := LiveIPSpread{UserID: u.UserID, GeoAvailable: geoAvailable}
	if !geoAvailable || lookup == nil || len(u.IPs) == 0 {
		out.Unlocated = len(u.IPs)
		return out
	}
	located := lookup(u.IPs)
	countries := map[string]struct{}{}
	cities := map[string]struct{}{}
	for _, ip := range u.IPs {
		g, ok := located[ip]
		if !ok || g.Empty() || g.CountryCode == "" {
			// A location with no country code cannot contribute to the
			// signal even if it carries a city, so it counts as unlocated
			// rather than as a country of its own.
			out.Unlocated++
			continue
		}
		out.Located++
		countries[g.CountryCode] = struct{}{}
		if g.City != "" {
			cities[g.CountryCode+"/"+g.City] = struct{}{}
		}
	}
	out.Countries = sortedKeys(countries)
	out.Cities = sortedKeys(cities)
	return out
}

func sortedKeys(m map[string]struct{}) []string {
	if len(m) == 0 {
		return nil
	}
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
