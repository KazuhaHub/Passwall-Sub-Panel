package mysql

import (
	"context"

	"gorm.io/gorm"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

type userRepo struct{ db *gorm.DB }

func (r *userRepo) Create(ctx context.Context, u *domain.User) error {
	row := userFromDomain(u)
	if err := r.db.WithContext(ctx).Create(row).Error; err != nil {
		return err
	}
	u.ID = row.ID
	u.CreatedAt = row.CreatedAt
	u.UpdatedAt = row.UpdatedAt
	return nil
}

func (r *userRepo) Update(ctx context.Context, u *domain.User) error {
	return r.db.WithContext(ctx).Save(userFromDomain(u)).Error
}

func (r *userRepo) Delete(ctx context.Context, id int64) error {
	return r.db.WithContext(ctx).Delete(&userRow{}, id).Error
}

func (r *userRepo) GetByID(ctx context.Context, id int64) (*domain.User, error) {
	var row userRow
	if err := r.db.WithContext(ctx).First(&row, id).Error; err != nil {
		return nil, wrapNotFound(err)
	}
	return row.toDomain(), nil
}

func (r *userRepo) GetByUPN(ctx context.Context, upn string) (*domain.User, error) {
	var row userRow
	if err := r.db.WithContext(ctx).Where("upn = ?", upn).First(&row).Error; err != nil {
		return nil, wrapNotFound(err)
	}
	return row.toDomain(), nil
}

func (r *userRepo) GetBySSO(ctx context.Context, provider, subject string) (*domain.User, error) {
	if provider == "" || subject == "" {
		return nil, domain.ErrNotFound
	}
	var row userRow
	if err := r.db.WithContext(ctx).
		Where("sso_provider = ? AND sso_subject = ?", provider, subject).
		First(&row).Error; err != nil {
		return nil, wrapNotFound(err)
	}
	return row.toDomain(), nil
}

func (r *userRepo) GetBySubToken(ctx context.Context, token string) (*domain.User, error) {
	var row userRow
	if err := r.db.WithContext(ctx).Where("sub_token = ?", token).First(&row).Error; err != nil {
		return nil, wrapNotFound(err)
	}
	return row.toDomain(), nil
}

func (r *userRepo) List(ctx context.Context, filter ports.UserFilter) ([]*domain.User, int64, error) {
	q := r.db.WithContext(ctx).Model(&userRow{})
	if filter.Search != "" {
		like := "%" + filter.Search + "%"
		// Search across the user-facing identifiers admins actually scan
		// the table for: account name, friendly display, email. Remark is
		// intentionally out — it's free-form admin notes; matching on it
		// surfaced "why does this user show up?" results that confused
		// people.
		q = q.Where("upn LIKE ? OR display_name LIKE ? OR email LIKE ?", like, like, like)
	}
	if filter.GroupID != nil {
		q = q.Where("group_id = ?", *filter.GroupID)
	}
	if filter.Role != nil {
		q = q.Where("role = ?", string(*filter.Role))
	}
	if filter.Enabled != nil {
		q = q.Where("enabled = ?", *filter.Enabled)
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
	q = q.Order("id DESC").Limit(filter.PageSize).Offset((filter.Page - 1) * filter.PageSize)

	var rows []userRow
	if err := q.Find(&rows).Error; err != nil {
		return nil, 0, err
	}
	out := make([]*domain.User, len(rows))
	for i := range rows {
		out[i] = rows[i].toDomain()
	}
	return out, total, nil
}

func (r *userRepo) ListByGroup(ctx context.Context, groupID int64) ([]*domain.User, error) {
	var rows []userRow
	if err := r.db.WithContext(ctx).Where("group_id = ?", groupID).Find(&rows).Error; err != nil {
		return nil, err
	}
	out := make([]*domain.User, len(rows))
	for i := range rows {
		out[i] = rows[i].toDomain()
	}
	return out, nil
}
