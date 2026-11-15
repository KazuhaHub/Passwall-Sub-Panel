package legal

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

type fakeStore struct {
	docs     map[string]*domain.LegalDocument // kind/locale
	consents map[int64]int                    // user -> highest accepted version
	methods  []string
}

// emptyStore has nothing published.
func emptyStore() *fakeStore {
	return &fakeStore{docs: map[string]*domain.LegalDocument{}, consents: map[int64]int{}}
}

// newFakeStore has one document published in zh-CN, so consent is reachable.
func newFakeStore() *fakeStore {
	f := emptyStore()
	f.put(domain.LegalKindTerms, "zh-CN", "terms")
	return f
}

func (f *fakeStore) HasDocumentIn(_ context.Context, locales []string) (bool, error) {
	for _, d := range f.docs {
		for _, l := range locales {
			if d.Locale == l {
				return true, nil
			}
		}
	}
	return false, nil
}

func (f *fakeStore) put(kind domain.LegalKind, locale, content string) {
	f.docs[string(kind)+"/"+locale] = &domain.LegalDocument{Kind: kind, Locale: locale, Version: 1, Content: content, PublishedAt: time.Unix(1_700_000_000, 0).UTC()}
}

func (f *fakeStore) Latest(_ context.Context, kind domain.LegalKind, locale string) (*domain.LegalDocument, error) {
	if d, ok := f.docs[string(kind)+"/"+locale]; ok {
		return d, nil
	}
	return nil, domain.ErrNotFound
}

func (f *fakeStore) HasConsentAtLeast(_ context.Context, userID int64, version int) (bool, error) {
	return f.consents[userID] >= version && f.consents[userID] > 0, nil
}

func (f *fakeStore) RecordConsent(_ context.Context, c domain.LegalConsent) error {
	if c.ConsentVersion > f.consents[c.UserID] {
		f.consents[c.UserID] = c.ConsentVersion
	}
	f.methods = append(f.methods, c.Method)
	return nil
}

type fakeSettings struct{ s ports.UISettings }

func (f fakeSettings) Load(context.Context, ports.UISettings) (ports.UISettings, error) {
	return f.s, nil
}

func svc(store *fakeStore, s ports.UISettings) *Service {
	return New(store, fakeSettings{s}, NewCollector(nil))
}

func TestPublic_NotFoundWhenDisabled(t *testing.T) {
	st := newFakeStore()
	st.put(domain.LegalKindPrivacy, "zh-CN", "x")
	_, err := svc(st, ports.UISettings{LegalEnabled: false, LegalConsentVersion: 1}).Public(context.Background(), domain.LegalKindPrivacy, "zh-CN")
	if !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("disabled: %v, want ErrNotFound", err)
	}
}

func TestPublic_FallbackChain(t *testing.T) {
	enabled := ports.UISettings{LegalEnabled: true, LegalConsentVersion: 3}
	ctx := context.Background()
	for _, c := range []struct {
		name         string
		have         []string
		lang         string
		wantLocale   string
		wantFallback string
	}{
		{"exact", []string{"zh-TW", "zh-CN", "en-US"}, "zh-TW", "zh-TW", ""},
		{"zh-TW falls to zh-CN", []string{"zh-CN", "en-US"}, "zh-TW", "zh-CN", "zh-TW"},
		{"zh-TW then zh-CN missing falls to en-US", []string{"en-US"}, "zh-TW", "en-US", "zh-TW"},
		{"unknown language falls to en-US", []string{"en-US", "zh-CN"}, "fr-FR", "en-US", "fr-FR"},
		{"unknown language, only zh-CN", []string{"zh-CN"}, "fr-FR", "zh-CN", "fr-FR"},
		{"en-US missing falls to zh-CN", []string{"zh-CN"}, "en-US", "zh-CN", "en-US"},
		{"no language asked is not a fallback", []string{"zh-CN"}, "", "zh-CN", ""},
	} {
		t.Run(c.name, func(t *testing.T) {
			st := emptyStore()
			for _, l := range c.have {
				st.put(domain.LegalKindTerms, l, "doc "+l)
			}
			got, err := svc(st, enabled).Public(ctx, domain.LegalKindTerms, c.lang)
			if err != nil {
				t.Fatal(err)
			}
			if got.Locale != c.wantLocale || got.FallbackFrom != c.wantFallback || got.Content != "doc "+c.wantLocale {
				t.Errorf("served %q (fallback_from %q) content %q; want %q / %q", got.Locale, got.FallbackFrom, got.Content, c.wantLocale, c.wantFallback)
			}
			if got.ConsentVersion != 3 {
				t.Errorf("consent_version = %d, want 3", got.ConsentVersion)
			}
		})
	}
}

func TestPublic_NotFoundWhenNothingPublished(t *testing.T) {
	_, err := svc(emptyStore(), ports.UISettings{LegalEnabled: true, LegalConsentVersion: 1}).Public(context.Background(), domain.LegalKindTerms, "zh-CN")
	if !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("nothing published: %v", err)
	}
}

func TestPending(t *testing.T) {
	ctx := context.Background()
	user := &domain.User{ID: 5, Role: domain.RoleUser}
	for _, c := range []struct {
		name     string
		settings ports.UISettings
		u        *domain.User
		accepted int
		want     bool
	}{
		{"enabled, version 1, nothing accepted", ports.UISettings{LegalEnabled: true, LegalConsentVersion: 1}, user, 0, true},
		{"accepted the current version", ports.UISettings{LegalEnabled: true, LegalConsentVersion: 2}, user, 2, false},
		{"accepted a newer one than current", ports.UISettings{LegalEnabled: true, LegalConsentVersion: 2}, user, 3, false},
		{"accepted an older version", ports.UISettings{LegalEnabled: true, LegalConsentVersion: 3}, user, 2, true},
		{"disabled", ports.UISettings{LegalEnabled: false, LegalConsentVersion: 3}, user, 0, false},
		{"never published (version 0)", ports.UISettings{LegalEnabled: true, LegalConsentVersion: 0}, user, 0, false},
		{"operator is never pending", ports.UISettings{LegalEnabled: true, LegalConsentVersion: 3}, &domain.User{ID: 6, Role: domain.RoleOperator}, 0, false},
		{"admin is never pending", ports.UISettings{LegalEnabled: true, LegalConsentVersion: 3}, &domain.User{ID: 7, Role: domain.RoleAdmin}, 0, false},
		{"a custom role is staff, never pending", ports.UISettings{LegalEnabled: true, LegalConsentVersion: 3}, &domain.User{ID: 8, Role: "billing-clerk"}, 0, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			st := newFakeStore()
			st.consents[c.u.ID] = c.accepted
			got, err := svc(st, c.settings).Pending(ctx, c.u)
			if err != nil || got != c.want {
				t.Errorf("Pending = %v, %v; want %v", got, err, c.want)
			}
		})
	}
}

func TestAccept(t *testing.T) {
	ctx := context.Background()
	st := newFakeStore()
	s := svc(st, ports.UISettings{LegalEnabled: true, LegalConsentVersion: 4})
	if err := s.Accept(ctx, 9, 3, domain.LegalConsentPrompt); !errors.Is(err, domain.ErrLegalConsentOutdated) {
		t.Fatalf("an older version: %v, want ErrLegalConsentOutdated", err)
	}
	if err := s.Accept(ctx, 9, 5, domain.LegalConsentPrompt); !errors.Is(err, domain.ErrLegalConsentOutdated) {
		t.Fatalf("a version from the future: %v, want ErrLegalConsentOutdated", err)
	}
	if st.consents[9] != 0 {
		t.Fatal("a refused accept wrote a record")
	}
	if err := s.Accept(ctx, 9, 4, domain.LegalConsentPrompt); err != nil {
		t.Fatal(err)
	}
	if st.consents[9] != 4 || st.methods[0] != "prompt" {
		t.Errorf("stored %d via %v", st.consents[9], st.methods)
	}
	// Disabled: there is nothing to accept.
	off := svc(newFakeStore(), ports.UISettings{LegalEnabled: false, LegalConsentVersion: 4})
	if err := off.Accept(ctx, 9, 4, domain.LegalConsentPrompt); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("disabled: %v, want ErrNotFound", err)
	}
}

func TestCheckRegistration(t *testing.T) {
	ctx := context.Background()
	two := 2
	three := 3
	for _, c := range []struct {
		name     string
		settings ports.UISettings
		sent     *int
		wantErr  bool
	}{
		{"matching version", ports.UISettings{LegalEnabled: true, LegalConsentVersion: 2}, &two, false},
		{"stale version", ports.UISettings{LegalEnabled: true, LegalConsentVersion: 3}, &two, true},
		{"a version from the future", ports.UISettings{LegalEnabled: true, LegalConsentVersion: 2}, &three, true},
		{"missing while required", ports.UISettings{LegalEnabled: true, LegalConsentVersion: 2}, nil, true},
		{"not required when disabled", ports.UISettings{LegalEnabled: false, LegalConsentVersion: 2}, nil, false},
		{"not required before the first publication", ports.UISettings{LegalEnabled: true, LegalConsentVersion: 0}, nil, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			required, err := svc(newFakeStore(), c.settings).CheckRegistration(ctx, c.sent)
			if c.wantErr != errors.Is(err, domain.ErrLegalConsentOutdated) {
				t.Errorf("err = %v, wantErr %v", err, c.wantErr)
			}
			if wantRequired := c.settings.LegalEnabled && c.settings.LegalConsentVersion > 0; required != wantRequired {
				t.Errorf("required = %v, want %v", required, wantRequired)
			}
		})
	}
}

// Consent is only asked for when somebody can read what they consent to. The
// fallback chain always ends at en-US and zh-CN, so a document that exists only
// in another locale (or none at all) is unreachable for most visitors: asking
// for consent to it would make a checkbox that links to a 404.
func TestConsentIsOnlyRequiredWhenADocumentIsReachable(t *testing.T) {
	ctx := context.Background()
	on := ports.UISettings{LegalEnabled: true, LegalConsentVersion: 2}
	user := &domain.User{ID: 5, Role: domain.RoleUser}
	for _, c := range []struct {
		name string
		st   func() *fakeStore
		want bool
	}{
		{"nothing published", emptyStore, false},
		{"only a ja-JP document", func() *fakeStore { s := emptyStore(); s.put(domain.LegalKindPrivacy, "ja-JP", "x"); return s }, false},
		{"an en-US document", func() *fakeStore { s := emptyStore(); s.put(domain.LegalKindPrivacy, "en-US", "x"); return s }, true},
		{"a zh-CN document", newFakeStore, true},
	} {
		t.Run(c.name, func(t *testing.T) {
			s := svc(c.st(), on)
			cur, err := s.Current(ctx)
			if err != nil || cur.Required() != c.want {
				t.Fatalf("Required = %v, %v; want %v", cur.Required(), err, c.want)
			}
			if pending, _ := s.Pending(ctx, user); pending != c.want {
				t.Errorf("Pending = %v, want %v", pending, c.want)
			}
			required, err := s.CheckRegistration(ctx, nil)
			if required != c.want || (c.want != errors.Is(err, domain.ErrLegalConsentOutdated)) {
				t.Errorf("CheckRegistration = %v, %v; want required %v", required, err, c.want)
			}
			if err := s.Accept(ctx, 5, 2, domain.LegalConsentPrompt); c.want == (err != nil) {
				t.Errorf("Accept = %v, want an error exactly when consent is not required", err)
			}
		})
	}
}

// countingStore counts the reachability lookups.
type countingStore struct {
	*fakeStore
	lookups int
	err     error
}

func (c *countingStore) HasDocumentIn(ctx context.Context, locales []string) (bool, error) {
	c.lookups++
	if c.err != nil {
		return false, c.err
	}
	return c.fakeStore.HasDocumentIn(ctx, locales)
}

// /api/auth/methods reads the consent state on every page load, so the
// reachability lookup sits behind the 30 second memo the counts use.
func TestCurrentRemembersReachabilityBriefly(t *testing.T) {
	ctx := context.Background()
	st := &countingStore{fakeStore: newFakeStore()}
	s := New(st, fakeSettings{ports.UISettings{LegalEnabled: true, LegalConsentVersion: 1}}, nil)
	now := time.Unix(1_700_000_000, 0)
	s.now = func() time.Time { return now }
	for i := 0; i < 5; i++ {
		if cur, err := s.Current(ctx); err != nil || !cur.Reachable {
			t.Fatalf("Current = %+v, %v", cur, err)
		}
	}
	if st.lookups != 1 {
		t.Fatalf("lookups = %d after five reads, want 1", st.lookups)
	}
	s.InvalidateReachable()
	_, _ = s.Current(ctx)
	if st.lookups != 2 {
		t.Errorf("lookups = %d after an invalidation, want 2", st.lookups)
	}
	now = now.Add(countCacheTTL + time.Second)
	_, _ = s.Current(ctx)
	if st.lookups != 3 {
		t.Errorf("lookups = %d after the memo expired, want 3", st.lookups)
	}
}

func TestCurrentDoesNotRememberAFailedLookup(t *testing.T) {
	ctx := context.Background()
	st := &countingStore{fakeStore: newFakeStore(), err: errors.New("db down")}
	s := New(st, fakeSettings{ports.UISettings{LegalEnabled: true, LegalConsentVersion: 1}}, nil)
	if _, err := s.Current(ctx); err == nil {
		t.Fatal("want the lookup error")
	}
	st.err = nil
	if cur, err := s.Current(ctx); err != nil || !cur.Reachable {
		t.Fatalf("after recovery = %+v, %v", cur, err)
	}
}

func TestCollectionIgnoresTheEnabledSwitch(t *testing.T) {
	s := svc(emptyStore(), ports.UISettings{LegalEnabled: false, SubLogRetentionDays: 7})
	dc, err := s.Collection(context.Background())
	if err != nil || dc["sub_log"].RetentionDays == nil || *dc["sub_log"].RetentionDays != 7 {
		t.Fatalf("Collection = %+v, %v", dc, err)
	}
}
