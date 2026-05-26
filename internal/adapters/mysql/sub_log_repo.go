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

// subLogSortAllowlist maps API names to the joined-table column names
// the query needs. The "sub_logs." prefix is required because every
// query carries a LEFT JOIN users — a bare "accessed_at" would be
// ambiguous on Postgres.
var subLogSortAllowlist = map[string]string{
	"accessed_at": "sub_logs.accessed_at",
	"id":          "sub_logs.id",
	"ip":          "sub_logs.ip",
	"client_type": "sub_logs.client_type",
}

func (r *subLogRepo) List(ctx context.Context, filter ports.SubLogFilter) ([]*domain.SubLog, int64, error) {
	if filter.PageSize <= 0 {
		filter.PageSize = 50
	}
	if filter.Page < 1 {
		filter.Page = 1
	}
	if filter.SortBy == "" {
		filter.SortBy = "accessed_at"
	}
	if filter.SortDir == "" {
		filter.SortDir = "desc"
	}

	// applyFilters constrains a sub_logs query, joined to users so search can
	// also hit upn / display_name. Reused for both the count and page query so
	// the total stays consistent with the rows returned.
	applyFilters := func(q *gorm.DB) *gorm.DB {
		q = q.Joins("LEFT JOIN users ON users.id = sub_logs.user_id")
		if filter.UserID != nil {
			q = q.Where("sub_logs.user_id = ?", *filter.UserID)
		}
		if filter.Since != nil {
			q = q.Where("sub_logs.accessed_at >= ?", *filter.Since)
		}
		if filter.Until != nil {
			q = q.Where("sub_logs.accessed_at <= ?", *filter.Until)
		}
		if kw := keywordLike(filter.Search); kw != "" {
			q = q.Where(
				"LOWER(sub_logs.ip) LIKE ? OR LOWER(sub_logs.ua) LIKE ? OR LOWER(sub_logs.client_type) LIKE ? OR LOWER(COALESCE(users.upn, '')) LIKE ? OR LOWER(COALESCE(users.display_name, '')) LIKE ?",
				kw, kw, kw, kw, kw)
		}
		return q
	}

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

	q := applyFilters(r.db.WithContext(ctx).Table("sub_logs")).
		Select("sub_logs.*, users.upn as user_upn, users.display_name as user_display, users.group_id as user_group_id")

	// Find first, then conditionally Count via inferTotalOrCount —
	// sub_logs is the highest-write-rate table, the COUNT-on-LIKE that
	// preceded every list was the most expensive single query in the
	// admin panel at scale. The session clone keeps q's WHERE/JOIN
	// reusable for Count without inheriting ORDER/LIMIT/OFFSET.
	var rows []subLogWithUser
	// sub_logs.id DESC breaks ties on the non-unique accessed_at so pagination
	// is stable on Postgres (equal-timestamp rows otherwise reorder per page).
	if err := applyPagination(q.Session(&gorm.Session{}), filter.Pagination, subLogSortAllowlist, "sub_logs.accessed_at").
		Order("sub_logs.id DESC").
		Find(&rows).Error; err != nil {
		return nil, 0, err
	}
	total, err := inferTotalOrCount(applyFilters(r.db.WithContext(ctx).Table("sub_logs")), filter.Pagination, len(rows))
	if err != nil {
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

