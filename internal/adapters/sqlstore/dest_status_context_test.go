package sqlstore

import (
	"encoding/json"
	"errors"
	"github.com/KazuhaHub/passwall-protocol/protocol"
	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/service/destpolicy"
	"gorm.io/gorm"
	"reflect"
	"slices"
	"testing"
	"time"
)

func TestDestinationStatusContextUsesNarrowConsistentLazyBodyProof(t *testing.T) {
	r := newDestDefinitionRepo(t)
	now := time.Now().UTC()
	p := &domain.Panel{Name: "status native", Kind: domain.PanelKindPSP, URL: "psp://status", APIToken: "private-panel-secret", PanelVersion: "v0.3.0"}
	if err := (&xuiPanelRepo{db: r.db}).Save(t.Context(), p); err != nil {
		t.Fatal(err)
	}
	a := nodeAgentRow{AgentID: "agt_status", PanelID: p.ID, Epoch: 1, CredentialSHA256: "private-agent-secret", ObservedCoreEngine: "xray", ObservedCapabilities: jsonStrings{protocol.CapabilityDestinationPolicy, "audit.hits.v1"}, LastSeen: &now}
	if err := r.db.Create(&a).Error; err != nil {
		t.Fatal(err)
	}
	policy := &protocol.DestinationPolicy{Collect: protocol.CollectHits, CollectRevision: 1, Rules: []protocol.DestinationRule{{ID: "p1", Action: protocol.RuleBlock, Ports: "443"}}}
	body, _ := json.Marshal(policy)
	digest := protocol.PolicyDigest(policy)
	runtime := destAgentPolicyRow{AgentID: a.AgentID, MintedBody: destBytes(body), MintedSHA256: digest, MintedKind: "desired", MintedAt: &now, ReportedSHA256: digest, ReportedState: "applied", AppliedBody: destBytes([]byte("private-lkg-must-not-load")), AppliedGroups: jsonInt64s{456}, PrecheckListeners: jsonStrings{"lst_1"}}
	if err := r.db.Create(&runtime).Error; err != nil {
		t.Fatal(err)
	}
	if err := r.db.Create(&nodeRow{ID: 1, PanelID: p.ID, InboundID: 1, DisplayName: "native-443", InboundSettings: "private-listener-config"}).Error; err != nil {
		t.Fatal(err)
	}
	problem := errors.New("status read leaked data or escaped transaction")
	bodyReads := 0
	if err := r.db.Callback().Query().Before("gorm:query").Register("dest-status-narrow-guard", func(tx *gorm.DB) {
		if _, ok := tx.Statement.ConnPool.(gorm.TxCommitter); !ok {
			tx.AddError(problem)
		}
		switch tx.Statement.Table {
		case "xui_panels":
			if !slices.Equal(tx.Statement.Selects, []string{"id", "name", "kind", "panel_version", "audit_collect", "audit_collect_revision"}) {
				tx.AddError(problem)
			}
		case "node_agents":
			if !slices.Equal(tx.Statement.Selects, []string{"agent_id", "panel_id", "observed_capabilities", "observed_core_engine", "last_seen"}) {
				tx.AddError(problem)
			}
		case "dest_agent_policy":
			if slices.Equal(tx.Statement.Selects, []string{"minted_body"}) {
				bodyReads++
			} else if !slices.Contains(tx.Statement.Omits, "MintedBody") || !slices.Contains(tx.Statement.Omits, "AppliedBody") {
				tx.AddError(problem)
			}
		case "nodes":
			if !slices.Equal(tx.Statement.Selects, []string{"id", "display_name"}) {
				tx.AddError(problem)
			}
		case "groups":
			if !slices.Equal(tx.Statement.Selects, []string{"id", "name"}) {
				tx.AddError(problem)
			}
		case "dest_lists", "dest_policies", "dest_policy_snapshots", "users", "psp_clients", "psp_client_inbounds":
			tx.AddError(problem)
		}
	}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = r.db.Callback().Query().Remove("dest-status-narrow-guard") })
	var cache destpolicy.CollectionFactsCache
	proof := func(p domain.DestStatusPanel, load func() ([]byte, error)) (domain.DestCollectionFacts, error) {
		if !destpolicy.NeedsCollectionProof(p, time.Minute, now) {
			return domain.DestCollectionFacts{}, nil
		}
		return cache.Read(p.Agent.AgentID, p.Runtime.MintedSHA256, load)
	}
	for i := 0; i < 2; i++ {
		got, err := r.StatusContext(t.Context(), proof)
		if err != nil || len(got.Panels) != 1 || !got.Panels[0].Facts.Hits || got.Panels[0].Listeners["lst_1"].Label != "native-443" || len(got.Panels[0].Runtime.MintedBody) != 0 || len(got.Panels[0].Runtime.AppliedBody) != 0 || got.Panels[0].Agent.CredentialSHA256 != "" {
			t.Fatalf("status projection failed narrow consistent guard: %v", err)
		}
	}
	if bodyReads != 1 {
		t.Fatal("warm status reread executable bodies")
	}
	if err := r.db.Model(&xuiPanelRow{}).Where("id = ?", p.ID).Update("audit_collect", "off").Error; err != nil {
		t.Fatal(err)
	}
	if _, err := r.StatusContext(t.Context(), proof); err != nil || bodyReads != 1 {
		t.Fatal("off mode read candidate body")
	}
	if err := r.db.Model(&destAgentPolicyRow{}).Where("agent_id = ?", a.AgentID).Update("over_limit", "broken private error").Error; err != nil {
		t.Fatal(err)
	}
	got, err := r.StatusContext(t.Context(), proof)
	if !errors.Is(err, domain.ErrUnavailable) || !reflect.DeepEqual(got, domain.DestStatusContext{}) {
		t.Fatal("failed status returned partial fleet metadata")
	}
}
