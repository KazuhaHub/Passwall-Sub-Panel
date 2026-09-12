package nodesync_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	nodeprotocol "github.com/KazuhaHub/passwall-node/protocol"

	"github.com/KazuhaHub/passwall-sub-panel/internal/adapters/sqlstore"
	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/service/nodesync"
	"github.com/KazuhaHub/passwall-sub-panel/internal/transport/http/handler"
)

// TestLive_RealNodeTaskEvidenceReceipt runs a test-only handler in the real
// Node worker, journal, report builder, HTTP client and synchronizer against
// PSP's real storage, coordinator and HTTP boundary. Its final redispatch is
// deliberately a local replay of the original PSP response through the real
// Node Processor, not an end-to-end PSP backup-restore simulation.
//
// PSP_LIVE_NODE_REPO=/absolute/path/to/Passwall-Node go test \
// ./internal/service/nodesync -run TestLive_RealNodeTaskEvidenceReceipt -v
func TestLive_RealNodeTaskEvidenceReceipt(t *testing.T) {
	nodeRepo := os.Getenv("PSP_LIVE_NODE_REPO")
	if nodeRepo == "" {
		t.Skip("set PSP_LIVE_NODE_REPO to run the real Node task receipt test")
	}
	nodeRepo, err := filepath.Abs(nodeRepo)
	if err != nil {
		t.Fatal(err)
	}
	if info, err := os.Stat(filepath.Join(nodeRepo, "go.mod")); err != nil || info.IsDir() {
		t.Fatalf("PSP_LIVE_NODE_REPO does not contain go.mod: %v", err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 90*time.Second)
	defer cancel()
	db, err := sqlstore.Open("sqlite", filepath.Join(t.TempDir(), "psp.db"))
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
	agentRow := &domain.NodeAgent{
		AgentID: "agt_task_receipt_live", Epoch: 1,
		CredentialSHA256: nodeprotocol.ComputeTaskInputSHA256("contract_credential", nil),
	}
	panel := &domain.XUIPanel{Kind: domain.PanelKindPSP, Name: "task-receipt", URL: "psp://" + agentRow.AgentID}
	if err := repos.NativeAgentProvisioning.Create(ctx, panel, agentRow); err != nil {
		t.Fatal(err)
	}
	task := &domain.NodeAgentTask{
		TaskID: "task-receipt-live", AgentID: agentRow.AgentID,
		Kind: "receipt_fixture.v1", Args: []byte{0, 0xff, 1},
	}
	task.InputSHA256 = nodeprotocol.ComputeTaskInputSHA256(task.Kind, task.Args)
	if _, created, err := repos.NodeAgentTask.CreateOrGet(ctx, task); err != nil || !created {
		t.Fatalf("create task = (%v, %v)", created, err)
	}
	coordinator, err := nodesync.New(nodesync.Options{
		Desired: repos.NativeDesired, Agents: repos.NodeAgent, Issues: repos.NodeAgentIssue,
		Tasks: repos.NodeAgentTask, Users: repos.User, Clients: repos.PSPClient,
		Nodes: repos.Node, Panels: repos.XUIPanel, Settings: repos.Settings,
	})
	if err != nil {
		t.Fatal(err)
	}
	syncHandler, err := handler.NewNodeSyncHandler(coordinator, handler.NodeAuthenticatorFunc(
		func(*http.Request) (string, error) { return agentRow.AgentID, nil },
	))
	if err != nil {
		t.Fatal(err)
	}
	var requests, receipts atomic.Int32
	serverErrors := make(chan error, 8)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		fail := func(err error) {
			select {
			case serverErrors <- err:
			default:
			}
			http.Error(w, "test receipt invariant failed", http.StatusInternalServerError)
		}
		requests.Add(1)
		body, err := io.ReadAll(io.LimitReader(request.Body, nodeprotocol.MaxSyncBodyBytes+1))
		if err != nil {
			fail(err)
			return
		}
		_ = request.Body.Close()
		request.Body = io.NopCloser(bytes.NewReader(body))
		var report nodeprotocol.NodeReport
		if err := json.Unmarshal(body, &report); err != nil {
			fail(err)
			return
		}
		receipt := int32(0)
		if len(report.TaskResults) != 0 {
			receipt = receipts.Add(1)
			if len(report.TaskResults) != 1 || report.TaskResults[0].ID != task.TaskID || !report.TaskResults[0].OK {
				fail(fmt.Errorf("unexpected real Node result: %+v", report.TaskResults))
				return
			}
			if receipt == 1 {
				// The real Node has finished its handler and committed terminal +
				// outbox before sending this report. Lose only PSP's task ledger.
				deleted := db.WithContext(ctx).Exec("DELETE FROM node_agent_tasks WHERE agent_id = ? AND task_id = ?", agentRow.AgentID, task.TaskID)
				if deleted.Error != nil || deleted.RowsAffected != 1 {
					fail(fmt.Errorf("lose PSP task row: rows=%d err=%v", deleted.RowsAffected, deleted.Error))
					return
				}
			}
		}
		recorded := httptest.NewRecorder()
		syncHandler.ServeHTTP(recorded, request)
		if recorded.Code != http.StatusOK {
			fail(fmt.Errorf("real PSP handler: status=%d body=%s", recorded.Code, recorded.Body.Bytes()))
			return
		}
		if receipt != 0 {
			// Inspect through the real repository BEFORE allowing any response
			// bytes to reach the Node. Whole-batch 2xx means durable evidence
			// receipt, not that the now-missing task has become succeeded.
			quarantined, err := repos.NodeAgentTask.GetQuarantinedResult(ctx, agentRow.AgentID, task.TaskID)
			payload, marshalErr := json.Marshal(report.TaskResults[0])
			if err != nil || marshalErr != nil || quarantined.Reason != domain.NodeAgentTaskQuarantineUnknownTask ||
				!bytes.Equal(quarantined.Payload, payload) || !bytes.Equal(quarantined.Result.Result, []byte{2, 0xff, 3}) {
				fail(fmt.Errorf("receipt not durably preserved before response: (%+v, %v), marshal=%v", quarantined, err, marshalErr))
				return
			}
			if receipt == 1 {
				http.Error(w, "receipt committed but response failed", http.StatusInternalServerError)
				return
			}
			if receipt == 2 {
				w.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(w, `{"envelope":{"next_poll_seconds":-1}}`)
				return
			}
		}
		for key, values := range recorded.Header() {
			w.Header()[key] = values
		}
		w.WriteHeader(recorded.Code)
		_, _ = w.Write(recorded.Body.Bytes())
	}))
	defer server.Close()

	// A test-owned source directory inside the Node module permits its
	// internal packages without adding a production CLI or exported test API.
	fixtureDir, err := os.MkdirTemp(nodeRepo, ".task-receipt-fixture-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(fixtureDir) })
	fixturePath := filepath.Join(fixtureDir, "main.go")
	if err := os.WriteFile(fixturePath, []byte(realNodeTaskReceiptFixture), 0o600); err != nil {
		t.Fatal(err)
	}
	// Run a module-relative package, not an absolute main.go file: the latter
	// becomes command-line-arguments and fails internal import checks when the
	// checksum-pinned Node module is copied to a CI-owned temporary directory.
	command := exec.CommandContext(ctx, "go", "run", "./"+filepath.Base(fixtureDir),
		server.URL+"/v1/node/sync", filepath.Join(t.TempDir(), "node.db"), agentRow.AgentID, task.TaskID)
	command.Dir = nodeRepo
	command.Env = append(os.Environ(), "GOWORK=off")
	output, commandErr := command.CombinedOutput()
	select {
	case err := <-serverErrors:
		t.Fatalf("PSP receipt invariant: %v; Node=%s", err, output)
	default:
	}
	if commandErr != nil {
		t.Fatalf("real Node fixture failed: %v\n%s", commandErr, output)
	}
	var evidence struct {
		Executions int  `json:"executions"`
		Delivered  bool `json:"delivered"`
		Outbox     int  `json:"outbox"`
	}
	if err := json.Unmarshal(output, &evidence); err != nil || evidence.Executions != 1 || !evidence.Delivered || evidence.Outbox != 0 {
		t.Fatalf("real Node final evidence = (%+v, %v), output=%s", evidence, err, output)
	}
	if requests.Load() != 5 || receipts.Load() != 4 {
		t.Fatalf("HTTP requests=%d receipt replays=%d, want 5/4", requests.Load(), receipts.Load())
	}
	var ledgerRows, quarantineRows int64
	if err := db.Table("node_agent_tasks").Where("task_id = ?", task.TaskID).Count(&ledgerRows).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Table("node_agent_task_result_quarantines").Where("agent_id = ? AND task_id = ?", agentRow.AgentID, task.TaskID).Count(&quarantineRows).Error; err != nil {
		t.Fatal(err)
	}
	if ledgerRows != 0 || quarantineRows != 1 {
		t.Fatalf("receipt invented a task or duplicated evidence: ledger=%d quarantine=%d", ledgerRows, quarantineRows)
	}
}

const realNodeTaskReceiptFixture = `package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sync/atomic"
	"time"

	"github.com/KazuhaHub/passwall-node/internal/agent"
	"github.com/KazuhaHub/passwall-node/internal/state"
	statesqlite "github.com/KazuhaHub/passwall-node/internal/state/sqlite"
	"github.com/KazuhaHub/passwall-node/protocol"
)

type idleRuntime struct{}
func (idleRuntime) UpsertListener(context.Context, protocol.Listener) error { return nil }
func (idleRuntime) RemoveListener(context.Context, protocol.ListenerKey) error { return nil }
func (idleRuntime) UpsertClient(context.Context, protocol.Client) error { return nil }
func (idleRuntime) RemoveClient(context.Context, protocol.ClientKey) error { return nil }

type captureSyncer struct {
	*agent.HTTPSyncer
	response protocol.SyncResponse
}
func (s *captureSyncer) Sync(ctx context.Context, report protocol.NodeReport) (protocol.SyncResponse, error) {
	response, err := s.HTTPSyncer.Sync(ctx, report)
	if err == nil { s.response = response }
	return response, err
}

func main() {
	if err := run(); err != nil { fmt.Fprintln(os.Stderr, err); os.Exit(1) }
}

func run() error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	store, err := statesqlite.Open(ctx, os.Args[2])
	if err != nil { return err }
	defer store.Close()
	var executions atomic.Int32
	registry, err := agent.NewTaskRegistry(map[string]agent.TaskHandler{
		"receipt_fixture.v1": agent.TaskHandlerFunc(func(context.Context, protocol.Task) ([]byte, error) {
			executions.Add(1)
			return []byte{2, 0xff, 3}, nil
		}),
	})
	if err != nil { return err }
	worker, err := agent.NewTaskWorker(agent.TaskWorkerOptions{Store: store, Registry: registry})
	if err != nil { return err }
	workerCtx, stopWorker := context.WithCancel(ctx)
	workerDone := make(chan error, 1)
	go func() { workerDone <- worker.Run(workerCtx) }()
	defer func() { stopWorker(); <-workerDone }()
	processor, err := agent.NewProcessor(agent.ProcessorOptions{
		Store: store, Runtime: idleRuntime{},
		Issues: agent.OutboxIssueSink{Store: store, Map: agent.DefaultIssueMapper, NowMS: func() int64 { return time.Now().UnixMilli() }},
		SkewToleranceRounds: 3, ObjectIssueTimeout: time.Minute, TaskWake: worker.Wake,
	})
	if err != nil { return err }
	httpSyncer, err := agent.NewHTTPSyncer(os.Args[1], agent.HTTPOptions{AllowInsecureHTTP: true})
	if err != nil { return err }
	syncer := &captureSyncer{HTTPSyncer: httpSyncer}
	synchronizer := agent.Synchronizer{
		Reports: agent.ReportBuilder{AgentID: os.Args[3], AgentVersion: "task-receipt-fixture",
			CoreEngine: "xray", CoreVersion: "coreless", CoreState: "running", Store: store, Capabilities: registry.Capabilities()},
		Syncer: syncer, Store: store, Processor: processor,
	}
	if _, err := synchronizer.SyncOnce(ctx, false); err != nil { return fmt.Errorf("initial offer: %w", err) }
	originalResponse := syncer.response
	if len(originalResponse.Tasks) != 1 || originalResponse.Tasks[0].ID != os.Args[4] { return fmt.Errorf("real PSP did not offer task: %+v", originalResponse.Tasks) }
	ticker := time.NewTicker(5*time.Millisecond)
	defer ticker.Stop()
	for {
		task, err := store.Task(ctx, os.Args[4])
		if err != nil { return err }
		if task.State == state.TaskSucceeded { break }
		select { case <-ctx.Done(): return ctx.Err(); case <-ticker.C: }
	}
	check := func(phase string, delivered bool) error {
		task, err := store.Task(ctx, os.Args[4])
		if err != nil { return err }
		batch, err := store.PendingOutbox(ctx, 256)
		if err != nil { return err }
		want := 1
		if delivered { want = 0 }
		if task.State != state.TaskSucceeded || task.ResultDelivered != delivered || len(batch.TaskResults) != want || len(batch.IDs) != want || executions.Load() != 1 {
			return fmt.Errorf("%s: journal=%+v outboxResults=%d outboxRows=%d executions=%d", phase, task, len(batch.TaskResults), len(batch.IDs), executions.Load())
		}
		return nil
	}
	if err := check("executed before HTTP receipt", false); err != nil { return err }
	// Removing the kind capability must not block receipt of old results.
	synchronizer.Reports.Capabilities = nil
	for _, phase := range []string{"500 after durable receipt", "invalid 2xx envelope"} {
		if _, err := synchronizer.SyncOnce(ctx, true); err == nil { return fmt.Errorf("%s unexpectedly accepted", phase) }
		if err := check(phase, false); err != nil { return err }
	}
	if _, err := synchronizer.SyncOnce(ctx, true); err != nil { return fmt.Errorf("valid evidence receipt: %w", err) }
	if err := check("valid receipt ACK", true); err != nil { return err }
	// Replay the original, genuinely offered response locally through the real
	// Processor. This is intentionally not a PSP restore/reoffer simulation.
	processed, err := processor.Process(ctx, originalResponse)
	if err != nil || !processed.ReportImmediately { return fmt.Errorf("local original-response replay: (%+v, %v)", processed, err) }
	if err := check("terminal replay rearmed", false); err != nil { return err }
	if _, err := store.ClaimNextTask(ctx, time.Now().UnixMilli()); !errors.Is(err, state.ErrNotFound) { return fmt.Errorf("terminal replay became executable: %v", err) }
	if _, err := synchronizer.SyncOnce(ctx, true); err != nil { return fmt.Errorf("replayed evidence receipt: %w", err) }
	if err := check("replayed receipt ACK", true); err != nil { return err }
	finalTask, err := store.Task(ctx, os.Args[4])
	if err != nil { return err }
	finalOutbox, err := store.PendingOutbox(ctx, 256)
	if err != nil { return err }
	return json.NewEncoder(os.Stdout).Encode(map[string]any{"executions": executions.Load(), "delivered": finalTask.ResultDelivered, "outbox": len(finalOutbox.IDs)})
}
`
