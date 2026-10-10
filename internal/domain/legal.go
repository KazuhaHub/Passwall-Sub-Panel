package domain

import (
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"golang.org/x/text/language"
)

const MaxLegalContentBytes = 60000

var ErrLegalContentTooLarge = errors.New("legal_content_too_large")
var ErrLegalVersionConflict = errors.New("legal_version_conflict")
var ErrLegalConsentOutdated = errors.New("legal_consent_outdated")

type LegalConsentStatus struct {
	Enabled        bool  `json:"enabled"`
	ConsentVersion int64 `json:"consent_version"`
	Pending        bool  `json:"pending"`
}

// LegalPublicDocument omits publisher identity and internal document IDs.
type LegalPublicDocument struct {
	Version        int64               `json:"version"`
	ConsentVersion int64               `json:"consent_version"`
	Locale         string              `json:"locale"`
	FallbackFrom   string              `json:"fallback_from,omitempty"`
	Content        string              `json:"content"`
	PublishedAt    time.Time           `json:"published_at"`
	DataCollection LegalDataCollection `json:"data_collection"`
}

// LegalDataCollection contains only public collection policy, never settings
// secrets or account identifiers. Zero retention means forever only for the
// subscription and authentication logs; risk values are runtime effective.
type LegalDataCollection struct {
	SubLogRetentionDays                 int                     `json:"sub_log_retention_days"`
	AuthEventRetentionDays              int                     `json:"auth_event_retention_days"`
	ConnectionRetentionDays             int                     `json:"connection_retention_days"`
	HWIDCaptured                        bool                    `json:"hwid_captured"`
	HWIDRetentionDays                   int                     `json:"hwid_retention_days"`
	FlagRecordRetentionDays             int                     `json:"flag_record_retention_days"`
	RiskAssessmentRefreshMinutes        int                     `json:"risk_assessment_refresh_minutes"`
	RiskReviewPurgeAfterDeletionMinutes int                     `json:"risk_review_purge_after_deletion_minutes"`
	Access                              []LegalAccessCollection `json:"access"`
}

type LegalAccessCollection struct {
	Kind          string `json:"kind"`
	Nodes         int    `json:"nodes"`
	RetentionDays int    `json:"retention_days"`
}

func ValidateLegalIdentity(kind, locale string) error {
	if kind != "terms" && kind != "privacy" {
		return fmt.Errorf("%w: legal kind", ErrValidation)
	}
	tag, err := language.Parse(locale)
	if err != nil || len(locale) == 0 || len(locale) > 16 || tag.String() != locale {
		return fmt.Errorf("%w: legal locale", ErrValidation)
	}
	return nil
}

type LegalDraft struct {
	Kind        string
	Locale      string
	Content     string
	ConsentBump bool
	PublishedBy int64
	// Optional for legacy callers; editors bind publication to the reviewed
	// latest version (zero means no publication in this kind and locale).
	ExpectedVersion *int64
}

func (d LegalDraft) Validate() error {
	if err := ValidateLegalIdentity(d.Kind, d.Locale); err != nil {
		return err
	}
	if d.ExpectedVersion != nil && *d.ExpectedVersion < 0 {
		return fmt.Errorf("%w: legal expected version", ErrValidation)
	}
	if len(d.Content) > MaxLegalContentBytes {
		return ErrLegalContentTooLarge
	}
	if !utf8.ValidString(d.Content) || strings.IndexByte(d.Content, 0) >= 0 || d.PublishedBy <= 0 {
		return fmt.Errorf("%w: legal publication", ErrValidation)
	}
	return nil
}

type LegalDocument struct {
	ID          int64     `json:"id"`
	Kind        string    `json:"kind"`
	Locale      string    `json:"locale"`
	Version     int64     `json:"version"`
	Content     string    `json:"content"`
	ConsentBump bool      `json:"consent_bump"`
	PublishedAt time.Time `json:"published_at"`
	PublishedBy int64     `json:"published_by"`
}

type LegalPublication struct {
	Document       LegalDocument `json:"document"`
	ConsentVersion int64         `json:"consent_version"`
}
