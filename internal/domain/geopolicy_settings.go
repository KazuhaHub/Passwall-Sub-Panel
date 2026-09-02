package domain

import "strings"

// GeoPolicySettings is the flat, storage-shaped form of a policy: what an
// admin actually types, and what the scoped-settings layer resolves per user.
//
// Separate from GeoAnomalyPolicy on purpose. The settings layer resolves
// user > group > global by REPLACING whole values, so the sparse pointer form
// (GeoPolicyOverrides) has nowhere to live there; this is the already-resolved
// result of that layering, which GeoPolicyFromSettings then validates.
//
// Keeping the two apart also means a stored value can be nonsense — an admin
// typed it, or it came from an older schema — without the judging code ever
// seeing an unusable policy.
type GeoPolicySettings struct {
	Scope           string
	MaxPlaces       int
	FlagAfterPolls  int
	ClearAfterPolls int
	MinPlacedRatio  float64
	CoTravel        string
	AllowAnywhere   bool
}

// GeoPolicyFromSettings turns stored settings into a policy that is safe to
// judge with.
//
// A ZERO value means "never configured", not "zero tolerance". A fresh
// install has empty settings, and reading those literally would give
// MaxPlaces 0 and FlagAfterPolls 0 — flagging every connected user
// immediately, on their first poll, including one sitting at home. So each
// unset field falls back to the shipped default rather than to its zero.
//
// This is the single place that distinction is made. Everything downstream
// receives a sanitised policy and does not have to ask whether a 0 was meant.
func GeoPolicyFromSettings(s GeoPolicySettings) GeoAnomalyPolicy {
	p := DefaultGeoPolicy()
	if sc := GeoScope(strings.TrimSpace(strings.ToLower(s.Scope))); sc.Valid() {
		p.Scope = sc
	}
	// MaxPlaces is the one field whose guard is currently redundant: the
	// shipped default is 1 and sanitized() also repairs 0 to 1, so removing
	// the check changes nothing today. Kept because that is a coincidence of
	// the current default, not a property — raising DefaultGeoPolicy's
	// MaxPlaces would make an unset setting silently strict without it.
	if s.MaxPlaces > 0 {
		p.MaxPlaces = s.MaxPlaces
	}
	if s.FlagAfterPolls > 0 {
		p.FlagAfterPolls = s.FlagAfterPolls
	}
	if s.ClearAfterPolls > 0 {
		p.ClearAfterPolls = s.ClearAfterPolls
	}
	if s.MinPlacedRatio > 0 {
		p.MinPlacedRatio = s.MinPlacedRatio
	}
	// AllowAnywhere has no "unset" — false IS the default, and a group that
	// sets it true is making a choice the global default cannot express as a
	// zero. Carried through verbatim.
	p.AllowAnywhere = s.AllowAnywhere
	p.CoTravel = parseCoTravel(s.CoTravel)
	return p.sanitized()
}

// parseCoTravel turns the stored text into place sets: one set per LINE,
// members comma-separated.
//
// Uppercased because country codes are conventionally upper and placeOf emits
// them that way; a set typed as "jp,tw" that silently never matched would be
// indistinguishable from one that was ignored, and an admin would have no way
// to tell which.
func parseCoTravel(raw string) [][]string {
	var out [][]string
	for _, line := range strings.Split(raw, "\n") {
		var set []string
		for _, part := range strings.Split(line, ",") {
			if p := strings.ToUpper(strings.TrimSpace(part)); p != "" {
				set = append(set, p)
			}
		}
		if len(set) > 0 {
			out = append(out, set)
		}
	}
	return out
}
