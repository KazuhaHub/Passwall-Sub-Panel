package sqlstore

import (
	"context"
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

	// Conflict: inspect the stored window.
	var existing ssoAssertionSeenRow
	if err := r.db.WithContext(ctx).
		Where("assertion_id = ?", assertionID).
		Take(&existing).Error; err != nil {
		// Row vanished between the insert and this read (a concurrent
		// DeleteExpired). Treat as a replay: refusing one legitimate login is
		// the safe direction for a security control, and a retry succeeds.
		return true, nil
	}
	if now.Before(existing.ExpiresAt) {
		return true, nil // still inside the window → genuine replay
	}
	// Stale row for a reused ID — take it over with the new window.
	if err := r.db.WithContext(ctx).
		Model(&ssoAssertionSeenRow{}).
		Where("assertion_id = ?", assertionID).
		Updates(map[string]any{"expires_at": expiresAt.UTC(), "consumed_at": now.UTC()}).Error; err != nil {
		return false, err
	}
	return false, nil
}

func (r *samlReplayRepo) DeleteExpired(ctx context.Context, now time.Time) (int64, error) {
	res := r.db.WithContext(ctx).
		Where("expires_at <= ?", now.UTC()).
		Delete(&ssoAssertionSeenRow{})
	return res.RowsAffected, res.Error
}
