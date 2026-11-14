package destlist

import (
	"context"
	"errors"
	"net/http"
	"slices"
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
