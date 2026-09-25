package domain

import (
	"fmt"
	"sort"
	"strings"
)

// GeoObservation is one poll's worth of evidence about one user: where the
// user's concurrent, non-excluded sources are, at every tier, independent of
// the policy's scope. The scope only decides which of these numbers
// EvaluateGeo judges; the rest stays evidence an operator can read.
type GeoObservation struct {
	UserID int64
	// Places are the distinct COUNTRIES occupied concurrently, already
	// folded through the policy's co-travel sets and sorted.
	Places []string
	// Placed and Unplaced split the user's kept sources by whether the
	// database resolved a country for them. Their sum is the sample size,
	// and it is what MinPlacedRatio is measured against.
	Placed   int
	Unplaced int
	// RegionKnown and CityKnown count the placed sources whose row also
	// named a region / a city. A coarse database leaves them low, and a
	// reader can see that the finer tiers had little to go on.
	RegionKnown, CityKnown int
	// RegionSpread is the number of distinct regions inside the widest
	// country by regions (RegionCountry; ties to the smallest code), and
	// Regions names them as sorted "CC/Region". The spread is never summed
	// across countries: that is the country tier's business, and summing
	// would count one person in two countries as a region spread too.
	RegionSpread  int
	RegionCountry string
	Regions       []string
	// CitySpread / CityCountry / Cities are the same at the city tier.
	CitySpread  int
	CityCountry string
	Cities      []string
	// Excluded, Stale and Networks describe what the sample was built from:
	// the sources set aside and why, the window addresses that were not
	// live, and how many distinct networks (/24, /48) the kept sources sit
	// in. None of them is judged; they let a reader tell a thin sample from
	// a clean one.
	Excluded GeoExcluded
	Stale    int
	Networks int
	// Spots are the kept sources by location, most-occupied first, at most
	// GeoEvidenceMaxSpots. Never an address.
	Spots []GeoSpot
	// GeoAvailable is false when lookup is switched off or unusable. Kept
	// separate from "nothing resolved" because the two look identical in
	// the numbers and mean entirely different things.
	GeoAvailable bool
}

// GeoSpot is one location a user was seen at and how many of their sources
// were there. Region and City are "" when the database did not say.
type GeoSpot struct {
	CC     string `json:"cc"`
	Region string `json:"region"`
	City   string `json:"city"`
	N      int    `json:"n"`
}

// GeoStreak is the little state carried between polls that makes the verdict
// stable. Persisted per user, and compared with != by the repository tests,
// so it must stay a comparable struct.
//
// Two counters rather than one: flagging and clearing have different
// thresholds on purpose, and a single signed counter would make an
// oscillating user drift instead of settling.
type GeoStreak struct {
	// Over is consecutive samples above tolerance, at ANY tier: a second
	// device that wanders from the next city to the next province has been
	// over the whole time, and restarting per tier would let it dodge.
	Over int
	// Under is consecutive samples within tolerance.
	Under int
	// Flagged is the latched state. It survives a single clean sample,
	// which is the whole point: without latching, a sharer who idles one
	// device for one poll clears their flag.
	Flagged bool
	// Tier is the tier of the last over-sample. It is kept while the flag is
	// latched, so a latched verdict can still say why it was raised, and is
	// "" once clean, exempt or disabled.
	Tier GeoTier
	// BanOver is consecutive samples over the BAN tolerances. Its own
	// counter because the ban has its own, looser line: a sample between the
	// flag and ban tolerances keeps the flag streak and breaks this one.
	BanOver int
}

// GeoVerdict is one evaluation's full result: the state, why, and the streak
// to carry forward.
//
// Reason is not decoration. Every state here except Clean will eventually be
// shown to a human deciding whether to act on somebody's account, and "this
// user is flagged" without "because they were in 2 places for 3 consecutive
// checks, and 1 is the tolerance" is not a basis for that decision.
type GeoVerdict struct {
	State  GeoState
	Places []string
	Reason string
	Streak GeoStreak
	// Sample is how many live addresses backed this verdict, and how many
	// of them could be placed. Carried so a reader can see a conclusion
	// drawn from two addresses for what it is.
	Placed   int
	Unplaced int
	// BanDue says an automatic suspension is due now. It has already been
	// CONSUMED: Streak.BanOver is back to 0, so the caller must act on it
	// (or record why not) in this cycle, because the next one will not
	// repeat it. That is deliberate — a suspended user goes idle, idle
	// freezes the streak, and an unconsumed streak would re-suspend on the
	// first over-sample after the lift.
	BanDue bool
	// BanTier is the coarsest tier over the ban tolerances, BanSpread the
	// count at that tier, and BanReason the admin/audit text naming the
	// places and the ban tolerance. Set only when BanDue.
	BanTier   GeoTier
	BanReason string
	BanSpread int
}

// EvaluateGeo applies a policy to one observation and the streak so far.
//
// Pure and total: same inputs, same verdict, no clock and no I/O. The
// hysteresis lives in the returned streak rather than in a field mutated in
// place, so a caller can evaluate without committing — which is what lets an
// admin preview a policy change against stored history before saving it.
//
// Order of the guards is deliberate. Disabled and Exempt come first because
// they mean "do not evaluate", not "evaluated and found clean", and an
// operator reading the state should see that distinction rather than a Clean
// they might mistake for evidence; both reset every streak, the ban streak
// included, so nothing accrued before is waiting when judging resumes. Idle,
// "all excluded" and every Unknown then FREEZE the streaks — flag and ban
// alike — because counting a sample nobody could judge as clean would let a
// sharer clear a flag by disconnecting or by routing through a relay.
//
// Only then are the tiers judged: one flag streak across all of them, named
// by the coarsest tier over, and a separate ban streak against the ban
// tolerances, which becomes BanDue only for a Flagged verdict.
func EvaluateGeo(p GeoAnomalyPolicy, obs GeoObservation, prev GeoStreak) GeoVerdict {
	p = p.sanitized()
	v := GeoVerdict{Places: obs.Places, Placed: obs.Placed, Unplaced: obs.Unplaced, Streak: prev}

	if p.Scope == GeoScopeOff {
		v.State = GeoStateDisabled
		v.Reason = "location checks are switched off for this account"
		v.Streak = GeoStreak{}
		return v
	}
	if p.AllowAnywhere {
		v.State = GeoStateExempt
		v.Reason = "this account is allowed to connect from anywhere"
		// Reset rather than freeze: if the exemption is later removed, the
		// account starts from a clean slate instead of inheriting a streak
		// accumulated while nobody was judging it.
		v.Streak = GeoStreak{}
		return v
	}

	sample := obs.Placed + obs.Unplaced
	if sample == 0 && obs.Excluded.Total() == 0 {
		v.State = GeoStateIdle
		// An idle user neither accrues nor sheds a streak. Counting idle
		// polls as clean would let a flagged account clear itself simply by
		// disconnecting for a while, which is the easiest evasion there is.
		// Addresses the upstream still remembers but that were not live are
		// idle too, and the reason says so rather than "nobody".
		if obs.Stale > 0 {
			v.Reason = fmt.Sprintf("no concurrent connections; %d address(es) seen earlier in the upstream window", obs.Stale)
		} else {
			v.Reason = "no live connections"
		}
		return v
	}
	if sample == 0 {
		// Somebody IS connected, and every source was set aside (a relay, a
		// shared exit, the ignore list). Not idle — PSP chose not to look —
		// and not clean either. Frozen, like every Unknown.
		v.State = GeoStateUnknown
		v.Reason = fmt.Sprintf("all %d concurrent address(es) are excluded (shared %d, listed %d, infrastructure %d, internal %d); no conclusion drawn",
			obs.Excluded.Total(), obs.Excluded.Shared, obs.Excluded.Listed, obs.Excluded.Infra, obs.Excluded.Internal)
		return v
	}
	if !obs.GeoAvailable {
		v.State = GeoStateUnknown
		v.Reason = "location lookup unavailable; no conclusion drawn"
		return v
	}
	if ratio := float64(obs.Placed) / float64(sample); ratio < p.MinPlacedRatio {
		v.State = GeoStateUnknown
		v.Reason = fmt.Sprintf("only %d of %d addresses could be located (%.0f%% required)",
			obs.Placed, sample, p.MinPlacedRatio*100)
		return v
	}

	flagTol := p.FlagTolerances()
	flagTier := overTier(p.Scope, obs, flagTol)
	over := flagTier != GeoTierNone
	if over {
		v.Streak.Over = prev.Over + 1
		v.Streak.Under = 0
		v.Streak.Tier = flagTier
	} else {
		v.Streak.Under = prev.Under + 1
		v.Streak.Over = 0
	}

	// The ban streak counts only samples over the BAN line, and any judged
	// sample under it breaks the run: "sustained" has to mean consecutive,
	// or a sharer seen two polls in three would eventually be suspended on
	// evidence that was never sustained.
	banTol := p.BanTolerances()
	banTier := GeoTierNone
	v.Streak.BanOver = 0
	if p.BanEnabled {
		if banTier = overTier(p.Scope, obs, banTol); banTier != GeoTierNone {
			v.Streak.BanOver = prev.BanOver + 1
		}
	}

	switch {
	case over && (prev.Flagged || v.Streak.Over >= p.FlagAfterPolls):
		v.Streak.Flagged = true
		v.State = GeoStateFlagged
		v.Reason = describeOver(flagTier, obs, flagTol) +
			fmt.Sprintf(", sustained for %d of %d checks", v.Streak.Over, p.FlagAfterPolls)
	case over:
		// Over tolerance but not yet sustained. Visible, never actionable —
		// this is the ramp, and hiding it would make the eventual flag look
		// like it came out of nowhere.
		v.Streak.Flagged = false
		v.State = GeoStateSuspect
		v.Reason = describeOver(flagTier, obs, flagTol) +
			fmt.Sprintf(", %d of %d checks so far", v.Streak.Over, p.FlagAfterPolls)
	case prev.Flagged && v.Streak.Under < p.ClearAfterPolls:
		// Latched. Clearing is deliberately slower than flagging so an
		// account cannot step just under the line between checks. The tier
		// that raised the flag stays with it, so the operator reading a
		// latched row still learns why.
		v.Streak.Flagged = true
		v.Streak.Tier = prev.Tier
		v.State = GeoStateFlagged
		v.Reason = fmt.Sprintf("within tolerance for %d of the %d checks needed to clear",
			v.Streak.Under, p.ClearAfterPolls)
		if prev.Tier != GeoTierNone {
			v.Reason += fmt.Sprintf("; flagged at the %s tier", prev.Tier)
		}
	default:
		v.Streak.Flagged = false
		v.Streak.Tier = GeoTierNone
		v.State = GeoStateClean
		if len(obs.Places) == 0 {
			v.Reason = "connected, but no address could be placed"
		} else {
			v.Reason = fmt.Sprintf("within tolerance: %d country(ies) %v, %d region(s) and %d city(ies) in the widest country; tolerances %d/%d/%d (scope %s)",
				len(obs.Places), obs.Places, obs.RegionSpread, obs.CitySpread,
				flagTol.Countries, flagTol.Regions, flagTol.Cities, p.Scope)
		}
	}

	// Only a Flagged verdict may be acted on (GeoState.Actionable). Because
	// ban tolerances are never below the flag ones, a ban-over sample is
	// always a flag-over sample, so this waits only for the flag's ramp.
	if p.BanEnabled && banTier != GeoTierNone && v.State.Actionable() && v.Streak.BanOver >= p.BanAfterPolls {
		v.BanDue = true
		v.BanTier = banTier
		v.BanSpread = spreadAt(banTier, obs)
		v.BanReason = describeOver(banTier, obs, banTol) +
			fmt.Sprintf(", sustained for %d of %d checks", v.Streak.BanOver, p.BanAfterPolls)
		v.Streak.BanOver = 0
	}
	return v
}

// overTier returns the COARSEST tier the sample is over at, or GeoTierNone.
// Coarsest, because two countries is the stronger statement than the cities
// that come with them, and the reason should lead with it. The scope decides
// which tiers are judged at all: country scope never looks below countries,
// region scope never at cities.
func overTier(scope GeoScope, obs GeoObservation, t GeoTolerances) GeoTier {
	switch {
	case len(obs.Places) > t.Countries:
		return GeoTierCountry
	case (scope == GeoScopeRegion || scope == GeoScopeCity) && obs.RegionSpread > t.Regions:
		return GeoTierRegion
	case scope == GeoScopeCity && obs.CitySpread > t.Cities:
		return GeoTierCity
	}
	return GeoTierNone
}

// spreadAt is the count judged at a tier.
func spreadAt(tier GeoTier, obs GeoObservation) int {
	switch tier {
	case GeoTierCountry:
		return len(obs.Places)
	case GeoTierRegion:
		return obs.RegionSpread
	case GeoTierCity:
		return obs.CitySpread
	}
	return 0
}

// describeOver names what was over, where, and against which tolerance — the
// numbers an operator needs before touching an account.
func describeOver(tier GeoTier, obs GeoObservation, t GeoTolerances) string {
	switch tier {
	case GeoTierRegion:
		return fmt.Sprintf("in %d regions of %s at once (%v); tolerance is %d",
			obs.RegionSpread, obs.RegionCountry, obs.Regions, t.Regions)
	case GeoTierCity:
		return fmt.Sprintf("in %d cities of %s at once (%v); tolerance is %d",
			obs.CitySpread, obs.CityCountry, obs.Cities, t.Cities)
	}
	return fmt.Sprintf("in %d countries at once (%v); tolerance is %d",
		len(obs.Places), obs.Places, t.Countries)
}

// ObserveGeo places one user's kept sources at every tier.
//
// Split from EvaluateGeo so the projection (which needs a geo database) and
// the judgement (which is arithmetic) can be tested and reasoned about
// separately — and so a stored observation can be re-judged under a different
// policy without re-querying anything. The policy matters here only for
// "off" (nothing is looked up for a principal nobody judges) and for
// co-travel, which folds countries.
//
// A row that names a country but no region or city counts at the country
// tier only. It is a placed address — the country it knows is real — but it
// is never a region or city of its own, or a coarse database would
// manufacture the finer spread.
func ObserveGeo(p GeoAnomalyPolicy, a UserAddresses, lookup GeoLookup, geoAvailable bool) GeoObservation {
	p = p.sanitized()
	obs := GeoObservation{
		UserID:       a.UserID,
		GeoAvailable: geoAvailable,
		Excluded:     a.Excluded,
		Stale:        a.Stale,
	}
	if len(a.Kept) == 0 {
		return obs
	}
	networks := map[string]struct{}{}
	for _, k := range a.Kept {
		if k.Network != "" {
			networks[k.Network] = struct{}{}
		}
	}
	obs.Networks = len(networks)
	if !geoAvailable || lookup == nil || p.Scope == GeoScopeOff {
		obs.Unplaced = len(a.Kept)
		return obs
	}

	lookupIPs := make([]string, 0, len(a.Kept))
	for _, k := range a.Kept {
		lookupIPs = append(lookupIPs, k.LookupIP)
	}
	located := lookup(lookupIPs)

	countries := map[string]struct{}{}
	regions := map[string]map[string]struct{}{}
	cities := map[string]map[string]struct{}{}
	spots := map[GeoSpot]int{}
	for _, k := range a.Kept {
		g, ok := located[k.LookupIP]
		// Upper-cased so a database answering "jp" is neither a second
		// country next to "JP" nor able to dodge a co-travel set.
		cc := strings.ToUpper(strings.TrimSpace(g.CountryCode))
		if !ok || cc == "" {
			// Without a country code nothing below it can be disambiguated:
			// "Springfield" is not a place until you know the country.
			obs.Unplaced++
			continue
		}
		obs.Placed++
		countries[cc] = struct{}{}
		region, city := strings.TrimSpace(g.Region), strings.TrimSpace(g.City)
		if region != "" {
			addTo(regions, cc, region)
			obs.RegionKnown++
		}
		if city != "" {
			addTo(cities, cc, city)
			obs.CityKnown++
		}
		spots[GeoSpot{CC: cc, Region: region, City: city}]++
	}

	obs.Places = sortedKeys(p.foldCoTravel(countries))
	obs.RegionCountry, obs.Regions = widest(regions)
	obs.RegionSpread = len(obs.Regions)
	obs.CityCountry, obs.Cities = widest(cities)
	obs.CitySpread = len(obs.Cities)
	obs.Spots = rankSpots(spots)
	return obs
}

func addTo(m map[string]map[string]struct{}, cc, name string) {
	if m[cc] == nil {
		m[cc] = map[string]struct{}{}
	}
	m[cc][name] = struct{}{}
}

// widest picks the country with the most distinct names at one tier (ties to
// the smallest code, so the answer is stable from poll to poll) and returns
// its names as sorted "CC/name".
func widest(byCountry map[string]map[string]struct{}) (string, []string) {
	best := ""
	for cc, names := range byCountry {
		if best == "" || len(names) > len(byCountry[best]) || (len(names) == len(byCountry[best]) && cc < best) {
			best = cc
		}
	}
	if best == "" {
		return "", nil
	}
	out := make([]string, 0, len(byCountry[best]))
	for name := range byCountry[best] {
		out = append(out, best+"/"+name)
	}
	sort.Strings(out)
	return best, out
}

// rankSpots orders the spots most-occupied first, then by country, region
// and city, and keeps at most GeoEvidenceMaxSpots: the evidence is stored per
// user per poll and must stay bounded however many sources a user has.
func rankSpots(counts map[GeoSpot]int) []GeoSpot {
	out := make([]GeoSpot, 0, len(counts))
	for s, n := range counts {
		s.N = n
		out = append(out, s)
	}
	sort.Slice(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if a.N != b.N {
			return a.N > b.N
		}
		if a.CC != b.CC {
			return a.CC < b.CC
		}
		if a.Region != b.Region {
			return a.Region < b.Region
		}
		return a.City < b.City
	})
	if len(out) > GeoEvidenceMaxSpots {
		out = out[:GeoEvidenceMaxSpots]
	}
	return out
}
