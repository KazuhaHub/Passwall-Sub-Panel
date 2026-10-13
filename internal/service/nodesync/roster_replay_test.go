package nodesync

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	protocol "github.com/KazuhaHub/passwall-protocol/protocol"
	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

type rosterReplayClients struct {
	ports.PSPClientRepo
	writes []domain.PSPClientInbound
}

func (r *rosterReplayClients) UpdateInboundState(_ context.Context, value domain.PSPClientInbound) error {
	r.writes = append(r.writes, value)
	return nil
}

type rosterReplayIssues struct {
	ports.NodeAgentIssueRepo
	missing int
}

func (r *rosterReplayIssues) RecordBatch(_ context.Context, _ string, values []domain.NodeAgentIssue, _ time.Time) error {
	r.missing += len(values)
	return nil
}

func TestRepeatedAppliedRosterAvoidsWritesWithoutHidingTransitions(t *testing.T) {
	for _, name := range []string{"unchanged", "pending", "version", "credentials", "failure_clock", "rejected", "missing", "stale_etag", "stale_epoch", "new_desired"} {
		t.Run(name, func(t *testing.T) {
			snapshot := &ports.NativeDesiredSnapshot{Nodes: []*domain.Node{{ID: 7}}}
			for i := int64(1); i <= 201; i++ {
				client := &domain.PSPClient{ID: i, UserID: i, Email: fmt.Sprintf("fixture-%d", i), UUID: fmt.Sprintf("fixture-uuid-%d", i), DesiredEnable: true}
				snapshot.Clients = append(snapshot.Clients, ports.NativeDesiredClient{Client: client, Inbounds: []domain.PSPClientInbound{{ClientID: i, NodeID: 7, State: domain.ClientApplyApplied, AppliedVersion: 4, AppliedEmail: client.Email, AppliedUUID: client.UUID}}})
			}
			body := buildRoster(snapshot, protocol.Version{Epoch: 1, Version: 2})
			encoded, err := json.Marshal(body)
			if err != nil {
				t.Fatal(err)
			}
			streams := map[string]*domain.NodeAgentStream{protocol.StreamRoster: {DesiredETag: "fixture-current", DesiredBody: encoded}}
			report := protocol.NodeReport{Have: map[string]protocol.StreamState{protocol.StreamRoster: {Applied: protocol.Version{Epoch: 1, Version: 4}, ETag: "fixture-current"}}}
			for i := int64(1); i <= 201; i++ {
				report.Objects = append(report.Objects, protocol.ObjectStatus{Stream: protocol.StreamRoster, Key: string(protocol.NewClientKey(i)), State: protocol.ObjectApplied})
			}
			attachment := &snapshot.Clients[0].Inbounds[0]
			wantWrites, wantMissing, wantInvalidations := 1, 0, 0
			wantState := domain.ClientApplyApplied
			switch name {
			case "unchanged":
				wantWrites = 0
			case "pending":
				attachment.State = domain.ClientApplyPending
			case "version":
				attachment.AppliedVersion = 3
			case "credentials":
				attachment.AppliedUUID = "old-confirmed-fixture"
				wantInvalidations = 1
			case "failure_clock":
				failed := time.Now().Add(-time.Minute)
				attachment.FirstFailedAt = &failed
			case "rejected":
				report.Objects[0].State = protocol.ObjectRejected
				report.Objects[0].FirstFailedAtMS = 123000
				wantState = domain.ClientApplyRejected
			case "missing":
				report.Objects = report.Objects[1:]
				wantState, wantMissing = domain.ClientApplyPending, 1
			case "stale_etag":
				report.Have[protocol.StreamRoster] = protocol.StreamState{Applied: protocol.Version{Epoch: 1, Version: 4}, ETag: "fixture-stale"}
				attachment.State = domain.ClientApplyPending
				wantWrites = 0
			case "stale_epoch":
				report.Have[protocol.StreamRoster] = protocol.StreamState{Applied: protocol.Version{Epoch: 2, Version: 4}, ETag: "fixture-current"}
				attachment.State = domain.ClientApplyPending
				wantWrites = 0
			case "new_desired":
				snapshot.Clients[0].Client.UUID = "new-desired-fixture"
				attachment.State = domain.ClientApplyPending
				wantWrites = 0
			}
			clients, issues := &rosterReplayClients{}, &rosterReplayIssues{}
			invalidations := 0
			service := &Service{clients: clients, issues: issues, invalidateRender: func() { invalidations++ }}
			if err := service.reconcileObjects(t.Context(), &domain.NodeAgent{AgentID: "fixture", Epoch: 1}, snapshot, report, streams, time.Now()); err != nil {
				t.Fatal(err)
			}
			if name == "unchanged" {
				if err := service.reconcileObjects(t.Context(), &domain.NodeAgent{AgentID: "fixture", Epoch: 1}, snapshot, report, streams, time.Now()); err != nil {
					t.Fatal(err)
				}
			}
			if len(clients.writes) != wantWrites || issues.missing != wantMissing || invalidations != wantInvalidations {
				t.Fatalf("writes=%d missing=%d invalidations=%d; wanted %d/%d/%d", len(clients.writes), issues.missing, invalidations, wantWrites, wantMissing, wantInvalidations)
			}
			if wantWrites == 1 {
				update := clients.writes[0]
				if update.ClientID != 1 || update.NodeID != 7 || update.State != wantState || update.AppliedVersion != 4 {
					t.Fatal("changed attachment lost its exact convergence transition")
				}
				if name == "rejected" && (update.FirstFailedAt == nil || update.FirstFailedAt.UnixMilli() != 123000) {
					t.Fatal("rejection lost its original failure clock")
				}
				if wantState == domain.ClientApplyApplied && (update.AppliedUUID != "fixture-uuid-1" || update.FirstFailedAt != nil) {
					t.Fatal("applied receipt lost credentials or retained a failure clock")
				}
			}
		})
	}
}
