package sqlstore

import (
	"context"
	"strings"
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
	if kw := keywordLike(filter.Search); kw != "" {
		q = q.Where(likeCols("actor", "action", "target"), kw, kw, kw)
	}
	if filter.Since != nil {
		q = q.Where("at >= ?", *filter.Since)
	}
	if filter.Until != nil {
		q = q.Where("at <= ?", *filter.Until)
	}
	// The rows this caller may not read (the audit read's admin-only
	// targets, for anyone but an administrator). Part of q, so the Count
	// below leaves them out too and the pager never promises rows it
	// cannot show. Escaped like a keyword and lowered like likeCols, so
	// the prefix is literal and the same rows drop out on every dialect;
	// COALESCE because target is nullable and NOT LIKE on NULL is NULL,
	// which would hide a row nobody asked to hide.
	for _, prefix := range filter.ExcludeTargetPrefixes {
		q = q.Where("LOWER(COALESCE(target, '')) NOT LIKE ? ESCAPE '"+likeEscapeChar+"'",
			likeEscaper.Replace(strings.ToLower(prefix))+"%")
	}

	// `at` is non-unique (admin actions can land within the same ms on a
	// busy panel), so applyPagination's single-column ORDER BY would let
	// rows shift between pages on equal-key bursts. Pre-pin "at, id" as
	// the user's primary intent, then apply pagination on top.
	if filter.SortBy == "" {
		filter.SortBy = "at"
	}
	if filter.SortDir == "" {
		filter.SortDir = "desc"
	}

	// Find first, then conditionally Count. The session clone lets us
	// reuse q's WHERE clauses for the Count without inheriting the
	// ORDER/LIMIT/OFFSET applyPagination just appended.
	pagedQ := applyPagination(q.Session(&gorm.Session{}), filter.Pagination, auditSortAllowlist, "at").Order("id DESC")
	var rows []auditRow
	if err := pagedQ.Find(&rows).Error; err != nil {
		return nil, 0, err
	}
	total, err := inferTotalOrCount(q, filter.Pagination, len(rows))
	if err != nil {
		return nil, 0, err
	}
	out := make([]*domain.AuditEntry, len(rows))
	for i := range rows {
		out[i] = rows[i].toDomain()
	}
	return out, total, nil
}

var auditSortAllowlist = map[string]string{
	"at":     "at",
	"id":     "id",
	"actor":  "actor",
	"action": "action",
}

func (r *auditRepo) Clear(ctx context.Context) error {
	return r.db.WithContext(ctx).Where("1 = 1").Delete(&auditRow{}).Error
}

func (r *auditRepo) DeleteBefore(ctx context.Context, cutoff time.Time) (int64, error) {
	res := r.db.WithContext(ctx).Where("at < ?", cutoff).Delete(&auditRow{})
	return res.RowsAffected, res.Error
}
