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

func TestSyncDoesNotDropLifecycleDeadlineIntoLegacyTaskWire(t *testing.T) {
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

func (r *lifecycleIgnoringTaskRepo) Offer(_ context.Context, _ string, _ []string, _, _ int, _ time.Time) ([]*domain.NodeAgentTask, error) {
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
