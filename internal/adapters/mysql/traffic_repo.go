package mysql

import (
	"context"
	"time"

	"gorm.io/gorm"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
)

type trafficRepo struct{ db *gorm.DB }

func (r *trafficRepo) Insert(ctx context.Context, s *domain.TrafficSnapshot) error {
	row := trafficRow{
		UserID:     s.UserID,
		UpBytes:    s.UpBytes,
		DownBytes:  s.DownBytes,
		TotalBytes: s.TotalBytes,
		CapturedAt: s.CapturedAt,
	}
	if err := r.db.WithContext(ctx).Create(&row).Error; err != nil {
		return err
	}
	s.ID = row.ID
	return nil
}

func (r *trafficRepo) LatestForUser(ctx context.Context, userID int64) (*domain.TrafficSnapshot, error) {
	var row trafficRow
	tx := r.db.WithContext(ctx).
		Where("user_id = ?", userID).
		Order("id DESC").
		Limit(1).
		Find(&row)
	if tx.Error != nil {
		return nil, tx.Error
	}
	if tx.RowsAffected == 0 {
		return nil, domain.ErrNotFound
	}
	return row.toDomain(), nil
}

func (r *trafficRepo) LastBefore(ctx context.Context, userID int64, before time.Time) (*domain.TrafficSnapshot, error) {
	var row trafficRow
	tx := r.db.WithContext(ctx).
		Where("user_id = ? AND captured_at < ?", userID, before).
		Order("captured_at DESC").
		Limit(1).
		Find(&row)
	if tx.Error != nil {
		return nil, tx.Error
	}
	if tx.RowsAffected == 0 {
		return nil, domain.ErrNotFound
	}
	return row.toDomain(), nil
}

func (r *trafficRepo) ListByUser(ctx context.Context, userID int64, since, until time.Time) ([]*domain.TrafficSnapshot, error) {
	var rows []trafficRow
	err := r.db.WithContext(ctx).
		Where("user_id = ? AND captured_at >= ? AND captured_at < ?", userID, since, until).
		Order("captured_at ASC").
		Find(&rows).Error
	if err != nil {
		return nil, err
	}
	out := make([]*domain.TrafficSnapshot, len(rows))
	for i := range rows {
		out[i] = rows[i].toDomain()
	}
	return out, nil
}
