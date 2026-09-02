package domain

import (
	"reflect"
	"strings"
	"testing"
)

// Each case here names the real person it protects. A verdict from this file
// ends up in front of a human deciding whether to suspend somebody's account,
// so "it returns true" is not what any of these assert — they assert which
// user is judged, and why.

func pol(mut func(*GeoAnomalyPolicy)) GeoAnomalyPolicy {
	p := DefaultGeoPolicy()
	if mut != nil {
		mut(&p)
	}
	return p
}

func obs(places []string, placed, unplaced int) GeoObservation {
	return GeoObservation{UserID: 7, Places: places, Placed: placed, Unplaced: unplaced, GeoAvailable: true}
}

// Run a sequence of samples through the state machine, returning every
// verdict. The streak is the whole point of this design, and a test that
// evaluates one sample in isolation cannot see it.
func sequence(p GeoAnomalyPolicy, samples ...GeoObservation) []GeoVerdict {
	var out []GeoVerdict
	streak := GeoStreak{}
	for _, s := range samples {
		v := EvaluateGeo(p, s, streak)
		streak = v.Streak
		out = append(out, v)
	}
	return out
}

func states(vs []GeoVerdict) []GeoState {
	out := make([]GeoState, 0, len(vs))
	for _, v := range vs {
		out = append(out, v.State)
	}
	return out
}

// ---------------------------------------------------------------- hysteresis

// The knob a naive detector omits, and the reason it flaps. One noisy sample
// must not raise an alert an operator will learn to ignore.
func TestEvaluate_OneNoisySampleDoesNotFlag(t *testing.T) {
	got := states(sequence(pol(nil),
		obs([]string{"JP"}, 2, 0),
		obs([]string{"DE", "JP"}, 2, 0), // one blip
		obs([]string{"JP"}, 2, 0),
	))
	want := []GeoState{GeoStateClean, GeoStateSuspect, GeoStateClean}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("states = %v, want %v — a single blip must surface as Suspect and clear", got, want)
	}
}

// Sustained is what flags. Suspect is the visible ramp so the eventual flag
// does not look like it came from nowhere.
func TestEvaluate_SustainedSpreadFlagsAfterThreshold(t *testing.T) {
	two := obs([]string{"DE", "JP"}, 2, 0)
	got := states(sequence(pol(nil), two, two, two, two))
	want := []GeoState{GeoStateSuspect, GeoStateSuspect, GeoStateFlagged, GeoStateFlagged}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("states = %v, want %v (FlagAfterPolls=3)", got, want)
	}
}

// Clearing is deliberately slower than flagging. Otherwise an account steps
// just under the line for one check and sheds the flag.
func TestEvaluate_ClearingIsSlowerThanFlagging(t *testing.T) {
	two := obs([]string{"DE", "JP"}, 2, 0)
	one := obs([]string{"JP"}, 2, 0)
	p := pol(func(p *GeoAnomalyPolicy) { p.FlagAfterPolls = 2; p.ClearAfterPolls = 4 })
	vs := sequence(p, two, two, one, one, one, one)
	got := states(vs)
	want := []GeoState{
		GeoStateSuspect, GeoStateFlagged,
		GeoStateFlagged, GeoStateFlagged, GeoStateFlagged, // latched for 3 clean checks
		GeoStateClean, // the 4th clears it
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("states = %v, want %v", got, want)
	}
}

// The easiest evasion there is: disconnect for a while and come back clean.
// Idle polls must neither accrue nor shed a streak.
func TestEvaluate_IdleDoesNotClearAFlag(t *testing.T) {
	two := obs([]string{"DE", "JP"}, 2, 0)
	idle := GeoObservation{UserID: 7, GeoAvailable: true}
	p := pol(func(p *GeoAnomalyPolicy) { p.FlagAfterPolls = 2; p.ClearAfterPolls = 2 })
	vs := sequence(p, two, two, idle, idle, idle, idle)
	got := states(vs)
	want := []GeoState{
		GeoStateSuspect, GeoStateFlagged,
		GeoStateIdle, GeoStateIdle, GeoStateIdle, GeoStateIdle,
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("states = %v, want %v", got, want)
	}
	// And the flag is still latched underneath, so reconnecting resumes
	// from flagged rather than from a clean slate.
	if !vs[len(vs)-1].Streak.Flagged {
		t.Fatal("idling must not silently drop the latched flag")
	}
}

// ----------------------------------------------------------------- tolerance

// The knob that separates "two countries is over" from "two is fine, three
// is over" without touching anything else.
func TestEvaluate_ToleranceIsANumberNotAHardcodedTwo(t *testing.T) {
	three := obs([]string{"DE", "JP", "US"}, 3, 0)
	two := obs([]string{"DE", "JP"}, 2, 0)
	p := pol(func(p *GeoAnomalyPolicy) { p.MaxPlaces = 2; p.FlagAfterPolls = 1 })

	if got := EvaluateGeo(p, two, GeoStreak{}).State; got != GeoStateClean {
		t.Fatalf("two places with tolerance 2: got %s, want clean", got)
	}
	if got := EvaluateGeo(p, three, GeoStreak{}).State; got != GeoStateFlagged {
		t.Fatalf("three places with tolerance 2: got %s, want flagged", got)
	}
}

// A tolerance of 0 would flag every connected user including one sitting at
// home. A misconfiguration must degrade into silence, not into accusing the
// whole fleet.
func TestEvaluate_ZeroToleranceIsRepairedNotObeyed(t *testing.T) {
	p := pol(func(p *GeoAnomalyPolicy) { p.MaxPlaces = 0; p.FlagAfterPolls = 1 })
	if got := EvaluateGeo(p, obs([]string{"JP"}, 1, 0), GeoStreak{}).State; got != GeoStateClean {
		t.Fatalf("one place must never be an anomaly: got %s", got)
	}
}

// ----------------------------------------------------------------- exemption

// The travelling account, the shared team credential, the operator's own test
// user. Excludable outright rather than by raising everyone's threshold.
func TestEvaluate_AllowAnywhereIsExemptNotClean(t *testing.T) {
	p := pol(func(p *GeoAnomalyPolicy) { p.AllowAnywhere = true })
	v := EvaluateGeo(p, obs([]string{"DE", "JP", "US"}, 3, 0), GeoStreak{Over: 9, Flagged: true})
	if v.State != GeoStateExempt {
		t.Fatalf("state = %s, want exempt — and NOT clean, which would read as evidence", v.State)
	}
	if v.Streak.Flagged || v.Streak.Over != 0 {
		t.Fatal("an exemption resets the streak so removing it later starts from a clean slate")
	}
}

// Off is a deliberate choice and must not be reported as Unknown, which means
// "I tried and could not tell".
func TestEvaluate_ScopeOffReportsDisabledNotUnknown(t *testing.T) {
	p := pol(func(p *GeoAnomalyPolicy) { p.Scope = GeoScopeOff })
	if got := EvaluateGeo(p, obs([]string{"DE", "JP"}, 2, 0), GeoStreak{}).State; got != GeoStateDisabled {
		t.Fatalf("state = %s, want disabled", got)
	}
}

// ------------------------------------------------------------------- unknown

// A stale or partial database must not read as a clean fleet.
func TestEvaluate_TooFewPlaceableAddressesIsUnknown(t *testing.T) {
	p := pol(func(p *GeoAnomalyPolicy) { p.MinPlacedRatio = 0.5; p.FlagAfterPolls = 1 })
	// 1 of 4 placed = 25%, below the 50% required.
	v := EvaluateGeo(p, obs([]string{"JP"}, 1, 3), GeoStreak{})
	if v.State != GeoStateUnknown {
		t.Fatalf("state = %s, want unknown", v.State)
	}
	if !strings.Contains(v.Reason, "1 of 4") {
		t.Fatalf("the reason must show the sample it refused to judge, got %q", v.Reason)
	}
}

func TestEvaluate_GeoUnavailableIsUnknown(t *testing.T) {
	o := obs([]string{"DE", "JP"}, 2, 0)
	o.GeoAvailable = false
	if got := EvaluateGeo(pol(nil), o, GeoStreak{}).State; got != GeoStateUnknown {
		t.Fatalf("state = %s, want unknown", got)
	}
}

// Unknown must not silently drain a latched flag either — the account is not
// cleared by the database breaking.
func TestEvaluate_UnknownDoesNotClearAFlag(t *testing.T) {
	unknown := GeoObservation{UserID: 7, Placed: 0, Unplaced: 3, GeoAvailable: false}
	v := EvaluateGeo(pol(nil), unknown, GeoStreak{Flagged: true, Under: 0})
	if !v.Streak.Flagged {
		t.Fatal("a broken database must not clear an existing flag")
	}
}

// ---------------------------------------------------------------- actionable

// Only Flagged may drive an automatic response. Suspect is deliberately below
// the line, and every other state means the detector cannot judge.
func TestGeoState_OnlyFlaggedIsActionable(t *testing.T) {
	for _, s := range []GeoState{
		GeoStateDisabled, GeoStateExempt, GeoStateUnknown,
		GeoStateIdle, GeoStateClean, GeoStateSuspect,
	} {
		if s.Actionable() {
			t.Fatalf("%s must not be actionable", s)
		}
	}
	if !GeoStateFlagged.Actionable() {
		t.Fatal("flagged must be actionable or the feature does nothing")
	}
}

// A verdict is shown to a human deciding about somebody's account. It has to
// carry the numbers that produced it.
func TestEvaluate_ReasonNamesTheNumbersBehindTheVerdict(t *testing.T) {
	two := obs([]string{"DE", "JP"}, 2, 0)
	p := pol(func(p *GeoAnomalyPolicy) { p.FlagAfterPolls = 2 })
	vs := sequence(p, two, two)
	r := vs[1].Reason
	for _, want := range []string{"DE", "JP", "tolerance is 1"} {
		if !strings.Contains(r, want) {
			t.Fatalf("reason %q must mention %q", r, want)
		}
	}
}
