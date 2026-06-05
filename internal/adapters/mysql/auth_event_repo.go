package mysql

import (
	"context"
	"time"

	"gorm.io/gorm"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

// authEventRepo backs the first-class authentication-event log. Mirrors
// auditRepo's shape (Insert / filtered List / DeleteBefore-for-retention).
type authEventRepo struct{ db *gorm.DB }

func (r *authEventRepo) Insert(ctx context.Context, e *domain.AuthEvent) error {
	row := authEventRow{
		UserID:  e.UserID,
		UPN:     e.UPN,
		Method:  string(e.Method),
		Outcome: string(e.Outcome),
		Reason:  e.Reason,
		IP:      e.IP,
		UA:      e.UA,
		At:      e.At,
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

func (r *authEventRepo) List(ctx context.Context, filter ports.AuthEventFilter) ([]*domain.AuthEvent, int64, error) {
	q := r.db.WithContext(ctx).Model(&authEventRow{})
	if filter.UserID != nil {
		q = q.Where("user_id = ?", *filter.UserID)
	}
	if filter.Method != "" {
		q = q.Where("method = ?", filter.Method)
	}
	if filter.Outcome != "" {
		q = q.Where("outcome = ?", filter.Outcome)
	}
	if kw := keywordLike(filter.Search); kw != "" {
		q = q.Where(likeCols("upn", "ip", "ua", "reason"), kw, kw, kw, kw)
	}
	if filter.Since != nil {
		q = q.Where("at >= ?", *filter.Since)
	}
	if filter.Until != nil {
		q = q.Where("at <= ?", *filter.Until)
	}
	// `at` is non-unique under bursts, so pin "at, id" (see auditRepo.List).
	if filter.SortBy == "" {
		filter.SortBy = "at"
	}
	if filter.SortDir == "" {
		filter.SortDir = "desc"
	}
	pagedQ := applyPagination(q.Session(&gorm.Session{}), filter.Pagination, authEventSortAllowlist, "at").Order("id DESC")
	var rows []authEventRow
	if err := pagedQ.Find(&rows).Error; err != nil {
		return nil, 0, err
	}
	total, err := inferTotalOrCount(q, filter.Pagination, len(rows))
	if err != nil {
		return nil, 0, err
	}
	out := make([]*domain.AuthEvent, len(rows))
	for i := range rows {
		out[i] = rows[i].toDomain()
	}
	return out, total, nil
}

var authEventSortAllowlist = map[string]string{
	"at": "at",
	"id": "id",
}

func (r *authEventRepo) DeleteBefore(ctx context.Context, cutoff time.Time) (int64, error) {
	res := r.db.WithContext(ctx).Where("at < ?", cutoff).Delete(&authEventRow{})
	return res.RowsAffected, res.Error
}

// RecentAuthFailures backs the login guard (captcha + account lockout). It
// counts only genuine credential failures (outcome=failure AND
// reason=invalid_credentials) for the given scope since `since`. Counting that
// one reason — not every failure — is deliberate: locked_out rejections,
// disabled-account attempts and server errors must not feed the count, or a
// locked-out source's continued retries would keep advancing the lock window
// and the lock would never expire.
func (r *authEventRepo) RecentAuthFailures(ctx context.Context, ip, upn string, since time.Time) (int64, time.Time, error) {
	q := r.db.WithContext(ctx).Model(&authEventRow{}).
		Where("outcome = ?", string(domain.AuthOutcomeFailure)).
		Where("reason = ?", domain.AuthReasonInvalidCredentials).
		Where("at >= ?", since)
	if ip != "" {
		q = q.Where("ip = ?", ip)
	}
	if upn != "" {
		q = q.Where("upn = ?", upn)
	}
	var count int64
	if err := q.Count(&count).Error; err != nil {
		return 0, time.Time{}, err
	}
	if count == 0 {
		return 0, time.Time{}, nil
	}
	// Read the newest matching row for its timestamp. MAX(at) scanned directly
	// can't go straight into time.Time under the pure-Go SQLite driver (it
	// hands back a string), so let GORM map the row's `at` column instead.
	// Fresh session so the Count above doesn't leak into this statement.
	var newest authEventRow
	if err := q.Session(&gorm.Session{}).Order("at DESC").Limit(1).Find(&newest).Error; err != nil {
		return count, time.Time{}, err
	}
	return count, newest.At, nil
}

// CountByReasonSince counts auth events with the given reason at or after
// `since`. Used by the notification center's login_security alert.
func (r *authEventRepo) CountByReasonSince(ctx context.Context, reason string, since time.Time) (int64, error) {
	var count int64
	err := r.db.WithContext(ctx).Model(&authEventRow{}).
		Where("reason = ?", reason).
		Where("at >= ?", since).
		Count(&count).Error
	return count, err
}
