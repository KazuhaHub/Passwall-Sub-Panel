package sqlstore

import (
	"context"
	"slices"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func (r *DestDefinitionRepo) ReadDestinationGroupModes(ctx context.Context, ids []int64) (map[int64]domain.DestGroupAccessMode, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	ids = slices.Clone(ids)
	slices.Sort(ids)
	ids = slices.Compact(ids)
	result := make(map[int64]domain.DestGroupAccessMode, len(ids))
	for _, id := range ids {
		if id <= 0 {
			return nil, domain.ErrValidation
		}
		result[id] = domain.DestGroupAccessOpen
	}
	if len(ids) == 0 {
		return result, nil
	}
	if r == nil || r.db == nil {
		return nil, domain.ErrUnavailable
	}
	private := &DestDefinitionRepo{db: r.db.Session(&gorm.Session{Logger: logger.Discard})}
	err := private.readTransaction(ctx, func(tx *gorm.DB) error {
		for start := 0; start < len(ids); start += 512 {
			var rows []destGroupModeRow
			if err := tx.Select("group_id", "mode", "stage").Where("group_id IN ?", ids[start:min(start+512, len(ids))]).Find(&rows).Error; err != nil {
				return err
			}
			for _, row := range rows {
				mode, err := domain.DestinationGroupAccessMode(row.Mode, row.Stage)
				if err != nil {
					return domain.ErrUnavailable
				}
				result[row.GroupID] = mode
			}
		}
		return nil
	})
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err != nil {
		return nil, domain.ErrUnavailable
	}
	return result, nil
}

var _ ports.DestGroupModeReadRepo = (*DestDefinitionRepo)(nil)
