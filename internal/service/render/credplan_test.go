package render

import (
	"context"
	"testing"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/pkg/crypto"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

func TestCredPlan_Password_NilDerivesStoredAndFallback(t *testing.T) {
	uuid := "uuid-x"
	// nil plan (gate off) → always the legacy derived value.
	wantDerived := crypto.DeriveProxyPassword(uuid, domain.ProtoTrojan, "")
	if got := (*credPlan)(nil).password(5, uuid, domain.ProtoTrojan, ""); got != wantDerived {
		t.Fatalf("nil plan must derive: got %q", got)
	}
	cp := &credPlan{byNode: map[int64]string{5: "STORED"}}
	// provisioned node → stored value.
	if got := cp.password(5, uuid, domain.ProtoTrojan, ""); got != "STORED" {
		t.Fatalf("provisioned node must use stored value, got %q", got)
	}
	// node absent from the plan (un-provisioned) → derived fallback.
	m := "2022-blake3-aes-256-gcm"
	if got := cp.password(9, uuid, domain.ProtoSS2022, m); got != crypto.DeriveProxyPassword(uuid, domain.ProtoSS2022, m) {
		t.Fatalf("un-provisioned node must fall back to derived, got %q", got)
	}
}

type fakePSPClients struct {
	ports.PSPClientRepo
	clients     []*domain.PSPClient
	attachments map[int64][]domain.PSPClientInbound
}

func (f fakePSPClients) ListByUser(context.Context, int64) ([]*domain.PSPClient, error) {
	return f.clients, nil
}
func (f fakePSPClients) ListInbounds(_ context.Context, clientID int64) ([]domain.PSPClientInbound, error) {
	return f.attachments[clientID], nil
}

func TestBuildCredPlan_GateOffReturnsNil(t *testing.T) {
	s := &Service{repos: ports.Repos{PSPClient: fakePSPClients{}}}
	if cp := s.buildCredPlan(context.Background(), &domain.User{ID: 1}, ports.UISettings{SubRenderUseSharedClient: false}); cp != nil {
		t.Fatal("gate off must yield a nil cred plan (legacy derive path)")
	}
}

func TestBuildCredPlan_GateOnMapsOnlyProvisioned(t *testing.T) {
	repo := fakePSPClients{
		clients: []*domain.PSPClient{{ID: 100, Password: "PW"}},
		attachments: map[int64][]domain.PSPClientInbound{
			100: {
				{ClientID: 100, NodeID: 5, Provisioned: true},
				{ClientID: 100, NodeID: 6, Provisioned: false}, // not live → excluded
			},
		},
	}
	s := &Service{repos: ports.Repos{PSPClient: repo}}
	cp := s.buildCredPlan(context.Background(), &domain.User{ID: 1}, ports.UISettings{SubRenderUseSharedClient: true})
	if cp == nil {
		t.Fatal("gate on with a provisioned attachment must yield a plan")
	}
	if cp.byNode[5] != "PW" {
		t.Fatalf("provisioned node 5 must map to stored PW, got %q", cp.byNode[5])
	}
	if _, ok := cp.byNode[6]; ok {
		t.Fatal("un-provisioned node 6 must NOT be in the plan (renders derived)")
	}
}
