package node

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/KazuhaHub/passwall-sub-panel/internal/adapters/sqlstore"
	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
	"github.com/KazuhaHub/passwall-sub-panel/internal/service/front"
	"github.com/KazuhaHub/passwall-sub-panel/internal/service/route"
	"github.com/KazuhaHub/passwall-sub-panel/internal/service/transit"
)

type routeCleanupFixture struct {
	svc          *Service
	repos        ports.Repos
	entry, exit  int64
	routed, kept *domain.Node
}

// newRouteCleanupFixture wires the node service to the real front service, whose
// per-server lock the detach holds, and the real routing service, whose cleanup
// takes the same lock.
func newRouteCleanupFixture(t *testing.T) *routeCleanupFixture {
	t.Helper()
	sqlstore.ConfigureSecretKey("test-only-node-route-cleanup-key")
	t.Cleanup(func() { sqlstore.ConfigureSecretKey("") })
	db, err := sqlstore.Open("sqlite", filepath.Join(t.TempDir(), "cleanup.db"))
	if err != nil {
		t.Fatal(err)
	}
	if err := sqlstore.EnsureSchema(db); err != nil {
		t.Fatal(err)
	}
	repos := sqlstore.NewRepos(db)
	ctx := t.Context()
	f := &routeCleanupFixture{repos: repos}
	for name, id := range map[string]*int64{"entry": &f.entry, "exit": &f.exit} {
		panel := &domain.Panel{Kind: domain.PanelKindPSP, Name: name, URL: "psp://" + name}
		if err := repos.XUIPanel.Save(ctx, panel); err != nil {
			t.Fatal(err)
		}
		*id = panel.ID
	}
	mk := func(inbound int, name string) *domain.Node {
		n := &domain.Node{PanelID: f.entry, InboundID: inbound, DisplayName: name, DesiredProtocol: "vless", DesiredPort: 4000 + inbound, Enabled: true, Region: "JP"}
		if err := repos.Node.Create(ctx, n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	f.routed, f.kept = mk(1, "routed"), mk(2, "kept")
	if err := repos.TransitRoutes.Replace(ctx, f.routed.ID, []domain.DestRoute{{
		ID: "r1", Target: domain.DestRouteTarget{Kind: domain.DestRouteServer, PanelID: f.exit}, Domains: []string{"domain:a.com"}}}); err != nil {
		t.Fatal(err)
	}
	links := transit.New(repos.Node, repos.Transit).WithLinks(repos.TransitLinks, repos.NodeAgent)
	if _, err := links.EnsureLink(ctx, f.entry, f.exit); err != nil {
		t.Fatal(err)
	}
	frontSvc := front.New(repos.TransitFronts, repos.Node, repos.XUIPanel, repos.NodeAgent, repos.Transit, repos.TransitLinks, nil)
	routes := route.New(route.Deps{Routes: repos.TransitRoutes, Nodes: repos.Node, Panels: repos.XUIPanel, Agents: repos.NodeAgent, Links: links, ServerLock: frontSvc.Lock})
	f.svc = New(repos.Node, nil, nil, &detachOwnershipCleaner{}, nil, nil, nil)
	f.svc.SetFronts(repos.TransitFronts, frontSvc)
	f.svc.SetRouteLinks(routes.NodeDeleted, routes.Lock)
	return f
}

func (f *routeCleanupFixture) linkExists(t *testing.T) bool {
	t.Helper()
	_, err := f.repos.TransitLinks.GetLink(t.Context(), f.entry, f.exit)
	return err == nil
}

// within fails the test, instead of hanging it, when a call does not return: the
// server locks are not reentrant, and a cleanup that took one the caller still
// held would wait forever.
func within(t *testing.T, name string, call func() error) {
	t.Helper()
	done := make(chan error, 1)
	go func() { done <- call() }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
	case <-time.After(10 * time.Second):
		t.Fatalf("%s did not return: it is waiting on a lock it already holds", name)
	}
}

// Detaching a node whose routes leave through another server removes the tunnel
// only they used, after the server lock the detach holds has been let go.
func TestDetachingANodeWithRoutesRemovesItsTunnelWithoutDeadlock(t *testing.T) {
	f := newRouteCleanupFixture(t)
	within(t, "DetachAndSync", func() error { return f.svc.DetachAndSync(context.Background(), f.routed.ID) })
	if f.linkExists(t) {
		t.Fatal("the tunnel only the deleted node's routes used was kept")
	}
	if _, err := f.repos.Node.GetByID(t.Context(), f.routed.ID); err == nil {
		t.Fatal("the node was not deleted")
	}
}

// The delete the queued task finally performs (no lock held) does the same, and a
// tunnel another node leaves through by default stays.
func TestDeletingANodeWithRoutesRemovesItsTunnelUnlessAnotherNodeUsesIt(t *testing.T) {
	f := newRouteCleanupFixture(t)
	if err := f.svc.nodes.UpdateMetadata(t.Context(), &domain.Node{ID: f.kept.ID, DisplayName: "kept", ServerAddress: "x", Region: "JP", EgressPanelID: f.exit}); err != nil {
		t.Fatal(err)
	}
	within(t, "deleteNode", func() error { return f.svc.deleteNode(context.Background(), f.routed.ID) })
	if !f.linkExists(t) {
		t.Fatal("a tunnel another node leaves through by default was removed")
	}
	if err := f.svc.nodes.UpdateMetadata(t.Context(), &domain.Node{ID: f.kept.ID, DisplayName: "kept", ServerAddress: "x", Region: "JP"}); err != nil {
		t.Fatal(err)
	}
	within(t, "DetachAndSync of the last user", func() error { return f.svc.DetachAndSync(context.Background(), f.kept.ID) })
}
