package sqlstore

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"gorm.io/gorm"
)

func (r *DestDefinitionRepo) ReorderPolicies(ctx context.Context, action domain.DestAction, ids []int64, now time.Time) error {
	if action != domain.DestAllow && action != domain.DestBlock && action != domain.DestObserve {
		return domain.ErrValidation
	}
	now, err := destWriteTime(now)
	if err != nil {
		return err
	}
	return r.mutate(ctx, now, func(tx *gorm.DB) (bool, error) {
		var rows []destPolicyRow
		if err := tx.Where("action = ?", string(action)).Find(&rows).Error; err != nil {
			return false, err
		}
		byID := make(map[int64]destPolicyRow, len(rows))
		for _, row := range rows {
			byID[row.ID] = row
		}
		if len(ids) != len(rows) {
			return false, fmt.Errorf("%w: dest_policy_order_stale", domain.ErrConflict)
		}
		ordered := make([]destPolicyRow, 0, len(ids))
		for _, id := range ids {
			row, ok := byID[id]
			if !ok {
				return false, fmt.Errorf("%w: dest_policy_order_stale", domain.ErrConflict)
			}
			ordered = append(ordered, row)
			delete(byID, id)
		}
		changed := false
		for i, row := range ordered {
			if row.Priority == i+1 {
				continue
			}
			if err := tx.Model(&destPolicyRow{}).Where("id = ?", row.ID).Updates(map[string]any{"priority": i + 1, "updated_at": nextDestRowTime(now, row.UpdatedAt)}).Error; err != nil {
				return false, err
			}
			changed = true
		}
		return changed, nil
	})
}
func (r *DestDefinitionRepo) SaveExemption(ctx context.Context, ex *domain.DestExemption, create bool, now time.Time) error {
	if ex == nil || ex.UserID <= 0 || (create && ex.CreatedBy <= 0) {
		return domain.ErrValidation
	}
	now, err := destWriteTime(now)
	if err != nil {
		return err
	}
	row := destExemptionRow{UserID: ex.UserID, Reason: ex.Reason, CreatedBy: ex.CreatedBy, ExpiresAt: ex.ExpiresAt}
	err = r.mutate(ctx, now, func(tx *gorm.DB) (bool, error) {
		if create {
			row.CreatedAt = now
			err := tx.Create(&row).Error
			if isUniqueViolationErr(err) {
				return false, fmt.Errorf("%w: dest_exemption_exists", domain.ErrAlreadyExists)
			}
			return true, err
		}
		var old destExemptionRow
		if err := tx.First(&old, "user_id = ?", row.UserID).Error; err != nil {
			return false, destinationRowError(err)
		}
		row.CreatedBy, row.CreatedAt = old.CreatedBy, old.CreatedAt
		if row.Reason == old.Reason && equalDestTime(row.ExpiresAt, old.ExpiresAt) {
			row = old
			return false, nil
		}
		return true, tx.Model(&destExemptionRow{}).Where("user_id = ?", row.UserID).Updates(map[string]any{"reason": row.Reason, "expires_at": row.ExpiresAt}).Error
	})
	if err == nil {
		*ex = domain.DestExemption{UserID: row.UserID, Reason: row.Reason, CreatedBy: row.CreatedBy, CreatedAt: row.CreatedAt, ExpiresAt: row.ExpiresAt}
	}
	return err
}
func (r *DestDefinitionRepo) DeleteExemption(ctx context.Context, userID int64, now time.Time) error {
	return r.mutate(ctx, now, func(tx *gorm.DB) (bool, error) {
		result := tx.Where("user_id = ?", userID).Delete(&destExemptionRow{})
		if result.Error != nil {
			return false, result.Error
		}
		if result.RowsAffected == 0 {
			return false, domain.ErrNotFound
		}
		return true, nil
	})
}
func (r *DestDefinitionRepo) PruneExpiredExemptions(ctx context.Context, now time.Time) (int64, error) {
	var removed int64
	err := r.mutate(ctx, now, func(tx *gorm.DB) (bool, error) {
		result := tx.Where("expires_at IS NOT NULL AND expires_at <= ?", now.UTC().Truncate(time.Millisecond)).Delete(&destExemptionRow{})
		removed = result.RowsAffected
		return removed > 0, result.Error
	})
	if err != nil {
		return 0, err
	}
	return removed, nil
}

func destinationRowError(err error) error {
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return domain.ErrNotFound
	}
	return err
}

func equalDestTime(a, b *time.Time) bool {
	return (a == nil && b == nil) || (a != nil && b != nil && a.Equal(*b))
}
