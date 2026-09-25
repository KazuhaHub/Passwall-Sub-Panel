package domain

import (
	"sort"
	"strings"
	"time"
)

// Concurrent-location anomaly detection, as a POLICY rather than a rule.
//
// The naive version — "two countries at once means sharing" — fails in both
// directions, and both failures are expensive. It accuses a user whose phone
// took one packet through a carrier's foreign PoP; it clears a sharer whose
// friend happens to be in the same country. It also flaps: a single noisy
// sample raises an alert, the next sample clears it, and the operator learns
// to ignore the alert. An anomaly detector that flaps is worse than none,
// because it converts a real signal into background noise.
//
// So every knob below exists because a specific real user would otherwise be
// wrongly judged:
//
//   - SCOPE AND TIERS — a location is judged at three tiers, country, region
//     (province) and city, and the scope names the finest tier judged. Two
//     countries at once is rare by accident; two provinces is the signal an
//     operator in a large country actually asks about; two cities is every
//     subscriber whose home broadband and phone carrier exit in different
//     cities of one province. One threshold over one opaque "place" could not
//     say all three, which is why v1 had to choose between country (blind
//     inside a country) and city (constant false alarms).
//   - TOLERANCE — per tier, because the tiers differ in how normal a second
//     one is: one extra country or province may be a work VPN or a family
//     member, and the city tolerance is one higher still to absorb the
//     home-and-phone pair. Each is a number, not a hardcoded 2.
//   - HYSTERESIS — a place must persist across several polls before it
//     counts, and must be gone for several before the flag clears. This is
//     what turns a jittery boolean into a stable one; it is the single most
//     important knob here and the one a naive implementation always omits.
//   - EXEMPTION — some principals legitimately appear anywhere: a travelling
//     account, a shared team credential, an operator's own test account.
//     They must be excludable outright rather than by raising the threshold
//     for everyone.
//   - CO-TRAVEL SETS — a user who genuinely uses two countries (a border
//     commuter, a JP/TW dual presence) should not be permanently flagged.
//     Naming the pair is more honest than raising their tolerance, because
//     it stays specific: a THIRD country still flags.
//   - UNKNOWN — with geo off, or with most addresses unplaceable, the honest
//     verdict is "cannot say". Collapsing that into "clean" is how a stale
//     database silently turns detection off.
//
// What to DO about a verdict is mostly decided elsewhere. Suspicion is not
// proof — split tunnelling, a corporate VPN and a travelling family member
// all produce this signal honestly — so the default response is a bell entry
// for an admin. The one automatic step, suspension, is off unless a scope
// turns it on, and this package only says when it is DUE (GeoVerdict.BanDue):
// on its own, looser tolerances and its own, longer streak, only for a
// Flagged verdict. Applying it, time-boxing it and lifting it live in the
// traffic poll, and every part of that has to stay reversible.

// GeoLookup resolves IPs to locations. Narrow on purpose so the domain does
// not depend on the geo service, and so a test can supply a fixed map.
// Implementations return no entry for an IP they cannot place.
type GeoLookup func(ips []string) map[string]GeoLocation

// GeoScope is the finest tier a policy judges. Every coarser tier is judged
// too, each against its own tolerance.
type GeoScope string

const (
	// GeoScopeOff disables location-based detection entirely. Distinct from
	// an unavailable database: this is a deliberate choice and reports as
	// Disabled, not Unknown.
	GeoScopeOff GeoScope = "off"
	// GeoScopeCountry judges countries only. It is also where an
	// unrecognised stored scope lands: the coarsest tier accuses least.
	GeoScopeCountry GeoScope = "country"
	// GeoScopeRegion judges countries and regions.
	GeoScopeRegion GeoScope = "region"
	// GeoScopeCity judges all three tiers, and is the default (D1). v1 kept
	// city off the default because one "place" threshold of 1 fired on
	// every commute; with its own tolerance of 2 the city tier absorbs the
	// home-and-phone pair and still sees a third city at once.
	GeoScopeCity GeoScope = "city"
)

// Valid reports whether the scope is one this package understands. An
// unrecognised scope must never silently behave like a permissive default.
func (s GeoScope) Valid() bool {
	switch s {
	case GeoScopeOff, GeoScopeCountry, GeoScopeRegion, GeoScopeCity:
		return true
	}
	return false
}

// GeoTier names the tier a sample was over at. It is stored with the streak
// and shown to the admin, so the values are stable strings.
type GeoTier string

const (
	// GeoTierNone — within every judged tolerance (or not judged at all).
	GeoTierNone    GeoTier = ""
	GeoTierCountry GeoTier = "country"
	GeoTierRegion  GeoTier = "region"
	GeoTierCity    GeoTier = "city"
)

// GeoTolerances is how many distinct places each tier may hold at once
// before the sample is over at that tier. Regions and Cities are counted
// inside ONE country (the widest), never summed across countries: two
// cities in Japan and two in Germany are the country tier's business.
type GeoTolerances struct{ Countries, Regions, Cities int }

// GeoBanMaxDurationMinutes caps an automatic suspension at a week. The ban is
// time-boxed so that a false positive heals itself without an admin; a value
// long enough to be permanent in practice would defeat that.
const GeoBanMaxDurationMinutes = 10080

// GeoState is a verdict, deliberately not a bool. Half of these states mean
// "do not act", and each for a different reason an operator needs to see.
type GeoState string

const (
	// GeoStateDisabled — detection is switched off by policy.
	GeoStateDisabled GeoState = "disabled"
	// GeoStateExempt — this principal is allowed to be anywhere.
	GeoStateExempt GeoState = "exempt"
	// GeoStateUnknown — geo is unavailable, or too few addresses could be
	// placed to draw a conclusion. NOT clean; the evidence is missing.
	GeoStateUnknown GeoState = "unknown"
	// GeoStateIdle — nobody is connected.
	GeoStateIdle GeoState = "idle"
	// GeoStateClean — within tolerance.
	GeoStateClean GeoState = "clean"
	// GeoStateSuspect — over tolerance, but not yet for long enough to
	// flag. Visible to an operator, never acted on automatically. This is
	// the state that keeps hysteresis honest instead of hiding the ramp.
	GeoStateSuspect GeoState = "suspect"
	// GeoStateFlagged — over tolerance and sustained.
	GeoStateFlagged GeoState = "flagged"
)

// Actionable reports whether an automatic response may consider this verdict.
// Only Flagged qualifies: Suspect is deliberately below the line, and every
// remaining state means the detector is not in a position to judge.
func (s GeoState) Actionable() bool { return s == GeoStateFlagged }

// GeoAnomalyPolicy is the resolved, effective policy for one principal.
//
// Plain values, not the pointer tri-state the limits use: PSP's scoped
// settings resolve group over global by replacing whole values, and there is
// no per-user layer, so by the time a policy exists every field is decided.
// GeoPolicyFromSettings is where "0 means never configured" is resolved.
type GeoAnomalyPolicy struct {
	// Scope is the finest tier judged.
	Scope GeoScope
	// MaxPlaces is the COUNTRY tolerance: how many distinct countries may be
	// occupied CONCURRENTLY before the sample counts as over. 1 means "two at
	// once is over". The name predates the tiers and is the stored key
	// (geo_anomaly.max_places), kept for compatibility.
	MaxPlaces int
	// MaxRegions is the region tolerance inside the widest country. Judged
	// at scope region and city.
	MaxRegions int
	// MaxCities is the city tolerance inside the widest country. Judged at
	// scope city only.
	MaxCities int
	// FlagAfterPolls is how many consecutive over-tolerance samples are
	// required before the state becomes Flagged. Below that it is Suspect.
	// 1 disables hysteresis on the way up and is not recommended.
	FlagAfterPolls int
	// ClearAfterPolls is how many consecutive within-tolerance samples are
	// required before a Flagged state clears. Asymmetry is intentional:
	// clearing should be slower than flagging so a sharer cannot idle out
	// of the flag between checks.
	ClearAfterPolls int
	// AllowAnywhere exempts the principal outright.
	AllowAnywhere bool
	// CoTravel groups COUNTRIES that do not count as separate from each
	// other. Each entry is a set of upper-case country codes; occupying two
	// in the same set counts as one country. A country outside every set
	// still counts. Regions and cities are never folded across countries.
	CoTravel [][]string
	// MinPlacedRatio is the fraction of a user's live addresses that must be
	// placeable before any conclusion is drawn. Below it the verdict is
	// Unknown. Guards against a stale or partial database quietly turning
	// detection into a clean bill of health.
	MinPlacedRatio float64

	// Automatic suspension (D3). Off by default. Its tolerances and streak
	// are its own, and sanitized() keeps each ban tolerance at or above the
	// flag tolerance of the same tier, so a sample over the ban line is
	// always over the flag line too — nobody can be suspended for a spread
	// the flag itself calls normal.
	BanEnabled      bool
	BanMaxCountries int
	BanMaxRegions   int
	BanMaxCities    int
	// BanAfterPolls is how many CONSECUTIVE samples over the ban tolerances
	// make a suspension due. Separate from FlagAfterPolls, and longer by
	// default, because the cost of a false positive is lost service rather
	// than a bell entry.
	BanAfterPolls int
	// BanDurationMinutes is how long an automatic suspension lasts before it
	// lifts itself. 1..GeoBanMaxDurationMinutes.
	BanDurationMinutes int
}

// FlagTolerances is what a sample is judged against for the flag.
func (p GeoAnomalyPolicy) FlagTolerances() GeoTolerances {
	return GeoTolerances{Countries: p.MaxPlaces, Regions: p.MaxRegions, Cities: p.MaxCities}
}

// BanTolerances is what a sample is judged against for the ban streak.
func (p GeoAnomalyPolicy) BanTolerances() GeoTolerances {
	return GeoTolerances{Countries: p.BanMaxCountries, Regions: p.BanMaxRegions, Cities: p.BanMaxCities}
}

// BanDuration is the automatic suspension's time box, read through
// sanitized() so a caller holding a raw policy can never schedule a
// zero-length suspension (lifted before anyone sees it) or an unbounded one.
func (p GeoAnomalyPolicy) BanDuration() time.Duration {
	return time.Duration(p.sanitized().BanDurationMinutes) * time.Minute
}

// DefaultGeoPolicy is the shipped default (D1, D3): judged down to the city
// tier with tolerances countries 1, regions 1, cities 2; three polls up and
// six down; automatic suspension off, with its own looser tolerances
// (1/2/3), six polls and a 60-minute time box ready for a scope that turns
// it on.
//
// The numbers are starting points chosen to be conservative in the direction
// that matters — they under-report rather than accuse — and they are meant to
// be tuned against a real distribution, not trusted as tuned.
func DefaultGeoPolicy() GeoAnomalyPolicy {
	return GeoAnomalyPolicy{
		Scope:              GeoScopeCity,
		MaxPlaces:          1,
		MaxRegions:         1,
		MaxCities:          2,
		FlagAfterPolls:     3,
		ClearAfterPolls:    6,
		MinPlacedRatio:     0.5,
		BanEnabled:         false,
		BanMaxCountries:    1,
		BanMaxRegions:      2,
		BanMaxCities:       3,
		BanAfterPolls:      6,
		BanDurationMinutes: 60,
	}
}

// sanitized repairs a policy that cannot be obeyed as written.
//
// It errs toward NOT accusing: an unusable value becomes the conservative one
// rather than the strict one, because a misconfiguration should degrade into
// silence, not into false accusations against a whole fleet.
func (p GeoAnomalyPolicy) sanitized() GeoAnomalyPolicy {
	if !p.Scope.Valid() {
		// The coarsest tier, not the default: an unreadable scope must not
		// judge more finely than the admin could have meant.
		p.Scope = GeoScopeCountry
	}
	// 0 would flag every connected user, including one at home.
	p.MaxPlaces = max(p.MaxPlaces, 1)
	p.MaxRegions = max(p.MaxRegions, 1)
	p.MaxCities = max(p.MaxCities, 1)
	if p.FlagAfterPolls < 1 {
		p.FlagAfterPolls = 1
	}
	if p.ClearAfterPolls < 1 {
		p.ClearAfterPolls = 1
	}
	if p.MinPlacedRatio < 0 {
		p.MinPlacedRatio = 0
	}
	if p.MinPlacedRatio > 1 {
		p.MinPlacedRatio = 1
	}
	// Tier by tier, so ban-over always implies flag-over. This is also what
	// makes an unset (0) ban tolerance safe on a policy that never went
	// through GeoPolicyFromSettings.
	p.BanMaxCountries = max(p.BanMaxCountries, p.MaxPlaces)
	p.BanMaxRegions = max(p.BanMaxRegions, p.MaxRegions)
	p.BanMaxCities = max(p.BanMaxCities, p.MaxCities)
	if p.BanAfterPolls < 1 {
		// The shipped 6, deliberately not 1: a zeroed field must not turn
		// a single sample into a suspension.
		p.BanAfterPolls = DefaultGeoPolicy().BanAfterPolls
	}
	p.BanDurationMinutes = min(max(p.BanDurationMinutes, 1), GeoBanMaxDurationMinutes)
	return p
}

// foldCoTravel collapses countries that the policy says travel together, so
// a user who legitimately spans two of them reads as occupying one. Only the
// country tier is folded; regions and cities are never merged across
// countries.
//
// A set is represented by its smallest member, so folding is stable and two
// members of one set can never be counted separately. Places named in no set
// are returned untouched — the point is to excuse a SPECIFIC pairing, not to
// raise the threshold, so a third unrelated place still counts.
func (p GeoAnomalyPolicy) foldCoTravel(places map[string]struct{}) map[string]struct{} {
	if len(p.CoTravel) == 0 || len(places) == 0 {
		return places
	}
	canon := map[string]string{}
	for _, set := range p.CoTravel {
		cleaned := make([]string, 0, len(set))
		for _, s := range set {
			if s = strings.TrimSpace(s); s != "" {
				cleaned = append(cleaned, s)
			}
		}
		if len(cleaned) == 0 {
			// Guards the indexing below, and nothing more. A ONE-member set
			// needs no special case: it folds its only place to itself, so
			// it is already the identity and excuses nothing. An earlier
			// version skipped singletons too, with a comment claiming that
			// stopped them becoming a blanket exemption — mutation testing
			// showed the two branches are behaviourally identical, so the
			// comment was describing a danger the code was not averting.
			// The property is still pinned by a test; it is delivered by
			// the fold being an identity, not by a guard.
			continue
		}
		sort.Strings(cleaned)
		rep := cleaned[0]
		for _, s := range cleaned {
			canon[s] = rep
		}
	}
	out := make(map[string]struct{}, len(places))
	for place := range places {
		if rep, ok := canon[place]; ok {
			out[rep] = struct{}{}
			continue
		}
		out[place] = struct{}{}
	}
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
