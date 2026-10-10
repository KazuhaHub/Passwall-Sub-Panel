package sqlstore

import (
	"context"
	"errors"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"gorm.io/gorm"
)

// Account access never needs credentials, entitlements or private list bodies.
// All display metadata comes from one consistent read transaction.
func (r *DestDefinitionRepo) UserAccess(ctx context.Context, userID int64) (domain.DestUserAccess, error) {
	if userID <= 0 {
		return domain.DestUserAccess{}, domain.ErrValidation
	}
	var result domain.DestUserAccess
	err := r.readTransaction(ctx, func(tx *gorm.DB) error {
		var user userRow
		if err := tx.Select("id", "upn", "group_id").First(&user, "id = ?", userID).Error; err != nil {
			return destinationRowError(err)
		}
		result.UPN = user.UPN
		if user.GroupID > 0 {
			var group groupRow
			err := tx.Select("id", "name").First(&group, "id = ?", user.GroupID).Error
			if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
				return err
			}
			if err == nil {
				mode, _, err := readGroupMode(tx, group.ID)
				if err != nil {
					return err
				}
				result.Group = &domain.DestUserAccessGroup{ID: group.ID, Name: group.Name, Mode: mode.Mode, Stage: mode.Stage}
			}
		}
		var exemption destExemptionRow
		err := tx.First(&exemption, "user_id = ?", userID).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil
		}
		if err != nil {
			return err
		}
		ex := destinationExemptionToDomain(exemption)
		result.Exemption = &ex
		var creator userRow
		err = tx.Select("id", "upn").First(&creator, "id = ?", ex.CreatedBy).Error
		if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		if err == nil {
			result.CreatedByUPN = &creator.UPN
		}
		return nil
	})
	if err != nil {
		return domain.DestUserAccess{}, err
	}
	return result, nil
}
