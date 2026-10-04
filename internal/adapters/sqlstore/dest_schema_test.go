package sqlstore

import (
	"bytes"
	"reflect"
	"strings"
	"testing"
	"time"
)

// Run through the actual boot migration, including the cross-dialect jobs.
func TestDestinationSchemaCreatesAllDurableTablesAndLargeListBodies(t *testing.T) {
	db, err := openIsolatedTestDB(t)
	if err != nil {
		t.Fatal(err)
	}
	if err := EnsureSchema(db); err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{"dest_lists", "dest_policies", "dest_exemptions", "dest_group_modes", "dest_policy_state", "dest_policy_snapshots", "dest_agent_policy", "dest_hits", "dest_usage_hourly", "dest_audit_batches", "dest_audit_loss_hourly", "dest_audit_ingest_budget"} {
		if !db.Migrator().HasTable(table) {
			t.Errorf("missing durable destination table %s", table)
		}
	}
	if t.Failed() {
		return
	}
	body := bytes.Repeat([]byte("domain:example.com\n"), 120000) // over 2 MiB
	if err := db.Table("dest_lists").Create(map[string]any{"name": "large", "kind": "custom", "entries": body, "source_text": body}).Error; err != nil {
		t.Fatal(err)
	}
	var got struct{ Entries, SourceText []byte }
	if err := db.Table("dest_lists").Select("entries", "source_text").Where("name = ?", "large").Take(&got).Error; err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got.Entries, body) || !bytes.Equal(got.SourceText, body) {
		t.Fatal("large list was truncated or altered")
	}
	if db.Dialector.Name() == "mysql" {
		columns, err := db.Migrator().ColumnTypes("dest_lists")
		if err != nil {
			t.Fatal(err)
		}
		for _, c := range columns {
			if (c.Name() == "entries" || c.Name() == "source_text") && !strings.EqualFold(c.DatabaseTypeName(), "longblob") {
				t.Errorf("%s must be longblob, got %s", c.Name(), c.DatabaseTypeName())
			}
		}
	}
}

func TestDestinationSchemaPreservesCandidateBytesNullableStateAndTrialZeroIDs(t *testing.T) {
	db, err := openIsolatedTestDB(t)
	if err != nil {
		t.Fatal(err)
	}
	if err := EnsureSchema(db); err != nil {
		t.Fatal(err)
	}
	state := destPolicyStateRow{ID: 1}
	if err := db.Create(&state).Error; err != nil {
		t.Fatal(err)
	}
	var loadedState destPolicyStateRow
	if err := db.First(&loadedState, 1).Error; err != nil {
		t.Fatal(err)
	}
	if loadedState.PublishError != nil || loadedState.PublishedAt != nil || loadedState.LastWriteAt != nil {
		t.Fatalf("fresh state fabricated dates or errors: %+v", loadedState)
	}
	when := time.Now().UTC().Truncate(time.Second)
	want := destAgentPolicyRow{AgentID: "agt_bytes", MintedKind: "paused", MintedBody: destBytes("null"), MintedGeneration: 7, MintedContext: strings.Repeat("b", 64), FallbackExhausted: true, AppliedBody: destBytes(`{"rules":[{"id":"p2"}]}`), AppliedSHA256: strings.Repeat("a", 64), AppliedAt: &when, AppliedRuleCount: 1, AppliedGroups: jsonInt64s{4, 9}, MintedAt: &when, UpdatedAt: when}
	if err := db.Create(&want).Error; err != nil {
		t.Fatal(err)
	}
	var got destAgentPolicyRow
	if err := db.Where("agent_id = ?", want.AgentID).Take(&got).Error; err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(want.MintedBody, got.MintedBody) || !bytes.Equal(want.AppliedBody, got.AppliedBody) || !reflect.DeepEqual(want.AppliedGroups, got.AppliedGroups) || !got.FallbackExhausted || got.MintedKind != "paused" || got.MintedGeneration != 7 || got.MintedContext != want.MintedContext || got.AppliedSHA256 != want.AppliedSHA256 || got.AppliedAt == nil || !got.AppliedAt.Equal(when) {
		t.Fatalf("candidate/LKG storage lost state: %+v", got)
	}
	trial := destHitRow{HourMS: 1791000000000, PanelID: 2, UserID: 0, Source: "g4", Action: "trial", Dest: "example.com", Port: 0, Count: 8, FirstAt: when, LastAt: when}
	if err := db.Create(&trial).Error; err != nil {
		t.Fatal(err)
	}
	var hit destHitRow
	if err := db.Where("source = ?", "g4").Take(&hit).Error; err != nil {
		t.Fatal(err)
	}
	if hit.UserID != 0 || hit.Port != 0 || hit.Count != 8 {
		t.Fatalf("trial identifiers were invented: %+v", hit)
	}
	// All new DDL must remain idempotent with stored candidates present.
	if err := EnsureSchema(db); err != nil {
		t.Fatal(err)
	}
	var after destAgentPolicyRow
	if err := db.Where("agent_id = ?", want.AgentID).Take(&after).Error; err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(after.MintedBody, want.MintedBody) || !bytes.Equal(after.AppliedBody, want.AppliedBody) {
		t.Fatal("second boot altered stored policy bodies")
	}
}

// A supported V4 database predates these columns. Adding them must initialize
// the safe recording default without changing existing operator metadata.
func TestDestinationPanelColumnsUpgradeExistingV4Rows(t *testing.T) {
	db, err := openIsolatedTestDB(t)
	if err != nil {
		t.Fatal(err)
	}
	// Migrate the real supported baseline, then reproduce only the historical
	// omission of these additive columns. Other V4 semantics remain intact.
	if err := EnsureSchema(db); err != nil {
		t.Fatal(err)
	}
	if err := db.Migrator().DropColumn(&xuiPanelRow{}, "AuditCollect"); err != nil {
		t.Fatal(err)
	}
	if err := db.Migrator().DropColumn(&xuiPanelRow{}, "AuditCollectRevision"); err != nil {
		t.Fatal(err)
	}
	if err := db.Table("xui_panels").Create(map[string]any{"name": "existing", "kind": "psp", "url": "psp://agt_existing", "remark": "retain"}).Error; err != nil {
		t.Fatal(err)
	}
	if err := EnsureSchema(db); err != nil {
		t.Fatal(err)
	}
	var panel xuiPanelRow
	if err := db.Where("name = ?", "existing").Take(&panel).Error; err != nil {
		t.Fatal(err)
	}
	if panel.AuditCollect != "hits" || panel.AuditCollectRevision != 1 || panel.Remark != "retain" {
		t.Fatalf("upgrade changed collection defaults or metadata: %+v", panel)
	}
}
