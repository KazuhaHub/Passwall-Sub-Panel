package sqlstore

import (
	"context"
	"fmt"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
)

// Privacy and terms.
//
// legal_documents holds one IMMUTABLE row per published version of a document
// (kind) in a locale: editing is publishing the next version, never an UPDATE.
// The version counts per (kind, locale), and the unique index is what makes two
// publishers racing for the same number safe: the loser retries once.
//
// legal_consents is one row per (account, consent version) the account
// accepted. It deliberately has no address and no user agent column
// (TestLegalTables_AreInTheSchema): consent is a fact about an account and a
// version, and nothing about where it was given from. There is no foreign key,
// like the risk tables: a user delete must never depend on this table, and the
// hourly cleanup removes the rows of deleted accounts (PurgeConsentOrphans).
//
// content is text, NOT NULL, no default (MySQL refuses a DEFAULT on TEXT), and
// bounded in BYTES (domain.LegalContentMaxBytes) so it fits MySQL's 64 KiB.

type legalDocumentRow struct {
	ID          int64  `gorm:"primaryKey;autoIncrement"`
	Kind        string `gorm:"size:16;not null;uniqueIndex:uk_legal_document_version,priority:1"`
	Locale      string `gorm:"size:16;not null;uniqueIndex:uk_legal_document_version,priority:2"`
	Version     int    `gorm:"not null;uniqueIndex:uk_legal_document_version,priority:3"`
	Content     string `gorm:"type:text;not null"`
	ConsentBump bool   `gorm:"not null;default:false"`
	PublishedAt time.Time
	PublishedBy int64 `gorm:"not null;default:0"`
}

func (legalDocumentRow) TableName() string { return "legal_documents" }

func (r legalDocumentRow) toDomain() *domain.LegalDocument {
	return &domain.LegalDocument{
		ID: r.ID, Kind: domain.LegalKind(r.Kind), Locale: r.Locale, Version: r.Version, Content: r.Content,
		ConsentBump: r.ConsentBump, PublishedAt: r.PublishedAt.UTC(), PublishedBy: r.PublishedBy,
	}
}

type legalConsentRow struct {
	UserID         int64     `gorm:"column:user_id;primaryKey;autoIncrement:false"`
	ConsentVersion int       `gorm:"column:consent_version;primaryKey;autoIncrement:false"`
	AcceptedAt     time.Time `gorm:"column:accepted_at;not null"`
	Method         string    `gorm:"column:method;size:16;not null"`
}

func (legalConsentRow) TableName() string { return "legal_consents" }

// LegalRepo is the legal_documents and legal_consents store.
type LegalRepo struct {
	db *gorm.DB
	// beforeInsert is a test seam: it runs after the next version was read and
	// before the row is inserted, which is where a competing publisher lands.
	beforeInsert func(kind, locale string, version int)
}

// NewLegalRepo builds the store over db.
func NewLegalRepo(db *gorm.DB) *LegalRepo { return &LegalRepo{db: db} }

// Publish stores d as the next version of its (kind, locale) and returns it.
// d.ID, d.Version and a zero d.PublishedAt are filled in. A collision on the
// version number is retried once; a second one is domain.ErrConflict.
func (r *LegalRepo) Publish(ctx context.Context, d domain.LegalDocument) (*domain.LegalDocument, error) {
	if _, ok := domain.ParseLegalKind(string(d.Kind)); !ok {
		return nil, fmt.Errorf("%w: unknown legal document kind", domain.ErrValidation)
	}
	locale, ok := domain.NormalizeLegalLocale(d.Locale)
	if !ok {
		return nil, fmt.Errorf("%w: locale is not a valid language tag", domain.ErrValidation)
	}
	if err := domain.ValidateLegalContent(d.Content); err != nil {
		return nil, err
	}
	at := d.PublishedAt
	if at.IsZero() {
		at = time.Now()
	}
	for attempt := 0; attempt < 2; attempt++ {
		var next int
		if err := r.db.WithContext(ctx).Model(&legalDocumentRow{}).
			Select("COALESCE(MAX(version), 0) + 1").
			Where("kind = ? AND locale = ?", string(d.Kind), locale).Scan(&next).Error; err != nil {
			return nil, fmt.Errorf("next legal version: %w", err)
		}
		if r.beforeInsert != nil {
			r.beforeInsert(string(d.Kind), locale, next)
		}
		row := legalDocumentRow{
			Kind: string(d.Kind), Locale: locale, Version: next, Content: d.Content,
			ConsentBump: d.ConsentBump, PublishedAt: at.UTC(), PublishedBy: d.PublishedBy,
		}
		err := r.db.WithContext(ctx).Create(&row).Error
		if err == nil {
			return row.toDomain(), nil
		}
		if !isUniqueViolationErr(err) {
			return nil, fmt.Errorf("publish legal document: %w", err)
		}
	}
	return nil, fmt.Errorf("%w: another publication took the version number twice", domain.ErrConflict)
}

// Latest is the highest version of a (kind, locale), domain.ErrNotFound when
// there is none.
func (r *LegalRepo) Latest(ctx context.Context, kind domain.LegalKind, locale string) (*domain.LegalDocument, error) {
	var row legalDocumentRow
	err := r.db.WithContext(ctx).Where("kind = ? AND locale = ?", string(kind), locale).Order("version DESC").Take(&row).Error
	if err != nil {
		return nil, wrapNotFound(err)
	}
	return row.toDomain(), nil
}

// LatestPerLocale is the newest version of a kind in every locale that has
// one, by locale.
func (r *LegalRepo) LatestPerLocale(ctx context.Context, kind domain.LegalKind) ([]*domain.LegalDocument, error) {
	var heads []struct {
		Locale string
		V      int
	}
	if err := r.db.WithContext(ctx).Model(&legalDocumentRow{}).Select("locale, MAX(version) AS v").
		Where("kind = ?", string(kind)).Group("locale").Order("locale").Scan(&heads).Error; err != nil {
		return nil, fmt.Errorf("legal document heads: %w", err)
	}
	out := make([]*domain.LegalDocument, 0, len(heads))
	for _, h := range heads {
		var row legalDocumentRow
		if err := r.db.WithContext(ctx).Where("kind = ? AND locale = ? AND version = ?", string(kind), h.Locale, h.V).Take(&row).Error; err != nil {
			return nil, fmt.Errorf("legal document %s/%s: %w", kind, h.Locale, err)
		}
		out = append(out, row.toDomain())
	}
	return out, nil
}

// History lists every version of a kind without its text: newest first, by
// locale.
func (r *LegalRepo) History(ctx context.Context, kind domain.LegalKind) ([]domain.LegalDocumentMeta, error) {
	var rows []legalDocumentRow
	if err := r.db.WithContext(ctx).
		Select("id", "kind", "locale", "version", "consent_bump", "published_at", "published_by").
		Where("kind = ?", string(kind)).Order("locale ASC, version DESC").Find(&rows).Error; err != nil {
		return nil, fmt.Errorf("legal history: %w", err)
	}
	out := make([]domain.LegalDocumentMeta, len(rows))
	for i, row := range rows {
		out[i] = domain.LegalDocumentMeta{ID: row.ID, Kind: domain.LegalKind(row.Kind), Locale: row.Locale, Version: row.Version,
			ConsentBump: row.ConsentBump, PublishedAt: row.PublishedAt.UTC(), PublishedBy: row.PublishedBy}
	}
	return out, nil
}

// Delete removes one version by id. It exists to undo a publication whose
// consent version could not be saved; published documents are otherwise never
// deleted.
func (r *LegalRepo) Delete(ctx context.Context, id int64) error {
	return r.db.WithContext(ctx).Where("id = ?", id).Delete(&legalDocumentRow{}).Error
}

// RecordConsent stores that an account accepted a consent version. Accepting
// twice is not an error and keeps the first record.
func (r *LegalRepo) RecordConsent(ctx context.Context, c domain.LegalConsent) error {
	if c.Method != domain.LegalConsentRegister && c.Method != domain.LegalConsentPrompt {
		return fmt.Errorf("%w: unknown consent method", domain.ErrValidation)
	}
	if c.ConsentVersion < 1 || c.UserID < 1 {
		return fmt.Errorf("%w: consent needs an account and a version of at least 1", domain.ErrValidation)
	}
	at := c.AcceptedAt
	if at.IsZero() {
		at = time.Now()
	}
	return r.db.WithContext(ctx).Clauses(clause.OnConflict{DoNothing: true}).Create(&legalConsentRow{
		UserID: c.UserID, ConsentVersion: c.ConsentVersion, AcceptedAt: at.UTC(), Method: c.Method,
	}).Error
}

// HasConsentAtLeast reports whether the account accepted version or a later
// one.
func (r *LegalRepo) HasConsentAtLeast(ctx context.Context, userID int64, version int) (bool, error) {
	var n int64
	if err := r.db.WithContext(ctx).Model(&legalConsentRow{}).
		Where("user_id = ? AND consent_version >= ?", userID, version).Limit(1).Count(&n).Error; err != nil {
		return false, fmt.Errorf("legal consent lookup: %w", err)
	}
	return n > 0, nil
}

// PurgeConsentOrphans deletes the consent records of accounts that no longer
// exist and returns how many. The hourly cleanup runs it. Portable as written.
func (r *LegalRepo) PurgeConsentOrphans(ctx context.Context) (int64, error) {
	res := r.db.WithContext(ctx).Exec("DELETE FROM legal_consents WHERE user_id NOT IN (SELECT id FROM users)")
	if res.Error != nil {
		return 0, fmt.Errorf("purge legal consent orphans: %w", res.Error)
	}
	return res.RowsAffected, nil
}

// CountAffectedUsers counts the accounts with role user (the only ones asked
// to consent) and how many of them already accepted version or a later one.
func (r *LegalRepo) CountAffectedUsers(ctx context.Context, version int) (affected, accepted int64, err error) {
	if err = r.db.WithContext(ctx).Table("users").Where("role = ?", string(domain.RoleUser)).Count(&affected).Error; err != nil {
		return 0, 0, fmt.Errorf("count users: %w", err)
	}
	if version < 1 {
		return affected, 0, nil
	}
	err = r.db.WithContext(ctx).Raw(
		"SELECT COUNT(*) FROM users WHERE role = ? AND EXISTS (SELECT 1 FROM legal_consents c WHERE c.user_id = users.id AND c.consent_version >= ?)",
		string(domain.RoleUser), version).Scan(&accepted).Error
	if err != nil {
		return 0, 0, fmt.Errorf("count accepted users: %w", err)
	}
	return affected, accepted, nil
}
