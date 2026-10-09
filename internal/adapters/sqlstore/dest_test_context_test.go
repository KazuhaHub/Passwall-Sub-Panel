package sqlstore

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"slices"
	"testing"
	"time"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"gorm.io/gorm"
)

func TestDestinationTestContextIsConsistentNarrowAndSelectsFirstNativeClient(t *testing.T) {
	r := newDestDefinitionRepo(t)
	u := userFromDomain(&domain.User{UPN: "simulation@example.test", GroupID: 17, UUID: "private-user-uuid", SubToken: "private-user-sub"})
	if err := r.db.Create(u).Error; err != nil {
		t.Fatal(err)
	}
	var nativeID int64
	for i, kind := range []domain.PanelKind{domain.PanelKind3XUI, domain.PanelKindPSP, domain.PanelKindPSP} {
		p := &domain.Panel{Name: fmt.Sprintf("simulation %s %d", kind, i), Kind: kind, URL: "fixture-url", APIToken: "private-panel-token", Password: "private-panel-password"}
		if err := (&xuiPanelRepo{db: r.db}).Save(t.Context(), p); err != nil {
			t.Fatal(err)
		}
		if err := r.db.Create(&pspClientRow{UserID: u.ID, PanelID: p.ID, Email: "simulation-client", UUID: "private-client-uuid", Password: "private-client-password"}).Error; err != nil {
			t.Fatal(err)
		}
		if kind == domain.PanelKindPSP && nativeID == 0 {
			nativeID = p.ID
			if err := r.db.Create(&nodeAgentRow{AgentID: "agt_simulation_reader", PanelID: p.ID, CredentialSHA256: "private-agent-digest"}).Error; err != nil {
				t.Fatal(err)
			}
			if err := r.db.Create(&destAgentPolicyRow{AgentID: "agt_simulation_reader", MintedBody: destBytes("private-minted-body"), AppliedBody: destBytes("private-applied-body")}).Error; err != nil {
				t.Fatal(err)
			}
		}
	}
	p := destinationTestPolicy("Simulation published policy")
	now := time.Now().UTC()
	if err := r.SavePolicy(t.Context(), &p, time.Time{}, now); err != nil {
		t.Fatal(err)
	}
	body := []byte(`{"generation":1}`)
	if err := r.Publish(t.Context(), 1, 0, body, now); err != nil {
		t.Fatal(err)
	}
	problem := errors.New("simulation read private columns or escaped its snapshot")
	if err := r.db.Callback().Query().Before("gorm:query").Register("dest-test-read-guard", func(tx *gorm.DB) {
		if _, ok := tx.Statement.ConnPool.(gorm.TxCommitter); !ok {
			tx.AddError(fmt.Errorf("%w: transaction type %T", problem, tx.Statement.ConnPool))
		}
		switch tx.Statement.Table {
		case "users":
			if !slices.Equal(tx.Statement.Selects, []string{"id", "group_id"}) {
				tx.AddError(problem)
			}
		case "xui_panels":
			if !slices.Equal(tx.Statement.Selects, []string{"id", "name", "kind"}) {
				tx.AddError(problem)
			}
		case "psp_clients":
			if !slices.Equal(tx.Statement.Selects, []string{"user_id"}) && !slices.Equal(tx.Statement.Selects, []string{"panel_id"}) {
				tx.AddError(problem)
			}
		case "node_agents":
			if slices.Contains(tx.Statement.Selects, "credential_sha256") || len(tx.Statement.Selects) == 0 {
				tx.AddError(problem)
			}
		case "dest_agent_policy":
			if !slices.Contains(tx.Statement.Omits, "MintedBody") || !slices.Contains(tx.Statement.Omits, "AppliedBody") {
				tx.AddError(problem)
			}
		case "dest_lists", "nodes", "psp_client_inbounds":
			tx.AddError(problem)
		}
	}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = r.db.Callback().Query().Remove("dest-test-read-guard") })
	got, err := r.TestContext(t.Context(), u.ID, 0)
	if err != nil || got.SelectedPanelID != nativeID || len(got.Panels) != 3 || len(got.UserIDs) != 1 || got.UserGroups[u.ID] != 17 || !got.Published || got.State.PublishedGeneration != 1 || !bytes.Equal(got.Snapshot.Body, body) || got.PolicyNames[p.ID] != p.Name {
		t.Fatalf("simulation failed its narrow consistent native-client read: err=%v uid=%d native=%d selected=%d panels=%d users=%d published=%t", err, u.ID, nativeID, got.SelectedPanelID, len(got.Panels), len(got.UserIDs), got.Published)
	}
	for _, p := range got.Panels {
		if p.Agent != nil && p.Agent.CredentialSHA256 != "" {
			t.Fatal("simulation exposed an agent credential")
		}
		if p.Runtime != nil && (len(p.Runtime.MintedBody) != 0 || len(p.Runtime.AppliedBody) != 0) {
			t.Fatal("simulation loaded executable runtime bodies")
		}
	}
}

func TestDestinationTestContextFailuresNeverReturnPartialMetadata(t *testing.T) {
	r := newDestDefinitionRepo(t)
	if _, err := r.TestContext(t.Context(), 9223372036854775807, 0); !errors.Is(err, domain.ErrNotFound) {
		t.Fatal("missing user was accepted")
	}
	if _, err := r.TestContext(t.Context(), 0, 9223372036854775807); !errors.Is(err, domain.ErrNotFound) {
		t.Fatal("missing panel was accepted")
	}
	if err := r.db.Callback().Query().Before("gorm:query").Register("dest-test-query-failure", func(tx *gorm.DB) {
		if tx.Statement.Table == "dest_policies" {
			tx.AddError(errors.New("private-simulation-store-marker"))
		}
	}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = r.db.Callback().Query().Remove("dest-test-query-failure") })
	got, err := r.TestContext(context.Background(), 0, 0)
	if err == nil || got.UserGroups != nil || got.Panels != nil || got.PolicyNames != nil {
		t.Fatal("simulation returned partial metadata after a failed query")
	}
}
