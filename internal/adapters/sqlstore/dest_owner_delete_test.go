package sqlstore

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"gorm.io/gorm"
)

func destinationTestUser(t *testing.T, r *DestDefinitionRepo, id int64) *domain.User {
	t.Helper()
	u := &domain.User{ID: id, UPN: fmt.Sprintf("dest-%d@example.test", id),
		UUID: fmt.Sprintf("dest-uuid-%d", id), SubToken: fmt.Sprintf("dest-token-%d", id), Role: domain.RoleUser}
	if err := NewRepos(r.db).User.Create(t.Context(), u); err != nil {
		t.Fatal(err)
	}
	return u
}

func TestDestinationUserDeletionRemovesExemptionWithGeneration(t *testing.T) {
	r := newDestDefinitionRepo(t)
	u := destinationTestUser(t, r, 12)
	now := time.UnixMilli(1791000000000).UTC()
	ex := domain.DestExemption{UserID: u.ID, CreatedBy: 9}
	if err := r.SaveExemption(t.Context(), &ex, true, now); err != nil {
		t.Fatal(err)
	}
	if err := NewRepos(r.db).User.Delete(t.Context(), u.ID); err != nil {
		t.Fatal(err)
	}
	defs, err := r.ReadDefinitions(t.Context())
	if err != nil || len(defs.Exemptions) != 0 || defs.State.Generation != 2 {
		t.Fatal("user deletion retained an exemption or missed publication")
	}
	if err := r.SaveExemption(t.Context(), &ex, true, now); !errors.Is(err, domain.ErrNotFound) {
		t.Fatal("exemption write recreated a deleted user's definition")
	}
	if err := NewRepos(r.db).User.Delete(t.Context(), u.ID); err != nil {
		t.Fatal(err)
	}
	state, _ := r.State(t.Context())
	if state.Generation != 2 {
		t.Fatal("idempotent deletion advanced publication")
	}
}

func TestDestinationUserDeletionRollsBackExemptionAndOwner(t *testing.T) {
	r := newDestDefinitionRepo(t)
	u := destinationTestUser(t, r, 12)
	now := time.UnixMilli(1791000000000).UTC()
	ex := domain.DestExemption{UserID: u.ID, CreatedBy: 9}
	if err := r.SaveExemption(t.Context(), &ex, true, now); err != nil {
		t.Fatal(err)
	}
	problem := errors.New("exemption generation failure")
	r.db.Callback().Update().Before("gorm:update").Register("user-cleanup-failure", func(tx *gorm.DB) {
		if tx.Statement.Table == "dest_policy_state" {
			tx.AddError(problem)
		}
	})
	t.Cleanup(func() { _ = r.db.Callback().Update().Remove("user-cleanup-failure") })
	if err := NewRepos(r.db).User.Delete(t.Context(), u.ID); !errors.Is(err, problem) {
		t.Fatal("owner deletion omitted definition transaction failure")
	}
	defs, err := r.ReadDefinitions(t.Context())
	_, ownerErr := NewRepos(r.db).User.GetByID(t.Context(), u.ID)
	if err != nil || ownerErr != nil || len(defs.Exemptions) != 1 || defs.State.Generation != 1 {
		t.Fatal("failed deletion partially removed the owner or exemption")
	}
}

func TestDestinationExemptionCreateAndUserDeleteCannotProduceOrphan(t *testing.T) {
	r := newDestDefinitionRepo(t)
	u := destinationTestUser(t, r, 12)
	now := time.UnixMilli(1791000000000).UTC()
	start := make(chan struct{})
	created, deleted := make(chan error, 1), make(chan error, 1)
	go func() {
		<-start
		ex := domain.DestExemption{UserID: u.ID, CreatedBy: 9}
		created <- r.SaveExemption(t.Context(), &ex, true, now)
	}()
	go func() {
		<-start
		deleted <- NewRepos(r.db).User.Delete(t.Context(), u.ID)
	}()
	close(start)
	createErr, deleteErr := <-created, <-deleted
	if deleteErr != nil || createErr != nil && !errors.Is(createErr, domain.ErrNotFound) {
		t.Fatalf("concurrent create/delete: create %v delete %v", createErr, deleteErr)
	}
	defs, err := r.ReadDefinitions(t.Context())
	if err != nil || len(defs.Exemptions) != 0 {
		t.Fatal("concurrent deletion left an orphan exemption")
	}
}

func TestDestinationNativeDeletionRemovesCandidateAndRollsBackCleanupFailures(t *testing.T) {
	for _, outcome := range []string{"delete", "failure", "not-converged"} {
		t.Run(outcome, func(t *testing.T) {
			r := newDestDefinitionRepo(t)
			repos := NewRepos(r.db)
			now := time.UnixMilli(1791000000000).UTC()
			panel := &domain.XUIPanel{Kind: domain.PanelKindPSP, Name: "candidate-owner", URL: "psp://agt_dest_delete"}
			agent := &domain.NodeAgent{AgentID: "agt_dest_delete", CredentialSHA256: strings.Repeat("a", 64), DesiredCoreVersion: "26.6.27"}
			if err := repos.NativeAgentProvisioning.Create(t.Context(), panel, agent); err != nil {
				t.Fatal(err)
			}
			if err := r.db.Create(&destAgentPolicyRow{AgentID: agent.AgentID, MintedKind: "empty", MintedBody: []byte("candidate"), AppliedBody: []byte("confirmed"), UpdatedAt: now}).Error; err != nil {
				t.Fatal(err)
			}
			problem := errors.New("candidate cleanup failure")
			if outcome == "failure" {
				r.db.Callback().Delete().Before("gorm:delete").Register("candidate-cleanup-failure", func(tx *gorm.DB) {
					if tx.Statement.Table == "dest_agent_policy" {
						tx.AddError(problem)
					}
				})
				t.Cleanup(func() { _ = r.db.Callback().Delete().Remove("candidate-cleanup-failure") })
			}
			if outcome == "not-converged" {
				if _, _, err := repos.NodeAgent.MintStream(t.Context(), agent.AgentID, domain.NodeAgentStreamConfig, []byte(`{"listeners":[]}`), now); err != nil {
					t.Fatal(err)
				}
			}
			err := repos.NativeAgentProvisioning.DeleteConverged(t.Context(), panel.ID)
			if outcome == "failure" && !errors.Is(err, problem) || outcome == "not-converged" && !errors.Is(err, domain.ErrValidation) || outcome == "delete" && err != nil {
				t.Fatalf("delete outcome %s: %v", outcome, err)
			}
			var policies int64
			if err := r.db.Model(&destAgentPolicyRow{}).Where("agent_id = ?", agent.AgentID).Count(&policies).Error; err != nil {
				t.Fatal(err)
			}
			_, panelErr := repos.XUIPanel.GetByID(t.Context(), panel.ID)
			_, agentErr := repos.NodeAgent.GetByAgentID(t.Context(), agent.AgentID)
			if outcome == "delete" {
				if policies != 0 || !errors.Is(panelErr, domain.ErrNotFound) || !errors.Is(agentErr, domain.ErrNotFound) {
					t.Fatal("successful native retirement left owner or candidate")
				}
			} else if policies != 1 || panelErr != nil || agentErr != nil {
				t.Fatal("failed native retirement lost owner or candidate")
			}
			state, err := r.State(t.Context())
			if err != nil || state.Generation != 0 {
				t.Fatal("runtime candidate cleanup changed definition generation")
			}
		})
	}
}
