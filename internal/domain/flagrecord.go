package domain

import (
	"bytes"
	"encoding/json"
	"time"
)

// FlagEvent names one change recorded in flag_records: an account's
// ATTENTION LEVEL moving on one source — what that source's bell entry or
// admin tab would show — its automatic suspension being applied or lifted,
// or an admin reviewing it (dismiss / trust). Stored (flag_records.event is
// varchar(24)), filtered on and localized by the SPA, so the values are
// stable strings.
type FlagEvent string

const (
	// The four attention changes, shared by the concurrent-location verdict
	// and every risk signal. Up to flagged is entering it from anywhere;
	// down from flagged is leaving it, to suspect or to nothing; suspect is
	// entered from nothing and left to nothing (AttentionEvent).
	FlagEnterSuspect FlagEvent = "enter_suspect"
	FlagEnterFlagged FlagEvent = "enter_flagged"
	FlagLeaveSuspect FlagEvent = "leave_suspect"
	FlagLeaveFlagged FlagEvent = "leave_flagged"
	// The automatic suspension's own four: applied by the poll, and left by
	// expiry, by a staff resume, or by another hold replacing it. Each is
	// named by its producer, never derived from levels (GeoAutoFlag).
	FlagAutoSuspended    FlagEvent = "auto_suspended"
	FlagAutoLiftedExpiry FlagEvent = "auto_lifted_expiry"
	FlagAutoLiftedAdmin  FlagEvent = "auto_lifted_admin"
	FlagAutoReplaced     FlagEvent = "auto_replaced"
)

// Review events, named by the admin action (source FlagSourceReview). They
// move no attention level: Level and PrevLevel are always "", and the reopen
// rule never reads them (ReviewFlag).
const (
	FlagReviewDismissed   FlagEvent = "dismissed"
	FlagReviewUndismissed FlagEvent = "undismissed"
	FlagReviewTrusted     FlagEvent = "trusted"
	FlagReviewUntrusted   FlagEvent = "untrusted"
)

// FlagEvents is every event, in the order above — the eight attention
// changes, then the four review actions: the closed set the admin filter is
// checked against. A fresh slice per call.
func FlagEvents() []FlagEvent {
	return []FlagEvent{
		FlagEnterSuspect, FlagEnterFlagged, FlagLeaveSuspect, FlagLeaveFlagged,
		FlagAutoSuspended, FlagAutoLiftedExpiry, FlagAutoLiftedAdmin, FlagAutoReplaced,
		FlagReviewDismissed, FlagReviewUndismissed, FlagReviewTrusted, FlagReviewUntrusted,
	}
}

// FlagLevel is an attention level: the level a record moved to (Level) and
// from (PrevLevel). None is the empty string, so a record that cleared an
// account stores "" — the admin filter names it "cleared".
type FlagLevel string

const (
	FlagLevelNone    FlagLevel = ""
	FlagLevelSuspect FlagLevel = "suspect"
	FlagLevelFlagged FlagLevel = "flagged"
	// FlagLevelSuspended is the geo_auto source's only level: its records
	// move to it or from it, never through suspect or flagged.
	FlagLevelSuspended FlagLevel = "suspended"
)

// A record's source: the concurrent-location verdict, its automatic
// suspension, or — for the risk signals — the signal's RiskKind string, so
// a risk record names the column of the risk tab it came from.
const (
	FlagSourceGeo     = "geo"
	FlagSourceGeoAuto = "geo_auto"
	// FlagSourceReview is an admin's review action (dismiss / trust). Its
	// records never carry an attention level and are excluded from every
	// attention computation: they say what an admin did, not what the
	// account did.
	FlagSourceReview = "review"
)

// FlagSources is every source in display order: the two geo sources, then
// each risk kind (RiskKinds), then review — LAST, because it is the one
// source that is no attention source (AttentionSources is this list without
// it). The closed set the admin filter is checked against; a fresh slice per
// call.
func FlagSources() []string {
	out := []string{FlagSourceGeo, FlagSourceGeoAuto}
	for _, k := range RiskKinds() {
		out = append(out, string(k))
	}
	return append(out, FlagSourceReview)
}

// FlagRecord is one row of flag_records: one account's attention changing on
// one source, with what the admin UI needs to say why at that moment.
//
// Written only when the level changes. A verdict that stays suspect, or
// churns between idle and clean, records nothing: the history would bury the
// changes that matter under ones the bell never showed. The geo and risk
// producers derive the event from the two levels (GeoFlagTransition,
// RiskFlagTransition); the geo_auto producers name theirs (GeoAutoFlag).
//
// ADDRESS-FREE, like the evidence it copies: Code is a branch name, Params
// the numbers and places the verdict was drawn from. A record is kept for
// months (risk.flag_record_retention_days) and outlives every connection it
// describes, so an address in it would be a log of where a subscriber
// connected from. No name either: UPN and DisplayName are read from users
// when listed, and a deleted account's records go with it.
//
// Source review: an admin's dismiss/trust action. Params hold the admin's id
// and, for a dismissal, the accepted levels — never a name and never the
// admin's note (the note is free text and could carry an address); both are
// read from users / risk_reviews when listed.
type FlagRecord struct {
	// ID is the store's; a writer's value is ignored.
	ID, UserID int64
	// Source: FlagSourceGeo, FlagSourceGeoAuto, a RiskKind string, or
	// FlagSourceReview.
	Source string
	Event  FlagEvent
	// Level is the level the account moved to, PrevLevel the one it left.
	Level, PrevLevel FlagLevel
	// State is the verdict's state at the change (a GeoState, shared by
	// the geo verdict and every risk signal); "" for geo_auto, which has
	// none.
	State GeoState
	// Code is the verdict's branch — a GeoReasonCode or a RiskCode — the
	// geo_auto producer's word, or a review record's event. What the SPA
	// localizes the record by.
	Code string
	// Params is what the SPA renders the sentence with, as JSON: a
	// GeoFlagParams for geo, the verdict's stored evidence verbatim for a
	// risk signal, the producer's numbers for geo_auto. nil ⇔ NULL.
	Params json.RawMessage
	// AtMS is when the change was judged (unix ms). 0 on write means now:
	// the store stamps it.
	AtMS int64
	// UPN and DisplayName are filled on the read side only, from the users
	// row the store joins.
	UPN, DisplayName string
}

// GeoFlagParams is a geo record's params: the streak counters the admin UI's
// reason sentences need — the stored verdict keeps them in its streak, not in
// its evidence, and the sentences for suspect, flagged and a latch clearing
// read over, under and the tier (utils/geoAnomaly.ts) — plus the evidence the
// verdict was drawn from. The json names are a contract with the SPA, which
// builds its row from them.
type GeoFlagParams struct {
	Over     int         `json:"over"`
	Under    int         `json:"under"`
	BanOver  int         `json:"ban_over"`
	Flagged  bool        `json:"flagged"`
	Tier     GeoTier     `json:"tier"`
	Evidence GeoEvidence `json:"evidence"`
}

// GeoAttention is the concurrent-location verdict's attention level, read
// the way the geo bell and tab read it: flagged while the streak is LATCHED
// (the bell counts the latch), suspect while the account has been over
// tolerance at least once in a row, none otherwise.
//
// From the streak, never from the state. Idle and unknown samples freeze the
// streak (EvaluateGeo), so a latched account that disconnects is still
// flagged and one mid-ramp is still suspect: suspect → idle → suspect is the
// same attention throughout, not a leave and an enter. Disabled and exempt
// RESET the streak, latch included, so an account the policy stops judging
// does leave — the record's state and code say it was the policy.
func GeoAttention(r GeoRecord) FlagLevel {
	return GeoAttentionOf(r.Streak.Flagged, r.Streak.Over)
}

// GeoAttentionOf is GeoAttention on the two streak fields alone, for the
// evidence-free reads (latched → flagged, over > 0 → suspect, else none):
// the risk center's queue reads two columns of every fresh geo row, not the
// whole record, and must judge exactly as the bell does.
func GeoAttentionOf(flagged bool, over int) FlagLevel {
	switch {
	case flagged:
		return FlagLevelFlagged
	case over > 0:
		return FlagLevelSuspect
	}
	return FlagLevelNone
}

// RiskAttention is a risk signal's attention level: its state, as the risk
// bell (flagged) and tab (suspect) read it. Every other state is none —
// unknown included, so a flag whose evidence went missing (a geo database
// gone, every source excluded) reads as having left, which is what the bell
// shows; the record's code says why.
func RiskAttention(s GeoState) FlagLevel {
	switch s {
	case GeoStateFlagged:
		return FlagLevelFlagged
	case GeoStateSuspect:
		return FlagLevelSuspect
	}
	return FlagLevelNone
}

// AttentionEvent names the change from prev to next, and reports false when
// there is none to record: the same level, or a level these tracks do not
// have (suspended belongs to geo_auto, whose producers name their events).
//
// Up to flagged is entering it, from suspect or from nothing. Down from
// flagged is leaving it, whether to suspect or to nothing: what an admin
// needs to know is that the account is no longer flagged, and Level says
// where it landed. Suspect is entered from nothing and left to nothing.
func AttentionEvent(prev, next FlagLevel) (FlagEvent, bool) {
	if prev == next || !attentionLevel(prev) || !attentionLevel(next) {
		return "", false
	}
	switch {
	case next == FlagLevelFlagged:
		return FlagEnterFlagged, true
	case prev == FlagLevelFlagged:
		return FlagLeaveFlagged, true
	case next == FlagLevelSuspect:
		return FlagEnterSuspect, true
	default:
		return FlagLeaveSuspect, true
	}
}

// attentionLevel reports whether l is one of the three levels the geo and
// risk tracks move between.
func attentionLevel(l FlagLevel) bool {
	return l == FlagLevelNone || l == FlagLevelSuspect || l == FlagLevelFlagged
}

// GeoFlagTransition is the record of one poll judgement, if it changed the
// account's geo attention: prev is the stored record the poll judged from
// (hadPrev false when there was none, which is level none — so a first-ever
// clean or idle verdict records nothing), next the record it stores.
//
// Code is the verdict's GeoReasonCode ("" for a record written without a
// Why); State is next's. Params carry the streak — over, under, ban-over, the
// latch and the tier — and next's evidence, which is address-free by
// construction (GeoEvidence).
func GeoFlagTransition(prev GeoRecord, hadPrev bool, next GeoRecord, atMS int64) (FlagRecord, bool) {
	from := FlagLevelNone
	if hadPrev {
		from = GeoAttention(prev)
	}
	to := GeoAttention(next)
	ev, ok := AttentionEvent(from, to)
	if !ok {
		return FlagRecord{}, false
	}
	code := ""
	if next.Evidence.Why != nil {
		code = string(next.Evidence.Why.Code)
	}
	// Every field is a plain value the verdict already produced, so this
	// cannot fail short of a NaN placed ratio, which the policy's sanitizer
	// rules out; a record with no params is still the record of the change.
	params, err := json.Marshal(GeoFlagParams{
		Over: next.Streak.Over, Under: next.Streak.Under, BanOver: next.Streak.BanOver,
		Flagged: next.Streak.Flagged, Tier: next.Streak.Tier, Evidence: next.Evidence,
	})
	if err != nil {
		params = nil
	}
	return FlagRecord{
		UserID: next.UserID, Source: FlagSourceGeo, Event: ev, Level: to, PrevLevel: from,
		State: next.State, Code: code, Params: params, AtMS: atMS,
	}, true
}

// RiskFlagTransition is the record of one risk verdict, if it changed the
// signal's attention: prev is the state stored before this run (hadPrev
// false when the account had no row for this kind), next the signal being
// saved. The source is the kind, the code the verdict's.
//
// Params are next's evidence verbatim — what the risk tab renders the same
// verdict from, already address-free (each evaluator's test holds it to
// that) — copied, so a caller reusing its buffer cannot rewrite the record.
// A verdict with nothing to show (idle, disabled, exempt) has no evidence,
// and its record has no params.
func RiskFlagTransition(prev GeoState, hadPrev bool, next RiskSignal, atMS int64) (FlagRecord, bool) {
	from := FlagLevelNone
	if hadPrev {
		from = RiskAttention(prev)
	}
	to := RiskAttention(next.State)
	ev, ok := AttentionEvent(from, to)
	if !ok {
		return FlagRecord{}, false
	}
	var params json.RawMessage
	if len(next.Evidence) > 0 {
		params = bytes.Clone(next.Evidence)
	}
	return FlagRecord{
		UserID: next.UserID, Source: string(next.Kind), Event: ev, Level: to, PrevLevel: from,
		State: next.State, Code: string(next.Code), Params: params, AtMS: atMS,
	}, true
}

// GeoAutoFlag is the record of the automatic suspension being applied or
// lifted. Its levels are fixed by the event: FlagAutoSuspended moves from
// none to suspended, and each of the three ways out moves from suspended
// back to none. It has no state (the suspension is not a verdict).
//
// Code and params are the producer's: the tier and the numbers of the ban,
// the reason it ended. They must be address-free, and in particular must
// never carry the ban's English reason, which names places beside the
// account in a sentence built for the audit log. Nil or empty params store
// nothing; a zero time leaves the stamp to the store.
func GeoAutoFlag(userID int64, ev FlagEvent, code string, params map[string]any, at time.Time) FlagRecord {
	level, prev := FlagLevelNone, FlagLevelSuspended
	if ev == FlagAutoSuspended {
		level, prev = FlagLevelSuspended, FlagLevelNone
	}
	var raw json.RawMessage
	if len(params) > 0 {
		// The producers pass numbers, strings and nil; a value JSON cannot
		// hold would lose the params, never the record.
		if b, err := json.Marshal(params); err == nil {
			raw = b
		}
	}
	var atMS int64
	if !at.IsZero() {
		atMS = at.UnixMilli()
	}
	return FlagRecord{
		UserID: userID, Source: FlagSourceGeoAuto, Event: ev, Level: level, PrevLevel: prev,
		Code: code, Params: raw, AtMS: atMS,
	}
}
