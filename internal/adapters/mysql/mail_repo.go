package mysql

import (
	"context"
	"errors"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

type mailRepo struct{ db *gorm.DB }

func (r *mailRepo) LoadSettings(ctx context.Context, defaults domain.MailSettings) (domain.MailSettings, error) {
	var row mailSettingsRow
	if err := r.db.WithContext(ctx).First(&row, 1).Error; err != nil {
		err = wrapNotFound(err)
		if errors.Is(err, domain.ErrNotFound) {
			return defaults, nil
		}
		return defaults, err
	}
	out, err := row.toDomain()
	if err != nil {
		return defaults, err
	}
	if out.SMTPPort <= 0 {
		out.SMTPPort = defaults.SMTPPort
	}
	if out.Encryption == "" {
		out.Encryption = defaults.Encryption
	}
	return out, nil
}

func (r *mailRepo) SaveSettings(ctx context.Context, s domain.MailSettings) error {
	row, err := mailSettingsFromDomain(s)
	if err != nil {
		return err
	}
	return r.db.WithContext(ctx).Clauses(clause.OnConflict{UpdateAll: true}).Create(row).Error
}

func (r *mailRepo) ListTemplates(ctx context.Context) ([]*domain.MailTemplate, error) {
	var rows []mailTemplateRow
	if err := r.db.WithContext(ctx).Order("kind ASC").Find(&rows).Error; err != nil {
		return nil, err
	}
	out := make([]*domain.MailTemplate, len(rows))
	for i := range rows {
		out[i] = rows[i].toDomain()
	}
	return out, nil
}

func (r *mailRepo) GetTemplate(ctx context.Context, kind domain.MailReminderKind) (*domain.MailTemplate, error) {
	var row mailTemplateRow
	if err := r.db.WithContext(ctx).First(&row, "kind = ?", string(kind)).Error; err != nil {
		return nil, wrapNotFound(err)
	}
	return row.toDomain(), nil
}

func (r *mailRepo) SaveTemplate(ctx context.Context, t *domain.MailTemplate) error {
	return r.db.WithContext(ctx).Clauses(clause.OnConflict{UpdateAll: true}).Create(mailTemplateFromDomain(t)).Error
}

func (r *mailRepo) HasSent(ctx context.Context, userID int64, kind domain.MailReminderKind, windowKey string) (bool, error) {
	var count int64
	err := r.db.WithContext(ctx).Model(&mailSentRow{}).
		Where("user_id = ? AND kind = ? AND window_key = ?", userID, string(kind), windowKey).
		Count(&count).Error
	return count > 0, err
}

func (r *mailRepo) RecordSent(ctx context.Context, userID int64, kind domain.MailReminderKind, windowKey, toEmail string) error {
	row := &mailSentRow{
		UserID:    userID,
		Kind:      string(kind),
		WindowKey: windowKey,
		ToEmail:   toEmail,
		SentAt:    time.Now(),
	}
	return r.db.WithContext(ctx).Clauses(clause.OnConflict{DoNothing: true}).Create(row).Error
}

func (r *mailRepo) ReserveSentSlot(ctx context.Context, userID int64, kind domain.MailReminderKind, windowKey, toEmail string) (bool, error) {
	row := &mailSentRow{
		UserID:    userID,
		Kind:      string(kind),
		WindowKey: windowKey,
		ToEmail:   toEmail,
		SentAt:    time.Now(),
	}
	// OnConflict DoNothing: RowsAffected is 1 when we inserted, 0 when the
	// (user_id, kind, window_key) row already existed (uk_mail_once).
	res := r.db.WithContext(ctx).Clauses(clause.OnConflict{DoNothing: true}).Create(row)
	if res.Error != nil {
		return false, res.Error
	}
	return res.RowsAffected > 0, nil
}

func (r *mailRepo) CountSentInWindow(ctx context.Context, userID int64, kind domain.MailReminderKind, windowKeyPrefix string) (int64, error) {
	var count int64
	err := r.db.WithContext(ctx).Model(&mailSentRow{}).
		Where("user_id = ? AND kind = ? AND window_key LIKE ?", userID, string(kind), windowKeyPrefix+"%").
		Count(&count).Error
	return count, err
}

// ListSent paginates over mail_sent joined with users so admin's Logs →
// Email tab can show "who got what kind of reminder". Same shape as
// subLogRepo.List — filter on user_id / since / until, ORDER BY sent_at
// DESC, pre-counted total for pagination.
func (r *mailRepo) ListSent(ctx context.Context, filter ports.EmailLogFilter) ([]*domain.EmailLog, int64, error) {
	if filter.PageSize <= 0 {
		filter.PageSize = 50
	}
	if filter.Page < 1 {
		filter.Page = 1
	}

	// applyFilters constrains a mail_sent query, joined to users so search can
	// also hit upn / display_name. Reused for count + page query.
	applyFilters := func(q *gorm.DB) *gorm.DB {
		q = q.Joins("LEFT JOIN users ON users.id = mail_sent.user_id")
		if filter.UserID != nil {
			q = q.Where("mail_sent.user_id = ?", *filter.UserID)
		}
		if filter.Since != nil {
			q = q.Where("mail_sent.sent_at >= ?", *filter.Since)
		}
		if filter.Until != nil {
			q = q.Where("mail_sent.sent_at <= ?", *filter.Until)
		}
		if kw := keywordLike(filter.Search); kw != "" {
			q = q.Where(
				likeCols("mail_sent.to_email", "mail_sent.kind", "COALESCE(users.upn, '')", "COALESCE(users.display_name, '')"),
				kw, kw, kw, kw)
		}
		return q
	}

	type mailSentWithUser struct {
		ID          int64
		UserID      int64
		Kind        string
		WindowKey   string
		ToEmail     string
		SentAt      time.Time
		UserUPN     string
		UserDisplay string
	}

	q := applyFilters(r.db.WithContext(ctx).Table("mail_sent")).
		Select("mail_sent.*, users.upn as user_upn, users.display_name as user_display")

	var rows []mailSentWithUser
	if filter.SortDir == "" {
		filter.SortDir = "desc"
	}
	// mail_sent.id DESC breaks ties on the non-unique sent_at so pagination is
	// stable on Postgres (mails sent in one pass share a near-identical sent_at).
	if err := applyPagination(q.Session(&gorm.Session{}), filter.Pagination, mailSentSortAllowlist, "mail_sent.sent_at").
		Order("mail_sent.id DESC").
		Find(&rows).Error; err != nil {
		return nil, 0, err
	}
	total, err := inferTotalOrCount(applyFilters(r.db.WithContext(ctx).Table("mail_sent")), filter.Pagination, len(rows))
	if err != nil {
		return nil, 0, err
	}

	out := make([]*domain.EmailLog, len(rows))
	for i, row := range rows {
		out[i] = &domain.EmailLog{
			ID:          row.ID,
			UserID:      row.UserID,
			UserUPN:     row.UserUPN,
			UserDisplay: row.UserDisplay,
			ToEmail:     row.ToEmail,
			Kind:        domain.MailReminderKind(row.Kind),
			WindowKey:   row.WindowKey,
			SentAt:      row.SentAt,
		}
	}
	return out, total, nil
}

var mailSentSortAllowlist = map[string]string{
	"sent_at":  "mail_sent.sent_at",
	"id":       "mail_sent.id",
	"kind":     "mail_sent.kind",
	"to_email": "mail_sent.to_email",
}

func (r *mailRepo) ClearSent(ctx context.Context) error {
	return r.db.WithContext(ctx).Where("1 = 1").Delete(&mailSentRow{}).Error
}

// DeleteSentBefore prunes mail_sent rows older than cutoff. Mirrors
// subLogRepo.DeleteBefore — driven by the MailSentRetentionDays setting,
// runs in the same hourly maintenance loop as the other retention crons.
func (r *mailRepo) DeleteSentBefore(ctx context.Context, cutoff time.Time) (int64, error) {
	result := r.db.WithContext(ctx).Where("sent_at < ?", cutoff).Delete(&mailSentRow{})
	return result.RowsAffected, result.Error
}
