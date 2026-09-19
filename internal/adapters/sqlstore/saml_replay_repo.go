package sqlstore

import (
	"context"
	"errors"
	"fmt"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// samlReplayRepo is the durable consumed-assertion set behind
// ports.SAMLReplayRepo. See that interface for why persisting this matters.
type samlReplayRepo struct{ db *gorm.DB }

// SeenOrAdd is insert-if-absent in ONE statement (INSERT ... ON CONFLICT DO
// NOTHING), which is what makes it safe against two concurrent submissions of
// the same stolen assertion: exactly one INSERT reports a row affected, so
// exactly one caller is told "not seen".
//
// A conflict alone does not prove a replay — the stored row may be a leftover
// whose window already closed. In that case the assertion is expired anyway and
// the SAML library rejects it independently, so we refresh the row and report
// "not seen" rather than failing a legitimate login on a recycled ID.
//
// That refresh is conditional on the row still being expired, so the takeover
// path keeps the same "exactly one caller wins" property as the insert path,
// and a read failure on the way is reported as an error rather than as a replay.
func (r *samlReplayRepo) SeenOrAdd(ctx context.Context, assertionID string, expiresAt time.Time, now time.Time) (bool, error) {
	if assertionID == "" {
		// Mirrors the in-memory cache: a blank ID is never recorded. The caller
		// rejects blank IDs outright before reaching here.
		return false, nil
	}
	row := ssoAssertionSeenRow{AssertionID: assertionID, ExpiresAt: expiresAt.UTC(), ConsumedAt: now.UTC()}
	res := r.db.WithContext(ctx).
		Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "assertion_id"}}, DoNothing: true}).
		Create(&row)
	if res.Error != nil {
		return false, res.Error
	}
	if res.RowsAffected == 1 {
		return false, nil // first time we have seen this ID
	}

	// Conflict: inspect the stored window. A read failure is NOT a replay — the
	// caller has to be able to tell "this assertion was already used" from "we
	// cannot tell", because only the second is an infrastructure fault worth
	// alarming on and only the first is an attack (ADR 0036 D5).
	existing, found, err := readExistingRow(ctx, r.db, assertionID)
	if err != nil {
		return false, fmt.Errorf("saml replay: reading conflicting row: %w", err)
	}
	if !found {
		// Row vanished between the insert and this read (a concurrent
		// DeleteExpired). Treat as a replay: refusing one legitimate login is
		// the safe direction for a security control, and a retry succeeds.
		return true, nil
	}
	if now.Before(existing.ExpiresAt) {
		return true, nil // still inside the window → genuine replay
	}
	// Stale row for a recycled ID. Refresh it, but only while it is STILL
	// expired: without that predicate two concurrent takeovers would both see
	// RowsAffected == 1 and both be told "first", which is exactly the
	// atomicity the insert path gets for free from the primary key.
	tookOver, err := r.takeOverExpiredRow(ctx, assertionID, expiresAt, now)
	if err != nil {
		return false, err
	}
	if tookOver {
		return false, nil
	}
	// Another caller took the row over first, so this presentation is a replay.
	return true, nil
}

// readExistingRow reads the row that made the insert conflict. found is false
// only when the row genuinely does not exist; an infrastructure failure comes
// back as an error rather than being folded into "no such row".
func readExistingRow(ctx context.Context, db *gorm.DB, assertionID string) (*ssoAssertionSeenRow, bool, error) {
	var existing ssoAssertionSeenRow
	err := db.WithContext(ctx).Where("assertion_id = ?", assertionID).Take(&existing).Error
	switch {
	case err == nil:
		return &existing, true, nil
	case errors.Is(err, gorm.ErrRecordNotFound):
		return nil, false, nil
	default:
		return nil, false, err
	}
}

// takeOverExpiredRow refreshes a closed-window row and reports whether THIS
// caller was the one that got it. The `expires_at <= now` predicate is the
// whole point: an unconditional UPDATE would hand the same window to every
// concurrent recycler of that ID.
func (r *samlReplayRepo) takeOverExpiredRow(ctx context.Context, assertionID string, expiresAt, now time.Time) (bool, error) {
	res := r.db.WithContext(ctx).
		Model(&ssoAssertionSeenRow{}).
		Where("assertion_id = ? AND expires_at <= ?", assertionID, now.UTC()).
		Updates(map[string]any{"expires_at": expiresAt.UTC(), "consumed_at": now.UTC()})
	if res.Error != nil {
		return false, res.Error
	}
	return res.RowsAffected == 1, nil
}

func (r *samlReplayRepo) DeleteExpired(ctx context.Context, now time.Time) (int64, error) {
	res := r.db.WithContext(ctx).
		Where("expires_at <= ?", now.UTC()).
		Delete(&ssoAssertionSeenRow{})
	return res.RowsAffected, res.Error
}
