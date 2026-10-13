package sqlstore

import (
	"context"
	"fmt"
	"time"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func (r *DestDefinitionRepo) AddGroupException(ctx context.Context, groupID int64, now time.Time, edit func(domain.DestList) (domain.DestList, error)) (int64, error) {
	if groupID <= 0 || edit == nil {
		return 0, domain.ErrValidation
	}
	if r == nil || r.db == nil {
		return 0, domain.ErrUnavailable
	}
	var listID int64
	private := &DestDefinitionRepo{db: r.db.Session(&gorm.Session{Logger: logger.Discard})}
	err := private.mutate(ctx, now, func(tx *gorm.DB) (bool, error) {
		if err := destinationGroupExists(tx, groupID); err != nil {
			return false, err
		}
		mode, _, err := readGroupMode(tx, groupID)
		if err != nil {
			return false, err
		}
		if mode.Mode != "allowlist" || mode.ExtraListID <= 0 || mode.ExtraListID == mode.BaseListID {
			return false, fmt.Errorf("%w: dest_mode_invalid_transition", domain.ErrConflict)
		}
		var old destListRow
		if err := tx.First(&old, "id = ?", mode.ExtraListID).Error; err != nil {
			return false, domain.ErrUnavailable
		}
		if old.OwnerGroupID != groupID || old.Kind != string(domain.DestListCustom) {
			return false, domain.ErrUnavailable
		}
		_, changed, err := editDestinationListRow(tx, old, now.UTC().Truncate(time.Millisecond), true, func(list domain.DestList, _ bool) (domain.DestList, error) { return edit(list) })
		if err != nil {
			return false, err
		}
		listID = old.ID
		return changed, nil
	})
	if err != nil {
		return 0, err
	}
	return listID, nil
}
