package node

import (
	"context"
	"testing"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

// recordingSyncer satisfies ClientSyncer and records which enrollment path was
// taken — the per-client AddClientToInbound or the batched
// BulkAddClientsToInbound.
type recordingSyncer struct {
	addCalls  int
	bulkCalls int
	bulkReqs  []ports.BulkClientAdd
}

func (r *recordingSyncer) AddClientToInbound(_ context.Context, _ int64, _ int64, _ int,
	_ domain.Protocol, _, _, _, _ string, _, _ int64) error {
	r.addCalls++
	return nil
}

func (r *recordingSyncer) BulkAddClientsToInbound(_ context.Context, _ int64, _ int, reqs []ports.BulkClientAdd) (int, error) {
	r.bulkCalls++
	r.bulkReqs = append(r.bulkReqs, reqs...)
	return len(reqs), nil
}

type oneAllGroup struct{ ports.GroupRepo }

func (oneAllGroup) List(context.Context) ([]*domain.Group, error) {
	return []*domain.Group{{ID: 1, TagFilter: domain.TagFilter{All: true}}}, nil
}

type twoMembers struct{ ports.UserRepo }

func (twoMembers) ListByGroup(_ context.Context, _ int64) ([]*domain.User, error) {
	return []*domain.User{
		{ID: 1, Enabled: true, UUID: "uuid-1"},
		{ID: 2, Enabled: true, UUID: "uuid-2"},
	}, nil
}

// syncExistingUsersToNode must enroll every eligible member in ONE bulkCreate
// call (BulkAddClientsToInbound) — not N per-client AddClientToInbound calls —
// so attaching a node to a populated group triggers a single Xray restart.
func TestSyncExistingUsersToNodeUsesBulkAdd(t *testing.T) {
	rec := &recordingSyncer{}
	client := &stubXUIClient{getResp: &ports.Inbound{
		ID: 20, Protocol: "vless", StreamSettings: `{"security":"reality"}`,
	}}
	svc := &Service{
		pool:     stubXUIPool{c: client},
		groups:   oneAllGroup{},
		users:    twoMembers{},
		syncer:   rec,
		settings: settingsStub{},
	}
	n := &domain.Node{ID: 5, PanelID: 10, InboundID: 20}

	if err := svc.syncExistingUsersToNode(context.Background(), n); err != nil {
		t.Fatalf("syncExistingUsersToNode: %v", err)
	}
	if rec.addCalls != 0 {
		t.Fatalf("must NOT use per-client AddClientToInbound, addCalls = %d", rec.addCalls)
	}
	if rec.bulkCalls != 1 {
		t.Fatalf("must enroll via ONE bulk call, bulkCalls = %d", rec.bulkCalls)
	}
	if len(rec.bulkReqs) != 2 {
		t.Fatalf("both members must be in the bulk request, got %d", len(rec.bulkReqs))
	}
	for _, r := range rec.bulkReqs {
		if r.Flow != "xtls-rprx-vision" {
			t.Fatalf("reality flow must propagate into each request: %#v", r)
		}
		if r.UserUUID == "" || r.Email == "" {
			t.Fatalf("request missing uuid/email: %#v", r)
		}
	}
}
