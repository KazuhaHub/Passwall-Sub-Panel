package domain

import (
	"sort"
	"strings"
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
//   - SCOPE — two cities is a commute, a carrier NAT pool, or a CDN-ish exit.
//     Two countries is rare by accident. Country is the default; city is
//     available for deployments that know their population is local, and is
//     never the default because in most fleets it means constant false alarms.
//   - TOLERANCE — one extra place may be entirely normal (a work VPN, a
//     second home, a family member abroad). The threshold is a number, not
//     a hardcoded 2.
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
// Nothing here decides what to DO about a verdict. Suspicion is not proof —
// split tunnelling, a corporate VPN and a travelling family member all
// produce this signal honestly — so the response ladder lives elsewhere and
// every automatic step in it has to be reversible.

// GeoLookup resolves IPs to locations. Narrow on purpose so the domain does
// not depend on the geo service, and so a test can supply a fixed map.
// Implementations return no entry for an IP they cannot place.
type GeoLookup func(ips []string) map[string]GeoLocation

// GeoScope is the granularity at which two observations count as different
// places.
type GeoScope string

const (
	// GeoScopeOff disables location-based detection entirely. Distinct from
	// an unavailable database: this is a deliberate choice and reports as
	// Disabled, not Unknown.
	GeoScopeOff GeoScope = "off"
	// GeoScopeCountry is the default. Rare by accident, and it does not
	// scale with how mobile a legitimate user is.
	GeoScopeCountry GeoScope = "country"
	// GeoScopeRegion is a middle setting for large countries where
	// cross-region concurrency is itself unusual.
	GeoScopeRegion GeoScope = "region"
	// GeoScopeCity is the strictest and is for deployments that know their
	// users are local. In a general fleet it fires on commutes and carrier
	// NAT pools, which is why it is never the default.
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
// Pointer fields are the tri-state the rest of PSP already uses for limits:
// nil means "inherit", so a group can state a policy and a user can override
// one field of it without restating the others. See ResolveGeoPolicy.
type GeoAnomalyPolicy struct {
	Scope GeoScope
	// MaxPlaces is how many distinct places may be occupied CONCURRENTLY
	// before the sample counts as over tolerance. 1 means "two at once is
	// over"; 2 tolerates a genuine second location.
	MaxPlaces int
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
	// CoTravel groups places that do not count as separate from each other.
	// Each entry is a set of place codes; occupying two places in the same
	// set counts as one place. A place outside every set still counts.
	CoTravel [][]string
	// MinPlacedRatio is the fraction of a user's live addresses that must be
	// placeable before any conclusion is drawn. Below it the verdict is
	// Unknown. Guards against a stale or partial database quietly turning
	// detection into a clean bill of health.
	MinPlacedRatio float64
}

// DefaultGeoPolicy is the shipped default: country granularity, one extra
// place tolerated as noise, three polls up and six down.
//
// The numbers are starting points chosen to be conservative in the direction
// that matters — they under-report rather than accuse — and they are meant to
// be tuned against a real distribution, not trusted as tuned. Nothing here is
// derived from this deployment's data because that data does not exist yet.
func DefaultGeoPolicy() GeoAnomalyPolicy {
	return GeoAnomalyPolicy{
		Scope:           GeoScopeCountry,
		MaxPlaces:       1,
		FlagAfterPolls:  3,
		ClearAfterPolls: 6,
		MinPlacedRatio:  0.5,
	}
}

// GeoPolicyOverrides is the sparse, per-principal form. A nil field inherits.
type GeoPolicyOverrides struct {
	Scope           *GeoScope
	MaxPlaces       *int
	FlagAfterPolls  *int
	ClearAfterPolls *int
	AllowAnywhere   *bool
	CoTravel        *[][]string
	MinPlacedRatio  *float64
}

// ResolveGeoPolicy layers user over group over the deployment default, one
// field at a time — the same inheritance the traffic and connection limits
// use, so an admin learns one model.
//
// Field-wise rather than whole-object: a user who may travel anywhere should
// not have to restate the scope and the hysteresis to say so.
func ResolveGeoPolicy(base GeoAnomalyPolicy, group, user GeoPolicyOverrides) GeoAnomalyPolicy {
	out := base
	apply := func(o GeoPolicyOverrides) {
		if o.Scope != nil {
			out.Scope = *o.Scope
		}
		if o.MaxPlaces != nil {
			out.MaxPlaces = *o.MaxPlaces
		}
		if o.FlagAfterPolls != nil {
			out.FlagAfterPolls = *o.FlagAfterPolls
		}
		if o.ClearAfterPolls != nil {
			out.ClearAfterPolls = *o.ClearAfterPolls
		}
		if o.AllowAnywhere != nil {
			out.AllowAnywhere = *o.AllowAnywhere
		}
		if o.CoTravel != nil {
			out.CoTravel = *o.CoTravel
		}
		if o.MinPlacedRatio != nil {
			out.MinPlacedRatio = *o.MinPlacedRatio
		}
	}
	apply(group)
	apply(user)
	return out.sanitized()
}

// sanitized repairs a policy that cannot be obeyed as written.
//
// It errs toward NOT accusing: an unusable value becomes the conservative one
// rather than the strict one, because a misconfiguration should degrade into
// silence, not into false accusations against a whole fleet.
func (p GeoAnomalyPolicy) sanitized() GeoAnomalyPolicy {
	if !p.Scope.Valid() {
		p.Scope = GeoScopeCountry
	}
	if p.MaxPlaces < 1 {
		// 0 would flag every connected user, including one at home.
		p.MaxPlaces = 1
	}
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
	return p
}

// placeOf projects a location onto the policy's scope. Empty means the
// address cannot contribute a place at this granularity — which is a real
// case: a database that resolves a country but no city cannot answer a
// city-scoped policy, and must count as unplaced rather than inventing one.
func (p GeoAnomalyPolicy) placeOf(g GeoLocation) string {
	cc := strings.TrimSpace(g.CountryCode)
	if cc == "" {
		// Without a country code nothing below it can be disambiguated:
		// "Springfield" is not a place until you know the country.
		return ""
	}
	switch p.Scope {
	case GeoScopeCountry:
		return cc
	case GeoScopeRegion:
		if r := strings.TrimSpace(g.Region); r != "" {
			return cc + "/" + r
		}
		return ""
	case GeoScopeCity:
		if c := strings.TrimSpace(g.City); c != "" {
			return cc + "/" + c
		}
		return ""
	}
	return ""
}

// foldCoTravel collapses places that the policy says travel together, so a
// user who legitimately spans two of them reads as occupying one.
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
