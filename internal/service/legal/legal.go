// Package legal serves the published terms and privacy documents and decides
// who still has to consent to them.
//
// The documents are plain Markdown the owner writes; the panel ships none
// (legal content is the owner's responsibility). What the panel adds is the
// "what we collect" list a document can include with a [[data-collection]]
// line: generated from the settings the panel really runs with
// (datacollection.go), so the page cannot drift from the retention it states.
//
// Consent is a single panel-wide counter, settings legal_consent_version,
// moved only by publishing a document marked as a material change. An account
// of role user is pending while it has no consent record at or above the
// counter. Nothing here blocks anything: pending only makes the SPA ask.
package legal

import (
	"context"
	"errors"
	"time"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

// Store is what the service needs of the document and consent store.
type Store interface {
	Latest(ctx context.Context, kind domain.LegalKind, locale string) (*domain.LegalDocument, error)
	HasConsentAtLeast(ctx context.Context, userID int64, version int) (bool, error)
	RecordConsent(ctx context.Context, c domain.LegalConsent) error
}

// SettingsLoader reads the global settings.
type SettingsLoader interface {
	Load(ctx context.Context, defaults ports.UISettings) (ports.UISettings, error)
}

// Service is the read side of the feature.
type Service struct {
	store     Store
	settings  SettingsLoader
	collector *Collector
	now       func() time.Time
}

// New builds the service. A nil collector lists nothing.
func New(store Store, settings SettingsLoader, collector *Collector) *Service {
	if collector == nil {
		collector = NewCollector(nil)
	}
	return &Service{store: store, settings: settings, collector: collector, now: time.Now}
}

// Collector is the data-collection list builder, for providers to register on.
func (s *Service) Collector() *Collector { return s.collector }

// PublicDocument is what the public endpoint serves.
type PublicDocument struct {
	Kind           domain.LegalKind
	Version        int
	ConsentVersion int
	Locale         string
	// FallbackFrom is the locale that was asked for when another was served
	// ("" when the asked-for one was served or none was asked for).
	FallbackFrom   string
	Content        string
	PublishedAt    time.Time
	DataCollection map[string]Item
}

// Public resolves a document for a language along the fallback chain
// (domain.LegalLocaleChain). domain.ErrNotFound when the feature is off or
// nothing is published in any locale of the chain.
func (s *Service) Public(ctx context.Context, kind domain.LegalKind, lang string) (*PublicDocument, error) {
	set, err := s.settings.Load(ctx, ports.UISettings{})
	if err != nil {
		return nil, err
	}
	if !set.LegalEnabled {
		return nil, domain.ErrNotFound
	}
	asked, _ := domain.NormalizeLegalLocale(lang)
	for _, locale := range domain.LegalLocaleChain(lang) {
		doc, err := s.store.Latest(ctx, kind, locale)
		if errors.Is(err, domain.ErrNotFound) {
			continue
		}
		if err != nil {
			return nil, err
		}
		dc, err := s.collector.Collect(ctx, set)
		if err != nil {
			return nil, err
		}
		out := &PublicDocument{
			Kind: kind, Version: doc.Version, ConsentVersion: set.LegalConsentVersion, Locale: doc.Locale,
			Content: doc.Content, PublishedAt: doc.PublishedAt, DataCollection: dc,
		}
		if asked != "" && asked != doc.Locale {
			out.FallbackFrom = asked
		}
		return out, nil
	}
	return nil, domain.ErrNotFound
}

// Current is the live consent state.
type Current struct {
	Enabled        bool
	ConsentVersion int
}

// Required reports whether consent is asked for at all: the feature is on and
// something was published.
func (c Current) Required() bool { return c.Enabled && c.ConsentVersion > 0 }

// Current reads the consent state.
func (s *Service) Current(ctx context.Context) (Current, error) {
	set, err := s.settings.Load(ctx, ports.UISettings{})
	if err != nil {
		return Current{}, err
	}
	return Current{Enabled: set.LegalEnabled, ConsentVersion: set.LegalConsentVersion}, nil
}

// Pending reports whether the account must be asked to consent: the feature is
// on, something was published, the account is of role user (staff are never
// asked) and it has no consent record at or above the current version.
func (s *Service) Pending(ctx context.Context, u *domain.User) (bool, error) {
	if u == nil || u.Role != domain.RoleUser {
		return false, nil
	}
	cur, err := s.Current(ctx)
	if err != nil || !cur.Required() {
		return false, err
	}
	ok, err := s.store.HasConsentAtLeast(ctx, u.ID, cur.ConsentVersion)
	if err != nil {
		return false, err
	}
	return !ok, nil
}

// Accept records that the account accepted version. domain.ErrNotFound while
// the feature is off; domain.ErrLegalConsentOutdated when version is not the
// current one (the page the account saw is stale).
func (s *Service) Accept(ctx context.Context, userID int64, version int, method string) error {
	cur, err := s.Current(ctx)
	if err != nil {
		return err
	}
	if !cur.Required() {
		return domain.ErrNotFound
	}
	if version != cur.ConsentVersion {
		return domain.ErrLegalConsentOutdated
	}
	return s.store.RecordConsent(ctx, domain.LegalConsent{UserID: userID, ConsentVersion: version, AcceptedAt: s.now(), Method: method})
}

// CheckRegistration decides whether a signup may go ahead given the consent
// version the form was submitted with (nil: the field was absent). It returns
// whether consent was required and domain.ErrLegalConsentOutdated when it was
// and the offered version is missing or not the current one.
func (s *Service) CheckRegistration(ctx context.Context, accepted *int) (bool, error) {
	cur, err := s.Current(ctx)
	if err != nil {
		return false, err
	}
	if !cur.Required() {
		return false, nil
	}
	if accepted == nil || *accepted != cur.ConsentVersion {
		return true, domain.ErrLegalConsentOutdated
	}
	return true, nil
}

// RecordRegistration stores the consent given on the signup form.
func (s *Service) RecordRegistration(ctx context.Context, userID int64, version int) error {
	return s.store.RecordConsent(ctx, domain.LegalConsent{UserID: userID, ConsentVersion: version, AcceptedAt: s.now(), Method: domain.LegalConsentRegister})
}
