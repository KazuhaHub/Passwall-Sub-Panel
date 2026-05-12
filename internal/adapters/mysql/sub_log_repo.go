package mysql

import (
	"context"

	"gorm.io/gorm"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

type subLogRepo struct{ db *gorm.DB }

func (r *subLogRepo) Insert(ctx context.Context, l *domain.SubLog) error {
	row := subLogRow{
		UserID:     l.UserID,
		IP:         l.IP,
		UA:         l.UA,
		ClientType: l.ClientType,
		AccessedAt: l.AccessedAt,
	}
	if err := r.db.WithContext(ctx).Create(&row).Error; err != nil {
		return err
	}
	l.ID = row.ID
	return nil
}

func (r *subLogRepo) List(ctx context.Context, filter ports.SubLogFilter) ([]*domain.SubLog, int64, error) {
	q := r.db.WithContext(ctx).Model(&subLogRow{})
	if filter.UserID != nil {
		q = q.Where("user_id = ?", *filter.UserID)
	}
	if filter.Since != nil {
		q = q.Where("accessed_at >= ?", *filter.Since)
	}
	if filter.Until != nil {
		q = q.Where("accessed_at <= ?", *filter.Until)
	}

	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	if filter.PageSize <= 0 {
		filter.PageSize = 50
	}
	if filter.Page < 1 {
		filter.Page = 1
	}
	q = q.Order("accessed_at DESC").Limit(filter.PageSize).Offset((filter.Page - 1) * filter.PageSize)

	var rows []subLogRow
	if err := q.Find(&rows).Error; err != nil {
		return nil, 0, err
	}
	out := make([]*domain.SubLog, len(rows))
	for i := range rows {
		out[i] = rows[i].toDomain()
	}
	return out, total, nil
}

