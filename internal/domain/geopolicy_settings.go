package domain

import "strings"

// GeoPolicySettings is the flat, storage-shaped form of a policy: what an
// admin actually types, and what the scoped-settings layer resolves per user.
//
// Separate from GeoAnomalyPolicy on purpose. The settings layer resolves
// group over global by REPLACING whole values (there is no per-user layer),
// and a stored 0 there means "never configured"; this is that already-merged
// result, which GeoPolicyFromSettings turns into defaults and validates.
//
// Keeping the two apart also means a stored value can be nonsense — an admin
// typed it, or it came from an older schema — without the judging code ever
// seeing an unusable policy.
type GeoPolicySettings struct {
	Scope                                        string
	MaxPlaces, MaxRegions, MaxCities             int
	FlagAfterPolls, ClearAfterPolls              int
	MinPlacedRatio                               float64
	CoTravel                                     string
	AllowAnywhere                                bool
	BanEnabled                                   bool
	BanMaxCountries, BanMaxRegions, BanMaxCities int
	BanAfterPolls, BanDurationMinutes            int
}

// GeoPolicyFromSettings turns stored settings into a policy that is safe to
// judge with.
//
// A ZERO value means "never configured", not "zero tolerance". A fresh
// install has empty settings, and reading those literally would give
// MaxPlaces 0 and FlagAfterPolls 0 — flagging every connected user
// immediately, on their first poll, including one sitting at home — and a
// BanAfterPolls of 0 would suspend on one sample. So each unset field falls
// back to the shipped default rather than to its zero.
//
// This is the single place that distinction is made. Everything downstream
// receives a sanitised policy and does not have to ask whether a 0 was meant.
func GeoPolicyFromSettings(s GeoPolicySettings) GeoAnomalyPolicy {
	p := DefaultGeoPolicy()
	switch sc := GeoScope(strings.ToLower(strings.TrimSpace(s.Scope))); {
	case sc == "":
		// Never configured: keep the default (city).
	case sc.Valid():
		p.Scope = sc
	default:
		// Explicit, not left to sanitized(): the default is now the FINEST
		// tier, so "invalid keeps the default" would make a typo judge
		// cities. An unrecognised value judges countries only.
		p.Scope = GeoScopeCountry
	}
	// Every int guard is load-bearing even where sanitized() would repair a
	// 0 to the same number today: that is a coincidence of the current
	// defaults, not a property, and raising a default would otherwise make
	// an unset setting silently strict.
	setIfPositive(&p.MaxPlaces, s.MaxPlaces)
	setIfPositive(&p.MaxRegions, s.MaxRegions)
	setIfPositive(&p.MaxCities, s.MaxCities)
	setIfPositive(&p.FlagAfterPolls, s.FlagAfterPolls)
	setIfPositive(&p.ClearAfterPolls, s.ClearAfterPolls)
	setIfPositive(&p.BanMaxCountries, s.BanMaxCountries)
	setIfPositive(&p.BanMaxRegions, s.BanMaxRegions)
	setIfPositive(&p.BanMaxCities, s.BanMaxCities)
	setIfPositive(&p.BanAfterPolls, s.BanAfterPolls)
	setIfPositive(&p.BanDurationMinutes, s.BanDurationMinutes)
	if s.MinPlacedRatio > 0 {
		p.MinPlacedRatio = s.MinPlacedRatio
	}
	// AllowAnywhere and BanEnabled have no "unset" — false IS the default,
	// and a group that sets one true is making a choice the global default
	// cannot express as a zero. Carried through verbatim.
	p.AllowAnywhere = s.AllowAnywhere
	p.BanEnabled = s.BanEnabled
	p.CoTravel = parseCoTravel(s.CoTravel)
	return p.sanitized()
}

func setIfPositive(dst *int, v int) {
	if v > 0 {
		*dst = v
	}
}

// parseCoTravel turns the stored text into country sets: one set per LINE,
// members comma-separated.
//
// Upper-cased because ObserveGeo upper-cases the country codes it folds; a
// set typed as "jp,tw" that silently never matched would be
// indistinguishable from one that was ignored, and an admin would have no
// way to tell which.
//
// A token containing "/" is dropped. It is the "CC/Region" shape v1's region
// scope suggested, and co-travel folds countries only, so such a token could
// never match anything — it would sit in the set looking like a rule while
// doing nothing. A set left empty is dropped with it.
//
// Lines that share a country are kept as written, not rejected or rewritten:
// the fold merges them transitively, which is what an admin who writes
// "CN,HK" and "HK,MO" means. Rejecting the overlap instead would make the
// admin restate the same intent as one longer line.
func parseCoTravel(raw string) [][]string {
	var out [][]string
	for _, line := range strings.Split(raw, "\n") {
		var set []string
		for _, part := range strings.Split(line, ",") {
			if p := strings.ToUpper(strings.TrimSpace(part)); p != "" && !strings.Contains(p, "/") {
				set = append(set, p)
			}
		}
		if len(set) > 0 {
			out = append(out, set)
		}
	}
	return out
}
