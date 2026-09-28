package riskcenter

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

// ---- The attention computation ----
//
// ONE computation answers "which accounts need a look, and why" for every
// surface that asks: the queue (待处理 and its cards), the Users page's
// levels, the bell's count, and — for one account — the drawer and the
// review actions (UserAttention). Each surface that computed its own answer
// would drift from the others, and an admin who clicks "2 accounts" in the
// bell must land on the same two.
//
// Attention is the level of a FRESH verdict, exactly as the bell has always
// counted it: a geo row the poll re-judged within GeoBellFreshness, a risk
// row the worker rewrote within AlertFreshness. A latched flag nobody
// re-judged for a day is history; the drawer still shows it, marked stale.
// On top of the verdicts:
//
//   - the detector's own suspension (geo_auto) is a source of its own, read
//     from the users row, with the time it was written;
//   - a trusted account's location sources are masked at read time
//     (domain.MaskTrusted) — the detectors re-judge it exempt only on their
//     next run;
//   - a dismissal is judged by the reopen rule (domain.EvaluateReview) over
//     the account's flag records since each source's verdict time, read only
//     for the accounts that rule can change.
//
// The fleet reads are evidence-free (the stores answer the narrow rows of
// ports/riskcenter.go); the evidence is loaded for one page at most.

// readWindow is what one computation reads its sources over: the clock,
// the runtime it resolved, and the bounds derived from them.
type readWindow struct {
	now  time.Time
	rt   domain.RiskRuntime
	poll time.Duration
	set  ports.UISettings
	// geoSince and riskSince bound freshness (inclusive), exactly the
	// bell's windows.
	geoSince, riskSince time.Time
	// historyFromMS is now minus the flag-record retention — the cutoff
	// the hourly prune deletes before, so a dismissal older than it may
	// have lost its history (it lapses).
	historyFromMS int64
}

func (s *Service) window(ctx context.Context) readWindow {
	rt, poll, set := s.runtime(ctx)
	now := s.now()
	return readWindow{
		now: now, rt: rt, poll: poll, set: set,
		geoSince:      now.Add(-rt.GeoBellFreshness(poll)),
		riskSince:     now.Add(-rt.AlertFreshness),
		historyFromMS: now.Add(-time.Duration(rt.FlagRecordRetentionDays) * 24 * time.Hour).UnixMilli(),
	}
}

// knownKind reports a kind this build computes. A row of any other kind is
// one a newer build wrote before a downgrade: nothing here rewrites it, so
// it would read as a live verdict for ever.
func knownKind(k domain.RiskKind) bool { return slices.Contains(domain.RiskKinds(), k) }

// needsSteps is the reopen rule's read condition: a dismissal that is not
// lapsed, over attention it could stop covering. Anything else the rule
// decides without records — never dismissed (nothing to reopen), no
// attention (nothing to list either way), lapsed (every accepted level
// already reads as none). Shared by the fleet read and the one-account read
// so both ask for the same accounts.
func needsSteps(rev domain.RiskReview, now domain.AttentionLevels, historyFromMS int64) bool {
	return rev.Dismissed() && !now.Empty() && rev.CutoffMS() >= historyFromMS
}

// assemble is the last step both reads share: mask the location sources
// of a trusted account, keep the verdict times of the sources left, and
// apply the reopen rule. A review row not in force (all cleared) is no
// review.
func assemble(levels domain.AttentionLevels, at map[string]int64, heldSinceMS int64,
	rev domain.RiskReview, hasReview bool, steps []domain.FlagStep, historyFromMS int64) domain.AccountAttention {
	if !hasReview {
		rev = domain.RiskReview{}
	}
	levels = domain.MaskTrusted(levels, rev.Trusted)
	var kept map[string]int64
	for src := range levels {
		if kept == nil {
			kept = make(map[string]int64, len(levels))
		}
		kept[src] = at[src]
	}
	if !levels.Held() {
		heldSinceMS = 0
	}
	a := domain.AccountAttention{Levels: levels, AtMS: kept, HeldSinceMS: heldSinceMS, Review: rev, HasReview: hasReview}
	a.State = domain.EvaluateReview(rev, domain.ReviewInputs{
		Now: levels, Trusted: rev.Trusted, HeldSinceMS: heldSinceMS, Steps: steps, HistoryFromMS: historyFromMS,
	})
	return a
}

// inForce reports a review row that covers anything: a dismissal or a
// trust. The actions never delete a row, so a cleared one is all zero.
func inForce(r domain.RiskReview) bool { return r.Dismissed() || r.Trusted }

// accounts computes the attention of the whole fleet: every account at
// attention now and, with includeReviewed, every account with a review row
// in force (a trusted account with nothing to show is still listed under
// 已信任). Any store error is returned: the queue fails loudly, and the bell
// logs and skips — an empty answer must mean "nothing", never "could not
// read".
func (s *Service) accounts(ctx context.Context, w readWindow, includeReviewed bool) (map[int64]*domain.AccountAttention, error) {
	levels := map[int64]domain.AttentionLevels{}
	at := map[int64]map[string]int64{}
	held := map[int64]int64{}
	put := func(uid int64, src string, l domain.FlagLevel, ms int64) {
		if levels[uid] == nil {
			levels[uid] = domain.AttentionLevels{}
			at[uid] = map[string]int64{}
		}
		levels[uid][src], at[uid][src] = l, ms
	}

	if s.d.Geo != nil {
		rows, err := s.d.Geo.AttentionLevels(ctx, w.geoSince)
		if err != nil {
			return nil, fmt.Errorf("risk center: geo attention: %w", err)
		}
		for _, r := range rows {
			if l := domain.GeoAttentionOf(r.Flagged, r.Over); l != domain.FlagLevelNone {
				put(r.UserID, domain.FlagSourceGeo, l, r.UpdatedAtMS)
			}
		}
	}
	if s.d.Signals != nil {
		rows, err := s.d.Signals.AttentionLevels(ctx, w.riskSince)
		if err != nil {
			return nil, fmt.Errorf("risk center: risk signal attention: %w", err)
		}
		for _, r := range rows {
			if !knownKind(r.Kind) {
				continue
			}
			if l := domain.RiskAttention(r.State); l != domain.FlagLevelNone {
				put(r.UserID, string(r.Kind), l, r.UpdatedAtMS)
			}
		}
	}
	if s.d.Holds != nil {
		holds, err := s.d.Holds.ListServiceHolds(ctx, domain.DisabledGeoAutoSuspend)
		if err != nil {
			return nil, fmt.Errorf("risk center: geo_auto holds: %w", err)
		}
		for _, h := range holds {
			put(h.UserID, domain.FlagSourceGeoAuto, domain.FlagLevelSuspended, h.SinceMS)
			held[h.UserID] = h.SinceMS
		}
	}
	reviews := map[int64]domain.RiskReview{}
	if s.d.Reviews != nil {
		list, err := s.d.Reviews.List(ctx)
		if err != nil {
			return nil, fmt.Errorf("risk center: risk reviews: %w", err)
		}
		for _, r := range list {
			if inForce(r) {
				reviews[r.UserID] = r
			}
		}
	}

	// The reopen rule's records, for the accounts it can change only, in
	// one read — after the trust mask, as the rule sees the levels.
	need := map[int64]int64{}
	for uid, rev := range reviews {
		if needsSteps(rev, domain.MaskTrusted(levels[uid], rev.Trusted), w.historyFromMS) {
			need[uid] = rev.CutoffMS()
		}
	}
	var steps map[int64][]domain.FlagStep
	if len(need) > 0 {
		var err error
		if steps, err = s.d.Flags.StepsSince(ctx, need); err != nil {
			return nil, fmt.Errorf("risk center: records since the dismissals: %w", err)
		}
	}

	out := make(map[int64]*domain.AccountAttention, len(levels))
	add := func(uid int64) {
		rev, has := reviews[uid]
		a := assemble(levels[uid], at[uid], held[uid], rev, has, steps[uid], w.historyFromMS)
		// A trusted account whose only attention was a location source
		// has none left; it is listed only among the reviewed.
		if a.Levels.Empty() && !(includeReviewed && has) {
			return
		}
		out[uid] = &a
	}
	for uid := range levels {
		add(uid)
	}
	if includeReviewed {
		for uid := range reviews {
			if _, done := out[uid]; !done {
				add(uid)
			}
		}
	}
	return out, nil
}

// UserAttention is the attention of ONE account, computed as the queue
// computes the fleet — the same freshness bounds (applied in Go with the
// store's own inclusive comparison), the same hold, the same trust mask and
// the same reopen rule over the same records — so the drawer and the review
// actions see exactly the account the queue listed
// (TestUserAttention_EqualsTheQueueComputation, and on the real stores
// TestRiskCenterAttentionAgreesAcrossReads). AtMS carries each source's
// verdict time, which a dismissal snapshots. A missing account is
// domain.ErrNotFound.
func (s *Service) UserAttention(ctx context.Context, userID int64) (domain.AccountAttention, error) {
	w := s.window(ctx)
	u, err := s.account(ctx, userID)
	if err != nil {
		return domain.AccountAttention{}, err
	}
	geoRows, sigRows, err := s.verdictsOf(ctx, userID)
	if err != nil {
		return domain.AccountAttention{}, err
	}
	return s.attentionOf(ctx, w, u, geoRows, sigRows)
}

// account reads one account; a missing one (or a nil answer) is
// domain.ErrNotFound, passed through for the handler's 404.
func (s *Service) account(ctx context.Context, userID int64) (*domain.User, error) {
	u, err := s.d.Users.GetByID(ctx, userID)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			return nil, err
		}
		return nil, fmt.Errorf("risk center: read account %d: %w", userID, err)
	}
	if u == nil {
		return nil, fmt.Errorf("risk center: account %d: %w", userID, domain.ErrNotFound)
	}
	return u, nil
}

// verdictsOf reads one account's full verdict rows, any state and any age:
// the drawer shows them all, and attentionOf picks the fresh ones.
func (s *Service) verdictsOf(ctx context.Context, userID int64) ([]domain.GeoRecord, []domain.RiskSignal, error) {
	var geoRows []domain.GeoRecord
	var sigRows []domain.RiskSignal
	if s.d.Geo != nil {
		rows, err := s.d.Geo.ListByUsers(ctx, []int64{userID})
		if err != nil {
			return nil, nil, fmt.Errorf("risk center: geo verdict of %d: %w", userID, err)
		}
		geoRows = rows
	}
	if s.d.Signals != nil {
		rows, err := s.d.Signals.ListByUsers(ctx, []int64{userID})
		if err != nil {
			return nil, nil, fmt.Errorf("risk center: risk signals of %d: %w", userID, err)
		}
		sigRows = rows
	}
	return geoRows, sigRows, nil
}

// attentionOf is accounts for one account whose rows are already read.
func (s *Service) attentionOf(ctx context.Context, w readWindow, u *domain.User,
	geoRows []domain.GeoRecord, sigRows []domain.RiskSignal) (domain.AccountAttention, error) {
	levels := domain.AttentionLevels{}
	at := map[string]int64{}
	for _, r := range geoRows {
		// The store's own comparison: updated_at >= since, in unix ms.
		if r.UserID != u.ID || r.UpdatedAtMS < w.geoSince.UnixMilli() {
			continue
		}
		if l := domain.GeoAttention(r); l != domain.FlagLevelNone {
			levels[domain.FlagSourceGeo], at[domain.FlagSourceGeo] = l, r.UpdatedAtMS
		}
	}
	for _, r := range sigRows {
		if r.UserID != u.ID || !knownKind(r.Kind) || r.UpdatedAtMS < w.riskSince.UnixMilli() {
			continue
		}
		if l := domain.RiskAttention(r.State); l != domain.FlagLevelNone {
			levels[string(r.Kind)], at[string(r.Kind)] = l, r.UpdatedAtMS
		}
	}
	var heldSinceMS int64
	if u.ServiceDisabledReason == domain.DisabledGeoAutoSuspend {
		if u.ServiceDisabledAt != nil {
			heldSinceMS = u.ServiceDisabledAt.UnixMilli()
		}
		levels[domain.FlagSourceGeoAuto], at[domain.FlagSourceGeoAuto] = domain.FlagLevelSuspended, heldSinceMS
	}

	var rev domain.RiskReview
	var has bool
	if s.d.Reviews != nil {
		r, found, err := s.d.Reviews.Get(ctx, u.ID)
		if err != nil {
			return domain.AccountAttention{}, fmt.Errorf("risk center: risk review of %d: %w", u.ID, err)
		}
		rev, has = r, found && inForce(r)
	}
	var steps []domain.FlagStep
	if has && needsSteps(rev, domain.MaskTrusted(levels, rev.Trusted), w.historyFromMS) {
		got, err := s.d.Flags.StepsSince(ctx, map[int64]int64{u.ID: rev.CutoffMS()})
		if err != nil {
			return domain.AccountAttention{}, fmt.Errorf("risk center: records since the dismissal of %d: %w", u.ID, err)
		}
		steps = got[u.ID]
	}
	return assemble(levels, at, heldSinceMS, rev, has, steps, w.historyFromMS), nil
}

// Levels is the Users page's risk column: every account at attention now,
// and every trusted one (level "" when it has nothing to show), each with
// its level, its hold, and whether it is open, dismissed in force or
// trusted — keyed by account id.
func (s *Service) Levels(ctx context.Context) (map[int64]UserLevel, error) {
	all, err := s.accounts(ctx, s.window(ctx), true)
	if err != nil {
		return nil, err
	}
	out := make(map[int64]UserLevel, len(all))
	for uid, a := range all {
		if a.Levels.Empty() && !a.Review.Trusted {
			continue
		}
		out[uid] = UserLevel{
			Level: a.Levels.Max(), AutoSuspended: a.Levels.Held(),
			Open: a.Open(), Dismissed: a.DismissedInForce(), Trusted: a.Review.Trusted,
		}
	}
	return out, nil
}

// UserLevel is one account on the Users page's risk column.
type UserLevel struct {
	Level                                   domain.FlagLevel
	AutoSuspended, Open, Dismissed, Trusted bool
}

// CountUrgent is the bell's number: the accounts open AND flagged or held
// by the detector — exactly the queue's 需立即处理. Suspect alone never
// rings.
func (s *Service) CountUrgent(ctx context.Context) (int64, error) {
	all, err := s.accounts(ctx, s.window(ctx), false)
	if err != nil {
		return 0, err
	}
	var n int64
	for _, a := range all {
		if a.Urgent() {
			n++
		}
	}
	return n, nil
}
