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
}

func (d LegalDraft) Validate() error {
	if err := ValidateLegalIdentity(d.Kind, d.Locale); err != nil {
		return err
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
