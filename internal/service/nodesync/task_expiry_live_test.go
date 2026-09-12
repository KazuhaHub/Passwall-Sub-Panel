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
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	nodeprotocol "github.com/KazuhaHub/passwall-node/protocol"

	"github.com/KazuhaHub/passwall-sub-panel/internal/adapters/sqlstore"
	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/service/nodesync"
	"github.com/KazuhaHub/passwall-sub-panel/internal/transport/http/handler"
)

// TestLive_RealNodeTaskExpiryContract exercises the published Node's real HTTP
// client, synchronizer, processor, worker and SQLite journal against PSP's real
// repository, coordinator and HTTP handler. The only handler is test-owned.
// Later response-task replays are deliberately injected by the HTTP fixture:
// they test delayed duplicate delivery, not PSP backup/restore or its dispatch
// policy. No database state is fabricated to stand in for a real worker claim.
//
// PSP_LIVE_NODE_REPO=/absolute/path/to/Passwall-Node go test \
// ./internal/service/nodesync -run TestLive_RealNodeTaskExpiryContract -v
func TestLive_RealNodeTaskExpiryContract(t *testing.T) {
	nodeRepo := os.Getenv("PSP_LIVE_NODE_REPO")
	if nodeRepo == "" {
		t.Skip("set PSP_LIVE_NODE_REPO to run the real Node task expiry test")
	}
	nodeRepo, err := filepath.Abs(nodeRepo)
	if err != nil {
		t.Fatal(err)
	}
	if info, err := os.Stat(filepath.Join(nodeRepo, "go.mod")); err != nil || info.IsDir() {
		t.Fatalf("PSP_LIVE_NODE_REPO does not contain go.mod: %v", err)
	}
	fixtureDir, err := os.MkdirTemp(nodeRepo, ".task-expiry-fixture-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(fixtureDir) })
	if err := os.WriteFile(filepath.Join(fixtureDir, "main.go"), []byte(realNodeTaskExpiryFixture), 0o600); err != nil {
		t.Fatal(err)
	}

	for _, mode := range []string{"late-completion-terminal-replay", "delayed-body-unknown-journal"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 90*time.Second)
			defer cancel()
			const baseMS int64 = 1_800_000_000_000
			const advanceMS int64 = 200
			const deadlineMS = baseMS + 100
			var nowMS atomic.Int64
			nowMS.Store(baseMS)
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
				AgentID: "agt_task_expiry_live", Epoch: 1,
				CredentialSHA256: nodeprotocol.ComputeTaskInputSHA256("contract_credential", nil),
			}
			panel := &domain.XUIPanel{Kind: domain.PanelKindPSP, Name: "task-expiry", URL: "psp://" + agentRow.AgentID}
			if err := repos.NativeAgentProvisioning.Create(ctx, panel, agentRow); err != nil {
				t.Fatal(err)
			}
			lifecycle, err := domain.NewNodeTaskLifecycleSnapshot(baseMS, deadlineMS, domain.DefaultNodeTaskLifecyclePolicy())
			if err != nil {
				t.Fatal(err)
			}
			task := &domain.NodeAgentTask{
				TaskID: "task-expiry-live", AgentID: agentRow.AgentID,
				Kind: "expiry_fixture.v1", Args: []byte{0, 0xff, 1}, Lifecycle: lifecycle,
			}
			task.InputSHA256 = nodeprotocol.ComputeTaskInputSHA256(task.Kind, task.Args)
			if _, created, err := repos.NodeAgentTask.CreateOrGet(ctx, task); err != nil || !created {
				t.Fatalf("create task = (%v, %v)", created, err)
			}
			coordinator, err := nodesync.New(nodesync.Options{
				Desired: repos.NativeDesired, Agents: repos.NodeAgent, Issues: repos.NodeAgentIssue,
				Tasks: repos.NodeAgentTask, Users: repos.User, Clients: repos.PSPClient,
				Nodes: repos.Node, Panels: repos.XUIPanel, Settings: repos.Settings,
				Now: func() time.Time { return time.UnixMilli(nowMS.Load()) },
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
			var requests, resultReceipts, issueReceipts atomic.Int32
			serverErrors := make(chan error, 8)
			var originalTask atomic.Pointer[nodeprotocol.Task]
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
				fail := func(err error) {
					select {
					case serverErrors <- err:
					default:
					}
					http.Error(w, "test expiry invariant failed", http.StatusInternalServerError)
				}
				round := requests.Add(1)
				if round > 1 {
					// Keep PSP time consistent with the Node's controlled elapsed
					// advance. A frozen old PSP timestamp could re-authorize work.
					nowMS.Store(baseMS + advanceMS)
				}
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
				if mode == "late-completion-terminal-replay" {
					wantResults := 0
					if round > 1 {
						wantResults = 1
					}
					if len(report.TaskResults) != wantResults || len(report.Issues) != 0 {
						fail(fmt.Errorf("round %d: results=%+v issues=%+v", round, report.TaskResults, report.Issues))
						return
					}
					if wantResults != 0 {
						result := report.TaskResults[0]
						if result.ID != task.TaskID || result.Kind != task.Kind || result.InputSHA256 != task.InputSHA256 ||
							result.NotAfterMS != deadlineMS || !result.OK || result.Indeterminate || result.ErrorCode != "" ||
							!bytes.Equal(result.Result, []byte{2, 0xff, 3}) {
							fail(fmt.Errorf("round %d: late success changed identity/outcome: %+v", round, result))
							return
						}
						resultReceipts.Add(1)
					}
				} else {
					wantIssues := 0
					if round == 2 {
						wantIssues = 1
					}
					if len(report.TaskResults) != 0 || len(report.Issues) != wantIssues {
						fail(fmt.Errorf("round %d fabricated a result or duplicated replay evidence: results=%+v issues=%+v", round, report.TaskResults, report.Issues))
						return
					}
					if wantIssues != 0 {
						issue := report.Issues[0]
						if issue.Code != nodeprotocol.IssueTaskReplayFenced || issue.Key != task.TaskID {
							fail(fmt.Errorf("unexpected unknown-journal evidence: %+v", issue))
							return
						}
						issueReceipts.Add(1)
					}
				}
				recorded := httptest.NewRecorder()
				syncHandler.ServeHTTP(recorded, request)
				if recorded.Code != http.StatusOK {
					fail(fmt.Errorf("real PSP handler: status=%d body=%s", recorded.Code, recorded.Body.Bytes()))
					return
				}
				var response nodeprotocol.SyncResponse
				if err := json.Unmarshal(recorded.Body.Bytes(), &response); err != nil {
					fail(err)
					return
				}
				if response.Envelope.ComputedAtMS != nowMS.Load() {
					fail(fmt.Errorf("real PSP response ignored controlled Now: %+v", response.Envelope))
					return
				}
				if round > 1 && len(response.Tasks) != 0 {
					fail(fmt.Errorf("round %d: real PSP redispatched terminal or expired authorization before test-local replay: %+v", round, response.Tasks))
					return
				}
				if round == 1 {
					if len(response.Tasks) != 1 || response.Tasks[0].ID != task.TaskID || response.Tasks[0].NotAfterMS != deadlineMS {
						fail(fmt.Errorf("real PSP did not offer the immutable deadline: %+v", response.Tasks))
						return
					}
					offered := response.Tasks[0]
					originalTask.Store(&offered)
				} else if mode == "delayed-body-unknown-journal" || round == 2 {
					// Explicit test-local delayed duplicate of a genuinely offered
					// task in a fresh real PSP envelope; not a restore simulation.
					offered := originalTask.Load()
					if offered == nil {
						fail(fmt.Errorf("round %d preceded the original offer", round))
						return
					}
					response.Tasks = []nodeprotocol.Task{*offered}
				}
				if mode == "delayed-body-unknown-journal" && round == 2 {
					// Prove evidence is durable before a response can ACK it at
					// the Node. A fenced receipt must not create a task outcome.
					var issueRows int64
					if err := db.Table("node_agent_issues").Where("agent_id = ? AND code = ? AND object_key = ?",
						agentRow.AgentID, nodeprotocol.IssueTaskReplayFenced, task.TaskID).Count(&issueRows).Error; err != nil || issueRows != 1 {
						fail(fmt.Errorf("Issue not durably received before response: rows=%d err=%v", issueRows, err))
						return
					}
				}
				encoded, err := json.Marshal(response)
				if err != nil {
					fail(err)
					return
				}
				for key, values := range recorded.Header() {
					w.Header()[key] = values
				}
				if mode == "delayed-body-unknown-journal" && round == 1 {
					w.Header().Set("X-Fixture-Elapsed-After-EOF-MS", strconv.FormatInt(advanceMS, 10))
				}
				w.WriteHeader(http.StatusOK)
				// Send a prefix before the controlled EOF advance. There is no
				// real sleep; the Node's body wrapper advances its elapsed source.
				_, _ = w.Write(encoded[:len(encoded)/2])
				w.(http.Flusher).Flush()
				_, _ = w.Write(encoded[len(encoded)/2:])
			}))
			t.Cleanup(server.Close)
			command := exec.CommandContext(ctx, "go", "run", "./"+filepath.Base(fixtureDir),
				server.URL+"/v1/node/sync", filepath.Join(t.TempDir(), "node.db"), agentRow.AgentID,
				task.TaskID, mode, strconv.FormatInt(baseMS, 10))
			command.Dir = nodeRepo
			command.Env = append(os.Environ(), "GOWORK=off")
			output, commandErr := command.CombinedOutput()
			select {
			case err := <-serverErrors:
				t.Fatalf("PSP expiry invariant: %v; Node=%s", err, output)
			default:
			}
			if commandErr != nil {
				t.Fatalf("real Node fixture failed: %v\n%s", commandErr, output)
			}
			var evidence struct {
				Executions int   `json:"executions"`
				Delivered  bool  `json:"delivered"`
				Journal    bool  `json:"journal"`
				Outbox     int   `json:"outbox"`
				StartedMS  int64 `json:"started_ms"`
				FinishedMS int64 `json:"finished_ms"`
			}
			if err := json.Unmarshal(output, &evidence); err != nil {
				t.Fatalf("real Node evidence = (%+v, %v), output=%s", evidence, err, output)
			}
			stored, err := repos.NodeAgentTask.GetByTaskID(ctx, task.TaskID)
			if err != nil {
				t.Fatal(err)
			}
			if stored.Lifecycle == nil || *stored.Lifecycle != *lifecycle || requests.Load() != 3 || evidence.Outbox != 0 {
				t.Fatalf("changed authorization or incomplete rounds: task=%+v requests=%d evidence=%+v", stored, requests.Load(), evidence)
			}
			if mode == "late-completion-terminal-replay" {
				if evidence.Executions != 1 || !evidence.Journal || !evidence.Delivered || evidence.StartedMS >= deadlineMS || evidence.FinishedMS <= deadlineMS ||
					stored.Status != domain.NodeAgentTaskSucceeded || stored.ResultOK == nil || !*stored.ResultOK ||
					stored.CompletedAt == nil || stored.CompletedAt.UnixMilli() <= deadlineMS || resultReceipts.Load() != 2 || issueReceipts.Load() != 0 {
					t.Fatalf("latest-start deadline became a completion TTL or terminal re-executed: task=%+v evidence=%+v receipts=%d/%d",
						stored, evidence, resultReceipts.Load(), issueReceipts.Load())
				}
			} else {
				var issueRows, quarantines int64
				if err := db.Table("node_agent_issues").Where("agent_id = ? AND code = ? AND object_key = ?",
					agentRow.AgentID, nodeprotocol.IssueTaskReplayFenced, task.TaskID).Count(&issueRows).Error; err != nil {
					t.Fatal(err)
				}
				if err := db.Table("node_agent_task_result_quarantines").Where("agent_id = ? AND task_id = ?", agentRow.AgentID, task.TaskID).Count(&quarantines).Error; err != nil {
					t.Fatal(err)
				}
				if evidence.Executions != 0 || evidence.Journal || evidence.Delivered || stored.Status != domain.NodeAgentTaskOffered || stored.ResultOK != nil || stored.CompletedAt != nil ||
					stored.OfferCount != 1 || stored.DispatchClosedAt == nil || stored.DispatchClosedReason != "task_authorization_expired" ||
					stored.LastOfferedAt == nil || stored.LastOfferedAt.UnixMilli() != baseMS ||
					issueRows != 1 || quarantines != 0 || issueReceipts.Load() != 1 || resultReceipts.Load() != 0 {
					t.Fatalf("unknown delayed receipt fabricated an outcome, redispatched expiry, or duplicated Issue: task=%+v evidence=%+v issues=%d quarantines=%d receipts=%d/%d",
						stored, evidence, issueRows, quarantines, resultReceipts.Load(), issueReceipts.Load())
				}
			}
		})
	}
}

const realNodeTaskExpiryFixture = `package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"sync"
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

// The transport still uses real HTTP. Only the elapsed source is controlled:
// it advances when the complete response body reaches EOF, not at headers.
type elapsedTransport struct { base http.RoundTripper; elapsed *atomic.Int64 }
func (t elapsedTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	response, err := t.base.RoundTrip(request)
	if err != nil { return nil, err }
	advance := int64(0)
	if raw := response.Header.Get("X-Fixture-Elapsed-After-EOF-MS"); raw != "" {
		advance, err = strconv.ParseInt(raw, 10, 64)
		if err != nil || advance < 0 { response.Body.Close(); return nil, fmt.Errorf("invalid fixture elapsed advance %q", raw) }
	}
	response.Body = &elapsedBody{ReadCloser: response.Body, advance: advance, elapsed: t.elapsed}
	return response, nil
}
type elapsedBody struct { io.ReadCloser; advance int64; elapsed *atomic.Int64; once sync.Once }
func (b *elapsedBody) Read(p []byte) (int, error) {
	n, err := b.ReadCloser.Read(p)
	if err == io.EOF { b.once.Do(func() { b.elapsed.Add(b.advance * int64(time.Millisecond)) }) }
	return n, err
}
type captureSyncer struct { *agent.HTTPSyncer; response protocol.SyncResponse }
func (s *captureSyncer) Sync(ctx context.Context, report protocol.NodeReport) (protocol.SyncResponse, error) {
	response, err := s.HTTPSyncer.Sync(ctx, report)
	if err == nil { s.response = response }
	return response, err
}
func main() {
	if err := run(); err != nil { fmt.Fprintln(os.Stderr, err); os.Exit(1) }
}
func run() (runErr error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	baseMS, err := strconv.ParseInt(os.Args[6], 10, 64)
	if err != nil { return err }
	deadlineMS := baseMS + 100
	mode := os.Args[5]
	store, err := statesqlite.Open(ctx, os.Args[2])
	if err != nil { return err }
	defer store.Close()
	var elapsed atomic.Int64
	now := func() time.Time { return time.UnixMilli(baseMS + elapsed.Load()/int64(time.Millisecond)) }
	clock, err := agent.NewControlPlaneTaskClock(agent.ClockOptions{
		Elapsed: func() (time.Duration, error) { return time.Duration(elapsed.Load()), nil },
		MaxAnchorAge: time.Second, MaxRoundTrip: time.Second, Uncertainty: time.Millisecond,
	})
	if err != nil { return err }
	var executions atomic.Int32
	registry, err := agent.NewTaskRegistry(map[string]agent.TaskHandler{
		"expiry_fixture.v1": agent.TaskHandlerFunc(func(_ context.Context, task protocol.Task) ([]byte, error) {
			executions.Add(1)
			if mode != "late-completion-terminal-replay" { return nil, fmt.Errorf("unknown delayed journal entered Execute") }
			if task.NotAfterMS != deadlineMS || now().UnixMilli() >= deadlineMS { return nil, fmt.Errorf("handler started without fresh authorization: %+v", task) }
			// Starting was authorized. Completing after the latest-start deadline
			// is still success; it is not a handler timeout or completion TTL.
			elapsed.Add(int64(200 * time.Millisecond))
			return []byte{2, 0xff, 3}, nil
		}),
	})
	if err != nil { return err }
	worker, err := agent.NewTaskWorker(agent.TaskWorkerOptions{Store: store, Registry: registry, Now: now, Clock: clock})
	if err != nil { return err }
	ready := make(chan struct{}, 4)
	worker.SetResultNotifier(func() { select { case ready <- struct{}{}: default: } })
	workerCtx, stopWorker := context.WithCancel(ctx)
	workerDone := make(chan error, 1)
	go func() { workerDone <- worker.Run(workerCtx) }()
	defer func() {
		stopWorker()
		if err := <-workerDone; runErr == nil && err != nil { runErr = fmt.Errorf("real worker failed: %w", err) }
	}()
	processor, err := agent.NewProcessor(agent.ProcessorOptions{
		Store: store, Runtime: idleRuntime{},
		Issues: agent.OutboxIssueSink{Store: store, Map: agent.DefaultIssueMapper, NowMS: func() int64 { return now().UnixMilli() }},
		SkewToleranceRounds: 3, ObjectIssueTimeout: time.Minute, Now: now,
		TaskWake: worker.Wake, TaskClock: clock,
	})
	if err != nil { return err }
	transport := http.DefaultTransport.(*http.Transport).Clone()
	defer transport.CloseIdleConnections()
	httpSyncer, err := agent.NewHTTPSyncer(os.Args[1], agent.HTTPOptions{
		AllowInsecureHTTP: true,
		Client: &http.Client{Transport: elapsedTransport{base: transport, elapsed: &elapsed}, Timeout: 10*time.Second},
	})
	if err != nil { return err }
	syncer := &captureSyncer{HTTPSyncer: httpSyncer}
	synchronizer := agent.Synchronizer{
		Reports: agent.ReportBuilder{AgentID: os.Args[3], AgentVersion: "task-expiry-fixture", CoreEngine: "xray",
			CoreVersion: "coreless", CoreState: "running", Store: store, Capabilities: worker.Capabilities()},
		Syncer: syncer, Store: store, Processor: processor, TaskClock: clock,
	}
	if _, err := synchronizer.SyncOnce(ctx, false); err != nil { return fmt.Errorf("initial deadline offer: %w", err) }
	if len(syncer.response.Tasks) != 1 || syncer.response.Tasks[0].ID != os.Args[4] || syncer.response.Tasks[0].NotAfterMS != deadlineMS {
		return fmt.Errorf("real PSP did not offer protected task: %+v", syncer.response.Tasks)
	}
	checkUnknown := func(phase string, issueCount int) error {
		if _, err := store.Task(ctx, os.Args[4]); !errors.Is(err, state.ErrNotFound) { return fmt.Errorf("%s invented journal: %v", phase, err) }
		batch, err := store.PendingOutbox(ctx, 256)
		if err != nil { return err }
		if executions.Load() != 0 || len(batch.TaskResults) != 0 || len(batch.Issues) != issueCount || len(batch.IDs) != issueCount {
			return fmt.Errorf("%s: executions=%d outbox=%+v", phase, executions.Load(), batch)
		}
		return nil
	}
	checkTerminal := func(phase string, delivered bool) error {
		task, err := store.Task(ctx, os.Args[4])
		if err != nil { return err }
		batch, err := store.PendingOutbox(ctx, 256)
		if err != nil { return err }
		want := 1
		if delivered { want = 0 }
		if executions.Load() != 1 || task.State != state.TaskSucceeded || task.NotAfterMS != deadlineMS || task.StartedAtMS >= deadlineMS ||
			task.FinishedAtMS <= deadlineMS || task.ResultDelivered != delivered || len(batch.TaskResults) != want || len(batch.IDs) != want || len(batch.Issues) != 0 {
			return fmt.Errorf("%s: executions=%d journal=%+v outbox=%+v", phase, executions.Load(), task, batch)
		}
		if _, err := store.ClaimNextTaskFenced(ctx, now().UnixMilli(), clock); !errors.Is(err, state.ErrNotFound) { return fmt.Errorf("%s terminal became executable: %v", phase, err) }
		return nil
	}
	if mode == "late-completion-terminal-replay" {
		select { case <-ready: case <-ctx.Done(): return ctx.Err() }
		if err := checkTerminal("started fresh and completed late", false); err != nil { return err }
		if _, err := synchronizer.SyncOnce(ctx, true); err != nil { return fmt.Errorf("late result receipt and terminal replay: %w", err) }
		if err := checkTerminal("expired terminal replay rearmed exact result", false); err != nil { return err }
		if _, err := synchronizer.SyncOnce(ctx, true); err != nil { return fmt.Errorf("terminal replay result receipt: %w", err) }
		if err := checkTerminal("terminal replay ACK", true); err != nil { return err }
		task, err := store.Task(ctx, os.Args[4])
		if err != nil { return err }
		return json.NewEncoder(os.Stdout).Encode(map[string]any{
			"executions": executions.Load(), "delivered": task.ResultDelivered, "journal": true, "outbox": 0,
			"started_ms": task.StartedAtMS, "finished_ms": task.FinishedAtMS,
		})
	}
	if err := checkUnknown("complete body crossed start deadline", 1); err != nil { return err }
	for round := 2; round <= 3; round++ {
		result, err := synchronizer.SyncOnce(ctx, true)
		if err != nil { return fmt.Errorf("unknown journal replay round %d: %w", round, err) }
		if result.ReportImmediately { return fmt.Errorf("duplicate fenced replay caused immediate-poll loop at round %d", round) }
		if err := checkUnknown(fmt.Sprintf("replay round %d after Issue ACK", round), 0); err != nil { return err }
	}
	return json.NewEncoder(os.Stdout).Encode(map[string]any{"executions": executions.Load(), "delivered": false, "journal": false, "outbox": 0})
}
`
