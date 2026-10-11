package sqlstore

import (
	"context"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func (r *DestDefinitionRepo) ReadDestinationGroupListeners(ctx context.Context) ([]domain.DestGroupListener, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if r == nil || r.db == nil {
		return nil, domain.ErrUnavailable
	}
	out := []domain.DestGroupListener{}
	err := r.db.WithContext(ctx).Session(&gorm.Session{Logger: logger.Discard}).Model(&nodeRow{}).
		Select("id", "panel_id", "sniffing").Where("enabled = ?", true).Order("id").Find(&out).Error
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err != nil {
		return nil, auditStorageError(err)
	}
	for _, item := range out {
		if item.NodeID <= 0 || item.PanelID <= 0 {
			return nil, domain.ErrUnavailable
		}
	}
	return out, nil
}

var _ ports.DestGroupListenerReadRepo = (*DestDefinitionRepo)(nil)
