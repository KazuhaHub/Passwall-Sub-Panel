package sqlstore

import (
	"context"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
	"time"
)

func (r *DestDefinitionRepo) PruneOrphanedExemptions(ctx context.Context, now time.Time) (int64, error) {
	private := *r
	private.db = r.db.Session(&gorm.Session{Logger: logger.Discard})
	var removed int64
	err := private.mutate(ctx, now, func(tx *gorm.DB) (bool, error) {
		result := tx.Where("NOT EXISTS (SELECT 1 FROM users WHERE users.id = dest_exemptions.user_id)").Delete(&destExemptionRow{})
		removed = result.RowsAffected
		return removed > 0, result.Error
	})
	if err != nil {
		return 0, auditStorageError(err)
	}
	return removed, nil
}
