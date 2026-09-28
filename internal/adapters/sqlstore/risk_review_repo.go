package sqlstore

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"unicode/utf8"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/pkg/log"
)

// riskReviewRow is one account an admin reviewed in the risk center: its
// dismissal and its trust, independent of each other.
//
// Current state, not history: the history is flag_records with source
// review, which Save appends in the same transaction as the upsert. A row
// whose dismissal and trust are both cleared stays, all zero, and covers
// nothing (List skips it); the actions never delete one.
//
// No foreign key, for the reason risk_signals has none: a user delete must
// never depend on an observation table. Reads JOIN users, so a deleted
// account's row is invisible at once, and the hourly cleanup purges it
// (PurgeOrphans).
//
// Holds no address. dismissed_levels is source → {level, at_ms}, the
// domain.DismissSnapshot contract; the note is admin-written and admin-only
// and is never copied into a flag record.
//
// The key is the account's id, given by the writer, never generated:
// autoIncrement:false because GORM otherwise marks a lone integer primary key
// auto-increment (a PostgreSQL bigserial behind a key nothing should ever
// draw from). Every default equals Go's zero value, so GORM substituting the
// default for a zero field on INSERT writes exactly what was asked — the
// trap bool_default_test.go records is a default:true bool, which this row
// has none of.
type riskReviewRow struct {
	UserID        int64 `gorm:"primaryKey;autoIncrement:false"`
	DismissedAtMS int64 `gorm:"column:dismissed_at_ms;not null;default:0"`
	DismissedBy   int64 `gorm:"column:dismissed_by;not null;default:0"`
	// DismissedLevels is the JSON DismissSnapshot. text, NULLABLE and NO
	// DEFAULT (MySQL refuses a DEFAULT on TEXT; TestSchemaNoDefaultOnTextColumns);
	// NULL is "no dismissal", its one stored form.
	DismissedLevels *string `gorm:"column:dismissed_levels;type:text"`
	// Note is bounded in characters (domain.ReviewNoteMaxRunes): varchar(200)
	// is characters on PostgreSQL and MySQL, and Save checks runes, so what
	// passes fits on every dialect.
	Note        string `gorm:"column:note;size:200;not null;default:''"`
	Trusted     bool   `gorm:"column:trusted;not null;default:false"`
	TrustedAtMS int64  `gorm:"column:trusted_at_ms;not null;default:0"`
	TrustedBy   int64  `gorm:"column:trusted_by;not null;default:0"`
	UpdatedAtMS int64  `gorm:"column:updated_at_ms;not null;default:0"`
}

func (riskReviewRow) TableName() string { return "risk_reviews" }

// riskReviewColumns is every column but the key: the upsert's DoUpdates.
// Named in full because an upsert rewrites only the columns it names, and
// one left out would keep its FIRST value for ever — an undismiss that left
// the snapshot behind, an untrust that stayed trusted.
var riskReviewColumns = []string{
	"dismissed_at_ms", "dismissed_by", "dismissed_levels", "note",
	"trusted", "trusted_at_ms", "trusted_by", "updated_at_ms",
}

// RiskReviewRepo is the risk_reviews store. A concrete type built from the
// database handle, like RiskSignalRepo: the review actions that write it, the
// risk center that reads it, the detectors that read only the trusted ids and
// the cleanup that purges it each declare the narrow interface they need.
type RiskReviewRepo struct{ db *gorm.DB }

func NewRiskReviewRepo(db *gorm.DB) *RiskReviewRepo { return &RiskReviewRepo{db: db} }

// Get returns the account's row; (zero, false, nil) when it has none — the
// common case, not an error. No JOIN: the caller already holds the account.
func (r *RiskReviewRepo) Get(ctx context.Context, userID int64) (domain.RiskReview, bool, error) {
	var rows []riskReviewRow
	// Find with a limit rather than First: a missing row is expected here,
	// and First would log it as a record-not-found error.
	if err := r.db.WithContext(ctx).Where("user_id = ?", userID).Limit(1).Find(&rows).Error; err != nil {
		return domain.RiskReview{}, false, fmt.Errorf("get risk review: %w", err)
	}
	if len(rows) == 0 {
		return domain.RiskReview{}, false, nil
	}
	out := riskReviewsFromRows(rows)
	return out[0], true, nil
}

// List returns every row in force — a dismissal stored or a trust — of an
// account that still exists, by user id: the queue's read. A cleared row
// covers nothing, and JOIN (not LEFT JOIN) hides a deleted account's row
// from the moment of the delete rather than from the next purge.
func (r *RiskReviewRepo) List(ctx context.Context) ([]domain.RiskReview, error) {
	var rows []riskReviewRow
	if err := r.db.WithContext(ctx).Model(&riskReviewRow{}).
		Joins("JOIN users ON users.id = risk_reviews.user_id").
		Where("risk_reviews.dismissed_at_ms > ? OR risk_reviews.trusted = ?", 0, true).
		Order("risk_reviews.user_id").
		Find(&rows).Error; err != nil {
		return nil, fmt.Errorf("list risk reviews: %w", err)
	}
	return riskReviewsFromRows(rows), nil
}

// ListTrusted returns the ids of the trusted accounts that still exist,
// ascending: what the traffic poll and the risk worker read once per judging
// step. One narrow column, so it costs nothing to read that often.
func (r *RiskReviewRepo) ListTrusted(ctx context.Context) ([]int64, error) {
	var ids []int64
	if err := r.db.WithContext(ctx).Table("risk_reviews").
		Joins("JOIN users ON users.id = risk_reviews.user_id").
		Where("risk_reviews.trusted = ?", true).
		Order("risk_reviews.user_id").
		Pluck("risk_reviews.user_id", &ids).Error; err != nil {
		return nil, fmt.Errorf("list trusted accounts: %w", err)
	}
	return ids, nil
}

// Save upserts the WHOLE row and appends rec, the record of the action that
// produced it, in ONE transaction.
//
// Checked first, before any write (a domain.ErrValidation, nothing written):
// an account id; a note of at most domain.ReviewNoteMaxRunes characters; a
// snapshot domain.ValidateDismissSnapshot accepts, present exactly when a
// dismissal is (a dismissal that accepted nothing would be reopened by
// anything, and a snapshot without a dismissal is a contradiction the reopen
// rule would silently ignore); and rec being this account's review record.
// The record's own columns are then checked by insertFlagRecords, inside the
// transaction, and its refusal rolls the upsert back.
//
// One transaction because the row and its record are one fact: a state
// saved without its record would read, later, as a state nobody chose, and
// a record without its state as an action that did nothing. Every statement
// runs on tx: on SQLite the transaction holds the pool's one connection
// (conn.go), and a statement on r.db would wait for it until its context
// gave up.
//
// The upsert names every column (riskReviewColumns), so zeroes overwrite: an
// undismiss writes 0s and a NULL snapshot, an untrust writes false. The
// conflict column is named explicitly rather than left to the dialect.
func (r *RiskReviewRepo) Save(ctx context.Context, rev domain.RiskReview, rec domain.FlagRecord) error {
	if err := validateRiskReview(rev, rec); err != nil {
		return err
	}
	levels, err := dismissSnapshotColumn(rev.Accepted)
	if err != nil {
		return err
	}
	row := riskReviewRow{
		UserID: rev.UserID, DismissedAtMS: rev.DismissedAtMS, DismissedBy: rev.DismissedBy,
		DismissedLevels: levels, Note: rev.Note,
		Trusted: rev.Trusted, TrustedAtMS: rev.TrustedAtMS, TrustedBy: rev.TrustedBy,
		UpdatedAtMS: rev.UpdatedAtMS,
	}
	upsert := clause.OnConflict{
		Columns:   []clause.Column{{Name: "user_id"}},
		DoUpdates: clause.AssignmentColumns(riskReviewColumns),
	}
	if err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Clauses(upsert).Create(&row).Error; err != nil {
			return err
		}
		return insertFlagRecords(tx, []domain.FlagRecord{rec})
	}); err != nil {
		return fmt.Errorf("save risk review: %w", err)
	}
	return nil
}

// validateRiskReview is Save's check of the review itself. The errors name
// the field and the account, never a value: the note is free text, and an
// error may reach a log.
func validateRiskReview(rev domain.RiskReview, rec domain.FlagRecord) error {
	switch {
	case rev.UserID <= 0:
		return fmt.Errorf("%w: a risk review needs an account", domain.ErrValidation)
	case utf8.RuneCountInString(rev.Note) > domain.ReviewNoteMaxRunes:
		return fmt.Errorf("%w: note too long: the review note of user %d is over %d characters",
			domain.ErrValidation, rev.UserID, domain.ReviewNoteMaxRunes)
	case rev.Dismissed() && len(rev.Accepted) == 0:
		return fmt.Errorf("%w: the dismissal of user %d accepts no level", domain.ErrValidation, rev.UserID)
	case !rev.Dismissed() && len(rev.Accepted) > 0:
		return fmt.Errorf("%w: user %d is not dismissed but carries accepted levels", domain.ErrValidation, rev.UserID)
	case rec.UserID != rev.UserID:
		return fmt.Errorf("%w: the review record of user %d names user %d", domain.ErrValidation, rev.UserID, rec.UserID)
	case rec.Source != domain.FlagSourceReview:
		return fmt.Errorf("%w: the record of a review of user %d is not a review record", domain.ErrValidation, rev.UserID)
	}
	if err := domain.ValidateDismissSnapshot(rev.Accepted); err != nil {
		return fmt.Errorf("risk review of user %d: %w", rev.UserID, err)
	}
	return nil
}

// dismissSnapshotColumn maps a snapshot to its column: NULL for none (nil or
// empty — the one stored form of "no dismissal"), the JSON otherwise.
func dismissSnapshotColumn(d domain.DismissSnapshot) (*string, error) {
	if len(d) == 0 {
		return nil, nil
	}
	b, err := json.Marshal(d)
	if err != nil {
		return nil, fmt.Errorf("%w: risk review snapshot: %v", domain.ErrValidation, err)
	}
	return riskEvidenceColumn(b), nil
}

// dismissSnapshotFrom is the reverse, and forgiving: NULL is no snapshot, and
// a value that is not a snapshot domain.ValidateDismissSnapshot accepts —
// unparsable, another shape, a level or source this build does not know, a
// negative time — reads as an EMPTY one, reported as unreadable. Save never
// writes such a value, but a row edited behind its back, or written by a
// build that knew other sources, must cost only its own snapshot: the
// dismissal then accepted nothing, so every current attention reopens it —
// the safe direction — where an error would fail the whole queue for one row.
func dismissSnapshotFrom(col *string) (snap domain.DismissSnapshot, unreadable bool) {
	if col == nil {
		return nil, false
	}
	raw := riskEvidenceFrom(col)
	if raw == nil {
		return nil, true
	}
	var d domain.DismissSnapshot
	if err := json.Unmarshal(raw, &d); err != nil || domain.ValidateDismissSnapshot(d) != nil {
		return nil, true
	}
	if len(d) == 0 {
		return nil, false
	}
	return d, false
}

// riskReviewsFromRows maps rows to reviews. Unreadable snapshots are logged
// once per read, with a count and never a value.
func riskReviewsFromRows(rows []riskReviewRow) []domain.RiskReview {
	out := make([]domain.RiskReview, 0, len(rows))
	unreadable := 0
	for _, row := range rows {
		snap, bad := dismissSnapshotFrom(row.DismissedLevels)
		if bad {
			unreadable++
		}
		out = append(out, domain.RiskReview{
			UserID: row.UserID, DismissedAtMS: row.DismissedAtMS, DismissedBy: row.DismissedBy,
			Accepted: snap, Note: row.Note,
			Trusted: row.Trusted, TrustedAtMS: row.TrustedAtMS, TrustedBy: row.TrustedBy,
			UpdatedAtMS: row.UpdatedAtMS,
		})
	}
	if unreadable > 0 {
		log.Warn("risk reviews: unreadable dismissal snapshots read as accepting nothing", "rows", unreadable)
	}
	return out
}

// PurgeOrphans deletes the rows of accounts that no longer exist and returns
// how many it deleted; the hourly cleanup runs it. Portable as written, for
// the reason RiskSignalRepo.PurgeOrphans gives.
func (r *RiskReviewRepo) PurgeOrphans(ctx context.Context) (int64, error) {
	res := r.db.WithContext(ctx).Exec("DELETE FROM risk_reviews WHERE user_id NOT IN (SELECT id FROM users)")
	if res.Error != nil {
		return 0, fmt.Errorf("purge risk review orphans: %w", res.Error)
	}
	return res.RowsAffected, nil
}

// idReadChunk is how many ids one IN list of the risk center's reads
// carries. Every dialect takes far more parameters than this (SQLite 32766,
// PostgreSQL and MySQL 65535), but the fleet has no bound, and a statement
// whose size follows the fleet is one that fails on the day it grows.
const idReadChunk = 500

// idChunks returns ids sorted, without repeats, in slices of at most
// idReadChunk. Sorted so each chunk's rows, read in id order, concatenate in
// id order; without repeats so an id asked twice is read once.
func idChunks(ids []int64) [][]int64 {
	if len(ids) == 0 {
		return nil
	}
	sorted := slices.Compact(slices.Sorted(slices.Values(ids)))
	out := make([][]int64, 0, (len(sorted)+idReadChunk-1)/idReadChunk)
	for chunk := range slices.Chunk(sorted, idReadChunk) {
		out = append(out, chunk)
	}
	return out
}
