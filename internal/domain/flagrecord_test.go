package domain

import (
	"encoding/json"
	"net/netip"
	"reflect"
	"slices"
	"testing"
	"time"
)

// A flag record is written when an account's ATTENTION LEVEL changes — what
// its bell entry or its tab would show — and at no other time. These tests
// pin the level each source is read at, the event each change is named by,
// and what a record carries: the code and the numbers the admin UI renders
// its sentence from, and never an address.

// flagOver is an account seen in two countries at once: over the default
// policy at every tier. flagWithin is one address in one city. flagIdle is
// nobody connected.
func flagObs(p GeoAnomalyPolicy, kind string) GeoObservation {
	places := lookupOf(map[string]GeoLocation{
		"203.0.113.7":     geoAt("CN", "Guangdong", "Shenzhen"),
		"2001:db8:1:2::5": geoAt("JP", "Kanto", "Tokyo"),
	})
	switch kind {
	case "over":
		return ObserveGeo(p, ips("203.0.113.7", "2001:db8:1:2::5"), places, true)
	case "within":
		return ObserveGeo(p, ips("203.0.113.7"), places, true)
	case "idle":
		return ObserveGeo(p, ips(), places, true)
	case "geo_off":
		// Somebody connected, and the geo database is unavailable: unknown.
		return ObserveGeo(p, ips("203.0.113.7", "2001:db8:1:2::5"), places, false)
	}
	panic("unknown observation " + kind)
}

// judgeFlag is one real poll judgement from a stored record, built the way
// the traffic poll stores it: the streak, the state and the evidence of the
// verdict EvaluateGeo returns.
func judgeFlag(p GeoAnomalyPolicy, prev GeoRecord, obs GeoObservation) GeoRecord {
	v := EvaluateGeo(p, obs, prev.Streak)
	return GeoRecord{
		UserID: 7, Streak: v.Streak, State: v.State, Reason: v.Reason, Places: v.Places,
		Concurrent: obs.Placed + obs.Unplaced, Excluded: obs.Excluded.Total(),
		Evidence: GeoEvidenceFrom(obs, v.Why),
	}
}

// flaggedRecord ramps an account through FlagAfterPolls over-samples to its
// first flagged verdict, returning every record on the way.
func flaggedRecord(t *testing.T, p GeoAnomalyPolicy) []GeoRecord {
	t.Helper()
	var out []GeoRecord
	prev := GeoRecord{}
	for range p.FlagAfterPolls {
		prev = judgeFlag(p, prev, flagObs(p, "over"))
		out = append(out, prev)
	}
	if last := out[len(out)-1]; last.State != GeoStateFlagged || !last.Streak.Flagged {
		t.Fatalf("after %d over-samples: %s %+v, want a flagged latch", p.FlagAfterPolls, last.State, last.Streak)
	}
	return out
}

// Only three levels change by attention: none, suspect and flagged. Up to
// flagged is entering it from anywhere; down from flagged is leaving it,
// whether to suspect or to nothing; suspect is entered from nothing and left
// to nothing. No change, no event. Suspended is not a level these tracks
// reach: the geo_auto source names its own four events.
func TestAttentionEvent_Table(t *testing.T) {
	const (
		none, suspect, flagged, suspended = FlagLevelNone, FlagLevelSuspect, FlagLevelFlagged, FlagLevelSuspended
	)
	for _, c := range []struct {
		prev, next FlagLevel
		want       FlagEvent
		ok         bool
	}{
		{none, none, "", false},
		{none, suspect, FlagEnterSuspect, true},
		{none, flagged, FlagEnterFlagged, true},
		{suspect, suspect, "", false},
		{suspect, flagged, FlagEnterFlagged, true},
		{suspect, none, FlagLeaveSuspect, true},
		{flagged, flagged, "", false},
		{flagged, suspect, FlagLeaveFlagged, true},
		{flagged, none, FlagLeaveFlagged, true},
		{none, suspended, "", false},
		{suspended, none, "", false},
		{flagged, suspended, "", false},
		{suspended, flagged, "", false},
		{"bogus", flagged, "", false},
	} {
		got, ok := AttentionEvent(c.prev, c.next)
		if got != c.want || ok != c.ok {
			t.Errorf("AttentionEvent(%q → %q) = %q, %v; want %q, %v", c.prev, c.next, got, ok, c.want, c.ok)
		}
	}
}

// The geo level is what the geo bell and tab show: the latch, then the
// over-streak — never the state alone. A latched account stays flagged
// through idle and unknown samples (they freeze the streak, and the bell
// counts the latch), and an account over tolerance is suspect from its first
// over-sample.
func TestGeoAttention_UsesTheLatchAndTheOverStreak(t *testing.T) {
	for _, c := range []struct {
		name string
		rec  GeoRecord
		want FlagLevel
	}{
		{"no streak", GeoRecord{State: GeoStateClean}, FlagLevelNone},
		{"one over-sample", GeoRecord{State: GeoStateSuspect, Streak: GeoStreak{Over: 1}}, FlagLevelSuspect},
		{"the latch", GeoRecord{State: GeoStateFlagged, Streak: GeoStreak{Over: 3, Flagged: true}}, FlagLevelFlagged},
		{"a latch clearing, no longer over", GeoRecord{State: GeoStateFlagged, Streak: GeoStreak{Under: 2, Flagged: true}}, FlagLevelFlagged},
		{"an idle latched account", GeoRecord{State: GeoStateIdle, Streak: GeoStreak{Over: 3, Flagged: true}}, FlagLevelFlagged},
		{"an idle account mid-ramp", GeoRecord{State: GeoStateIdle, Streak: GeoStreak{Over: 1}}, FlagLevelSuspect},
		{"a flagged state with no latch is not read", GeoRecord{State: GeoStateFlagged}, FlagLevelNone},
		{"under-samples alone", GeoRecord{State: GeoStateClean, Streak: GeoStreak{Under: 4}}, FlagLevelNone},
	} {
		if got := GeoAttention(c.rec); got != c.want {
			t.Errorf("%s: GeoAttention = %q, want %q", c.name, got, c.want)
		}
	}
}

// The risk level is the state, as the risk bell reads it: flagged and
// suspect, and every other state is no attention — unknown included, so a
// flag whose evidence went missing reads as having left, which is what the
// bell shows.
func TestRiskAttention_IsTheState(t *testing.T) {
	for state, want := range map[GeoState]FlagLevel{
		GeoStateFlagged:  FlagLevelFlagged,
		GeoStateSuspect:  FlagLevelSuspect,
		GeoStateClean:    FlagLevelNone,
		GeoStateIdle:     FlagLevelNone,
		GeoStateUnknown:  FlagLevelNone,
		GeoStateDisabled: FlagLevelNone,
		GeoStateExempt:   FlagLevelNone,
		"":               FlagLevelNone,
	} {
		if got := RiskAttention(state); got != want {
			t.Errorf("RiskAttention(%q) = %q, want %q", state, got, want)
		}
	}
}

// An account the detector meets for the first time has no previous level:
// its first over-sample enters suspect, and a first clean or idle verdict
// records nothing — it never had attention to leave.
func TestGeoFlagTransition_NewUserEnteringSuspect(t *testing.T) {
	p := DefaultGeoPolicy()
	next := judgeFlag(p, GeoRecord{}, flagObs(p, "over"))
	rec, ok := GeoFlagTransition(GeoRecord{}, false, next, 1_758_000_000_000)
	if !ok {
		t.Fatalf("first over-sample recorded nothing; want enter_suspect (next %s %+v)", next.State, next.Streak)
	}
	want := FlagRecord{
		UserID: 7, Source: FlagSourceGeo, Event: FlagEnterSuspect,
		Level: FlagLevelSuspect, PrevLevel: FlagLevelNone,
		State: GeoStateSuspect, Code: string(GeoWhySuspect), AtMS: 1_758_000_000_000,
	}
	got := rec
	got.Params = nil
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("record = %+v\nwant     %+v", got, want)
	}
	if len(rec.Params) == 0 {
		t.Fatal("the record carries no params; the admin UI renders its sentence from them")
	}

	for _, kind := range []string{"within", "idle"} {
		first := judgeFlag(p, GeoRecord{}, flagObs(p, kind))
		if rec, ok := GeoFlagTransition(GeoRecord{}, false, first, 1); ok {
			t.Errorf("a first-ever %s verdict recorded %+v; want nothing", first.State, rec)
		}
	}
}

// The ramp records exactly two events: suspect on the first over-sample,
// flagged when the streak reaches flag_after_polls. The samples between are
// the same level and record nothing.
func TestGeoFlagTransition_RampRecordsEnterSuspectThenEnterFlagged(t *testing.T) {
	p := DefaultGeoPolicy()
	ramp := flaggedRecord(t, p)
	var got []FlagEvent
	prev, had := GeoRecord{}, false
	for _, next := range ramp {
		if rec, ok := GeoFlagTransition(prev, had, next, 1); ok {
			got = append(got, rec.Event)
		}
		prev, had = next, true
	}
	if want := []FlagEvent{FlagEnterSuspect, FlagEnterFlagged}; !slices.Equal(got, want) {
		t.Fatalf("ramp of %d over-samples recorded %v, want %v", len(ramp), got, want)
	}
}

// Idle and unknown FREEZE the streak: a flagged account that disconnects, or
// whose sources can no longer be placed, is still latched, and the bell
// still counts it. Recording a leave there would put a false "cleared" in
// the history every time a sharer idles, and the next over-sample a false
// "flagged" — churn, not change.
func TestGeoFlagTransition_IdleNeverLeaves(t *testing.T) {
	p := DefaultGeoPolicy()
	ramp := flaggedRecord(t, p)
	flagged := ramp[len(ramp)-1]
	for _, kind := range []string{"idle", "geo_off"} {
		next := judgeFlag(p, flagged, flagObs(p, kind))
		if next.State == GeoStateFlagged || next.State == GeoStateClean {
			t.Fatalf("%s sample judged %s; the fixture must be idle or unknown", kind, next.State)
		}
		if rec, ok := GeoFlagTransition(flagged, true, next, 1); ok {
			t.Errorf("flagged → %s recorded %+v; the latch is frozen, nothing changed", next.State, rec)
		}
		// And back: the next over-sample is still the same latch.
		back := judgeFlag(p, next, flagObs(p, "over"))
		if rec, ok := GeoFlagTransition(next, true, back, 1); ok {
			t.Errorf("%s → over recorded %+v; the latch never left", next.State, rec)
		}
	}
	// A suspect account mid-ramp idles the same way.
	suspect := ramp[0]
	idle := judgeFlag(p, suspect, flagObs(p, "idle"))
	if rec, ok := GeoFlagTransition(suspect, true, idle, 1); ok {
		t.Errorf("suspect → idle recorded %+v; the over-streak is frozen", rec)
	}
}

// A latch clears only after clear_after_polls clean samples in a row. The
// clean samples before that are still flagged (the latch holds) and record
// nothing; the one that clears it records leave_flagged, with the clean
// verdict's code and the under-count that cleared it.
func TestGeoFlagTransition_LatchClearingIsLeaveFlagged(t *testing.T) {
	p := DefaultGeoPolicy()
	ramp := flaggedRecord(t, p)
	prev := ramp[len(ramp)-1]
	var recs []FlagRecord
	for i := range p.ClearAfterPolls {
		next := judgeFlag(p, prev, flagObs(p, "within"))
		if rec, ok := GeoFlagTransition(prev, true, next, int64(i+1)); ok {
			recs = append(recs, rec)
		}
		prev = next
	}
	if len(recs) != 1 {
		t.Fatalf("clearing over %d clean samples recorded %d events: %+v; want one", p.ClearAfterPolls, len(recs), recs)
	}
	rec := recs[0]
	if rec.Event != FlagLeaveFlagged || rec.Level != FlagLevelNone || rec.PrevLevel != FlagLevelFlagged {
		t.Fatalf("clearing record %s %q←%q, want leave_flagged none←flagged", rec.Event, rec.Level, rec.PrevLevel)
	}
	if rec.State != GeoStateClean || rec.Code != string(GeoWhyCleanWithin) || rec.AtMS != int64(p.ClearAfterPolls) {
		t.Fatalf("clearing record state/code/at = %s/%s/%d, want clean/clean_within on the %dth clean sample", rec.State, rec.Code, rec.AtMS, p.ClearAfterPolls)
	}
	var params GeoFlagParams
	if err := json.Unmarshal(rec.Params, &params); err != nil {
		t.Fatalf("params: %v", err)
	}
	if params.Under != p.ClearAfterPolls || params.Flagged || params.Over != 0 {
		t.Fatalf("params %+v, want the %d under-samples that cleared the latch", params, p.ClearAfterPolls)
	}
}

// Disabling detection or exempting the account RESETS the streak, latch
// included: the account is no longer flagged, and the bell stops counting
// it. That is a leave, recorded with the state and code that say why — the
// policy, not the account's behaviour, cleared it.
func TestGeoFlagTransition_DisabledResetIsLeaveFlagged(t *testing.T) {
	p := DefaultGeoPolicy()
	ramp := flaggedRecord(t, p)
	flagged := ramp[len(ramp)-1]

	off := p
	off.Scope = GeoScopeOff
	exempt := p
	exempt.AllowAnywhere = true
	for _, c := range []struct {
		policy GeoAnomalyPolicy
		state  GeoState
		code   GeoReasonCode
	}{
		{off, GeoStateDisabled, GeoWhyDisabled},
		{exempt, GeoStateExempt, GeoWhyExempt},
	} {
		next := judgeFlag(c.policy, flagged, flagObs(c.policy, "over"))
		rec, ok := GeoFlagTransition(flagged, true, next, 1)
		if !ok {
			t.Fatalf("flagged → %s recorded nothing; the latch was reset", next.State)
		}
		if rec.Event != FlagLeaveFlagged || rec.Level != FlagLevelNone || rec.State != c.state || rec.Code != string(c.code) {
			t.Fatalf("flagged → %s: %s level %q state %s code %s; want leave_flagged, none, %s, %s",
				next.State, rec.Event, rec.Level, rec.State, rec.Code, c.state, c.code)
		}
	}
}

// A geo record's params are what the admin UI's reason sentences need —
// the over, under and ban-over counts, the latch and the tier, which the
// stored verdict keeps in its streak — plus the evidence the verdict was
// drawn from. The names are a contract with the SPA. And none of it is an
// address: the record outlives the connection by months.
func TestGeoFlagTransition_ParamsCarryTheStreakAndNoAddress(t *testing.T) {
	p := DefaultGeoPolicy()
	p.BanEnabled = true
	p.BanAfterPolls = 10 // counted, never due within the ramp
	ramp := flaggedRecord(t, p)
	prev, next := ramp[len(ramp)-2], ramp[len(ramp)-1]
	rec, ok := GeoFlagTransition(prev, true, next, 1)
	if !ok || rec.Event != FlagEnterFlagged {
		t.Fatalf("record %+v, %v; want enter_flagged", rec, ok)
	}

	var params GeoFlagParams
	if err := json.Unmarshal(rec.Params, &params); err != nil {
		t.Fatalf("params %s: %v", rec.Params, err)
	}
	if params.Over != 3 || params.Under != 0 || params.BanOver != 3 || !params.Flagged || params.Tier != GeoTierCountry {
		t.Fatalf("params streak %+v, want over 3, under 0, ban_over 3, flagged, tier country", params)
	}
	wantEvidence, _ := json.Marshal(next.Evidence)
	gotEvidence, _ := json.Marshal(params.Evidence)
	if string(gotEvidence) != string(wantEvidence) {
		t.Fatalf("params evidence %s\nwant the verdict's %s", gotEvidence, wantEvidence)
	}

	var tree map[string]any
	if err := json.Unmarshal(rec.Params, &tree); err != nil {
		t.Fatalf("params: %v", err)
	}
	keys := make([]string, 0, len(tree))
	for k := range tree {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	if want := []string{"ban_over", "evidence", "flagged", "over", "tier", "under"}; !slices.Equal(keys, want) {
		t.Fatalf("params keys %v, want %v (the SPA reads these names)", keys, want)
	}
	assertNoAddressIn(t, rec.Params, "Tokyo", "Shenzhen")
}

// assertNoAddressIn walks every string of a JSON document — keys included —
// and fails on one that parses as an address or a network. mustHave keeps it
// honest: the document has to carry the places it is about.
func assertNoAddressIn(t *testing.T, raw json.RawMessage, mustHave ...string) {
	t.Helper()
	var tree any
	if err := json.Unmarshal(raw, &tree); err != nil {
		t.Fatalf("unmarshal %s: %v", raw, err)
	}
	seen := map[string]bool{}
	walkJSONStrings(tree, func(s string) {
		seen[s] = true
		if _, err := netip.ParseAddr(s); err == nil {
			t.Errorf("params carry the address %q: %s", s, raw)
		}
		if _, err := netip.ParsePrefix(s); err == nil {
			t.Errorf("params carry the network %q: %s", s, raw)
		}
	})
	for _, s := range mustHave {
		if !seen[s] {
			t.Errorf("params %s lack %q: the walk is vacuous", raw, s)
		}
	}
}

// A risk record follows the state, like the risk bell: every change of level
// is recorded, and nothing else. Its source is the signal's kind, its code
// the verdict's. Unknown is no attention, so a flag whose evidence went
// missing (a geo database gone, every source excluded) is recorded leaving —
// the code says why.
func TestRiskFlagTransition_Table(t *testing.T) {
	for _, c := range []struct {
		name      string
		prev      GeoState
		hadPrev   bool
		next      GeoState
		code      RiskCode
		event     FlagEvent
		level     FlagLevel
		prevLevel FlagLevel
	}{
		{"first suspect", "", false, GeoStateSuspect, RiskCodeOverBuilding, FlagEnterSuspect, FlagLevelSuspect, FlagLevelNone},
		{"first flagged", "", false, GeoStateFlagged, RiskCodeOver, FlagEnterFlagged, FlagLevelFlagged, FlagLevelNone},
		{"first clean", "", false, GeoStateClean, RiskCodeWithin, "", "", ""},
		{"first idle", "", false, GeoStateIdle, RiskCodeNoFetches, "", "", ""},
		{"clean → suspect", GeoStateClean, true, GeoStateSuspect, RiskCodeOverBuilding, FlagEnterSuspect, FlagLevelSuspect, FlagLevelNone},
		{"suspect → flagged", GeoStateSuspect, true, GeoStateFlagged, RiskCodeOver, FlagEnterFlagged, FlagLevelFlagged, FlagLevelSuspect},
		{"flagged → suspect", GeoStateFlagged, true, GeoStateSuspect, RiskCodeOverBuilding, FlagLeaveFlagged, FlagLevelSuspect, FlagLevelFlagged},
		{"flagged → unknown", GeoStateFlagged, true, GeoStateUnknown, RiskCodeGeoUnavailable, FlagLeaveFlagged, FlagLevelNone, FlagLevelFlagged},
		{"flagged → disabled", GeoStateFlagged, true, GeoStateDisabled, RiskCodeSignalOff, FlagLeaveFlagged, FlagLevelNone, FlagLevelFlagged},
		{"suspect → clean", GeoStateSuspect, true, GeoStateClean, RiskCodeWithin, FlagLeaveSuspect, FlagLevelNone, FlagLevelSuspect},
		{"unknown → flagged", GeoStateUnknown, true, GeoStateFlagged, RiskCodeOver, FlagEnterFlagged, FlagLevelFlagged, FlagLevelNone},
		{"flagged stays flagged", GeoStateFlagged, true, GeoStateFlagged, RiskCodeOver, "", "", ""},
		{"suspect stays suspect", GeoStateSuspect, true, GeoStateSuspect, RiskCodeOverBuilding, "", "", ""},
		{"idle → clean is churn", GeoStateIdle, true, GeoStateClean, RiskCodeWithin, "", "", ""},
		{"clean → idle is churn", GeoStateClean, true, GeoStateIdle, RiskCodeNoFetches, "", "", ""},
		{"unknown → idle is churn", GeoStateUnknown, true, GeoStateIdle, RiskCodeNoFetches, "", "", ""},
	} {
		t.Run(c.name, func(t *testing.T) {
			next := RiskSignal{UserID: 9, Kind: RiskKindDevices, State: c.next, Code: c.code}
			rec, ok := RiskFlagTransition(c.prev, c.hadPrev, next, 1_758_000_000_000)
			if c.event == "" {
				if ok {
					t.Fatalf("recorded %+v; want nothing", rec)
				}
				return
			}
			want := FlagRecord{
				UserID: 9, Source: string(RiskKindDevices), Event: c.event, Level: c.level, PrevLevel: c.prevLevel,
				State: c.next, Code: string(c.code), AtMS: 1_758_000_000_000,
			}
			if !ok || !reflect.DeepEqual(rec, want) {
				t.Fatalf("record %+v, %v\nwant   %+v", rec, ok, want)
			}
		})
	}
}

// A risk record's params are the verdict's stored evidence, byte for byte:
// the evidence is what the risk tab renders the verdict from, so the record
// renders the same way, and it is already address-free (each evaluator's
// test holds it to that). A copy, so the caller reusing its buffer cannot
// rewrite history. A verdict with nothing to show — idle, disabled, exempt —
// has no evidence, and neither does its record.
func TestRiskFlagTransition_ParamsAreTheStoredEvidence(t *testing.T) {
	evidence := json.RawMessage(`{"v":1,"provinces":[{"cc":"CN","region":"Guangdong","days":3},{"cc":"CN","region":"Hunan","days":2}]}`)
	next := RiskSignal{UserID: 9, Kind: RiskKindSubSpread, State: GeoStateFlagged, Code: RiskCodeSpread, Evidence: evidence}
	rec, ok := RiskFlagTransition(GeoStateClean, true, next, 1)
	if !ok {
		t.Fatal("clean → flagged recorded nothing")
	}
	if string(rec.Params) != string(evidence) {
		t.Fatalf("params %s, want the evidence verbatim %s", rec.Params, evidence)
	}
	evidence[2] = 'X'
	if rec.Params[2] == 'X' {
		t.Fatal("params alias the caller's evidence buffer")
	}
	assertNoAddressIn(t, rec.Params, "Guangdong")

	idle := RiskSignal{UserID: 9, Kind: RiskKindSubSpread, State: GeoStateIdle, Code: RiskCodeNoFetches}
	rec, ok = RiskFlagTransition(GeoStateFlagged, true, idle, 1)
	if !ok || rec.Params != nil {
		t.Fatalf("flagged → idle: %+v, %v; want leave_flagged with no params", rec, ok)
	}
}

// geo_auto is its own track, with its own four events: suspended when the
// poll applies it, and back to nothing when it expires, when staff lift it,
// or when another hold replaces it. Its params are the producer's numbers,
// its code the producer's word; nil params store nothing, and a zero time
// leaves the stamp to the store.
func TestGeoAutoFlag_LevelsAndParams(t *testing.T) {
	at := time.UnixMilli(1_758_000_000_000)
	for _, c := range []struct {
		event     FlagEvent
		code      string
		params    map[string]any
		level     FlagLevel
		prevLevel FlagLevel
		json      string
	}{
		{FlagAutoSuspended, "country", map[string]any{"tier": "country", "spread": 2, "duration_minutes": 60},
			FlagLevelSuspended, FlagLevelNone, `{"duration_minutes":60,"spread":2,"tier":"country"}`},
		{FlagAutoLiftedExpiry, "expired", map[string]any{"duration_minutes": 60, "suspended_at_ms": nil},
			FlagLevelNone, FlagLevelSuspended, `{"duration_minutes":60,"suspended_at_ms":null}`},
		{FlagAutoLiftedAdmin, "admin_resume", nil, FlagLevelNone, FlagLevelSuspended, ""},
		{FlagAutoReplaced, "replaced", map[string]any{"replaced_by": "admin"}, FlagLevelNone, FlagLevelSuspended, `{"replaced_by":"admin"}`},
	} {
		rec := GeoAutoFlag(42, c.event, c.code, c.params, at)
		want := FlagRecord{UserID: 42, Source: FlagSourceGeoAuto, Event: c.event, Level: c.level, PrevLevel: c.prevLevel, Code: c.code, AtMS: at.UnixMilli()}
		got := rec
		got.Params = nil
		if !reflect.DeepEqual(got, want) {
			t.Errorf("%s: %+v\nwant %+v", c.event, got, want)
		}
		if string(rec.Params) != c.json {
			t.Errorf("%s: params %s, want %s", c.event, rec.Params, c.json)
		}
		if c.params == nil && rec.Params != nil {
			t.Errorf("%s: nil params stored as %q, want nil", c.event, rec.Params)
		}
		if rec.Params != nil {
			assertNoAddressIn(t, rec.Params)
		}
	}
	if rec := GeoAutoFlag(42, FlagAutoLiftedAdmin, "admin_resume", nil, time.Time{}); rec.AtMS != 0 {
		t.Fatalf("zero time stamped %d; want 0 so the store stamps it", rec.AtMS)
	}
}

// The sources a record can name: the concurrent-location verdict, its
// automatic suspension, each risk signal by its kind, and — last, because it
// is no attention source — an admin's review action. The list the admin
// filter is checked against. The events are the eight attention changes and
// the four review actions, in that order.
func TestFlagSources_AreGeoGeoAutoEveryRiskKindAndReview(t *testing.T) {
	want := []string{"geo", "geo_auto", "sub_spread", "devices", "usage_shift", "login_country", "review"}
	if got := FlagSources(); !slices.Equal(got, want) {
		t.Fatalf("FlagSources() = %v, want %v", got, want)
	}
	events := []FlagEvent{
		FlagEnterSuspect, FlagEnterFlagged, FlagLeaveSuspect, FlagLeaveFlagged,
		FlagAutoSuspended, FlagAutoLiftedExpiry, FlagAutoLiftedAdmin, FlagAutoReplaced,
		"dismissed", "undismissed", "trusted", "untrusted",
	}
	if got := FlagEvents(); !slices.Equal(got, events) {
		t.Fatalf("FlagEvents() = %v, want %v", got, events)
	}
}
