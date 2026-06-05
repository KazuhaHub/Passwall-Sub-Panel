package mysql

import (
	"context"
	"time"

	"gorm.io/gorm"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
)

// certEventRow is one append-only cert issuance/renewal activity entry. It holds
// NO secrets (never PEM material); CertName is snapshotted so the log survives
// the cert being deleted.
type certEventRow struct {
	ID        int64     `gorm:"primaryKey;autoIncrement"`
	CertID    int64     `gorm:"column:cert_id;index"`
	CertName  string    `gorm:"column:cert_name;size:255"`
	Kind      string    `gorm:"column:kind;size:16"`
	Success   bool      `gorm:"column:success"`
	Message   string    `gorm:"column:message;type:text"`
	CreatedAt time.Time `gorm:"column:created_at;index"`
}

func (certEventRow) TableName() string { return "cert_events" }

func (r *certEventRow) toDomain() *domain.CertEvent {
	return &domain.CertEvent{
		ID:        r.ID,
		CertID:    r.CertID,
		CertName:  r.CertName,
		Kind:      domain.CertEventKind(r.Kind),
		Success:   r.Success,
		Message:   r.Message,
		CreatedAt: r.CreatedAt,
	}
}

type certEventRepo struct{ db *gorm.DB }

func (r *certEventRepo) Create(ctx context.Context, e *domain.CertEvent) error {
	row := &certEventRow{
		CertID:   e.CertID,
		CertName: e.CertName,
		Kind:     string(e.Kind),
		Success:  e.Success,
		Message:  e.Message,
	}
	if err := r.db.WithContext(ctx).Create(row).Error; err != nil {
		return err
	}
	e.ID = row.ID
	e.CreatedAt = row.CreatedAt
	return nil
}

func (r *certEventRepo) ListPaged(ctx context.Context, limit, offset int) ([]*domain.CertEvent, int64, error) {
	if limit <= 0 {
		limit = 50
	}
	if offset < 0 {
		offset = 0
	}
	var total int64
	if err := r.db.WithContext(ctx).Model(&certEventRow{}).Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var rows []certEventRow
	if err := r.db.WithContext(ctx).Order("id DESC").Limit(limit).Offset(offset).Find(&rows).Error; err != nil {
		return nil, 0, err
	}
	out := make([]*domain.CertEvent, len(rows))
	for i := range rows {
		out[i] = rows[i].toDomain()
	}
	return out, total, nil
}

func (r *certEventRepo) PruneOlderThan(ctx context.Context, cutoff time.Time) (int64, error) {
	res := r.db.WithContext(ctx).Where("created_at < ?", cutoff).Delete(&certEventRow{})
	return res.RowsAffected, res.Error
}
