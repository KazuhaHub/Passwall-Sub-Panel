package sqlstore

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	nodeprotocol "github.com/KazuhaHub/passwall-node/protocol"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

func nodeTaskPolicySettings(policy domain.NodeTaskLifecyclePolicy) ports.UISettings {
	return ports.UISettings{
		NodeTaskOfflineReconcileDays: policy.OfflineReconcileDays,
		NodeTaskBackupRestoreDays:    policy.BackupRestoreDays,
		NodeTaskResultRetentionDays:  policy.ResultRetentionDays,
	}
}

func nodeTaskPolicyTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := openTestDB(t)
	if err != nil {
		t.Fatal(err)
	}
	if err := EnsureSchema(db); err != nil {
		t.Fatal(err)
	}
	return db
}

func TestKVSettingsNodeTaskPolicyDefaultsRoundTripAndLegacySave(t *testing.T) {
	db := nodeTaskPolicyTestDB(t)
	repo := newKVSettingsRepo(db)
	ctx := context.Background()
	defaults := domain.DefaultNodeTaskLifecyclePolicy()
	custom := domain.NodeTaskLifecyclePolicy{OfflineReconcileDays: 7, BackupRestoreDays: 45, ResultRetentionDays: 120}
	fresh, err := repo.Load(ctx, ports.UISettings{})
	if err != nil || fresh.NodeTaskLifecyclePolicy() != defaults {
		t.Fatalf("fresh = (%+v, %v), want %+v", fresh.NodeTaskLifecyclePolicy(), err, defaults)
	}
	for _, reader := range []ports.SettingsRepo{repo, NewCachingSettingsRepo(repo)} {
		got, err := reader.Load(ctx, nodeTaskPolicySettings(custom))
		if err != nil || got.NodeTaskLifecyclePolicy() != defaults {
			t.Fatalf("caller fallback selected a different global product policy: %+v, %v", got.NodeTaskLifecyclePolicy(), err)
		}
	}
	// A legacy writer's all-zero group means omitted, not explicit zero/reset.
	if err := repo.Save(ctx, ports.UISettings{SiteTitle: "legacy"}); err != nil {
		t.Fatal(err)
	}
	var policyRows int64
	if err := db.Model(&settingRow{}).Where("type = ? AND name IN ?", "runtime", []string{
		"node_task_offline_reconcile_days", "node_task_backup_restore_days", "node_task_result_retention_days",
	}).Count(&policyRows).Error; err != nil || policyRows != 3 {
		t.Fatalf("legacy first save did not seed three policy keys: count=%d, err=%v", policyRows, err)
	}
	// A settings caller's fallback cannot replace the selected global policy.
	withFallback, err := repo.Load(ctx, nodeTaskPolicySettings(custom))
	if err != nil || withFallback.NodeTaskLifecyclePolicy() != defaults {
		t.Fatalf("caller fallback = (%+v, %v)", withFallback.NodeTaskLifecyclePolicy(), err)
	}
	if err := repo.Save(ctx, nodeTaskPolicySettings(custom)); err != nil {
		t.Fatal(err)
	}
	var rows []settingRow
	if err := db.Where("type = ? AND name IN ?", "runtime", []string{
		"node_task_offline_reconcile_days", "node_task_backup_restore_days", "node_task_result_retention_days",
	}).Order("name").Find(&rows).Error; err != nil {
		t.Fatal(err)
	}
	if len(rows) != 3 || rows[0].Name != "node_task_backup_restore_days" || rows[0].Value != "45" ||
		rows[1].Value != "7" || rows[2].Value != "120" {
		t.Fatalf("canonical KV rows = %+v", rows)
	}
	// Reconstruct the repository to prove DB persistence, not cached fallback.
	loaded, err := newKVSettingsRepo(db).Load(ctx, nodeTaskPolicySettings(defaults))
	if err != nil || loaded.NodeTaskLifecyclePolicy() != custom {
		t.Fatalf("persisted custom = (%+v, %v)", loaded.NodeTaskLifecyclePolicy(), err)
	}
	if err := repo.Save(ctx, ports.UISettings{SiteTitle: "other legacy change"}); err != nil {
		t.Fatal(err)
	}
	afterLegacy, err := repo.Load(ctx, ports.UISettings{})
	if err != nil || afterLegacy.NodeTaskLifecyclePolicy() != custom || afterLegacy.SiteTitle != "other legacy change" {
		t.Fatalf("legacy writer reset custom policy: %+v, %v", afterLegacy.NodeTaskLifecyclePolicy(), err)
	}
	// Both smaller and larger valid policies remain editable; no floor at 90.
	for _, policy := range []domain.NodeTaskLifecyclePolicy{
		{OfflineReconcileDays: 1, BackupRestoreDays: 1, ResultRetentionDays: 1},
		{OfflineReconcileDays: 3650, BackupRestoreDays: 3650, ResultRetentionDays: 3650},
	} {
		if err := repo.Save(ctx, nodeTaskPolicySettings(policy)); err != nil {
			t.Fatal(err)
		}
		got, err := repo.Load(ctx, ports.UISettings{})
		if err != nil || got.NodeTaskLifecyclePolicy() != policy {
			t.Fatalf("custom boundary = (%+v, %v), want %+v", got.NodeTaskLifecyclePolicy(), err, policy)
		}
	}
}

func TestKVSettingsNodeTaskPolicyInvalidSaveIsAtomic(t *testing.T) {
	db := nodeTaskPolicyTestDB(t)
	repo := newKVSettingsRepo(db)
	ctx := context.Background()
	initial := nodeTaskPolicySettings(domain.DefaultNodeTaskLifecyclePolicy())
	initial.SiteTitle = "unchanged"
	if err := repo.Save(ctx, initial); err != nil {
		t.Fatal(err)
	}
	for _, policy := range []domain.NodeTaskLifecyclePolicy{
		{OfflineReconcileDays: 0, BackupRestoreDays: 30, ResultRetentionDays: 90},
		{OfflineReconcileDays: -1, BackupRestoreDays: 30, ResultRetentionDays: 90},
		{OfflineReconcileDays: 30, BackupRestoreDays: 3651, ResultRetentionDays: 3650},
		{OfflineReconcileDays: 30, BackupRestoreDays: 30, ResultRetentionDays: 29},
	} {
		incoming := nodeTaskPolicySettings(policy)
		incoming.SiteTitle = "must roll back"
		if err := repo.Save(ctx, incoming); !errors.Is(err, domain.ErrValidation) {
			t.Fatalf("invalid save %+v: %v", policy, err)
		}
		got, err := repo.Load(ctx, ports.UISettings{})
		if err != nil || got.SiteTitle != initial.SiteTitle || got.NodeTaskLifecyclePolicy() != initial.NodeTaskLifecyclePolicy() {
			t.Fatalf("invalid save changed settings: policy=%+v, title=%q, err=%v", got.NodeTaskLifecyclePolicy(), got.SiteTitle, err)
		}
	}
}

func TestKVSettingsNodeTaskPolicyStoredInvalidIsNotDefaulted(t *testing.T) {
	db := nodeTaskPolicyTestDB(t)
	repo := newKVSettingsRepo(db)
	ctx := context.Background()
	for _, value := range []string{"0", "-1", "3651", "not-an-integer"} {
		if err := repo.Save(ctx, nodeTaskPolicySettings(domain.DefaultNodeTaskLifecyclePolicy())); err != nil {
			t.Fatal(err)
		}
		if err := db.Model(&settingRow{}).Where("type = ? AND name = ?", "runtime", "node_task_result_retention_days").UpdateColumn("value", value).Error; err != nil {
			t.Fatal(err)
		}
		if _, err := repo.Load(ctx, ports.UISettings{}); err == nil {
			t.Fatalf("malformed stored value %q became a default", value)
		}
	}
}

func TestKVSettingsNodeTaskPolicyCacheAndGlobalScope(t *testing.T) {
	db := nodeTaskPolicyTestDB(t)
	repo := NewCachingSettingsRepo(newKVSettingsRepo(db))
	ctx := context.Background()
	if _, err := repo.Load(ctx, ports.UISettings{}); err != nil {
		t.Fatal(err)
	}
	custom := domain.NodeTaskLifecyclePolicy{OfflineReconcileDays: 60, BackupRestoreDays: 14, ResultRetentionDays: 120}
	if err := repo.Save(ctx, nodeTaskPolicySettings(custom)); err != nil {
		t.Fatal(err)
	}
	got, err := repo.Load(ctx, ports.UISettings{})
	if err != nil || got.NodeTaskLifecyclePolicy() != custom {
		t.Fatalf("save not immediately visible through cache: %+v, %v", got.NodeTaskLifecyclePolicy(), err)
	}
	if err := repo.Save(ctx, ports.UISettings{SiteTitle: "legacy cache change"}); err != nil {
		t.Fatal(err)
	}
	got, err = repo.Load(ctx, ports.UISettings{})
	if err != nil || got.NodeTaskLifecyclePolicy() != custom {
		t.Fatalf("legacy cached save reset custom policy: %+v, %v", got.NodeTaskLifecyclePolicy(), err)
	}
	invalid := nodeTaskPolicySettings(custom)
	invalid.NodeTaskResultRetentionDays = 1
	if err := repo.Save(ctx, invalid); !errors.Is(err, domain.ErrValidation) {
		t.Fatalf("invalid cached save: %v", err)
	}
	got, err = repo.Load(ctx, ports.UISettings{})
	if err != nil || got.NodeTaskLifecyclePolicy() != custom || got.SiteTitle != "legacy cache change" {
		t.Fatalf("failed save polluted cache: %+v, %v", got.NodeTaskLifecyclePolicy(), err)
	}
	var overrides []ports.ScopeOverride
	for _, name := range []string{"node_task_offline_reconcile_days", "node_task_backup_restore_days", "node_task_result_retention_days"} {
		key := "runtime." + name
		if !KnownSettingKeys()[key] || ports.OverridableScopeKeys[key] {
			t.Fatalf("policy key %s is unknown or group-overridable", key)
		}
		overrides = append(overrides, ports.ScopeOverride{Type: "runtime", Name: name, Value: "1"})
	}
	if scoped := applyScopeOverrides(got, overrides); scoped.NodeTaskLifecyclePolicy() != custom {
		t.Fatalf("stray scoped rows changed global policy: %+v", scoped.NodeTaskLifecyclePolicy())
	}
}

func TestKVSettingsNodeTaskPolicyDefaultsOnlyMissingKeys(t *testing.T) {
	db := nodeTaskPolicyTestDB(t)
	repo := newKVSettingsRepo(db)
	ctx := t.Context()
	custom := domain.NodeTaskLifecyclePolicy{OfflineReconcileDays: 7, BackupRestoreDays: 14, ResultRetentionDays: 120}
	for _, name := range []string{"node_task_offline_reconcile_days", "node_task_backup_restore_days", "node_task_result_retention_days"} {
		if err := repo.Save(ctx, nodeTaskPolicySettings(custom)); err != nil {
			t.Fatal(err)
		}
		if err := db.Where("type = ? AND name = ?", "runtime", name).Delete(&settingRow{}).Error; err != nil {
			t.Fatal(err)
		}
		want := custom
		defaults := domain.DefaultNodeTaskLifecyclePolicy()
		switch name {
		case "node_task_offline_reconcile_days":
			want.OfflineReconcileDays = defaults.OfflineReconcileDays
		case "node_task_backup_restore_days":
			want.BackupRestoreDays = defaults.BackupRestoreDays
		case "node_task_result_retention_days":
			want.ResultRetentionDays = defaults.ResultRetentionDays
		}
		cached := NewCachingSettingsRepo(repo)
		for _, reader := range []ports.SettingsRepo{repo, cached, cached} {
			got, err := reader.Load(ctx, nodeTaskPolicySettings(custom))
			if err != nil || got.NodeTaskLifecyclePolicy() != want {
				t.Fatalf("missing %s: %+v, %v, want %+v", name, got.NodeTaskLifecyclePolicy(), err, want)
			}
		}
		// First legacy save seeds the missing default and preserves the other
		// two values. It cannot rewrite a non-missing custom field to default.
		if err := repo.Save(ctx, ports.UISettings{SiteTitle: "legacy partial-key save"}); err != nil {
			t.Fatal(err)
		}
		got, err := repo.Load(ctx, ports.UISettings{})
		if err != nil || got.NodeTaskLifecyclePolicy() != want {
			t.Fatalf("legacy seed of %s reset another key: %+v, %v", name, got.NodeTaskLifecyclePolicy(), err)
		}
	}
}

func TestKVSettingsNodeTaskPolicyLegacySeedPreservesInterveningExplicitInsert(t *testing.T) {
	db := nodeTaskPolicyTestDB(t)
	repo := newKVSettingsRepo(db)
	custom := domain.NodeTaskLifecyclePolicy{OfflineReconcileDays: 7, BackupRestoreDays: 14, ResultRetentionDays: 120}
	inserted := false
	const callback = "test:task_policy_intervening_insert"
	if err := db.Callback().Create().Before("gorm:create").Register(callback, func(tx *gorm.DB) {
		rows, ok := tx.Statement.Dest.(*[]settingRow)
		if inserted || !ok || len(*rows) != 3 {
			return
		}
		for _, row := range *rows {
			if !isNodeTaskLifecycleSetting(row.Type, row.Name) {
				return
			}
		}
		inserted = true
		// Deterministically exercise rows becoming present AFTER Save's
		// existence read, BEFORE its insert-only defaults hit the unique key.
		// This is not a claim of a separate physical transaction race test.
		values := []settingRow{
			{Type: "runtime", Name: "node_task_offline_reconcile_days", Value: "7"},
			{Type: "runtime", Name: "node_task_backup_restore_days", Value: "14"},
			{Type: "runtime", Name: "node_task_result_retention_days", Value: "120"},
		}
		tx.AddError(tx.Session(&gorm.Session{NewDB: true}).Clauses(clause.OnConflict{DoNothing: true}).Create(&values).Error)
	}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Callback().Create().Remove(callback) })
	if err := repo.Save(t.Context(), ports.UISettings{SiteTitle: "legacy first save"}); err != nil {
		t.Fatal(err)
	}
	got, err := repo.Load(t.Context(), ports.UISettings{})
	if !inserted || err != nil || got.NodeTaskLifecyclePolicy() != custom {
		t.Fatalf("legacy defaults overwrote intervening explicit policy: inserted=%v, policy=%+v, err=%v", inserted, got.NodeTaskLifecyclePolicy(), err)
	}
}

func TestKVSettingsNodeTaskPolicyChangesNeverCleanExistingEvidence(t *testing.T) {
	db := nodeTaskPolicyTestDB(t)
	repos := NewRepos(db)
	ctx := context.Background()
	createTaskTestAgent(t, repos, "agt_task_policy", 830)
	old := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	terminal := newTask("task-policy-terminal", "agt_task_policy", "reality_probe.v1", []byte("original args"))
	terminal.CreatedAt = old
	if _, _, err := repos.NodeAgentTask.CreateOrGet(ctx, terminal); err != nil {
		t.Fatal(err)
	}
	if tasks, err := repos.NodeAgentTask.Offer(ctx, terminal.AgentID, []string{terminal.Kind}, 1, int(nodeprotocol.MaxSyncBodyBytes), old); err != nil || len(tasks) != 1 {
		t.Fatalf("offer = %d, %v", len(tasks), err)
	}
	if err := repos.NodeAgentTask.CompleteBatch(ctx, terminal.AgentID, []domain.NodeAgentTaskResult{{
		TaskID: terminal.TaskID, Kind: terminal.Kind, InputSHA256: terminal.InputSHA256, OK: true, Result: []byte("original full result"),
	}}, old); err != nil {
		t.Fatal(err)
	}
	queued := newTask("task-policy-queued", terminal.AgentID, terminal.Kind, []byte("unresolved"))
	queued.CreatedAt = old
	if _, _, err := repos.NodeAgentTask.CreateOrGet(ctx, queued); err != nil {
		t.Fatal(err)
	}
	if err := repos.NodeAgentTask.CompleteBatch(ctx, terminal.AgentID, []domain.NodeAgentTaskResult{{
		TaskID: "task-policy-unknown", Kind: terminal.Kind, InputSHA256: nodeprotocol.ComputeTaskInputSHA256(terminal.Kind, nil), OK: true, Result: []byte("original quarantined result"),
	}}, old); err != nil {
		t.Fatal(err)
	}
	var beforeTasks []nodeAgentTaskRow
	var beforeQuarantine []nodeAgentTaskResultQuarantineRow
	if err := db.Order("task_id").Find(&beforeTasks).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Order("task_id").Find(&beforeQuarantine).Error; err != nil {
		t.Fatal(err)
	}
	for _, policy := range []domain.NodeTaskLifecyclePolicy{
		domain.DefaultNodeTaskLifecyclePolicy(),
		{OfflineReconcileDays: 1, BackupRestoreDays: 1, ResultRetentionDays: 1},
		{OfflineReconcileDays: 3650, BackupRestoreDays: 3650, ResultRetentionDays: 3650},
	} {
		if err := repos.Settings.Save(ctx, nodeTaskPolicySettings(policy)); err != nil {
			t.Fatal(err)
		}
		var afterTasks []nodeAgentTaskRow
		var afterQuarantine []nodeAgentTaskResultQuarantineRow
		if err := db.Order("task_id").Find(&afterTasks).Error; err != nil {
			t.Fatal(err)
		}
		if err := db.Order("task_id").Find(&afterQuarantine).Error; err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(afterTasks, beforeTasks) || !reflect.DeepEqual(afterQuarantine, beforeQuarantine) {
			t.Fatalf("policy %+v changed existing task/evidence rows", policy)
		}
	}
}
