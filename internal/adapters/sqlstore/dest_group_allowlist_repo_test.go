package sqlstore

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
)

// A group's allowlist is a property of the group (nullable columns on groups_),
// written through the dest side: only those columns, with an updated_at
// precondition, and the dest generation advances in the same transaction.

func newGroupAllowlistRepos(t *testing.T) (*DestRepos, *groupRepo, context.Context) {
	t.Helper()
	repos, db, ctx := newDestRepos(t)
	return repos, &groupRepo{db: db}, ctx
}

func mkGroup(t *testing.T, gr *groupRepo, slug string) *domain.Group {
	t.Helper()
	g := &domain.Group{Slug: slug, Name: slug}
	if err := gr.Create(context.Background(), g); err != nil {
		t.Fatal(err)
	}
	return g
}

func allowlist(state domain.DestState, lists ...int64) *domain.DestAllowlist {
	return &domain.DestAllowlist{State: state, ListIDs: lists}
}

func TestGroupAllowlistColumns_AreNullableAndHaveNoTextDefault(t *testing.T) {
	_, db, _ := newDestRepos(t)
	cols, err := db.Migrator().ColumnTypes(&groupRow{})
	if err != nil {
		t.Fatal(err)
	}
	found := map[string]bool{}
	for _, c := range cols {
		switch c.Name() {
		case "dest_allowlist_state", "dest_allowlist_list_ids", "dest_allowlist_base_list_id", "dest_allowlist_extra_list_id",
			"dest_allowlist_state_changed_at", "dest_allowlist_updated_at":
			found[c.Name()] = true
			if nullable, ok := c.Nullable(); ok && !nullable {
				t.Errorf("%s is NOT NULL: an upgraded install has rows that do not have it", c.Name())
			}
			if def, ok := c.DefaultValue(); ok && def != "" {
				t.Errorf("%s has default %q", c.Name(), def)
			}
		}
	}
	if len(found) != 6 {
		t.Fatalf("columns found = %v", found)
	}
}

func TestGroupRepo_ANewGroupHasNoAllowlistAndReadsAsOff(t *testing.T) {
	_, gr, ctx := newGroupAllowlistRepos(t)
	g := mkGroup(t, gr, "plain")
	got, err := gr.GetByID(ctx, g.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.DestAllowlist.State != domain.DestStateOff || len(got.DestAllowlist.ListIDs) != 0 ||
		got.DestAllowlist.BaseListID != 0 || got.DestAllowlist.UpdatedAt != nil || got.DestAllowlist.StateChangedAt != nil {
		t.Fatalf("allowlist = %+v, want off and empty", got.DestAllowlist)
	}
}

func TestGroupRepo_RoundTripsTheAllowlistThroughEveryRead(t *testing.T) {
	_, gr, ctx := newGroupAllowlistRepos(t)
	at := time.Date(2026, 11, 14, 1, 2, 3, 0, time.UTC)
	g := &domain.Group{Slug: "g", Name: "g", DestAllowlist: domain.DestAllowlist{
		State: domain.DestStateReport, ListIDs: []int64{3, 1}, BaseListID: 7, ExtraListID: 8, StateChangedAt: &at, UpdatedAt: &at,
	}}
	if err := gr.Create(ctx, g); err != nil {
		t.Fatal(err)
	}
	check := func(where string, got *domain.Group) {
		t.Helper()
		a := got.DestAllowlist
		if a.State != domain.DestStateReport || len(a.ListIDs) != 2 || a.ListIDs[0] != 1 || a.ListIDs[1] != 3 ||
			a.BaseListID != 7 || a.ExtraListID != 8 || a.StateChangedAt == nil || !a.StateChangedAt.Equal(at) || a.UpdatedAt == nil || !a.UpdatedAt.Equal(at) {
			t.Errorf("%s: allowlist = %+v", where, a)
		}
	}
	byID, _ := gr.GetByID(ctx, g.ID)
	check("GetByID", byID)
	bySlug, _ := gr.GetBySlug(ctx, "g")
	check("GetBySlug", bySlug)
	all, _ := gr.List(ctx)
	check("List", all[0])
}

// The group editor saves a whole group it read earlier. It must not be able to
// put back an older allowlist, or turn one off by not knowing about it.
func TestGroupRepo_UpdateNeverWritesTheAllowlist(t *testing.T) {
	repos, gr, ctx := newGroupAllowlistRepos(t)
	g := mkGroup(t, gr, "g")
	ls := makeLists(t, repos, 1)
	if err := repos.GroupAllowlists.Save(ctx, g.ID, allowlist(domain.DestStateOn, ls[0]), nil); err != nil {
		t.Fatal(err)
	}
	stale := *g // read before the allowlist was set
	stale.Name = "renamed"
	if err := gr.Update(ctx, &stale); err != nil {
		t.Fatal(err)
	}
	got, _ := gr.GetByID(ctx, g.ID)
	if got.Name != "renamed" || got.DestAllowlist.State != domain.DestStateOn || len(got.DestAllowlist.ListIDs) != 1 {
		t.Fatalf("group = %+v: the edit must change the name and leave the allowlist", got)
	}
}

func TestGroupAllowlistRepo_SaveAdvancesTheGenerationAndWritesOnlyItsColumns(t *testing.T) {
	repos, gr, ctx := newGroupAllowlistRepos(t)
	g := mkGroup(t, gr, "g")
	g.Remark = "keep me"
	if err := gr.Update(ctx, g); err != nil {
		t.Fatal(err)
	}
	ls := makeLists(t, repos, 2)
	if err := repos.GroupAllowlists.Save(ctx, g.ID, allowlist(domain.DestStateReport, ls[1], ls[0], ls[0]), nil); err != nil {
		t.Fatal(err)
	}
	if gen := destGeneration(t, repos, ctx); gen != 1 {
		t.Fatalf("generation = %d, want 1", gen)
	}
	got, _ := gr.GetByID(ctx, g.ID)
	if got.Remark != "keep me" || got.Name != "g" {
		t.Fatalf("other columns moved: %+v", got)
	}
	a, err := repos.GroupAllowlists.Get(ctx, g.ID)
	if err != nil {
		t.Fatal(err)
	}
	if a.State != domain.DestStateReport || len(a.ListIDs) != 2 || a.ListIDs[0] != 1 || a.ListIDs[1] != 2 || a.UpdatedAt == nil || a.StateChangedAt == nil {
		t.Fatalf("allowlist = %+v (list ids sorted, once each)", a)
	}
	if _, err := repos.GroupAllowlists.Get(ctx, 999); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("Get of a missing group = %v", err)
	}
	if err := repos.GroupAllowlists.Save(ctx, 999, allowlist(domain.DestStateOn), nil); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("Save on a missing group = %v", err)
	}
	if gen := destGeneration(t, repos, ctx); gen != 1 {
		t.Fatalf("a refused write moved the generation to %d", gen)
	}
}

func TestGroupAllowlistRepo_RefusesWhatTheColumnsCannotHold(t *testing.T) {
	repos, gr, ctx := newGroupAllowlistRepos(t)
	g := mkGroup(t, gr, "g")
	for name, a := range map[string]*domain.DestAllowlist{
		"unknown state":  allowlist("maybe"),
		"empty state":    allowlist(""),
		"bad list":       allowlist(domain.DestStateOn, 0),
		"negative base":  {State: domain.DestStateOn, BaseListID: -1},
		"negative extra": {State: domain.DestStateOn, ExtraListID: -1},
	} {
		if err := repos.GroupAllowlists.Save(ctx, g.ID, a, nil); !errors.Is(err, domain.ErrValidation) {
			t.Errorf("%s: err = %v, want a validation error", name, err)
		}
	}
	if gen := destGeneration(t, repos, ctx); gen != 0 {
		t.Fatalf("generation = %d", gen)
	}
}

func TestGroupAllowlistRepo_StaleWriteIsAConflict(t *testing.T) {
	repos, gr, ctx := newGroupAllowlistRepos(t)
	g := mkGroup(t, gr, "g")
	if err := repos.GroupAllowlists.Save(ctx, g.ID, allowlist(domain.DestStateReport), nil); err != nil {
		t.Fatal(err)
	}
	first, _ := repos.GroupAllowlists.Get(ctx, g.ID)
	seen := *first.UpdatedAt
	time.Sleep(3 * time.Millisecond)
	if err := repos.GroupAllowlists.Save(ctx, g.ID, allowlist(domain.DestStateOn), &seen); err != nil {
		t.Fatal(err)
	}
	gen := destGeneration(t, repos, ctx)
	if err := repos.GroupAllowlists.Save(ctx, g.ID, allowlist(domain.DestStateOff), &seen); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("stale save = %v, want ErrConflict", err)
	}
	if got, _ := repos.GroupAllowlists.Get(ctx, g.ID); got.State != domain.DestStateOn {
		t.Fatalf("the stale write landed: %+v", got)
	}
	if destGeneration(t, repos, ctx) != gen {
		t.Fatal("a refused write moved the generation")
	}
	// A precondition on an allowlist that was never written is a conflict too.
	other := mkGroup(t, gr, "h")
	if err := repos.GroupAllowlists.Save(ctx, other.ID, allowlist(domain.DestStateOn), &seen); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("precondition on an unwritten allowlist = %v", err)
	}
}

func TestGroupAllowlistRepo_StateChangedAtMovesOnlyWithTheState(t *testing.T) {
	repos, gr, ctx := newGroupAllowlistRepos(t)
	g := mkGroup(t, gr, "g")
	save := func(a *domain.DestAllowlist) *domain.DestAllowlist {
		t.Helper()
		time.Sleep(3 * time.Millisecond)
		if err := repos.GroupAllowlists.Save(ctx, g.ID, a, nil); err != nil {
			t.Fatal(err)
		}
		got, _ := repos.GroupAllowlists.Get(ctx, g.ID)
		return got
	}
	ls := makeLists(t, repos, 2)
	a := save(allowlist(domain.DestStateReport, ls[0]))
	b := save(allowlist(domain.DestStateReport, ls[1]))
	if !b.StateChangedAt.Equal(*a.StateChangedAt) || !b.UpdatedAt.After(*a.UpdatedAt) {
		t.Fatalf("state_changed_at %v -> %v, updated_at %v -> %v", a.StateChangedAt, b.StateChangedAt, a.UpdatedAt, b.UpdatedAt)
	}
	c := save(allowlist(domain.DestStateOn, ls[1]))
	if !c.StateChangedAt.After(*b.StateChangedAt) {
		t.Fatalf("state_changed_at did not move with the state: %v -> %v", b.StateChangedAt, c.StateChangedAt)
	}
}

func ownedList(name, entries string) *domain.DestList {
	return &domain.DestList{
		Name: name, Kind: domain.DestListCustom, Side: domain.DestSideDest,
		Entries: []byte(entries), SourceText: []byte(entries), EntryCount: strings.Count(entries, "\n") + 1,
		ContentSHA256: strings.Repeat("b", 64),
	}
}

func TestGroupAllowlistRepo_CreateWithOwnedListsIsOneTransaction(t *testing.T) {
	repos, gr, ctx := newGroupAllowlistRepos(t)
	g := mkGroup(t, gr, "g")
	base, extra := ownedList("g base", "domain:doh.example"), ownedList("g extra", "domain:more.example")
	a := allowlist(domain.DestStateReport, makeLists(t, repos, 1)[0])
	if err := repos.GroupAllowlists.CreateWithOwnedLists(ctx, g.ID, a, base, extra); err != nil {
		t.Fatal(err)
	}
	if gen := destGeneration(t, repos, ctx); gen != 1 {
		t.Fatalf("generation = %d, want one bump for the lists and the state together", gen)
	}
	got, _ := repos.GroupAllowlists.Get(ctx, g.ID)
	if got.BaseListID == 0 || got.ExtraListID == 0 || got.BaseListID == got.ExtraListID || got.State != domain.DestStateReport {
		t.Fatalf("allowlist = %+v", got)
	}
	for _, id := range []int64{got.BaseListID, got.ExtraListID} {
		l, err := repos.Lists.GetByID(ctx, id)
		if err != nil || l.OwnerGroupID != g.ID {
			t.Fatalf("list %d = %+v, %v: it must be owned by the group", id, l, err)
		}
	}
	if a.BaseListID != got.BaseListID {
		t.Fatalf("the caller's allowlist was not given the new ids: %+v", a)
	}
	// Once the group has an allowlist it is not created again.
	err := repos.GroupAllowlists.CreateWithOwnedLists(ctx, g.ID, allowlist(domain.DestStateOn), ownedList("b2", "domain:a.example"), ownedList("e2", "domain:b.example"))
	if !errors.Is(err, domain.ErrAlreadyExists) {
		t.Fatalf("second create = %v, want ErrAlreadyExists", err)
	}
	if lists, _ := repos.Lists.List(ctx, false); len(lists) != 3 { // the one it names and its two own
		t.Fatalf("lists = %d: the refused create left some", len(lists))
	}
}

func TestGroupAllowlistRepo_CreateWithOwnedListsLeavesNothingOnFailure(t *testing.T) {
	repos, gr, ctx := newGroupAllowlistRepos(t)
	g := mkGroup(t, gr, "g")
	bad := ownedList("g extra", "domain:more.example")
	bad.Kind = "nonsense"
	if err := repos.GroupAllowlists.CreateWithOwnedLists(ctx, g.ID, allowlist(domain.DestStateReport), ownedList("g base", "domain:doh.example"), bad); err == nil {
		t.Fatal("an invalid owned list was accepted")
	}
	// A group that does not exist: the lists are not left behind either.
	if err := repos.GroupAllowlists.CreateWithOwnedLists(ctx, 999, allowlist(domain.DestStateReport), ownedList("b", "domain:a.example"), ownedList("e", "domain:b.example")); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("missing group = %v", err)
	}
	if lists, _ := repos.Lists.List(ctx, false); len(lists) != 0 {
		t.Fatalf("%d orphan lists left", len(lists))
	}
	if got, _ := repos.GroupAllowlists.Get(ctx, g.ID); got.State != domain.DestStateOff {
		t.Fatalf("allowlist = %+v", got)
	}
	if gen := destGeneration(t, repos, ctx); gen != 0 {
		t.Fatalf("generation = %d after rolled-back creates", gen)
	}
}

func TestDestReadDefinitions_CarriesTheGroupAllowlists(t *testing.T) {
	repos, gr, ctx := newGroupAllowlistRepos(t)
	g1, g2, g3 := mkGroup(t, gr, "a"), mkGroup(t, gr, "b"), mkGroup(t, gr, "c")
	ls := makeLists(t, repos, 2)
	for _, c := range []struct {
		g *domain.Group
		a *domain.DestAllowlist
	}{{g2, allowlist(domain.DestStateOn, ls[1])}, {g1, allowlist(domain.DestStateOff, ls[0])}} {
		if err := repos.GroupAllowlists.Save(ctx, c.g.ID, c.a, nil); err != nil {
			t.Fatal(err)
		}
	}
	_ = g3 // never had one: absent
	defs, err := repos.ReadDefinitions(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(defs.GroupModes) != 2 || defs.GroupModes[0].GroupID != g1.ID || defs.GroupModes[0].State != domain.DestStateOff ||
		defs.GroupModes[1].GroupID != g2.ID || defs.GroupModes[1].State != domain.DestStateOn || defs.GroupModes[1].ListIDs[0] != ls[1] {
		t.Fatalf("group modes = %+v", defs.GroupModes)
	}
	if defs.Generation != destGeneration(t, repos, ctx) {
		t.Fatal("not read at the generation")
	}
}

// A refresh of a list a group in force uses is something the compiler can see.
func TestDestListRepo_RefreshOfAGroupListAdvancesTheGenerationOnlyInForce(t *testing.T) {
	repos, gr, ctx := newGroupAllowlistRepos(t)
	g := mkGroup(t, gr, "g")
	used, idle := testList("used"), testList("idle")
	for _, l := range []*domain.DestList{used, idle} {
		if err := repos.Lists.Create(ctx, l); err != nil {
			t.Fatal(err)
		}
	}
	if err := repos.GroupAllowlists.Save(ctx, g.ID, allowlist(domain.DestStateOn, used.ID), nil); err != nil {
		t.Fatal(err)
	}
	content := func(sha string) domain.DestListContent {
		return domain.DestListContent{Entries: []byte("domain:x.example"), EntryCount: 1, ContentSHA256: strings.Repeat(sha, 64), FetchedAt: time.Now()}
	}
	before := destGeneration(t, repos, ctx)
	if _, err := repos.Lists.UpdateContent(ctx, idle.ID, content("c")); err != nil {
		t.Fatal(err)
	}
	if destGeneration(t, repos, ctx) != before {
		t.Fatal("a list no group uses moved the generation")
	}
	if _, err := repos.Lists.UpdateContent(ctx, used.ID, content("d")); err != nil {
		t.Fatal(err)
	}
	if destGeneration(t, repos, ctx) != before+1 {
		t.Fatal("a list a group in force uses did not advance the generation")
	}
	if err := repos.GroupAllowlists.Save(ctx, g.ID, allowlist(domain.DestStateOff, used.ID), nil); err != nil {
		t.Fatal(err)
	}
	before = destGeneration(t, repos, ctx)
	if _, err := repos.Lists.UpdateContent(ctx, used.ID, content("e")); err != nil {
		t.Fatal(err)
	}
	if destGeneration(t, repos, ctx) != before {
		t.Fatal("a list of an off group moved the generation")
	}
}

func TestGroupRepo_DeleteTakesTheGroupsOwnedListsAndAdvancesTheGeneration(t *testing.T) {
	repos, gr, ctx := newGroupAllowlistRepos(t)
	g, bystander := mkGroup(t, gr, "g"), mkGroup(t, gr, "h")
	shared := testList("shared")
	if err := repos.Lists.Create(ctx, shared); err != nil {
		t.Fatal(err)
	}
	if err := repos.GroupAllowlists.CreateWithOwnedLists(ctx, g.ID, allowlist(domain.DestStateOn, shared.ID), ownedList("b", "domain:a.example"), ownedList("e", "domain:b.example")); err != nil {
		t.Fatal(err)
	}
	if err := repos.GroupAllowlists.CreateWithOwnedLists(ctx, bystander.ID, allowlist(domain.DestStateOff), ownedList("b2", "domain:a.example"), ownedList("e2", "domain:b.example")); err != nil {
		t.Fatal(err)
	}
	before := destGeneration(t, repos, ctx)
	if err := gr.Delete(ctx, g.ID); err != nil {
		t.Fatal(err)
	}
	if destGeneration(t, repos, ctx) != before+1 {
		t.Fatal("deleting a group with an allowlist must advance the generation once")
	}
	lists, _ := repos.Lists.List(ctx, false)
	var owners []int64
	for _, l := range lists {
		owners = append(owners, l.OwnerGroupID)
	}
	if len(lists) != 3 { // shared, and the bystander's two
		t.Fatalf("lists left = %v: only the deleted group's owned lists go", owners)
	}
	for _, l := range lists {
		if l.OwnerGroupID == g.ID {
			t.Fatalf("list %d of the deleted group is still there", l.ID)
		}
	}
	// A group with nothing to do with the destination policy: no publish.
	plain := mkGroup(t, gr, "plain")
	before = destGeneration(t, repos, ctx)
	if err := gr.Delete(ctx, plain.ID); err != nil {
		t.Fatal(err)
	}
	if destGeneration(t, repos, ctx) != before {
		t.Fatal("deleting a plain group advanced the generation")
	}
}
