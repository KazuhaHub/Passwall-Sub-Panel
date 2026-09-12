package handler

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	nodeprotocol "github.com/KazuhaHub/passwall-node/protocol"

	"github.com/KazuhaHub/passwall-sub-panel/internal/adapters/sqlstore"
	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
	"github.com/KazuhaHub/passwall-sub-panel/internal/service/nodesync"
)

func newHTTPReceiptFixture(t *testing.T) (ports.Repos, *NodeSyncHandler, time.Time) {
	t.Helper()
	db, err := sqlstore.Open("sqlite", filepath.Join(t.TempDir(), "node-http-receipts.db"))
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
		AgentID: "agt_http_receipts", PanelID: 929,
		CredentialSHA256: nodeprotocol.ComputeTaskInputSHA256("agt_http_receipts", nil),
	}); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 9, 12, 14, 0, 0, 0, time.UTC)
	service, err := nodesync.New(nodesync.Options{
		Desired: repos.NativeDesired, Agents: repos.NodeAgent, Issues: repos.NodeAgentIssue, Tasks: repos.NodeAgentTask,
		Users: repos.User, Clients: repos.PSPClient, Nodes: repos.Node, Settings: repos.Settings,
		Now: func() time.Time { return now },
	})
	if err != nil {
		t.Fatal(err)
	}
	handler, err := NewNodeSyncHandler(service, NodeAuthenticatorFunc(func(*http.Request) (string, error) {
		return "agt_http_receipts", nil
	}))
	if err != nil {
		t.Fatal(err)
	}
	return repos, handler, now
}

func createHTTPReceiptTask(t *testing.T, tasks ports.NodeAgentTaskRepo, id, agentID string) *domain.NodeAgentTask {
	t.Helper()
	task := &domain.NodeAgentTask{TaskID: id, AgentID: agentID, Kind: "reality_probe.v1", Args: []byte("input")}
	task.InputSHA256 = nodeprotocol.ComputeTaskInputSHA256(task.Kind, task.Args)
	if _, _, err := tasks.CreateOrGet(t.Context(), task); err != nil {
		t.Fatal(err)
	}
	return task
}

func offerHTTPReceiptTask(t *testing.T, tasks ports.NodeAgentTaskRepo, task *domain.NodeAgentTask, now time.Time) {
	t.Helper()
	if _, err := tasks.Offer(t.Context(), task.AgentID, ports.NodeAgentTaskOfferSupport{EligibleKinds: []string{task.Kind}}, nodeprotocol.MaxTasksPerResponse,
		int(nodeprotocol.MaxSyncBodyBytes), now); err != nil {
		t.Fatal(err)
	}
}

func postHTTPReceiptReport(t *testing.T, handler http.Handler, now time.Time, results ...nodeprotocol.TaskResult) *httptest.ResponseRecorder {
	t.Helper()
	report := nodeprotocol.NodeReport{
		AgentID: "agt_http_receipts", ProtocolVersion: nodeprotocol.ProtocolVersion1,
		ReportedAtMS: now.UnixMilli(), Partial: true,
		Have: map[string]nodeprotocol.StreamState{
			nodeprotocol.StreamConfig: {}, nodeprotocol.StreamRoster: {}, nodeprotocol.StreamDirectives: {},
		},
		// No current handler capabilities: results are durable evidence, not a
		// request to authorize a new dispatch.
		TaskResults: results,
	}
	payload, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/v1/node/sync", bytes.NewReader(payload)))
	return response
}

func httpReceiptResult(task *domain.NodeAgentTask) nodeprotocol.TaskResult {
	return nodeprotocol.TaskResult{
		ID: task.TaskID, Kind: task.Kind, InputSHA256: task.InputSHA256,
		OK: true, Result: []byte("original result"),
	}
}

func TestNodeSyncHTTPReceivesMixedKnownAndUnknownResultsDurably(t *testing.T) {
	repos, handler, now := newHTTPReceiptFixture(t)
	task := createHTTPReceiptTask(t, repos.NodeAgentTask, "task-http-receipt-normal", "agt_http_receipts")
	offerHTTPReceiptTask(t, repos.NodeAgentTask, task, now)
	queued := createHTTPReceiptTask(t, repos.NodeAgentTask, "task-http-receipt-queued", task.AgentID)
	orphan := nodeprotocol.TaskResult{
		ID: "task-http-receipt-orphan", Kind: task.Kind,
		InputSHA256: nodeprotocol.ComputeTaskInputSHA256(task.Kind, nil), OK: true, Result: []byte{0, 0xff, 1},
	}
	response := postHTTPReceiptReport(t, handler, now, httpReceiptResult(task), orphan, httpReceiptResult(queued))
	if response.Code != http.StatusOK {
		t.Fatalf("mixed receipt status = %d body %s", response.Code, response.Body.String())
	}
	var reply nodeprotocol.SyncResponse
	if err := json.Unmarshal(response.Body.Bytes(), &reply); err != nil {
		t.Fatal(err)
	}
	if err := nodeprotocol.ValidateSyncResponse(reply); err != nil || len(reply.Tasks) != 0 {
		t.Fatalf("receipt response unexpectedly dispatched work = (%+v, %v)", reply, err)
	}
	stored, err := repos.NodeAgentTask.GetByTaskID(t.Context(), task.TaskID)
	if err != nil || stored.Status != domain.NodeAgentTaskSucceeded {
		t.Fatalf("normal terminal = (%+v, %v)", stored, err)
	}
	quarantined, err := repos.NodeAgentTask.GetQuarantinedResult(t.Context(), task.AgentID, orphan.ID)
	if err != nil || quarantined.Reason != domain.NodeAgentTaskQuarantineUnknownTask ||
		!bytes.Equal(quarantined.Result.Result, orphan.Result) {
		t.Fatalf("unknown durable result = (%+v, %v)", quarantined, err)
	}
	if _, err := repos.NodeAgentTask.GetByTaskID(t.Context(), orphan.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("unknown receipt created a task: %v", err)
	}
	stored, err = repos.NodeAgentTask.GetByTaskID(t.Context(), queued.TaskID)
	if err != nil || stored.Status != domain.NodeAgentTaskQueued || stored.DispatchClosedAt == nil ||
		stored.CompletedAt != nil || stored.ResultOK != nil {
		t.Fatalf("queued receipt fabricated a terminal or failed to close dispatch = (%+v, %v)", stored, err)
	}
	quarantined, err = repos.NodeAgentTask.GetQuarantinedResult(t.Context(), task.AgentID, queued.TaskID)
	if err != nil || quarantined.Reason != domain.NodeAgentTaskQuarantineNeverOffered {
		t.Fatalf("queued durable evidence = (%+v, %v)", quarantined, err)
	}
}

func TestNodeSyncHTTPConflictingEvidenceRollsBackWholeResultBatch(t *testing.T) {
	for _, conflict := range []string{"identity", "foreign_owner", "terminal"} {
		t.Run(conflict, func(t *testing.T) {
			repos, handler, now := newHTTPReceiptFixture(t)
			normal := createHTTPReceiptTask(t, repos.NodeAgentTask, "task-http-atomic-normal", "agt_http_receipts")
			offerHTTPReceiptTask(t, repos.NodeAgentTask, normal, now)
			knownAgent := normal.AgentID
			if conflict == "foreign_owner" {
				knownAgent = "agt_http_foreign"
				if err := repos.NodeAgent.Create(t.Context(), &domain.NodeAgent{
					AgentID: knownAgent, PanelID: 930,
					CredentialSHA256: nodeprotocol.ComputeTaskInputSHA256(knownAgent, nil),
				}); err != nil {
					t.Fatal(err)
				}
			}
			known := createHTTPReceiptTask(t, repos.NodeAgentTask, "task-http-atomic-conflict", knownAgent)
			offerHTTPReceiptTask(t, repos.NodeAgentTask, known, now)
			bad := httpReceiptResult(known)
			switch conflict {
			case "identity":
				bad.InputSHA256 = nodeprotocol.ComputeTaskInputSHA256(known.Kind, []byte("different input"))
			case "terminal":
				original := httpReceiptResult(known)
				if err := repos.NodeAgentTask.CompleteBatch(t.Context(), known.AgentID, []domain.NodeAgentTaskResult{{
					TaskID: original.ID, Kind: original.Kind, InputSHA256: original.InputSHA256,
					OK: original.OK, Result: original.Result,
				}}, now); err != nil {
					t.Fatal(err)
				}
				bad.Result = []byte("conflicting terminal")
			}
			orphan := nodeprotocol.TaskResult{
				ID: "task-http-atomic-orphan", Kind: normal.Kind,
				InputSHA256: nodeprotocol.ComputeTaskInputSHA256(normal.Kind, nil), OK: true,
			}
			response := postHTTPReceiptReport(t, handler, now, httpReceiptResult(normal), orphan, bad)
			if response.Code < 500 || response.Code >= 600 {
				t.Fatalf("conflicting batch status = %d body %s", response.Code, response.Body.String())
			}
			stored, err := repos.NodeAgentTask.GetByTaskID(t.Context(), normal.TaskID)
			if err != nil || stored.Status != domain.NodeAgentTaskOffered || stored.CompletedAt != nil {
				t.Fatalf("valid neighbor committed despite conflict = (%+v, %v)", stored, err)
			}
			if _, err := repos.NodeAgentTask.GetQuarantinedResult(t.Context(), normal.AgentID, orphan.ID); !errors.Is(err, domain.ErrNotFound) {
				t.Fatalf("unknown neighbor committed despite conflict: %v", err)
			}
		})
	}
}
