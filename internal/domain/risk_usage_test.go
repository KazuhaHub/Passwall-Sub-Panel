package domain

import (
	"encoding/json"
	"math"
	"reflect"
	"slices"
	"sort"
	"strings"
	"testing"
)

const usageGiB = RiskGiB

// defaultUsagePolicy is the shipped policy as the worker hands it over:
// ratio 3, floor 3 GiB.
func defaultUsagePolicy() UsageShiftPolicy {
	return UsageShiftPolicy{Ratio: RiskDefaultUsageRatio, FloorBytes: RiskDefaultUsageFloorGB * RiskGiB}
}

// usageSeriesDays is the shipped series length: 28 baseline days, then the 7
// recent days that are judged.
const usageSeriesDays = RiskUsageBaselineDays + RiskUsageRecentDays

// usageDays builds a series of the shipped 35 days, oldest first: k = 0..27
// is the baseline, k = 28..34 the recent week.
func usageDays(fill func(k int) int64) []int64 {
	return usageDaysOf(usageSeriesDays, fill)
}

// usageDaysOf builds an n-day series, oldest first.
func usageDaysOf(n int, fill func(k int) int64) []int64 {
	s := make([]int64, n)
	for k := range s {
		s[k] = fill(k)
	}
	return s
}

// soloInput is an account alone on its panel: the fleet is the account, so
// everyone else used nothing and the fleet factor is 1 on every day. The
// fleet is a copy — withFleet adds to it, and must not add to the account.
func soloInput(user []int64) UsageShiftInput {
	return UsageShiftInput{EndDate: "2026-09-24", User: user, Fleet: slices.Clone(user)}
}

// withFleet adds the rest of the fleet's usage to the account's own.
func withFleet(user, others []int64) UsageShiftInput {
	in := soloInput(user)
	for k := range in.Fleet {
		in.Fleet[k] = user[k] + others[k]
	}
	return in
}

// steadyWithRecent is 1 GiB a day, with the listed recent days (0 = the
// oldest of the seven) at 10 GiB.
func steadyWithRecent(recent ...int) []int64 {
	return steadyOf(RiskUsageBaselineDays, RiskUsageRecentDays, recent...)
}

// steadyOf is steadyWithRecent for a series of baseline + recent days:
// 1 GiB a day, the listed recent days (0 = the oldest judged day) at 10 GiB.
func steadyOf(baseline, recent int, over ...int) []int64 {
	s := usageDaysOf(baseline+recent, func(int) int64 { return usageGiB })
	for _, i := range over {
		s[baseline+i] = 10 * usageGiB
	}
	return s
}

// usageDayPolicy is the shipped ratio and floor judged with the given days:
// the fleet's baseline and recent lengths, the group's warm-up and over-day
// thresholds. 0 leaves a knob unset.
func usageDayPolicy(baseline, recent, warmup, flag, suspect int) UsageShiftPolicy {
	p := defaultUsagePolicy()
	p.BaselineDays, p.RecentDays = baseline, recent
	p.WarmupDays, p.FlagDays, p.SuspectDays = warmup, flag, suspect
	return p
}

func wantUsage(t *testing.T, v RiskVerdict, state GeoState, code RiskCode) {
	t.Helper()
	if v.State != state || v.Code != code {
		t.Fatalf("verdict = %s/%s, want %s/%s", v.State, v.Code, state, code)
	}
}

// An account needs 14 days of its own history before its usage is judged.
// A median over fewer days is a median over a first week that is mostly
// setup — importing profiles, a first big download — and would call the
// account's ordinary second week "a change".
func TestUsageShift_WarmupUntilFourteenDays(t *testing.T) {
	for _, c := range []struct {
		history int
		state   GeoState
		code    RiskCode
	}{
		{13, GeoStateUnknown, RiskCodeWarmup},
		{14, GeoStateClean, RiskCodeWithin},
	} {
		first := RiskUsageBaselineDays - c.history
		user := usageDays(func(k int) int64 {
			if k >= first {
				return usageGiB
			}
			return 0
		})
		v, ev := EvaluateUsageShift(defaultUsagePolicy(), soloInput(user))
		wantUsage(t, v, c.state, c.code)
		if ev == nil || ev.HistoryDays != c.history {
			t.Fatalf("history %d: evidence = %+v, want HistoryDays %d", c.history, ev, c.history)
		}
	}
}

// The baseline median is taken over the days since the account first used
// anything, not over all 28. An account that started two weeks ago at a
// steady 2 GiB a day has a median of 2 GiB; counting the 14 days before it
// existed as zeros would halve that, and the account's own ordinary usage
// would read as a sustained rise.
//
// The policy is loosened (ratio 1.5, floor 1 GiB) so that the halved median
// would actually cross: at 2 GiB the threshold is 3 GiB and 2.5 GiB is
// within; at 1 GiB it would be 1.5 GiB and every recent day would be over.
func TestUsageShift_BaselineIgnoresDaysBeforeFirstUse(t *testing.T) {
	user := usageDays(func(k int) int64 {
		switch {
		case k < 14:
			return 0
		case k >= 28 && k <= 31:
			return 5 * usageGiB / 2
		default:
			return 2 * usageGiB
		}
	})
	v, ev := EvaluateUsageShift(UsageShiftPolicy{Ratio: 1.5, FloorBytes: usageGiB}, soloInput(user))
	wantUsage(t, v, GeoStateClean, RiskCodeWithin)
	if ev.HistoryDays != 14 || ev.Median != 2*usageGiB || ev.OverDays != 0 {
		t.Fatalf("history %d, median %d, over %d; want 14, %d, 0", ev.HistoryDays, ev.Median, ev.OverDays, 2*usageGiB)
	}
}

// Zeros AFTER the first day of use are real: the account existed and used
// nothing. A median of zero makes every threshold zero, so the floor is what
// keeps a light account's first real week from reading as a change — and
// above the floor, a sustained rise from nothing is exactly the signal. A day
// is over only ABOVE its threshold: four days at exactly the floor are not
// four over-days.
func TestUsageShift_ZeroMedianUsesTheFloor(t *testing.T) {
	for _, c := range []struct {
		recent int64
		state  GeoState
		code   RiskCode
		over   int
	}{
		{4 * usageGiB, GeoStateFlagged, RiskCodeSustained, 4},
		{2 * usageGiB, GeoStateClean, RiskCodeWithin, 0},
		{3 * usageGiB, GeoStateClean, RiskCodeWithin, 0},
	} {
		user := usageDays(func(k int) int64 {
			switch {
			case k == 0:
				return usageGiB
			case k >= 28 && k <= 31:
				return c.recent
			}
			return 0
		})
		v, ev := EvaluateUsageShift(defaultUsagePolicy(), soloInput(user))
		wantUsage(t, v, c.state, c.code)
		if ev.Median != 0 || ev.OverDays != c.over {
			t.Fatalf("recent days at %d: median %d, over days %d; want 0 and %d", c.recent, ev.Median, ev.OverDays, c.over)
		}
		for i, th := range ev.Thresholds {
			if th != 3*usageGiB {
				t.Fatalf("threshold[%d] = %d, want the floor %d", i, th, 3*usageGiB)
			}
		}
	}
}

// One heavy day is a download, not a change of habit.
func TestUsageShift_OneDaySpikeStaysClean(t *testing.T) {
	v, ev := EvaluateUsageShift(defaultUsagePolicy(), soloInput(steadyWithRecent(3)))
	wantUsage(t, v, GeoStateClean, RiskCodeWithin)
	if ev.OverDays != 1 {
		t.Fatalf("over days = %d, want 1 (the spike is counted, just not judged)", ev.OverDays)
	}
}

// Two or three heavy days in a week is worth a look, not a flag.
func TestUsageShift_TwoDaySpikeIsSuspect(t *testing.T) {
	for _, recent := range [][]int{{2, 3}, {0, 3, 6}} {
		v, ev := EvaluateUsageShift(defaultUsagePolicy(), soloInput(steadyWithRecent(recent...)))
		wantUsage(t, v, GeoStateSuspect, RiskCodeBuilding)
		if ev.OverDays != len(recent) {
			t.Fatalf("over days = %d, want %d", ev.OverDays, len(recent))
		}
	}
}

// Four heavy days of seven is the week's habit. They need not be
// consecutive: someone else using the account on alternate days is the same
// change as someone using it every day of a four-day stretch.
func TestUsageShift_FourOfSevenIsFlagged(t *testing.T) {
	for _, recent := range [][]int{{0, 2, 4, 6}, {0, 1, 2, 3, 4, 5, 6}} {
		v, ev := EvaluateUsageShift(defaultUsagePolicy(), soloInput(steadyWithRecent(recent...)))
		wantUsage(t, v, GeoStateFlagged, RiskCodeSustained)
		want := make([]bool, RiskUsageRecentDays)
		for _, i := range recent {
			want[i] = true
		}
		if !reflect.DeepEqual(ev.Over, want) {
			t.Fatalf("over = %v, want %v", ev.Over, want)
		}
	}
}

// A holiday, a new streaming season, a fleet-wide software update: when
// everyone's usage rises, one account's rise is not a change in who uses it.
// The rest of the fleet quadrupled this week, so this account's 3.5x is
// within — without the fleet factor it would be flagged on all seven days.
func TestUsageShift_FleetWideRiseIsDampened(t *testing.T) {
	user := usageDays(func(k int) int64 {
		if k >= RiskUsageBaselineDays {
			return 7 * usageGiB
		}
		return 2 * usageGiB
	})
	others := usageDays(func(k int) int64 {
		if k >= RiskUsageBaselineDays {
			return 400 * usageGiB
		}
		return 100 * usageGiB
	})
	v, ev := EvaluateUsageShift(defaultUsagePolicy(), withFleet(user, others))
	wantUsage(t, v, GeoStateClean, RiskCodeWithin)
	for i, ff := range ev.FleetFactors {
		if ff != 4 {
			t.Fatalf("fleet factor[%d] = %v, want 4", i, ff)
		}
	}
	if ev.Thresholds[0] != 24*usageGiB {
		t.Fatalf("threshold = %d, want 3 x 2 GiB x 4 = %d", ev.Thresholds[0], 24*usageGiB)
	}
}

// The fleet factor leaves the judged account out. On a small panel one heavy
// account IS most of the fleet; counted in, its own rise would raise its own
// factor and excuse itself — here from 3x (flagged) down to within.
func TestUsageShift_OwnRiseDoesNotRaiseItsOwnFleetFactor(t *testing.T) {
	user := usageDays(func(k int) int64 {
		if k >= 28 && k <= 31 {
			return 10 * usageGiB
		}
		return 2 * usageGiB
	})
	others := usageDays(func(int) int64 { return 2 * usageGiB })
	v, ev := EvaluateUsageShift(defaultUsagePolicy(), withFleet(user, others))
	wantUsage(t, v, GeoStateFlagged, RiskCodeSustained)
	for i, ff := range ev.FleetFactors {
		if ff != 1 {
			t.Fatalf("fleet factor[%d] = %v, want 1 — the account's own rise leaked into its factor", i, ff)
		}
	}
}

// No traffic this week is not evidence of anything, and a stored series of
// zeros would only be noise: idle, with no evidence at all.
func TestUsageShift_AllRecentZeroIsIdleWithNilEvidence(t *testing.T) {
	for name, user := range map[string][]int64{
		"quiet week after use": usageDays(func(k int) int64 {
			if k < RiskUsageBaselineDays {
				return 5 * usageGiB
			}
			return 0
		}),
		"never used": make([]int64, usageSeriesDays),
	} {
		v, ev := EvaluateUsageShift(defaultUsagePolicy(), soloInput(user))
		if v.State != GeoStateIdle || v.Code != RiskCodeNoUsage || ev != nil {
			t.Fatalf("%s: %s/%s with evidence %+v, want idle/no_usage and nil evidence", name, v.State, v.Code, ev)
		}
	}
}

// Off means off before anything else: no judgement and no evidence, even for
// usage that would be flagged and a retention that could not be judged.
func TestUsageShift_OffIsDisabled(t *testing.T) {
	p := defaultUsagePolicy()
	p.Off = true
	in := soloInput(steadyWithRecent(0, 1, 2, 3, 4, 5, 6))
	for _, retention := range []int{0, 30} {
		in.HistoryRetentionDays = retention
		v, ev := EvaluateUsageShift(p, in)
		if v.State != GeoStateDisabled || v.Code != RiskCodeSignalOff || ev != nil {
			t.Fatalf("retention %d: %s/%s with evidence %+v, want disabled/signal_off and nil evidence", retention, v.State, v.Code, ev)
		}
	}
}

// The hourly rollup is pruned at traffic_history_days. Below 35, the oldest
// baseline days are gone and read as zeros — a shortened, deflated baseline
// that would accuse. Unknown, never clean: the series cannot be trusted. At
// exactly 35 it still cannot: the prune cuts at now minus 35 days, an
// instant, while the series starts at the local midnight 35 days ago, so
// once today has begun the first day is already partly deleted. 36 keeps it
// whole. 0 means the rollup is never pruned.
func TestUsageShift_HistoryRetentionShorterThanTheSeriesIsUnknown(t *testing.T) {
	user := steadyWithRecent(0, 1, 2, 3)
	for _, c := range []struct {
		retention int
		state     GeoState
		code      RiskCode
	}{
		{30, GeoStateUnknown, RiskCodeRetentionShort},
		{34, GeoStateUnknown, RiskCodeRetentionShort},
		{35, GeoStateUnknown, RiskCodeRetentionShort},
		{36, GeoStateFlagged, RiskCodeSustained},
		{0, GeoStateFlagged, RiskCodeSustained},
	} {
		in := soloInput(user)
		in.HistoryRetentionDays = c.retention
		v, ev := EvaluateUsageShift(defaultUsagePolicy(), in)
		if v.State != c.state || v.Code != c.code {
			t.Fatalf("retention %d: %s/%s, want %s/%s", c.retention, v.State, v.Code, c.state, c.code)
		}
		if ev == nil || ev.HistoryRetentionDays != c.retention {
			t.Fatalf("retention %d: evidence %+v does not carry it", c.retention, ev)
		}
	}
}

// A fleet factor is a ratio of two medians and can be arbitrarily large.
// Ratio × median × factor then exceeds int64, and converting such a float is
// implementation-defined in Go (amd64 yields the minimum int64, which the
// floor would then replace — turning a huge threshold into the smallest one).
// It must saturate: an unrepresentably high threshold is still high.
func TestUsageShift_HugeFleetFactorSaturatesTheThreshold(t *testing.T) {
	user := steadyWithRecent(0, 1, 2, 3, 4, 5, 6)
	others := usageDays(func(k int) int64 {
		if k >= RiskUsageBaselineDays {
			return 1e18
		}
		return 1
	})
	v, ev := EvaluateUsageShift(defaultUsagePolicy(), withFleet(user, others))
	wantUsage(t, v, GeoStateClean, RiskCodeWithin)
	for i, th := range ev.Thresholds {
		if th != math.MaxInt64 {
			t.Fatalf("threshold[%d] = %d, want saturated at %d", i, th, int64(math.MaxInt64))
		}
	}
}

// The evidence is what the admin UI draws: the whole series, the median and
// every number the recent week was held to. Its field names are a wire
// contract (the SPA reads them straight from the API), fleet factors are
// rounded for display, and a verdict that stopped before judging still sends
// empty lists rather than nulls.
func TestUsageShift_EvidenceShape(t *testing.T) {
	user := usageDays(func(k int) int64 {
		if k == 30 {
			return 20 * usageGiB
		}
		return 2 * usageGiB
	})
	others := usageDays(func(k int) int64 {
		if k == RiskUsageBaselineDays {
			return 9 * usageGiB / 2 // factor 1.125 on the first recent day
		}
		return 4 * usageGiB
	})
	in := withFleet(user, others)
	in.HistoryRetentionDays = 730
	v, ev := EvaluateUsageShift(defaultUsagePolicy(), in)
	wantUsage(t, v, GeoStateClean, RiskCodeWithin)
	want := &UsageShiftEvidence{
		V:                    RiskEvidenceVersion,
		EndDate:              "2026-09-24",
		HistoryRetentionDays: 730,
		Series:               user,
		HistoryDays:          28,
		Median:               2 * usageGiB,
		Ratio:                3,
		Floor:                3 * usageGiB,
		// 3 x 2 GiB = 6 GiB; x 1.125 on the first day = 6.75 GiB.
		Thresholds: []int64{27 * usageGiB / 4, 6 * usageGiB, 6 * usageGiB, 6 * usageGiB, 6 * usageGiB, 6 * usageGiB, 6 * usageGiB},
		Over:       []bool{false, false, true, false, false, false, false},
		OverDays:   1,
		// Displayed to two decimals; the threshold used the exact factor.
		FleetFactors: []float64{1.13, 1, 1, 1, 1, 1, 1},
		// The days it was judged with: the policy set none, so the shipped
		// ones, as numbers rather than as an absence.
		BaselineDays: 28,
		RecentDays:   7,
		WarmupDays:   14,
		FlagDays:     4,
		SuspectDays:  2,
	}
	if !reflect.DeepEqual(ev, want) {
		t.Fatalf("evidence =\n%+v\nwant\n%+v", ev, want)
	}

	raw, err := json.Marshal(ev)
	if err != nil {
		t.Fatal(err)
	}
	var keys map[string]json.RawMessage
	if err := json.Unmarshal(raw, &keys); err != nil {
		t.Fatal(err)
	}
	got := make([]string, 0, len(keys))
	for k := range keys {
		got = append(got, k)
	}
	sort.Strings(got)
	wantKeys := []string{"baseline_days", "end_date", "flag_days", "fleet_factors", "floor", "history_days", "history_retention_days", "median", "over", "over_days", "ratio", "recent_days", "series", "suspect_days", "thresholds", "v", "warmup_days"}
	if !reflect.DeepEqual(got, wantKeys) {
		t.Fatalf("evidence keys = %v, want %v", got, wantKeys)
	}

	// Stopped before judging: the series and history only, lists empty.
	in = soloInput(usageDays(func(k int) int64 {
		if k >= 20 {
			return usageGiB
		}
		return 0
	}))
	v, ev = EvaluateUsageShift(defaultUsagePolicy(), in)
	wantUsage(t, v, GeoStateUnknown, RiskCodeWarmup)
	raw, err = json.Marshal(ev)
	if err != nil {
		t.Fatal(err)
	}
	for _, frag := range []string{`"thresholds":[]`, `"over":[]`, `"fleet_factors":[]`, `"history_days":8`, `"median":0`, `"end_date":"2026-09-24"`, `"warmup_days":14`, `"recent_days":7`} {
		if !strings.Contains(string(raw), frag) {
			t.Fatalf("warmup evidence %s lacks %s", raw, frag)
		}
	}
	if len(ev.Series) != usageSeriesDays {
		t.Fatalf("warmup series has %d days, want %d", len(ev.Series), usageSeriesDays)
	}
	if strings.Contains(string(raw), "history_retention_days") {
		t.Fatalf("warmup evidence %s carries history_retention_days for an unbounded rollup", raw)
	}
}

// usageFixtures reaches every branch of EvaluateUsageShift once.
func usageFixtures() []struct {
	p  UsageShiftPolicy
	in UsageShiftInput
} {
	off := defaultUsagePolicy()
	off.Off = true
	short := soloInput(steadyWithRecent())
	short.HistoryRetentionDays = 30
	young := soloInput(usageDays(func(k int) int64 {
		if k >= 20 {
			return usageGiB
		}
		return 0
	}))
	return []struct {
		p  UsageShiftPolicy
		in UsageShiftInput
	}{
		{off, soloInput(steadyWithRecent())},
		{defaultUsagePolicy(), short},
		{defaultUsagePolicy(), soloInput(make([]int64, usageSeriesDays))},
		{defaultUsagePolicy(), young},
		{defaultUsagePolicy(), soloInput(steadyWithRecent(0, 1, 2, 3))},
		{defaultUsagePolicy(), soloInput(steadyWithRecent(0, 1))},
		{defaultUsagePolicy(), soloInput(steadyWithRecent())},
	}
}

// AllRiskCodes()[usage_shift] is the list the SPA's locale keys are checked
// against, so it must be exactly the codes this evaluator can return.
func TestUsageShift_CodesAreExactlyAllRiskCodes(t *testing.T) {
	reached := map[RiskCode]bool{}
	for _, f := range usageFixtures() {
		v, _ := EvaluateUsageShift(f.p, f.in)
		reached[v.Code] = true
	}
	listed := map[RiskCode]bool{}
	for _, c := range AllRiskCodes()[RiskKindUsageShift] {
		listed[c] = true
	}
	if !reflect.DeepEqual(reached, listed) {
		t.Fatalf("usage_shift codes reached %v, AllRiskCodes lists %v", reached, listed)
	}
}

// The series' two lengths are the fleet's (risk.usage_baseline_days,
// risk.usage_recent_days), handed over in the policy: with a two-week
// baseline and three judged days the evaluator reads a 17-day series, takes
// the median over its first 14 and judges the last 3. Two of three over,
// with a group that flags at two, is the judged days' habit.
func TestEvaluateUsageShift_BaselineAndRecentComeFromThePolicy(t *testing.T) {
	p := usageDayPolicy(14, 3, 0, 2, 0)
	v, ev := EvaluateUsageShift(p, soloInput(steadyOf(14, 3, 0, 2)))
	wantUsage(t, v, GeoStateFlagged, RiskCodeSustained)
	if len(ev.Series) != 17 || ev.HistoryDays != 14 || ev.Median != usageGiB {
		t.Fatalf("series %d days, history %d, median %d; want 17, 14 and 1 GiB", len(ev.Series), ev.HistoryDays, ev.Median)
	}
	if !reflect.DeepEqual(ev.Over, []bool{true, false, true}) || ev.OverDays != 2 ||
		len(ev.Thresholds) != 3 || len(ev.FleetFactors) != 3 {
		t.Fatalf("over %v (%d days), %d thresholds, %d factors; want the 3 judged days, 2 over", ev.Over, ev.OverDays, len(ev.Thresholds), len(ev.FleetFactors))
	}

	// One of the three is a download, as one of seven is.
	v, _ = EvaluateUsageShift(p, soloInput(steadyOf(14, 3, 1)))
	wantUsage(t, v, GeoStateClean, RiskCodeWithin)
}

// The warm-up and the two over-day thresholds are the group's. A warm-up of
// 7 judges an account with 8 days of history, which the shipped 14 still
// calls learning; a group that flags at 5 and calls 3 suspect reads four
// over-days as suspect and two as within, where the shipped 4 and 2 read
// them as flagged and suspect.
func TestEvaluateUsageShift_ThresholdsComeFromThePolicy(t *testing.T) {
	young := soloInput(usageDays(func(k int) int64 {
		switch {
		case k >= RiskUsageBaselineDays && k < RiskUsageBaselineDays+4:
			return 10 * usageGiB
		case k >= 20:
			return usageGiB
		}
		return 0
	}))
	v, _ := EvaluateUsageShift(defaultUsagePolicy(), young)
	wantUsage(t, v, GeoStateUnknown, RiskCodeWarmup)
	v, ev := EvaluateUsageShift(usageDayPolicy(0, 0, 7, 0, 0), young)
	wantUsage(t, v, GeoStateFlagged, RiskCodeSustained)
	if ev.HistoryDays != 8 || ev.WarmupDays != 7 {
		t.Fatalf("history %d, warm-up %d; want 8 judged under a warm-up of 7", ev.HistoryDays, ev.WarmupDays)
	}

	strict := usageDayPolicy(0, 0, 0, 5, 3)
	for _, c := range []struct {
		over          []int
		shipped, mine GeoState
	}{
		{[]int{0, 1, 2, 3, 4}, GeoStateFlagged, GeoStateFlagged},
		{[]int{0, 1, 2, 3}, GeoStateFlagged, GeoStateSuspect},
		{[]int{0, 1, 2}, GeoStateSuspect, GeoStateSuspect},
		{[]int{0, 1}, GeoStateSuspect, GeoStateClean},
	} {
		in := soloInput(steadyWithRecent(c.over...))
		if v, _ := EvaluateUsageShift(defaultUsagePolicy(), in); v.State != c.shipped {
			t.Errorf("%d over-days under the shipped 4/2: %s, want %s", len(c.over), v.State, c.shipped)
		}
		if v, _ := EvaluateUsageShift(strict, in); v.State != c.mine {
			t.Errorf("%d over-days under a group's 5/3: %s, want %s", len(c.over), v.State, c.mine)
		}
	}
}

// The evidence records the days each verdict was judged with — the fleet's
// lengths and the group's thresholds, resolved — so the admin's sentence
// ("2 of the last 3 days", "flagged at 2", "judged from 7") reads the
// numbers that applied, never the shipped ones. They are always present,
// on a verdict that stopped before judging too: the UI must not have to
// guess them.
func TestEvaluateUsageShift_EvidenceCarriesTheDaysItWasJudgedWith(t *testing.T) {
	daysOf := func(v RiskVerdict, ev *UsageShiftEvidence) [5]int {
		t.Helper()
		if ev == nil {
			t.Fatalf("%s/%s carries no evidence", v.State, v.Code)
		}
		return [5]int{ev.BaselineDays, ev.RecentDays, ev.WarmupDays, ev.FlagDays, ev.SuspectDays}
	}
	v, ev := EvaluateUsageShift(usageDayPolicy(20, 5, 10, 3, 2), soloInput(steadyOf(20, 5)))
	if got := daysOf(v, ev); got != [5]int{20, 5, 10, 3, 2} {
		t.Fatalf("judged with 20/5/10/3/2, evidence says %v", got)
	}
	v, ev = EvaluateUsageShift(defaultUsagePolicy(), soloInput(steadyWithRecent()))
	if got := daysOf(v, ev); got != [5]int{28, 7, 14, 4, 2} {
		t.Fatalf("judged with no day set, evidence says %v; want the shipped 28/7/14/4/2 it judged with", got)
	}

	short := soloInput(steadyOf(20, 5))
	short.HistoryRetentionDays = 10
	young := soloInput(usageDaysOf(25, func(k int) int64 {
		if k >= 18 {
			return usageGiB
		}
		return 0
	}))
	for name, in := range map[string]UsageShiftInput{"retention_short": short, "warmup": young} {
		v, ev := EvaluateUsageShift(usageDayPolicy(20, 5, 10, 3, 2), in)
		if v.Code != RiskCode(name) {
			t.Fatalf("%s fixture read %s", name, v.Code)
		}
		raw, err := json.Marshal(ev)
		if err != nil {
			t.Fatal(err)
		}
		for _, frag := range []string{`"baseline_days":20`, `"recent_days":5`, `"warmup_days":10`, `"flag_days":3`, `"suspect_days":2`} {
			if !strings.Contains(string(raw), frag) {
				t.Errorf("%s evidence %s lacks %s", name, raw, frag)
			}
		}
	}
}

// The series must be exactly baseline + recent days long, both the account's
// and the fleet's. Any other length is a series that does not describe the
// days the policy judges: read anyway, it would index past its end or take
// a recent day for a baseline one. It is what a series cut short looks like,
// so it reads unknown/retention_short — never clean, never a panic.
func TestEvaluateUsageShift_SeriesLengthMismatchIsRetentionShort(t *testing.T) {
	longFleet := soloInput(steadyWithRecent(0, 1, 2, 3))
	longFleet.Fleet = append(longFleet.Fleet, usageGiB)
	for name, c := range map[string]struct {
		p  UsageShiftPolicy
		in UsageShiftInput
	}{
		"the shipped 35 days under a 14+3 policy": {usageDayPolicy(14, 3, 0, 2, 0), soloInput(steadyWithRecent(0, 1, 2, 3))},
		"17 days under the shipped 28+7":          {defaultUsagePolicy(), soloInput(steadyOf(14, 3, 0, 1, 2))},
		"a fleet one day longer":                  {defaultUsagePolicy(), longFleet},
		"no series at all":                        {defaultUsagePolicy(), UsageShiftInput{EndDate: "2026-09-24"}},
	} {
		v, ev := EvaluateUsageShift(c.p, c.in)
		if v.State != GeoStateUnknown || v.Code != RiskCodeRetentionShort {
			t.Errorf("%s: %s/%s, want unknown/retention_short", name, v.State, v.Code)
			continue
		}
		if ev == nil || ev.Series == nil || len(ev.Thresholds) != 0 || ev.Thresholds == nil || ev.OverDays != 0 {
			t.Errorf("%s: evidence %+v, want the series as handed over and empty (not null) lists", name, ev)
		}
	}
}

// The rollup's retention is held against the series actually read: with a
// 14+3 policy the series is 17 days, so a 17-day retention is short (the
// prune has already begun on its first day, as 35 is under the shipped 35)
// and 18 keeps it whole. 20 is judged — under the old fixed 35 it read
// retention_short for a series that was intact.
func TestEvaluateUsageShift_RetentionShortCoversTheWholeSeries(t *testing.T) {
	p := usageDayPolicy(14, 3, 0, 2, 0)
	for _, c := range []struct {
		retention int
		state     GeoState
		code      RiskCode
	}{
		{16, GeoStateUnknown, RiskCodeRetentionShort},
		{17, GeoStateUnknown, RiskCodeRetentionShort},
		{18, GeoStateFlagged, RiskCodeSustained},
		{20, GeoStateFlagged, RiskCodeSustained},
		{0, GeoStateFlagged, RiskCodeSustained},
	} {
		in := soloInput(steadyOf(14, 3, 0, 2))
		in.HistoryRetentionDays = c.retention
		v, ev := EvaluateUsageShift(p, in)
		if v.State != c.state || v.Code != c.code {
			t.Errorf("retention %d under a 17-day series: %s/%s, want %s/%s", c.retention, v.State, v.Code, c.state, c.code)
		}
		if ev == nil || ev.HistoryRetentionDays != c.retention {
			t.Errorf("retention %d: evidence %+v does not carry it", c.retention, ev)
		}
	}
}

// (guard) Upgrading must change nothing. A policy that sets none of the
// day knobs judges exactly as one that sets the shipped 28/7/14/4/2 — the
// constants these settings replaced, frozen here as literals — and the
// defaults the worker hands over (DefaultRiskRuntime's lengths,
// DefaultRiskPolicy's thresholds) are those same numbers.
//
// Mutation: a default flag of 5 turns this red.
func TestEvaluateUsageShift_DefaultPolicyMatchesTheFormerConstants(t *testing.T) {
	rt, p := DefaultRiskRuntime(), DefaultRiskPolicy()
	if got := [5]int{rt.UsageBaselineDays, rt.UsageRecentDays, p.UsageWarmupDays, p.UsageFlagDays, p.UsageSuspectDays}; got != [5]int{28, 7, 14, 4, 2} {
		t.Fatalf("shipped usage days = %v, want the former constants 28/7/14/4/2", got)
	}
	if got := [5]int{RiskUsageBaselineDays, RiskUsageRecentDays, RiskUsageWarmupDays, RiskUsageFlagDays, RiskUsageSuspectDays}; got != [5]int{28, 7, 14, 4, 2} {
		t.Fatalf("the named defaults moved: %v", got)
	}
	for i, f := range usageFixtures() {
		explicit := f.p
		explicit.BaselineDays, explicit.RecentDays = 28, 7
		explicit.WarmupDays, explicit.FlagDays, explicit.SuspectDays = 14, 4, 2
		v0, ev0 := EvaluateUsageShift(f.p, f.in)
		v1, ev1 := EvaluateUsageShift(explicit, f.in)
		if v0 != v1 || !reflect.DeepEqual(ev0, ev1) {
			t.Errorf("fixture %d: unset days judged %s/%s %+v, the former constants %s/%s %+v", i, v0.State, v0.Code, ev0, v1.State, v1.Code, ev1)
		}
	}
}
