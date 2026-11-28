package sharedclient

import (
	"context"
	"testing"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
)

// One heal sweep reconciles every user, and each used to list the whole panel:
// U users on one panel were U full listings. Inside a sweep the panel is listed
// once, and that one listing answers "nothing to clean up" for every user it
// shows as converged.
func TestReconcileOrphans_SweepListsAPanelOnceForConvergedUsers(t *testing.T) {
	clients := &fakeClients{attachments: []domain.PSPClientInbound{{NodeID: 1}}, byUser: []*domain.PSPClient{
		{ID: 1, UserID: 18, PanelID: 10, Email: "u18@psp.local"},
	}}
	xui := &fakeXUI{liveClients: map[string][]int{
		"u18@psp.local":    {1, 2},
		"u18-n7@psp.local": {1}, // legacy fallback: not this pass's business
		"u1@psp.local":     {1},
		"Kazuha Home 0":    {1},
	}}
	svc := New(clients, fakePool{c: xui}, fakeNodes{})
	ctx := svc.WithOrphanSweep(context.Background())
	for i := 0; i < 5; i++ { // the fake repo answers every user id with the same rows
		if err := svc.ReconcileOrphans(ctx, 18); err != nil {
			t.Fatal(err)
		}
	}
	if xui.listCalls != 1 {
		t.Fatalf("a sweep must list the panel once for converged users, got %d listings", xui.listCalls)
	}
	if len(xui.deleted) != 0 {
		t.Fatalf("nothing to delete, got %v", xui.deleted)
	}
}

// Outside a sweep nothing changes: every call reads the panel itself.
func TestReconcileOrphans_WithoutSweepListsEveryTime(t *testing.T) {
	clients := &fakeClients{attachments: []domain.PSPClientInbound{{NodeID: 1}}, byUser: []*domain.PSPClient{
		{ID: 1, UserID: 18, PanelID: 10, Email: "u18@psp.local"},
	}}
	xui := &fakeXUI{liveClients: map[string][]int{"u18@psp.local": {1}}}
	svc := New(clients, fakePool{c: xui}, fakeNodes{})
	for i := 0; i < 3; i++ {
		if err := svc.ReconcileOrphans(context.Background(), 18); err != nil {
			t.Fatal(err)
		}
	}
	if xui.listCalls != 3 {
		t.Fatalf("want one listing per call outside a sweep, got %d", xui.listCalls)
	}
}

// The sweep's listing is old by the time a user's turn comes. It may only say
// "nothing to do"; a delete is always decided on a listing read at that moment.
// Here the old listing shows a covered orphan, but the replacement has since
// lost the inbound the orphan serves: deleting on the old listing would cut the
// user off that inbound.
func TestReconcileOrphans_SweepNeverDeletesOnTheOldListing(t *testing.T) {
	clients := &fakeClients{attachments: []domain.PSPClientInbound{{NodeID: 1}}, byUser: []*domain.PSPClient{
		{ID: 1, UserID: 18, PanelID: 10, Email: "u18@psp.local"},
	}}
	xui := &fakeXUI{liveClients: map[string][]int{
		"u18@psp.local":           {1, 2},
		"u18-k49f8cae4@psp.local": {2},
	}}
	svc := New(clients, fakePool{c: xui}, fakeNodes{})
	ctx := svc.WithOrphanSweep(context.Background())
	// Another user's turn takes the sweep's listing.
	if _, err := svc.sweepListing(ctx, 10, xui); err != nil {
		t.Fatal(err)
	}
	// The panel changes before user 18's turn.
	xui.liveClients = map[string][]int{
		"u18@psp.local":           {1},
		"u18-k49f8cae4@psp.local": {2},
	}
	if err := svc.ReconcileOrphans(ctx, 18); err != nil {
		t.Fatal(err)
	}
	if len(xui.deleted) != 0 {
		t.Fatalf("the orphan's inbound is no longer covered: nothing may be deleted, got %v", xui.deleted)
	}
	if xui.listCalls != 2 {
		t.Fatalf("a possible delete must be decided on a listing of its own, got %d listings", xui.listCalls)
	}
}

// The same resync that reaches this pass may have just created the replacement,
// which the sweep's listing cannot show. "Not up yet" from the old listing is
// therefore not an answer: the panel is read again, and the cleanup happens in
// this pass instead of waiting for the next sweep.
func TestReconcileOrphans_SweepReadsAgainWhenTheOldListingLacksTheReplacement(t *testing.T) {
	clients := &fakeClients{attachments: []domain.PSPClientInbound{{NodeID: 1}}, byUser: []*domain.PSPClient{
		{ID: 1, UserID: 18, PanelID: 10, Email: "u18@psp.local"},
	}}
	xui := &fakeXUI{liveClients: map[string][]int{
		"u18-kf2d62608@psp.local": {1},
	}}
	svc := New(clients, fakePool{c: xui}, fakeNodes{})
	ctx := svc.WithOrphanSweep(context.Background())
	if _, err := svc.sweepListing(ctx, 10, xui); err != nil {
		t.Fatal(err)
	}
	xui.liveClients = map[string][]int{
		"u18@psp.local":           {1},
		"u18-kf2d62608@psp.local": {1},
	}
	if err := svc.ReconcileOrphans(ctx, 18); err != nil {
		t.Fatal(err)
	}
	if len(xui.deleted) != 1 || xui.deleted[0].email != "u18-kf2d62608@psp.local" {
		t.Fatalf("the covered orphan must be deleted on the fresh listing, got %v", xui.deleted)
	}
}

// A sweep listing that failed is no answer either: the user's own read decides,
// exactly as it does outside a sweep.
func TestReconcileOrphans_SweepListingFailureFallsBackToAFreshRead(t *testing.T) {
	clients := &fakeClients{attachments: []domain.PSPClientInbound{{NodeID: 1}}, byUser: []*domain.PSPClient{
		{ID: 1, UserID: 18, PanelID: 10, Email: "u18@psp.local"},
	}}
	xui := &fakeXUI{listErrOnCall: 1, liveClients: map[string][]int{
		"u18@psp.local":           {1},
		"u18-kf2d62608@psp.local": {1},
	}}
	svc := New(clients, fakePool{c: xui}, fakeNodes{})
	if err := svc.ReconcileOrphans(svc.WithOrphanSweep(context.Background()), 18); err != nil {
		t.Fatal(err)
	}
	if len(xui.deleted) != 1 {
		t.Fatalf("want the orphan deleted on the fallback read, got %v", xui.deleted)
	}
}
