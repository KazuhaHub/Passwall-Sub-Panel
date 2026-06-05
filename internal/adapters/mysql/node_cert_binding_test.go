package mysql

import (
	"testing"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
)

func TestNodeCertBindingAndListByCertID(t *testing.T) {
	repos, _, ctx := newCertReposTest(t)
	nodes := repos.Node

	n1 := &domain.Node{PanelID: 1, InboundID: 2, DisplayName: "A", Region: "TW"}
	n2 := &domain.Node{PanelID: 1, InboundID: 3, DisplayName: "B", Region: "TW"}
	if err := nodes.Create(ctx, n1); err != nil {
		t.Fatalf("create n1: %v", err)
	}
	if err := nodes.Create(ctx, n2); err != nil {
		t.Fatalf("create n2: %v", err)
	}

	if err := nodes.UpdateCertBinding(ctx, n1.ID, domain.CertSourceManaged, 5); err != nil {
		t.Fatalf("bind: %v", err)
	}

	got, err := nodes.ListByCertID(ctx, 5)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(got) != 1 || got[0].ID != n1.ID {
		t.Fatalf("ListByCertID(5) = %d nodes, want [n1]", len(got))
	}
	if got[0].CertSource != domain.CertSourceManaged || got[0].CertID != 5 {
		t.Fatalf("binding not persisted: source=%q id=%d", got[0].CertSource, got[0].CertID)
	}

	// Rebinding n1 away (to manual/0) removes it from the cert's node set —
	// what the renewal worker relies on so a detached node isn't re-deployed.
	if err := nodes.UpdateCertBinding(ctx, n1.ID, domain.CertSourceManual, 0); err != nil {
		t.Fatalf("rebind: %v", err)
	}
	got, _ = nodes.ListByCertID(ctx, 5)
	if len(got) != 0 {
		t.Fatalf("ListByCertID(5) after rebind = %d, want 0", len(got))
	}
}
