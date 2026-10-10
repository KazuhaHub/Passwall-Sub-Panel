package sqlstore

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/KazuhaHub/passwall-protocol/protocol"
	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func legalCollectionNode(t *testing.T, db *gorm.DB, mode string, rules []protocol.DestinationRule) (int64, string) {
	t.Helper()
	p := &domain.Panel{Name: "private-disclosure-node", Kind: domain.PanelKindPSP, URL: "psp://private-disclosure", APIToken: "private-panel-credential"}
	if err := (&xuiPanelRepo{db: db}).Save(t.Context(), p); err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&xuiPanelRow{}).Where("id = ?", p.ID).Update("audit_collect", mode).Error; err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	agentID := fmt.Sprintf("agt_legal_collection_%d", p.ID)
	a := nodeAgentRow{AgentID: agentID, PanelID: p.ID, Epoch: 1, CredentialSHA256: "private-agent-credential", ObservedCoreEngine: "xray",
		ObservedCapabilities: jsonStrings{protocol.CapabilityDestinationPolicy, "audit.hits.v1", "audit.usage.v1"}, LastSeen: &now}
	if err := db.Create(&a).Error; err != nil {
		t.Fatal(err)
	}
	policy := &protocol.DestinationPolicy{Collect: protocol.CollectLevel(mode), CollectRevision: 1, Rules: rules}
	if err := protocol.ValidateDestinationPolicy(policy); err != nil {
		t.Fatal(err)
	}
	body, err := json.Marshal(policy)
	if err != nil {
		t.Fatal(err)
	}
	digest := protocol.PolicyDigest(policy)
	runtime := destAgentPolicyRow{AgentID: agentID, MintedBody: destBytes(body), MintedSHA256: digest, MintedKind: "desired", MintedAt: &now,
		ReportedSHA256: digest, ReportedState: "applied", AppliedBody: destBytes([]byte("private-previous-body"))}
	if err := db.Create(&runtime).Error; err != nil {
		t.Fatal(err)
	}
	return p.ID, agentID
}

func TestLegalCollectionCurrentProofRetentionAndPrivateProjection(t *testing.T) {
	db, repos := legalTestRepos(t)
	s := ports.UISettings{LegalEnabled: true, NodePollSeconds: 60, DestHitRetentionDays: 43, DestTrialRetentionDays: 12, DestUsageRetentionDays: 9}
	if err := repos.Settings.Save(t.Context(), s); err != nil {
		t.Fatal(err)
	}
	if _, err := repos.Legal.Publish(t.Context(), legalDraft("privacy", "en-US", "[[data-collection]]", false)); err != nil {
		t.Fatal(err)
	}
	rules := []protocol.DestinationRule{{ID: "p1", Action: protocol.RuleBlock, Domains: []string{"domain:private-destination.example"}},
		{ID: "g9", Action: protocol.RuleObserve, CatchAll: true, Subjects: []protocol.SubjectKey{"usr_123"}}}
	hitsID, _ := legalCollectionNode(t, db, "hits", rules)
	usageID, _ := legalCollectionNode(t, db, "hits_and_usage", nil)
	for _, scenario := range []string{"off", "sing-box", "no-capability", "offline", "pending", "old-revision"} {
		id, agent := legalCollectionNode(t, db, "hits", rules)
		var err error
		switch scenario {
		case "off":
			err = db.Model(&xuiPanelRow{}).Where("id = ?", id).Update("audit_collect", "off").Error
		case "sing-box":
			err = db.Model(&nodeAgentRow{}).Where("agent_id = ?", agent).Update("observed_core_engine", "sing-box").Error
		case "no-capability":
			err = db.Model(&nodeAgentRow{}).Where("agent_id = ?", agent).Update("observed_capabilities", jsonStrings{protocol.CapabilityDestinationPolicy}).Error
		case "offline":
			err = db.Model(&nodeAgentRow{}).Where("agent_id = ?", agent).Update("last_seen", time.Now().Add(-4*time.Hour)).Error
		case "pending":
			err = db.Model(&destAgentPolicyRow{}).Where("agent_id = ?", agent).Update("reported_sha256", "previous").Error
		case "old-revision":
			err = db.Model(&xuiPanelRow{}).Where("id = ?", id).Update("audit_collect_revision", 2).Error
		}
		if err != nil {
			t.Fatal(err)
		}
	}
	bodyReads := 0
	if err := db.Callback().Query().Before("gorm:query").Register("legal_collection_projection", func(tx *gorm.DB) {
		if _, ok := tx.Statement.ConnPool.(gorm.TxCommitter); !ok {
			tx.AddError(errors.New("disclosure read escaped transaction"))
			return
		}
		if _, locked := tx.Statement.Clauses["FOR"]; locked {
			tx.AddError(errors.New("disclosure mixed current and snapshot reads"))
			return
		}
		switch tx.Statement.Table {
		case "settings", "legal_documents":
		case "xui_panels":
			if !slices.Equal(tx.Statement.Selects, []string{"id", "kind", "audit_collect", "audit_collect_revision"}) {
				tx.AddError(errors.New("private panel projection"))
			}
		case "node_agents":
			if !slices.Equal(tx.Statement.Selects, []string{"agent_id", "panel_id", "observed_capabilities", "observed_core_engine", "last_seen"}) {
				tx.AddError(errors.New("private agent projection"))
			}
		case "dest_agent_policy":
			if slices.Equal(tx.Statement.Selects, []string{"minted_body"}) {
				bodyReads++
			} else if !slices.Equal(tx.Statement.Selects, []string{"agent_id", "minted_sha256", "minted_at", "reported_sha256", "reported_state", "fallback_exhausted"}) {
				tx.AddError(errors.New("private runtime projection"))
			}
		default:
			tx.AddError(fmt.Errorf("unneeded disclosure table %s", tx.Statement.Table))
		}
	}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Callback().Query().Remove("legal_collection_projection") })
	want := []domain.LegalAccessCollection{{Kind: "hits", Nodes: 1, RetentionDays: 43}, {Kind: "trial", Nodes: 1, RetentionDays: 12}, {Kind: "usage", Nodes: 1, RetentionDays: 9}}
	check := func() {
		t.Helper()
		collection, err := repos.Legal.DataCollection(t.Context())
		if err != nil || !reflect.DeepEqual(collection.Access, want) {
			t.Fatalf("collection %+v: %v", collection, err)
		}
		doc, err := repos.Legal.Public(t.Context(), "privacy", "en-US")
		if err != nil || !reflect.DeepEqual(doc.DataCollection.Access, want) {
			t.Fatalf("public collection %+v: %v", doc, err)
		}
		raw, _ := json.Marshal(doc)
		for _, forbidden := range []string{"private-", "usr_123", "agt_", "panel_id", "agent_id", "policy_id", "domain:"} {
			if strings.Contains(string(raw), forbidden) {
				t.Fatalf("public disclosure leaked %s", forbidden)
			}
		}
	}
	check()
	firstReads := bodyReads
	check()
	if bodyReads != firstReads {
		t.Fatal("warm disclosure reread immutable candidate bodies")
	}
	if err := db.Model(&xuiPanelRow{}).Where("id = ?", hitsID).Update("audit_collect", "off").Error; err != nil {
		t.Fatal(err)
	}
	want = want[2:]
	check()
	if err := db.Model(&xuiPanelRow{}).Where("id = ?", usageID).Update("audit_collect", "off").Error; err != nil {
		t.Fatal(err)
	}
	want = []domain.LegalAccessCollection{}
	check()
}

func TestLegalCollectionFailureReturnsNoPartialPolicyOrPrivateError(t *testing.T) {
	db, repos := legalTestRepos(t)
	if err := repos.Settings.Save(t.Context(), ports.UISettings{LegalEnabled: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := repos.Legal.Publish(t.Context(), legalDraft("privacy", "en-US", "[[data-collection]]", false)); err != nil {
		t.Fatal(err)
	}
	recorder := &auditTraceRecorder{Interface: logger.Discard}
	r := repos.Legal.(*legalRepo)
	r.db = db.Session(&gorm.Session{Logger: recorder})
	if err := db.Callback().Query().Before("gorm:query").Register("legal_collection_failure", func(tx *gorm.DB) {
		if tx.Statement.Table == "xui_panels" {
			tx.AddError(errors.New("private-driver-destination.example"))
		}
	}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Callback().Query().Remove("legal_collection_failure") })
	if got, err := r.DataCollection(t.Context()); err != domain.ErrUnavailable || !reflect.DeepEqual(got, domain.LegalDataCollection{}) {
		t.Fatal("partial disclosure or private error escaped")
	}
	if got, err := r.Public(t.Context(), "privacy", "en-US"); err != domain.ErrUnavailable || !reflect.DeepEqual(got, domain.LegalPublicDocument{}) {
		t.Fatal("partial public document or private error escaped")
	}
	if len(recorder.queries) != 0 {
		t.Fatal("disclosure reads traced private SQL")
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := r.DataCollection(ctx); !errors.Is(err, context.Canceled) {
		t.Fatal("collection cancellation was lost")
	}
	if _, err := r.Public(ctx, "privacy", "en-US"); !errors.Is(err, context.Canceled) {
		t.Fatal("public cancellation was lost")
	}
}

func TestLegalCollectionSettingsAndNodesShareRepeatableRead(t *testing.T) {
	for _, public := range []bool{false, true} {
		t.Run(fmt.Sprintf("public=%t", public), func(t *testing.T) {
			db, repos := legalTestRepos(t)
			if db.Dialector.Name() == "sqlite" {
				t.Skip("concurrent writer requires the server-dialect MVCC lanes")
			}
			if err := repos.Settings.Save(t.Context(), ports.UISettings{LegalEnabled: true, DestHitRetentionDays: 43, NodePollSeconds: 60}); err != nil {
				t.Fatal(err)
			}
			if _, err := repos.Legal.Publish(t.Context(), legalDraft("privacy", "en-US", "[[data-collection]]", false)); err != nil {
				t.Fatal(err)
			}
			id, _ := legalCollectionNode(t, db, "hits", []protocol.DestinationRule{{ID: "p1", Action: protocol.RuleBlock, Ports: "443"}})
			fired := false
			if err := db.Callback().Query().After("gorm:query").Register("legal_collection_concurrent_writer", func(tx *gorm.DB) {
				if _, ok := tx.Statement.Dest.(*[]settingRow); !ok || fired {
					return
				}
				fired = true
				err := db.Transaction(func(writer *gorm.DB) error {
					if err := writer.Model(&settingRow{}).Where("type = ? AND name = ?", "dest", "hit_retention_days").Update("value", "99").Error; err != nil {
						return err
					}
					return writer.Model(&xuiPanelRow{}).Where("id = ?", id).Update("audit_collect", "off").Error
				})
				if err != nil {
					tx.AddError(err)
				}
			}); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = db.Callback().Query().Remove("legal_collection_concurrent_writer") })
			read := func() (domain.LegalDataCollection, error) {
				if public {
					doc, err := repos.Legal.Public(t.Context(), "privacy", "en-US")
					return doc.DataCollection, err
				}
				return repos.Legal.DataCollection(t.Context())
			}
			got, err := read()
			want := []domain.LegalAccessCollection{{Kind: "hits", Nodes: 1, RetentionDays: 43}}
			if err != nil || !fired || !reflect.DeepEqual(got.Access, want) {
				t.Fatalf("mixed disclosure snapshot %+v: %v", got.Access, err)
			}
			got, err = read()
			if err != nil || got.Access == nil || len(got.Access) != 0 {
				t.Fatalf("next read did not see committed off: %+v %v", got.Access, err)
			}
		})
	}
}
