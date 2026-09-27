package domain

import (
	"math"
	"slices"
)

// The usage_shift series and its thresholds, in panel-local calendar days.
//
// The series is the whole days before today: baseline days the account is
// measured against, then the recent days that are judged. Today is left out
// because it is not over yet — a half day would always read low. Shipped,
// that is 35 days: 28 baseline days (k = 0..27), then 7 recent days
// (k = 28..34).
//
// Each number below is the shipped value of a setting. The two lengths are
// fleet-wide (risk.usage_baseline_days, risk.usage_recent_days, in
// RiskRuntime): the worker reads ONE fleet series per run, and every
// account's fleet factor is taken from it, so every account's series has
// the same days. The three thresholds are per group (risk.usage_warmup_days,
// risk.usage_flag_days, risk.usage_suspect_days, in RiskPolicy) and are
// bounded by the lengths (RiskPolicy.Bounded).
const (
	RiskUsageBaselineDays = 28
	RiskUsageRecentDays   = 7
	// RiskUsageWarmupDays: an account is judged only once it has 14
	// baseline days from its first day of use on. A median over a first week
	// is a median over setup — profiles imported, a first big download.
	RiskUsageWarmupDays = 14
	// RiskUsageFlagDays: over on 4 of the 7 recent days is the week's habit.
	RiskUsageFlagDays = 4
	// RiskUsageSuspectDays: over on 2 or 3 is worth a look. One day alone is
	// a download, and stays clean.
	RiskUsageSuspectDays = 2

	// RiskUsageWarmupMinDays is the shortest warm-up a group may set, for
	// the reason RiskUsageWarmupDays gives: under a week, the median is
	// taken over setup. A shorter one only ever accuses more, so it is
	// raised, the way RiskUsageRatioMin raises the ratio.
	RiskUsageWarmupMinDays = 7
	// RiskUsageOverDaysMin is the fewest over-days a group may flag or call
	// suspect at: one day alone is a download, never a change of habit.
	RiskUsageOverDaysMin = 2
)

// UsageShiftPolicy is the part of RiskPolicy, and of the fleet's
// RiskRuntime, usage_shift judges with. The worker hands over
// RiskPolicyFromSettings' values bounded by the runtime, so Ratio is at
// least RiskUsageRatioMin, FloorBytes at least RiskGiB, and the days are
// inside their bounds with WarmupDays <= BaselineDays, FlagDays <=
// RecentDays and SuspectDays <= FlagDays.
type UsageShiftPolicy struct {
	Off        bool
	Ratio      float64
	FloorBytes int64
	// BaselineDays and RecentDays are the series' two lengths, the fleet's
	// risk.usage_baseline_days and risk.usage_recent_days; the input must
	// be exactly their sum long. WarmupDays, FlagDays and SuspectDays are
	// the group's thresholds. 0 (or negative) is "never configured" and
	// judges with the shipped RiskUsage* value — never with a baseline of no
	// days or a flag at no over-days, which would read a series of nothing
	// or flag every account that used anything.
	BaselineDays, RecentDays, WarmupDays, FlagDays, SuspectDays int
}

// usageShiftDays is a policy's day knobs with "never configured" resolved
// to the shipped values: what the evaluator judges with, and what its
// evidence says it judged with.
type usageShiftDays struct {
	baseline, recent, warmup, flag, suspect int
}

func (p UsageShiftPolicy) days() usageShiftDays {
	orShipped := func(v, shipped int) int {
		if v <= 0 {
			return shipped
		}
		return v
	}
	return usageShiftDays{
		baseline: orShipped(p.BaselineDays, RiskUsageBaselineDays),
		recent:   orShipped(p.RecentDays, RiskUsageRecentDays),
		warmup:   orShipped(p.WarmupDays, RiskUsageWarmupDays),
		flag:     orShipped(p.FlagDays, RiskUsageFlagDays),
		suspect:  orShipped(p.SuspectDays, RiskUsageSuspectDays),
	}
}

// UsageShiftInput is one account's daily traffic beside the whole fleet's.
type UsageShiftInput struct {
	// EndDate is the panel-local date of the series' last day (yesterday),
	// so the UI can label the columns without knowing the panel's zone.
	EndDate string
	// User and Fleet are daily byte totals, oldest first, each exactly the
	// policy's BaselineDays + RecentDays long. Fleet includes the account
	// itself; the evaluator takes it back out.
	User, Fleet []int64
	// HistoryRetentionDays is traffic_history_days, how long the hourly
	// rollup is kept; 0 = never pruned.
	HistoryRetentionDays int
}

// UsageShiftEvidence is what the admin UI draws for usage_shift: the whole
// series, the median and every number the recent days were held to. Bytes,
// dates, ratios and day counts only — nothing about where or how the
// traffic was used.
//
// The field names are a wire contract: the SPA reads them from the API as
// they were stored. A verdict that stopped before judging (retention_short,
// warmup) carries the series, the history, the date and the days, with the
// lists empty rather than null.
type UsageShiftEvidence struct {
	V                    int     `json:"v"`
	EndDate              string  `json:"end_date"`
	HistoryRetentionDays int     `json:"history_retention_days,omitempty"`
	Series               []int64 `json:"series"` // BaselineDays + RecentDays, oldest first
	// HistoryDays is how many baseline days there are from the account's
	// first day of use on (that day included); the median is taken over
	// exactly these.
	HistoryDays int     `json:"history_days"`
	Median      int64   `json:"median"`
	Ratio       float64 `json:"ratio"`
	Floor       int64   `json:"floor"`
	// Thresholds, Over and FleetFactors have one entry per recent day,
	// oldest first. The factors are rounded to two decimals for display;
	// the thresholds were computed with the exact ones.
	Thresholds   []int64   `json:"thresholds"`
	Over         []bool    `json:"over"`
	OverDays     int       `json:"over_days"`
	FleetFactors []float64 `json:"fleet_factors"`
	// The days this verdict was judged with — the fleet's two lengths and
	// the group's three thresholds, "never configured" already resolved to
	// the shipped value — so the admin's sentence ("2 of the last 3 days",
	// "flagged at 2", "judged from 7") reads the numbers that applied, not
	// the shipped ones. Always present on a row written from now on; a row
	// stored before these existed lacks them, and was judged with the
	// shipped values (28, 7, 14, 4, 2), which is what a reader must assume.
	BaselineDays int `json:"baseline_days"`
	RecentDays   int `json:"recent_days"`
	WarmupDays   int `json:"warmup_days"`
	FlagDays     int `json:"flag_days"`
	SuspectDays  int `json:"suspect_days"`
}

// EvaluateUsageShift judges whether an account's daily traffic has risen,
// and stayed risen, against its own history.
//
// It is a change detector and nothing more: a new household member, a new
// device, a new habit and a shared credential all look the same here, which
// is why the UI says "usage changed" and never "shared". With n = baseline +
// recent days (35 shipped), the branches, first match wins:
//
//  1. Off → disabled / signal_off, no evidence.
//  2. the account's or the fleet's series is not exactly n days →
//     unknown / retention_short. It does not describe the days the policy
//     judges: read anyway it would index past its end, or take a recent day
//     for a baseline one. A series cut short is what a short retention
//     leaves, and it is unknown, never clean. The worker builds both at n,
//     so this is a guard, not a path.
//  3. 0 < HistoryRetentionDays ≤ n → unknown / retention_short. The rollup
//     is pruned before the series starts, so its oldest days read as zeros:
//     a deflated baseline that would accuse. Unknown, never clean. n is
//     short too: the prune cuts at now minus the retention, an instant,
//     while the series starts at a local midnight n days back, so once
//     today has begun the first day is already partly deleted. It takes
//     n + 1 to keep the whole series.
//  4. every recent day 0 → idle / no_usage, no evidence.
//  5. fewer history days than the warm-up (14 shipped) → unknown / warmup.
//  6. over on at least the flag days (4 shipped) → flagged / sustained.
//  7. over on at least the suspect days (2 shipped) → suspect / building.
//  8. otherwise → clean / within.
//
// The baseline is the account's own median over its history days only —
// from its first day of use (the first baseline day above zero) to the end
// of the baseline. Zeros after that day are real (the account existed and
// used nothing); zeros before it are days it did not exist, and counting
// them would halve a new account's median.
//
// A recent day is over when it exceeds
//
//	max(floor(Ratio × median × fleetFactor), FloorBytes)
//
// The floor keeps a light account's first real week (median near zero) from
// reading as a change. The fleet factor blunts rises the whole fleet shares —
// a holiday, a new season of something — and it is leave-one-out: the rest
// of the fleet's day over the rest of the fleet's baseline median, never
// below 1. On a small panel one heavy account is most of the fleet, and
// counted in, its own rise would raise its own factor and excuse itself.
func EvaluateUsageShift(p UsageShiftPolicy, in UsageShiftInput) (RiskVerdict, *UsageShiftEvidence) {
	if p.Off {
		return RiskVerdict{State: GeoStateDisabled, Code: RiskCodeSignalOff}, nil
	}
	d := p.days()
	n := d.baseline + d.recent
	ev := &UsageShiftEvidence{
		V:                    RiskEvidenceVersion,
		EndDate:              in.EndDate,
		HistoryRetentionDays: in.HistoryRetentionDays,
		// Never null: the UI draws the series whatever the verdict.
		Series:       append([]int64{}, in.User...),
		Thresholds:   []int64{},
		Over:         []bool{},
		FleetFactors: []float64{},
		BaselineDays: d.baseline,
		RecentDays:   d.recent,
		WarmupDays:   d.warmup,
		FlagDays:     d.flag,
		SuspectDays:  d.suspect,
	}
	if len(in.User) != n || len(in.Fleet) != n {
		return RiskVerdict{State: GeoStateUnknown, Code: RiskCodeRetentionShort}, ev
	}
	first := d.baseline
	for k := range d.baseline {
		if in.User[k] > 0 {
			first = k
			break
		}
	}
	ev.HistoryDays = d.baseline - first
	if in.HistoryRetentionDays > 0 && in.HistoryRetentionDays <= n {
		return RiskVerdict{State: GeoStateUnknown, Code: RiskCodeRetentionShort}, ev
	}
	recent := in.User[d.baseline:]
	if !slices.ContainsFunc(recent, func(b int64) bool { return b != 0 }) {
		return RiskVerdict{State: GeoStateIdle, Code: RiskCodeNoUsage}, nil
	}
	if ev.HistoryDays < d.warmup {
		return RiskVerdict{State: GeoStateUnknown, Code: RiskCodeWarmup}, ev
	}

	other := make([]int64, n)
	for k := range other {
		other[k] = max(0, in.Fleet[k]-in.User[k])
	}
	fleetMedian := medianBytes(other[:d.baseline])
	ev.Median = medianBytes(in.User[first:d.baseline])
	ev.Ratio = p.Ratio
	ev.Floor = p.FloorBytes
	ev.Thresholds = make([]int64, 0, d.recent)
	ev.Over = make([]bool, 0, d.recent)
	ev.FleetFactors = make([]float64, 0, d.recent)
	for k := d.baseline; k < n; k++ {
		ff := 1.0
		if fleetMedian > 0 {
			ff = max(1, float64(other[k])/float64(fleetMedian))
		}
		threshold := max(saturatingBytes(p.Ratio*float64(ev.Median)*ff), p.FloorBytes)
		over := in.User[k] > threshold
		if over {
			ev.OverDays++
		}
		ev.Thresholds = append(ev.Thresholds, threshold)
		ev.Over = append(ev.Over, over)
		ev.FleetFactors = append(ev.FleetFactors, math.Round(ff*100)/100)
	}
	switch {
	case ev.OverDays >= d.flag:
		return RiskVerdict{State: GeoStateFlagged, Code: RiskCodeSustained}, ev
	case ev.OverDays >= d.suspect:
		return RiskVerdict{State: GeoStateSuspect, Code: RiskCodeBuilding}, ev
	}
	return RiskVerdict{State: GeoStateClean, Code: RiskCodeWithin}, ev
}

// medianBytes is the median of a set of daily totals: the middle value, or
// the mean of the two middle ones. 0 for an empty set.
func medianBytes(days []int64) int64 {
	if len(days) == 0 {
		return 0
	}
	s := slices.Clone(days)
	slices.Sort(s)
	mid := len(s) / 2
	if len(s)%2 == 1 {
		return s[mid]
	}
	// lo + half the gap rather than (lo+hi)/2: the sum of two large daily
	// totals can overflow; the gap between two non-negative ones cannot.
	lo, hi := s[mid-1], s[mid]
	return lo + (hi-lo)/2
}

// saturatingBytes floors a threshold computed in float64 to whole bytes,
// saturating at the int64 maximum. The fleet factor is a ratio of medians
// and can be arbitrarily large, and Go leaves converting an unrepresentable
// float implementation-defined (amd64 yields the int64 MINIMUM, which the
// floor would then replace: the highest threshold turned into the lowest).
// NaN saturates too. Anything unrepresentably high is simply "never over".
func saturatingBytes(f float64) int64 {
	if !(f < math.MaxInt64) {
		return math.MaxInt64
	}
	return int64(math.Floor(f))
}
