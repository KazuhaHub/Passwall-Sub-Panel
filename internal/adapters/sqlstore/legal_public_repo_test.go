package sqlstore

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

func TestLegalPublic_FallbacksAndRedaction(t *testing.T) {
	_, repos := legalTestRepos(t)
	ctx := context.Background()
	enableLegal(t, repos)
	if _, err := repos.Legal.Publish(ctx, legalDraft("privacy", "en-US", "English", false)); err != nil {
		t.Fatal(err)
	}
	if _, err := repos.Legal.Publish(ctx, legalDraft("privacy", "zh-CN", "中文", true)); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ request, locale, fallback, content string }{
		{"zh-TW", "zh-CN", "zh-TW", "中文"},
		{"fr-FR", "en-US", "fr-FR", "English"},
		{"en-us", "en-US", "", "English"},
		{"", "en-US", "", "English"},
		{"zh-CN", "zh-CN", "", "中文"},
	} {
		doc, err := repos.Legal.Public(ctx, "privacy", tc.request)
		if err != nil || doc.Locale != tc.locale || doc.FallbackFrom != tc.fallback || doc.Content != tc.content || doc.ConsentVersion != 2 || doc.Version != 1 {
			t.Fatalf("%q => %+v: %v", tc.request, doc, err)
		}
		encoded, err := json.Marshal(doc)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(encoded), "published_by") || strings.Contains(string(encoded), `"id"`) {
			t.Fatalf("publisher metadata leaked: %s", encoded)
		}
	}
	if _, err := repos.Legal.Publish(ctx, legalDraft("privacy", "zh-TW", "繁體", false)); err != nil {
		t.Fatal(err)
	}
	if doc, err := repos.Legal.Public(ctx, "privacy", "zh-TW"); err != nil || doc.Content != "繁體" || doc.FallbackFrom != "" {
		t.Fatalf("exact locale %+v: %v", doc, err)
	}
}

func TestLegalPublic_DisclosesDurableEffectiveSettings(t *testing.T) {
	_, repos := legalTestRepos(t)
	ctx := context.Background()
	enableLegal(t, repos)
	if _, err := repos.Legal.Publish(ctx, legalDraft("privacy", "en-US", "[[data-collection]]", false)); err != nil {
		t.Fatal(err)
	}
	for _, settings := range []ports.UISettings{
		{LegalEnabled: true},
		{LegalEnabled: true, SubLogRetentionDays: 14, AuthEventRetentionDays: 30, RiskHWIDCaptureOff: true, RiskRefreshIntervalMinutes: 25,
			RiskConnectionRetentionDays: 90, RiskFlagRecordRetentionDays: 365, CaptchaSecretKey: "private-captcha-secret", GeoIPUpdateToken: "private-update-token"},
	} {
		if err := repos.Settings.Save(ctx, settings); err != nil {
			t.Fatal(err)
		}
		doc, err := repos.Legal.Public(ctx, "privacy", "en-US")
		if err != nil {
			t.Fatal(err)
		}
		if want := ports.LegalDataCollectionFromSettings(settings); !reflect.DeepEqual(doc.DataCollection, want) {
			t.Fatalf("disclosed %+v, want durable %+v", doc.DataCollection, want)
		}
		body, err := json.Marshal(doc)
		if err != nil {
			t.Fatal(err)
		}
		for _, forbidden := range []string{"private-captcha-secret", "private-update-token", "captcha_secret_key", "geo_ip_update_token", "published_by"} {
			if strings.Contains(string(body), forbidden) {
				t.Fatalf("private setting or identity %q leaked", forbidden)
			}
		}
	}
}

func TestLegalPublic_DisabledAbsentAndInvalid(t *testing.T) {
	_, repos := legalTestRepos(t)
	ctx := context.Background()
	if _, err := repos.Legal.Public(ctx, "terms", "en-US"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("default disabled: %v", err)
	}
	enableLegal(t, repos)
	if _, err := repos.Legal.Public(ctx, "terms", "en-US"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("no publication: %v", err)
	}
	if _, err := repos.Legal.Publish(ctx, legalDraft("terms", "zh-CN", "中文", false)); err != nil {
		t.Fatal(err)
	}
	if doc, err := repos.Legal.Public(ctx, "terms", "de-DE"); err != nil || doc.Locale != "zh-CN" {
		t.Fatalf("English absent fallback %+v: %v", doc, err)
	}
	if _, err := repos.Legal.Public(ctx, "privacy", "en-US"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("missing kind: %v", err)
	}
	for _, tc := range []struct{ kind, locale string }{{"unknown", "en-US"}, {"terms", "../../en"}} {
		if _, err := repos.Legal.Public(ctx, tc.kind, tc.locale); !errors.Is(err, domain.ErrValidation) {
			t.Fatalf("invalid identity %+v: %v", tc, err)
		}
	}
	if err := repos.Settings.Save(ctx, ports.UISettings{LegalEnabled: false}); err != nil {
		t.Fatal(err)
	}
	if _, err := repos.Legal.Public(ctx, "terms", "zh-CN"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("disabled published document: %v", err)
	}
}

func TestLegalPublic_ConsistentBodyAndVersionDuringPublication(t *testing.T) {
	_, repos := legalTestRepos(t)
	ctx := context.Background()
	enableLegal(t, repos)
	if _, err := repos.Legal.Publish(ctx, legalDraft("terms", "en-US", "1", false)); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	results := make(chan error, 9)
	wg.Go(func() {
		for i := int64(2); i <= 17; i++ {
			if _, err := repos.Legal.Publish(ctx, legalDraft("terms", "en-US", strconv.FormatInt(i, 10), true)); err != nil {
				results <- err
				return
			}
		}
		results <- nil
	})
	for range 8 {
		wg.Go(func() {
			for range 16 {
				doc, err := repos.Legal.Public(ctx, "terms", "fr-FR")
				if err != nil {
					results <- err
					return
				}
				if doc.Version != doc.ConsentVersion || doc.Content != strconv.FormatInt(doc.ConsentVersion, 10) {
					results <- fmt.Errorf("mixed publication: %+v", doc)
					return
				}
			}
			results <- nil
		})
	}
	wg.Wait()
	close(results)
	for err := range results {
		if err != nil {
			t.Fatal(err)
		}
	}
}
