package mysql

import (
	"context"

	"gorm.io/gorm"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
)

type nodeRepo struct{ db *gorm.DB }

func (r *nodeRepo) Create(ctx context.Context, n *domain.Node) error {
	row := nodeFromDomain(n)
	if err := r.db.WithContext(ctx).Create(row).Error; err != nil {
		return err
	}
	n.ID = row.ID
	n.CreatedAt = row.CreatedAt
	return nil
}

func (r *nodeRepo) Update(ctx context.Context, n *domain.Node) error {
	return r.db.WithContext(ctx).Save(nodeFromDomain(n)).Error
}

func (r *nodeRepo) Delete(ctx context.Context, id int64) error {
	return r.db.WithContext(ctx).Delete(&nodeRow{}, id).Error
}

func (r *nodeRepo) GetByID(ctx context.Context, id int64) (*domain.Node, error) {
	var row nodeRow
	if err := r.db.WithContext(ctx).First(&row, id).Error; err != nil {
		return nil, wrapNotFound(err)
	}
	return row.toDomain(), nil
}

func (r *nodeRepo) GetByPanelInbound(ctx context.Context, panel string, inboundID int) (*domain.Node, error) {
	var row nodeRow
	err := r.db.WithContext(ctx).
		Where("panel_name = ? AND inbound_id = ?", panel, inboundID).
		First(&row).Error
	if err != nil {
		return nil, wrapNotFound(err)
	}
	return row.toDomain(), nil
}

func (r *nodeRepo) List(ctx context.Context) ([]*domain.Node, error) {
	var rows []nodeRow
	if err := r.db.WithContext(ctx).Order("sort_order ASC, id ASC").Find(&rows).Error; err != nil {
		return nil, err
	}
	out := make([]*domain.Node, len(rows))
	for i := range rows {
		out[i] = rows[i].toDomain()
	}
	return out, nil
}

func (r *nodeRepo) ListEnabled(ctx context.Context) ([]*domain.Node, error) {
	var rows []nodeRow
	err := r.db.WithContext(ctx).
		Where("enabled = ?", true).
		Order("sort_order ASC, id ASC").
		Find(&rows).Error
	if err != nil {
		return nil, err
	}
	out := make([]*domain.Node, len(rows))
	for i := range rows {
		out[i] = rows[i].toDomain()
	}
	return out, nil
}
