package nodesync

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	protocol "github.com/KazuhaHub/passwall-protocol/protocol"
	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

type rosterBatchClients struct {
	*rosterReplayClients
	batches int
	values  []domain.PSPClientInbound
	err     error
}

func (r *rosterBatchClients) UpdateInboundStates(_ context.Context, values []domain.PSPClientInbound) error {
	r.batches++
	r.values = append([]domain.PSPClientInbound(nil), values...)
	return r.err
}

func TestRosterReconciliationUsesOneOptionalBatchAndWithholdsInvalidationOnFailure(t *testing.T) {
	for _, failed := range []bool{false, true} {
		t.Run(map[bool]string{false: "success", true: "failure"}[failed], func(t *testing.T) {
			snapshot := &ports.NativeDesiredSnapshot{Nodes: []*domain.Node{{ID: 7}}}
			report := protocol.NodeReport{Have: map[string]protocol.StreamState{protocol.StreamRoster: {Applied: protocol.Version{Epoch: 1, Version: 4}, ETag: "fixture-current"}}}
			for i := int64(1); i <= 201; i++ {
				snapshot.Clients = append(snapshot.Clients, ports.NativeDesiredClient{Client: &domain.PSPClient{ID: i, UserID: i, UUID: "fixture-confirmed", DesiredEnable: true}, Inbounds: []domain.PSPClientInbound{{ClientID: i, NodeID: 7, State: domain.ClientApplyPending}}})
				report.Objects = append(report.Objects, protocol.ObjectStatus{Stream: protocol.StreamRoster, Key: string(protocol.NewClientKey(i)), State: protocol.ObjectApplied})
			}
			encoded, err := json.Marshal(buildRoster(snapshot, protocol.Version{Epoch: 1, Version: 2}))
			if err != nil {
				t.Fatal(err)
			}
			clients := &rosterBatchClients{rosterReplayClients: &rosterReplayClients{}}
			wantErr := errors.New("owned attachment batch failure")
			if failed {
				clients.err = wantErr
			}
			invalidations := 0
			service := &Service{clients: clients, issues: &rosterReplayIssues{}, invalidateRender: func() { invalidations++ }}
			err = service.reconcileObjects(t.Context(), &domain.NodeAgent{AgentID: "fixture", Epoch: 1}, snapshot, report, map[string]*domain.NodeAgentStream{protocol.StreamRoster: {DesiredETag: "fixture-current", DesiredBody: encoded}}, time.Now())
			if clients.batches != 1 || len(clients.values) != 201 || len(clients.writes) != 0 {
				t.Fatal("reconciliation returned to per-attachment calls or lost part of the report")
			}
			for i, value := range clients.values {
				if value.ClientID != int64(i+1) || value.NodeID != 7 || value.State != domain.ClientApplyApplied || value.AppliedVersion != 4 || value.AppliedUUID != "fixture-confirmed" {
					t.Fatal("batch lost its original scoped receipt")
				}
			}
			if failed {
				if !errors.Is(err, wantErr) || invalidations != 0 {
					t.Fatal("failed attachment batch invalidated render state or lost its cause")
				}
			} else if err != nil || invalidations != 1 {
				t.Fatal("successful attachment batch lost render invalidation")
			}
		})
	}
}
