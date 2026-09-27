package sqlstore

import (
	"context"
	"fmt"
	"sort"
	"time"

	"gorm.io/gorm"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

type subLogRepo struct{ db *gorm.DB }

func (r *subLogRepo) Insert(ctx context.Context, l *domain.SubLog) error {
	row := subLogRow{
		UserID:      l.UserID,
		IP:          l.IP,
		UA:          l.UA,
		ClientType:  l.ClientType,
		AccessedAt:  l.AccessedAt,
		DeviceID:    l.DeviceID,
		DeviceLabel: l.DeviceLabel,
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
				likeCols("sub_logs.ip", "sub_logs.ua", "sub_logs.client_type", "COALESCE(users.upn, '')", "COALESCE(users.display_name, '')"),
				kw, kw, kw, kw, kw)
		}
		return q
	}

	// The device columns ride along on sub_logs.*; search deliberately does
	// not cover them (a digest and an admin-only label are not search terms
	// an operator may probe).
	type subLogWithUser struct {
		ID          int64
		UserID      int64
		IP          string
		UA          string
		ClientType  string
		AccessedAt  time.Time
		DeviceID    string
		DeviceLabel string
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
			DeviceID:    row.DeviceID,
			DeviceLabel: row.DeviceLabel,
		}
	}
	return out, total, nil
}

const (
	// subLogScanBatch is ScanSince's default batch: a week of fetches can be
	// hundreds of thousands of rows, and a batch this size bounds what one
	// pass holds in memory to a few megabytes.
	subLogScanBatch = 5000
	// subLogScanSlack widens ScanSince's SQL lower bound so the database can
	// only over-select. SQLite compares times as zone-bearing strings, and
	// sub_logs rows carry the writer's zone (time.Now() in the handler), so
	// between a row and a bound in arbitrary zones the string order can be
	// off by up to 26 hours (UTC+14 against UTC-12). Against a UTC bound a
	// row's wall clock reads at most 12 hours earlier than its instant, so a
	// bound 24 hours early never drops a row of the window; the exact cut is
	// then made in Go on real instants. MySQL and Postgres compare true
	// datetimes and merely read up to a day of extra rows.
	subLogScanSlack = 24 * time.Hour
)

// ScanSince streams the fetch window in primary-key batches (GORM's
// FindInBatches: ORDER BY id, then WHERE id > last). Each batch query has
// finished and released its connection before fn runs, so fn may itself use
// the database even on SQLite's single connection. The output slice is reused
// across batches, which is why fn must not retain it.
func (r *subLogRepo) ScanSince(ctx context.Context, since time.Time, batch int, fn func([]domain.SubLog) error) error {
	if batch <= 0 {
		batch = subLogScanBatch
	}
	var rows []subLogRow
	out := make([]domain.SubLog, 0, batch)
	return r.db.WithContext(ctx).Model(&subLogRow{}).
		Where("accessed_at >= ?", since.UTC().Add(-subLogScanSlack)).
		FindInBatches(&rows, batch, func(*gorm.DB, int) error {
			out = out[:0]
			for i := range rows {
				// The exact cut the SQL bound cannot make (see subLogScanSlack).
				if rows[i].AccessedAt.Before(since) {
					continue
				}
				out = append(out, rows[i].toDomain())
			}
			// A batch wholly inside the slack is not a batch of the window.
			if len(out) == 0 {
				return nil
			}
			return fn(out)
		}).Error
}

// RecentForUsers is the risk center's device-inference read: one page of
// accounts' recent fetches, newest first.
//
// The SQL is bounded like ScanSince's, subLogScanSlack early, and the exact
// cut is made here on real instants, for ScanSince's reason: SQLite compares
// the zone-bearing strings the rows were written as. For the same reason the
// database is not asked to order by accessed_at — across two offsets (a
// daylight-saving change, a moved process zone) the string order is not the
// time order — but by id, which is insert order: LIMIT then keeps the most
// recently WRITTEN rows, the ones a live connection's fetches are, and the
// order returned is by instant, sorted here (the id breaks a tie). A row
// inside the slack can take a slot of the limit; it is among the oldest
// written, so it is the first to go when the limit bites.
//
// The account list is bounded because every caller is one page of an admin
// list: more is a bug upstream, refused before it becomes a scan of the
// fleet's fetches.
func (r *subLogRepo) RecentForUsers(ctx context.Context, userIDs []int64, since time.Time, limit int) ([]domain.SubLog, error) {
	if len(userIDs) > ports.SubLogRecentMaxUsers {
		return nil, fmt.Errorf("%w: at most %d accounts per recent-fetch read, got %d",
			domain.ErrValidation, ports.SubLogRecentMaxUsers, len(userIDs))
	}
	if len(userIDs) == 0 {
		return nil, nil
	}
	if limit <= 0 || limit > ports.SubLogRecentMaxRows {
		limit = ports.SubLogRecentMaxRows
	}
	var rows []subLogRow
	if err := r.db.WithContext(ctx).Model(&subLogRow{}).
		Where("user_id IN ? AND accessed_at >= ?", userIDs, since.UTC().Add(-subLogScanSlack)).
		Order("id DESC").Limit(limit).
		Find(&rows).Error; err != nil {
		return nil, fmt.Errorf("recent fetches: %w", err)
	}
	out := make([]domain.SubLog, 0, len(rows))
	for i := range rows {
		if rows[i].AccessedAt.Before(since) {
			continue
		}
		out = append(out, rows[i].toDomain())
	}
	sort.SliceStable(out, func(i, j int) bool {
		if !out[i].AccessedAt.Equal(out[j].AccessedAt) {
			return out[i].AccessedAt.After(out[j].AccessedAt)
		}
		return out[i].ID > out[j].ID
	})
	return out, nil
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
