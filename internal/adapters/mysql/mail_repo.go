package mysql

import (
	"context"
	"errors"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
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
	out := row.toDomain()
	if out.SMTPPort <= 0 {
		out.SMTPPort = defaults.SMTPPort
	}
	if out.Encryption == "" {
		out.Encryption = defaults.Encryption
	}
	if out.ExpireBeforeDays <= 0 {
		out.ExpireBeforeDays = defaults.ExpireBeforeDays
	}
	if out.TrafficRemainPercent <= 0 {
		out.TrafficRemainPercent = defaults.TrafficRemainPercent
	}
	return out, nil
}

func (r *mailRepo) SaveSettings(ctx context.Context, s domain.MailSettings) error {
	return r.db.WithContext(ctx).Clauses(clause.OnConflict{UpdateAll: true}).Create(mailSettingsFromDomain(s)).Error
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
