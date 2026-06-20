package user

import (
	"context"
	"testing"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

// Minimal fakes: embed the full interface (nil) and override only the methods
// BackfillPSPClients actually calls, so unrelated interface churn never touches
// this test.
type bfUserRepo struct {
	ports.UserRepo
	users []*domain.User
}

func (r *bfUserRepo) List(context.Context, ports.UserFilter) ([]*domain.User, int64, error) {
	return r.users, int64(len(r.users)), nil
}

type bfGroupRepo struct {
	ports.GroupRepo
	g *domain.Group
}

func (r *bfGroupRepo) GetByID(context.Context, int64) (*domain.Group, error) { return r.g, nil }

type bfSelector struct{ nodes []*domain.Node }

func (s bfSelector) NodesFor(context.Context, *domain.Group) ([]*domain.Node, error) {
	return s.nodes, nil
}

type bfSettings struct{ ports.ScopedSettings }

func (bfSettings) Load(_ context.Context, d ports.UISettings) (ports.UISettings, error) { return d, nil }

type bfPSP struct{ synced []int64 }

func (p *bfPSP) SyncUser(_ context.Context, userID int64, _ string, _ domain.EmailRules, _ []*domain.Node) error {
	p.synced = append(p.synced, userID)
	return nil
}

func TestBackfillPSPClients(t *testing.T) {
	users := []*domain.User{
		{ID: 1, UUID: "u1", GroupID: 1},
		{ID: 2, UUID: "u2", GroupID: 1, AutoDisabledReason: domain.DisabledPendingDelete}, // skipped
		{ID: 3, UUID: "u3", GroupID: 1},
	}
	psp := &bfPSP{}
	svc := &Service{
		users:    &bfUserRepo{users: users},
		groups:   &bfGroupRepo{g: &domain.Group{ID: 1}},
		selector: bfSelector{nodes: []*domain.Node{{ID: 10, PanelID: 1, Protocol: "vless"}}},
		settings: bfSettings{},
		psp:      psp,
	}

	res, err := svc.BackfillPSPClients(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if res.Processed != 2 || res.Skipped != 1 || res.Errors != 0 {
		t.Fatalf("result = %+v, want processed 2 / skipped 1 / errors 0", res)
	}
	if len(psp.synced) != 2 || psp.synced[0] != 1 || psp.synced[1] != 3 {
		t.Fatalf("synced = %v, want SyncUser for users 1 and 3 (pending-delete 2 skipped)", psp.synced)
	}

	// nil provisioner → whole pass is a no-op.
	svc.psp = nil
	if res2, _ := svc.BackfillPSPClients(context.Background()); res2.Processed != 0 {
		t.Fatalf("nil provisioner must be a no-op, got %+v", res2)
	}
}
