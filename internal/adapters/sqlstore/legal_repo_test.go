package sqlstore

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"gorm.io/gorm"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

// The legal tables hold the two published documents (immutable rows, one per
// version) and who accepted which consent version (no address, ever). Ids are
// never asserted: a reused server schema does not reset auto-increment.

func newLegalRepo(t *testing.T) (*LegalRepo, *gorm.DB) {
	t.Helper()
	db, err := openTestDB(t)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if err := ensureTestSchema(db); err != nil {
		t.Fatalf("schema: %v", err)
	}
	return NewLegalRepo(db), db
}

func legalUser(t *testing.T, db *gorm.DB, name string, role domain.Role) *domain.User {
	t.Helper()
	u := &domain.User{
		UPN: name + "@example.test", Email: name + "@example.test", SSOProvider: domain.SSOProviderLocal, SSOSubject: name + "@example.test",
		Role: role, Enabled: true, UUID: "99999999-9999-4999-9999-" + strings.Repeat(name[:1], 12),
		SubToken: "fixture-legal-token-" + name, TrafficResetPeriod: domain.ResetMonthly,
	}
	if err := NewRepos(db).User.Create(context.Background(), u); err != nil {
		t.Fatalf("create user %s: %v", name, err)
	}
	return u
}

func pub(kind domain.LegalKind, locale, content string, bump bool) domain.LegalDocument {
	return domain.LegalDocument{Kind: kind, Locale: locale, Content: content, ConsentBump: bump, PublishedBy: 7}
}

func TestLegalTables_AreInTheSchema(t *testing.T) {
	_, db := newLegalRepo(t)
	for _, table := range []string{"legal_documents", "legal_consents"} {
		if !db.Migrator().HasTable(table) {
			t.Errorf("table %s was not created by EnsureSchema", table)
		}
	}
	if !db.Migrator().HasIndex(&legalDocumentRow{}, "uk_legal_document_version") {
		t.Error("legal_documents has no unique (kind, locale, version) index")
	}
	// No address column: the consent record names who and which version only.
	for _, col := range []string{"ip", "ip_address", "remote_addr", "user_agent"} {
		if db.Migrator().HasColumn(&legalConsentRow{}, col) {
			t.Errorf("legal_consents has a %q column", col)
		}
	}
}

func TestLegalPublish_VersionsCountPerKindAndLocale(t *testing.T) {
	r, _ := newLegalRepo(t)
	ctx := context.Background()
	v := func(kind domain.LegalKind, locale string) int {
		t.Helper()
		d, err := r.Publish(ctx, pub(kind, locale, "body "+locale, false))
		if err != nil {
			t.Fatalf("publish %s/%s: %v", kind, locale, err)
		}
		return d.Version
	}
	if a, b, c := v(domain.LegalKindTerms, "zh-CN"), v(domain.LegalKindTerms, "zh-CN"), v(domain.LegalKindTerms, "zh-CN"); a != 1 || b != 2 || c != 3 {
		t.Fatalf("versions %d %d %d, want 1 2 3", a, b, c)
	}
	// Another locale and another kind each start at 1.
	if got := v(domain.LegalKindTerms, "en-US"); got != 1 {
		t.Errorf("first en-US terms = %d, want 1", got)
	}
	if got := v(domain.LegalKindPrivacy, "zh-CN"); got != 1 {
		t.Errorf("first zh-CN privacy = %d, want 1", got)
	}
}

func TestLegalPublish_StoresTheDocumentImmutably(t *testing.T) {
	r, _ := newLegalRepo(t)
	ctx := context.Background()
	d1, err := r.Publish(ctx, pub(domain.LegalKindPrivacy, "zh-CN", "第一版", true))
	if err != nil {
		t.Fatal(err)
	}
	if d1.ID == 0 || d1.PublishedAt.IsZero() || d1.PublishedAt.Location() != time.UTC || !d1.ConsentBump || d1.PublishedBy != 7 {
		t.Fatalf("published row %+v", d1)
	}
	if _, err := r.Publish(ctx, pub(domain.LegalKindPrivacy, "zh-CN", "第二版", false)); err != nil {
		t.Fatal(err)
	}
	got, err := r.Latest(ctx, domain.LegalKindPrivacy, "zh-CN")
	if err != nil || got.Version != 2 || got.Content != "第二版" {
		t.Fatalf("latest = %+v, %v", got, err)
	}
	// Version 1 is still there, unchanged.
	hist, err := r.History(ctx, domain.LegalKindPrivacy)
	if err != nil || len(hist) != 2 {
		t.Fatalf("history = %+v, %v", hist, err)
	}
	if hist[0].Version != 2 || hist[1].Version != 1 || !hist[1].ConsentBump {
		t.Errorf("history order/fields: %+v", hist)
	}
	if _, err := r.Latest(ctx, domain.LegalKindTerms, "zh-CN"); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("latest of an unpublished kind: %v, want ErrNotFound", err)
	}
}

func TestLegalPublish_RetriesOnceWhenTheVersionIsTaken(t *testing.T) {
	r, db := newLegalRepo(t)
	ctx := context.Background()
	if _, err := r.Publish(ctx, pub(domain.LegalKindTerms, "zh-CN", "one", false)); err != nil {
		t.Fatal(err)
	}
	// A competing publisher takes version 2 between our read and our insert.
	calls := 0
	r.beforeInsert = func(kind, locale string, version int) {
		calls++
		if calls == 1 {
			if err := db.Create(&legalDocumentRow{Kind: kind, Locale: locale, Version: version, Content: "rival", PublishedAt: time.Now().UTC()}).Error; err != nil {
				t.Errorf("rival insert: %v", err)
			}
		}
	}
	d, err := r.Publish(ctx, pub(domain.LegalKindTerms, "zh-CN", "mine", false))
	if err != nil {
		t.Fatalf("publish after one collision: %v", err)
	}
	if d.Version != 3 || calls != 2 {
		t.Errorf("version %d after %d attempts, want 3 after 2", d.Version, calls)
	}
}

func TestLegalPublish_GivesUpAfterTheSecondCollision(t *testing.T) {
	r, db := newLegalRepo(t)
	ctx := context.Background()
	r.beforeInsert = func(kind, locale string, version int) {
		_ = db.Create(&legalDocumentRow{Kind: kind, Locale: locale, Version: version, Content: "rival", PublishedAt: time.Now().UTC()}).Error
	}
	if _, err := r.Publish(ctx, pub(domain.LegalKindTerms, "zh-CN", "mine", false)); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("two collisions: %v, want ErrConflict", err)
	}
}

func TestLegalPublish_Validates(t *testing.T) {
	r, db := newLegalRepo(t)
	ctx := context.Background()
	if _, err := r.Publish(ctx, pub(domain.LegalKindTerms, "zh-CN", strings.Repeat("a", domain.LegalContentMaxBytes), false)); err != nil {
		t.Errorf("exactly 60000 bytes: %v", err)
	}
	if _, err := r.Publish(ctx, pub(domain.LegalKindTerms, "zh-CN", strings.Repeat("a", domain.LegalContentMaxBytes+1), false)); !errors.Is(err, domain.ErrLegalContentTooLarge) {
		t.Errorf("60001 bytes: %v", err)
	}
	if _, err := r.Publish(ctx, pub("cookies", "zh-CN", "x", false)); !errors.Is(err, domain.ErrValidation) {
		t.Errorf("unknown kind: %v", err)
	}
	if _, err := r.Publish(ctx, pub(domain.LegalKindTerms, "zh_CN", "x", false)); !errors.Is(err, domain.ErrValidation) {
		t.Errorf("bad locale: %v", err)
	}
	var n int64
	db.Model(&legalDocumentRow{}).Where("kind = ? AND locale = ?", "terms", "zh-CN").Count(&n)
	if n != 1 {
		t.Errorf("%d rows stored, want only the 60000-byte one", n)
	}
}

func TestLegalPublish_CJKContentRoundTripsAtTheLimit(t *testing.T) {
	r, _ := newLegalRepo(t)
	ctx := context.Background()
	body := strings.Repeat("条", 20000) // 60000 bytes
	if _, err := r.Publish(ctx, pub(domain.LegalKindPrivacy, "zh-TW", body, false)); err != nil {
		t.Fatalf("publish: %v", err)
	}
	got, err := r.Latest(ctx, domain.LegalKindPrivacy, "zh-TW")
	if err != nil || got.Content != body {
		t.Fatalf("round trip failed: %v (len %d)", err, len(got.Content))
	}
}

func TestLegalLatestPerLocale(t *testing.T) {
	r, _ := newLegalRepo(t)
	ctx := context.Background()
	for _, p := range []struct{ locale, body string }{{"zh-CN", "cn1"}, {"zh-CN", "cn2"}, {"en-US", "en1"}} {
		if _, err := r.Publish(ctx, pub(domain.LegalKindTerms, p.locale, p.body, false)); err != nil {
			t.Fatal(err)
		}
	}
	docs, err := r.LatestPerLocale(ctx, domain.LegalKindTerms)
	if err != nil || len(docs) != 2 {
		t.Fatalf("docs = %+v, %v", docs, err)
	}
	got := map[string]string{}
	for _, d := range docs {
		got[d.Locale] = d.Content
	}
	if got["zh-CN"] != "cn2" || got["en-US"] != "en1" {
		t.Errorf("latest per locale = %v", got)
	}
}

func TestLegalConsent_RecordsIdempotentlyAndAnswersAtLeast(t *testing.T) {
	r, db := newLegalRepo(t)
	ctx := context.Background()
	u := legalUser(t, db, "alice", domain.RoleUser)
	if ok, err := r.HasConsentAtLeast(ctx, u.ID, 1); ok || err != nil {
		t.Fatalf("before any consent: %v, %v", ok, err)
	}
	at := time.Now().UTC().Truncate(time.Second)
	for i := 0; i < 2; i++ { // the second write is a no-op, not an error
		if err := r.RecordConsent(ctx, domain.LegalConsent{UserID: u.ID, ConsentVersion: 2, AcceptedAt: at, Method: domain.LegalConsentPrompt}); err != nil {
			t.Fatalf("record #%d: %v", i, err)
		}
	}
	var n int64
	db.Model(&legalConsentRow{}).Where("user_id = ?", u.ID).Count(&n)
	if n != 1 {
		t.Fatalf("%d consent rows, want 1", n)
	}
	for v, want := range map[int]bool{1: true, 2: true, 3: false} {
		if ok, err := r.HasConsentAtLeast(ctx, u.ID, v); err != nil || ok != want {
			t.Errorf("HasConsentAtLeast(%d) = %v, %v; want %v", v, ok, err, want)
		}
	}
}

func TestLegalConsent_RejectsAnUnknownMethodAndAZeroVersion(t *testing.T) {
	r, db := newLegalRepo(t)
	ctx := context.Background()
	u := legalUser(t, db, "carol", domain.RoleUser)
	if err := r.RecordConsent(ctx, domain.LegalConsent{UserID: u.ID, ConsentVersion: 1, Method: "ip-sniffed"}); !errors.Is(err, domain.ErrValidation) {
		t.Errorf("unknown method: %v", err)
	}
	if err := r.RecordConsent(ctx, domain.LegalConsent{UserID: u.ID, ConsentVersion: 0, Method: domain.LegalConsentRegister}); !errors.Is(err, domain.ErrValidation) {
		t.Errorf("version 0: %v", err)
	}
}

func TestLegalPurgeConsentOrphans(t *testing.T) {
	r, db := newLegalRepo(t)
	ctx := context.Background()
	kept, gone := legalUser(t, db, "kate", domain.RoleUser), legalUser(t, db, "gina", domain.RoleUser)
	for _, u := range []*domain.User{kept, gone} {
		if err := r.RecordConsent(ctx, domain.LegalConsent{UserID: u.ID, ConsentVersion: 1, Method: domain.LegalConsentRegister}); err != nil {
			t.Fatal(err)
		}
	}
	if err := NewRepos(db).User.Delete(ctx, gone.ID); err != nil {
		t.Fatal(err)
	}
	n, err := r.PurgeConsentOrphans(ctx)
	if err != nil || n != 1 {
		t.Fatalf("purged %d, %v; want 1", n, err)
	}
	if ok, _ := r.HasConsentAtLeast(ctx, kept.ID, 1); !ok {
		t.Error("the living account's consent was purged")
	}
}

func TestLegalCountAffectedUsers_CountsOnlyRoleUser(t *testing.T) {
	r, db := newLegalRepo(t)
	ctx := context.Background()
	before, beforeAccepted, err := r.CountAffectedUsers(ctx, 1)
	if err != nil {
		t.Fatal(err)
	}
	u1 := legalUser(t, db, "uma", domain.RoleUser)
	legalUser(t, db, "vic", domain.RoleUser)
	legalUser(t, db, "opal", domain.RoleOperator)
	legalUser(t, db, "adam", domain.RoleAdmin)
	if err := r.RecordConsent(ctx, domain.LegalConsent{UserID: u1.ID, ConsentVersion: 1, Method: domain.LegalConsentRegister}); err != nil {
		t.Fatal(err)
	}
	affected, accepted, err := r.CountAffectedUsers(ctx, 1)
	if err != nil {
		t.Fatal(err)
	}
	if affected-before != 2 || accepted-beforeAccepted != 1 {
		t.Errorf("affected +%d (want 2), accepted +%d (want 1)", affected-before, accepted-beforeAccepted)
	}
}

func consentVersionOf(t *testing.T, db *gorm.DB) int {
	t.Helper()
	s, err := NewRepos(db).Settings.Load(context.Background(), ports.UISettings{})
	if err != nil {
		t.Fatal(err)
	}
	return s.LegalConsentVersion
}

// The document and the consent version it announces are one transaction: the
// row and the setting land together or not at all.
func TestLegalPublishWithConsent_MovesTheVersionWithTheDocument(t *testing.T) {
	r, db := newLegalRepo(t)
	ctx := context.Background()
	step := func(bump bool, wantVersion int, wantBumped bool) {
		t.Helper()
		d, v, bumped, err := r.PublishWithConsent(ctx, pub(domain.LegalKindTerms, "zh-CN", "text", bump), bump)
		if err != nil || d == nil {
			t.Fatalf("publish: %v", err)
		}
		if v != wantVersion || bumped != wantBumped {
			t.Fatalf("bump=%v: consent version %d (bumped %v), want %d (%v)", bump, v, bumped, wantVersion, wantBumped)
		}
		if got := consentVersionOf(t, db); got != wantVersion {
			t.Fatalf("the settings read back %d, want %d", got, wantVersion)
		}
	}
	step(false, 1, true) // the first publication is always version 1
	step(false, 1, false)
	step(true, 2, true)
	step(true, 3, true)
	step(false, 3, false)
}

// A value written through the settings store (a whole-record save) is what the
// bump starts from: both writers use the one key.
func TestLegalPublishWithConsent_BumpsTheStoredValue(t *testing.T) {
	r, db := newLegalRepo(t)
	ctx := context.Background()
	if err := NewRepos(db).Settings.Save(ctx, ports.UISettings{LegalConsentVersion: 5}); err != nil {
		t.Fatal(err)
	}
	if _, v, _, err := r.PublishWithConsent(ctx, pub(domain.LegalKindTerms, "zh-CN", "x", true), true); err != nil || v != 6 {
		t.Fatalf("v = %d, %v; want 6", v, err)
	}
}

func TestLegalPublishWithConsent_RollsTheDocumentBackWhenTheVersionCannotBeWritten(t *testing.T) {
	r, db := newLegalRepo(t)
	ctx := context.Background()
	r.beforeConsentWrite = func() error { return errors.New("disk full") }
	if _, _, _, err := r.PublishWithConsent(ctx, pub(domain.LegalKindTerms, "zh-CN", "x", true), true); err == nil {
		t.Fatal("want the write error")
	}
	if _, err := r.Latest(ctx, domain.LegalKindTerms, "zh-CN"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("the document stayed published: %v", err)
	}
	if got := consentVersionOf(t, db); got != 0 {
		t.Errorf("consent version = %d after a rolled-back publication", got)
	}
}

func TestLegalPublishWithConsent_RetriesOnceOnAVersionCollision(t *testing.T) {
	r, db := newLegalRepo(t)
	ctx := context.Background()
	calls := 0
	r.beforeInsert = func(kind, locale string, version int) {
		calls++
		if calls == 1 {
			_ = db.Create(&legalDocumentRow{Kind: kind, Locale: locale, Version: version, Content: "rival", PublishedAt: time.Now().UTC()}).Error
		}
	}
	d, v, _, err := r.PublishWithConsent(ctx, pub(domain.LegalKindTerms, "zh-CN", "mine", false), false)
	if err != nil || d.Version != 2 || v != 1 || calls != 2 {
		t.Fatalf("version %v consent %d after %d attempts, err %v", d, v, calls, err)
	}
}

func TestLegalHasDocumentIn(t *testing.T) {
	r, _ := newLegalRepo(t)
	ctx := context.Background()
	if ok, err := r.HasDocumentIn(ctx, []string{"en-US", "zh-CN"}); ok || err != nil {
		t.Fatalf("empty: %v, %v", ok, err)
	}
	if _, err := r.Publish(ctx, pub(domain.LegalKindPrivacy, "ja-JP", "x", false)); err != nil {
		t.Fatal(err)
	}
	if ok, _ := r.HasDocumentIn(ctx, []string{"en-US", "zh-CN"}); ok {
		t.Error("a ja-JP document is not reachable through the fallback chain")
	}
	if _, err := r.Publish(ctx, pub(domain.LegalKindPrivacy, "en-US", "x", false)); err != nil {
		t.Fatal(err)
	}
	if ok, _ := r.HasDocumentIn(ctx, []string{"en-US", "zh-CN"}); !ok {
		t.Error("an en-US document is reachable")
	}
}

func TestLegalCountGroupsOverridingBool(t *testing.T) {
	r, db := newLegalRepo(t)
	ctx := context.Background()
	repos := NewRepos(db)
	mk := func(name string) int64 {
		g := &domain.Group{Slug: name, Name: name}
		if err := repos.Group.Create(ctx, g); err != nil {
			t.Fatalf("group %s: %v", name, err)
		}
		return g.ID
	}
	set := func(g int64, typ, name, v string) {
		t.Helper()
		if err := repos.ScopeSettings.SetOverride(ctx, "group", g, ports.ScopeOverride{Type: typ, Name: name, Value: v}); err != nil {
			t.Fatalf("override %s.%s: %v", typ, name, err)
		}
	}
	a, b, c, gone := mk(fmt.Sprintf("la-%d", time.Now().UnixNano())), mk(fmt.Sprintf("lb-%d", time.Now().UnixNano())), mk(fmt.Sprintf("lc-%d", time.Now().UnixNano())), mk(fmt.Sprintf("ld-%d", time.Now().UnixNano()))
	set(a, "risk", "devices_off", "0")    // counts
	set(a, "risk", "sub_spread_off", "0") // the same group again: counted once
	set(b, "risk", "dest_block_off", "0") // counts
	set(c, "risk", "devices_off", "1")    // switches a signal OFF: not an enabling override
	set(gone, "risk", "devices_off", "0") // its group is deleted below
	set(c, "risk", "max_devices", "0")    // not one of the asked names
	d := mk(fmt.Sprintf("le-%d", time.Now().UnixNano()))
	set(d, "risk", "devices_off", "false") // the text "false" is false too: counts
	set(d, "risk", "dest_block_off", "true")
	if err := repos.Group.Delete(ctx, gone); err != nil {
		t.Fatal(err)
	}
	names := []string{"sub_spread_off", "devices_off", "dest_block_off"}
	if n, err := r.CountGroupsOverridingBool(ctx, "risk", names, false); err != nil || n != 3 {
		t.Fatalf("counted %d groups (%v), want a, b and d", n, err)
	}
	set(c, "risk", "dest_block_off", "0")
	if n, _ := r.CountGroupsOverridingBool(ctx, "risk", names, false); n != 4 {
		t.Errorf("after c enables one: %d, want 4", n)
	}
	if n, _ := r.CountGroupsOverridingBool(ctx, "risk", nil, false); n != 0 {
		t.Errorf("no names: %d, want 0", n)
	}
}
