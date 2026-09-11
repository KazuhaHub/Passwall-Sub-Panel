package sqlstore

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"testing"
	"time"

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
	if err := repos.NativeAgentProvisioning.DeleteConverged(ctx, panel.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := repos.XUIPanel.GetByID(ctx, panel.ID); err == nil {
		t.Fatal("native panel remained after atomic deletion")
	}
	if _, err := repos.NodeAgent.GetByAgentID(ctx, agent.AgentID); err == nil {
		t.Fatal("native agent remained after atomic deletion")
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
