package domain

import (
	"encoding/json"
	"errors"
	"maps"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"
)

// An admin's review of an account — dismiss it until it escalates, or trust
// it — and the rule that decides when a dismissal stops covering it. These
// tests pin the rule row by row (the reopen table), the level algebra the
// queue sorts and counts by, and what a review record may carry: the admin's
// id and the accepted levels, never a name, a note or an address.

// reviewD is the dismissal time every row of the reopen table uses.
const reviewD = int64(1_000_000)

// accepted is one snapshot entry; the verdict time defaults to D−100, a
// verdict read shortly before the admin dismissed.
func accepted(l FlagLevel, at ...int64) AcceptedLevel {
	a := AcceptedLevel{Level: l, AtMS: reviewD - 100}
	if len(at) > 0 {
		a.AtMS = at[0]
	}
	return a
}

// step is one non-review flag record as the reopen rule reads it.
func step(src string, l FlagLevel, st GeoState, at int64) FlagStep {
	return FlagStep{Source: src, Level: l, State: st, AtMS: at}
}

const (
	srcGeo     = FlagSourceGeo
	srcGeoAuto = FlagSourceGeoAuto
	srcSub     = string(RiskKindSubSpread)
	srcDev     = string(RiskKindDevices)
	srcUsage   = string(RiskKindUsageShift)
	srcLogin   = string(RiskKindLoginCountry)
)

// The reopen rule, one row per case the design and its critiques named. A
// dismissal accepts the levels the admin saw; it stops covering the account
// when a source goes above what was accepted — now, or at any moment since
// (sticky), or when a source that fully cleared comes back at all. Flapping
// inside the accepted level never reopens, "cannot tell" is not a clear, and
// a dismissal whose history may have been pruned lapses instead of becoming
// permanent.
func TestEvaluateReview_Table(t *testing.T) {
	const D = reviewD
	const S = D - 600
	none, suspect, flagged, suspended := FlagLevelNone, FlagLevelSuspect, FlagLevelFlagged, FlagLevelSuspended
	for _, c := range []struct {
		name         string
		notDismissed bool
		snapshot     DismissSnapshot
		steps        []FlagStep
		now          AttentionLevels
		heldSince    int64
		trusted      bool
		historyFrom  int64
		keptFrom     int64
		want         ReviewState
	}{
		{name: "1 not dismissed", notDismissed: true,
			now:  AttentionLevels{srcGeo: flagged},
			want: ReviewState{}},
		{name: "2 same level",
			snapshot: DismissSnapshot{srcGeo: accepted(flagged)},
			now:      AttentionLevels{srcGeo: flagged},
			want:     ReviewState{Dismissed: true}},
		{name: "3 flapping inside the accepted level",
			snapshot: DismissSnapshot{srcGeo: accepted(flagged)},
			steps:    []FlagStep{step(srcGeo, suspect, GeoStateSuspect, D+10), step(srcGeo, flagged, GeoStateFlagged, D+20)},
			now:      AttentionLevels{srcGeo: flagged},
			want:     ReviewState{Dismissed: true}},
		{name: "4 dropped to suspect",
			snapshot: DismissSnapshot{srcGeo: accepted(flagged)},
			steps:    []FlagStep{step(srcGeo, suspect, GeoStateSuspect, D+10)},
			now:      AttentionLevels{srcGeo: suspect},
			want:     ReviewState{Dismissed: true}},
		{name: "5 escalation now",
			snapshot: DismissSnapshot{srcGeo: accepted(suspect)},
			steps:    []FlagStep{step(srcGeo, flagged, GeoStateFlagged, D+10)},
			now:      AttentionLevels{srcGeo: flagged},
			want:     ReviewState{Dismissed: true, Reopened: true, Escalated: []string{srcGeo}}},
		{name: "6 new source",
			snapshot: DismissSnapshot{srcGeo: accepted(flagged)},
			steps:    []FlagStep{step(srcDev, suspect, GeoStateSuspect, D+10)},
			now:      AttentionLevels{srcGeo: flagged, srcDev: suspect},
			want:     ReviewState{Dismissed: true, Reopened: true, Escalated: []string{srcDev}}},
		{name: "7 cleared then re-entered lower",
			snapshot: DismissSnapshot{srcGeo: accepted(flagged)},
			steps:    []FlagStep{step(srcGeo, none, GeoStateClean, D+10), step(srcGeo, suspect, GeoStateSuspect, D+20)},
			now:      AttentionLevels{srcGeo: suspect},
			want:     ReviewState{Dismissed: true, Reopened: true, Escalated: []string{srcGeo}}},
		{name: "8 cleared and still none",
			snapshot: DismissSnapshot{srcGeo: accepted(flagged)},
			steps:    []FlagStep{step(srcGeo, none, GeoStateClean, D+10)},
			now:      AttentionLevels{},
			want:     ReviewState{Dismissed: true}},
		{name: "9 clear before the source's verdict time",
			snapshot: DismissSnapshot{srcGeo: accepted(flagged, D-100)},
			steps:    []FlagStep{step(srcGeo, none, GeoStateClean, D-150)},
			now:      AttentionLevels{srcGeo: flagged},
			want:     ReviewState{Dismissed: true}},
		{name: "10 clear at exactly the verdict time",
			snapshot: DismissSnapshot{srcGeo: accepted(flagged, D-100)},
			steps:    []FlagStep{step(srcGeo, none, GeoStateClean, D-100)},
			now:      AttentionLevels{srcGeo: suspect},
			want:     ReviewState{Dismissed: true}},
		{name: "11 race: clear stamped before D, after the verdict",
			snapshot: DismissSnapshot{srcGeo: accepted(flagged, D-300)},
			steps:    []FlagStep{step(srcGeo, none, GeoStateClean, D-200), step(srcGeo, suspect, GeoStateSuspect, D+50)},
			now:      AttentionLevels{srcGeo: suspect},
			want:     ReviewState{Dismissed: true, Reopened: true, Escalated: []string{srcGeo}}},
		{name: "12 escalation that receded sticks",
			snapshot: DismissSnapshot{srcDev: accepted(suspect)},
			steps:    []FlagStep{step(srcDev, flagged, GeoStateFlagged, D+10), step(srcDev, suspect, GeoStateSuspect, D+20)},
			now:      AttentionLevels{srcDev: suspect},
			want:     ReviewState{Dismissed: true, Reopened: true, Escalated: []string{srcDev}}},
		{name: "13 escalated, then cleared, other source still present",
			snapshot: DismissSnapshot{srcDev: accepted(suspect), srcGeo: accepted(flagged)},
			steps:    []FlagStep{step(srcDev, flagged, GeoStateFlagged, D+10), step(srcDev, none, GeoStateClean, D+20)},
			now:      AttentionLevels{srcGeo: flagged},
			want:     ReviewState{Dismissed: true, Reopened: true, Escalated: []string{srcDev}}},
		{name: "14 unknown is not a clear",
			snapshot: DismissSnapshot{srcSub: accepted(flagged)},
			steps:    []FlagStep{step(srcSub, none, GeoStateUnknown, D+10), step(srcSub, flagged, GeoStateFlagged, D+20)},
			now:      AttentionLevels{srcSub: flagged},
			want:     ReviewState{Dismissed: true}},
		{name: "15 exempt is a clear",
			snapshot: DismissSnapshot{srcSub: accepted(flagged)},
			steps:    []FlagStep{step(srcSub, none, GeoStateExempt, D+10), step(srcSub, suspect, GeoStateSuspect, D+20)},
			now:      AttentionLevels{srcSub: suspect},
			want:     ReviewState{Dismissed: true, Reopened: true, Escalated: []string{srcSub}}},
		{name: "16 cleared, re-entered at the same level",
			snapshot: DismissSnapshot{srcDev: accepted(flagged)},
			steps:    []FlagStep{step(srcDev, none, GeoStateIdle, D+10), step(srcDev, flagged, GeoStateFlagged, D+20)},
			now:      AttentionLevels{srcDev: flagged},
			want:     ReviewState{Dismissed: true, Reopened: true, Escalated: []string{srcDev}}},
		{name: "17 lapsed",
			snapshot:    DismissSnapshot{srcGeo: accepted(flagged, D-100)},
			historyFrom: D,
			now:         AttentionLevels{srcGeo: flagged},
			want:        ReviewState{Dismissed: true, Lapsed: true, Reopened: true, Escalated: []string{srcGeo}}},
		{name: "18 lapsed, nothing now",
			snapshot:    DismissSnapshot{srcGeo: accepted(flagged, D-100)},
			historyFrom: D,
			now:         AttentionLevels{},
			want:        ReviewState{Dismissed: true, Lapsed: true}},
		{name: "19 not lapsed at the boundary",
			snapshot:    DismissSnapshot{srcGeo: accepted(flagged, D-100)},
			historyFrom: D - 100,
			now:         AttentionLevels{srcGeo: flagged},
			want:        ReviewState{Dismissed: true}},
		{name: "20 geo_auto held at dismissal, never lifted",
			snapshot:  DismissSnapshot{srcGeoAuto: accepted(suspended, S)},
			now:       AttentionLevels{srcGeoAuto: suspended},
			heldSince: S,
			want:      ReviewState{Dismissed: true}},
		{name: "21 geo_auto lifted then re-suspended (records)",
			snapshot:  DismissSnapshot{srcGeoAuto: accepted(suspended, S)},
			steps:     []FlagStep{step(srcGeoAuto, none, "", D+5), step(srcGeoAuto, suspended, "", D+50)},
			now:       AttentionLevels{srcGeoAuto: suspended},
			heldSince: D + 50,
			want:      ReviewState{Dismissed: true, Reopened: true, Escalated: []string{srcGeoAuto}}},
		{name: "22 geo_auto re-suspension, records lost",
			snapshot:  DismissSnapshot{srcGeoAuto: accepted(suspended, S)},
			now:       AttentionLevels{srcGeoAuto: suspended},
			heldSince: D + 50,
			want:      ReviewState{Dismissed: true, Reopened: true, Escalated: []string{srcGeoAuto}}},
		{name: "23 geo_auto new after the dismissal",
			snapshot:  DismissSnapshot{srcGeo: accepted(flagged)},
			now:       AttentionLevels{srcGeo: flagged, srcGeoAuto: suspended},
			heldSince: D + 50,
			want:      ReviewState{Dismissed: true, Reopened: true, Escalated: []string{srcGeoAuto}}},
		{name: "24 another source's clear is irrelevant",
			snapshot: DismissSnapshot{srcGeo: accepted(flagged), srcSub: accepted(flagged)},
			steps:    []FlagStep{step(srcDev, none, GeoStateClean, D+1)},
			now:      AttentionLevels{srcGeo: flagged, srcSub: flagged},
			want:     ReviewState{Dismissed: true}},
		{name: "25 two escalations, ordered",
			snapshot: DismissSnapshot{srcGeo: accepted(suspect), srcUsage: accepted(suspect)},
			now:      AttentionLevels{srcUsage: flagged, srcGeo: flagged},
			want:     ReviewState{Dismissed: true, Reopened: true, Escalated: []string{srcGeo, srcUsage}}},
		{name: "26 trusted: location steps ignored",
			snapshot: DismissSnapshot{srcSub: accepted(suspect), srcDev: accepted(suspect)},
			steps:    []FlagStep{step(srcSub, flagged, GeoStateFlagged, D+10)},
			now:      AttentionLevels{srcDev: suspect},
			trusted:  true,
			want:     ReviewState{Dismissed: true}},
		{name: "27 absent source read from D, not from the oldest cutoff",
			snapshot: DismissSnapshot{srcGeo: accepted(flagged, D-100)},
			steps:    []FlagStep{step(srcDev, suspect, GeoStateSuspect, D-50), step(srcDev, none, GeoStateClean, D-20)},
			now:      AttentionLevels{srcGeo: flagged},
			want:     ReviewState{Dismissed: true}},
		// The store settles a leave to unknown with a record of the definite
		// verdict that followed (RiskUnknownSettled): that one is a clear.
		{name: "28 unknown, then clean, then re-entered: new",
			snapshot: DismissSnapshot{srcSub: accepted(flagged)},
			steps: []FlagStep{step(srcSub, none, GeoStateUnknown, D+10), step(srcSub, none, GeoStateClean, D+20),
				step(srcSub, suspect, GeoStateSuspect, D+30), step(srcSub, flagged, GeoStateFlagged, D+40)},
			now:  AttentionLevels{srcSub: flagged},
			want: ReviewState{Dismissed: true, Reopened: true, Escalated: []string{srcSub}}},
		{name: "29 unknown, then back through suspect without a clean: the same",
			snapshot: DismissSnapshot{srcSub: accepted(flagged)},
			steps: []FlagStep{step(srcSub, none, GeoStateUnknown, D+10), step(srcSub, suspect, GeoStateSuspect, D+20),
				step(srcSub, flagged, GeoStateFlagged, D+30)},
			now:  AttentionLevels{srcSub: flagged},
			want: ReviewState{Dismissed: true}},
		// The retention was raised after the prune had deleted past the
		// dismissal: the setting says its history is kept, the records say
		// it is not — the dismissal's own record, written at D, is gone.
		{name: "30 lapsed: records pruned past D, retention since raised",
			snapshot:    DismissSnapshot{srcGeo: accepted(suspect, D-100)},
			historyFrom: D - 1000, keptFrom: D + 1,
			now:  AttentionLevels{srcGeo: suspect},
			want: ReviewState{Dismissed: true, Lapsed: true, Reopened: true, Escalated: []string{srcGeo}}},
		{name: "31 not lapsed: the dismissal's own record is the oldest kept",
			snapshot:    DismissSnapshot{srcGeo: accepted(suspect, D-100)},
			historyFrom: D - 1000, keptFrom: D,
			now:  AttentionLevels{srcGeo: suspect},
			want: ReviewState{Dismissed: true}},
	} {
		t.Run(c.name, func(t *testing.T) {
			r := RiskReview{UserID: 7, DismissedAtMS: D, DismissedBy: 1, Accepted: c.snapshot}
			if c.notDismissed {
				r = RiskReview{UserID: 7}
			}
			in := ReviewInputs{Now: c.now, Trusted: c.trusted, HeldSinceMS: c.heldSince, Steps: c.steps,
				HistoryFromMS: c.historyFrom, KeptFromMS: c.keptFrom}
			got := EvaluateReview(r, in)
			if got.Dismissed != c.want.Dismissed || got.Lapsed != c.want.Lapsed || got.Reopened != c.want.Reopened ||
				!slices.Equal(got.Escalated, c.want.Escalated) {
				t.Fatalf("EvaluateReview = %+v, want %+v", got, c.want)
			}
		})
	}
}

// The rule is pure: it never writes into the inputs it was handed, so the
// read side can evaluate the same snapshot and steps more than once.
func TestEvaluateReview_LeavesItsInputsAlone(t *testing.T) {
	snap := DismissSnapshot{srcGeo: accepted(FlagLevelSuspect)}
	now := AttentionLevels{srcGeo: FlagLevelFlagged}
	steps := []FlagStep{step(srcGeo, FlagLevelFlagged, GeoStateFlagged, reviewD+10)}
	EvaluateReview(RiskReview{DismissedAtMS: reviewD, Accepted: snap}, ReviewInputs{Now: now, Steps: steps})
	if snap[srcGeo] != accepted(FlagLevelSuspect) || len(snap) != 1 || now[srcGeo] != FlagLevelFlagged || len(now) != 1 ||
		steps[0] != step(srcGeo, FlagLevelFlagged, GeoStateFlagged, reviewD+10) {
		t.Fatalf("inputs changed: %+v %+v %+v", snap, now, steps)
	}
}

// The attention sources are every flag source but review, in display order;
// the location sources are the three trust exempts.
func TestAttentionAndLocationSources(t *testing.T) {
	if got, want := AttentionSources(), []string{"geo", "geo_auto", "sub_spread", "devices", "usage_shift", "login_country", "dest_block"}; !slices.Equal(got, want) {
		t.Fatalf("AttentionSources() = %v, want %v", got, want)
	}
	if got, want := LocationSources(), []string{"geo", "sub_spread", "login_country"}; !slices.Equal(got, want) {
		t.Fatalf("LocationSources() = %v, want %v", got, want)
	}
	// A fresh slice per call: a caller appending to one must not grow the next.
	a := AttentionSources()
	a[0] = "mutated"
	if AttentionSources()[0] != "geo" {
		t.Fatal("AttentionSources shares its backing array")
	}
}

// The account's level is its worst verdict, never the hold: geo_auto is a
// state of the service, shown by its own chip, and counts toward urgency on
// its own. Sources read worst first, the hold last; the urgency class is the
// queue's first sort key.
func TestAttentionLevels_MaxHeldSourcesUrgency(t *testing.T) {
	none, suspect, flagged, suspended := FlagLevelNone, FlagLevelSuspect, FlagLevelFlagged, FlagLevelSuspended

	for _, c := range []struct {
		name    string
		a       AttentionLevels
		max     FlagLevel
		held    bool
		empty   bool
		urgency int
	}{
		{"nil", nil, none, false, true, 0},
		{"empty", AttentionLevels{}, none, false, true, 0},
		{"a stray none entry reads as absent", AttentionLevels{srcGeo: none}, none, false, true, 0},
		{"suspect", AttentionLevels{srcGeo: suspect}, suspect, false, false, 1},
		{"held alone", AttentionLevels{srcGeoAuto: suspended}, none, true, false, 2},
		{"held and suspect", AttentionLevels{srcGeoAuto: suspended, srcDev: suspect}, suspect, true, false, 2},
		{"flagged", AttentionLevels{srcDev: flagged, srcGeo: suspect}, flagged, false, false, 3},
		{"flagged and held", AttentionLevels{srcGeo: flagged, srcGeoAuto: suspended}, flagged, true, false, 4},
	} {
		if got := c.a.Max(); got != c.max {
			t.Errorf("%s: Max() = %q, want %q", c.name, got, c.max)
		}
		if got := c.a.Held(); got != c.held {
			t.Errorf("%s: Held() = %v, want %v", c.name, got, c.held)
		}
		if got := c.a.Empty(); got != c.empty {
			t.Errorf("%s: Empty() = %v, want %v", c.name, got, c.empty)
		}
		if got := c.a.UrgencyClass(); got != c.urgency {
			t.Errorf("%s: UrgencyClass() = %d, want %d", c.name, got, c.urgency)
		}
	}

	all := AttentionLevels{
		srcLogin: flagged, srcGeo: suspect, srcGeoAuto: suspended, srcDev: flagged, srcSub: suspect, srcUsage: none,
	}
	if got, want := all.Sources(), []string{srcDev, srcLogin, srcGeo, srcSub, srcGeoAuto}; !slices.Equal(got, want) {
		t.Fatalf("Sources() = %v, want %v", got, want)
	}
	if got := (AttentionLevels{srcGeoAuto: suspended}).Sources(); !slices.Equal(got, []string{srcGeoAuto}) {
		t.Fatalf("held-only Sources() = %v", got)
	}
	if got := AttentionLevels(nil).Sources(); len(got) != 0 {
		t.Fatalf("nil Sources() = %v, want none", got)
	}

	for _, r := range []struct {
		l    FlagLevel
		rank int
	}{{none, 0}, {suspect, 1}, {flagged, 2}, {suspended, 2}} {
		if got := AttentionRank(r.l); got != r.rank {
			t.Errorf("AttentionRank(%q) = %d, want %d", r.l, got, r.rank)
		}
	}

	// Clone is a copy, nil for empty, and drops a stray none entry.
	if AttentionLevels(nil).Clone() != nil || (AttentionLevels{srcGeo: none}).Clone() != nil {
		t.Fatal("Clone of an empty set is not nil")
	}
	src := AttentionLevels{srcGeo: flagged, srcDev: none}
	cp := src.Clone()
	if !maps.Equal(cp, AttentionLevels{srcGeo: flagged}) {
		t.Fatalf("Clone() = %v", cp)
	}
	cp[srcGeo] = suspect
	if src[srcGeo] != flagged {
		t.Fatal("Clone shares its map")
	}
}

// Stored and posted level sets are held to the closed vocabulary: known
// attention sources only, the hold at suspended only, every verdict at
// suspect or flagged. The error names the key and never echoes a value.
func TestValidateAttentionLevels(t *testing.T) {
	for _, ok := range []AttentionLevels{
		nil, {},
		{srcGeo: FlagLevelFlagged, srcGeoAuto: FlagLevelSuspended, srcSub: FlagLevelSuspect, srcDev: FlagLevelFlagged,
			srcUsage: FlagLevelSuspect, srcLogin: FlagLevelFlagged},
	} {
		if err := ValidateAttentionLevels(ok); err != nil {
			t.Errorf("ValidateAttentionLevels(%v) = %v, want nil", ok, err)
		}
	}
	for _, c := range []struct {
		name, key string
		a         AttentionLevels
	}{
		{"unknown key", "bogus", AttentionLevels{"bogus": FlagLevelFlagged}},
		{"review is no attention source", FlagSourceReview, AttentionLevels{FlagSourceReview: FlagLevelFlagged}},
		{"geo_auto flagged", srcGeoAuto, AttentionLevels{srcGeoAuto: FlagLevelFlagged}},
		{"geo suspended", srcGeo, AttentionLevels{srcGeo: FlagLevelSuspended}},
		{"a none entry", srcDev, AttentionLevels{srcDev: FlagLevelNone}},
		{"a made-up level", srcSub, AttentionLevels{srcSub: "evil-value"}},
	} {
		err := ValidateAttentionLevels(c.a)
		if !errors.Is(err, ErrValidation) {
			t.Errorf("%s: err = %v, want ErrValidation", c.name, err)
			continue
		}
		if !strings.Contains(err.Error(), c.key) {
			t.Errorf("%s: %q does not name the key %q", c.name, err, c.key)
		}
		if strings.Contains(err.Error(), "evil-value") {
			t.Errorf("%s: %q echoes the value", c.name, err)
		}
	}
}

// A dismissal snapshot is a level set plus verdict times; a negative time is
// no instant and is refused.
func TestValidateDismissSnapshot(t *testing.T) {
	if err := ValidateDismissSnapshot(nil); err != nil {
		t.Fatalf("empty snapshot: %v", err)
	}
	if err := ValidateDismissSnapshot(DismissSnapshot{srcGeo: {Level: FlagLevelFlagged, AtMS: 0}, srcGeoAuto: {Level: FlagLevelSuspended, AtMS: 5}}); err != nil {
		t.Fatalf("valid snapshot: %v", err)
	}
	for name, d := range map[string]DismissSnapshot{
		"negative at_ms": {srcGeo: {Level: FlagLevelFlagged, AtMS: -1}},
		"bad level":      {srcGeoAuto: {Level: FlagLevelFlagged, AtMS: 5}},
		"unknown source": {"bogus": {Level: FlagLevelFlagged, AtMS: 5}},
	} {
		if err := ValidateDismissSnapshot(d); !errors.Is(err, ErrValidation) {
			t.Errorf("%s: err = %v, want ErrValidation", name, err)
		}
	}
}

// The snapshot's JSON is the risk_reviews.dismissed_levels column contract.
func TestDismissSnapshot_JSONShape(t *testing.T) {
	d := DismissSnapshot{srcGeo: {Level: FlagLevelFlagged, AtMS: 5}}
	b, err := json.Marshal(d)
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != `{"geo":{"level":"flagged","at_ms":5}}` {
		t.Fatalf("marshal = %s", b)
	}
	var back DismissSnapshot
	if err := json.Unmarshal(b, &back); err != nil || !maps.Equal(back, d) {
		t.Fatalf("round trip = %v, %v", back, err)
	}
	if got := d.Levels(); !maps.Equal(got, AttentionLevels{srcGeo: FlagLevelFlagged}) {
		t.Fatalf("Levels() = %v", got)
	}
	if DismissSnapshot(nil).Levels() != nil {
		t.Fatal("Levels() of an empty snapshot is not nil")
	}
}

// Trust exempts the location sources and nothing else: devices and usage are
// not about where the account is, and a hold already applied stays visible.
func TestMaskTrusted(t *testing.T) {
	in := AttentionLevels{
		srcGeo: FlagLevelFlagged, srcGeoAuto: FlagLevelSuspended, srcSub: FlagLevelSuspect,
		srcDev: FlagLevelFlagged, srcUsage: FlagLevelSuspect, srcLogin: FlagLevelFlagged,
	}
	orig := maps.Clone(in)
	got := MaskTrusted(in, true)
	want := AttentionLevels{srcGeoAuto: FlagLevelSuspended, srcDev: FlagLevelFlagged, srcUsage: FlagLevelSuspect}
	if !maps.Equal(got, want) {
		t.Fatalf("MaskTrusted(trusted) = %v, want %v", got, want)
	}
	if !maps.Equal(in, orig) {
		t.Fatalf("input changed: %v", in)
	}
	untrusted := MaskTrusted(in, false)
	if !maps.Equal(untrusted, in) {
		t.Fatalf("MaskTrusted(untrusted) = %v, want the input", untrusted)
	}
	untrusted[srcGeo] = FlagLevelSuspect
	if in[srcGeo] != FlagLevelFlagged {
		t.Fatal("MaskTrusted(untrusted) returned the input map itself")
	}
	if MaskTrusted(AttentionLevels{srcGeo: FlagLevelFlagged}, true) != nil {
		t.Fatal("masking every source did not read as empty (nil)")
	}
}

// A dismissal lapses when its history may be gone, by either measure: its
// oldest cutoff before the retention's (what the prune deletes before now),
// or the dismissal itself before the oldest record still stored (what the
// prune already deleted — more than the retention says once it is raised).
// Both boundaries are kept; 0 is "unknown" for each; nothing that is not a
// dismissal ever lapses.
func TestRiskReview_Lapsed(t *testing.T) {
	const D = reviewD
	r := RiskReview{DismissedAtMS: D, Accepted: DismissSnapshot{srcGeo: accepted(FlagLevelFlagged, D-100)}}
	for _, c := range []struct {
		history, kept int64
		want          bool
	}{
		{0, 0, false},
		{D - 100, 0, false},
		{D - 99, 0, true},
		{0, D, false},
		{0, D + 1, true},
		{D - 1000, D + 1, true},
		{D - 1000, D - 5000, false},
	} {
		if got := r.Lapsed(c.history, c.kept); got != c.want {
			t.Errorf("Lapsed(history %d, kept %d) = %v, want %v", c.history, c.kept, got, c.want)
		}
	}
	if (RiskReview{}).Lapsed(D, D) {
		t.Fatal("a row with no dismissal lapsed")
	}
}

// Each source's records count from that source's own verdict time; a source
// the snapshot does not name, or names without a time, from the dismissal.
// The overall cutoff — how far back the store must read — is the earliest.
func TestRiskReview_Cutoffs(t *testing.T) {
	const D = reviewD
	r := RiskReview{DismissedAtMS: D, Accepted: DismissSnapshot{
		srcGeo: {Level: FlagLevelFlagged, AtMS: D - 100},
		srcDev: {Level: FlagLevelSuspect, AtMS: 0},
	}}
	for src, want := range map[string]int64{srcGeo: D - 100, srcDev: D, srcSub: D} {
		if got := r.SourceCutoffMS(src); got != want {
			t.Errorf("SourceCutoffMS(%s) = %d, want %d", src, got, want)
		}
	}
	if got := r.CutoffMS(); got != D-100 {
		t.Fatalf("CutoffMS() = %d, want %d", got, D-100)
	}
	// A verdict time after the dismissal cannot move the cutoff past it.
	late := RiskReview{DismissedAtMS: D, Accepted: DismissSnapshot{srcGeo: {Level: FlagLevelFlagged, AtMS: D + 50}}}
	if got := late.CutoffMS(); got != D {
		t.Fatalf("CutoffMS() with a late verdict = %d, want %d", got, D)
	}
	if !r.Dismissed() || (RiskReview{}).Dismissed() {
		t.Fatal("Dismissed() does not read DismissedAtMS")
	}
	if got := (RiskReview{Trusted: true}).CutoffMS(); got != 0 {
		t.Fatalf("CutoffMS() when not dismissed = %d, want 0", got)
	}
}

// A clear is a leave to no attention the evidence supports. A leave to
// "cannot tell" (unknown) is not: the evidence went missing, the situation
// did not end. The hold has no state, and its lift always clears.
func TestFlagStep_IsClear(t *testing.T) {
	for _, st := range []GeoState{GeoStateClean, GeoStateIdle, GeoStateExempt, GeoStateDisabled} {
		if !step(srcSub, FlagLevelNone, st, 1).IsClear() {
			t.Errorf("a leave in state %s is not a clear", st)
		}
	}
	if step(srcSub, FlagLevelNone, GeoStateUnknown, 1).IsClear() {
		t.Error("a leave to unknown is a clear")
	}
	if !step(srcGeoAuto, FlagLevelNone, "", 1).IsClear() {
		t.Error("a geo_auto lift is not a clear")
	}
	for _, l := range []FlagLevel{FlagLevelSuspect, FlagLevelFlagged, FlagLevelSuspended} {
		if step(srcGeo, l, GeoStateClean, 1).IsClear() || step(srcGeoAuto, l, "", 1).IsClear() {
			t.Errorf("a step to %s is a clear", l)
		}
	}
}

// Open is "needs a look": attention now, and no dismissal covering it. A
// dismissal is in force only while there is something for it to cover.
// Urgent is what the bell counts: open and flagged, or open and held.
func TestAccountAttention_OpenUrgentInForceSnapshot(t *testing.T) {
	dismissed := RiskReview{UserID: 7, DismissedAtMS: reviewD, Accepted: DismissSnapshot{srcGeo: accepted(FlagLevelFlagged)}}
	reopened := ReviewState{Dismissed: true, Reopened: true, Escalated: []string{srcGeo}}
	for _, c := range []struct {
		name                  string
		a                     AccountAttention
		open, urgent, inForce bool
	}{
		{"not dismissed, suspect", AccountAttention{Levels: AttentionLevels{srcGeo: FlagLevelSuspect}}, true, false, false},
		{"not dismissed, flagged", AccountAttention{Levels: AttentionLevels{srcGeo: FlagLevelFlagged}}, true, true, false},
		{"not dismissed, held", AccountAttention{Levels: AttentionLevels{srcGeoAuto: FlagLevelSuspended}}, true, true, false},
		{"trusted only, not dismissed", AccountAttention{Levels: AttentionLevels{srcDev: FlagLevelSuspect},
			Review: RiskReview{Trusted: true}, HasReview: true}, true, false, false},
		{"dismissed, covered", AccountAttention{Levels: AttentionLevels{srcGeo: FlagLevelFlagged},
			Review: dismissed, HasReview: true, State: ReviewState{Dismissed: true}}, false, false, true},
		{"dismissed, reopened", AccountAttention{Levels: AttentionLevels{srcGeo: FlagLevelFlagged},
			Review: dismissed, HasReview: true, State: reopened}, true, true, false},
		{"empty, not dismissed", AccountAttention{}, false, false, false},
		{"empty, dismissed", AccountAttention{Review: dismissed, HasReview: true, State: ReviewState{Dismissed: true}}, false, false, false},
		{"empty, reopened", AccountAttention{Review: dismissed, HasReview: true, State: reopened}, false, false, false},
	} {
		if got := c.a.Open(); got != c.open {
			t.Errorf("%s: Open() = %v, want %v", c.name, got, c.open)
		}
		if got := c.a.Urgent(); got != c.urgent {
			t.Errorf("%s: Urgent() = %v, want %v", c.name, got, c.urgent)
		}
		if got := c.a.DismissedInForce(); got != c.inForce {
			t.Errorf("%s: DismissedInForce() = %v, want %v", c.name, got, c.inForce)
		}
	}

	a := AccountAttention{
		Levels: AttentionLevels{srcGeo: FlagLevelFlagged, srcGeoAuto: FlagLevelSuspended, srcDev: FlagLevelSuspect},
		AtMS:   map[string]int64{srcGeo: 11, srcGeoAuto: 22},
	}
	want := DismissSnapshot{
		srcGeo:     {Level: FlagLevelFlagged, AtMS: 11},
		srcGeoAuto: {Level: FlagLevelSuspended, AtMS: 22},
		srcDev:     {Level: FlagLevelSuspect, AtMS: 0},
	}
	if got := a.Snapshot(); !maps.Equal(got, want) {
		t.Fatalf("Snapshot() = %v, want %v", got, want)
	}
	if err := ValidateDismissSnapshot(a.Snapshot()); err != nil {
		t.Fatalf("Snapshot() does not validate: %v", err)
	}
	if (AccountAttention{}).Snapshot() != nil {
		t.Fatal("Snapshot() of no attention is not nil")
	}
}

// A review record says who acted and, for a dismissal, what was accepted —
// the admin's id, never a name, never the note, never an address. It moves
// no attention level: its levels and state are empty and its code is the
// event.
func TestReviewFlag_LevelsParamsAndCode(t *testing.T) {
	at := time.UnixMilli(1_758_000_000_000)
	rec := ReviewFlag(42, FlagReviewDismissed, ReviewFlagParams{By: 7, Levels: AttentionLevels{srcGeo: FlagLevelFlagged}}, at)
	want := FlagRecord{UserID: 42, Source: FlagSourceReview, Event: FlagReviewDismissed, Code: "dismissed", AtMS: at.UnixMilli()}
	got := rec
	got.Params = nil
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ReviewFlag = %+v\nwant %+v (no level, no state)", got, want)
	}
	if string(rec.Params) != `{"by":7,"levels":{"geo":"flagged"}}` {
		t.Fatalf("params = %s", rec.Params)
	}
	assertNoAddressIn(t, rec.Params)

	for _, ev := range []FlagEvent{FlagReviewUndismissed, FlagReviewTrusted, FlagReviewUntrusted} {
		r := ReviewFlag(42, ev, ReviewFlagParams{By: 7}, at)
		if r.Source != FlagSourceReview || r.Event != ev || r.Code != string(ev) || string(r.Params) != `{"by":7}` {
			t.Errorf("%s: %+v params %s", ev, r, r.Params)
		}
	}
	if r := ReviewFlag(42, FlagReviewTrusted, ReviewFlagParams{By: 7, Levels: AttentionLevels{}}, at); string(r.Params) != `{"by":7}` {
		t.Errorf("empty levels not omitted: %s", r.Params)
	}
	if r := ReviewFlag(42, FlagReviewTrusted, ReviewFlagParams{By: 7}, time.Time{}); r.AtMS != 0 {
		t.Fatalf("zero time stamped %d; want 0 so the store stamps it", r.AtMS)
	}
	// The events are stored strings (flag_records.event), so their values
	// are pinned, not just their names.
	for ev, s := range map[FlagEvent]string{
		FlagReviewDismissed: "dismissed", FlagReviewUndismissed: "undismissed",
		FlagReviewTrusted: "trusted", FlagReviewUntrusted: "untrusted",
	} {
		if string(ev) != s {
			t.Errorf("event %q, want %q", ev, s)
		}
	}
}

// The evidence-free reads (the queue reads two streak columns, not a whole
// record) must judge geo attention exactly as GeoAttention does.
func TestGeoAttentionOf_MatchesGeoAttention(t *testing.T) {
	for _, flagged := range []bool{false, true} {
		for _, over := range []int{0, 1, 2, 5} {
			rec := GeoRecord{Streak: GeoStreak{Flagged: flagged, Over: over}}
			if got, want := GeoAttentionOf(flagged, over), GeoAttention(rec); got != want {
				t.Errorf("GeoAttentionOf(%v, %d) = %q, GeoAttention = %q", flagged, over, got, want)
			}
		}
	}
	for _, c := range []struct {
		flagged bool
		over    int
		want    FlagLevel
	}{
		{true, 0, FlagLevelFlagged}, {false, 2, FlagLevelSuspect}, {false, 0, FlagLevelNone},
	} {
		if got := GeoAttentionOf(c.flagged, c.over); got != c.want {
			t.Errorf("GeoAttentionOf(%v, %d) = %q, want %q", c.flagged, c.over, got, c.want)
		}
	}
}
