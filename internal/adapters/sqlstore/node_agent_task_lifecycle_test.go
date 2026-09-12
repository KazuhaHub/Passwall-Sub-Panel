package sqlstore

import (
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	nodeprotocol "github.com/KazuhaHub/passwall-node/protocol"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

func taskLifecycleSnapshot(t *testing.T, issued, deadline int64, policy domain.NodeTaskLifecyclePolicy) *domain.NodeTaskLifecycleSnapshot {
	t.Helper()
	snapshot, err := domain.NewNodeTaskLifecycleSnapshot(issued, deadline, policy)
	if err != nil {
		t.Fatal(err)
	}
	return snapshot
}

func TestNodeAgentTaskLifecyclePersistsIndependentCopiesAcrossSettingsChanges(t *testing.T) {
	repos, db := newTaskTestReposWithQuota(t, defaultNodeAgentTaskQuota())
	createTaskTestAgent(t, repos, "agt_snapshot", 850)
	task := newTask("task-snapshot", "agt_snapshot", "reality_probe.v1", []byte(`{"target":"example.test:443"}`))
	task.Lifecycle = taskLifecycleSnapshot(t, 1_700_000_000_000, 1_700_000_300_000, domain.DefaultNodeTaskLifecyclePolicy())
	want := *task.Lifecycle
	stored, created, err := repos.NodeAgentTask.CreateOrGet(t.Context(), task)
	if err != nil || !created || stored.Lifecycle == nil || *stored.Lifecycle != want {
		t.Fatalf("create = (%+v,%v,%v), want frozen snapshot", stored, created, err)
	}
	task.Lifecycle.Policy.ResultRetentionDays = 365
	task.Lifecycle.IssuedAtMS++
	stored.Lifecycle.Policy.OfflineReconcileDays = 1
	stored.Lifecycle.NotAfterMS++
	settings := newKVSettingsRepo(db)
	for _, policy := range []domain.NodeTaskLifecyclePolicy{
		{OfflineReconcileDays: 1, BackupRestoreDays: 1, ResultRetentionDays: 1},
		{OfflineReconcileDays: 3650, BackupRestoreDays: 3650, ResultRetentionDays: 3650},
	} {
		if err := settings.Save(t.Context(), nodeTaskPolicySettings(policy)); err != nil {
			t.Fatal(err)
		}
		// Fresh repos must load the frozen original policy, not today's settings.
		loaded, err := NewRepos(db).NodeAgentTask.GetByTaskID(t.Context(), task.TaskID)
		if err != nil || loaded.Lifecycle == nil || *loaded.Lifecycle != want {
			t.Fatalf("reconstructed snapshot = (%+v,%v), want %+v", loaded, err, want)
		}
		loaded.Lifecycle.FullResultRetainUntilMS++
	}
	var payload string
	if err := db.Table("node_agent_tasks").Select("lifecycle").Where("task_id = ?", task.TaskID).Scan(&payload).Error; err != nil {
		t.Fatal(err)
	}
	var persisted domain.NodeTaskLifecycleSnapshot
	if err := json.Unmarshal([]byte(payload), &persisted); err != nil || persisted != want {
		t.Fatalf("persisted JSON = %q, error %v", payload, err)
	}
}

func TestNodeAgentTaskLifecycleExactIDBindsEveryImmutableField(t *testing.T) {
	repos := newTaskTestRepos(t)
	createTaskTestAgent(t, repos, "agt_snapshot_exact", 851)
	first := newTask("task-snapshot-exact", "agt_snapshot_exact", "reality_probe.v1", nil)
	first.Lifecycle = taskLifecycleSnapshot(t, 1000, 2000, domain.DefaultNodeTaskLifecyclePolicy())
	if _, created, err := repos.NodeAgentTask.CreateOrGet(t.Context(), first); err != nil || !created {
		t.Fatalf("first create = (%v,%v)", created, err)
	}
	replay := newTask(first.TaskID, first.AgentID, first.Kind, first.Args)
	replay.Lifecycle = first.Lifecycle.Clone()
	if got, created, err := repos.NodeAgentTask.CreateOrGet(t.Context(), replay); err != nil || created || *got.Lifecycle != *first.Lifecycle {
		t.Fatalf("exact replay = (%+v,%v,%v)", got, created, err)
	}
	for _, test := range []struct {
		name   string
		change func(*domain.NodeAgentTask)
	}{
		{"issued", func(task *domain.NodeAgentTask) { task.Lifecycle.IssuedAtMS++ }},
		{"deadline", func(task *domain.NodeAgentTask) {
			task.Lifecycle.NotAfterMS++
			task.Lifecycle.FullResultRetainUntilMS++
		}},
		{"offline", func(task *domain.NodeAgentTask) { task.Lifecycle.Policy.OfflineReconcileDays = 1 }},
		{"backup", func(task *domain.NodeAgentTask) { task.Lifecycle.Policy.BackupRestoreDays = 1 }},
		{"retention", func(task *domain.NodeAgentTask) {
			policy := task.Lifecycle.Policy
			policy.ResultRetentionDays = 120
			task.Lifecycle = taskLifecycleSnapshot(t, 1000, 2000, policy)
		}},
		{"legacy_absence", func(task *domain.NodeAgentTask) { task.Lifecycle = nil }},
	} {
		t.Run(test.name, func(t *testing.T) {
			candidate := newTask(first.TaskID, first.AgentID, first.Kind, first.Args)
			candidate.Lifecycle = first.Lifecycle.Clone()
			test.change(candidate)
			if _, _, err := repos.NodeAgentTask.CreateOrGet(t.Context(), candidate); !errors.Is(err, domain.ErrConflict) {
				t.Fatalf("changed immutable snapshot = %v, want ErrConflict", err)
			}
		})
	}
	got, err := repos.NodeAgentTask.GetByTaskID(t.Context(), first.TaskID)
	if err != nil || got.Lifecycle == nil || *got.Lifecycle != *first.Lifecycle {
		t.Fatalf("conflict changed stored snapshot = (%+v,%v)", got, err)
	}
}

func TestNodeAgentTaskLifecycleIdempotencyAliasNeverExtendsOrUpgradesOriginal(t *testing.T) {
	for _, legacy := range []bool{false, true} {
		t.Run(fmt.Sprintf("legacy_%v", legacy), func(t *testing.T) {
			repos := newTaskTestRepos(t)
			createTaskTestAgent(t, repos, "agt_snapshot_alias", 852)
			first := newTask("task-snapshot-alias", "agt_snapshot_alias", "reality_probe.v1", nil)
			key := strings.Repeat("a1", 32)
			first.IdempotencyKeySHA256 = &key
			if !legacy {
				first.Lifecycle = taskLifecycleSnapshot(t, 1000, 2000, domain.DefaultNodeTaskLifecyclePolicy())
			}
			original, _, err := repos.NodeAgentTask.CreateOrGet(t.Context(), first)
			if err != nil {
				t.Fatal(err)
			}
			for index, snapshot := range []*domain.NodeTaskLifecycleSnapshot{
				taskLifecycleSnapshot(t, 3000, 4000, domain.NodeTaskLifecyclePolicy{OfflineReconcileDays: 7, BackupRestoreDays: 45, ResultRetentionDays: 180}),
				nil,
			} {
				alias := newTask(fmt.Sprintf("task-snapshot-alias-%d", index), first.AgentID, first.Kind, first.Args)
				alias.IdempotencyKeySHA256 = &key
				alias.Lifecycle = snapshot
				got, created, err := repos.NodeAgentTask.CreateOrGet(t.Context(), alias)
				if err != nil || created || got.TaskID != first.TaskID || !reflect.DeepEqual(got.Lifecycle, original.Lifecycle) {
					t.Fatalf("idempotency alias = (%+v,%v,%v), want original %+v", got, created, err, original.Lifecycle)
				}
			}
			// Aliasing does not excuse an invalid incoming authorization snapshot.
			alias := newTask("task-snapshot-alias-invalid", first.AgentID, first.Kind, first.Args)
			alias.IdempotencyKeySHA256 = &key
			alias.Lifecycle = &domain.NodeTaskLifecycleSnapshot{}
			if _, _, err := repos.NodeAgentTask.CreateOrGet(t.Context(), alias); !errors.Is(err, domain.ErrValidation) {
				t.Fatalf("invalid alias snapshot = %v, want ErrValidation", err)
			}
			if legacy {
				exact := newTask(first.TaskID, first.AgentID, first.Kind, first.Args)
				exact.IdempotencyKeySHA256 = &key
				exact.Lifecycle = taskLifecycleSnapshot(t, 1000, 2000, domain.DefaultNodeTaskLifecyclePolicy())
				if _, _, err := repos.NodeAgentTask.CreateOrGet(t.Context(), exact); !errors.Is(err, domain.ErrConflict) {
					t.Fatalf("exact ID cannot upgrade legacy absence = %v", err)
				}
			}
		})
	}
}

func TestNodeAgentTaskLifecycleConcurrentAliasesKeepOneOriginalSnapshot(t *testing.T) {
	repos, db := newTaskTestReposWithQuota(t, defaultNodeAgentTaskQuota())
	createTaskTestAgent(t, repos, "agt_snapshot_concurrent", 853)
	const workers = 16
	key := strings.Repeat("b2", 32)
	results := make(chan *domain.NodeAgentTask, workers)
	errorsCh := make(chan error, workers)
	var wg sync.WaitGroup
	for i := range workers {
		task := newTask(fmt.Sprintf("task-snapshot-concurrent-%02d", i), "agt_snapshot_concurrent", "reality_probe.v1", nil)
		task.IdempotencyKeySHA256 = &key
		task.Lifecycle = taskLifecycleSnapshot(t, int64(1000+i), int64(2000+i), domain.DefaultNodeTaskLifecyclePolicy())
		wg.Go(func() {
			got, _, err := repos.NodeAgentTask.CreateOrGet(t.Context(), task)
			if err != nil {
				errorsCh <- err
				return
			}
			results <- got
		})
	}
	wg.Wait()
	close(results)
	close(errorsCh)
	for err := range errorsCh {
		t.Errorf("concurrent alias failed: %v", err)
	}
	var original *domain.NodeAgentTask
	for got := range results {
		if original == nil {
			original = got
		} else if got.TaskID != original.TaskID || !reflect.DeepEqual(got.Lifecycle, original.Lifecycle) {
			t.Errorf("alias replaced original snapshot: %+v vs %+v", got, original)
		}
	}
	var count int64
	if err := db.Model(&nodeAgentTaskRow{}).Where("agent_id = ?", "agt_snapshot_concurrent").Count(&count).Error; err != nil || count != 1 {
		t.Fatalf("concurrent aliases created %d rows, error %v", count, err)
	}
}

func TestNodeAgentTaskLifecycleProtectedRequestsStayQueuedAndConsumeQuota(t *testing.T) {
	repos, _ := newTaskTestReposWithQuota(t, nodeAgentTaskQuota{MaxActiveTasks: 1, MaxActiveArgBytes: 1024})
	createTaskTestAgent(t, repos, "agt_snapshot_gated", 854)
	task := newTask("task-snapshot-gated", "agt_snapshot_gated", "reality_probe.v1", []byte("input"))
	task.Lifecycle = taskLifecycleSnapshot(t, 1000, 2000, domain.DefaultNodeTaskLifecyclePolicy())
	if _, _, err := repos.NodeAgentTask.CreateOrGet(t.Context(), task); err != nil {
		t.Fatal(err)
	}
	for _, offeredAt := range []time.Time{time.UnixMilli(1500).UTC(), time.UnixMilli(task.Lifecycle.FullResultRetainUntilMS + 1).UTC()} {
		offered, err := repos.NodeAgentTask.Offer(t.Context(), task.AgentID, ports.NodeAgentTaskOfferSupport{EligibleKinds: []string{task.Kind}}, 64, int(nodeprotocol.MaxSyncBodyBytes), offeredAt)
		if err != nil || len(offered) != 0 {
			t.Fatalf("protected request reached legacy transport = (%+v,%v)", offered, err)
		}
	}
	stored, err := repos.NodeAgentTask.GetByTaskID(t.Context(), task.TaskID)
	if err != nil || stored.Status != domain.NodeAgentTaskQueued || stored.OfferCount != 0 || stored.DispatchClosedAt == nil || stored.DispatchClosedReason != "task_authorization_expired" || *stored.Lifecycle != *task.Lifecycle {
		t.Fatalf("expiry closure altered queued outcome or snapshot = (%+v,%v)", stored, err)
	}
	extra := newTask("task-snapshot-quota-extra", task.AgentID, task.Kind, nil)
	if _, _, err := repos.NodeAgentTask.CreateOrGet(t.Context(), extra); !errors.Is(err, domain.ErrResourceExhausted) {
		t.Fatalf("protected queued request must retain active quota = %v", err)
	}
}

func TestNodeAgentTaskLifecycleReceiptAndLateTerminalReplayDoNotRewriteSnapshot(t *testing.T) {
	for _, offered := range []bool{false, true} {
		t.Run(fmt.Sprintf("already_offered_%v", offered), func(t *testing.T) {
			repos, db := newTaskTestReposWithQuota(t, defaultNodeAgentTaskQuota())
			createTaskTestAgent(t, repos, "agt_snapshot_receipt", 855)
			task := newTask("task-snapshot-receipt", "agt_snapshot_receipt", "reality_probe.v1", nil)
			task.Lifecycle = taskLifecycleSnapshot(t, 1000, 2000, domain.DefaultNodeTaskLifecyclePolicy())
			if _, _, err := repos.NodeAgentTask.CreateOrGet(t.Context(), task); err != nil {
				t.Fatal(err)
			}
			if offered {
				// Simulate an existing offered row, not a new protected-wire producer.
				if err := db.Model(&nodeAgentTaskRow{}).Where("task_id = ?", task.TaskID).UpdateColumn("status", string(domain.NodeAgentTaskOffered)).Error; err != nil {
					t.Fatal(err)
				}
			}
			result := domain.NodeAgentTaskResult{TaskID: task.TaskID, Kind: task.Kind, InputSHA256: task.InputSHA256, NotAfterMS: task.Lifecycle.NotAfterMS, OK: true, Result: []byte(`{"reachable":true}`)}
			late := time.UnixMilli(task.Lifecycle.FullResultRetainUntilMS + 86_400_000).UTC()
			if err := repos.NodeAgentTask.CompleteBatch(t.Context(), task.AgentID, []domain.NodeAgentTaskResult{result}, late); err != nil {
				t.Fatal(err)
			}
			if err := repos.NodeAgentTask.CompleteBatch(t.Context(), task.AgentID, []domain.NodeAgentTaskResult{result}, late.Add(24*time.Hour)); err != nil {
				t.Fatal(err)
			}
			got, err := repos.NodeAgentTask.GetByTaskID(t.Context(), task.TaskID)
			if err != nil || got.Lifecycle == nil || *got.Lifecycle != *task.Lifecycle {
				t.Fatalf("receipt/replay changed original floor = (%+v,%v)", got, err)
			}
			if offered {
				if got.Status != domain.NodeAgentTaskSucceeded || got.CompletedAt == nil || !got.CompletedAt.Equal(late) {
					t.Fatalf("offered receipt did not retain original completion = %+v", got)
				}
			} else if got.Status != domain.NodeAgentTaskQueued || got.DispatchClosedAt == nil {
				t.Fatalf("never-offered receipt must remain unresolved = %+v", got)
			}
		})
	}
}

func TestNodeAgentTaskLifecycleLegacyNullAndInvalidCreateFailClosed(t *testing.T) {
	repos, db := newTaskTestReposWithQuota(t, defaultNodeAgentTaskQuota())
	createTaskTestAgent(t, repos, "agt_snapshot_legacy", 856)
	legacy := newTask("task-snapshot-legacy", "agt_snapshot_legacy", "reality_probe.v1", nil)
	if _, _, err := repos.NodeAgentTask.CreateOrGet(t.Context(), legacy); err != nil {
		t.Fatal(err)
	}
	var nullRows int64
	if err := db.Model(&nodeAgentTaskRow{}).Where("task_id = ? AND lifecycle IS NULL", legacy.TaskID).Count(&nullRows).Error; err != nil || nullRows != 1 {
		t.Fatalf("legacy snapshot must be SQL NULL, rows %d error %v", nullRows, err)
	}
	loaded, err := NewRepos(db).NodeAgentTask.GetByTaskID(t.Context(), legacy.TaskID)
	if err != nil || loaded.Lifecycle != nil {
		t.Fatalf("SQL NULL became guessed lifecycle = (%+v,%v)", loaded, err)
	}
	columns, err := db.Migrator().ColumnTypes(&nodeAgentTaskRow{})
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, column := range columns {
		if column.Name() == "lifecycle" {
			found = true
			if !strings.EqualFold(column.DatabaseTypeName(), "text") {
				t.Fatalf("lifecycle storage type = %s, want TEXT", column.DatabaseTypeName())
			}
			if nullable, ok := column.Nullable(); ok && !nullable {
				t.Fatal("legacy lifecycle column must remain nullable")
			}
			if value, ok := column.DefaultValue(); ok && value != "" {
				t.Fatalf("TEXT lifecycle must not have a default: %q", value)
			}
		}
	}
	if !found {
		t.Fatal("nullable lifecycle column was not migrated")
	}
	for _, snapshot := range []*domain.NodeTaskLifecycleSnapshot{{}, taskLifecycleSnapshot(t, 1000, 2000, domain.DefaultNodeTaskLifecyclePolicy())} {
		candidate := newTask("task-snapshot-invalid", legacy.AgentID, legacy.Kind, nil)
		candidate.Lifecycle = snapshot
		if snapshot.IssuedAtMS != 0 {
			snapshot.FullResultRetainUntilMS++
		}
		if _, _, err := repos.NodeAgentTask.CreateOrGet(t.Context(), candidate); !errors.Is(err, domain.ErrValidation) {
			t.Fatalf("invalid new snapshot = %v, want ErrValidation", err)
		}
	}
	if _, err := repos.NodeAgentTask.GetByTaskID(t.Context(), "task-snapshot-invalid"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("invalid snapshot was persisted: %v", err)
	}
}

func TestNodeAgentTaskLifecycleCorruptStoredJSONCannotBecomeLegacy(t *testing.T) {
	repos, db := newTaskTestReposWithQuota(t, defaultNodeAgentTaskQuota())
	createTaskTestAgent(t, repos, "agt_snapshot_corrupt", 857)
	task := newTask("task-snapshot-corrupt", "agt_snapshot_corrupt", "reality_probe.v1", nil)
	task.Lifecycle = taskLifecycleSnapshot(t, 1000, 2000, domain.DefaultNodeTaskLifecyclePolicy())
	if _, _, err := repos.NodeAgentTask.CreateOrGet(t.Context(), task); err != nil {
		t.Fatal(err)
	}
	valid, err := json.Marshal(task.Lifecycle)
	if err != nil {
		t.Fatal(err)
	}
	badFloor := task.Lifecycle.Clone()
	badFloor.FullResultRetainUntilMS++
	badFloorJSON, err := json.Marshal(badFloor)
	if err != nil {
		t.Fatal(err)
	}
	for index, payload := range []string{"", "null", "{}", "{", string(badFloorJSON), string(valid) + " {}", strings.TrimSuffix(string(valid), "}") + `,"private_marker":"not-for-errors"}`} {
		t.Run(fmt.Sprintf("bad_json_%d", index), func(t *testing.T) {
			if err := db.Exec("UPDATE node_agent_tasks SET lifecycle = ? WHERE task_id = ?", payload, task.TaskID).Error; err != nil {
				t.Fatal(err)
			}
			if got, err := repos.NodeAgentTask.GetByTaskID(t.Context(), task.TaskID); err == nil || got != nil || strings.Contains(err.Error(), "not-for-errors") {
				t.Fatalf("corrupt snapshot read = (%+v,%v), must fail without stored content", got, err)
			}
			if got, created, err := repos.NodeAgentTask.CreateOrGet(t.Context(), task); err == nil || got != nil || created {
				t.Fatalf("corrupt snapshot replay = (%+v,%v,%v), must fail", got, created, err)
			}
			result := domain.NodeAgentTaskResult{TaskID: task.TaskID, Kind: task.Kind, InputSHA256: task.InputSHA256, OK: true, Result: []byte("ok")}
			if err := repos.NodeAgentTask.CompleteBatch(t.Context(), task.AgentID, []domain.NodeAgentTaskResult{result}, time.Now().UTC()); err == nil {
				t.Fatal("corrupt lifecycle accepted a receipt")
			}
			// Unsupported protected rows are still decoded before expiry/filtering;
			// corruption must never silently become legacy history.
			if offered, err := repos.NodeAgentTask.Offer(t.Context(), task.AgentID, ports.NodeAgentTaskOfferSupport{EligibleKinds: []string{task.Kind}}, 64, int(nodeprotocol.MaxSyncBodyBytes), time.Now().UTC()); err == nil || offered != nil {
				t.Fatalf("corrupt protected row did not fail closed = (%+v,%v)", offered, err)
			}
		})
	}
	var unchanged int64
	if err := db.Table("node_agent_tasks").Where("task_id = ? AND status = ? AND dispatch_closed_at IS NULL AND offer_count = 0", task.TaskID, string(domain.NodeAgentTaskQueued)).Count(&unchanged).Error; err != nil || unchanged != 1 {
		t.Fatalf("invalid snapshot changed task state, rows %d error %v", unchanged, err)
	}
}

func TestNodeAgentTaskLifecycleStorageCodecValidatesBothDirections(t *testing.T) {
	snapshot := taskLifecycleSnapshot(t, 1000, 2000, domain.DefaultNodeTaskLifecyclePolicy())
	value, err := nodeAgentTaskLifecycleFromDomain(snapshot).Value()
	if err != nil {
		t.Fatal(err)
	}
	for _, raw := range []any{value, []byte(value.(string))} {
		var codec nodeAgentTaskLifecycleJSON
		if err := codec.Scan(raw); err != nil || *codec.toDomain() != *snapshot {
			t.Fatalf("codec scan = (%+v,%v), want original", codec, err)
		}
	}
	var invalid nodeAgentTaskLifecycleJSON
	if _, err := invalid.Value(); err == nil {
		t.Fatal("zero snapshot storage write must fail closed")
	}
	for _, raw := range []any{nil, 0, "null", "{}"} {
		if err := invalid.Scan(raw); err == nil {
			t.Fatalf("invalid stored snapshot %T accepted", raw)
		}
	}
}
