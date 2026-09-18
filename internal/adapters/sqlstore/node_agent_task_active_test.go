package sqlstore

import (
	"context"
	"testing"

	nodeprotocol "github.com/KazuhaHub/passwall-node/protocol"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

const activeProbeKind = "diagnostics.collect.v1"

func mintProbeTask(t *testing.T, repos ports.Repos, agentID, taskID, kind string) {
	t.Helper()
	args := []byte(`{"schema_version":1,"sections":[],"max_events":0}`)
	if _, _, err := repos.NodeAgentTask.CreateOrGet(context.Background(), &domain.NodeAgentTask{
		TaskID: taskID, AgentID: agentID, Kind: kind, Args: args,
		InputSHA256: nodeprotocol.ComputeTaskInputSHA256(kind, args),
		Status:      domain.NodeAgentTaskQueued,
	}); err != nil {
		t.Fatalf("mint %s: %v", taskID, err)
	}
}

// Section 13.1 allows one ACTIVE diagnostic per agent, and the query behind
// that has to mean exactly that: the same agent and kind, still non-terminal.
// Anything narrower merges a repeat that should have been refused, and anything
// wider refuses a second diagnostic that should be allowed.
func TestActiveByKindFindsOnlyTheAgentsNonTerminalTask(t *testing.T) {
	ctx := context.Background()
	repos, db := newTaskTestReposWithQuota(t, defaultNodeAgentTaskQuota())
	createTaskTestAgent(t, repos, "agt_active", 1)
	createTaskTestAgent(t, repos, "agt_other", 2)

	if task, err := repos.NodeAgentTask.ActiveByKind(ctx, "agt_active", activeProbeKind); err != nil || task != nil {
		t.Fatalf("an agent with no tasks returned (%+v, %v)", task, err)
	}

	mintProbeTask(t, repos, "agt_active", "diag-1", activeProbeKind)
	found, err := repos.NodeAgentTask.ActiveByKind(ctx, "agt_active", activeProbeKind)
	if err != nil || found == nil || found.TaskID != "diag-1" {
		t.Fatalf("active = (%+v, %v), want diag-1", found, err)
	}

	// A DIFFERENT KIND IS A DIFFERENT QUESTION, and a different agent is a
	// different machine; neither may be merged into this one.
	if task, err := repos.NodeAgentTask.ActiveByKind(ctx, "agt_active", "agent.upgrade.v1"); err != nil || task != nil {
		t.Fatalf("another kind returned (%+v, %v)", task, err)
	}
	if task, err := repos.NodeAgentTask.ActiveByKind(ctx, "agt_other", activeProbeKind); err != nil || task != nil {
		t.Fatalf("another agent returned (%+v, %v)", task, err)
	}

	// A TERMINAL TASK IS NOT ACTIVE, which is what lets the same agent run
	// another diagnostic tomorrow. Without this the "one active" rule would
	// silently become "one ever".
	if err := db.Exec(`UPDATE node_agent_tasks SET status = ? WHERE task_id = ?`,
		string(domain.NodeAgentTaskSucceeded), "diag-1").Error; err != nil {
		t.Fatal(err)
	}
	if task, err := repos.NodeAgentTask.ActiveByKind(ctx, "agt_active", activeProbeKind); err != nil || task != nil {
		t.Fatalf("a finished task was returned as active: (%+v, %v)", task, err)
	}
}
