package domain

import (
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"time"
)

// An admin's review of one account, and the rule that decides what the risk
// center's queue still asks about it.
//
// Two actions, each current state rather than history (risk_reviews; the
// history is flag_records with source review):
//
//   - Dismiss accepts what the account shows NOW — the level of every
//     attention source and the time of the verdict each was read from — and
//     hides it from the queue until something new or worse happens. Not
//     forever: a dismissal covers the situation the admin saw, not the
//     account (EvaluateReview says exactly when it stops).
//   - Trust is a standing exemption from the location judgements: the
//     location sources (geo, sub_spread, login_country) are not judged for
//     the account at all, which is what an admin means by "this one travels
//     or shares a household with good reason". It never touches devices or
//     usage_shift, which are not about where the account is.
//
// Everything here is pure: the read side gathers the levels, the hold, the
// review row and the flag records, and these functions decide.

// locationSources are the sources trust exempts, in AttentionSources order.
var locationSources = [...]string{FlagSourceGeo, string(RiskKindSubSpread), string(RiskKindLoginCountry)}

// AttentionSources is FlagSources without FlagSourceReview, in display
// order: every source an account's attention can come from. A fresh slice
// per call.
func AttentionSources() []string {
	return slices.DeleteFunc(FlagSources(), func(s string) bool { return s == FlagSourceReview })
}

// LocationSources are the sources trust exempts (geo, sub_spread,
// login_country): the three that judge WHERE an account is. A fresh slice
// per call.
func LocationSources() []string { return slices.Clone(locationSources[:]) }

func isLocationSource(s string) bool { return slices.Contains(locationSources[:], s) }

// AttentionLevels maps an attention source to its current level. A source at
// no attention is ABSENT (never stored as ""). nil and empty are equivalent.
//
// The readers below treat a stray "" entry as absent too, so a caller that
// built the map loosely still reads the account right; only
// ValidateAttentionLevels, the gate on what is stored and posted, refuses it.
type AttentionLevels map[string]FlagLevel

// AttentionRank orders levels WITHIN one source: "" 0, suspect 1, flagged 2,
// suspended 2. Suspended is geo_auto's only level, so it ranks as that
// source's top; ranks of different sources are never compared with each
// other. Anything else ranks as none.
func AttentionRank(l FlagLevel) int {
	switch l {
	case FlagLevelSuspect:
		return 1
	case FlagLevelFlagged, FlagLevelSuspended:
		return 2
	}
	return 0
}

// Max is the account's level over every source except geo_auto: flagged >
// suspect > "". The hold is a state of the service, not a verdict, and is
// read by Held; folding it in would make every held account "flagged" and
// the level chip would say the same thing as the hold chip beside it.
func (a AttentionLevels) Max() FlagLevel {
	out := FlagLevelNone
	for s, l := range a {
		if s == FlagSourceGeoAuto {
			continue
		}
		switch l {
		case FlagLevelFlagged:
			return FlagLevelFlagged
		case FlagLevelSuspect:
			out = FlagLevelSuspect
		}
	}
	return out
}

// Held reports a geo_auto hold (a["geo_auto"] == suspended).
func (a AttentionLevels) Held() bool { return a[FlagSourceGeoAuto] == FlagLevelSuspended }

// Empty reports that no source is at attention.
func (a AttentionLevels) Empty() bool {
	for _, l := range a {
		if l != FlagLevelNone {
			return false
		}
	}
	return true
}

// Clone returns a copy without stray none entries (nil for empty).
func (a AttentionLevels) Clone() AttentionLevels {
	if a.Empty() {
		return nil
	}
	out := make(AttentionLevels, len(a))
	for s, l := range a {
		if l != FlagLevelNone {
			out[s] = l
		}
	}
	return out
}

// Sources returns the present sources ordered by (AttentionRank desc,
// AttentionSources order), geo_auto after every flagged/suspect source: the
// row's signal chips read worst first, and the hold — which has its own chip
// and ranks 2 only within its own source — never pushes a verdict aside.
// Keys outside AttentionSources are not listed. Never nil, so a DTO built
// from it serializes as [] rather than null.
func (a AttentionLevels) Sources() []string {
	out := make([]string, 0, len(a))
	for _, s := range AttentionSources() {
		if s != FlagSourceGeoAuto && a[s] != FlagLevelNone {
			out = append(out, s)
		}
	}
	// Stable, so equal ranks keep AttentionSources order.
	slices.SortStableFunc(out, func(x, y string) int { return AttentionRank(a[y]) - AttentionRank(a[x]) })
	if a[FlagSourceGeoAuto] != FlagLevelNone {
		out = append(out, FlagSourceGeoAuto)
	}
	return out
}

// UrgencyClass: 4 flagged∧held, 3 flagged, 2 held, 1 suspect, 0 none (the
// queue's first sort key). Held ranks above suspect because the bell rings
// for a hold: a suspended subscriber is the one waiting on an admin, and a
// hold-only account sorted below the suspect rows would be the last thing
// the admin the bell sent here would find.
func (a AttentionLevels) UrgencyClass() int {
	m, held := a.Max(), a.Held()
	switch {
	case m == FlagLevelFlagged && held:
		return 4
	case m == FlagLevelFlagged:
		return 3
	case held:
		return 2
	case m == FlagLevelSuspect:
		return 1
	}
	return 0
}

// ValidateAttentionLevels: every key ∈ AttentionSources; geo_auto's value is
// suspended; every other value is suspect or flagged (a source at no
// attention is absent, never ""). ErrValidation otherwise. The error names
// the key, never a value: the levels arrive in request bodies, and a 400
// that echoes a posted value is an echo of whatever was posted.
func ValidateAttentionLevels(a AttentionLevels) error {
	// Sorted, so the same bad map always names the same key.
	for _, s := range slices.Sorted(maps.Keys(a)) {
		if err := validateAttentionEntry(s, a[s]); err != nil {
			return err
		}
	}
	return nil
}

func validateAttentionEntry(s string, l FlagLevel) error {
	switch {
	case !slices.Contains(AttentionSources(), s):
		return fmt.Errorf("%w: unknown attention source %q", ErrValidation, s)
	case s == FlagSourceGeoAuto:
		if l != FlagLevelSuspended {
			return fmt.Errorf("%w: the level of %s must be suspended", ErrValidation, s)
		}
	case l != FlagLevelSuspect && l != FlagLevelFlagged:
		return fmt.Errorf("%w: the level of %s must be suspect or flagged", ErrValidation, s)
	}
	return nil
}

// MaskTrusted drops the location sources when trusted (returns a new map;
// input untouched; nil for empty). The read side masks at read time rather
// than waiting for the verdicts to come back exempt: the poll re-judges
// within one interval and the risk worker within an hour, and a just-trusted
// account lingering in the queue for that long would read as "trust did
// nothing".
func MaskTrusted(a AttentionLevels, trusted bool) AttentionLevels {
	out := a.Clone()
	if trusted {
		for _, s := range locationSources {
			delete(out, s)
		}
	}
	if out.Empty() {
		return nil
	}
	return out
}

// AcceptedLevel is one source of a dismissal snapshot: the level the admin
// accepted and the time of the verdict it was read from (geo/risk row
// updated_at, geo_auto service_disabled_at; 0 = unknown, read as the
// dismissal time). JSON names are the risk_reviews column contract.
type AcceptedLevel struct {
	Level FlagLevel `json:"level"`
	AtMS  int64     `json:"at_ms"`
}

// DismissSnapshot is what a dismissal accepted, per attention source.
//
// The verdict time is stored beside the level because "after the dismissal"
// is the wrong cutoff for a source's records: a geo record is stamped with
// its poll's START but written after the streak save, so a clear the admin
// never saw can carry a stamp just before the dismissal. Each source's
// records count from its own verdict — what the admin was actually shown.
type DismissSnapshot map[string]AcceptedLevel

// Levels returns the snapshot's levels alone (nil for empty), verbatim — so
// validating them validates the snapshot.
func (d DismissSnapshot) Levels() AttentionLevels {
	if len(d) == 0 {
		return nil
	}
	out := make(AttentionLevels, len(d))
	for s, a := range d {
		out[s] = a.Level
	}
	return out
}

// ValidateDismissSnapshot: ValidateAttentionLevels(d.Levels()) and every
// AtMS >= 0 (a negative time is no instant, and would read as "before
// everything" to the cutoffs).
func ValidateDismissSnapshot(d DismissSnapshot) error {
	if err := ValidateAttentionLevels(d.Levels()); err != nil {
		return err
	}
	for _, s := range slices.Sorted(maps.Keys(d)) {
		if d[s].AtMS < 0 {
			return fmt.Errorf("%w: the verdict time of %s must not be negative", ErrValidation, s)
		}
	}
	return nil
}

// ReviewNoteMaxRunes bounds an admin's review note. risk_reviews.note is
// varchar(200), which is characters on PostgreSQL and MySQL, so the bound
// is runes, not bytes: 200 CJK characters fit.
const ReviewNoteMaxRunes = 200

// RiskReview is one account's review row (risk_reviews): its dismissal and
// its trust, independent of each other. A row whose dismissal and trust are
// both cleared is all zero and covers nothing.
type RiskReview struct {
	UserID        int64
	DismissedAtMS int64           // 0 = not dismissed
	DismissedBy   int64           // admin user id
	Accepted      DismissSnapshot // what the dismissal accepted; nil when not dismissed
	Note          string          // ≤ ReviewNoteMaxRunes; admin-only, never copied into a flag record
	Trusted       bool
	TrustedAtMS   int64
	TrustedBy     int64
	UpdatedAtMS   int64
}

// Dismissed reports that a dismissal is stored (it may still have been
// reopened or have lapsed: EvaluateReview).
func (r RiskReview) Dismissed() bool { return r.DismissedAtMS > 0 }

// SourceCutoffMS is the instant after which source s's records are news to
// the dismissal: Accepted[s].AtMS when s is in the snapshot with AtMS > 0,
// else DismissedAtMS. A source the admin never saw counts from the
// dismissal — one that entered and left before it must not count.
func (r RiskReview) SourceCutoffMS(s string) int64 {
	if a, ok := r.Accepted[s]; ok && a.AtMS > 0 {
		return a.AtMS
	}
	return r.DismissedAtMS
}

// CutoffMS is how far back the store must read for this dismissal: the
// minimum of SourceCutoffMS over every snapshot source and DismissedAtMS (0
// when not dismissed). It is also what decides a lapse.
func (r RiskReview) CutoffMS() int64 {
	if !r.Dismissed() {
		return 0
	}
	c := r.DismissedAtMS
	for s := range r.Accepted {
		c = min(c, r.SourceCutoffMS(s))
	}
	return c
}

// FlagStep is one non-review flag record reduced to what the reopen rule
// reads.
type FlagStep struct {
	Source string
	Level  FlagLevel
	State  GeoState
	AtMS   int64
}

// IsClear: Level == "" and (Source == geo_auto or State != unknown). A leave
// to "cannot tell" is not a clear: the evidence went missing, the situation
// did not end. The risk kinds map unknown to no attention (RiskAttention),
// so a GeoIP database swap, a settings hiccup or region display turned off
// writes a leave for every row — counting those as clears would reopen
// every dismissal in the fleet. Disabled and exempt are policy decisions
// (trust included) and do clear. A geo_auto lift has no state and always
// clears.
func (s FlagStep) IsClear() bool {
	return s.Level == FlagLevelNone && (s.Source == FlagSourceGeoAuto || s.State != GeoStateUnknown)
}

// ReviewInputs is what the read side knows about one account now.
type ReviewInputs struct {
	Now           AttentionLevels // fresh levels, already trust-masked
	Trusted       bool            // location sources are ignored entirely
	HeldSinceMS   int64           // service_disabled_at of a current geo_auto hold; 0 otherwise
	Steps         []FlagStep      // non-review records with AtMS > r.CutoffMS(), ordered by (AtMS, id)
	HistoryFromMS int64           // now − flag-record retention; 0 = unknown (never lapses)
}

// ReviewState is what the reopen rule decides about one account now.
type ReviewState struct {
	Dismissed bool     // a dismissal is stored
	Lapsed    bool     // its history may be pruned: every accepted level reads as none
	Reopened  bool     // the dismissal no longer covers the account
	Escalated []string // sources that made it reopen, AttentionSources order
}

// EvaluateReview is the reopen rule: whether a stored dismissal still covers
// the account. Pure, total, no clock — the read side passes the retention
// cutoff in.
//
// Per attention source, in AttentionSources order (so Escalated is
// deterministic), starting from the level the dismissal accepted:
//
//   - Records count STRICTLY after the source's own cutoff (SourceCutoffMS).
//     A shared cutoff — the earliest of all — would read a flagged→suspect
//     history that preceded the verdict the admin saw as an escalation.
//     Records the snapshot already reflects are at or before that time: risk
//     records are stamped inside the same transaction as their row, so one
//     slightly after the row's updated_at carries exactly the snapshot level
//     and escalates nothing.
//   - A clear (FlagStep.IsClear) resets the accepted level to none: the
//     situation the admin accepted ended, so any re-entry — even at the same
//     level — is a new one.
//   - A record above the accepted level ESCALATES, and the escalation
//     sticks: a situation that went worse overnight and receded by morning
//     still reopens, and only a new dismissal accepts it. Comparing the
//     current level alone would silently re-hide it. Flapping inside the
//     accepted level never reopens.
//   - The current level above the accepted one escalates too, for records
//     that were lost or are not written yet (they are best-effort).
//   - geo_auto: a hold whose service_disabled_at is newer than the one the
//     snapshot accepted is a re-suspension, read exactly from the users row
//     whatever the records say.
//
// Trusted accounts skip the location sources entirely: trust exempts them,
// and their records up to the trust are about a judgement no longer made.
//
// A dismissal LAPSES when its oldest cutoff is older than the flag-record
// retention: its history may have been pruned, so its records can no longer
// prove nothing happened. Every accepted level then reads as none and any
// current attention reopens it — the alternative, trusting the snapshot
// alone after a prune, would make a dismissal permanent, the opposite of
// "until something new happens".
//
// Reopened needs no current attention: an account whose every source went
// quiet after an escalation reads as reopened, and AccountAttention.Open
// still requires attention now before listing it.
//
// Review records never reach this function (the store excludes them).
func EvaluateReview(r RiskReview, in ReviewInputs) ReviewState {
	if !r.Dismissed() {
		return ReviewState{}
	}
	st := ReviewState{Dismissed: true}
	st.Lapsed = in.HistoryFromMS > 0 && r.CutoffMS() < in.HistoryFromMS
	for _, s := range AttentionSources() {
		if in.Trusted && isLocationSource(s) {
			continue
		}
		accepted := r.Accepted[s].Level // "" when s was not in the snapshot
		escalated := false
		if st.Lapsed {
			accepted = FlagLevelNone
		} else {
			cutoff := r.SourceCutoffMS(s)
			for _, step := range in.Steps {
				if step.Source != s || step.AtMS <= cutoff {
					continue
				}
				if step.IsClear() {
					accepted = FlagLevelNone
					continue
				}
				if AttentionRank(step.Level) > AttentionRank(accepted) {
					escalated = true
				}
			}
		}
		if AttentionRank(in.Now[s]) > AttentionRank(accepted) {
			escalated = true
		}
		if _, had := r.Accepted[FlagSourceGeoAuto]; s == FlagSourceGeoAuto && had && in.Now.Held() &&
			in.HeldSinceMS > r.SourceCutoffMS(FlagSourceGeoAuto) {
			escalated = true
		}
		if escalated {
			st.Escalated = append(st.Escalated, s)
		}
	}
	st.Reopened = len(st.Escalated) > 0
	return st
}

// AccountAttention is everything the queue knows about one account now.
type AccountAttention struct {
	Levels      AttentionLevels  // fresh, trust-masked
	AtMS        map[string]int64 // verdict time of each present source
	HeldSinceMS int64            // service_disabled_at of a geo_auto hold, 0 otherwise
	Review      RiskReview       // zero when HasReview is false
	HasReview   bool
	State       ReviewState
}

// Open is "needs a look": attention now, and no dismissal covering it
// (never dismissed, reopened, or lapsed with attention now).
func (a AccountAttention) Open() bool {
	return !a.Levels.Empty() && (!a.Review.Dismissed() || a.State.Reopened)
}

// Urgent is what the bell counts: Open() && (Levels.Max() == flagged ||
// Levels.Held()). Suspect is a "look when you can", never a ring.
func (a AccountAttention) Urgent() bool {
	return a.Open() && (a.Levels.Max() == FlagLevelFlagged || a.Levels.Held())
}

// DismissedInForce is a dismissal that currently covers something:
// Review.Dismissed() && !State.Reopened && !Levels.Empty(). A dismissal with
// nothing left to cover is not listed as dismissed — it is simply quiet.
func (a AccountAttention) DismissedInForce() bool {
	return a.Review.Dismissed() && !a.State.Reopened && !a.Levels.Empty()
}

// Snapshot builds the DismissSnapshot of the current levels with their
// verdict times (0 where unknown — read as the dismissal time); nil when
// there is no attention to accept.
func (a AccountAttention) Snapshot() DismissSnapshot {
	if a.Levels.Empty() {
		return nil
	}
	out := make(DismissSnapshot, len(a.Levels))
	for s, l := range a.Levels {
		if l != FlagLevelNone {
			out[s] = AcceptedLevel{Level: l, AtMS: max(a.AtMS[s], 0)}
		}
	}
	return out
}

// ReviewFlagParams is a review record's params (json names are a contract
// with the SPA): the acting admin's id and, for a dismissal, the levels it
// accepted. The admin's name is resolved when the record is listed, so a
// renamed admin reads under the current name and a deleted one as an id.
type ReviewFlagParams struct {
	By     int64           `json:"by"`
	Levels AttentionLevels `json:"levels,omitempty"` // dismissed only
}

// ReviewFlag builds the record of one review action: Source review, Event
// ev, Level/PrevLevel/State "" (an admin's action moves no attention level),
// Code string(ev), Params marshalled, AtMS at.UnixMilli() (zero time → 0,
// stamped by the store).
//
// It carries no note: the note is free text and could carry an address,
// and flag_records is address-free and kept for months. The note lives only
// in risk_reviews and the audit row.
func ReviewFlag(userID int64, ev FlagEvent, p ReviewFlagParams, at time.Time) FlagRecord {
	p.Levels = p.Levels.Clone()
	var raw json.RawMessage
	// An int64 and a string map cannot fail to marshal; if one ever did,
	// the record of the action is kept without its params, as GeoAutoFlag
	// keeps its own.
	if b, err := json.Marshal(p); err == nil {
		raw = b
	}
	var atMS int64
	if !at.IsZero() {
		atMS = at.UnixMilli()
	}
	return FlagRecord{
		UserID: userID, Source: FlagSourceReview, Event: ev, Code: string(ev), Params: raw, AtMS: atMS,
	}
}
