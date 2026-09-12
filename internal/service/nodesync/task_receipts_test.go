package nodesync

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	nodeprotocol "github.com/KazuhaHub/passwall-node/protocol"

	"github.com/KazuhaHub/passwall-sub-panel/internal/adapters/sqlstore"
	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

type receiptAppliedAgentRepo struct {
	ports.NodeAgentRepo
	failure error
}

func (r *receiptAppliedAgentRepo) RecordApplied(ctx context.Context, agentID string, stream domain.NodeAgentStreamName, epoch, version uint64, etag string, seenAt time.Time) error {
	if r.failure != nil {
		return r.failure
	}
	return r.NodeAgentRepo.RecordApplied(ctx, agentID, stream, epoch, version, etag, seenAt)
}

func newReceiptTestRepos(t *testing.T) ports.Repos {
	t.Helper()
	db, err := sqlstore.Open("sqlite", filepath.Join(t.TempDir(), "task-receipts.db"))
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	if err := sqlstore.EnsureSchema(db); err != nil {
		t.Fatal(err)
	}
	repos := sqlstore.NewRepos(db)
	if err := repos.NodeAgent.Create(t.Context(), &domain.NodeAgent{
		AgentID: "agt_receipts", PanelID: 919,
		CredentialSHA256: nodeprotocol.ComputeTaskInputSHA256("agt_receipts", nil),
	}); err != nil {
		t.Fatal(err)
	}
	return repos
}

func newReceiptTask(t *testing.T, tasks ports.NodeAgentTaskRepo, id string) *domain.NodeAgentTask {
	t.Helper()
	task := &domain.NodeAgentTask{
		TaskID: id, AgentID: "agt_receipts", Kind: "reality_probe.v1", Args: []byte("input"),
	}
	task.InputSHA256 = nodeprotocol.ComputeTaskInputSHA256(task.Kind, task.Args)
	if _, _, err := tasks.CreateOrGet(t.Context(), task); err != nil {
		t.Fatal(err)
	}
	return task
}

func receiptReport(now time.Time, results ...nodeprotocol.TaskResult) nodeprotocol.NodeReport {
	return nodeprotocol.NodeReport{
		AgentID: "agt_receipts", ProtocolVersion: nodeprotocol.ProtocolVersion1,
		ReportedAtMS: now.UnixMilli(), Partial: true, Have: emptyProtocolHave(), TaskResults: results,
	}
}

func TestSyncDurableReceiptsSurviveLaterIssueAndStreamFailures(t *testing.T) {
	repos := newReceiptTestRepos(t)
	task := newReceiptTask(t, repos.NodeAgentTask, "task-receipt-offered")
	now := time.Date(2026, 9, 12, 12, 0, 0, 0, time.UTC)
	if _, err := repos.NodeAgentTask.Offer(t.Context(), task.AgentID, []string{task.Kind}, 1,
		int(nodeprotocol.MaxSyncBodyBytes), now); err != nil {
		t.Fatal(err)
	}
	issues := &switchableIssueRepo{NodeAgentIssueRepo: repos.NodeAgentIssue, fail: true}
	agents := &receiptAppliedAgentRepo{NodeAgentRepo: repos.NodeAgent}
	service, err := New(Options{
		Desired: repos.NativeDesired, Agents: agents, Issues: issues, Tasks: repos.NodeAgentTask,
		Users: repos.User, Clients: repos.PSPClient, Nodes: repos.Node, Settings: repos.Settings,
		Now: func() time.Time { return now },
	})
	if err != nil {
		t.Fatal(err)
	}
	normal := nodeprotocol.TaskResult{
		ID: task.TaskID, Kind: task.Kind, InputSHA256: task.InputSHA256,
		OK: true, Result: []byte("original terminal"),
	}
	orphan := nodeprotocol.TaskResult{
		ID: "task-receipt-orphan", Kind: task.Kind,
		InputSHA256: nodeprotocol.ComputeTaskInputSHA256(task.Kind, nil),
		OK:          true, Result: []byte{0, 0xff, 1},
	}
	// Neither capability is advertised this round. Receipt of a previously
	// executed outbox must not depend on the node still exposing its handler.
	report := receiptReport(now, normal, orphan)
	if _, err := service.Sync(t.Context(), report); err == nil {
		t.Fatal("later issue persistence failure acknowledged the report")
	}
	first, err := repos.NodeAgentTask.GetQuarantinedResult(t.Context(), task.AgentID, orphan.ID)
	if err != nil || first.Reason != domain.NodeAgentTaskQuarantineUnknownTask ||
		!bytes.Equal(first.Result.Result, orphan.Result) {
		t.Fatalf("receipt before issue failure = (%+v, %v)", first, err)
	}
	terminal, err := repos.NodeAgentTask.GetByTaskID(t.Context(), task.TaskID)
	if err != nil || terminal.Status != domain.NodeAgentTaskSucceeded || terminal.CompletedAt == nil {
		t.Fatalf("terminal before issue failure = (%+v, %v)", terminal, err)
	}
	completedAt := *terminal.CompletedAt
	issues.fail = false
	streamFailure := errors.New("forced stream persistence failure")
	agents.failure = streamFailure
	now = now.Add(time.Second)
	if _, err := service.Sync(t.Context(), report); !errors.Is(err, streamFailure) {
		t.Fatalf("later stream failure = %v, want injected failure", err)
	}
	agents.failure = nil
	now = now.Add(time.Second)
	response, err := service.Sync(t.Context(), report)
	if err != nil || len(response.Tasks) != 0 {
		t.Fatalf("receipt replay = (%+v, %v)", response, err)
	}
	replayed, err := repos.NodeAgentTask.GetQuarantinedResult(t.Context(), task.AgentID, orphan.ID)
	if err != nil || replayed.PayloadSHA256 != first.PayloadSHA256 ||
		!replayed.FirstSeenAt.Equal(first.FirstSeenAt) || !bytes.Equal(replayed.Payload, first.Payload) {
		t.Fatalf("quarantine replay changed original evidence = (%+v, %v)", replayed, err)
	}
	terminal, err = repos.NodeAgentTask.GetByTaskID(t.Context(), task.TaskID)
	if err != nil || terminal.CompletedAt == nil || !terminal.CompletedAt.Equal(completedAt) ||
		!bytes.Equal(terminal.Result, normal.Result) {
		t.Fatalf("terminal replay changed original result = (%+v, %v)", terminal, err)
	}
	if _, err := repos.NodeAgentTask.GetByTaskID(t.Context(), orphan.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("orphan receipt created a task: %v", err)
	}
}

func TestSyncNeverOfferedReceiptClosesDispatchWithoutReleasingQuota(t *testing.T) {
	repos := newReceiptTestRepos(t)
	task := newReceiptTask(t, repos.NodeAgentTask, "task-receipt-queued")
	now := time.Date(2026, 9, 12, 13, 0, 0, 0, time.UTC)
	service, err := New(Options{
		Desired: repos.NativeDesired, Agents: repos.NodeAgent, Issues: repos.NodeAgentIssue, Tasks: repos.NodeAgentTask,
		Users: repos.User, Clients: repos.PSPClient, Nodes: repos.Node, Settings: repos.Settings,
		Now: func() time.Time { return now },
	})
	if err != nil {
		t.Fatal(err)
	}
	result := nodeprotocol.TaskResult{
		ID: task.TaskID, Kind: task.Kind, InputSHA256: task.InputSHA256, OK: true, Result: []byte("evidence"),
	}
	report := receiptReport(now, result)
	report.Capabilities = []string{nodeprotocol.CapabilityTaskExecutionV1, nodeprotocol.TaskCapability(task.Kind)}
	response, err := service.Sync(t.Context(), report)
	if err != nil || len(response.Tasks) != 0 {
		t.Fatalf("never-offered receipt = (%+v, %v)", response, err)
	}
	stored, err := repos.NodeAgentTask.GetByTaskID(t.Context(), task.TaskID)
	if err != nil || stored.Status != domain.NodeAgentTaskQueued || stored.CompletedAt != nil ||
		stored.ResultOK != nil || stored.OfferCount != 0 || stored.DispatchClosedAt == nil {
		t.Fatalf("receipt fabricated an outcome or failed to close dispatch = (%+v, %v)", stored, err)
	}
	quarantined, err := repos.NodeAgentTask.GetQuarantinedResult(t.Context(), task.AgentID, task.TaskID)
	if err != nil || quarantined.Reason != domain.NodeAgentTaskQuarantineNeverOffered ||
		!bytes.Equal(quarantined.Result.Result, result.Result) {
		t.Fatalf("never-offered evidence = (%+v, %v)", quarantined, err)
	}
	// Fill the remaining default 256 active slots through the real repository.
	// The dispatch-closed unresolved row must still consume its original slot.
	for index := 1; index < 256; index++ {
		newReceiptTask(t, repos.NodeAgentTask, fmt.Sprintf("task-receipt-fill-%03d", index))
	}
	overflow := &domain.NodeAgentTask{
		TaskID: "task-receipt-overflow", AgentID: task.AgentID, Kind: task.Kind,
		InputSHA256: nodeprotocol.ComputeTaskInputSHA256(task.Kind, nil),
	}
	if _, _, err := repos.NodeAgentTask.CreateOrGet(t.Context(), overflow); !errors.Is(err, domain.ErrResourceExhausted) {
		t.Fatalf("closed unresolved row no longer consumes active quota: %v", err)
	}
	offered, err := repos.NodeAgentTask.Offer(t.Context(), task.AgentID, []string{task.Kind},
		nodeprotocol.MaxTasksPerResponse, int(nodeprotocol.MaxSyncBodyBytes), now.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	for _, candidate := range offered {
		if candidate.TaskID == task.TaskID {
			t.Fatal("dispatch-closed task was offered again")
		}
	}
}
