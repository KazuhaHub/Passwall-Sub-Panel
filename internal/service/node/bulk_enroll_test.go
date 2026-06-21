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

type recordingTasks struct {
	ports.SyncTaskRepo
	created []*domain.SyncTask
}

func (r *recordingTasks) GetActiveByTarget(context.Context, domain.SyncTaskType, string, int64) (*domain.SyncTask, error) {
	return nil, domain.ErrNotFound // no existing task → enqueue
}
func (r *recordingTasks) Create(_ context.Context, t *domain.SyncTask) error {
	r.created = append(r.created, t)
	return nil
}

// v3.9.0: new-node enrollment must NOT create per-node clients (the ownership
// model is retired). Instead it enqueues a user_resync per eligible member, and
// ResyncMembership re-provisions each member's SHARED client to include the node.
func TestSyncExistingUsersToNodeEnqueuesResync(t *testing.T) {
	rec := &recordingSyncer{}
	tasks := &recordingTasks{}
	client := &stubXUIClient{getResp: &ports.Inbound{
		ID: 20, Protocol: "vless", StreamSettings: `{"security":"reality"}`,
	}}
	svc := &Service{
		pool:     stubXUIPool{c: client},
		groups:   oneAllGroup{},
		users:    twoMembers{},
		syncer:   rec,
		settings: settingsStub{},
		tasks:    tasks,
	}
	n := &domain.Node{ID: 5, PanelID: 10, InboundID: 20}

	if err := svc.syncExistingUsersToNode(context.Background(), n); err != nil {
		t.Fatalf("syncExistingUsersToNode: %v", err)
	}
	// No per-node client writes (no add, no bulk).
	if rec.addCalls != 0 || rec.bulkCalls != 0 {
		t.Fatalf("must NOT create per-node clients: add=%d bulk=%d", rec.addCalls, rec.bulkCalls)
	}
	// One user_resync task per eligible member.
	if len(tasks.created) != 2 {
		t.Fatalf("want 2 user_resync tasks enqueued, got %d", len(tasks.created))
	}
	for _, ct := range tasks.created {
		if ct.Type != domain.SyncTaskUserResync || ct.TargetType != "user" {
			t.Fatalf("expected a user_resync task targeting a user, got %+v", ct)
		}
	}
}
