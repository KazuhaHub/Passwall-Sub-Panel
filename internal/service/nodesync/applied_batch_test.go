package nodesync

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/KazuhaHub/passwall-protocol/protocol"
	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

type appliedBatchAgentRepo struct {
	ports.NodeAgentRepo
	batch                ports.NodeAgentAppliedBatchRepo
	calls, reads, scalar int
	receipts             []ports.NodeAgentStreamApplied
	failure              error
	foreign              bool
}

func (r *appliedBatchAgentRepo) GetStream(ctx context.Context, owner string, name domain.NodeAgentStreamName) (*domain.NodeAgentStream, error) {
	r.reads++
	return r.NodeAgentRepo.GetStream(ctx, owner, name)
}

func (r *appliedBatchAgentRepo) RecordApplied(ctx context.Context, owner string, name domain.NodeAgentStreamName, epoch, version uint64, etag string, now time.Time) error {
	r.scalar++
	return r.NodeAgentRepo.RecordApplied(ctx, owner, name, epoch, version, etag, now)
}

func (r *appliedBatchAgentRepo) RecordAppliedBatch(ctx context.Context, owner string, receipts []ports.NodeAgentStreamApplied, now time.Time) ([]*domain.NodeAgentStream, error) {
	r.calls++
	r.receipts = append([]ports.NodeAgentStreamApplied(nil), receipts...)
	if r.failure != nil {
		return nil, r.failure
	}
	documents, err := r.batch.RecordAppliedBatch(ctx, owner, receipts, now)
	if r.foreign && err == nil {
		documents[0].AgentID = "agt_foreign_fixture"
	}
	return documents, err
}

func TestReportStreamBatchAvoidsScalarRoundTripsAndPublishesOnlyAfterSuccess(t *testing.T) {
	for _, kind := range []string{"success", "failure", "foreign"} {
		t.Run(kind, func(t *testing.T) {
			repos := newReceiptTestRepos(t)
			agent, err := repos.NodeAgent.GetByAgentID(t.Context(), "agt_receipts")
			if err != nil {
				t.Fatal(err)
			}
			batch := &appliedBatchAgentRepo{NodeAgentRepo: repos.NodeAgent, batch: repos.NodeAgent.(ports.NodeAgentAppliedBatchRepo), foreign: kind == "foreign"}
			if kind == "failure" {
				batch.failure = errors.New("owned stream batch failure")
			}
			s := &Service{agents: batch, tasks: repos.NodeAgentTask, issues: repos.NodeAgentIssue, reports: make(map[string]receivedFullReport)}
			report := protocol.NodeReport{AgentID: agent.AgentID, ProtocolVersion: protocol.ProtocolVersion1, Have: map[string]protocol.StreamState{
				protocol.StreamConfig: {}, protocol.StreamRoster: {}, protocol.StreamDirectives: {},
			}}
			err = s.ingestReport(t.Context(), agent, &ports.NativeDesiredSnapshot{}, report, time.Now().UTC())
			if batch.calls != 1 || batch.reads != 0 || batch.scalar != 0 || len(batch.receipts) != 3 {
				t.Fatal("one report retained independent stream read/write round trips")
			}
			_, _, published := s.fullReport(agent.AgentID)
			if kind == "success" && (err != nil || !published) {
				t.Fatal("successful receipt vector did not publish its full observation")
			}
			if kind != "success" && (err == nil || published) {
				t.Fatal("failed or foreign issuer receipts published a full observation")
			}
		})
	}
}
