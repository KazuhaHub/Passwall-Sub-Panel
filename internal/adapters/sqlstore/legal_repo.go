package sqlstore

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strconv"
	"time"

	"github.com/go-sql-driver/mysql"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
)

type legalDocumentRow struct {
	ID          int64     `gorm:"primaryKey;autoIncrement"`
	Kind        string    `gorm:"size:16;not null;uniqueIndex:uk_legal_version,priority:1"`
	Locale      string    `gorm:"size:16;not null;uniqueIndex:uk_legal_version,priority:2"`
	Version     int64     `gorm:"not null;uniqueIndex:uk_legal_version,priority:3"`
	Content     string    `gorm:"type:text;not null"`
	ConsentBump bool      `gorm:"not null"`
	PublishedAt time.Time `gorm:"not null"`
	PublishedBy int64     `gorm:"not null"`
}

func (legalDocumentRow) TableName() string { return "legal_documents" }

type legalRepo struct {
	db         *gorm.DB
	invalidate func()
}

func (r *legalRepo) Publish(ctx context.Context, draft domain.LegalDraft) (domain.LegalPublication, error) {
	if err := draft.Validate(); err != nil {
		return domain.LegalPublication{}, err
	}
	for attempt := 0; attempt < 2; attempt++ {
		publication, err := r.publishOnce(ctx, draft)
		if err == nil {
			if r.invalidate != nil {
				r.invalidate()
			}
			return publication, nil
		}
		if !errors.Is(err, domain.ErrLegalVersionConflict) {
			return domain.LegalPublication{}, err
		}
	}
	return domain.LegalPublication{}, domain.ErrLegalVersionConflict
}

func (r *legalRepo) publishOnce(ctx context.Context, draft domain.LegalDraft) (domain.LegalPublication, error) {
	var publication domain.LegalPublication
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// One durable global row serializes publication across kinds/locales.
		// Its insert-only initialization cannot reset an existing consent version.
		state := settingRow{Type: "legal", Name: "consent_version", Value: "0", UpdatedAt: time.Now().UTC()}
		if err := tx.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "type"}, {Name: "name"}}, DoNothing: true}).Create(&state).Error; err != nil {
			return err
		}
		state = settingRow{}
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("type = ? AND name = ?", "legal", "consent_version").First(&state).Error; err != nil {
			return err
		}
		consent, err := strconv.ParseInt(state.Value, 10, 64)
		if err != nil || consent < 0 || state.Encrypted {
			return fmt.Errorf("%w: invalid stored legal consent version", domain.ErrValidation)
		}
		if consent == 0 {
			consent = 1
		} else if draft.ConsentBump {
			if consent == math.MaxInt64 {
				return fmt.Errorf("%w: legal consent version exhausted", domain.ErrValidation)
			}
			consent++
		}
		var last int64
		if err := tx.Model(&legalDocumentRow{}).Where("kind = ? AND locale = ?", draft.Kind, draft.Locale).Select("COALESCE(MAX(version), 0)").Scan(&last).Error; err != nil {
			return err
		}
		if last < 0 || last == math.MaxInt64 {
			return fmt.Errorf("%w: legal document version exhausted", domain.ErrValidation)
		}
		doc := legalDocumentRow{Kind: draft.Kind, Locale: draft.Locale, Version: last + 1, Content: draft.Content, ConsentBump: draft.ConsentBump, PublishedBy: draft.PublishedBy, PublishedAt: time.Now().UTC()}
		if err := tx.Create(&doc).Error; err != nil {
			if legalUniqueViolation(err) {
				return fmt.Errorf("%w: %w", domain.ErrLegalVersionConflict, err)
			}
			return err
		}
		if err := tx.Model(&settingRow{}).Where("type = ? AND name = ?", "legal", "consent_version").Updates(map[string]any{"value": strconv.FormatInt(consent, 10), "updated_at": doc.PublishedAt}).Error; err != nil {
			return err
		}
		publication = domain.LegalPublication{Document: doc.document(), ConsentVersion: consent}
		return nil
	})
	return publication, err
}

func legalUniqueViolation(err error) bool {
	if errors.Is(err, gorm.ErrDuplicatedKey) {
		return true
	}
	var mysqlErr *mysql.MySQLError
	if errors.As(err, &mysqlErr) {
		return mysqlErr.Number == 1062
	}
	var sqlState interface{ SQLState() string }
	if errors.As(err, &sqlState) {
		return sqlState.SQLState() == "23505"
	}
	var sqliteCode interface{ Code() int }
	return errors.As(err, &sqliteCode) && sqliteCode.Code() == 2067
}

func (r *legalRepo) Latest(ctx context.Context, kind, locale string) (domain.LegalDocument, error) {
	if err := domain.ValidateLegalIdentity(kind, locale); err != nil {
		return domain.LegalDocument{}, err
	}
	var row legalDocumentRow
	err := r.db.WithContext(ctx).Where("kind = ? AND locale = ?", kind, locale).Order("version DESC").First(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return domain.LegalDocument{}, domain.ErrNotFound
	}
	return row.document(), err
}

func (r *legalRepo) History(ctx context.Context, kind, locale string, before int64, limit int) ([]domain.LegalDocument, error) {
	if err := domain.ValidateLegalIdentity(kind, locale); err != nil {
		return nil, err
	}
	if before < 0 || limit < 1 || limit > 50 {
		return nil, fmt.Errorf("%w: legal history bounds", domain.ErrValidation)
	}
	q := r.db.WithContext(ctx).Where("kind = ? AND locale = ?", kind, locale)
	if before > 0 {
		q = q.Where("version < ?", before)
	}
	var rows []legalDocumentRow
	if err := q.Order("version DESC").Limit(limit).Find(&rows).Error; err != nil {
		return nil, err
	}
	out := make([]domain.LegalDocument, len(rows))
	for i, row := range rows {
		out[i] = row.document()
	}
	return out, nil
}

func (r legalDocumentRow) document() domain.LegalDocument {
	return domain.LegalDocument{ID: r.ID, Kind: r.Kind, Locale: r.Locale, Version: r.Version, Content: r.Content, ConsentBump: r.ConsentBump, PublishedBy: r.PublishedBy, PublishedAt: r.PublishedAt}
}
