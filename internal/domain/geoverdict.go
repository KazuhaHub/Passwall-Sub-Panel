package domain

import "fmt"

// GeoObservation is one poll's worth of evidence about one user, after the
// policy's scope has been applied.
type GeoObservation struct {
	UserID int64
	// Places are the distinct locations occupied concurrently, already
	// folded through the policy's co-travel sets and sorted.
	Places []string
	// Placed and Unplaced split the user's live addresses by whether the
	// database could resolve one at this scope. Their sum is the sample
	// size, and it is what MinPlacedRatio is measured against.
	Placed   int
	Unplaced int
	// GeoAvailable is false when lookup is switched off or unusable. Kept
	// separate from "nothing resolved" because the two look identical in
	// the numbers and mean entirely different things.
	GeoAvailable bool
}

// GeoStreak is the little state carried between polls that makes the verdict
// stable. Persisted per user.
//
// Two counters rather than one: flagging and clearing have different
// thresholds on purpose, and a single signed counter would make an
// oscillating user drift instead of settling.
type GeoStreak struct {
	// Over is consecutive samples above tolerance.
	Over int
	// Under is consecutive samples within tolerance.
	Under int
	// Flagged is the latched state. It survives a single clean sample,
	// which is the whole point: without latching, a sharer who idles one
	// device for one poll clears their flag.
	Flagged bool
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
// they might mistake for evidence. Unknown precedes the count because a
// conclusion drawn from unplaceable addresses is not a conclusion.
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
	if sample == 0 {
		v.State = GeoStateIdle
		v.Reason = "no live connections"
		// An idle user neither accrues nor sheds a streak. Counting idle
		// polls as clean would let a flagged account clear itself simply by
		// disconnecting for a while, which is the easiest evasion there is.
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

	over := len(obs.Places) > p.MaxPlaces
	if over {
		v.Streak.Over = prev.Over + 1
		v.Streak.Under = 0
	} else {
		v.Streak.Under = prev.Under + 1
		v.Streak.Over = 0
	}

	switch {
	case over && (prev.Flagged || v.Streak.Over >= p.FlagAfterPolls):
		v.Streak.Flagged = true
		v.State = GeoStateFlagged
		v.Reason = fmt.Sprintf("in %d places at once (%v); tolerance is %d, sustained for %d of %d checks",
			len(obs.Places), obs.Places, p.MaxPlaces, v.Streak.Over, p.FlagAfterPolls)
	case over:
		// Over tolerance but not yet sustained. Visible, never actionable —
		// this is the ramp, and hiding it would make the eventual flag look
		// like it came out of nowhere.
		v.Streak.Flagged = false
		v.State = GeoStateSuspect
		v.Reason = fmt.Sprintf("in %d places at once (%v); tolerance is %d, %d of %d checks so far",
			len(obs.Places), obs.Places, p.MaxPlaces, v.Streak.Over, p.FlagAfterPolls)
	case prev.Flagged && v.Streak.Under < p.ClearAfterPolls:
		// Latched. Clearing is deliberately slower than flagging so an
		// account cannot step just under the line between checks.
		v.Streak.Flagged = true
		v.State = GeoStateFlagged
		v.Reason = fmt.Sprintf("within tolerance for %d of the %d checks needed to clear",
			v.Streak.Under, p.ClearAfterPolls)
	default:
		v.Streak.Flagged = false
		v.State = GeoStateClean
		if len(obs.Places) == 0 {
			v.Reason = "connected, but no address could be placed"
		} else {
			v.Reason = fmt.Sprintf("in %d place(s) (%v); tolerance is %d", len(obs.Places), obs.Places, p.MaxPlaces)
		}
	}
	return v
}

// ObserveGeo projects one user's live IPs into a policy-scoped observation.
//
// Split from EvaluateGeo so the projection (which needs a geo database) and
// the judgement (which is arithmetic) can be tested and reasoned about
// separately — and so a stored observation can be re-judged under a different
// policy without re-querying anything.
func ObserveGeo(p GeoAnomalyPolicy, u UserLiveIPs, lookup GeoLookup, geoAvailable bool) GeoObservation {
	p = p.sanitized()
	obs := GeoObservation{UserID: u.UserID, GeoAvailable: geoAvailable}
	if len(u.IPs) == 0 {
		return obs
	}
	if !geoAvailable || lookup == nil || p.Scope == GeoScopeOff {
		obs.Unplaced = len(u.IPs)
		return obs
	}
	located := lookup(u.IPs)
	places := map[string]struct{}{}
	for _, ip := range u.IPs {
		g, ok := located[ip]
		if !ok {
			obs.Unplaced++
			continue
		}
		place := p.placeOf(g)
		if place == "" {
			// Resolvable, but not at this granularity — a country-only row
			// under a city-scoped policy. Unplaced, never a place of its
			// own, or a coarse database manufactures the signal.
			obs.Unplaced++
			continue
		}
		obs.Placed++
		places[place] = struct{}{}
	}
	obs.Places = sortedKeys(p.foldCoTravel(places))
	return obs
}
