package mysql

import (
	"context"

	"gorm.io/gorm"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
)

type groupRepo struct{ db *gorm.DB }

func (r *groupRepo) Create(ctx context.Context, g *domain.Group) error {
	row := groupFromDomain(g)
	if err := r.db.WithContext(ctx).Create(row).Error; err != nil {
		return err
	}
	g.ID = row.ID
	g.CreatedAt = row.CreatedAt
	return nil
}

func (r *groupRepo) Update(ctx context.Context, g *domain.Group) error {
	return r.db.WithContext(ctx).Save(groupFromDomain(g)).Error
}

func (r *groupRepo) Delete(ctx context.Context, id int64) error {
	return r.db.WithContext(ctx).Delete(&groupRow{}, id).Error
}

func (r *groupRepo) GetByID(ctx context.Context, id int64) (*domain.Group, error) {
	var row groupRow
	if err := r.db.WithContext(ctx).First(&row, id).Error; err != nil {
		return nil, wrapNotFound(err)
	}
	return row.toDomain(), nil
}

func (r *groupRepo) GetBySlug(ctx context.Context, slug string) (*domain.Group, error) {
	var row groupRow
	if err := r.db.WithContext(ctx).Where("slug = ?", slug).First(&row).Error; err != nil {
		return nil, wrapNotFound(err)
	}
	return row.toDomain(), nil
}

func (r *groupRepo) List(ctx context.Context) ([]*domain.Group, error) {
	var rows []groupRow
	if err := r.db.WithContext(ctx).Order("id ASC").Find(&rows).Error; err != nil {
		return nil, err
	}
	out := make([]*domain.Group, len(rows))
	for i := range rows {
		out[i] = rows[i].toDomain()
	}
	return out, nil
}

func (r *groupRepo) CountMembers(ctx context.Context, id int64) (int64, error) {
	var n int64
	err := r.db.WithContext(ctx).Model(&userRow{}).Where("group_id = ?", id).Count(&n).Error
	return n, err
}
