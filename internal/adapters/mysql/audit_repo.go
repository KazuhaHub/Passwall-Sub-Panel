package mysql

import (
	"context"
	"time"

	"gorm.io/gorm"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

type auditRepo struct{ db *gorm.DB }

func (r *auditRepo) Insert(ctx context.Context, e *domain.AuditEntry) error {
	row := auditRow{
		Actor:      e.Actor,
		Action:     e.Action,
		Target:     e.Target,
		BeforeJSON: e.BeforeJSON,
		AfterJSON:  e.AfterJSON,
		IP:         e.IP,
		At:         e.At,
	}
	if row.At.IsZero() {
		row.At = time.Now()
	}
	if err := r.db.WithContext(ctx).Create(&row).Error; err != nil {
		return err
	}
	e.ID = row.ID
	e.At = row.At
	return nil
}

func (r *auditRepo) List(ctx context.Context, filter ports.AuditFilter) ([]*domain.AuditEntry, int64, error) {
	q := r.db.WithContext(ctx).Model(&auditRow{})
	if filter.Actor != "" {
		q = q.Where("actor = ?", filter.Actor)
	}
	if filter.Action != "" {
		q = q.Where("action = ?", filter.Action)
	}
	if filter.Since != nil {
		q = q.Where("at >= ?", *filter.Since)
	}
	if filter.Until != nil {
		q = q.Where("at <= ?", *filter.Until)
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
	// id DESC is a deterministic tiebreaker: `at` is a non-unique timestamp,
	// and Postgres gives no stable order for equal-key rows across LIMIT/OFFSET
	// pages (so a burst of same-`at` entries could repeat/skip while paging).
	q = q.Order("at DESC, id DESC").Limit(filter.PageSize).Offset((filter.Page - 1) * filter.PageSize)

	var rows []auditRow
	if err := q.Find(&rows).Error; err != nil {
		return nil, 0, err
	}
	out := make([]*domain.AuditEntry, len(rows))
	for i := range rows {
		out[i] = rows[i].toDomain()
	}
	return out, total, nil
}

func (r *auditRepo) Clear(ctx context.Context) error {
	return r.db.WithContext(ctx).Where("1 = 1").Delete(&auditRow{}).Error
}

func (r *auditRepo) DeleteBefore(ctx context.Context, cutoff time.Time) (int64, error) {
	res := r.db.WithContext(ctx).Where("at < ?", cutoff).Delete(&auditRow{})
	return res.RowsAffected, res.Error
}
