package front

import (
	"context"
	"strings"
	"testing"

	nodeprotocol "github.com/KazuhaHub/passwall-protocol/protocol"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
)

// A tunnel the entry no longer uses is kept while a front routes to it; once
// the front drops the route (or is deleted) the front service says so, after
// its own lock is released, so the owner of the tunnel can remove it.
func TestFrontSaveAndDeleteReleaseTheTunnelsTheyStopRoutingTo(t *testing.T) {
	e := newEnv(realityNode(1, "hk", 443, "a.example.com"))
	link := domain.TransitLink{
		ID: 7, EntryPanelID: 3, ExitPanelID: panelID, Port: 23456,
		Protocol:          nodeprotocol.LinkProtocolVLESSReality,
		RealityUUID:       "d342d11e-d424-4583-b36e-524ab1f0afa4",
		RealityPrivateKey: strings.Repeat("A", 43),
		RealityShortID:    "0123456789abcdef",
		RealityServerName: "link.example.com", RealityTarget: "link.example.com:443",
	}
	e.svc.links = fakeLinks{link}
	var released [][]int64
	e.svc.WithLinkRelease(func(_ context.Context, ids []int64) {
		// The server lock must be free: the owner takes it to delete the link.
		e.svc.Lock(panelID)()
		released = append(released, ids)
	})
	withLink := simpleInput()
	withLink.Routes = append(withLink.Routes, domain.FrontRoute{
		ServerNames: []string{link.RealityServerName},
		Target:      domain.FrontTarget{Kind: domain.FrontTargetLink, LinkID: link.ID},
	})
	saved, err := e.svc.Create(t.Context(), panelID, withLink)
	if err != nil {
		t.Fatal(err)
	}
	if len(released) != 0 {
		t.Fatalf("a save that adds the route released %v", released)
	}
	if _, err := e.svc.Update(t.Context(), panelID, saved.Front.ID, withLink); err != nil {
		t.Fatal(err)
	}
	if len(released) != 0 {
		t.Fatalf("a save that keeps the route released %v", released)
	}
	if _, err := e.svc.Update(t.Context(), panelID, saved.Front.ID, simpleInput()); err != nil {
		t.Fatal(err)
	}
	if len(released) != 1 || len(released[0]) != 1 || released[0][0] != link.ID {
		t.Fatalf("a save that drops the route released %v, want [[7]]", released)
	}
	released = nil
	saved, err = e.svc.Update(t.Context(), panelID, saved.Front.ID, withLink)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.Delete(t.Context(), panelID, saved.Front.ID, true); err != nil {
		t.Fatal(err)
	}
	if len(released) != 1 || released[0][0] != link.ID {
		t.Fatalf("deleting the front released %v, want [[7]]", released)
	}
}
