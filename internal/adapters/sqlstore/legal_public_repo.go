package sqlstore

import (
	"context"
	"errors"
	"fmt"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
	"golang.org/x/text/language"
	"gorm.io/gorm"
)

func (r *legalRepo) Public(ctx context.Context, kind, requested string) (domain.LegalPublicDocument, error) {
	if requested == "" {
		requested = "en-US"
	}
	tag, err := language.Parse(requested)
	if err != nil {
		return domain.LegalPublicDocument{}, fmt.Errorf("%w: legal locale", domain.ErrValidation)
	}
	requested = tag.String()
	if err := domain.ValidateLegalIdentity(kind, requested); err != nil {
		return domain.LegalPublicDocument{}, err
	}
	locales := []string{requested}
	if requested == "zh-TW" {
		locales = append(locales, "zh-CN")
	}
	locales = append(locales, "en-US", "zh-CN")
	var public domain.LegalPublicDocument
	err = r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// Hold publication's global row while choosing the document, including
		// fallback queries. A cached settings read followed by Latest could pair
		// an old body with a new consent version at READ COMMITTED isolation.
		state, err := readLegalState(tx, false)
		if err != nil {
			return err
		}
		if !state.Enabled || state.ConsentVersion == 0 {
			return domain.ErrNotFound
		}
		seen := make(map[string]bool, len(locales))
		for _, locale := range locales {
			if seen[locale] {
				continue
			}
			seen[locale] = true
			var row legalDocumentRow
			err := tx.Where("kind = ? AND locale = ?", kind, locale).Order("version DESC").First(&row).Error
			if errors.Is(err, gorm.ErrRecordNotFound) {
				continue
			}
			if err != nil {
				return err
			}
			public = domain.LegalPublicDocument{Version: row.Version, ConsentVersion: state.ConsentVersion, Locale: row.Locale, Content: row.Content, PublishedAt: row.PublishedAt}
			// Read durable collection settings in this transaction rather than
			// combining the public body with a stale cached disclosure. Only the
			// explicit public policy fields leave the repository.
			settings, err := newKVSettingsRepo(tx).Load(ctx, ports.UISettings{})
			if err != nil {
				return err
			}
			public.DataCollection = ports.LegalDataCollectionFromSettings(settings)
			if locale != requested {
				public.FallbackFrom = requested
			}
			return nil
		}
		return domain.ErrNotFound
	})
	if err != nil {
		return domain.LegalPublicDocument{}, err
	}
	return public, nil
}
