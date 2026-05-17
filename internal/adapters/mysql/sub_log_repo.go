package mysql

import (
	"context"
	"time"

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
	if filter.PageSize <= 0 {
		filter.PageSize = 50
	}
	if filter.Page < 1 {
		filter.Page = 1
	}

	// Build base query for count
	countQ := r.db.WithContext(ctx).Model(&subLogRow{})
	if filter.UserID != nil {
		countQ = countQ.Where("user_id = ?", *filter.UserID)
	}
	if filter.Since != nil {
		countQ = countQ.Where("accessed_at >= ?", *filter.Since)
	}
	if filter.Until != nil {
		countQ = countQ.Where("accessed_at <= ?", *filter.Until)
	}
	var total int64
	if err := countQ.Count(&total).Error; err != nil {
		return nil, 0, err
	}

	// Query with user join
	type subLogWithUser struct {
		ID          int64
		UserID      int64
		IP          string
		UA          string
		ClientType  string
		AccessedAt  time.Time
		UserUPN     string
		UserDisplay string
		UserGroupID int64
	}

	q := r.db.WithContext(ctx).
		Table("sub_logs").
		Select("sub_logs.*, users.upn as user_upn, users.display_name as user_display, users.group_id as user_group_id").
		Joins("LEFT JOIN users ON users.id = sub_logs.user_id")

	if filter.UserID != nil {
		q = q.Where("sub_logs.user_id = ?", *filter.UserID)
	}
	if filter.Since != nil {
		q = q.Where("sub_logs.accessed_at >= ?", *filter.Since)
	}
	if filter.Until != nil {
		q = q.Where("sub_logs.accessed_at <= ?", *filter.Until)
	}

	var rows []subLogWithUser
	if err := q.Order("sub_logs.accessed_at DESC").
		Limit(filter.PageSize).
		Offset((filter.Page - 1) * filter.PageSize).
		Find(&rows).Error; err != nil {
		return nil, 0, err
	}

	out := make([]*domain.SubLog, len(rows))
	for i, row := range rows {
		out[i] = &domain.SubLog{
			ID:          row.ID,
			UserID:      row.UserID,
			UserUPN:     row.UserUPN,
			UserDisplay: row.UserDisplay,
			UserGroupID: row.UserGroupID,
			IP:          row.IP,
			UA:          row.UA,
			ClientType:  row.ClientType,
			AccessedAt:  row.AccessedAt,
		}
	}
	return out, total, nil
}

func (r *subLogRepo) Clear(ctx context.Context) error {
	return r.db.WithContext(ctx).Where("1 = 1").Delete(&subLogRow{}).Error
}

func (r *subLogRepo) DeleteBefore(ctx context.Context, cutoff time.Time) (int64, error) {
	// accessed_at is a DATETIME column; passing a raw Unix int64 here makes
	// MySQL try to parse the integer as a DATETIME literal and fail with
	// 1292 (22007) "Incorrect datetime value". GORM serializes time.Time
	// into the right DATETIME representation automatically.
	result := r.db.WithContext(ctx).Where("accessed_at < ?", cutoff).Delete(&subLogRow{})
	return result.RowsAffected, result.Error
}

