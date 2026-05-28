package mysql

import (
	"context"
	"time"

	"gorm.io/gorm"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

// separatorRepo is the GORM-backed implementation of ports.SeparatorRepo.
// Operates on the `nodes_separator` table — see separatorRow in schema.go
// and domain.SeparatorEntry for the entity shape.
type separatorRepo struct{ db *gorm.DB }

// Static interface assertion so a method signature drift breaks build.
var _ ports.SeparatorRepo = (*separatorRepo)(nil)

func (r *separatorRepo) Create(ctx context.Context, s *domain.SeparatorEntry) error {
	if s.CreatedAt.IsZero() {
		s.CreatedAt = time.Now()
	}
	row := separatorFromDomain(s)
	if err := r.db.WithContext(ctx).Create(row).Error; err != nil {
		return err
	}
	s.ID = row.ID
	return nil
}

func (r *separatorRepo) Update(ctx context.Context, s *domain.SeparatorEntry) error {
	row := separatorFromDomain(s)
	return r.db.WithContext(ctx).Save(row).Error
}

func (r *separatorRepo) Delete(ctx context.Context, id int64) error {
	return r.db.WithContext(ctx).Delete(&separatorRow{}, id).Error
}

func (r *separatorRepo) GetByID(ctx context.Context, id int64) (*domain.SeparatorEntry, error) {
	var row separatorRow
	if err := r.db.WithContext(ctx).First(&row, id).Error; err != nil {
		return nil, wrapNotFound(err)
	}
	return row.toDomain(), nil
}

func (r *separatorRepo) List(ctx context.Context) ([]*domain.SeparatorEntry, error) {
	var rows []separatorRow
	if err := r.db.WithContext(ctx).Order("sort_order ASC, id ASC").Find(&rows).Error; err != nil {
		return nil, err
	}
	out := make([]*domain.SeparatorEntry, 0, len(rows))
	for i := range rows {
		out = append(out, rows[i].toDomain())
	}
	return out, nil
}

var separatorSortAllowlist = map[string]string{
	"id":           "id",
	"display_name": "display_name",
	"sort_order":   "sort_order",
	"created_at":   "created_at",
}

func (r *separatorRepo) ListPaged(ctx context.Context, p ports.Pagination) ([]*domain.SeparatorEntry, int64, error) {
	q := r.db.WithContext(ctx).Model(&separatorRow{})
	if like := keywordLike(p.Keyword); like != "" {
		q = q.Where(likeCols("display_name"), like)
	}
	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var rows []separatorRow
	if err := applyPagination(q, p, separatorSortAllowlist, "sort_order").Find(&rows).Error; err != nil {
		return nil, 0, err
	}
	out := make([]*domain.SeparatorEntry, 0, len(rows))
	for i := range rows {
		out = append(out, rows[i].toDomain())
	}
	return out, total, nil
}

// BatchUpdateSortOrder rewrites sort_order for every listed separator
// in one transaction. Mirrors nodeRepo.BatchUpdateSortOrder so the
// admin drag-to-reorder bar can update both tables atomically (the
// frontend issues two PUTs, one per kind).
func (r *separatorRepo) BatchUpdateSortOrder(ctx context.Context, updates []ports.SeparatorSortUpdate) error {
	if len(updates) == 0 {
		return nil
	}
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		for _, u := range updates {
			if err := tx.Model(&separatorRow{}).
				Where("id = ?", u.SeparatorID).
				Update("sort_order", u.SortOrder).Error; err != nil {
				return err
			}
		}
		return nil
	})
}

func (r *separatorRepo) ListEnabled(ctx context.Context) ([]*domain.SeparatorEntry, error) {
	var rows []separatorRow
	if err := r.db.WithContext(ctx).
		Where("enabled = ?", true).
		Order("sort_order ASC, id ASC").
		Find(&rows).Error; err != nil {
		return nil, err
	}
	out := make([]*domain.SeparatorEntry, 0, len(rows))
	for i := range rows {
		out = append(out, rows[i].toDomain())
	}
	return out, nil
}
