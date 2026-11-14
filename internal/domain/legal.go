package domain

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

// LegalKind is one of the two documents the panel can publish.
type LegalKind string

const (
	LegalKindTerms   LegalKind = "terms"
	LegalKindPrivacy LegalKind = "privacy"
)

// ParseLegalKind accepts exactly the two stored spellings.
func ParseLegalKind(s string) (LegalKind, bool) {
	switch LegalKind(s) {
	case LegalKindTerms, LegalKindPrivacy:
		return LegalKind(s), true
	}
	return "", false
}

// LegalContentMaxBytes bounds a document. MySQL's TEXT holds 64 KiB, so the
// limit is in bytes (a CJK character is three) and stays under it.
const LegalContentMaxBytes = 60000

// How a consent was given. The record carries nothing else about the act: no
// address, no user agent.
const (
	LegalConsentRegister = "register"
	LegalConsentPrompt   = "prompt"
)

var (
	// ErrLegalContentTooLarge is a validation failure (HTTP 400,
	// legal_content_too_large).
	ErrLegalContentTooLarge = fmt.Errorf("%w: legal content exceeds %d bytes", ErrValidation, LegalContentMaxBytes)
	// ErrLegalConsentOutdated says the consent version a caller offers is not
	// the current one (HTTP 409, legal_consent_outdated): the page it was shown
	// is stale and must be reloaded.
	ErrLegalConsentOutdated = errors.New("legal consent version is outdated")
)

// ValidateLegalContent checks a document's text.
func ValidateLegalContent(content string) error {
	if strings.TrimSpace(content) == "" {
		return fmt.Errorf("%w: legal content is empty", ErrValidation)
	}
	if len(content) > LegalContentMaxBytes {
		return ErrLegalContentTooLarge
	}
	return nil
}

// LegalDocument is one immutable published version of a document in one
// locale. Editing is publishing the next version.
type LegalDocument struct {
	ID          int64
	Kind        LegalKind
	Locale      string
	Version     int
	Content     string
	ConsentBump bool
	PublishedAt time.Time
	// PublishedBy is the publishing account's id (0: unknown).
	PublishedBy int64
}

// LegalDocumentMeta is a version without its text, for lists.
type LegalDocumentMeta struct {
	ID          int64
	Kind        LegalKind
	Locale      string
	Version     int
	ConsentBump bool
	PublishedAt time.Time
	PublishedBy int64
}

// LegalConsent is one account's acceptance of a consent version.
type LegalConsent struct {
	UserID         int64
	ConsentVersion int
	AcceptedAt     time.Time
	Method         string
}

// NormalizeLegalLocale canonicalises a locale tag (language lower-case, a
// two-letter region upper-case) and reports whether it is well formed: 2-3
// letters, then up to two more subtags of 2-8 alphanumerics, 16 characters at
// most.
func NormalizeLegalLocale(s string) (string, bool) {
	s = strings.TrimSpace(s)
	if len(s) < 2 || len(s) > 16 {
		return "", false
	}
	parts := strings.Split(s, "-")
	if len(parts) > 3 {
		return "", false
	}
	for i, p := range parts {
		min, max := 2, 8
		if i == 0 {
			max = 3
		}
		if len(p) < min || len(p) > max {
			return "", false
		}
		for _, r := range p {
			isAlpha := (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z')
			if !isAlpha && !(i > 0 && r >= '0' && r <= '9') {
				return "", false
			}
		}
	}
	parts[0] = strings.ToLower(parts[0])
	if len(parts) > 1 && len(parts[1]) == 2 {
		parts[1] = strings.ToUpper(parts[1])
	}
	return strings.Join(parts, "-"), true
}

// LegalLocaleChain is the order in which locales are tried for a request:
// the requested one, zh-TW falls back to zh-CN, then en-US, then zh-CN. A
// request that is empty or not a locale starts at en-US.
func LegalLocaleChain(lang string) []string {
	var chain []string
	add := func(l string) {
		for _, have := range chain {
			if have == l {
				return
			}
		}
		chain = append(chain, l)
	}
	if l, ok := NormalizeLegalLocale(lang); ok {
		add(l)
		if l == "zh-TW" {
			add("zh-CN")
		}
	}
	add("en-US")
	add("zh-CN")
	return chain
}
