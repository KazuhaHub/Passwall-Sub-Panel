// Package riskreview is the write side of the risk center's review state: an
// admin dismissing an account's current signals until they escalate, and
// trusting an account. It never suspends anyone; the only service-state
// write it can make is lifting the location detector's OWN suspension when
// an admin trusts the account and asks for it (ResumeGeoAutoIfHeld).
//
// A package of its own, not a method set on the risk center: every
// dependency of riskcenter is pinned read-only on purpose
// (TestRiskCenterCannotWriteServiceState), and the review row is the one
// thing an admin writes from there. What this package is handed is pinned
// the same way (TestRiskReviewDepsAreNarrow).
//
// Each action is read → check → write under a per-account lock: the account
// must exist, its attention is read exactly as the queue computes it
// (riskcenter.Service.UserAttention), the action is refused with a conflict
// naming what the admin did not see, and otherwise the whole review row and
// the record of the action are written in one transaction (Store.Save). Two
// admins — or one admin's stale tab — acting on one account are serialized
// by that lock, so the second gets a conflict and never a duplicate record.
package riskreview

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/pkg/keyedmutex"
	"github.com/KazuhaHub/passwall-sub-panel/internal/pkg/log"
)

// Deps is everything the review actions touch, each as the narrowest
// interface that serves it. Now is the clock (nil = time.Now).
type Deps struct {
	Store     Store
	Attention AttentionReader
	Users     UserReader
	Resumer   GeoAutoResumer
	Now       func() time.Time
}

// Store is risk_reviews: one account's row, and the upsert of the whole row
// together with the record of the action that produced it (one transaction —
// sqlstore.RiskReviewRepo.Save).
type Store interface {
	Get(ctx context.Context, userID int64) (domain.RiskReview, bool, error)
	Save(ctx context.Context, rev domain.RiskReview, rec domain.FlagRecord) error
}

// AttentionReader is the risk center's one-account read
// (riskcenter.Service.UserAttention): the fresh, trust-masked levels with
// their verdict times, and what the reopen rule decides about the stored
// dismissal — the very computation the queue lists the account by, so a
// dismissal accepts exactly what the admin was shown.
type AttentionReader interface {
	UserAttention(ctx context.Context, userID int64) (domain.AccountAttention, error)
}

// UserReader resolves the account an action names.
type UserReader interface {
	GetByID(ctx context.Context, id int64) (*domain.User, error)
}

// GeoAutoResumer lifts the location detector's own suspension, and nothing
// else (user.Service.ResumeGeoAutoIfHeld): conditional on the hold still
// being geo_auto, so a person's pause is never touched by a trust.
type GeoAutoResumer interface {
	ResumeGeoAutoIfHeld(ctx context.Context, userID int64) (bool, error)
}

// Actor is the admin acting. ID is what the row and the record store; UPN
// is for this process's logs only and is never stored — flag_records is
// name-free, and the read side names the admin by their CURRENT UPN.
type Actor struct {
	ID  int64
	UPN string
}

// TrustResult is a trust and, when asked, the lift of the detector's hold.
type TrustResult struct {
	Review domain.RiskReview
	// Resumed: the hold was lifted.
	Resumed bool
	// ResumeErr, with Resumed: lifted, but the push to the panels failed
	// and could not be queued. Without Resumed: not lifted. Either way the
	// trust stands — a resume failure never undoes it.
	ResumeErr error
}

// The refusals. Each conflict names what changed since the admin looked, so
// the SPA can refresh and say why; none is written anywhere.
var (
	ErrNothingToDismiss = fmt.Errorf("%w: nothing to dismiss", domain.ErrConflict)
	ErrAlreadyDismissed = fmt.Errorf("%w: already dismissed", domain.ErrConflict)
	ErrNotDismissed     = fmt.Errorf("%w: not dismissed", domain.ErrConflict)
	ErrAlreadyTrusted   = fmt.Errorf("%w: already trusted", domain.ErrConflict)
	ErrNotTrusted       = fmt.Errorf("%w: not trusted", domain.ErrConflict)
	ErrChanged          = fmt.Errorf("%w: attention changed", domain.ErrConflict)
	ErrNoteTooLong      = fmt.Errorf("%w: note too long", domain.ErrValidation)
)

// Service runs the four review actions.
type Service struct {
	d Deps
	// locks serializes the actions per account, read → check → Save. Never
	// held across the trust's resume (see Trust), and never together with
	// the user service's own lock.
	locks keyedmutex.Map[int64]
}

// New builds the service over d.
func New(d Deps) *Service { return &Service{d: d} }

func (s *Service) now() time.Time {
	if s.d.Now != nil {
		return s.d.Now()
	}
	return time.Now()
}

// begin takes the account's lock and reads what every action decides on:
// the account (a missing one is domain.ErrNotFound, before anything else is
// read), the stored review row, and the account's attention now.
//
// The row an action rewrites is the store's own, read here under the lock —
// not the attention view's copy, which reads a row that covers nothing as
// no review, and is empty altogether in a risk center wired without its
// review reader. Rewriting from that copy would, for a trust, write a zero
// dismissal over a stored one. The checks that are about what the ADMIN SAW
// (in force, reopened, the levels) read the attention, which is what the
// queue and the drawer show.
func (s *Service) begin(ctx context.Context, userID int64) (func(), domain.RiskReview, domain.AccountAttention, error) {
	unlock := s.locks.Lock(userID)
	fail := func(err error) (func(), domain.RiskReview, domain.AccountAttention, error) {
		unlock()
		return func() {}, domain.RiskReview{}, domain.AccountAttention{}, err
	}
	u, err := s.d.Users.GetByID(ctx, userID)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			return fail(err)
		}
		return fail(fmt.Errorf("risk review: read account %d: %w", userID, err))
	}
	if u == nil {
		return fail(fmt.Errorf("risk review: account %d: %w", userID, domain.ErrNotFound))
	}
	row, found, err := s.d.Store.Get(ctx, userID)
	if err != nil {
		return fail(fmt.Errorf("risk review: read the review of %d: %w", userID, err))
	}
	if !found {
		row = domain.RiskReview{}
	}
	row.UserID = userID
	att, err := s.d.Attention.UserAttention(ctx, userID)
	if err != nil {
		return fail(fmt.Errorf("risk review: attention of %d: %w", userID, err))
	}
	return unlock, row, att, nil
}

// save stamps the row now and writes it with its record, stamped now too.
func (s *Service) save(ctx context.Context, rev *domain.RiskReview, ev domain.FlagEvent, p domain.ReviewFlagParams, now time.Time) error {
	rev.UpdatedAtMS = now.UnixMilli()
	if err := s.d.Store.Save(ctx, *rev, domain.ReviewFlag(rev.UserID, ev, p, now)); err != nil {
		return fmt.Errorf("risk review: save the review of %d: %w", rev.UserID, err)
	}
	return nil
}

// Dismiss accepts the account's current attention until something new or
// worse happens (the reopen rule, domain.EvaluateReview): the snapshot is
// every present source with its level and the time of the verdict it was
// read from, so each source's later records are read from its own verdict,
// never from a shared cutoff.
//
// expected is the levels the admin saw (nil: not sent, no check). A current
// level ranking above what the admin saw — a source they were not shown
// counts as none — is ErrChanged: an admin must never accept an escalation
// they never saw. A level that has since dropped is accepted as it is now.
//
// A dismissal still in force is ErrAlreadyDismissed; a reopened or lapsed
// one is replaced (the admin accepted the escalation). The note is trimmed,
// at most domain.ReviewNoteMaxRunes characters, kept on the row for admins
// and never copied into the record — it is free text and could carry an
// address.
func (s *Service) Dismiss(ctx context.Context, userID int64, by Actor, note string, expected domain.AttentionLevels) (domain.RiskReview, error) {
	note = strings.TrimSpace(note)
	if utf8.RuneCountInString(note) > domain.ReviewNoteMaxRunes {
		return domain.RiskReview{}, ErrNoteTooLong
	}
	if expected != nil {
		if err := domain.ValidateAttentionLevels(expected); err != nil {
			return domain.RiskReview{}, fmt.Errorf("risk review: what the admin saw: %w", err)
		}
	}
	unlock, row, att, err := s.begin(ctx, userID)
	defer unlock()
	if err != nil {
		return domain.RiskReview{}, err
	}
	switch {
	case att.Levels.Empty():
		return domain.RiskReview{}, ErrNothingToDismiss
	case att.DismissedInForce():
		return domain.RiskReview{}, ErrAlreadyDismissed
	case expected != nil && worseThan(att.Levels, expected):
		return domain.RiskReview{}, ErrChanged
	}
	now := s.now()
	row.DismissedAtMS, row.DismissedBy = now.UnixMilli(), by.ID
	row.Accepted, row.Note = att.Snapshot(), note
	if err := s.save(ctx, &row, domain.FlagReviewDismissed, domain.ReviewFlagParams{By: by.ID, Levels: att.Levels}, now); err != nil {
		return domain.RiskReview{}, err
	}
	log.Info("risk review: dismissed", "user_id", userID, "by", by.ID, "by_upn", by.UPN, "sources", att.Levels.Sources())
	return row, nil
}

// worseThan reports a source whose current level ranks above the one the
// admin saw (absent from what they saw = none).
func worseThan(now, saw domain.AttentionLevels) bool {
	for src, l := range now {
		if domain.AttentionRank(l) > domain.AttentionRank(saw[src]) {
			return true
		}
	}
	return false
}

// Undismiss withdraws the stored dismissal whole — time, admin, snapshot and
// note — whether or not it still covers the account. Nothing stored is
// ErrNotDismissed.
func (s *Service) Undismiss(ctx context.Context, userID int64, by Actor) (domain.RiskReview, error) {
	unlock, row, _, err := s.begin(ctx, userID)
	defer unlock()
	if err != nil {
		return domain.RiskReview{}, err
	}
	if !row.Dismissed() {
		return domain.RiskReview{}, ErrNotDismissed
	}
	now := s.now()
	row.DismissedAtMS, row.DismissedBy, row.Accepted, row.Note = 0, 0, nil, ""
	if err := s.save(ctx, &row, domain.FlagReviewUndismissed, domain.ReviewFlagParams{By: by.ID}, now); err != nil {
		return domain.RiskReview{}, err
	}
	log.Info("risk review: undismissed", "user_id", userID, "by", by.ID, "by_upn", by.UPN)
	return row, nil
}

// Trust exempts the account from the location judgements (geo, sub_spread,
// login_country): the read side masks them at once, the detectors judge it
// exempt on their next run, and the user repo refuses a geo_auto suspension
// of it outright. Recorded whatever the account shows now — a quiet account
// can be trusted ahead of a trip. Already trusted is ErrAlreadyTrusted.
//
// With resume, the location detector's own hold is lifted AFTER the review
// lock is released: the lift holds the user lock through a panel push that
// can take minutes, and it is conditional (only while the hold is still
// geo_auto) and idempotent, so it needs no review lock — while holding one
// would make every other action on the account wait for the push. An untrust
// landing between the trust's save and the lift leaves the lift in place:
// the admin asked for it, and the hold is the detector's own. A lift that
// fails never undoes the trust; TrustResult says how it failed.
func (s *Service) Trust(ctx context.Context, userID int64, by Actor, resume bool) (TrustResult, error) {
	unlock, row, _, err := s.begin(ctx, userID)
	defer unlock()
	if err != nil {
		return TrustResult{}, err
	}
	if row.Trusted {
		return TrustResult{}, ErrAlreadyTrusted
	}
	now := s.now()
	row.Trusted, row.TrustedAtMS, row.TrustedBy = true, now.UnixMilli(), by.ID
	if err := s.save(ctx, &row, domain.FlagReviewTrusted, domain.ReviewFlagParams{By: by.ID}, now); err != nil {
		return TrustResult{}, err
	}
	log.Info("risk review: trusted", "user_id", userID, "by", by.ID, "by_upn", by.UPN)
	unlock()

	res := TrustResult{Review: row}
	if !resume {
		return res, nil
	}
	if s.d.Resumer == nil {
		res.ResumeErr = fmt.Errorf("%w: resuming the service is not wired in this deployment", domain.ErrUnavailable)
		return res, nil
	}
	res.Resumed, res.ResumeErr = s.d.Resumer.ResumeGeoAutoIfHeld(ctx, userID)
	if res.ResumeErr != nil {
		log.Warn("risk review: trusted, but lifting the location hold failed",
			"user_id", userID, "by", by.ID, "lifted", res.Resumed, "err", res.ResumeErr)
	}
	return res, nil
}

// Untrust withdraws the trust: the location judgements apply again, and
// since a trusted verdict was an exempt one (a clear), any re-entry after it
// is new to a dismissal. Not trusted is ErrNotTrusted.
func (s *Service) Untrust(ctx context.Context, userID int64, by Actor) (domain.RiskReview, error) {
	unlock, row, _, err := s.begin(ctx, userID)
	defer unlock()
	if err != nil {
		return domain.RiskReview{}, err
	}
	if !row.Trusted {
		return domain.RiskReview{}, ErrNotTrusted
	}
	now := s.now()
	row.Trusted, row.TrustedAtMS, row.TrustedBy = false, 0, 0
	if err := s.save(ctx, &row, domain.FlagReviewUntrusted, domain.ReviewFlagParams{By: by.ID}, now); err != nil {
		return domain.RiskReview{}, err
	}
	log.Info("risk review: untrusted", "user_id", userID, "by", by.ID, "by_upn", by.UPN)
	return row, nil
}
