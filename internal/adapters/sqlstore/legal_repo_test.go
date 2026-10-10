package sqlstore

import (
	"context"
	"errors"
	"math"
	"strings"
	"sync"
	"testing"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
	"gorm.io/gorm"
)

func legalTestRepos(t *testing.T) (*gorm.DB, ports.Repos) {
	t.Helper()
	db, err := openTestDB(t)
	if err != nil {
		t.Fatal(err)
	}
	if err := ensureTestSchema(db); err != nil {
		t.Fatal(err)
	}
	return db, NewRepos(db)
}

func legalDraft(kind, locale, content string, bump bool) domain.LegalDraft {
	return domain.LegalDraft{Kind: kind, Locale: locale, Content: content, ConsentBump: bump, PublishedBy: 42}
}

func TestLegalPublishImmutableVersionsAndGlobalConsent(t *testing.T) {
	db, repos := legalTestRepos(t)
	ctx := context.Background()
	cases := []struct {
		draft            domain.LegalDraft
		version, consent int64
	}{
		{legalDraft("terms", "zh-CN", "original", false), 1, 1},
		{legalDraft("terms", "zh-CN", "typo correction", false), 2, 1},
		{legalDraft("privacy", "en-US", "privacy", false), 1, 1},
		{legalDraft("terms", "en-US", "major translation", true), 1, 2},
	}
	for _, c := range cases {
		p, err := repos.Legal.Publish(ctx, c.draft)
		if err != nil {
			t.Fatal(err)
		}
		if p.Document.Version != c.version || p.ConsentVersion != c.consent || p.Document.Content != c.draft.Content || p.Document.PublishedBy != 42 || p.Document.PublishedAt.IsZero() || p.Document.ID <= 0 {
			t.Fatalf("publication = %+v", p)
		}
	}
	var original legalDocumentRow
	if err := db.Where("kind = ? AND locale = ? AND version = ?", "terms", "zh-CN", 1).First(&original).Error; err != nil {
		t.Fatal(err)
	}
	if original.Content != "original" {
		t.Fatalf("history overwritten: %+v", original)
	}
	latest, err := repos.Legal.Latest(ctx, "terms", "zh-CN")
	if err != nil || latest.Version != 2 || latest.Content != "typo correction" {
		t.Fatalf("latest = %+v, %v", latest, err)
	}
	history, err := repos.Legal.History(ctx, "terms", "zh-CN", 0, 1)
	if err != nil || len(history) != 1 || history[0].Version != 2 {
		t.Fatalf("history = %+v, %v", history, err)
	}
	history, err = repos.Legal.History(ctx, "terms", "zh-CN", 2, 1)
	if err != nil || len(history) != 1 || history[0].Version != 1 {
		t.Fatalf("history cursor = %+v, %v", history, err)
	}
}

func TestLegalPublishInvalidatesSettingsAndSettingsCannotChangeConsent(t *testing.T) {
	_, repos := legalTestRepos(t)
	ctx := context.Background()
	fresh, err := repos.Settings.Load(ctx, ports.UISettings{LegalEnabled: true, LegalConsentVersion: 91})
	if err != nil || fresh.LegalEnabled || fresh.LegalConsentVersion != 0 {
		t.Fatalf("fresh = %+v, %v", fresh, err)
	}
	for i := range 2 {
		if _, err := repos.Legal.Publish(ctx, legalDraft("privacy", "zh-CN", "published", i > 0)); err != nil {
			t.Fatal(err)
		}
		current, err := repos.Settings.Load(ctx, ports.UISettings{})
		if err != nil || current.LegalConsentVersion != int64(i+1) {
			t.Fatalf("cached consent = %d, %v", current.LegalConsentVersion, err)
		}
	}
	for _, forged := range []int64{0, -1, 99} {
		fresh.LegalConsentVersion = forged
		fresh.LegalEnabled = true
		fresh.SiteTitle = "new branding"
		if err := repos.Settings.Save(ctx, fresh); err != nil {
			t.Fatal(err)
		}
		current, err := repos.Settings.Load(ctx, ports.UISettings{})
		if err != nil || current.LegalConsentVersion != 2 || !current.LegalEnabled || current.SiteTitle != "new branding" {
			t.Fatalf("settings write changed consent: %+v, %v", current, err)
		}
	}
}

func TestLegalPublishRollsBackDocumentWhenConsentWriteFails(t *testing.T) {
	db, repos := legalTestRepos(t)
	ctx := context.Background()
	if _, err := repos.Legal.Publish(ctx, legalDraft("privacy", "zh-CN", "original", false)); err != nil {
		t.Fatal(err)
	}
	before, err := repos.Settings.Load(ctx, ports.UISettings{})
	if err != nil {
		t.Fatal(err)
	}
	failure := errors.New("consent write failed")
	if err := db.Callback().Update().Before("gorm:update").Register("legal-fail-consent", func(tx *gorm.DB) {
		if tx.Statement.Table == "settings" {
			tx.AddError(failure)
		}
	}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Callback().Update().Remove("legal-fail-consent") })
	if _, err := repos.Legal.Publish(ctx, legalDraft("privacy", "zh-CN", "failed major", true)); !errors.Is(err, failure) {
		t.Fatalf("error = %v", err)
	}
	var count int64
	if err := db.Model(&legalDocumentRow{}).Count(&count).Error; err != nil || count != 1 {
		t.Fatalf("document count = %d, %v", count, err)
	}
	after, err := repos.Settings.Load(ctx, ports.UISettings{})
	if err != nil || after.LegalConsentVersion != before.LegalConsentVersion {
		t.Fatalf("failed transaction changed consent: %d, %v", after.LegalConsentVersion, err)
	}
}

func TestLegalPublishConcurrentMajorVersions(t *testing.T) {
	_, repos := legalTestRepos(t)
	ctx := context.Background()
	const n = 8
	results := make(chan domain.LegalPublication, n)
	errors := make(chan error, n)
	var wg sync.WaitGroup
	for range n {
		wg.Go(func() {
			p, err := repos.Legal.Publish(ctx, legalDraft("terms", "zh-CN", "concurrent", true))
			results <- p
			errors <- err
		})
	}
	wg.Wait()
	close(results)
	close(errors)
	for err := range errors {
		if err != nil {
			t.Fatal(err)
		}
	}
	versions, consents := map[int64]bool{}, map[int64]bool{}
	for p := range results {
		versions[p.Document.Version] = true
		consents[p.ConsentVersion] = true
	}
	for i := int64(1); i <= n; i++ {
		if !versions[i] || !consents[i] {
			t.Fatalf("lost concurrent version %d: %v/%v", i, versions, consents)
		}
	}
	current, err := repos.Settings.Load(ctx, ports.UISettings{})
	if err != nil || current.LegalConsentVersion != n {
		t.Fatalf("consent = %d, %v", current.LegalConsentVersion, err)
	}
}

func TestLegalPublishRejectsInvalidAndOversizedDraftsWithoutWrites(t *testing.T) {
	db, repos := legalTestRepos(t)
	ctx := context.Background()
	for _, draft := range []domain.LegalDraft{
		legalDraft("other", "zh-CN", "x", false),
		legalDraft("terms", "../../zh", "x", false),
		legalDraft("privacy", "zh-CN", strings.Repeat("中", 20001), false),
		legalDraft("terms", "en-US", string([]byte{0xff}), false),
		legalDraft("terms", "en-US", "text\x00text", false),
		{Kind: "terms", Locale: "en-US", Content: "x", PublishedBy: 0},
	} {
		if _, err := repos.Legal.Publish(ctx, draft); err == nil {
			t.Fatalf("accepted invalid draft %+v", draft)
		}
	}
	var count int64
	if err := db.Model(&legalDocumentRow{}).Count(&count).Error; err != nil || count != 0 {
		t.Fatalf("invalid drafts wrote %d rows, %v", count, err)
	}
	if _, err := repos.Legal.Publish(ctx, legalDraft("privacy", "zh-CN", strings.Repeat("中", 20000), false)); err != nil {
		t.Fatalf("exact byte limit rejected: %v", err)
	}
}

func TestLegalPublishRefusesConsentOverflowWithoutDocument(t *testing.T) {
	db, repos := legalTestRepos(t)
	if err := db.Create(&settingRow{Type: "legal", Name: "consent_version", Value: "9223372036854775807"}).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := repos.Legal.Publish(context.Background(), legalDraft("terms", "en-US", "major", true)); err == nil {
		t.Fatal("accepted consent overflow")
	}
	current, err := repos.Settings.Load(context.Background(), ports.UISettings{})
	if err != nil || current.LegalConsentVersion != math.MaxInt64 {
		t.Fatalf("overflow changed consent: %d, %v", current.LegalConsentVersion, err)
	}
	var count int64
	if err := db.Model(&legalDocumentRow{}).Count(&count).Error; err != nil || count != 0 {
		t.Fatalf("overflow wrote document: %d, %v", count, err)
	}
}

func TestLegalPublishRetriesVersionCollisionOnlyOnce(t *testing.T) {
	for _, failAlways := range []bool{false, true} {
		t.Run(map[bool]string{false: "one collision", true: "exhausted collision"}[failAlways], func(t *testing.T) {
			db, repos := legalTestRepos(t)
			attempts := 0
			if err := db.Callback().Create().Before("gorm:create").Register("legal-collision", func(tx *gorm.DB) {
				if tx.Statement.Table == "legal_documents" {
					attempts++
					if attempts == 1 || failAlways {
						tx.AddError(gorm.ErrDuplicatedKey)
					}
				}
			}); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = db.Callback().Create().Remove("legal-collision") })
			p, err := repos.Legal.Publish(context.Background(), legalDraft("privacy", "en-US", "publication", true))
			if attempts != 2 {
				t.Fatalf("attempts = %d, error = %v", attempts, err)
			}
			if failAlways {
				if err == nil {
					t.Fatal("exhausted collision accepted")
				}
				return
			}
			if err != nil || p.Document.Version != 1 || p.ConsentVersion != 1 {
				t.Fatalf("retry = %+v, %v", p, err)
			}
		})
	}
}

func TestLegalPublishClassifiesActualDialectVersionConstraint(t *testing.T) {
	db, repos := legalTestRepos(t)
	publication, err := repos.Legal.Publish(context.Background(), legalDraft("privacy", "en-US", "first", false))
	if err != nil {
		t.Fatal(err)
	}
	duplicate := legalDocumentRow{Kind: "privacy", Locale: "en-US", Version: publication.Document.Version, Content: "duplicate", PublishedBy: 42, PublishedAt: publication.Document.PublishedAt}
	err = db.Create(&duplicate).Error
	if err == nil || !legalUniqueViolation(err) {
		t.Fatalf("dialect version constraint was not recognized: %v", err)
	}
	if legalUniqueViolation(errors.New("database locked")) {
		t.Fatal("transient failure misclassified as a version collision")
	}
}

func TestLegalPublishReadsAreBoundedAndUnavailableIsNotEmptyContent(t *testing.T) {
	_, repos := legalTestRepos(t)
	ctx := context.Background()
	if _, err := repos.Legal.Latest(ctx, "privacy", "en-US"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("missing document = %v", err)
	}
	for _, bounds := range []struct {
		before int64
		limit  int
	}{{-1, 1}, {0, 0}, {0, 51}} {
		if _, err := repos.Legal.History(ctx, "privacy", "en-US", bounds.before, bounds.limit); !errors.Is(err, domain.ErrValidation) {
			t.Fatalf("invalid history bounds accepted: %+v, %v", bounds, err)
		}
	}
	history, err := repos.Legal.History(ctx, "privacy", "en-US", 0, 50)
	if err != nil || history == nil || len(history) != 0 {
		t.Fatalf("empty history = %+v, %v", history, err)
	}
	if _, err := repos.Legal.Latest(ctx, "unknown", "en-US"); !errors.Is(err, domain.ErrValidation) {
		t.Fatalf("invalid kind = %v", err)
	}
}

func TestLegalPublishFirstSettingsSaveCannotOverwriteInterveningPublication(t *testing.T) {
	db, repos := legalTestRepos(t)
	inserted := false
	if err := db.Callback().Create().Before("gorm:create").Register("legal-first-settings-race", func(tx *gorm.DB) {
		row, ok := tx.Statement.Dest.(*settingRow)
		if inserted || !ok || row.Type != "legal" || row.Name != "consent_version" {
			return
		}
		inserted = true
		publicationState := settingRow{Type: "legal", Name: "consent_version", Value: "7"}
		tx.AddError(tx.Session(&gorm.Session{NewDB: true}).Create(&publicationState).Error)
	}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Callback().Create().Remove("legal-first-settings-race") })
	if err := repos.Settings.Save(context.Background(), ports.UISettings{LegalConsentVersion: 99, LegalEnabled: true}); err != nil {
		t.Fatal(err)
	}
	current, err := repos.Settings.Load(context.Background(), ports.UISettings{})
	if err != nil || !inserted || current.LegalConsentVersion != 7 || !current.LegalEnabled {
		t.Fatalf("first settings save replaced publication state: inserted=%v, version=%d, error=%v", inserted, current.LegalConsentVersion, err)
	}
}
