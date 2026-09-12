package sqlstore

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"testing"
	"time"

	nodeprotocol "github.com/KazuhaHub/passwall-node/protocol"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
)

func TestNativeAgentProvisioningIsAtomicAndDeletesOnlyAfterEmptyConvergence(t *testing.T) {
	db, err := openTestDB(t)
	if err != nil {
		t.Fatal(err)
	}
	if err := EnsureSchema(db); err != nil {
		t.Fatal(err)
	}
	repos := NewRepos(db)
	digest := sha256.Sum256([]byte("pspn_0123456789abcdefghijklmnopqrstuvwxyzABCDEFG"))
	panel := &domain.XUIPanel{Kind: domain.PanelKindPSP, Name: "native-1", URL: "psp://agt_native_1"}
	agent := &domain.NodeAgent{
		AgentID: "agt_native_1", CredentialSHA256: hex.EncodeToString(digest[:]), DesiredCoreVersion: "26.6.27",
	}
	ctx := context.Background()
	if err := repos.NativeAgentProvisioning.Create(ctx, panel, agent); err != nil {
		t.Fatal(err)
	}
	if panel.ID == 0 || agent.ID == 0 || agent.PanelID != panel.ID {
		t.Fatalf("provisioned identities = panel %+v agent %+v", panel, agent)
	}
	loaded, err := repos.NodeAgent.GetByPanelID(ctx, panel.ID)
	if err != nil || loaded.AgentID != agent.AgentID {
		t.Fatalf("agent lookup = (%+v, %v)", loaded, err)
	}

	now := time.Now().UTC()
	stream, _, err := repos.NodeAgent.MintStream(ctx, agent.AgentID, domain.NodeAgentStreamConfig,
		[]byte(`{"listeners":[{"key":"lst_1"}],"coverage":{"entries":1}}`), now)
	if err != nil {
		t.Fatal(err)
	}
	if err := repos.NodeAgent.RecordApplied(ctx, agent.AgentID, domain.NodeAgentStreamConfig,
		agent.Epoch, stream.DesiredVersion, stream.DesiredETag, now); err != nil {
		t.Fatal(err)
	}
	if err := repos.NativeAgentProvisioning.DeleteConverged(ctx, panel.ID); err == nil {
		t.Fatal("native panel with a non-empty confirmed config was deleted")
	}

	emptyConfig, _, err := repos.NodeAgent.MintStream(ctx, agent.AgentID, domain.NodeAgentStreamConfig,
		[]byte(`{"listeners":[],"coverage":{}}`), now.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if err := repos.NodeAgent.RecordApplied(ctx, agent.AgentID, domain.NodeAgentStreamConfig,
		agent.Epoch, emptyConfig.DesiredVersion, emptyConfig.DesiredETag, now.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	activeTask := newTask("task-block-delete", agent.AgentID, "reality_probe.v1", []byte("probe"))
	if _, _, err := repos.NodeAgentTask.CreateOrGet(ctx, activeTask); err != nil {
		t.Fatal(err)
	}
	if err := repos.NativeAgentProvisioning.DeleteConverged(ctx, panel.ID); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("delete with active native task = %v, want ErrConflict", err)
	}
	if _, err := repos.NodeAgentTask.Offer(ctx, agent.AgentID, []string{activeTask.Kind}, 1, int(nodeprotocol.MaxSyncBodyBytes), now.Add(2*time.Second)); err != nil {
		t.Fatal(err)
	}
	if err := repos.NodeAgentTask.CompleteBatch(ctx, agent.AgentID, []domain.NodeAgentTaskResult{{
		TaskID: activeTask.TaskID, Kind: activeTask.Kind, InputSHA256: activeTask.InputSHA256,
		OK: true, Result: []byte("done"),
	}}, now.Add(3*time.Second)); err != nil {
		t.Fatal(err)
	}
	if err := repos.NativeAgentProvisioning.DeleteConverged(ctx, panel.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := repos.XUIPanel.GetByID(ctx, panel.ID); err == nil {
		t.Fatal("native panel remained after atomic deletion")
	}
	if _, err := repos.NodeAgent.GetByAgentID(ctx, agent.AgentID); err == nil {
		t.Fatal("native agent remained after atomic deletion")
	}
	if _, err := repos.NodeAgentTask.GetByTaskID(ctx, activeTask.TaskID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("terminal task tombstone remained after native agent deletion: %v", err)
	}
}

func TestNativeAgentTaskCreateAndDeletionCannotProduceAnOrphan(t *testing.T) {
	db, err := openTestDB(t)
	if err != nil {
		t.Fatal(err)
	}
	if err := EnsureSchema(db); err != nil {
		t.Fatal(err)
	}
	repos := NewRepos(db)
	ctx := context.Background()
	// Repeat against one schema so the MySQL job exercises the actual InnoDB
	// secondary-index lock path enough times to catch a lock-order regression,
	// without paying for another database and migration per attempt.
	for attempt := 0; attempt < 16; attempt++ {
		t.Run(fmt.Sprintf("attempt_%02d", attempt), func(t *testing.T) {
			agentID := fmt.Sprintf("agt_task_delete_race_%02d", attempt)
			digest := sha256.Sum256([]byte("pspn_task_delete_race_" + agentID))
			panel := &domain.XUIPanel{
				Kind: domain.PanelKindPSP, Name: fmt.Sprintf("native-task-race-%02d", attempt), URL: "psp://" + agentID,
			}
			agent := &domain.NodeAgent{
				AgentID: agentID, CredentialSHA256: hex.EncodeToString(digest[:]), DesiredCoreVersion: "26.6.27",
			}
			if err := repos.NativeAgentProvisioning.Create(ctx, panel, agent); err != nil {
				t.Fatal(err)
			}
			task := newTask(fmt.Sprintf("task-delete-race-%02d", attempt), agent.AgentID, "reality_probe.v1", []byte("probe"))
			start := make(chan struct{})
			createResult := make(chan error, 1)
			deleteResult := make(chan error, 1)
			go func() {
				<-start
				_, _, err := repos.NodeAgentTask.CreateOrGet(ctx, task)
				createResult <- err
			}()
			go func() {
				<-start
				deleteResult <- repos.NativeAgentProvisioning.DeleteConverged(ctx, panel.ID)
			}()
			close(start)
			createErr, deleteErr := <-createResult, <-deleteResult

			switch {
			case createErr == nil && errors.Is(deleteErr, domain.ErrConflict):
				if _, err := repos.NodeAgent.GetByAgentID(ctx, agent.AgentID); err != nil {
					t.Fatalf("winning task create lost its agent: %v", err)
				}
				if _, err := repos.NodeAgentTask.GetByTaskID(ctx, task.TaskID); err != nil {
					t.Fatalf("winning task create was not durable: %v", err)
				}
			case deleteErr == nil && errors.Is(createErr, domain.ErrNotFound):
				if _, err := repos.NodeAgentTask.GetByTaskID(ctx, task.TaskID); !errors.Is(err, domain.ErrNotFound) {
					t.Fatalf("successful deletion left an orphan task: %v", err)
				}
				if _, err := repos.NodeAgent.GetByAgentID(ctx, agent.AgentID); !errors.Is(err, domain.ErrNotFound) {
					t.Fatalf("successful deletion left agent identity: %v", err)
				}
			default:
				t.Fatalf("create/delete race = create %v, delete %v; want exactly one valid winner", createErr, deleteErr)
			}
		})
	}
}

func TestNativeAgentCredentialRotationRevokesOldSecretWithoutResettingState(t *testing.T) {
	db, err := openTestDB(t)
	if err != nil {
		t.Fatal(err)
	}
	if err := EnsureSchema(db); err != nil {
		t.Fatal(err)
	}
	repos := NewRepos(db)
	oldDigest := sha256.Sum256([]byte("pspn_old_0123456789abcdefghijklmnopqrstuvwxyz"))
	panel := &domain.XUIPanel{Kind: domain.PanelKindPSP, Name: "native-rotate", URL: "psp://agt_rotate"}
	agent := &domain.NodeAgent{
		AgentID: "agt_rotate", CredentialSHA256: hex.EncodeToString(oldDigest[:]), DesiredCoreVersion: "26.6.27",
	}
	ctx := context.Background()
	if err := repos.NativeAgentProvisioning.Create(ctx, panel, agent); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	stream, _, err := repos.NodeAgent.MintStream(ctx, agent.AgentID, domain.NodeAgentStreamConfig,
		[]byte(`{"listeners":[],"coverage":{}}`), now)
	if err != nil {
		t.Fatal(err)
	}
	if err := repos.NodeAgent.RecordApplied(ctx, agent.AgentID, domain.NodeAgentStreamConfig,
		agent.Epoch, stream.DesiredVersion, stream.DesiredETag, now); err != nil {
		t.Fatal(err)
	}

	newDigest := sha256.Sum256([]byte("pspn_new_0123456789abcdefghijklmnopqrstuvwxyz"))
	rotated, err := repos.NativeAgentProvisioning.RotateCredential(ctx, panel.ID, hex.EncodeToString(newDigest[:]))
	if err != nil {
		t.Fatal(err)
	}
	if rotated.AgentID != agent.AgentID || rotated.Epoch != agent.Epoch || rotated.DesiredCoreVersion != agent.DesiredCoreVersion {
		t.Fatalf("rotation changed stable agent state: before=%+v after=%+v", agent, rotated)
	}
	if _, err := repos.NodeAgent.GetByCredentialSHA256(ctx, hex.EncodeToString(oldDigest[:])); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("old credential still authenticates: %v", err)
	}
	if current, err := repos.NodeAgent.GetByCredentialSHA256(ctx, hex.EncodeToString(newDigest[:])); err != nil || current.AgentID != agent.AgentID {
		t.Fatalf("new credential lookup = (%+v, %v)", current, err)
	}
	after, err := repos.NodeAgent.GetStream(ctx, agent.AgentID, domain.NodeAgentStreamConfig)
	if err != nil || after.AppliedETag != stream.DesiredETag || after.AppliedVersion != stream.DesiredVersion {
		t.Fatalf("rotation reset convergence state: (%+v, %v)", after, err)
	}
}
