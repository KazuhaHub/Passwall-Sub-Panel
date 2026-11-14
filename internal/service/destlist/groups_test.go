package destlist

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
)

// An allowlist group holds lists: its own base and extra lists and the ones it
// names. A list a group holds is in use (any state of the group), and a list an
// "on" group holds cannot be edited into one that is not ready.

// group makes a group with the given allowlist.
func (h *harness) group(name string, state domain.DestState, lists ...int64) (*domain.Group, *domain.DestAllowlist) {
	h.t.Helper()
	g := &domain.Group{Slug: name, Name: name}
	if err := h.groups.Create(context.Background(), g); err != nil {
		h.t.Fatal(err)
	}
	a := &domain.DestAllowlist{State: state, ListIDs: lists}
	base := &domain.DestList{Name: name + " base", Kind: domain.DestListCustom, Side: domain.DestSideDest, Entries: []byte("domain:doh.example"), EntryCount: 1, ContentSHA256: strings.Repeat("b", 64)}
	extra := &domain.DestList{Name: name + " extra", Kind: domain.DestListCustom, Side: domain.DestSideDest, EntryCount: 0, ContentSHA256: strings.Repeat("e", 64)}
	if err := h.repos.GroupAllowlists.CreateWithOwnedLists(context.Background(), g.ID, a, base, extra); err != nil {
		h.t.Fatal(err)
	}
	return g, a
}

func TestUsedBy_NamesTheAllowlistGroupsThatHoldEachList(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	a, _, _ := h.svc.CreateCustom(ctx, "a", "", []byte("a.example.com"))
	p := h.policy("p", domain.DestStateOn, domain.DestActionBlock, a.ID)
	g, al := h.group("Guests", domain.DestStateOff, a.ID) // off still holds
	used, err := h.svc.UsedBy(ctx)
	if err != nil {
		t.Fatal(err)
	}
	want := []PolicyRef{{Kind: "policy", ID: p.ID, Name: "p"}, {Kind: "group", ID: g.ID, Name: "Guests"}}
	if !slices.Equal(used[a.ID], want) {
		t.Fatalf("used[a] = %+v, want %+v: the policies, then the groups", used[a.ID], want)
	}
	for _, id := range []int64{al.BaseListID, al.ExtraListID} {
		if got := used[id]; len(got) != 1 || got[0].Kind != "group" || got[0].ID != g.ID {
			t.Fatalf("used[owned %d] = %+v", id, got)
		}
	}
}

func TestDeleteList_RefusesWhileAnAllowlistGroupHoldsIt(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	l, _, _ := h.svc.CreateCustom(ctx, "mine", "", []byte("example.com"))
	g, al := h.group("Guests", domain.DestStateReport, l.ID)
	for _, id := range []int64{l.ID, al.BaseListID} {
		err := h.svc.DeleteList(ctx, id)
		var inUse *ListInUseError
		if !errors.As(err, &inUse) || !errors.Is(err, ErrListInUse) || !slices.Equal(inUse.GroupIDs, []int64{g.ID}) || len(inUse.PolicyIDs) != 0 {
			t.Fatalf("delete %d: err = %v, want it to name group %d", id, err, g.ID)
		}
	}
	if _, err := h.repos.Lists.GetByID(ctx, l.ID); err != nil {
		t.Fatalf("a list a group holds was deleted: %v", err)
	}
}

// D5: a list an "on" group holds is not edited into one that is not ready (the
// allowlist would deny everything it names); a report group's or an off group's
// list can be.
func TestUpdateRemote_RefusesToMakeAListAnOnGroupHoldsNotReady(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	body := "a.example.com\n"
	srv := newListServer(t, func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(body)) })
	h.svc.doer = srv.Client()
	l, _, err := h.svc.CreateRemote(ctx, "feed", srv.URL+"/one", "")
	if err != nil {
		t.Fatal(err)
	}
	g, al := h.group("Guests", domain.DestStateOn, l.ID)

	body = "" // the new URL answers with nothing: the list would be empty, not ready
	_, _, err = h.svc.UpdateRemote(ctx, l.ID, "feed", srv.URL+"/two")
	var nr *ListNotReadyError
	if !errors.As(err, &nr) || !errors.Is(err, ErrListNotReady) || !errors.Is(err, domain.ErrConflict) || !slices.Equal(nr.ListIDs, []int64{l.ID}) {
		t.Fatalf("err = %v, want ListNotReadyError for list %d", err, l.ID)
	}
	if stored, _ := h.repos.Lists.GetByID(ctx, l.ID); stored.SourceURL != srv.URL+"/one" || stored.EntryCount != 1 {
		t.Fatalf("the refused edit changed the list: %+v", stored)
	}
	// An edit that keeps it ready is fine, and so is the same edit once the
	// group is back to report.
	body = "z.example.com\n"
	if _, _, err := h.svc.UpdateRemote(ctx, l.ID, "feed", srv.URL+"/three"); err != nil {
		t.Fatalf("an edit that keeps the list ready was refused: %v", err)
	}
	al.State = domain.DestStateReport
	if err := h.repos.GroupAllowlists.Save(ctx, g.ID, al, al.UpdatedAt); err != nil {
		t.Fatal(err)
	}
	body = ""
	if _, _, err := h.svc.UpdateRemote(ctx, l.ID, "feed", srv.URL+"/four"); err != nil {
		t.Fatalf("a report group's list was refused: %v", err)
	}
}

// A refresh is not an edit an admin can be told "no" to, but it must not blank
// the list an "on" allowlist is made of: it is recorded as failed and the
// previous entries stay.
func TestRefresh_KeepsTheEntriesOfAListAnOnGroupHoldsWhenTheFeedGoesEmpty(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	body := "a.example.com\n"
	srv := newListServer(t, func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(body)) })
	h.svc.doer = srv.Client()
	l, _, err := h.svc.CreateRemote(ctx, "feed", srv.URL+"/one", "")
	if err != nil {
		t.Fatal(err)
	}
	g, al := h.group("Guests", domain.DestStateOn, l.ID)
	gen := h.generation()

	body = ""
	out, err := h.svc.RefreshList(ctx, l.ID)
	if err != nil || out.Result != RefreshFailed || out.Error == "" {
		t.Fatalf("outcome = %+v, %v: the feed went empty under an on group", out, err)
	}
	stored, _ := h.repos.Lists.GetByID(ctx, l.ID)
	if stored.EntryCount != 1 || stored.LastError == "" || h.generation() != gen {
		t.Fatalf("entries %d, last_error %q, generation %d -> %d: the previous entries stay", stored.EntryCount, stored.LastError, gen, h.generation())
	}
	// With the group back to report the feed may go empty.
	al.State = domain.DestStateReport
	if err := h.repos.GroupAllowlists.Save(ctx, g.ID, al, al.UpdatedAt); err != nil {
		t.Fatal(err)
	}
	if out, err := h.svc.RefreshList(ctx, l.ID); err != nil || out.Result != RefreshUpdated {
		t.Fatalf("outcome = %+v, %v", out, err)
	}
}

// The geosite update guard judges the lists the allowlist groups hold like the
// ones the policies hold: an update that would push a group's category past the
// wire's caps is refused, and a custom list only a group holds that references a
// category still forces a republish when the category changes.
func TestGeositeUpdate_JudgesTheListsAnAllowlistGroupHolds(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	big := func(n int, prefix string) []string {
		out := make([]string, n)
		for i := range out {
			out[i] = "domain:" + prefix + strconv.Itoa(i) + ".example"
		}
		return out
	}
	h.up.release("20261101000000", map[string][]string{"a": big(30000, "a"), "b": big(15000, "b"), "c": {"domain:c1.example"}})
	if _, err := h.svc.GeositeUpdate(ctx, UpdateOptions{}); err != nil {
		t.Fatal(err)
	}
	la, _ := h.svc.CreateGeosite(ctx, "a", nil)
	lb, _ := h.svc.CreateGeosite(ctx, "b", nil)
	custom, _, err := h.svc.CreateCustom(ctx, "refs", "", []byte("geosite:c"))
	if err != nil {
		t.Fatal(err)
	}
	h.group("Guests", domain.DestStateOn, la.ID, lb.ID, custom.ID)

	h.up.release("20261102000000", map[string][]string{"a": big(30000, "a"), "b": big(25000, "b"), "c": {"domain:c1.example"}}) // 55 000 > 50 000
	_, err = h.svc.GeositeUpdate(ctx, UpdateOptions{})
	var rej *UpdateRejectedError
	if !errors.As(err, &rej) || rej.Reason != "policy_over_limit" || rej.Kind != "domains" || rej.Used != 55002 { // a, b, the custom list's geosite:c and the base host
		t.Fatalf("err = %v (%+v): the group lists are not counted", err, *rej.UpdateRejection)
	}

	// Within the limits, a change to a category only the group's custom list uses republishes.
	h.up.release("20261103000000", map[string][]string{"a": big(30000, "a"), "b": big(15000, "b"), "c": {"domain:c1.example", "domain:c2.example"}})
	gen := h.generation()
	if _, err := h.svc.GeositeUpdate(ctx, UpdateOptions{}); err != nil {
		t.Fatal(err)
	}
	if h.generation() == gen {
		t.Fatal("the update moved a category a group's custom list references and the generation stayed")
	}
}

// A custom list an "on" group names is not edited down to nothing, the same end
// state PUT /dest/groups refuses ("an empty custom list allows nothing"); the
// group's own extra list may be empty.
func TestCustomEdits_RefuseEmptyingAListAnOnGroupNames(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	l, _, _ := h.svc.CreateCustom(ctx, "mine", "", []byte("a.example.com\nb.example.com"))
	g, al := h.group("Guests", domain.DestStateOn, l.ID)

	if _, _, err := h.svc.AddEntries(ctx, l.ID, nil, []string{"a.example.com", "b.example.com"}); !errors.Is(err, ErrListNotReady) {
		t.Fatalf("emptying by removal: %v", err)
	}
	if _, _, err := h.svc.UpdateCustom(ctx, l.ID, "mine", []byte("# nothing\n")); !errors.Is(err, ErrListNotReady) {
		t.Fatalf("emptying by rewrite: %v", err)
	}
	if stored, _ := h.repos.Lists.GetByID(ctx, l.ID); stored.EntryCount != 2 {
		t.Fatalf("a refused edit changed the list: %d entries", stored.EntryCount)
	}
	if _, _, err := h.svc.AddEntries(ctx, l.ID, nil, []string{"a.example.com"}); err != nil {
		t.Fatalf("an edit that leaves entries was refused: %v", err)
	}
	// The group's own extra list may be emptied, and a report group's list too.
	if _, _, err := h.svc.AddEntries(ctx, al.ExtraListID, []string{"x.example.com"}, nil); err != nil {
		t.Fatal(err)
	}
	if _, _, err := h.svc.AddEntries(ctx, al.ExtraListID, nil, []string{"x.example.com"}); err != nil {
		t.Fatalf("the group's own list was refused: %v", err)
	}
	al.State = domain.DestStateReport
	if err := h.repos.GroupAllowlists.Save(ctx, g.ID, al, al.UpdatedAt); err != nil {
		t.Fatal(err)
	}
	if _, _, err := h.svc.AddEntries(ctx, l.ID, nil, []string{"b.example.com"}); err != nil {
		t.Fatalf("a report group's list was refused: %v", err)
	}
}
