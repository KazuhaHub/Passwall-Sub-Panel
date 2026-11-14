package sqlstore

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
)

// What the allowlist-group admin API reads from the store: the groups that have
// an allowlist, who holds a list (groups included), and the report-only rows of
// a group folded by destination.

func TestGroupAllowlistRepo_ListReturnsGroupsWithARowByIdWithTheirNames(t *testing.T) {
	repos, gr, ctx := newGroupAllowlistRepos(t)
	none := mkGroup(t, gr, "none")
	_ = none
	a, b := mkGroup(t, gr, "a"), mkGroup(t, gr, "b")
	l := testList("l")
	if err := repos.Lists.Create(ctx, l); err != nil {
		t.Fatal(err)
	}
	// Written out of order; one of them off, which still has a row.
	if err := repos.GroupAllowlists.CreateWithOwnedLists(ctx, b.ID, allowlist(domain.DestStateOff, l.ID), ownedList("bb", "domain:a.example"), ownedList("be", "domain:b.example")); err != nil {
		t.Fatal(err)
	}
	if err := repos.GroupAllowlists.CreateWithOwnedLists(ctx, a.ID, allowlist(domain.DestStateOn, l.ID), ownedList("ab", "domain:a.example"), ownedList("ae", "domain:b.example")); err != nil {
		t.Fatal(err)
	}
	got, err := repos.GroupAllowlists.List(ctx)
	if err != nil || len(got) != 2 {
		t.Fatalf("list = %+v, %v: only groups with a row", got, err)
	}
	if got[0].GroupID != a.ID || got[0].GroupName != "a" || got[0].Allowlist.State != domain.DestStateOn || !reflect.DeepEqual(got[0].Allowlist.ListIDs, []int64{l.ID}) ||
		got[0].Allowlist.BaseListID == 0 || got[0].Allowlist.UpdatedAt == nil {
		t.Fatalf("first = %+v", got[0])
	}
	if got[1].GroupID != b.ID || got[1].Allowlist.State != domain.DestStateOff {
		t.Fatalf("second = %+v: an off group keeps its row", got[1])
	}
}

func TestDestListRepo_DeleteUnusedRefusesAListAnAllowlistGroupHolds(t *testing.T) {
	repos, gr, ctx := newGroupAllowlistRepos(t)
	g := mkGroup(t, gr, "g")
	shared := testList("shared")
	if err := repos.Lists.Create(ctx, shared); err != nil {
		t.Fatal(err)
	}
	// The group is OFF: it still holds its lists.
	if err := repos.GroupAllowlists.CreateWithOwnedLists(ctx, g.ID, allowlist(domain.DestStateOff, shared.ID), ownedList("b", "domain:a.example"), ownedList("e", "domain:b.example")); err != nil {
		t.Fatal(err)
	}
	al, _ := repos.GroupAllowlists.Get(ctx, g.ID)
	for _, id := range []int64{shared.ID, al.BaseListID, al.ExtraListID} {
		use, err := repos.Lists.DeleteUnused(ctx, id)
		if err != nil || !reflect.DeepEqual(use.GroupIDs, []int64{g.ID}) || len(use.PolicyIDs) != 0 {
			t.Fatalf("DeleteUnused(%d) = %+v, %v, want held by the group", id, use, err)
		}
		if _, err := repos.Lists.GetByID(ctx, id); err != nil {
			t.Fatalf("list %d held by a group was deleted: %v", id, err)
		}
	}
	// Dropped from the group's lists, the list goes.
	al.ListIDs = nil
	if err := repos.GroupAllowlists.Save(ctx, g.ID, al, al.UpdatedAt); err != nil {
		t.Fatal(err)
	}
	if use, err := repos.Lists.DeleteUnused(ctx, shared.ID); err != nil || use.Any() {
		t.Fatalf("DeleteUnused = %+v, %v", use, err)
	}
}

func TestDestHitRepo_TrialSitesFoldsTrialRowsBySite(t *testing.T) {
	r, _ := newHitRepo(t)
	ctx := context.Background()
	h := 400 * hourMS
	rows := []domain.DestHit{
		// group 3's trial rows (observe, user 0): two panels, two hours.
		hit(h, 1, 0, "g3", "observe", "a.example", 0, 10, h+1, h+10),
		hit(h, 2, 0, "g3", "observe", "a.example", 0, 5, h+2, h+30),
		hit(h+hourMS, 1, 0, "g3", "observe", "a.example", 0, 1, h+hourMS, h+hourMS+99),
		hit(h, 1, 0, "g3", "observe", "(ip)", 0, 20, h+1, h+2),
		hit(h, 1, 0, "g3", "observe", "b.example", 0, 20, h+1, h+2),
		// not group 3's trial: another group, a block row, an account's row, an old hour.
		hit(h, 1, 0, "g4", "observe", "other.example", 0, 99, h, h),
		hit(h, 1, 7, "g3", "block", "denied.example", 443, 99, h, h),
		hit(h, 1, 7, "g3", "observe", "user.example", 443, 99, h, h),
		hit(h-5*hourMS, 1, 0, "g3", "observe", "old.example", 0, 99, h-5*hourMS, h-5*hourMS),
		hit(h, 1, 7, "p3", "observe", "policy.example", 443, 99, h, h),
	}
	if _, err := r.StoreBatch(ctx, "agt_a", "b1", time.Now(), rows); err != nil {
		t.Fatal(err)
	}
	got, err := r.TrialSites(ctx, "g3", h-hourMS)
	if err != nil {
		t.Fatal(err)
	}
	want := []domain.DestTrialSite{
		{Site: "a.example", Count: 16, LastMS: h + hourMS + 99}, // 10+5+1, the latest instant over all of them
		{Site: "(ip)", Count: 20, LastMS: h + 2},
		{Site: "b.example", Count: 20, LastMS: h + 2},
	}
	// Most seen first, ties by name.
	want = []domain.DestTrialSite{want[1], want[2], want[0]}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("sites = %+v\nwant   %+v", got, want)
	}
	if got, err := r.TrialSites(ctx, "g9", 0); err != nil || len(got) != 0 {
		t.Fatalf("a group with no rows: %+v, %v", got, err)
	}
}

func TestDestHitRepo_TrialDroppedCountsOnlyTrialOverflow(t *testing.T) {
	r, _ := newHitRepo(t)
	ctx := context.Background()
	h := 400 * hourMS
	for _, d := range []struct {
		hour, panel int64
		kind        string
		n           int64
	}{
		{h, 1, domain.DestDropNodeTrial, 1000}, {h, 2, domain.DestDropNodeTrial, 50}, {h + 3*hourMS, 1, domain.DestDropNodeTrial, 7},
		{h, 1, domain.DestDropNodeHits, 5}, {h, 1, domain.DestDropQueueFull, 9},
	} {
		if err := r.AddDrops(ctx, d.hour, d.panel, d.kind, d.n); err != nil {
			t.Fatal(err)
		}
	}
	if got, err := r.TrialDropped(ctx, h, h+2*hourMS); err != nil || got != 1050 {
		t.Fatalf("trial dropped in [h, h+2h) = %d, %v, want 1050", got, err)
	}
	if got, _ := r.TrialDropped(ctx, 0, 0); got != 1057 {
		t.Fatalf("trial dropped overall = %d", got)
	}
}
