package nodesync

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	nodeprotocol "github.com/KazuhaHub/passwall-node/protocol"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

func TestSyncDoesNotAuthorizeLifecycleTaskWithoutExpiryCapability(t *testing.T) {
	repos := newReceiptTestRepos(t)
	now := time.Date(2026, 9, 12, 13, 0, 0, 0, time.UTC)
	lifecycle, err := domain.NewNodeTaskLifecycleSnapshot(now.UnixMilli(), now.Add(time.Minute).UnixMilli(), domain.DefaultNodeTaskLifecyclePolicy())
	if err != nil {
		t.Fatal(err)
	}
	protected := &domain.NodeAgentTask{
		TaskID: "task-lifecycle-protected", AgentID: "agt_receipts", Kind: "reality_probe.v1",
		InputSHA256: nodeprotocol.ComputeTaskInputSHA256("reality_probe.v1", nil), Lifecycle: lifecycle,
	}
	if _, _, err := repos.NodeAgentTask.CreateOrGet(t.Context(), protected); err != nil {
		t.Fatal(err)
	}
	service, err := New(Options{
		Desired: repos.NativeDesired, Agents: repos.NodeAgent, Issues: repos.NodeAgentIssue, Tasks: repos.NodeAgentTask,
		Users: repos.User, Clients: repos.PSPClient, Nodes: repos.Node, Settings: repos.Settings,
		Now: func() time.Time { return now },
	})
	if err != nil {
		t.Fatal(err)
	}
	report := receiptReport(now)
	report.Capabilities = []string{nodeprotocol.CapabilityTaskExecutionV1, nodeprotocol.TaskCapability(protected.Kind)}
	for _, at := range []time.Time{now, now.Add(2 * time.Minute), now.Add(100 * 24 * time.Hour)} {
		now = at
		response, err := service.Sync(t.Context(), report)
		if err != nil || len(response.Tasks) != 0 || response.Envelope.NextPollSeconds != defaultNextPollSeconds {
			t.Fatalf("pre-expiry wire authorized protected request at %v: response=%+v err=%v", at, response, err)
		}
		stored, err := repos.NodeAgentTask.GetByTaskID(t.Context(), protected.TaskID)
		if err != nil || stored.Status != domain.NodeAgentTaskQueued || stored.OfferCount != 0 ||
			stored.ResultOK != nil || stored.CompletedAt != nil || !reflect.DeepEqual(stored.Lifecycle, lifecycle) {
			t.Fatalf("blocked request changed at %v: task=%+v err=%v", at, stored, err)
		}
	}
	// The additive schema must not break the published foundation contract or
	// let a blocked older request starve an unrelated legacy fixture.
	legacy := newReceiptTask(t, repos.NodeAgentTask, "task-lifecycle-legacy")
	response, err := service.Sync(t.Context(), report)
	if err != nil || len(response.Tasks) != 1 || response.Tasks[0].ID != legacy.TaskID {
		t.Fatalf("legacy foundation replay was blocked: response=%+v err=%v", response, err)
	}
}

type lifecycleIgnoringTaskRepo struct {
	ports.NodeAgentTaskRepo
	task *domain.NodeAgentTask
}

func (r *lifecycleIgnoringTaskRepo) Offer(_ context.Context, _ string, _ ports.NodeAgentTaskOfferSupport, _, _ int, _ time.Time) ([]*domain.NodeAgentTask, error) {
	return []*domain.NodeAgentTask{r.task}, nil
}

func TestTaskProjectionFailsClosedWhenPortIgnoresLifecycleGate(t *testing.T) {
	lifecycle, err := domain.NewNodeTaskLifecycleSnapshot(1, 2, domain.DefaultNodeTaskLifecyclePolicy())
	if err != nil {
		t.Fatal(err)
	}
	service := &Service{tasks: &lifecycleIgnoringTaskRepo{task: &domain.NodeAgentTask{
		TaskID: "task-lifecycle-bad-port", Kind: "reality_probe.v1", Lifecycle: lifecycle,
		InputSHA256: nodeprotocol.ComputeTaskInputSHA256("reality_probe.v1", nil), OfferCount: 1,
	}}}
	tasks, first, err := service.offerTasks(t.Context(), "agt_test", []string{
		nodeprotocol.CapabilityTaskExecutionV1, nodeprotocol.TaskCapability("reality_probe.v1"),
	}, int(nodeprotocol.MaxSyncBodyBytes), time.UnixMilli(1))
	if !errors.Is(err, domain.ErrConflict) || len(tasks) != 0 || first {
		t.Fatalf("port dropped immutable deadline: tasks=%+v first=%v err=%v", tasks, first, err)
	}
}

func TestSyncLifecycleDispatchRequiresCurrentTripleCapabilityAndEchoesDeadline(t *testing.T) {
	const kind = "reality_probe.v1"
	for _, test := range []struct {
		name      string
		caps      []string
		wantOffer bool
	}{
		{"none", nil, false},
		{"kind_only", []string{nodeprotocol.TaskCapability(kind)}, false},
		{"expiry_kind_without_execution", []string{nodeprotocol.CapabilityTaskExpiryV1, nodeprotocol.TaskCapability(kind)}, false},
		{"execution_expiry_without_kind", []string{nodeprotocol.CapabilityTaskExecutionV1, nodeprotocol.CapabilityTaskExpiryV1}, false},
		{"legacy_execution_kind", []string{nodeprotocol.CapabilityTaskExecutionV1, nodeprotocol.TaskCapability(kind)}, false},
		{"all_three", []string{nodeprotocol.CapabilityTaskExecutionV1, nodeprotocol.CapabilityTaskExpiryV1, nodeprotocol.TaskCapability(kind)}, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			repos := newReceiptTestRepos(t)
			now := time.UnixMilli(30_000)
			snapshot, err := domain.NewNodeTaskLifecycleSnapshot(now.UnixMilli(), now.Add(time.Minute).UnixMilli(), domain.DefaultNodeTaskLifecyclePolicy())
			if err != nil {
				t.Fatal(err)
			}
			task := &domain.NodeAgentTask{
				TaskID: "task-triple-capability", AgentID: "agt_receipts", Kind: kind,
				InputSHA256: nodeprotocol.ComputeTaskInputSHA256(kind, nil), Lifecycle: snapshot,
			}
			if _, _, err := repos.NodeAgentTask.CreateOrGet(t.Context(), task); err != nil {
				t.Fatal(err)
			}
			service, err := New(Options{
				Desired: repos.NativeDesired, Agents: repos.NodeAgent, Issues: repos.NodeAgentIssue, Tasks: repos.NodeAgentTask,
				Users: repos.User, Clients: repos.PSPClient, Nodes: repos.Node, Settings: repos.Settings,
				Now: func() time.Time { return now },
			})
			if err != nil {
				t.Fatal(err)
			}
			report := receiptReport(now)
			report.Capabilities = test.caps
			response, err := service.Sync(t.Context(), report)
			if err != nil {
				t.Fatal(err)
			}
			if test.wantOffer {
				if len(response.Tasks) != 1 || response.Tasks[0].NotAfterMS != snapshot.NotAfterMS ||
					response.Tasks[0].InputSHA256 != task.InputSHA256 || response.Envelope.NextPollSeconds != 1 {
					t.Fatalf("deadline not dispatched intact: %+v", response)
				}
				// Capabilities are current-report facts, not sticky observations.
				report.Capabilities = nil
				if response, err := service.Sync(t.Context(), report); err != nil || len(response.Tasks) != 0 {
					t.Fatalf("previous capability leaked into next report: %+v err=%v", response, err)
				}
				// A legitimate late success is accepted even after deadline and
				// capability disappearance; start expiry is not completion TTL.
				now = time.UnixMilli(snapshot.NotAfterMS + 1)
				report.TaskResults = []nodeprotocol.TaskResult{{
					ID: task.TaskID, Kind: kind, InputSHA256: task.InputSHA256, NotAfterMS: snapshot.NotAfterMS,
					OK: true, Result: []byte("actual late success"),
				}}
				if _, err := service.Sync(t.Context(), report); err != nil {
					t.Fatal(err)
				}
				stored, err := repos.NodeAgentTask.GetByTaskID(t.Context(), task.TaskID)
				if err != nil || stored.Status != domain.NodeAgentTaskSucceeded || stored.CompletedAt == nil ||
					!stored.CompletedAt.Equal(now) || !reflect.DeepEqual(stored.Lifecycle, snapshot) {
					t.Fatalf("late outcome changed original authorization: %+v err=%v", stored, err)
				}
			} else if len(response.Tasks) != 0 || response.Envelope.NextPollSeconds != defaultNextPollSeconds {
				t.Fatalf("missing capability authorized work: %+v", response)
			}
		})
	}
}

func TestSyncCapabilitylessExpiryClosureDoesNotReopenOnClockRollback(t *testing.T) {
	repos := newReceiptTestRepos(t)
	now := time.UnixMilli(30_000)
	snapshot, err := domain.NewNodeTaskLifecycleSnapshot(now.UnixMilli(), now.Add(time.Minute).UnixMilli(), domain.DefaultNodeTaskLifecyclePolicy())
	if err != nil {
		t.Fatal(err)
	}
	task := &domain.NodeAgentTask{
		TaskID: "task-capless-expiry", AgentID: "agt_receipts", Kind: "reality_probe.v1",
		InputSHA256: nodeprotocol.ComputeTaskInputSHA256("reality_probe.v1", nil), Lifecycle: snapshot,
	}
	if _, _, err := repos.NodeAgentTask.CreateOrGet(t.Context(), task); err != nil {
		t.Fatal(err)
	}
	service, err := New(Options{
		Desired: repos.NativeDesired, Agents: repos.NodeAgent, Issues: repos.NodeAgentIssue, Tasks: repos.NodeAgentTask,
		Users: repos.User, Clients: repos.PSPClient, Nodes: repos.Node, Settings: repos.Settings,
		Now: func() time.Time { return now },
	})
	if err != nil {
		t.Fatal(err)
	}
	now = time.UnixMilli(snapshot.NotAfterMS)
	report := receiptReport(now)
	if response, err := service.Sync(t.Context(), report); err != nil || len(response.Tasks) != 0 {
		t.Fatalf("expiry sync=%+v err=%v", response, err)
	}
	stored, err := repos.NodeAgentTask.GetByTaskID(t.Context(), task.TaskID)
	if err != nil || stored.DispatchClosedAt == nil || stored.DispatchClosedReason != "task_authorization_expired" ||
		stored.Status != domain.NodeAgentTaskQueued || stored.ResultOK != nil || stored.CompletedAt != nil || stored.OfferCount != 0 {
		t.Fatalf("expiry fabricated a terminal or skipped closure: %+v err=%v", stored, err)
	}
	originalClosure := *stored.DispatchClosedAt
	now = time.UnixMilli(snapshot.IssuedAtMS + 1)
	report.Capabilities = []string{nodeprotocol.CapabilityTaskExecutionV1, nodeprotocol.CapabilityTaskExpiryV1, nodeprotocol.TaskCapability(task.Kind)}
	if response, err := service.Sync(t.Context(), report); err != nil || len(response.Tasks) != 0 {
		t.Fatalf("rollback reopened dispatch: %+v err=%v", response, err)
	}
	stored, err = repos.NodeAgentTask.GetByTaskID(t.Context(), task.TaskID)
	if err != nil || !stored.DispatchClosedAt.Equal(originalClosure) || !reflect.DeepEqual(stored.Lifecycle, snapshot) {
		t.Fatalf("closure/snapshot was rewritten: %+v err=%v", stored, err)
	}
}

func TestTaskOfferSupportDoesNotTreatInfrastructureAsExecutableKinds(t *testing.T) {
	support := taskOfferSupport([]string{nodeprotocol.CapabilityTaskExecutionV1, nodeprotocol.CapabilityTaskExpiryV1, nodeprotocol.TaskCapability("reality_probe.v1")})
	if !support.SupportsExpiry || !reflect.DeepEqual(support.EligibleKinds, []string{"reality_probe.v1"}) {
		t.Fatalf("support=%+v", support)
	}
}
