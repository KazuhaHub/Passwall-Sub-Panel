package domain

import "encoding/json"

// RiskKind names one observe-only risk signal.
//
// Each kind is judged on its own and stored as its own (user, kind) row with
// one of the seven GeoState values and a code. There is no score and no
// cross-signal escalation: four independent "suspect"s are four rows for an
// admin to read, not a verdict about the account. Nothing in PSP acts on
// these rows — they are shown, never enforced.
//
// The values are stored (risk_signals.kind is varchar(24) and half the
// primary key), sent to the SPA and named by the risk.*_off settings, so
// they are stable strings.
type RiskKind string

const (
	// RiskKindSubSpread — subscription fetches from provinces that no single
	// client links together.
	RiskKindSubSpread RiskKind = "sub_spread"
	// RiskKindDevices — more distinct declared devices than the policy
	// allows.
	RiskKindDevices RiskKind = "devices"
	// RiskKindUsageShift — a sustained rise in daily traffic over the
	// account's own history.
	RiskKindUsageShift RiskKind = "usage_shift"
	// RiskKindLoginCountry — a panel login from a country the account's
	// subscription fetches have not established.
	RiskKindLoginCountry RiskKind = "login_country"
)

// RiskKinds is every kind in display order — the admin table's column order,
// and the order the API returns each account's signals in.
func RiskKinds() []RiskKind {
	return []RiskKind{RiskKindSubSpread, RiskKindDevices, RiskKindUsageShift, RiskKindLoginCountry}
}

// RiskCode names the branch behind one signal's verdict, the way
// GeoReasonCode does for the concurrent-location verdict: the UI localizes
// the code with the numbers in the evidence, and nothing stores prose. A code
// is at most 32 bytes (risk_signals.code is varchar(32)).
type RiskCode string

const (
	RiskCodeSignalOff      RiskCode = "signal_off"
	RiskCodeScopeOff       RiskCode = "scope_off"
	RiskCodeScopeCountry   RiskCode = "scope_country"
	RiskCodeAllowAnywhere  RiskCode = "allow_anywhere"
	RiskCodeTrusted        RiskCode = "trusted"
	RiskCodeNoFetches      RiskCode = "no_fetches"
	RiskCodeRetentionShort RiskCode = "retention_short"
	RiskCodeGeoUnavailable RiskCode = "geo_unavailable"
	RiskCodeAllExcluded    RiskCode = "all_excluded"
	RiskCodeLowPlaced      RiskCode = "low_placed"
	RiskCodeNoRegions      RiskCode = "no_regions"
	RiskCodeSpread         RiskCode = "spread"
	RiskCodeSpreadBuilding RiskCode = "spread_building"
	RiskCodeWithin         RiskCode = "within"
	RiskCodeCaptureOff     RiskCode = "capture_off"
	RiskCodeNoHWID         RiskCode = "no_hwid"
	RiskCodeOver           RiskCode = "over"
	RiskCodeOverBuilding   RiskCode = "over_building"
	RiskCodeNoUsage        RiskCode = "no_usage"
	RiskCodeWarmup         RiskCode = "warmup"
	RiskCodeSustained      RiskCode = "sustained"
	RiskCodeBuilding       RiskCode = "building"
	RiskCodeNoRecentLogins RiskCode = "no_recent_logins"
	RiskCodeUnplaced       RiskCode = "unplaced"
	RiskCodeLearning       RiskCode = "learning"
	RiskCodeNewCountry     RiskCode = "new_country"
	RiskCodeKnownCountries RiskCode = "known_countries"
)

// AllRiskCodes maps each kind to every code its evaluator can return.
//
// It is the list the SPA's locale keys are checked against, so it must be
// exactly what each evaluator produces: a code missing here is a verdict a UI
// cannot explain, and one listed but never produced is a translation nobody
// reads. Each evaluator's code-exactness test holds its kind's list to its
// branches. A fresh map per call, so no caller can edit the table.
func AllRiskCodes() map[RiskKind][]RiskCode {
	return map[RiskKind][]RiskCode{
		RiskKindSubSpread: {
			RiskCodeSignalOff, RiskCodeScopeOff, RiskCodeScopeCountry, RiskCodeAllowAnywhere, RiskCodeTrusted,
			RiskCodeNoFetches, RiskCodeRetentionShort, RiskCodeAllExcluded, RiskCodeGeoUnavailable,
			RiskCodeLowPlaced, RiskCodeNoRegions,
			RiskCodeSpread, RiskCodeSpreadBuilding, RiskCodeWithin,
		},
		RiskKindDevices: {
			RiskCodeSignalOff, RiskCodeCaptureOff, RiskCodeNoFetches, RiskCodeRetentionShort,
			RiskCodeNoHWID, RiskCodeOver, RiskCodeOverBuilding, RiskCodeWithin,
		},
		RiskKindUsageShift: {
			RiskCodeSignalOff, RiskCodeRetentionShort, RiskCodeNoUsage, RiskCodeWarmup,
			RiskCodeSustained, RiskCodeBuilding, RiskCodeWithin,
		},
		RiskKindLoginCountry: {
			RiskCodeSignalOff, RiskCodeScopeOff, RiskCodeAllowAnywhere, RiskCodeTrusted, RiskCodeNoRecentLogins,
			RiskCodeGeoUnavailable, RiskCodeUnplaced, RiskCodeLearning,
			RiskCodeNewCountry, RiskCodeKnownCountries,
		},
	}
}

// RiskVerdict is one signal's judgement: v2's seven states, never a score.
//
// The states mean what they mean for the concurrent-location verdict, so the
// admin UI colours both with one table — and "unknown" (the evidence is
// missing) is never drawn as clean. Only flagged reaches the notification
// bell; no state reaches the account holder or changes their service.
type RiskVerdict struct {
	State GeoState
	Code  RiskCode
}

// RiskEvidenceVersion is written into every signal's evidence, so a reader
// can tell a shape it knows from one a later build wrote. The rows are
// overwritten hourly, so an older shape lasts at most one refresh after an
// upgrade.
const RiskEvidenceVersion = 1

// RiskSignal is one stored (user, kind) row: the latest verdict for that
// signal, overwritten on every refresh. No history — an admin reads what the
// account looks like now, and last week's rows would be last week's window.
//
// Evidence NEVER contains an address or a coordinate: provinces and
// countries with day masks, client labels with a short digest prefix, byte
// totals, login countries. It is nil (NULL in the store) for a verdict with
// nothing to show — idle, disabled, exempt — so nothing outlives the window
// it described.
type RiskSignal struct {
	UserID int64
	Kind   RiskKind
	State  GeoState
	Code   RiskCode
	// Evidence is the evaluator's evidence as JSON; nil ⇔ NULL. The store
	// refuses anything that is not valid JSON, because the admin API serves
	// it verbatim as a raw JSON value.
	Evidence json.RawMessage
	// UpdatedAtMS is when the store last wrote the row (unix ms). The store
	// stamps it; a writer's value is ignored, so a row can never claim to be
	// fresher than its last write.
	UpdatedAtMS int64
	// UPN and DisplayName are filled on the read side only, from the users
	// row the store joins; a writer leaves them empty.
	UPN, DisplayName string
}
