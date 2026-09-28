package domain

// GeoReasonCode names the EvaluateGeo branch behind a verdict. The English
// Reason stays byte-identical (logs, audit and older SPAs read it); the code,
// with GeoWhy's policy snapshot and the evidence counts, is what a UI localizes.
//
// One code per sentence EvaluateGeo can write, even where the state and the
// evidence would let a reader guess the branch. Two of them cannot be guessed
// at all — lookup switched off and too few addresses placed store the same
// counts (ObserveGeo marks every kept source unplaced when lookup is off) —
// and a UI that derived the rest from combinations of streaks and counts
// would break silently the next time a branch moved. The values are stored
// and sent to the SPA, so they are stable strings.
type GeoReasonCode string

const (
	GeoWhyDisabled         GeoReasonCode = "disabled"
	GeoWhyExempt           GeoReasonCode = "exempt"
	GeoWhyTrusted          GeoReasonCode = "trusted"
	GeoWhyIdleStale        GeoReasonCode = "idle_stale"
	GeoWhyIdleNone         GeoReasonCode = "idle_none"
	GeoWhyUnknownExcluded  GeoReasonCode = "unknown_excluded"
	GeoWhyUnknownGeoOff    GeoReasonCode = "unknown_geo_off"
	GeoWhyUnknownLowRatio  GeoReasonCode = "unknown_low_ratio"
	GeoWhySuspect          GeoReasonCode = "suspect"
	GeoWhyFlaggedSustained GeoReasonCode = "flagged_sustained"
	GeoWhyFlaggedClearing  GeoReasonCode = "flagged_clearing"
	GeoWhyCleanUnplaced    GeoReasonCode = "clean_unplaced"
	GeoWhyCleanWithin      GeoReasonCode = "clean_within"
)

// AllGeoReasonCodes is every code, in the order above.
//
// It is the list the SPA's locale keys are checked against, so it must be
// exactly the codes EvaluateGeo produces: a code missing here is a branch a
// UI would render in English forever, and one listed but never produced is a
// translation nobody reads. TestAllGeoReasonCodesIsExactlyTheBranches holds
// it to the branches.
func AllGeoReasonCodes() []GeoReasonCode {
	return []GeoReasonCode{
		GeoWhyDisabled, GeoWhyExempt, GeoWhyTrusted,
		GeoWhyIdleStale, GeoWhyIdleNone,
		GeoWhyUnknownExcluded, GeoWhyUnknownGeoOff, GeoWhyUnknownLowRatio,
		GeoWhySuspect, GeoWhyFlaggedSustained, GeoWhyFlaggedClearing,
		GeoWhyCleanUnplaced, GeoWhyCleanWithin,
	}
}

// GeoWhy is the machine-readable twin of GeoVerdict.Reason: the branch, the
// tier it names, and the SANITIZED policy the verdict was judged against.
// Persisted because the policy is resolved per group (the traffic poll's
// geoPolicyCache) and a UI recomputing it from global settings would show the
// wrong numbers.
//
// Only the flag side of the policy. The ban tolerances and streak explain
// BanReason, which goes to the log and the audit row and never to the SPA,
// and the co-travel sets are an admin's free text, already folded into the
// places the evidence counts. Nothing here is an address: it is a branch and
// a handful of numbers, which is what lets it ride in the stored evidence.
//
// A comparable struct on purpose, so a test can hold a whole snapshot to ==.
type GeoWhy struct {
	Code           GeoReasonCode `json:"code"`
	Tier           GeoTier       `json:"tier,omitempty"` // suspect/flagged_sustained: the over tier; flagged_clearing: prev.Tier
	Scope          GeoScope      `json:"scope"`
	Tol            GeoTolerances `json:"tol"`
	FlagAfter      int           `json:"flag_after"`
	ClearAfter     int           `json:"clear_after"`
	MinPlacedRatio float64       `json:"min_placed_ratio"`
}
