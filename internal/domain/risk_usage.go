package domain

import (
	"math"
	"slices"
)

// The usage_shift series and its thresholds, in panel-local calendar days.
//
// The series is the 35 whole days before today: 28 baseline days (k = 0..27)
// the account is measured against, then the 7 recent days (k = 28..34) that
// are judged. Today is left out because it is not over yet — a half day
// would always read low.
const (
	RiskUsageSeriesDays   = 35
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
)

// UsageShiftPolicy is the part of RiskPolicy usage_shift judges with. The
// worker hands over RiskPolicyFromSettings' values, so Ratio is at least
// RiskUsageRatioMin and FloorBytes at least RiskGiB.
type UsageShiftPolicy struct {
	Off        bool
	Ratio      float64
	FloorBytes int64
}

// UsageShiftInput is one account's daily traffic beside the whole fleet's.
type UsageShiftInput struct {
	// EndDate is the panel-local date of the series' last day (yesterday),
	// so the UI can label the columns without knowing the panel's zone.
	EndDate string
	// User and Fleet are daily byte totals, oldest first. Fleet includes the
	// account itself; the evaluator takes it back out.
	User, Fleet [RiskUsageSeriesDays]int64
	// HistoryRetentionDays is traffic_history_days, how long the hourly
	// rollup is kept; 0 = never pruned.
	HistoryRetentionDays int
}

// UsageShiftEvidence is what the admin UI draws for usage_shift: the whole
// series, the median and every number the recent week was held to. Bytes,
// dates and ratios only — nothing about where or how the traffic was used.
//
// The field names are a wire contract: the SPA reads them from the API as
// they were stored. A verdict that stopped before judging (retention_short,
// warmup) carries the series, the history and the date, with the lists empty
// rather than null.
type UsageShiftEvidence struct {
	V                    int     `json:"v"`
	EndDate              string  `json:"end_date"`
	HistoryRetentionDays int     `json:"history_retention_days,omitempty"`
	Series               []int64 `json:"series"` // 35, oldest first
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
}

// EvaluateUsageShift judges whether an account's daily traffic has risen,
// and stayed risen, against its own history.
//
// It is a change detector and nothing more: a new household member, a new
// device, a new habit and a shared credential all look the same here, which
// is why the UI says "usage changed" and never "shared". The branches, first
// match wins:
//
//  1. Off → disabled / signal_off, no evidence.
//  2. 0 < HistoryRetentionDays ≤ 35 → unknown / retention_short. The rollup
//     is pruned before the series starts, so its oldest days read as zeros:
//     a deflated baseline that would accuse. Unknown, never clean. 35 is
//     short too: the prune cuts at now minus the retention, an instant,
//     while the series starts at a local midnight 35 days back, so once
//     today has begun the first day is already partly deleted. It takes 36
//     to keep the whole series.
//  3. every recent day 0 → idle / no_usage, no evidence.
//  4. fewer than 14 history days → unknown / warmup.
//  5. over on 4+ recent days → flagged / sustained.
//  6. over on 2–3 → suspect / building.
//  7. otherwise → clean / within.
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
	first := RiskUsageBaselineDays
	for k := range RiskUsageBaselineDays {
		if in.User[k] > 0 {
			first = k
			break
		}
	}
	ev := &UsageShiftEvidence{
		V:                    RiskEvidenceVersion,
		EndDate:              in.EndDate,
		HistoryRetentionDays: in.HistoryRetentionDays,
		Series:               slices.Clone(in.User[:]),
		HistoryDays:          RiskUsageBaselineDays - first,
		Thresholds:           []int64{},
		Over:                 []bool{},
		FleetFactors:         []float64{},
	}
	if in.HistoryRetentionDays > 0 && in.HistoryRetentionDays <= RiskUsageSeriesDays {
		return RiskVerdict{State: GeoStateUnknown, Code: RiskCodeRetentionShort}, ev
	}
	recent := in.User[RiskUsageBaselineDays:]
	if !slices.ContainsFunc(recent, func(b int64) bool { return b != 0 }) {
		return RiskVerdict{State: GeoStateIdle, Code: RiskCodeNoUsage}, nil
	}
	if ev.HistoryDays < RiskUsageWarmupDays {
		return RiskVerdict{State: GeoStateUnknown, Code: RiskCodeWarmup}, ev
	}

	var other [RiskUsageSeriesDays]int64
	for k := range other {
		other[k] = max(0, in.Fleet[k]-in.User[k])
	}
	fleetMedian := medianBytes(other[:RiskUsageBaselineDays])
	ev.Median = medianBytes(in.User[first:RiskUsageBaselineDays])
	ev.Ratio = p.Ratio
	ev.Floor = p.FloorBytes
	ev.Thresholds = make([]int64, 0, RiskUsageRecentDays)
	ev.Over = make([]bool, 0, RiskUsageRecentDays)
	ev.FleetFactors = make([]float64, 0, RiskUsageRecentDays)
	for k := RiskUsageBaselineDays; k < RiskUsageSeriesDays; k++ {
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
	case ev.OverDays >= RiskUsageFlagDays:
		return RiskVerdict{State: GeoStateFlagged, Code: RiskCodeSustained}, ev
	case ev.OverDays >= RiskUsageSuspectDays:
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
