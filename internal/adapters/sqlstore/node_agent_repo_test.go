package sqlstore

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"testing"
	"time"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
)

func TestNodeAgentCreateMintsAllStreamsAndStoresOnlyDigest(t *testing.T) {
	db, err := openTestDB(t)
	if err != nil {
		t.Fatal(err)
	}
	if err := EnsureSchema(db); err != nil {
		t.Fatal(err)
	}
	repo := NewRepos(db).NodeAgent
	agent := &domain.NodeAgent{
		AgentID: "agt_01", PanelID: 10,
		CredentialSHA256: strings.Repeat("ab", 32),
	}
	if err := repo.Create(context.Background(), agent); err != nil {
		t.Fatal(err)
	}
	if agent.ID == 0 || agent.Epoch != 1 {
		t.Fatalf("created agent = %+v", agent)
	}
	agents, err := repo.List(context.Background())
	if err != nil || len(agents) != 1 || agents[0].AgentID != agent.AgentID {
		t.Fatalf("agent list = (%+v, %v)", agents, err)
	}
	streams, err := repo.ListStreams(context.Background(), agent.AgentID)
	if err != nil {
		t.Fatal(err)
	}
	if len(streams) != 3 {
		t.Fatalf("stream count = %d, want exactly 3", len(streams))
	}
	for _, stream := range streams {
		if !stream.Stream.Valid() || stream.DesiredVersion != 0 || stream.DesiredETag != "" {
			t.Fatalf("invalid initial stream: %+v", stream)
		}
	}
}

func TestNodeAgentCredentialLookupUsesDigestAndDigestIsUnique(t *testing.T) {
	db, err := openTestDB(t)
	if err != nil {
		t.Fatal(err)
	}
	if err := EnsureSchema(db); err != nil {
		t.Fatal(err)
	}
	repo := NewRepos(db).NodeAgent
	digest := sha256.Sum256([]byte("a sufficiently long opaque node credential"))
	hexDigest := hex.EncodeToString(digest[:])
	first := &domain.NodeAgent{AgentID: "agt_auth_1", PanelID: 101, CredentialSHA256: hexDigest}
	if err := repo.Create(context.Background(), first); err != nil {
		t.Fatal(err)
	}
	loaded, err := repo.GetByCredentialSHA256(context.Background(), hexDigest)
	if err != nil || loaded.AgentID != first.AgentID {
		t.Fatalf("credential lookup = (%+v, %v)", loaded, err)
	}
	duplicate := &domain.NodeAgent{AgentID: "agt_auth_2", PanelID: 102, CredentialSHA256: hexDigest}
	if err := repo.Create(context.Background(), duplicate); err == nil {
		t.Fatal("duplicate credential digest was accepted")
	}
}

func TestNodeAgentCoreSelectionUsesColumnScopedUpdate(t *testing.T) {
	db, err := openTestDB(t)
	if err != nil {
		t.Fatal(err)
	}
	if err := EnsureSchema(db); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	repo := NewRepos(db).NodeAgent
	agent := &domain.NodeAgent{
		AgentID: "agt_core", PanelID: 15, CredentialSHA256: strings.Repeat("12", 32),
	}
	if err := repo.Create(ctx, agent); err != nil {
		t.Fatal(err)
	}
	if err := repo.UpdateCoreSelection(ctx, agent.AgentID, "26.9.9", true); err != nil {
		t.Fatal(err)
	}
	loaded, err := repo.GetByAgentID(ctx, agent.AgentID)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.DesiredCoreVersion != "26.9.9" || !loaded.AllowRestrictedReality || loaded.CredentialSHA256 != agent.CredentialSHA256 {
		t.Fatalf("updated agent = %+v", loaded)
	}
}

func TestNodeAgentMintStreamIsContentIdempotentAndConvergesByETag(t *testing.T) {
	db, err := openTestDB(t)
	if err != nil {
		t.Fatal(err)
	}
	if err := EnsureSchema(db); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	repo := NewRepos(db).NodeAgent
	agent := &domain.NodeAgent{
		AgentID: "agt_02", PanelID: 11,
		CredentialSHA256: strings.Repeat("cd", 32),
	}
	if err := repo.Create(ctx, agent); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC)
	bodyA := []byte(`{"clients":[],"coverage":{"entries":0}}`)
	first, minted, err := repo.MintStream(ctx, agent.AgentID, domain.NodeAgentStreamRoster, bodyA, now)
	if err != nil || !minted {
		t.Fatalf("first mint = (%+v, %v, %v)", first, minted, err)
	}
	second, minted, err := repo.MintStream(ctx, agent.AgentID, domain.NodeAgentStreamRoster, bodyA, now.Add(time.Minute))
	if err != nil || minted {
		t.Fatalf("same-content mint = (%+v, %v, %v)", second, minted, err)
	}
	if second.DesiredVersion != first.DesiredVersion || second.DesiredETag != first.DesiredETag {
		t.Fatalf("same input advanced identity: first=%+v second=%+v", first, second)
	}

	bodyB := []byte(`{"clients":[{"key":"cli_7"}],"coverage":{"entries":1}}`)
	third, minted, err := repo.MintStream(ctx, agent.AgentID, domain.NodeAgentStreamRoster, bodyB, now.Add(2*time.Minute))
	if err != nil || !minted || third.DesiredVersion != first.DesiredVersion+1 {
		t.Fatalf("changed mint = (%+v, %v, %v)", third, minted, err)
	}
	rollback, minted, err := repo.MintStream(ctx, agent.AgentID, domain.NodeAgentStreamRoster, bodyA, now.Add(3*time.Minute))
	if err != nil || !minted || rollback.DesiredVersion != third.DesiredVersion+1 {
		t.Fatalf("rollback mint = (%+v, %v, %v)", rollback, minted, err)
	}

	// The agent still holds A from v1. Desired is A again at v3, so ETag says
	// converged even though versions differ. A version comparison would lie.
	if err := repo.RecordApplied(ctx, agent.AgentID, domain.NodeAgentStreamRoster,
		agent.Epoch, first.DesiredVersion, first.DesiredETag, now.Add(4*time.Minute)); err != nil {
		t.Fatal(err)
	}
	final, err := repo.GetStream(ctx, agent.AgentID, domain.NodeAgentStreamRoster)
	if err != nil {
		t.Fatal(err)
	}
	if !final.Converged() || final.PendingSince != nil || final.AppliedVersion == final.DesiredVersion {
		t.Fatalf("ETag convergence not preserved across A/B/A: %+v", final)
	}
	if final.AppliedEpoch != agent.Epoch {
		t.Fatalf("applied epoch = %d, want %d", final.AppliedEpoch, agent.Epoch)
	}
	loaded, err := repo.GetByAgentID(ctx, agent.AgentID)
	if err != nil || loaded.LastSeen == nil || !loaded.LastSeen.Equal(now.Add(4*time.Minute)) {
		t.Fatalf("agent last_seen not updated: (%+v, %v)", loaded, err)
	}
}

func TestNodeAgentRejectsPlaintextCredential(t *testing.T) {
	db, err := openTestDB(t)
	if err != nil {
		t.Fatal(err)
	}
	if err := EnsureSchema(db); err != nil {
		t.Fatal(err)
	}
	err = NewRepos(db).NodeAgent.Create(context.Background(), &domain.NodeAgent{
		AgentID: "agt_bad", PanelID: 12, CredentialSHA256: "plaintext-secret",
	})
	if err == nil {
		t.Fatal("plaintext registration credential was accepted for persistence")
	}
}

func TestNodeAgentRecordAppliedRejectsStatePSPNeverMinted(t *testing.T) {
	db, err := openTestDB(t)
	if err != nil {
		t.Fatal(err)
	}
	if err := EnsureSchema(db); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	repo := NewRepos(db).NodeAgent
	agent := &domain.NodeAgent{
		AgentID: "agt_impossible", PanelID: 13,
		CredentialSHA256: strings.Repeat("ef", 32),
	}
	if err := repo.Create(ctx, agent); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 9, 9, 13, 0, 0, 0, time.UTC)
	desired, _, err := repo.MintStream(ctx, agent.AgentID, domain.NodeAgentStreamRoster, []byte(`{"clients":[]}`), now)
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name    string
		epoch   uint64
		version uint64
		etag    string
	}{
		{name: "future epoch", epoch: agent.Epoch + 1, version: desired.DesiredVersion, etag: desired.DesiredETag},
		{name: "future version", epoch: agent.Epoch, version: desired.DesiredVersion + 1, etag: desired.DesiredETag},
		{name: "same version different etag", epoch: agent.Epoch, version: desired.DesiredVersion, etag: strings.Repeat("0", 64)},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if err := repo.RecordApplied(ctx, agent.AgentID, domain.NodeAgentStreamRoster,
				test.epoch, test.version, test.etag, now.Add(time.Minute)); err == nil {
				t.Fatal("impossible applied state was accepted")
			}
			stream, err := repo.GetStream(ctx, agent.AgentID, domain.NodeAgentStreamRoster)
			if err != nil {
				t.Fatal(err)
			}
			if stream.AppliedEpoch != 0 || stream.AppliedVersion != 0 || stream.AppliedETag != "" || stream.LastSeen != nil {
				t.Fatalf("rejected state changed stream: %+v", stream)
			}
		})
	}
}
