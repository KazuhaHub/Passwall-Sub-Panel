package sqlstore

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/schema"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
)

// The destination-policy tables are one family: definitions the admin edits
// (lists, policies, exemptions, source rules), the publish bookkeeping
// (state, snapshots) and the per-agent delivery state. These tests pin what
// the policy compiler and its admin API rely on: every column survives a round
// trip on every dialect (entries of 2 MiB included, which is past MySQL's
// 64 KiB TEXT), every DEFINITION write advances the generation in the same
// transaction, and a write nobody changed does not.

func newDestRepos(t *testing.T) (*DestRepos, *gorm.DB, context.Context) {
	t.Helper()
	db, err := openTestDB(t)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if err := ensureTestSchema(db); err != nil {
		t.Fatalf("schema: %v", err)
	}
	return NewDestRepos(db), db, context.Background()
}

func destGeneration(t *testing.T, repos *DestRepos, ctx context.Context) int64 {
	t.Helper()
	st, err := repos.State.Get(ctx)
	if err != nil {
		t.Fatalf("state: %v", err)
	}
	return st.Generation
}

func TestDestTables_AreInTheSchema(t *testing.T) {
	_, db, _ := newDestRepos(t)
	for _, table := range []string{
		"dest_lists", "dest_policies", "dest_exemptions", "dest_source_rules",
		"dest_policy_state", "dest_policy_snapshots", "dest_agent_policy",
	} {
		if !db.Migrator().HasTable(table) {
			t.Errorf("table %s was not created by EnsureSchema", table)
		}
	}
}

// The width checks the repos make before a write must equal the size tags, or
// a value one dialect refuses could be accepted by another.
func TestDestRows_WidthsMatchTheColumns(t *testing.T) {
	size := func(model any, field string) int {
		s, err := schema.Parse(model, &sync.Map{}, schema.NamingStrategy{})
		if err != nil {
			t.Fatalf("parse %T: %v", model, err)
		}
		f := s.LookUpField(field)
		if f == nil {
			t.Fatalf("%T has no field %s", model, field)
		}
		return f.Size
	}
	for _, c := range []struct {
		model any
		field string
		want  int
	}{
		{&destListRow{}, "Name", destNameWidth},
		{&destListRow{}, "Kind", destKindWidth},
		{&destListRow{}, "Side", destSideWidth},
		{&destListRow{}, "SourceURL", destURLWidth},
		{&destListRow{}, "GeositeCategory", destCategoryWidth},
		{&destListRow{}, "GeositeAttrs", destAttrsWidth},
		{&destListRow{}, "LastError", destErrorWidth},
		{&destPolicyRow{}, "Name", destNameWidth},
		{&destPolicyRow{}, "Description", destDescriptionWidth},
		{&destPolicyRow{}, "TemplateKey", destTemplateKeyWidth},
		{&destPolicyRow{}, "State", destStateWidth},
		{&destExemptionRow{}, "Reason", destReasonWidth},
	} {
		if got := size(c.model, c.field); got != c.want {
			t.Errorf("%T.%s size tag = %d, the repo checks %d", c.model, c.field, got, c.want)
		}
	}
}

// ---- lists ----

func testList(name string) *domain.DestList {
	return &domain.DestList{
		Name: name, Kind: domain.DestListCustom, Side: domain.DestSideDest,
		Entries: []byte("domain:example.com\nfull:www.example.org"), SourceText: []byte("# my list\nexample.com\nfull:www.example.org\n"),
		EntryCount: 2, ContentSHA256: strings.Repeat("a", 64),
	}
}

func TestDestListRepo_RoundTripsEveryColumn(t *testing.T) {
	repos, _, ctx := newDestRepos(t)
	fetched := time.Now().UTC().Truncate(time.Second)
	in := &domain.DestList{
		Name: "BT trackers", Kind: domain.DestListRemote, Side: domain.DestSideDest,
		SourceURL: "https://example.com/list.txt", Entries: []byte("domain:a.example\nregexp:^x\\.y$"),
		SourceText: nil, EntryCount: 2, RegexpCount: 1, ContentSHA256: strings.Repeat("b", 64),
		LastFetchedAt: &fetched, LastError: "broad_entry: keyword:ab", OwnerGroupID: 0,
	}
	if err := repos.Lists.Create(ctx, in); err != nil {
		t.Fatal(err)
	}
	if in.ID == 0 || in.CreatedAt.IsZero() || in.UpdatedAt.IsZero() {
		t.Fatalf("create did not stamp the row: %+v", in)
	}
	got, err := repos.Lists.GetByID(ctx, in.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != in.Name || got.Kind != in.Kind || got.Side != in.Side || got.SourceURL != in.SourceURL ||
		!bytes.Equal(got.Entries, in.Entries) || got.EntryCount != 2 || got.RegexpCount != 1 ||
		got.ContentSHA256 != in.ContentSHA256 || got.LastError != in.LastError {
		t.Fatalf("round trip lost a column: got %+v want %+v", got, in)
	}
	if got.LastFetchedAt == nil || !got.LastFetchedAt.Equal(fetched) {
		t.Fatalf("last_fetched_at = %v, want %v", got.LastFetchedAt, fetched)
	}

	// A list written without a side is a destination list.
	plain := testList("plain")
	plain.Side = ""
	if err := repos.Lists.Create(ctx, plain); err != nil {
		t.Fatal(err)
	}
	if back, _ := repos.Lists.GetByID(ctx, plain.ID); back.Side != domain.DestSideDest {
		t.Fatalf("side = %q, want dest", back.Side)
	}
}

// A 2 MiB entries column is the size the design budgets for (a geosite
// expansion is megabytes): MySQL's TEXT would refuse it, a longblob does not.
func TestDestListRepo_HoldsTwoMebibytesOfEntries(t *testing.T) {
	repos, _, ctx := newDestRepos(t)
	entries := bytes.Repeat([]byte("domain:abcdefghij.example.com\n"), (2<<20)/30+1)
	source := bytes.Repeat([]byte("abcdefghij.example.com\n"), (2<<20)/23+1)
	l := testList("big")
	l.Entries, l.SourceText = entries, source
	if err := repos.Lists.Create(ctx, l); err != nil {
		t.Fatalf("a 2 MiB list was refused: %v", err)
	}
	got, err := repos.Lists.GetByID(ctx, l.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got.Entries, entries) || !bytes.Equal(got.SourceText, source) {
		t.Fatalf("2 MiB columns changed on the way back: %d/%d bytes, want %d/%d", len(got.Entries), len(got.SourceText), len(entries), len(source))
	}
}

func TestDestListRepo_ListLeavesTheBigColumnsOutUnlessAsked(t *testing.T) {
	repos, _, ctx := newDestRepos(t)
	if err := repos.Lists.Create(ctx, testList("one")); err != nil {
		t.Fatal(err)
	}
	slim, err := repos.Lists.List(ctx, false)
	if err != nil || len(slim) != 1 {
		t.Fatalf("list = %v, %v", slim, err)
	}
	if len(slim[0].Entries) != 0 || len(slim[0].SourceText) != 0 || slim[0].EntryCount != 2 {
		t.Fatalf("a slim list read carried the big columns: %+v", slim[0])
	}
	full, err := repos.Lists.List(ctx, true)
	if err != nil || len(full[0].Entries) == 0 {
		t.Fatalf("a full list read lost its entries: %v, %v", full, err)
	}
}

func TestDestListRepo_RefusesWhatTheColumnsCannotHold(t *testing.T) {
	repos, _, ctx := newDestRepos(t)
	cases := map[string]func(*domain.DestList){
		"empty name":      func(l *domain.DestList) { l.Name = "" },
		"long name":       func(l *domain.DestList) { l.Name = strings.Repeat("n", destNameWidth+1) },
		"unknown kind":    func(l *domain.DestList) { l.Kind = "ftp" },
		"unknown side":    func(l *domain.DestList) { l.Side = "both" },
		"long url":        func(l *domain.DestList) { l.SourceURL = strings.Repeat("u", destURLWidth+1) },
		"long category":   func(l *domain.DestList) { l.GeositeCategory = strings.Repeat("c", destCategoryWidth+1) },
		"long attrs":      func(l *domain.DestList) { l.GeositeAttrs = strings.Repeat("a", destAttrsWidth+1) },
		"bad sha":         func(l *domain.DestList) { l.ContentSHA256 = "xyz" },
		"negative count":  func(l *domain.DestList) { l.EntryCount = -1 },
		"owner is signed": func(l *domain.DestList) { l.OwnerGroupID = -3 },
	}
	for name, mutate := range cases {
		l := testList("x")
		mutate(l)
		if err := repos.Lists.Create(ctx, l); !errors.Is(err, domain.ErrValidation) {
			t.Errorf("%s: err = %v, want ErrValidation", name, err)
		}
	}
	// A long error is shortened, not refused: it is a note, not a definition.
	l := testList("noted")
	l.LastError = strings.Repeat("e", destErrorWidth+100)
	if err := repos.Lists.Create(ctx, l); err != nil {
		t.Fatal(err)
	}
	if got, _ := repos.Lists.GetByID(ctx, l.ID); len(got.LastError) != destErrorWidth {
		t.Fatalf("last_error is %d bytes, want it cut to %d", len(got.LastError), destErrorWidth)
	}
}

// F13: a refresh that fetched the same bytes must not advance the generation,
// or every refresh would restart every core. Changed bytes must.
func TestDestListRepo_UpdateContentBumpsOnlyWhenTheDigestChanged(t *testing.T) {
	repos, _, ctx := newDestRepos(t)
	l := testList("remote")
	l.Kind = domain.DestListRemote
	l.SourceURL = "https://example.com/a.txt"
	if err := repos.Lists.Create(ctx, l); err != nil {
		t.Fatal(err)
	}
	// Only a list a policy in force uses is something the compiler can see.
	if err := repos.Policies.Create(ctx, testPolicy("uses it", domain.DestActionBlock, l.ID)); err != nil {
		t.Fatal(err)
	}
	pol, _ := repos.Policies.GetByName(ctx, "uses it")
	pol.State = domain.DestStateOn
	if err := repos.Policies.Update(ctx, pol, nil); err != nil {
		t.Fatal(err)
	}
	g0 := destGeneration(t, repos, ctx)

	same := domain.DestListContent{
		Entries: l.Entries, EntryCount: 2, ContentSHA256: l.ContentSHA256, FetchedAt: time.Now(),
	}
	changed, err := repos.Lists.UpdateContent(ctx, l.ID, same)
	if err != nil || changed {
		t.Fatalf("same content: changed=%v err=%v", changed, err)
	}
	if got := destGeneration(t, repos, ctx); got != g0 {
		t.Fatalf("an unchanged refresh moved the generation %d -> %d", g0, got)
	}
	got, _ := repos.Lists.GetByID(ctx, l.ID)
	if got.LastFetchedAt == nil {
		t.Fatal("an unchanged refresh must still record that it fetched")
	}

	next := domain.DestListContent{
		Entries: []byte("domain:new.example"), EntryCount: 1, ContentSHA256: strings.Repeat("c", 64), FetchedAt: time.Now(),
	}
	changed, err = repos.Lists.UpdateContent(ctx, l.ID, next)
	if err != nil || !changed {
		t.Fatalf("new content: changed=%v err=%v", changed, err)
	}
	if got := destGeneration(t, repos, ctx); got != g0+1 {
		t.Fatalf("generation = %d, want %d", got, g0+1)
	}
	back, _ := repos.Lists.GetByID(ctx, l.ID)
	if string(back.Entries) != "domain:new.example" || back.EntryCount != 1 || back.LastError != "" {
		t.Fatalf("content not stored or error not cleared: %+v", back)
	}
	// The pasted text and name are not the refresh's to touch.
	if back.Name != "remote" {
		t.Fatalf("name changed to %q", back.Name)
	}
}

// A refresh of a list no non-off policy uses stores the entries and moves
// nothing: no node would run a different policy because of it.
func TestDestListRepo_UpdateContentOfAnUnusedListDoesNotBump(t *testing.T) {
	repos, _, ctx := newDestRepos(t)
	l := testList("remote")
	l.Kind, l.SourceURL = domain.DestListRemote, "https://example.com/a.txt"
	if err := repos.Lists.Create(ctx, l); err != nil {
		t.Fatal(err)
	}
	off := testPolicy("off", domain.DestActionBlock, l.ID) // state off
	if err := repos.Policies.Create(ctx, off); err != nil {
		t.Fatal(err)
	}
	g0 := destGeneration(t, repos, ctx)
	changed, err := repos.Lists.UpdateContent(ctx, l.ID, domain.DestListContent{
		Entries: []byte("domain:new.example"), EntryCount: 1, ContentSHA256: strings.Repeat("c", 64), FetchedAt: time.Now(),
	})
	if err != nil || !changed || destGeneration(t, repos, ctx) != g0 {
		t.Fatalf("changed=%v err=%v generation %d -> %d", changed, err, g0, destGeneration(t, repos, ctx))
	}
}

// A geosite update lands every list it touches in one transaction, and the
// verdict is taken inside it: the generation moves only for lists a non-off
// policy uses, a Decide error aborts the lot, and notes ride along.
func TestDestListRepo_ApplyBatchDecidesInsideTheTransaction(t *testing.T) {
	repos, _, ctx := newDestRepos(t)
	mk := func(name string) *domain.DestList {
		l := testList(name)
		l.Kind, l.GeositeCategory = domain.DestListGeosite, name
		return l
	}
	used, unused, broken := mk("used"), mk("unused"), mk("broken")
	for _, l := range []*domain.DestList{used, unused, broken} {
		if err := repos.Lists.Create(ctx, l); err != nil {
			t.Fatal(err)
		}
	}
	pol := testPolicy("on", domain.DestActionBlock, used.ID)
	pol.State = domain.DestStateOn
	if err := repos.Policies.Create(ctx, pol); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	next := func(id int64, sha string) domain.DestListContentUpdate {
		return domain.DestListContentUpdate{ListID: id, DestListContent: domain.DestListContent{
			Entries: []byte("domain:" + sha + ".example"), EntryCount: 1, ContentSHA256: strings.Repeat(sha, 64), FetchedAt: now,
		}}
	}
	g0 := destGeneration(t, repos, ctx)

	var sawInUse map[int64]bool
	var sawForce []*domain.DestPolicy
	changed, bumped, err := repos.Lists.ApplyBatch(ctx, domain.DestListBatch{
		Contents: []domain.DestListContentUpdate{next(unused.ID, "e")},
		Decide: func(inUse map[int64]bool, inForce []*domain.DestPolicy) (map[int64]string, bool, error) {
			sawInUse, sawForce = inUse, inForce
			return nil, false, nil
		},
	})
	if err != nil || bumped || !reflect.DeepEqual(changed, []int64{unused.ID}) {
		t.Fatalf("unused change: changed %v bumped %v err %v", changed, bumped, err)
	}
	if !sawInUse[used.ID] || sawInUse[unused.ID] || len(sawForce) != 1 || sawForce[0].Name != "on" {
		t.Fatalf("Decide saw inUse %v inForce %v; want the policy read in the transaction", sawInUse, sawForce)
	}
	if destGeneration(t, repos, ctx) != g0 {
		t.Fatal("a change to an unused list moved the generation")
	}

	changed, bumped, err = repos.Lists.ApplyBatch(ctx, domain.DestListBatch{
		Contents: []domain.DestListContentUpdate{next(used.ID, "f"), next(unused.ID, "a")},
		Decide: func(map[int64]bool, []*domain.DestPolicy) (map[int64]string, bool, error) {
			return map[int64]string{broken.ID: "geosite_category_gone"}, false, nil
		},
	})
	if err != nil || !bumped || len(changed) != 2 || destGeneration(t, repos, ctx) != g0+1 {
		t.Fatalf("used change: changed %v bumped %v err %v generation %d", changed, bumped, err, destGeneration(t, repos, ctx))
	}
	if got, _ := repos.Lists.GetByID(ctx, broken.ID); got.LastError != "geosite_category_gone" || !bytes.Equal(got.Entries, broken.Entries) {
		t.Fatalf("the note was not recorded beside the old entries: %+v", got)
	}

	// Identical content bumps nothing; forceBump moves it regardless.
	_, bumped, _ = repos.Lists.ApplyBatch(ctx, domain.DestListBatch{Contents: []domain.DestListContentUpdate{next(used.ID, "f")}})
	if bumped || destGeneration(t, repos, ctx) != g0+1 {
		t.Fatal("identical content bumped")
	}
	if _, bumped, _ = repos.Lists.ApplyBatch(ctx, domain.DestListBatch{
		Decide: func(map[int64]bool, []*domain.DestPolicy) (map[int64]string, bool, error) { return nil, true, nil },
	}); !bumped || destGeneration(t, repos, ctx) != g0+2 {
		t.Fatal("forceBump did not bump")
	}

	// A Decide error aborts the whole batch; so does a missing list.
	before := destGeneration(t, repos, ctx)
	boom := errors.New("refused")
	if _, _, err := repos.Lists.ApplyBatch(ctx, domain.DestListBatch{
		Contents: []domain.DestListContentUpdate{next(used.ID, "9")},
		Decide:   func(map[int64]bool, []*domain.DestPolicy) (map[int64]string, bool, error) { return nil, false, boom },
	}); !errors.Is(err, boom) {
		t.Fatalf("a Decide error: %v", err)
	}
	if _, _, err := repos.Lists.ApplyBatch(ctx, domain.DestListBatch{
		Contents: []domain.DestListContentUpdate{next(used.ID, "9"), next(424242, "8")},
	}); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("a batch naming a missing list: %v", err)
	}
	if got, _ := repos.Lists.GetByID(ctx, used.ID); got.ContentSHA256 != strings.Repeat("f", 64) || destGeneration(t, repos, ctx) != before {
		t.Fatalf("a failed batch landed: %s", got.ContentSHA256)
	}
}

func TestDestListRepo_FetchErrorKeepsTheEntries(t *testing.T) {
	repos, _, ctx := newDestRepos(t)
	l := testList("remote")
	l.Kind = domain.DestListRemote
	l.SourceURL = "https://example.com/a.txt"
	if err := repos.Lists.Create(ctx, l); err != nil {
		t.Fatal(err)
	}
	g0 := destGeneration(t, repos, ctx)
	if err := repos.Lists.SetFetchError(ctx, l.ID, "dest_list_too_large"); err != nil {
		t.Fatal(err)
	}
	got, _ := repos.Lists.GetByID(ctx, l.ID)
	if got.LastError != "dest_list_too_large" || !bytes.Equal(got.Entries, l.Entries) {
		t.Fatalf("a failed refresh must keep the old entries and record why: %+v", got)
	}
	if destGeneration(t, repos, ctx) != g0 {
		t.Fatal("a failed refresh moved the generation")
	}
	if err := repos.Lists.SetFetchError(ctx, 9999, "x"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("missing list: %v", err)
	}
}

func TestDestListRepo_UpdateRewritesACustomListAndBumpsOnChange(t *testing.T) {
	repos, _, ctx := newDestRepos(t)
	l := testList("mine")
	if err := repos.Lists.Create(ctx, l); err != nil {
		t.Fatal(err)
	}
	g0 := destGeneration(t, repos, ctx)

	// Renaming alone does not change what the compiler sees.
	l.Name = "renamed"
	if err := repos.Lists.Update(ctx, l); err != nil {
		t.Fatal(err)
	}
	if destGeneration(t, repos, ctx) != g0 {
		t.Fatal("a rename moved the generation")
	}

	l.Entries, l.SourceText, l.EntryCount, l.ContentSHA256 = []byte("domain:other.example"), []byte("other.example"), 1, strings.Repeat("d", 64)
	if err := repos.Lists.Update(ctx, l); err != nil {
		t.Fatal(err)
	}
	if destGeneration(t, repos, ctx) != g0+1 {
		t.Fatalf("new entries did not advance the generation")
	}
	got, _ := repos.Lists.GetByID(ctx, l.ID)
	if got.Name != "renamed" || string(got.SourceText) != "other.example" {
		t.Fatalf("update lost: %+v", got)
	}
	if err := repos.Lists.Update(ctx, &domain.DestList{ID: 777, Name: "x", Kind: domain.DestListCustom}); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("update of a missing list: %v", err)
	}
}

func TestDestListRepo_FindGeositeReusesAnIdenticalList(t *testing.T) {
	repos, _, ctx := newDestRepos(t)
	mk := func(cat, attrs string) *domain.DestList {
		l := testList("geosite " + cat + " " + attrs)
		l.Kind, l.GeositeCategory, l.GeositeAttrs = domain.DestListGeosite, cat, attrs
		return l
	}
	a, b := mk("category-pt", ""), mk("category-pt", "cn")
	for _, l := range []*domain.DestList{a, b} {
		if err := repos.Lists.Create(ctx, l); err != nil {
			t.Fatal(err)
		}
	}
	got, err := repos.Lists.FindGeosite(ctx, "category-pt", "")
	if err != nil || got.ID != a.ID {
		t.Fatalf("category-pt with no attrs = %+v, %v; want list %d", got, err, a.ID)
	}
	got, err = repos.Lists.FindGeosite(ctx, "category-pt", "cn")
	if err != nil || got.ID != b.ID {
		t.Fatalf("category-pt@cn = %+v, %v; want list %d", got, err, b.ID)
	}
	if _, err := repos.Lists.FindGeosite(ctx, "category-pt", "!cn"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("an attr set nobody made must be not found, got %v", err)
	}
	// A list that belongs to an allowlist group is never reused by another.
	owned := mk("category-ads-all", "")
	owned.OwnerGroupID = 5
	if err := repos.Lists.Create(ctx, owned); err != nil {
		t.Fatal(err)
	}
	if _, err := repos.Lists.FindGeosite(ctx, "category-ads-all", ""); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("a group-owned list was offered for reuse: %v", err)
	}
}

func TestDestListRepo_DeleteAndEntries(t *testing.T) {
	repos, _, ctx := newDestRepos(t)
	l := testList("gone")
	if err := repos.Lists.Create(ctx, l); err != nil {
		t.Fatal(err)
	}
	entries, err := repos.Lists.Entries(ctx, l.ID)
	if err != nil || !bytes.Equal(entries, l.Entries) {
		t.Fatalf("entries = %q, %v", entries, err)
	}
	if err := repos.Lists.Delete(ctx, l.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := repos.Lists.GetByID(ctx, l.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("get after delete: %v", err)
	}
	if err := repos.Lists.Delete(ctx, l.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("second delete: %v", err)
	}
}

// ---- policies ----

func testPolicy(name string, action domain.DestAction, lists ...int64) *domain.DestPolicy {
	return &domain.DestPolicy{
		Name: name, Description: "d", Action: action, State: domain.DestStateOff, ListIDs: lists,
		Inline: domain.DestPolicyInline{CIDRs: []string{"203.0.113.0/24"}, Ports: "25,465,587", Network: "tcp", Protocols: []string{"bittorrent"}, Private: true},
		Scope:  domain.DestScopeAll,
	}
}

// makeLists creates n custom lists and returns their ids.
func makeLists(t *testing.T, repos *DestRepos, n int) []int64 {
	t.Helper()
	ids := make([]int64, 0, n)
	for i := 0; i < n; i++ {
		l := testList(fmt.Sprintf("made %d", i))
		if err := repos.Lists.Create(context.Background(), l); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, l.ID)
	}
	return ids
}

func TestDestPolicyRepo_RoundTripsEveryColumn(t *testing.T) {
	repos, _, ctx := newDestRepos(t)
	lids := makeLists(t, repos, 2)
	p := testPolicy("no bt", domain.DestActionBlock, lids...)
	p.State = domain.DestStateOn
	p.Scope = domain.DestScopePanels
	p.PanelIDs = []int64{7, 9}
	p.GroupIDs = []int64{1}
	p.CountsAsRisk = true
	p.TemplateKey = "bittorrent"
	if err := repos.Policies.Create(ctx, p); err != nil {
		t.Fatal(err)
	}
	got, err := repos.Policies.GetByID(ctx, p.ID)
	if err != nil {
		t.Fatal(err)
	}
	want := *p
	got.CreatedAt, got.UpdatedAt, want.CreatedAt, want.UpdatedAt = time.Time{}, time.Time{}, time.Time{}, time.Time{}
	if !reflect.DeepEqual(*got, want) {
		t.Fatalf("round trip:\n got %+v\nwant %+v", *got, want)
	}
	if p.Priority != 1 {
		t.Fatalf("the first policy's priority = %d, want 1", p.Priority)
	}
	byName, err := repos.Policies.GetByName(ctx, "no bt")
	if err != nil || byName.ID != p.ID {
		t.Fatalf("get by name = %+v, %v", byName, err)
	}
}

func TestDestPolicyRepo_AssignsPrioritiesPerActionAndReorders(t *testing.T) {
	repos, _, ctx := newDestRepos(t)
	a, b, c := testPolicy("a", domain.DestActionBlock), testPolicy("b", domain.DestActionBlock), testPolicy("c", domain.DestActionAllow)
	for _, p := range []*domain.DestPolicy{a, b, c} {
		if err := repos.Policies.Create(ctx, p); err != nil {
			t.Fatal(err)
		}
	}
	if a.Priority != 1 || b.Priority != 2 || c.Priority != 1 {
		t.Fatalf("priorities a=%d b=%d c=%d, want 1 2 1 (per action)", a.Priority, b.Priority, c.Priority)
	}
	g0 := destGeneration(t, repos, ctx)
	if err := repos.Policies.Reorder(ctx, domain.DestActionBlock, []int64{b.ID, a.ID}); err != nil {
		t.Fatal(err)
	}
	list, _ := repos.Policies.List(ctx)
	order := []string{}
	for _, p := range list {
		if p.Action == domain.DestActionBlock {
			order = append(order, p.Name)
		}
	}
	if !reflect.DeepEqual(order, []string{"b", "a"}) {
		t.Fatalf("block order = %v, want [b a]", order)
	}
	if destGeneration(t, repos, ctx) != g0+1 {
		t.Fatal("a reorder is a definition write and must advance the generation")
	}
	// An id of another action, an unknown id, or a missing one refuses the lot.
	if err := repos.Policies.Reorder(ctx, domain.DestActionBlock, []int64{a.ID, c.ID}); !errors.Is(err, domain.ErrValidation) {
		t.Fatalf("reorder with a foreign id: %v", err)
	}
	if err := repos.Policies.Reorder(ctx, domain.DestActionBlock, []int64{a.ID}); !errors.Is(err, domain.ErrValidation) {
		t.Fatalf("reorder that leaves a policy out: %v", err)
	}
}

func TestDestPolicyRepo_NameIsUniqueAndValidated(t *testing.T) {
	repos, _, ctx := newDestRepos(t)
	if err := repos.Policies.Create(ctx, testPolicy("dup", domain.DestActionBlock)); err != nil {
		t.Fatal(err)
	}
	if err := repos.Policies.Create(ctx, testPolicy("dup", domain.DestActionAllow)); !errors.Is(err, domain.ErrAlreadyExists) {
		t.Fatalf("duplicate name: %v, want ErrAlreadyExists", err)
	}
	for name, mutate := range map[string]func(*domain.DestPolicy){
		"empty name":      func(p *domain.DestPolicy) { p.Name = "" },
		"unknown action":  func(p *domain.DestPolicy) { p.Action = "observe" },
		"unknown state":   func(p *domain.DestPolicy) { p.State = "maybe" },
		"unknown scope":   func(p *domain.DestPolicy) { p.Scope = "some" },
		"long desc":       func(p *domain.DestPolicy) { p.Description = strings.Repeat("d", destDescriptionWidth+1) },
		"long template":   func(p *domain.DestPolicy) { p.TemplateKey = strings.Repeat("k", destTemplateKeyWidth+1) },
		"groups no ids":   func(p *domain.DestPolicy) { p.Scope = domain.DestScopeGroups },
		"panels no ids":   func(p *domain.DestPolicy) { p.Scope = domain.DestScopePanels },
		"negative prio":   func(p *domain.DestPolicy) { p.Priority = -1 },
		"zero list id":    func(p *domain.DestPolicy) { p.ListIDs = []int64{0} },
		"duplicate lists": func(p *domain.DestPolicy) { p.ListIDs = []int64{4, 4} },
	} {
		p := testPolicy("v-"+name, domain.DestActionBlock)
		mutate(p)
		if err := repos.Policies.Create(ctx, p); !errors.Is(err, domain.ErrValidation) {
			t.Errorf("%s: err = %v, want ErrValidation", name, err)
		}
	}
}

func TestDestPolicyRepo_WritesAdvanceTheGenerationInTheirOwnTransaction(t *testing.T) {
	repos, db, ctx := newDestRepos(t)
	g0 := destGeneration(t, repos, ctx)
	p := testPolicy("p", domain.DestActionBlock)
	if err := repos.Policies.Create(ctx, p); err != nil {
		t.Fatal(err)
	}
	if got := destGeneration(t, repos, ctx); got != g0+1 {
		t.Fatalf("create: generation %d, want %d", got, g0+1)
	}
	st, _ := repos.State.Get(ctx)
	if st.FirstUnpublishedAt == nil || st.LastWriteAt == nil {
		t.Fatalf("a write must stamp first_unpublished_at and last_write_at: %+v", st)
	}
	first := *st.FirstUnpublishedAt

	time.Sleep(5 * time.Millisecond)
	p.State = domain.DestStateOn
	if err := repos.Policies.Update(ctx, p, nil); err != nil {
		t.Fatal(err)
	}
	st, _ = repos.State.Get(ctx)
	if st.Generation != g0+2 || !st.FirstUnpublishedAt.Equal(first) {
		t.Fatalf("update: generation %d first_unpublished_at moved %v -> %v (must keep the FIRST)", st.Generation, first, st.FirstUnpublishedAt)
	}
	if err := repos.Policies.Delete(ctx, p.ID); err != nil {
		t.Fatal(err)
	}
	if got := destGeneration(t, repos, ctx); got != g0+3 {
		t.Fatalf("delete: generation %d, want %d", got, g0+3)
	}

	// The bump is in the SAME transaction: when the write rolls back, so does
	// the bump. Force the policy insert to fail after the bump would have run.
	before := destGeneration(t, repos, ctx)
	if err := db.Callback().Create().Before("gorm:create").Register("test:fail_dest_policy", func(tx *gorm.DB) {
		if tx.Statement.Table == "dest_policies" {
			_ = tx.AddError(errors.New("forced"))
		}
	}); err != nil {
		t.Fatal(err)
	}
	defer db.Callback().Create().Remove("test:fail_dest_policy")
	if err := repos.Policies.Create(ctx, testPolicy("rolled back", domain.DestActionBlock)); err == nil {
		t.Fatal("the forced failure did not surface")
	}
	if got := destGeneration(t, repos, ctx); got != before {
		t.Fatalf("a rolled-back write advanced the generation %d -> %d", before, got)
	}
}

func TestDestPolicyRepo_UpdateHonoursThePrecondition(t *testing.T) {
	repos, _, ctx := newDestRepos(t)
	p := testPolicy("cas", domain.DestActionBlock)
	if err := repos.Policies.Create(ctx, p); err != nil {
		t.Fatal(err)
	}
	loaded, _ := repos.Policies.GetByID(ctx, p.ID)
	stale := loaded.UpdatedAt

	time.Sleep(5 * time.Millisecond)
	loaded.Description = "first writer"
	if err := repos.Policies.Update(ctx, loaded, &stale); err != nil {
		t.Fatalf("first writer: %v", err)
	}
	second := *loaded
	second.Description = "second writer"
	if err := repos.Policies.Update(ctx, &second, &stale); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("a writer holding the old updated_at: %v, want ErrConflict", err)
	}
	got, _ := repos.Policies.GetByID(ctx, p.ID)
	if got.Description != "first writer" {
		t.Fatalf("a conflicting write was applied: %q", got.Description)
	}
	if err := repos.Policies.Update(ctx, &domain.DestPolicy{ID: 4242, Name: "x", Action: domain.DestActionBlock, State: domain.DestStateOff, Scope: domain.DestScopeAll}, nil); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("update of a missing policy: %v", err)
	}
	// Renaming onto another policy's name is a conflict, not a 500.
	other := testPolicy("other", domain.DestActionBlock)
	if err := repos.Policies.Create(ctx, other); err != nil {
		t.Fatal(err)
	}
	renamed, _ := repos.Policies.GetByID(ctx, p.ID)
	renamed.Name = "other"
	if err := repos.Policies.Update(ctx, renamed, nil); !errors.Is(err, domain.ErrAlreadyExists) {
		t.Fatalf("rename onto an existing name: %v", err)
	}
}

func TestDestPolicyRepo_PolicyIDsUsingList(t *testing.T) {
	repos, _, ctx := newDestRepos(t)
	lids := makeLists(t, repos, 2)
	a := testPolicy("a", domain.DestActionBlock, lids[0], lids[1])
	b := testPolicy("b", domain.DestActionAllow, lids[1])
	c := testPolicy("c", domain.DestActionBlock)
	for _, p := range []*domain.DestPolicy{a, b, c} {
		if err := repos.Policies.Create(ctx, p); err != nil {
			t.Fatal(err)
		}
	}
	got, err := repos.Policies.UsingList(ctx, lids[1])
	if err != nil || !reflect.DeepEqual(got, []int64{a.ID, b.ID}) {
		t.Fatalf("using list = %v, %v; want [%d %d]", got, err, a.ID, b.ID)
	}
	got, _ = repos.Policies.UsingList(ctx, 99999)
	if len(got) != 0 {
		t.Fatalf("using list 99 = %v", got)
	}
}

// ---- instantiate (lists + policy in one transaction) ----

func TestDestRepos_InstantiateReusesIdenticalGeositeLists(t *testing.T) {
	repos, _, ctx := newDestRepos(t)
	geo := func(cat string) domain.DestList {
		return domain.DestList{
			Name: "geosite:" + cat, Kind: domain.DestListGeosite, Side: domain.DestSideDest, GeositeCategory: cat,
			Entries: []byte("domain:" + cat + ".example"), EntryCount: 1, ContentSHA256: strings.Repeat("e", 64),
		}
	}
	now := time.Now()
	first := geo("category-pt")
	first.LastFetchedAt = &now
	existing := first
	if err := repos.Lists.Create(ctx, &existing); err != nil {
		t.Fatal(err)
	}

	pol := testPolicy("PT sites", domain.DestActionBlock)
	pol.TemplateKey = "pt"
	got, err := repos.InstantiateTemplate(ctx, []domain.DestList{first, geo("category-public-tracker")}, pol)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.ListIDs) != 2 || got.ListIDs[0] != existing.ID {
		t.Fatalf("list ids = %v; the identical existing list %d must be reused, in the order given", got.ListIDs, existing.ID)
	}
	all, _ := repos.Lists.List(ctx, false)
	if len(all) != 2 {
		t.Fatalf("%d lists, want 2 (one reused, one created)", len(all))
	}
	if pol.ID == 0 {
		t.Fatal("the policy was not created")
	}

	// A second instantiation of the same template is refused.
	again := testPolicy("PT sites 2", domain.DestActionBlock)
	again.TemplateKey = "pt"
	if _, err := repos.InstantiateTemplate(ctx, []domain.DestList{first}, again); !errors.Is(err, domain.ErrAlreadyExists) {
		t.Fatalf("second instantiate: %v, want ErrAlreadyExists", err)
	}
}

func TestDestRepos_InstantiateIsAtomic(t *testing.T) {
	repos, _, ctx := newDestRepos(t)
	if err := repos.Policies.Create(ctx, testPolicy("taken", domain.DestActionBlock)); err != nil {
		t.Fatal(err)
	}
	g0 := destGeneration(t, repos, ctx)
	l := domain.DestList{
		Name: "geosite:category-x", Kind: domain.DestListGeosite, Side: domain.DestSideDest, GeositeCategory: "category-x",
		Entries: []byte("domain:x.example"), EntryCount: 1, ContentSHA256: strings.Repeat("e", 64),
	}
	pol := testPolicy("taken", domain.DestActionBlock) // the name collides, so the policy insert fails
	if _, err := repos.InstantiateTemplate(ctx, []domain.DestList{l}, pol); !errors.Is(err, domain.ErrAlreadyExists) {
		t.Fatalf("err = %v, want ErrAlreadyExists", err)
	}
	lists, _ := repos.Lists.List(ctx, false)
	if len(lists) != 0 {
		t.Fatalf("a failed instantiate left %d list(s) behind", len(lists))
	}
	if destGeneration(t, repos, ctx) != g0 {
		t.Fatal("a failed instantiate advanced the generation")
	}
}

// ---- exemptions ----

func TestDestExemptionRepo_UpsertDeleteAndExpire(t *testing.T) {
	repos, _, ctx := newDestRepos(t)
	g0 := destGeneration(t, repos, ctx)
	soon := time.Now().Add(-time.Minute).UTC().Truncate(time.Second)
	if err := repos.Exemptions.Upsert(ctx, &domain.DestExemption{UserID: 5, Reason: "ops", CreatedBy: 1, ExpiresAt: &soon}); err != nil {
		t.Fatal(err)
	}
	if err := repos.Exemptions.Upsert(ctx, &domain.DestExemption{UserID: 6, Reason: "forever", CreatedBy: 1}); err != nil {
		t.Fatal(err)
	}
	if got := destGeneration(t, repos, ctx); got != g0+2 {
		t.Fatalf("generation %d, want %d", got, g0+2)
	}
	// Upsert replaces.
	if err := repos.Exemptions.Upsert(ctx, &domain.DestExemption{UserID: 6, Reason: "changed", CreatedBy: 2}); err != nil {
		t.Fatal(err)
	}
	got, err := repos.Exemptions.Get(ctx, 6)
	if err != nil || got.Reason != "changed" || got.CreatedBy != 2 || got.ExpiresAt != nil {
		t.Fatalf("get = %+v, %v", got, err)
	}
	all, _ := repos.Exemptions.List(ctx)
	if len(all) != 2 || all[0].UserID != 5 || all[0].ExpiresAt == nil || !all[0].ExpiresAt.Equal(soon) {
		t.Fatalf("list = %+v", all)
	}

	before := destGeneration(t, repos, ctx)
	n, err := repos.Exemptions.DeleteExpired(ctx, time.Now())
	if err != nil || n != 1 {
		t.Fatalf("delete expired = %d, %v", n, err)
	}
	if destGeneration(t, repos, ctx) != before+1 {
		t.Fatal("expiring an exemption is a definition write")
	}
	n, _ = repos.Exemptions.DeleteExpired(ctx, time.Now())
	if n != 0 || destGeneration(t, repos, ctx) != before+1 {
		t.Fatal("nothing expired must change nothing")
	}
	if err := repos.Exemptions.Delete(ctx, 6); err != nil {
		t.Fatal(err)
	}
	if err := repos.Exemptions.Delete(ctx, 6); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("second delete: %v", err)
	}
	if err := repos.Exemptions.Upsert(ctx, &domain.DestExemption{UserID: 0}); !errors.Is(err, domain.ErrValidation) {
		t.Fatalf("exemption without a user: %v", err)
	}
	if err := repos.Exemptions.Upsert(ctx, &domain.DestExemption{UserID: 9, Reason: strings.Repeat("r", destReasonWidth+1)}); !errors.Is(err, domain.ErrValidation) {
		t.Fatalf("long reason: %v", err)
	}
}

// ---- source rules ----

func TestDestSourceRuleRepo_UpsertGetListAndCanonicalForm(t *testing.T) {
	repos, _, ctx := newDestRepos(t)
	g0 := destGeneration(t, repos, ctx)
	r := &domain.DestSourceRule{NodeID: 12, Countries: []string{"cn", "RU", "cn"}, CIDRs: []string{"198.51.100.0/24", "203.0.113.0/24"}, State: domain.DestStateOn}
	if err := repos.SourceRules.Upsert(ctx, r, nil); err != nil {
		t.Fatal(err)
	}
	got, err := repos.SourceRules.Get(ctx, 12)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got.Countries, []string{"CN", "RU"}) || !reflect.DeepEqual(got.CIDRs, []string{"198.51.100.0/24", "203.0.113.0/24"}) || got.State != domain.DestStateOn {
		t.Fatalf("stored = %+v; countries must be upper-case, sorted and unique", got)
	}
	if destGeneration(t, repos, ctx) != g0+1 {
		t.Fatal("a source rule write advances the generation")
	}

	for name, bad := range map[string]*domain.DestSourceRule{
		"no node":         {NodeID: 0, State: domain.DestStateOn},
		"bad state":       {NodeID: 1, State: "x"},
		"bad country":     {NodeID: 1, State: domain.DestStateOn, Countries: []string{"CHN"}},
		"unmasked cidr":   {NodeID: 1, State: domain.DestStateOn, CIDRs: []string{"198.51.100.7/24"}},
		"not a cidr":      {NodeID: 1, State: domain.DestStateOn, CIDRs: []string{"nope"}},
		"unsorted cidrs":  {NodeID: 1, State: domain.DestStateOn, CIDRs: []string{"203.0.113.0/24", "198.51.100.0/24"}},
		"duplicate cidrs": {NodeID: 1, State: domain.DestStateOn, CIDRs: []string{"198.51.100.0/24", "198.51.100.0/24"}},
	} {
		if err := repos.SourceRules.Upsert(ctx, bad, nil); !errors.Is(err, domain.ErrValidation) {
			t.Errorf("%s: %v, want ErrValidation", name, err)
		}
	}

	if err := repos.SourceRules.Upsert(ctx, &domain.DestSourceRule{NodeID: 3, Countries: []string{"US"}, State: domain.DestStateOff}, nil); err != nil {
		t.Fatal(err)
	}
	all, err := repos.SourceRules.List(ctx)
	if err != nil || len(all) != 2 || all[0].NodeID != 3 || all[1].NodeID != 12 {
		t.Fatalf("list = %+v, %v; want node-id order", all, err)
	}
	if _, err := repos.SourceRules.Get(ctx, 404); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("get missing: %v", err)
	}
}

func TestDestSourceRuleRepo_Precondition(t *testing.T) {
	repos, _, ctx := newDestRepos(t)
	r := &domain.DestSourceRule{NodeID: 1, Countries: []string{"CN"}, State: domain.DestStateOn}
	if err := repos.SourceRules.Upsert(ctx, r, nil); err != nil {
		t.Fatal(err)
	}
	first, _ := repos.SourceRules.Get(ctx, 1)
	stale := first.UpdatedAt
	time.Sleep(5 * time.Millisecond)
	if err := repos.SourceRules.Upsert(ctx, &domain.DestSourceRule{NodeID: 1, Countries: []string{"RU"}, State: domain.DestStateOn}, &stale); err != nil {
		t.Fatalf("matching precondition: %v", err)
	}
	if err := repos.SourceRules.Upsert(ctx, &domain.DestSourceRule{NodeID: 1, Countries: []string{"US"}, State: domain.DestStateOn}, &stale); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("stale precondition: %v, want ErrConflict", err)
	}
	// A precondition on a row that does not exist yet is also a conflict: the
	// caller believed it was editing something.
	ghost := time.Now()
	if err := repos.SourceRules.Upsert(ctx, &domain.DestSourceRule{NodeID: 50, State: domain.DestStateOn}, &ghost); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("precondition on a new row: %v, want ErrConflict", err)
	}
}

func TestDeletingANodeDeletesItsSourceRule(t *testing.T) {
	repos, db, ctx := newDestRepos(t)
	nodes := &nodeRepo{db: db}
	keep := &domain.Node{PanelID: 1, InboundID: 1, DisplayName: "keep"}
	drop := &domain.Node{PanelID: 1, InboundID: 2, DisplayName: "drop"}
	for _, n := range []*domain.Node{keep, drop} {
		if err := nodes.Create(ctx, n); err != nil {
			t.Fatal(err)
		}
		if err := repos.SourceRules.Upsert(ctx, &domain.DestSourceRule{NodeID: n.ID, Countries: []string{"CN"}, State: domain.DestStateOn}, nil); err != nil {
			t.Fatal(err)
		}
	}
	g0 := destGeneration(t, repos, ctx)
	if err := nodes.Delete(ctx, drop.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := repos.SourceRules.Get(ctx, drop.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("the deleted node's source rule remained: %v", err)
	}
	if _, err := repos.SourceRules.Get(ctx, keep.ID); err != nil {
		t.Fatalf("another node's rule was removed: %v", err)
	}
	if destGeneration(t, repos, ctx) != g0+1 {
		t.Fatal("removing an enforced source rule is a definition write")
	}
	// A node without a rule deletes quietly and moves nothing.
	plain := &domain.Node{PanelID: 1, InboundID: 3, DisplayName: "plain"}
	if err := nodes.Create(ctx, plain); err != nil {
		t.Fatal(err)
	}
	g1 := destGeneration(t, repos, ctx)
	if err := nodes.Delete(ctx, plain.ID); err != nil {
		t.Fatal(err)
	}
	if destGeneration(t, repos, ctx) != g1 {
		t.Fatal("deleting a node with no source rule moved the generation")
	}
}

// ---- state and snapshots ----

func TestDestStateRepo_GenerationLifecycle(t *testing.T) {
	repos, _, ctx := newDestRepos(t)
	st, err := repos.State.Get(ctx)
	if err != nil || st.Generation != 0 || st.PublishedGeneration != 0 || st.Unpublished() {
		t.Fatalf("a fresh state = %+v, %v; want everything zero", st, err)
	}
	for i := 0; i < 3; i++ {
		if err := repos.State.Bump(ctx); err != nil {
			t.Fatal(err)
		}
	}
	st, _ = repos.State.Get(ctx)
	if st.Generation != 3 || !st.Unpublished() || st.FirstUnpublishedAt == nil {
		t.Fatalf("after three bumps: %+v", st)
	}

	// Publishing generation 2 while 3 exists leaves the state unpublished and
	// keeps the first-unpublished mark: the write that made 3 is still waiting.
	ok, err := repos.State.CompletePublish(ctx, 2, 0)
	if err != nil || !ok {
		t.Fatalf("publish 2: %v %v", ok, err)
	}
	st, _ = repos.State.Get(ctx)
	if st.PublishedGeneration != 2 || !st.Unpublished() || st.FirstUnpublishedAt == nil || st.PublishedAt == nil {
		t.Fatalf("after publishing 2 of 3: %+v", st)
	}
	// The compare-and-set: a publisher that read the old published generation
	// loses.
	ok, err = repos.State.CompletePublish(ctx, 3, 0)
	if err != nil || ok {
		t.Fatalf("a stale publisher won: %v %v", ok, err)
	}
	ok, err = repos.State.CompletePublish(ctx, 3, 2)
	if err != nil || !ok {
		t.Fatalf("publish 3: %v %v", ok, err)
	}
	st, _ = repos.State.Get(ctx)
	if st.PublishedGeneration != 3 || st.Unpublished() || st.FirstUnpublishedAt != nil {
		t.Fatalf("fully published: %+v; first_unpublished_at must clear", st)
	}
}

func TestDestStateRepo_PausedAndPublishError(t *testing.T) {
	repos, _, ctx := newDestRepos(t)
	if err := repos.State.SetPaused(ctx, true); err != nil {
		t.Fatal(err)
	}
	st, _ := repos.State.Get(ctx)
	if !st.Paused {
		t.Fatal("paused did not stick")
	}
	g := st.Generation
	if err := repos.State.SetPaused(ctx, true); err != nil {
		t.Fatal(err)
	}
	if got, _ := repos.State.Get(ctx); got.Generation != g {
		t.Fatal("pausing must not touch the generation: it is not a definition")
	}

	at := time.Now().UTC().Truncate(time.Second)
	if err := repos.State.SetPublishError(ctx, &domain.DestPublishError{Kind: "domains", Used: 60000, Limit: 50000}, at); err != nil {
		t.Fatal(err)
	}
	st, _ = repos.State.Get(ctx)
	if st.PublishError == nil || st.PublishError.Kind != "domains" || st.PublishError.Used != 60000 || st.PublishErrorAt == nil {
		t.Fatalf("publish error = %+v", st)
	}
	// A successful publish clears it.
	if err := repos.State.Bump(ctx); err != nil {
		t.Fatal(err)
	}
	if ok, err := repos.State.CompletePublish(ctx, 1, 0); err != nil || !ok {
		t.Fatal(ok, err)
	}
	st, _ = repos.State.Get(ctx)
	if st.PublishError != nil || st.PublishErrorAt != nil {
		t.Fatalf("a successful publish left the error: %+v", st)
	}
}

func TestDestStateRepo_Snapshots(t *testing.T) {
	repos, _, ctx := newDestRepos(t)
	big := bytes.Repeat([]byte("x"), 3<<20)
	for g, body := range map[int64][]byte{1: []byte(`{"a":1}`), 2: []byte(`{"a":2}`), 3: big} {
		if err := repos.State.SaveSnapshot(ctx, g, body); err != nil {
			t.Fatalf("save %d: %v", g, err)
		}
	}
	got, err := repos.State.Snapshot(ctx, 3)
	if err != nil || !bytes.Equal(got.Body, big) || got.Generation != 3 || got.CreatedAt.IsZero() {
		t.Fatalf("snapshot 3 = %d bytes, %v", len(got.Body), err)
	}
	// Saving the same generation again keeps the first body: a generation's
	// snapshot is immutable.
	if err := repos.State.SaveSnapshot(ctx, 1, []byte(`{"a":"again"}`)); err != nil {
		t.Fatal(err)
	}
	if got, _ := repos.State.Snapshot(ctx, 1); string(got.Body) != `{"a":1}` {
		t.Fatalf("snapshot 1 was overwritten: %s", got.Body)
	}
	n, err := repos.State.PruneSnapshotsExcept(ctx, 3, 2)
	if err != nil || n != 1 {
		t.Fatalf("prune = %d, %v", n, err)
	}
	if _, err := repos.State.Snapshot(ctx, 1); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("pruned snapshot: %v", err)
	}
	if _, err := repos.State.Snapshot(ctx, 2); err != nil {
		t.Fatalf("kept snapshot: %v", err)
	}
}

// ---- agent policy ----

func TestDestAgentPolicyRepo_PatchWritesOnlyTheGivenColumns(t *testing.T) {
	repos, _, ctx := newDestRepos(t)
	if _, err := repos.Agents.Get(ctx, "agt_1"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("get of an unknown agent: %v", err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	desired, minted := strings.Repeat("a", 64), strings.Repeat("b", 64)
	if err := repos.Agents.Patch(ctx, "agt_1", domain.DestAgentPolicyPatch{
		DesiredSHA256: &desired, MintedSHA256: &minted, MintedAt: &now,
	}); err != nil {
		t.Fatal(err)
	}
	applied := []byte(`{"rules":[]}`)
	appliedSHA, groups := strings.Repeat("c", 64), []int64{4, 5}
	rules := 3
	if err := repos.Agents.Patch(ctx, "agt_1", domain.DestAgentPolicyPatch{
		AppliedSHA256: &appliedSHA, AppliedBody: &applied, AppliedAt: &now, AppliedRuleCount: &rules, AppliedGroups: &groups,
	}); err != nil {
		t.Fatal(err)
	}
	reason, over := domain.DestFallbackOverLimit, `{"kind":"subjects","used":9,"limit":8}`
	state, issue := "rejected", "destination_policy_rejected"
	if err := repos.Agents.Patch(ctx, "agt_1", domain.DestAgentPolicyPatch{
		FallbackReason: &reason, OverLimit: &over, ReportedState: &state, ReportedIssue: &issue, ReportedAt: &now,
	}); err != nil {
		t.Fatal(err)
	}
	got, err := repos.Agents.Get(ctx, "agt_1")
	if err != nil {
		t.Fatal(err)
	}
	// Earlier columns survive later patches (column-level writes).
	if got.DesiredSHA256 != desired || got.MintedSHA256 != minted || got.MintedAt == nil || !got.MintedAt.Equal(now) {
		t.Fatalf("the first patch was lost: %+v", got)
	}
	if got.AppliedSHA256 != appliedSHA || !bytes.Equal(got.AppliedBody, applied) || got.AppliedRuleCount != 3 || !reflect.DeepEqual(got.AppliedGroups, groups) {
		t.Fatalf("the applied record: %+v", got)
	}
	if got.FallbackReason != domain.DestFallbackOverLimit || got.OverLimit != over || got.ReportedState != "rejected" || got.ReportedIssue != issue {
		t.Fatalf("the fallback record: %+v", got)
	}
	if got.CollectEffective != "" {
		t.Fatalf("collect_effective = %q, want the empty default", got.CollectEffective)
	}

	// An empty body CLEARS the LKG (the "LKG itself rejected" case): a pointer
	// to a nil slice is a write, a nil pointer is not.
	var none []byte
	empty := ""
	zero := 0
	if err := repos.Agents.Patch(ctx, "agt_1", domain.DestAgentPolicyPatch{AppliedBody: &none, AppliedSHA256: &empty, AppliedRuleCount: &zero}); err != nil {
		t.Fatal(err)
	}
	got, _ = repos.Agents.Get(ctx, "agt_1")
	if len(got.AppliedBody) != 0 || got.AppliedSHA256 != "" || got.DesiredSHA256 != desired {
		t.Fatalf("clearing the LKG: %+v", got)
	}

	// Too wide is refused.
	long := strings.Repeat("x", 65)
	if err := repos.Agents.Patch(ctx, "agt_1", domain.DestAgentPolicyPatch{DesiredSHA256: &long}); !errors.Is(err, domain.ErrValidation) {
		t.Fatalf("a 65-byte digest: %v", err)
	}
	badReason := domain.DestFallbackReason("exploded")
	if err := repos.Agents.Patch(ctx, "agt_1", domain.DestAgentPolicyPatch{FallbackReason: &badReason}); !errors.Is(err, domain.ErrValidation) {
		t.Fatalf("an unknown fallback reason: %v", err)
	}
	if err := repos.Agents.Patch(ctx, "", domain.DestAgentPolicyPatch{}); !errors.Is(err, domain.ErrValidation) {
		t.Fatalf("no agent id: %v", err)
	}

	if err := repos.Agents.Delete(ctx, "agt_1"); err != nil {
		t.Fatal(err)
	}
	if _, err := repos.Agents.Get(ctx, "agt_1"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("get after delete: %v", err)
	}
	if err := repos.Agents.Delete(ctx, "agt_1"); err != nil {
		t.Fatalf("deleting a row that is not there is not an error: %v", err)
	}
}

// A native panel's removal deletes its policy-delivery row in the same
// transaction as the agent identity: nothing else would find it again.
func TestDeletingANativePanelDeletesItsAgentPolicyRow(t *testing.T) {
	repos, db, ctx := newDestRepos(t)
	all := NewRepos(db)
	panel := &domain.XUIPanel{Kind: domain.PanelKindPSP, Name: "native-dest", URL: "psp://agt_dest_1"}
	agent := &domain.NodeAgent{AgentID: "agt_dest_1", CredentialSHA256: strings.Repeat("a", 64), DesiredCoreVersion: "26.6.27"}
	if err := all.NativeAgentProvisioning.Create(ctx, panel, agent); err != nil {
		t.Fatal(err)
	}
	other := &domain.XUIPanel{Kind: domain.PanelKindPSP, Name: "native-other", URL: "psp://agt_dest_2"}
	otherAgent := &domain.NodeAgent{AgentID: "agt_dest_2", CredentialSHA256: strings.Repeat("b", 64), DesiredCoreVersion: "26.6.27"}
	if err := all.NativeAgentProvisioning.Create(ctx, other, otherAgent); err != nil {
		t.Fatal(err)
	}
	sha := strings.Repeat("d", 64)
	for _, id := range []string{agent.AgentID, otherAgent.AgentID} {
		if err := repos.Agents.Patch(ctx, id, domain.DestAgentPolicyPatch{DesiredSHA256: &sha}); err != nil {
			t.Fatal(err)
		}
	}

	now := time.Now().UTC()
	empty, _, err := all.NodeAgent.MintStream(ctx, agent.AgentID, domain.NodeAgentStreamConfig, []byte(`{"listeners":[],"coverage":{}}`), now)
	if err != nil {
		t.Fatal(err)
	}
	if err := all.NodeAgent.RecordApplied(ctx, agent.AgentID, domain.NodeAgentStreamConfig, agent.Epoch, empty.DesiredVersion, empty.DesiredETag, now); err != nil {
		t.Fatal(err)
	}
	if err := all.NativeAgentProvisioning.DeleteConverged(ctx, panel.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := repos.Agents.Get(ctx, agent.AgentID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("the deleted panel's agent policy row remained: %v", err)
	}
	if _, err := repos.Agents.Get(ctx, otherAgent.AgentID); err != nil {
		t.Fatalf("another agent's row was removed: %v", err)
	}
}

// ---- review fixes ----

// Names are unique case-insensitively on EVERY dialect: MySQL's collation
// already is, PostgreSQL's and SQLite's are not, so the fold is done in Go.
func TestDestPolicyRepo_NamesAreFoldedForUniqueness(t *testing.T) {
	repos, _, ctx := newDestRepos(t)
	if err := repos.Policies.Create(ctx, testPolicy("Block BT", domain.DestActionBlock)); err != nil {
		t.Fatal(err)
	}
	if err := repos.Policies.Create(ctx, testPolicy("block bt", domain.DestActionBlock)); !errors.Is(err, domain.ErrAlreadyExists) {
		t.Fatalf("a case variant: %v", err)
	}
	other := testPolicy("other", domain.DestActionBlock)
	if err := repos.Policies.Create(ctx, other); err != nil {
		t.Fatal(err)
	}
	other.Name = "BLOCK BT"
	if err := repos.Policies.Update(ctx, other, nil); !errors.Is(err, domain.ErrAlreadyExists) {
		t.Fatalf("renaming onto a case variant: %v", err)
	}
	// Renaming a policy to its own name in another case is fine.
	first, _ := repos.Policies.GetByName(ctx, "Block BT")
	first.Name = "BLOCK BT"
	if err := repos.Policies.Update(ctx, first, nil); err != nil {
		t.Fatalf("recasing its own name: %v", err)
	}
}

// What Create hands back is what a read returns: the timestamps are the ones
// the database kept (milliseconds), because the next PUT's precondition is
// compared against them.
func TestDestRepos_CreateReturnsTheStoredTimestamps(t *testing.T) {
	repos, _, ctx := newDestRepos(t)
	p := testPolicy("ts", domain.DestActionBlock)
	if err := repos.Policies.Create(ctx, p); err != nil {
		t.Fatal(err)
	}
	got, _ := repos.Policies.GetByID(ctx, p.ID)
	if !p.UpdatedAt.Equal(got.UpdatedAt) || !p.CreatedAt.Equal(got.CreatedAt) {
		t.Fatalf("created %v/%v, stored %v/%v", p.CreatedAt, p.UpdatedAt, got.CreatedAt, got.UpdatedAt)
	}
	l := testList("ts")
	if err := repos.Lists.Create(ctx, l); err != nil {
		t.Fatal(err)
	}
	gl, _ := repos.Lists.GetByID(ctx, l.ID)
	if !l.UpdatedAt.Equal(gl.UpdatedAt) {
		t.Fatalf("list updated_at %v vs stored %v", l.UpdatedAt, gl.UpdatedAt)
	}
	inst := testPolicy("inst", domain.DestActionBlock)
	inst.TemplateKey = "k"
	if _, err := repos.InstantiateTemplate(ctx, nil, inst); err != nil {
		t.Fatal(err)
	}
	gi, _ := repos.Policies.GetByID(ctx, inst.ID)
	if !inst.UpdatedAt.Equal(gi.UpdatedAt) {
		t.Fatalf("instantiated updated_at %v vs stored %v", inst.UpdatedAt, gi.UpdatedAt)
	}
}

// Name and description are not compiled, and a reorder that changes nothing
// changes nothing: neither may restart the fleet.
func TestDestPolicyRepo_EditsTheCompilerCannotSeeDoNotBump(t *testing.T) {
	repos, _, ctx := newDestRepos(t)
	a, b := testPolicy("a", domain.DestActionBlock), testPolicy("b", domain.DestActionBlock)
	for _, p := range []*domain.DestPolicy{a, b} {
		if err := repos.Policies.Create(ctx, p); err != nil {
			t.Fatal(err)
		}
	}
	g := destGeneration(t, repos, ctx)
	cur, _ := repos.Policies.GetByID(ctx, a.ID)
	cur.Name, cur.Description = "renamed", "a new description"
	if err := repos.Policies.Update(ctx, cur, nil); err != nil {
		t.Fatal(err)
	}
	if got, _ := repos.Policies.GetByID(ctx, a.ID); got.Name != "renamed" || got.Description != "a new description" {
		t.Fatalf("the edit was not stored: %+v", got)
	}
	if destGeneration(t, repos, ctx) != g {
		t.Fatal("a rename moved the generation")
	}
	if err := repos.Policies.Reorder(ctx, domain.DestActionBlock, []int64{a.ID, b.ID}); err != nil {
		t.Fatal(err)
	}
	if destGeneration(t, repos, ctx) != g {
		t.Fatal("a reorder that changed nothing moved the generation")
	}
	cur.State = domain.DestStateOn
	if err := repos.Policies.Update(ctx, cur, nil); err != nil || destGeneration(t, repos, ctx) != g+1 {
		t.Fatalf("a state change must bump: %v generation %d", err, destGeneration(t, repos, ctx))
	}
	// counts_as_risk is read by the risk signal only: it does not bump.
	cur.CountsAsRisk = true
	if err := repos.Policies.Update(ctx, cur, nil); err != nil || destGeneration(t, repos, ctx) != g+1 {
		t.Fatalf("a counts_as_risk edit must not bump: %v generation %d", err, destGeneration(t, repos, ctx))
	}
	cur.Inline.Ports = "26"
	if err := repos.Policies.Update(ctx, cur, nil); err != nil || destGeneration(t, repos, ctx) != g+2 {
		t.Fatalf("a compiled field must bump: %v", err)
	}
}

func TestDestListRepo_DeleteUnusedChecksAndDeletesTogether(t *testing.T) {
	repos, _, ctx := newDestRepos(t)
	l := testList("l")
	if err := repos.Lists.Create(ctx, l); err != nil {
		t.Fatal(err)
	}
	p := testPolicy("p", domain.DestActionBlock, l.ID)
	if err := repos.Policies.Create(ctx, p); err != nil {
		t.Fatal(err)
	}
	using, err := repos.Lists.DeleteUnused(ctx, l.ID)
	if err != nil || !reflect.DeepEqual(using.PolicyIDs, []int64{p.ID}) {
		t.Fatalf("DeleteUnused of a used list = %v, %v", using, err)
	}
	if _, err := repos.Lists.GetByID(ctx, l.ID); err != nil {
		t.Fatalf("a used list was deleted: %v", err)
	}
	if err := repos.Policies.Delete(ctx, p.ID); err != nil {
		t.Fatal(err)
	}
	if using, err := repos.Lists.DeleteUnused(ctx, l.ID); err != nil || using.Any() {
		t.Fatalf("DeleteUnused = %v, %v", using, err)
	}
	if _, err := repos.Lists.DeleteUnused(ctx, l.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("second delete: %v", err)
	}
}

// Setting a note to the value it already has is not "not found": MySQL reports
// changed rows, not matched ones, so the existence check is its own read.
func TestDestListRepo_SetFetchErrorToTheSameValueTwice(t *testing.T) {
	repos, _, ctx := newDestRepos(t)
	l := testList("l")
	if err := repos.Lists.Create(ctx, l); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if err := repos.Lists.SetFetchError(ctx, l.ID, "same"); err != nil {
			t.Fatalf("attempt %d: %v", i, err)
		}
	}
}

// The GeoIP digest the policy was reconciled with is persisted, so a change is
// noticed from what the DATABASE says even after a restart.
func TestDestStateRepo_ReconcileGeoIPPersistsTheDigestAndBumpsOnRequest(t *testing.T) {
	repos, _, ctx := newDestRepos(t)
	if st, _ := repos.State.Get(ctx); st.GeoIPSHA256 != "" {
		t.Fatalf("fresh digest %q", st.GeoIPSHA256)
	}
	g := destGeneration(t, repos, ctx)
	sum := strings.Repeat("a", 64)
	if err := repos.State.ReconcileGeoIP(ctx, sum, false); err != nil {
		t.Fatal(err)
	}
	if st, _ := repos.State.Get(ctx); st.GeoIPSHA256 != sum || st.Generation != g {
		t.Fatalf("after a quiet reconcile: %+v", st)
	}
	if err := repos.State.ReconcileGeoIP(ctx, "", true); err != nil {
		t.Fatal(err)
	}
	if st, _ := repos.State.Get(ctx); st.GeoIPSHA256 != "" || st.Generation != g+1 {
		t.Fatalf("after a bumping reconcile: %+v", st)
	}
	if err := repos.State.ReconcileGeoIP(ctx, "xyz", false); !errors.Is(err, domain.ErrValidation) {
		t.Fatalf("a bad digest: %v", err)
	}
}

// A policy may only name lists that exist, checked inside the locked
// transaction on create and on update.
func TestDestPolicyRepo_RefusesAListThatDoesNotExist(t *testing.T) {
	repos, _, ctx := newDestRepos(t)
	if err := repos.Policies.Create(ctx, testPolicy("ghost", domain.DestActionBlock, 4242)); !errors.Is(err, domain.ErrValidation) {
		t.Fatalf("create naming a missing list: %v", err)
	}
	l := makeLists(t, repos, 1)
	p := testPolicy("real", domain.DestActionBlock, l[0])
	if err := repos.Policies.Create(ctx, p); err != nil {
		t.Fatal(err)
	}
	if _, err := repos.Lists.DeleteUnused(ctx, l[0]); err != nil {
		t.Fatal(err)
	}
	// (the list is in use, so it stayed; make the policy name a deleted one)
	free := makeLists(t, repos, 1)
	if _, err := repos.Lists.DeleteUnused(ctx, free[0]); err != nil {
		t.Fatal(err)
	}
	cur, _ := repos.Policies.GetByID(ctx, p.ID)
	cur.ListIDs = []int64{free[0]}
	if err := repos.Policies.Update(ctx, cur, nil); !errors.Is(err, domain.ErrValidation) {
		t.Fatalf("update naming a deleted list: %v", err)
	}
}

// A refresh and a policy turned on, racing: whichever order they serialise in,
// the generation moves after the LATER of the two commits, so the snapshot a
// publish reads at the final generation holds the policy and the new entries.
func TestDestRepos_AContentRefreshAndAPolicyTurnedOnRaceSafely(t *testing.T) {
	for i := 0; i < 20; i++ {
		repos, _, ctx := newDestRepos(t)
		l := testList("l")
		l.Kind, l.SourceURL = domain.DestListRemote, "https://example.com/a"
		if err := repos.Lists.Create(ctx, l); err != nil {
			t.Fatal(err)
		}
		p := testPolicy("p", domain.DestActionBlock, l.ID)
		if err := repos.Policies.Create(ctx, p); err != nil {
			t.Fatal(err)
		}
		g0 := destGeneration(t, repos, ctx)
		var wg sync.WaitGroup
		wg.Add(2)
		go func() {
			defer wg.Done()
			cur, _ := repos.Policies.GetByID(ctx, p.ID)
			cur.State = domain.DestStateOn
			if err := repos.Policies.Update(ctx, cur, nil); err != nil {
				t.Error(err)
			}
		}()
		go func() {
			defer wg.Done()
			if _, err := repos.Lists.UpdateContent(ctx, l.ID, domain.DestListContent{
				Entries: []byte("domain:new.example"), EntryCount: 1, ContentSHA256: strings.Repeat("c", 64), FetchedAt: time.Now(),
			}); err != nil {
				t.Error(err)
			}
		}()
		wg.Wait()
		got, _ := repos.Lists.GetByID(ctx, l.ID)
		pol, _ := repos.Policies.GetByID(ctx, p.ID)
		if string(got.Entries) != "domain:new.example" || pol.State != domain.DestStateOn {
			t.Fatalf("iteration %d: entries %q state %s", i, got.Entries, pol.State)
		}
		// The policy turning on always bumps; the refresh bumps iff it
		// serialised after it. Either way the last commit is covered.
		if destGeneration(t, repos, ctx) < g0+1 {
			t.Fatalf("iteration %d: nothing was published", i)
		}
	}
}

func TestDestListRepo_ApplyBatchRecordsTheGeositeRelease(t *testing.T) {
	repos, _, ctx := newDestRepos(t)
	if _, _, err := repos.Lists.ApplyBatch(ctx, domain.DestListBatch{Release: "20261101000000"}); err != nil {
		t.Fatal(err)
	}
	if st, _ := repos.State.Get(ctx); st.GeositeRelease != "20261101000000" {
		t.Fatalf("release %q", st.GeositeRelease)
	}
	// A failed batch records nothing.
	if _, _, err := repos.Lists.ApplyBatch(ctx, domain.DestListBatch{Release: "20261102000000", Contents: []domain.DestListContentUpdate{{ListID: 9999}}}); err == nil {
		t.Fatal("expected failure")
	}
	if st, _ := repos.State.Get(ctx); st.GeositeRelease != "20261101000000" {
		t.Fatalf("release after a failed batch %q", st.GeositeRelease)
	}
}

// counts_as_risk is read by the risk signal, never by the compiler: an edit of
// only that flag must not republish (and so must not restart a core).
func TestPolicyUpdate_OnlyCountsAsRiskDoesNotBumpTheGeneration(t *testing.T) {
	repos, _, ctx := newDestRepos(t)
	if err := repos.Policies.Create(ctx, testPolicy("p", domain.DestActionBlock)); err != nil {
		t.Fatal(err)
	}
	pol, _ := repos.Policies.GetByName(ctx, "p")
	g0 := destGeneration(t, repos, ctx)
	pol.CountsAsRisk = true
	if err := repos.Policies.Update(ctx, pol, nil); err != nil {
		t.Fatal(err)
	}
	if got := destGeneration(t, repos, ctx); got != g0 {
		t.Fatalf("a counts_as_risk-only edit moved the generation %d -> %d", g0, got)
	}
	back, _ := repos.Policies.GetByID(ctx, pol.ID)
	if !back.CountsAsRisk {
		t.Fatal("the flag was not stored")
	}
	pol.Inline.Ports = "25"
	if err := repos.Policies.Update(ctx, pol, nil); err != nil || destGeneration(t, repos, ctx) != g0+1 {
		t.Fatalf("a compiled change must bump: %v", err)
	}
}
