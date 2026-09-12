package sqlstore

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	nodeprotocol "github.com/KazuhaHub/passwall-node/protocol"
	sqlitedriver "github.com/glebarez/sqlite"
	mysqldriver "gorm.io/driver/mysql"
	"gorm.io/gorm"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

func TestNodeAgentTaskInputDigestGoldenVector(t *testing.T) {
	const want = "7578595b14afff1b40892f0b8a74b1b6c271fa08fd26cd90f2f46250a9bdb920"
	if got := nodeprotocol.ComputeTaskInputSHA256("reality_probe.v1", []byte(`{"target":"example.com:443"}`)); got != want {
		t.Fatalf("task input digest = %q, want %q", got, want)
	}
}

func TestNodeAgentTaskDefaultActiveQuotaIsStableAndProtocolAligned(t *testing.T) {
	if defaultMaxActiveNodeAgentTasks != 256 {
		t.Fatalf("default active task count = %d, want 256", defaultMaxActiveNodeAgentTasks)
	}
	if defaultMaxActiveNodeAgentArgBytes != 16<<20 {
		t.Fatalf("default active task bytes = %d, want 16 MiB", defaultMaxActiveNodeAgentArgBytes)
	}
	if defaultMaxActiveNodeAgentTasks != 4*int64(nodeprotocol.MaxTasksPerResponse) {
		t.Fatalf("default active task count no longer equals four full offer windows")
	}
	if defaultMaxActiveNodeAgentArgBytes != 4*int64(nodeprotocol.MaxTaskArgsBytesPerResponse) {
		t.Fatalf("default active task bytes no longer equals four full offer windows")
	}
}

func TestNodeAgentTaskInsertConflictSQLIsValidPerDialect(t *testing.T) {
	row := &nodeAgentTaskRow{
		TaskID: "task-sql", AgentID: "agt_sql", Kind: "reality_probe.v1",
		Args: []byte{}, InputSHA256: nodeprotocol.ComputeTaskInputSHA256("reality_probe.v1", nil),
		Status: string(domain.NodeAgentTaskQueued),
	}
	mysqlConn, _ := sql.Open("mysql", "u:p@tcp(127.0.0.1:3306)/d")
	t.Cleanup(func() { _ = mysqlConn.Close() })
	mysqlDB, err := gorm.Open(mysqldriver.New(mysqldriver.Config{
		Conn: mysqlConn, SkipInitializeWithVersion: true,
	}), &gorm.Config{DryRun: true, DisableAutomaticPing: true})
	if err != nil {
		t.Fatal(err)
	}
	mysqlSQL := mysqlDB.ToSQL(func(tx *gorm.DB) *gorm.DB {
		return tx.Clauses(nodeAgentTaskInsertConflictClause("mysql")).Create(row)
	})
	marker := "ON DUPLICATE KEY UPDATE"
	index := strings.Index(mysqlSQL, marker)
	if index < 0 || strings.TrimSpace(mysqlSQL[index+len(marker):]) == "" ||
		!strings.Contains(mysqlSQL[index+len(marker):], "task_id") {
		t.Fatalf("MySQL task no-op conflict SQL is invalid: %q", mysqlSQL)
	}

	sqliteDB, err := gorm.Open(sqlitedriver.Open(":memory:"), &gorm.Config{DryRun: true})
	if err != nil {
		t.Fatal(err)
	}
	sqliteSQL := sqliteDB.ToSQL(func(tx *gorm.DB) *gorm.DB {
		return tx.Clauses(nodeAgentTaskInsertConflictClause("sqlite")).Create(row)
	})
	if !strings.Contains(sqliteSQL, "DO NOTHING") {
		t.Fatalf("SQLite task conflict SQL must use native DO NOTHING: %q", sqliteSQL)
	}
}

func TestNodeAgentTaskSchemaRejectsInvalidStatusAndNullArgs(t *testing.T) {
	for _, test := range []struct {
		name   string
		column string
		value  any
	}{
		{name: "invalid_status", column: "status", value: "running"},
		{name: "null_args", column: "args", value: gorm.Expr("NULL")},
	} {
		t.Run(test.name, func(t *testing.T) {
			db, err := openTestDB(t)
			if err != nil {
				t.Fatal(err)
			}
			if err := EnsureSchema(db); err != nil {
				t.Fatal(err)
			}
			repos := NewRepos(db)
			createTaskTestAgent(t, repos, "agt_task_schema", 711)
			task := newTask("task-schema", "agt_task_schema", "reality_probe.v1", nil)
			if _, _, err := repos.NodeAgentTask.CreateOrGet(t.Context(), task); err != nil {
				t.Fatal(err)
			}
			if err := db.Model(&nodeAgentTaskRow{}).Where("task_id = ?", task.TaskID).
				UpdateColumn(test.column, test.value).Error; err == nil {
				t.Fatalf("schema accepted %s=%v", test.column, test.value)
			}
		})
	}
}

func TestNodeAgentTaskCreateIsIdempotentAndContentBound(t *testing.T) {
	repos := newTaskTestRepos(t)
	ctx := context.Background()
	createTaskTestAgent(t, repos, "agt_task_create", 700)

	key := strings.Repeat("ab", 32)
	first := newTask("task-create-1", "agt_task_create", "reality_probe.v1", []byte(`{"host":"example.test"}`))
	first.IdempotencyKeySHA256 = &key
	stored, created, err := repos.NodeAgentTask.CreateOrGet(ctx, first)
	if err != nil || !created || stored.Status != domain.NodeAgentTaskQueued {
		t.Fatalf("first create = (%+v, %v, %v)", stored, created, err)
	}

	replayed, created, err := repos.NodeAgentTask.CreateOrGet(ctx, first)
	if err != nil || created || replayed.TaskID != first.TaskID {
		t.Fatalf("same request replay = (%+v, %v, %v)", replayed, created, err)
	}

	// An API retry may mint a fresh candidate task ID before it discovers the
	// idempotency-key row. Equal input must resolve to the original operation.
	retry := newTask("task-create-retry", first.AgentID, first.Kind, first.Args)
	retry.IdempotencyKeySHA256 = &key
	replayed, created, err = repos.NodeAgentTask.CreateOrGet(ctx, retry)
	if err != nil || created || replayed.TaskID != first.TaskID {
		t.Fatalf("idempotency-key replay = (%+v, %v, %v)", replayed, created, err)
	}

	conflict := newTask(first.TaskID, first.AgentID, first.Kind, []byte(`{"host":"other.test"}`))
	conflict.IdempotencyKeySHA256 = &key
	if _, _, err := repos.NodeAgentTask.CreateOrGet(ctx, conflict); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("task ID content conflict = %v, want ErrConflict", err)
	}
	conflict = newTask("task-create-other", first.AgentID, first.Kind, []byte(`{"host":"other.test"}`))
	conflict.IdempotencyKeySHA256 = &key
	if _, _, err := repos.NodeAgentTask.CreateOrGet(ctx, conflict); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("idempotency content conflict = %v, want ErrConflict", err)
	}

	badDigest := newTask("task-create-bad", first.AgentID, first.Kind, []byte("input"))
	badDigest.InputSHA256 = strings.Repeat("0", 64)
	if _, _, err := repos.NodeAgentTask.CreateOrGet(ctx, badDigest); !errors.Is(err, domain.ErrValidation) {
		t.Fatalf("mismatched digest = %v, want ErrValidation", err)
	}
}

func TestNodeAgentTaskActiveCountQuotaPreservesReplayAndConflictSemantics(t *testing.T) {
	quota := nodeAgentTaskQuota{MaxActiveTasks: 2, MaxActiveArgBytes: 1024}
	repos, _ := newTaskTestReposWithQuota(t, quota)
	ctx := context.Background()
	createTaskTestAgent(t, repos, "agt_task_count_quota", 712)

	key := strings.Repeat("ef", 32)
	first := newTask("task-count-quota-1", "agt_task_count_quota", "reality_probe.v1", []byte("one"))
	first.IdempotencyKeySHA256 = &key
	second := newTask("task-count-quota-2", first.AgentID, first.Kind, []byte("two"))
	for _, task := range []*domain.NodeAgentTask{first, second} {
		if _, created, err := repos.NodeAgentTask.CreateOrGet(ctx, task); err != nil || !created {
			t.Fatalf("create %q = (created %v, %v)", task.TaskID, created, err)
		}
	}
	if offered, err := repos.NodeAgentTask.Offer(ctx, first.AgentID, []string{first.Kind}, 1,
		int(nodeprotocol.MaxSyncBodyBytes), time.Now().UTC()); err != nil || len(offered) != 1 {
		t.Fatalf("offer first task = (%+v, %v)", offered, err)
	}

	// Exact request and idempotency-key replays are lookups, not new backlog,
	// and must continue working while the agent is exactly at its quota.
	if stored, created, err := repos.NodeAgentTask.CreateOrGet(ctx, first); err != nil || created || stored.TaskID != first.TaskID {
		t.Fatalf("exact replay at quota = (%+v, %v, %v)", stored, created, err)
	}
	retry := newTask("task-count-quota-retry", first.AgentID, first.Kind, first.Args)
	retry.IdempotencyKeySHA256 = &key
	if stored, created, err := repos.NodeAgentTask.CreateOrGet(ctx, retry); err != nil || created || stored.TaskID != first.TaskID {
		t.Fatalf("idempotency replay at quota = (%+v, %v, %v)", stored, created, err)
	}

	conflict := newTask(first.TaskID, first.AgentID, first.Kind, []byte("different"))
	conflict.IdempotencyKeySHA256 = &key
	if _, _, err := repos.NodeAgentTask.CreateOrGet(ctx, conflict); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("identity conflict at quota = %v, want ErrConflict", err)
	}
	third := newTask("task-count-quota-3", first.AgentID, first.Kind, nil)
	if _, _, err := repos.NodeAgentTask.CreateOrGet(ctx, third); !errors.Is(err, domain.ErrResourceExhausted) {
		t.Fatalf("create beyond count quota = %v, want ErrResourceExhausted", err)
	}
	if _, err := repos.NodeAgentTask.GetByTaskID(ctx, third.TaskID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("rejected count-quota task was persisted: %v", err)
	}

	result := domain.NodeAgentTaskResult{
		TaskID: first.TaskID, Kind: first.Kind, InputSHA256: first.InputSHA256, OK: true,
	}
	if err := repos.NodeAgentTask.CompleteBatch(ctx, first.AgentID, []domain.NodeAgentTaskResult{result}, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	if _, created, err := repos.NodeAgentTask.CreateOrGet(ctx, third); err != nil || !created {
		t.Fatalf("create after terminal frees count = (created %v, %v)", created, err)
	}
}

func TestNodeAgentTaskActiveByteQuotaCountsQueuedAndOfferedRawArgs(t *testing.T) {
	quota := nodeAgentTaskQuota{MaxActiveTasks: 10, MaxActiveArgBytes: 10}
	repos, _ := newTaskTestReposWithQuota(t, quota)
	ctx := context.Background()
	createTaskTestAgent(t, repos, "agt_task_byte_quota", 713)

	// Include NUL/non-UTF-8 bytes: the quota measures the BLOB/bytea payload,
	// not character count, JSON/base64 expansion, or an estimated row size.
	first := newTask("task-byte-quota-1", "agt_task_byte_quota", "reality_probe.v1", []byte{0, 0xff, 0xc3, 0xa9, 1, 2})
	second := newTask("task-byte-quota-2", first.AgentID, first.Kind, []byte("7890"))
	for _, task := range []*domain.NodeAgentTask{first, second} {
		if _, created, err := repos.NodeAgentTask.CreateOrGet(ctx, task); err != nil || !created {
			t.Fatalf("create %q = (created %v, %v)", task.TaskID, created, err)
		}
	}
	if offered, err := repos.NodeAgentTask.Offer(ctx, first.AgentID, []string{first.Kind}, 1,
		int(nodeprotocol.MaxSyncBodyBytes), time.Now().UTC()); err != nil || len(offered) != 1 {
		t.Fatalf("offer first task = (%+v, %v)", offered, err)
	}
	beyond := newTask("task-byte-quota-3", first.AgentID, first.Kind, []byte("x"))
	if _, _, err := repos.NodeAgentTask.CreateOrGet(ctx, beyond); !errors.Is(err, domain.ErrResourceExhausted) {
		t.Fatalf("create beyond byte quota = %v, want ErrResourceExhausted", err)
	}
	if _, err := repos.NodeAgentTask.GetByTaskID(ctx, beyond.TaskID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("rejected byte-quota task was persisted: %v", err)
	}

	if err := repos.NodeAgentTask.CompleteBatch(ctx, first.AgentID, []domain.NodeAgentTaskResult{{
		TaskID: first.TaskID, Kind: first.Kind, InputSHA256: first.InputSHA256, OK: true,
	}}, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	if _, created, err := repos.NodeAgentTask.CreateOrGet(ctx, beyond); err != nil || !created {
		t.Fatalf("create after terminal frees bytes = (created %v, %v)", created, err)
	}
}

func TestNodeAgentTaskActiveQuotaIsIsolatedPerAgent(t *testing.T) {
	quota := nodeAgentTaskQuota{MaxActiveTasks: 1, MaxActiveArgBytes: 4}
	repos, _ := newTaskTestReposWithQuota(t, quota)
	ctx := context.Background()
	createTaskTestAgent(t, repos, "agt_task_quota_a", 714)
	createTaskTestAgent(t, repos, "agt_task_quota_b", 715)

	firstA := newTask("task-quota-agent-a-1", "agt_task_quota_a", "reality_probe.v1", []byte("1234"))
	secondA := newTask("task-quota-agent-a-2", firstA.AgentID, firstA.Kind, nil)
	firstB := newTask("task-quota-agent-b-1", "agt_task_quota_b", firstA.Kind, []byte("1234"))
	if _, _, err := repos.NodeAgentTask.CreateOrGet(ctx, firstA); err != nil {
		t.Fatal(err)
	}
	if _, _, err := repos.NodeAgentTask.CreateOrGet(ctx, secondA); !errors.Is(err, domain.ErrResourceExhausted) {
		t.Fatalf("second agent A task = %v, want ErrResourceExhausted", err)
	}
	if _, created, err := repos.NodeAgentTask.CreateOrGet(ctx, firstB); err != nil || !created {
		t.Fatalf("first agent B task = (created %v, %v)", created, err)
	}
}

func TestNodeAgentTaskConcurrentCreatesStopExactlyAtActiveQuota(t *testing.T) {
	const (
		limit   = 8
		workers = 32
	)
	for _, test := range []struct {
		name  string
		quota nodeAgentTaskQuota
	}{
		{name: "count", quota: nodeAgentTaskQuota{MaxActiveTasks: limit, MaxActiveArgBytes: workers}},
		{name: "bytes", quota: nodeAgentTaskQuota{MaxActiveTasks: workers, MaxActiveArgBytes: limit}},
	} {
		t.Run(test.name, func(t *testing.T) {
			repos, db := newTaskTestReposWithQuota(t, test.quota)
			ctx := context.Background()
			const agentID = "agt_task_concurrent_quota"
			createTaskTestAgent(t, repos, agentID, 716)

			start := make(chan struct{})
			errs := make(chan error, workers)
			var wg sync.WaitGroup
			for i := 0; i < workers; i++ {
				wg.Add(1)
				go func(index int) {
					defer wg.Done()
					<-start
					task := newTask(fmt.Sprintf("task-concurrent-quota-%02d", index), agentID, "reality_probe.v1", []byte{'x'})
					_, _, err := repos.NodeAgentTask.CreateOrGet(ctx, task)
					errs <- err
				}(i)
			}
			close(start)
			wg.Wait()
			close(errs)

			created := 0
			exhausted := 0
			for err := range errs {
				switch {
				case err == nil:
					created++
				case errors.Is(err, domain.ErrResourceExhausted):
					exhausted++
				default:
					t.Fatalf("concurrent create returned unexpected error: %v", err)
				}
			}
			if created != limit || exhausted != workers-limit {
				t.Fatalf("concurrent outcomes = created %d exhausted %d, want %d/%d", created, exhausted, limit, workers-limit)
			}
			var active int64
			if err := db.Model(&nodeAgentTaskRow{}).
				Where("agent_id = ? AND status IN ?", agentID,
					[]string{string(domain.NodeAgentTaskQueued), string(domain.NodeAgentTaskOffered)}).
				Count(&active).Error; err != nil {
				t.Fatal(err)
			}
			if active != limit {
				t.Fatalf("persisted active tasks = %d, want %d", active, limit)
			}
		})
	}
}

func TestNodeAgentTaskEveryTerminalStateReleasesActiveQuota(t *testing.T) {
	quota := nodeAgentTaskQuota{MaxActiveTasks: 1, MaxActiveArgBytes: 1}
	repos, _ := newTaskTestReposWithQuota(t, quota)
	ctx := context.Background()
	const agentID = "agt_task_terminal_quota"
	createTaskTestAgent(t, repos, agentID, 717)
	for _, terminal := range []domain.NodeAgentTaskStatus{
		domain.NodeAgentTaskSucceeded, domain.NodeAgentTaskFailed, domain.NodeAgentTaskIndeterminate,
	} {
		task := newTask("task-terminal-quota-"+string(terminal), agentID, "reality_probe.v1", []byte{'x'})
		if _, created, err := repos.NodeAgentTask.CreateOrGet(ctx, task); err != nil || !created {
			t.Fatalf("create after earlier terminal state = (created %v, %v)", created, err)
		}
		if _, err := repos.NodeAgentTask.Offer(ctx, agentID, []string{task.Kind}, 1,
			int(nodeprotocol.MaxSyncBodyBytes), time.Now().UTC()); err != nil {
			t.Fatal(err)
		}
		result := domain.NodeAgentTaskResult{
			TaskID: task.TaskID, Kind: task.Kind, InputSHA256: task.InputSHA256,
			OK: terminal == domain.NodeAgentTaskSucceeded, Indeterminate: terminal == domain.NodeAgentTaskIndeterminate,
		}
		if !result.OK {
			result.ErrorCode, result.Error = "expected_terminal", "expected terminal outcome"
		}
		if err := repos.NodeAgentTask.CompleteBatch(ctx, agentID, []domain.NodeAgentTaskResult{result}, time.Now().UTC()); err != nil {
			t.Fatal(err)
		}
	}
	if _, created, err := repos.NodeAgentTask.CreateOrGet(ctx,
		newTask("task-terminal-quota-after-all", agentID, "reality_probe.v1", []byte{'x'})); err != nil || !created {
		t.Fatalf("create after indeterminate terminal = (created %v, %v)", created, err)
	}
}

func TestNodeAgentTaskQuotaInvalidLimitsAndOverflowFailClosed(t *testing.T) {
	cases := []struct {
		name      string
		quota     nodeAgentTaskQuota
		usage     nodeAgentTaskActiveUsage
		requested int64
	}{
		{name: "zero_count", quota: nodeAgentTaskQuota{MaxActiveArgBytes: 1}},
		{name: "negative_count", quota: nodeAgentTaskQuota{MaxActiveTasks: -1, MaxActiveArgBytes: 1}},
		{name: "zero_bytes", quota: nodeAgentTaskQuota{MaxActiveTasks: 1}},
		{name: "negative_bytes", quota: nodeAgentTaskQuota{MaxActiveTasks: 1, MaxActiveArgBytes: -1}},
		{
			name: "addition_would_overflow", quota: nodeAgentTaskQuota{MaxActiveTasks: 2, MaxActiveArgBytes: 1<<63 - 1},
			usage: nodeAgentTaskActiveUsage{ArgBytes: 1<<63 - 2}, requested: 2,
		},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			err := checkNodeAgentTaskActiveQuota(test.quota, test.usage, test.requested)
			if !errors.Is(err, domain.ErrResourceExhausted) {
				t.Fatalf("quota check = %v, want ErrResourceExhausted", err)
			}
		})
	}
}

func TestNodeAgentTaskOwnerIdentityIsByteExactAcrossDatabaseCollations(t *testing.T) {
	repos := newTaskTestRepos(t)
	ctx := context.Background()
	createTaskTestAgent(t, repos, "agt_Task_Case", 710)

	exact := newTask("task-case-exact", "agt_Task_Case", "reality_probe.v1", nil)
	if _, created, err := repos.NodeAgentTask.CreateOrGet(ctx, exact); err != nil || !created {
		t.Fatalf("exact owner identity = (created %v, %v)", created, err)
	}
	if _, err := repos.NodeAgentTask.GetByTaskID(ctx, strings.ToUpper(exact.TaskID)); err == nil {
		t.Fatal("case-folded task lookup unexpectedly returned a different byte identity")
	}
	variant := newTask("task-case-variant", "agt_task_case", "reality_probe.v1", nil)
	if _, _, err := repos.NodeAgentTask.CreateOrGet(ctx, variant); err == nil {
		t.Fatal("case-folded agent identity unexpectedly created a task")
	}

	// MySQL's default collation finds this row and must reject the byte-different
	// lineage explicitly. SQLite/Postgres may return not-found first; both are
	// fail-closed and the all-dialect CI exercises the former branch.
	failed := newTask("task-case-failed", exact.AgentID, exact.Kind, nil)
	if _, _, err := repos.NodeAgentTask.CreateOrGet(ctx, failed); err != nil {
		t.Fatal(err)
	}
	if _, err := repos.NodeAgentTask.Offer(ctx, failed.AgentID, []string{failed.Kind}, nodeprotocol.MaxTasksPerResponse, int(nodeprotocol.MaxSyncBodyBytes), time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := repos.NodeAgentTask.CompleteBatch(ctx, failed.AgentID, []domain.NodeAgentTaskResult{{
		TaskID: failed.TaskID, Kind: failed.Kind, InputSHA256: failed.InputSHA256,
		ErrorCode: "probe_failed", Error: "expected failure",
	}}, time.Now()); err != nil {
		t.Fatal(err)
	}
	retry := newTask("task-case-retry", failed.AgentID, failed.Kind, nil)
	retry.SupersedesTaskID = strings.ToUpper(failed.TaskID)
	if _, _, err := repos.NodeAgentTask.CreateOrGet(ctx, retry); err == nil {
		t.Fatal("case-folded superseded task identity unexpectedly created a retry")
	}
}

func TestNodeAgentTaskConcurrentIdempotentCreateHasOneWinner(t *testing.T) {
	repos := newTaskTestRepos(t)
	ctx := context.Background()
	createTaskTestAgent(t, repos, "agt_task_concurrent", 701)
	key := strings.Repeat("cd", 32)

	const workers = 8
	type outcome struct {
		taskID  string
		created bool
		err     error
	}
	results := make(chan outcome, workers)
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			task := newTask(fmt.Sprintf("task-concurrent-%d", index), "agt_task_concurrent", "reality_probe.v1", []byte("same"))
			task.IdempotencyKeySHA256 = &key
			stored, created, err := repos.NodeAgentTask.CreateOrGet(ctx, task)
			if err != nil {
				results <- outcome{err: err}
				return
			}
			results <- outcome{taskID: stored.TaskID, created: created}
		}(i)
	}
	wg.Wait()
	close(results)
	winners := 0
	resolvedID := ""
	for result := range results {
		if result.err != nil {
			t.Fatal(result.err)
		}
		if result.created {
			winners++
		}
		if resolvedID == "" {
			resolvedID = result.taskID
		} else if result.taskID != resolvedID {
			t.Fatalf("idempotent creates resolved different IDs: %q and %q", resolvedID, result.taskID)
		}
	}
	if winners != 1 {
		t.Fatalf("created winners = %d, want 1", winners)
	}
}

func TestNodeAgentTaskOfferRetransmitsAndTerminalReplayIsIdempotent(t *testing.T) {
	repos := newTaskTestRepos(t)
	ctx := context.Background()
	createTaskTestAgent(t, repos, "agt_task_lifecycle", 702)
	task := newTask("task-lifecycle-1", "agt_task_lifecycle", "reality_probe.v1", []byte(`{"target":"example.test:443"}`))
	if _, _, err := repos.NodeAgentTask.CreateOrGet(ctx, task); err != nil {
		t.Fatal(err)
	}
	firstAt := time.Date(2026, 9, 11, 10, 0, 0, 0, time.UTC)
	unsupported, err := repos.NodeAgentTask.Offer(ctx, task.AgentID, []string{"agent_upgrade.v1"}, 64, int(nodeprotocol.MaxSyncBodyBytes), firstAt)
	if err != nil || len(unsupported) != 0 {
		t.Fatalf("unsupported offer = (%+v, %v)", unsupported, err)
	}

	first, err := repos.NodeAgentTask.Offer(ctx, task.AgentID, []string{task.Kind}, 64, int(nodeprotocol.MaxSyncBodyBytes), firstAt)
	if err != nil || len(first) != 1 || first[0].Status != domain.NodeAgentTaskOffered || first[0].OfferCount != 1 ||
		first[0].FirstOfferedAt == nil || !first[0].FirstOfferedAt.Equal(firstAt) {
		t.Fatalf("first offer = (%+v, %v)", first, err)
	}
	secondAt := firstAt.Add(time.Minute)
	second, err := repos.NodeAgentTask.Offer(ctx, task.AgentID, []string{task.Kind}, 64, int(nodeprotocol.MaxSyncBodyBytes), secondAt)
	if err != nil || len(second) != 1 || second[0].OfferCount != 2 ||
		second[0].FirstOfferedAt == nil || !second[0].FirstOfferedAt.Equal(firstAt) ||
		second[0].LastOfferedAt == nil || !second[0].LastOfferedAt.Equal(secondAt) {
		t.Fatalf("repeated offer = (%+v, %v)", second, err)
	}

	result := domain.NodeAgentTaskResult{
		TaskID: task.TaskID, Kind: task.Kind, InputSHA256: task.InputSHA256,
		OK: true, Result: []byte(`{"reachable":true}`),
	}
	completedAt := secondAt.Add(time.Minute)
	if err := repos.NodeAgentTask.CompleteBatch(ctx, task.AgentID, []domain.NodeAgentTaskResult{result}, completedAt); err != nil {
		t.Fatal(err)
	}
	completed, err := repos.NodeAgentTask.GetByTaskID(ctx, task.TaskID)
	if err != nil || completed.Status != domain.NodeAgentTaskSucceeded || completed.ResultOK == nil || !*completed.ResultOK ||
		completed.CompletedAt == nil || !completed.CompletedAt.Equal(completedAt) {
		t.Fatalf("completed task = (%+v, %v)", completed, err)
	}
	if err := repos.NodeAgentTask.CompleteBatch(ctx, task.AgentID, []domain.NodeAgentTaskResult{result}, completedAt.Add(time.Hour)); err != nil {
		t.Fatalf("equal terminal replay: %v", err)
	}
	replayed, err := repos.NodeAgentTask.GetByTaskID(ctx, task.TaskID)
	if err != nil || replayed.CompletedAt == nil || !replayed.CompletedAt.Equal(completedAt) {
		t.Fatalf("terminal replay changed completion: (%+v, %v)", replayed, err)
	}
	result.Result = []byte(`{"reachable":false}`)
	if err := repos.NodeAgentTask.CompleteBatch(ctx, task.AgentID, []domain.NodeAgentTaskResult{result}, completedAt.Add(2*time.Hour)); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("conflicting terminal replay = %v, want ErrConflict", err)
	}
	remaining, err := repos.NodeAgentTask.Offer(ctx, task.AgentID, []string{task.Kind}, 64, int(nodeprotocol.MaxSyncBodyBytes), completedAt.Add(3*time.Hour))
	if err != nil || len(remaining) != 0 {
		t.Fatalf("terminal task was re-offered = (%+v, %v)", remaining, err)
	}
}

func TestNodeAgentTaskConcurrentOfferAndEqualCompletionRemainIdempotent(t *testing.T) {
	repos := newTaskTestRepos(t)
	ctx := context.Background()
	createTaskTestAgent(t, repos, "agt_task_race", 708)
	task := newTask("task-race-1", "agt_task_race", "reality_probe.v1", []byte("race"))
	if _, _, err := repos.NodeAgentTask.CreateOrGet(ctx, task); err != nil {
		t.Fatal(err)
	}

	runConcurrently := func(operation func() error) []error {
		t.Helper()
		start := make(chan struct{})
		errs := make(chan error, 2)
		var wg sync.WaitGroup
		for i := 0; i < 2; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				<-start
				errs <- operation()
			}()
		}
		close(start)
		wg.Wait()
		close(errs)
		return []error{<-errs, <-errs}
	}

	offeredAt := time.Date(2026, 9, 11, 12, 0, 0, 0, time.UTC)
	for _, err := range runConcurrently(func() error {
		offered, err := repos.NodeAgentTask.Offer(ctx, task.AgentID, []string{task.Kind}, 1, int(nodeprotocol.MaxSyncBodyBytes), offeredAt)
		if err == nil && len(offered) != 1 {
			return fmt.Errorf("offered %d tasks, want 1", len(offered))
		}
		return err
	}) {
		if err != nil {
			t.Fatalf("concurrent offer: %v", err)
		}
	}
	afterOffer, err := repos.NodeAgentTask.GetByTaskID(ctx, task.TaskID)
	if err != nil || afterOffer.Status != domain.NodeAgentTaskOffered || afterOffer.OfferCount != 2 {
		t.Fatalf("task after concurrent offer = (%+v, %v)", afterOffer, err)
	}

	result := domain.NodeAgentTaskResult{
		TaskID: task.TaskID, Kind: task.Kind, InputSHA256: task.InputSHA256,
		OK: true, Result: []byte("done"),
	}
	completedAt := offeredAt.Add(time.Minute)
	for _, err := range runConcurrently(func() error {
		return repos.NodeAgentTask.CompleteBatch(ctx, task.AgentID, []domain.NodeAgentTaskResult{result}, completedAt)
	}) {
		if err != nil {
			t.Fatalf("concurrent equal completion: %v", err)
		}
	}
	completed, err := repos.NodeAgentTask.GetByTaskID(ctx, task.TaskID)
	if err != nil || completed.Status != domain.NodeAgentTaskSucceeded || completed.CompletedAt == nil || !completed.CompletedAt.Equal(completedAt) {
		t.Fatalf("task after concurrent completion = (%+v, %v)", completed, err)
	}
}

func TestNodeAgentTaskCompleteBatchRejectsUnknownCrossAgentAndNeverOfferedAtomically(t *testing.T) {
	repos := newTaskTestRepos(t)
	ctx := context.Background()
	createTaskTestAgent(t, repos, "agt_task_batch_a", 703)
	createTaskTestAgent(t, repos, "agt_task_batch_b", 704)
	one := newTask("task-batch-1", "agt_task_batch_a", "reality_probe.v1", []byte("one"))
	two := newTask("task-batch-2", "agt_task_batch_a", "reality_probe.v1", []byte("two"))
	queued := newTask("task-batch-queued", "agt_task_batch_a", "reality_probe.v1", []byte("queued"))
	foreign := newTask("task-batch-foreign", "agt_task_batch_b", "reality_probe.v1", []byte("foreign"))
	for _, task := range []*domain.NodeAgentTask{one, two, queued, foreign} {
		if _, _, err := repos.NodeAgentTask.CreateOrGet(ctx, task); err != nil {
			t.Fatal(err)
		}
	}
	if offered, err := repos.NodeAgentTask.Offer(ctx, one.AgentID, []string{one.Kind}, 2, int(nodeprotocol.MaxSyncBodyBytes), time.Now().UTC()); err != nil || len(offered) != 2 {
		t.Fatalf("offer first pair = (%+v, %v)", offered, err)
	}
	if offered, err := repos.NodeAgentTask.Offer(ctx, foreign.AgentID, []string{foreign.Kind}, 1, int(nodeprotocol.MaxSyncBodyBytes), time.Now().UTC()); err != nil || len(offered) != 1 {
		t.Fatalf("offer foreign = (%+v, %v)", offered, err)
	}
	success := func(task *domain.NodeAgentTask) domain.NodeAgentTaskResult {
		return domain.NodeAgentTaskResult{TaskID: task.TaskID, Kind: task.Kind, InputSHA256: task.InputSHA256, OK: true, Result: []byte("ok")}
	}
	assertStillOffered := func(taskID string) {
		t.Helper()
		stored, err := repos.NodeAgentTask.GetByTaskID(ctx, taskID)
		if err != nil || stored.Status != domain.NodeAgentTaskOffered {
			t.Fatalf("task %q changed despite batch rollback: (%+v, %v)", taskID, stored, err)
		}
	}

	unknown := success(two)
	unknown.TaskID = "task-batch-unknown"
	if err := repos.NodeAgentTask.CompleteBatch(ctx, one.AgentID, []domain.NodeAgentTaskResult{success(one), unknown}, time.Now()); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("unknown result = %v, want ErrConflict", err)
	}
	assertStillOffered(one.TaskID)

	if err := repos.NodeAgentTask.CompleteBatch(ctx, one.AgentID, []domain.NodeAgentTaskResult{success(one), success(queued)}, time.Now()); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("never-offered result = %v, want ErrConflict", err)
	}
	assertStillOffered(one.TaskID)

	if err := repos.NodeAgentTask.CompleteBatch(ctx, one.AgentID, []domain.NodeAgentTaskResult{success(one), success(foreign)}, time.Now()); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("cross-agent result = %v, want ErrConflict", err)
	}
	assertStillOffered(one.TaskID)

	badIdentity := success(two)
	badIdentity.InputSHA256 = strings.Repeat("0", 64)
	if err := repos.NodeAgentTask.CompleteBatch(ctx, one.AgentID, []domain.NodeAgentTaskResult{success(one), badIdentity}, time.Now()); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("conflicting identity = %v, want ErrConflict", err)
	}
	assertStillOffered(one.TaskID)

	if err := repos.NodeAgentTask.CompleteBatch(ctx, one.AgentID, []domain.NodeAgentTaskResult{success(one), success(two)}, time.Now()); err != nil {
		t.Fatalf("valid batch: %v", err)
	}
}

func TestNodeAgentTaskOfferHonorsAggregateWireBudgetAndStableOrder(t *testing.T) {
	repos := newTaskTestRepos(t)
	ctx := context.Background()
	createTaskTestAgent(t, repos, "agt_task_budget", 705)
	createdAt := time.Date(2026, 9, 11, 11, 0, 0, 0, time.UTC)
	for i := 0; i < 5; i++ {
		task := newTask(fmt.Sprintf("task-budget-%d", i), "agt_task_budget", "reality_probe.v1",
			[]byte(strings.Repeat(string(rune('a'+i)), nodeprotocol.MaxTaskArgsBytes)))
		task.CreatedAt = createdAt
		if _, _, err := repos.NodeAgentTask.CreateOrGet(ctx, task); err != nil {
			t.Fatal(err)
		}
	}
	offered, err := repos.NodeAgentTask.Offer(ctx, "agt_task_budget", []string{"reality_probe.v1"}, 64, int(nodeprotocol.MaxSyncBodyBytes), createdAt.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	want := nodeprotocol.MaxTaskArgsBytesPerResponse / nodeprotocol.MaxTaskArgsBytes
	if len(offered) != want {
		t.Fatalf("offered count = %d, want aggregate-budget count %d", len(offered), want)
	}
	for i := range offered {
		if offered[i].TaskID != fmt.Sprintf("task-budget-%d", i) {
			t.Fatalf("offer[%d] = %q, stable order lost", i, offered[i].TaskID)
		}
	}
	last, err := repos.NodeAgentTask.GetByTaskID(ctx, "task-budget-4")
	if err != nil || last.Status != domain.NodeAgentTaskQueued {
		t.Fatalf("task beyond aggregate budget = (%+v, %v)", last, err)
	}
}

func TestNodeAgentTaskOfferMarksOnlyRowsThatFitExactJSONBudget(t *testing.T) {
	repos := newTaskTestRepos(t)
	ctx := context.Background()
	createTaskTestAgent(t, repos, "agt_task_json_budget", 709)
	task := newTask("task-json-budget", "agt_task_json_budget", "reality_probe.v1", []byte("payload"))
	if _, _, err := repos.NodeAgentTask.CreateOrGet(ctx, task); err != nil {
		t.Fatal(err)
	}
	wire, err := json.Marshal(nodeprotocol.Task{
		ID: task.TaskID, Kind: task.Kind, Args: task.Args, InputSHA256: task.InputSHA256,
	})
	if err != nil {
		t.Fatal(err)
	}
	exactArrayBytes := len("[]") + len(wire)
	offeredAt := time.Date(2026, 9, 11, 11, 30, 0, 0, time.UTC)
	tooSmall, err := repos.NodeAgentTask.Offer(ctx, task.AgentID, []string{task.Kind}, 1, exactArrayBytes-1, offeredAt)
	if err != nil || len(tooSmall) != 0 {
		t.Fatalf("too-small encoded budget = (%+v, %v)", tooSmall, err)
	}
	stored, err := repos.NodeAgentTask.GetByTaskID(ctx, task.TaskID)
	if err != nil || stored.Status != domain.NodeAgentTaskQueued || stored.OfferCount != 0 {
		t.Fatalf("task was marked without fitting response = (%+v, %v)", stored, err)
	}
	exact, err := repos.NodeAgentTask.Offer(ctx, task.AgentID, []string{task.Kind}, 1, exactArrayBytes, offeredAt)
	if err != nil || len(exact) != 1 || exact[0].Status != domain.NodeAgentTaskOffered {
		t.Fatalf("exact encoded budget = (%+v, %v)", exact, err)
	}
}

func TestNodeAgentTaskFailedRetryRequiresNewIDAndSameAgent(t *testing.T) {
	repos := newTaskTestRepos(t)
	ctx := context.Background()
	createTaskTestAgent(t, repos, "agt_task_retry_a", 706)
	createTaskTestAgent(t, repos, "agt_task_retry_b", 707)
	failed := newTask("task-retry-failed", "agt_task_retry_a", "reality_probe.v1", []byte("first"))
	if _, _, err := repos.NodeAgentTask.CreateOrGet(ctx, failed); err != nil {
		t.Fatal(err)
	}
	if _, err := repos.NodeAgentTask.Offer(ctx, failed.AgentID, []string{failed.Kind}, 1, int(nodeprotocol.MaxSyncBodyBytes), time.Now()); err != nil {
		t.Fatal(err)
	}
	failedResult := domain.NodeAgentTaskResult{
		TaskID: failed.TaskID, Kind: failed.Kind, InputSHA256: failed.InputSHA256,
		OK: false, ErrorCode: "probe_failed", Error: "probe timed out",
	}
	if err := repos.NodeAgentTask.CompleteBatch(ctx, failed.AgentID, []domain.NodeAgentTaskResult{failedResult}, time.Now()); err != nil {
		t.Fatal(err)
	}
	failedResult.Result = []byte{}
	if err := repos.NodeAgentTask.CompleteBatch(ctx, failed.AgentID, []domain.NodeAgentTaskResult{failedResult}, time.Now()); err != nil {
		t.Fatalf("nil/empty terminal payload replay: %v", err)
	}

	retry := newTask("task-retry-second", failed.AgentID, failed.Kind, failed.Args)
	retry.SupersedesTaskID = failed.TaskID
	if stored, created, err := repos.NodeAgentTask.CreateOrGet(ctx, retry); err != nil || !created || stored.TaskID == failed.TaskID {
		t.Fatalf("failed retry create = (%+v, %v, %v)", stored, created, err)
	}
	wrongAgent := newTask("task-retry-wrong-agent", "agt_task_retry_b", failed.Kind, failed.Args)
	wrongAgent.SupersedesTaskID = failed.TaskID
	if _, _, err := repos.NodeAgentTask.CreateOrGet(ctx, wrongAgent); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("cross-agent supersedes = %v, want ErrConflict", err)
	}

	indeterminate := newTask("task-retry-indeterminate", failed.AgentID, "agent_upgrade.v1", []byte("uncertain"))
	if _, _, err := repos.NodeAgentTask.CreateOrGet(ctx, indeterminate); err != nil {
		t.Fatal(err)
	}
	if _, err := repos.NodeAgentTask.Offer(ctx, indeterminate.AgentID, []string{indeterminate.Kind}, 1, int(nodeprotocol.MaxSyncBodyBytes), time.Now()); err != nil {
		t.Fatal(err)
	}
	uncertainResult := domain.NodeAgentTaskResult{
		TaskID: indeterminate.TaskID, Kind: indeterminate.Kind, InputSHA256: indeterminate.InputSHA256,
		Indeterminate: true, ErrorCode: "execution_interrupted", Error: "outcome is unknown after restart",
	}
	if err := repos.NodeAgentTask.CompleteBatch(ctx, indeterminate.AgentID, []domain.NodeAgentTaskResult{uncertainResult}, time.Now()); err != nil {
		t.Fatal(err)
	}
	stored, err := repos.NodeAgentTask.GetByTaskID(ctx, indeterminate.TaskID)
	if err != nil || stored.Status != domain.NodeAgentTaskIndeterminate || !stored.ResultIndeterminate ||
		stored.ResultOK == nil || *stored.ResultOK {
		t.Fatalf("indeterminate terminal = (%+v, %v)", stored, err)
	}
	if err := repos.NodeAgentTask.CompleteBatch(ctx, indeterminate.AgentID, []domain.NodeAgentTaskResult{uncertainResult}, time.Now()); err != nil {
		t.Fatalf("indeterminate replay: %v", err)
	}
	ordinaryFailure := uncertainResult
	ordinaryFailure.Indeterminate = false
	if err := repos.NodeAgentTask.CompleteBatch(ctx, indeterminate.AgentID, []domain.NodeAgentTaskResult{ordinaryFailure}, time.Now()); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("indeterminate bit conflict = %v, want ErrConflict", err)
	}
	blindRetry := newTask("task-retry-indeterminate-second", indeterminate.AgentID, indeterminate.Kind, indeterminate.Args)
	blindRetry.SupersedesTaskID = indeterminate.TaskID
	if _, _, err := repos.NodeAgentTask.CreateOrGet(ctx, blindRetry); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("blind retry of indeterminate task = %v, want ErrConflict", err)
	}
}

func newTaskTestRepos(t *testing.T) ports.Repos {
	t.Helper()
	db, err := openTestDB(t)
	if err != nil {
		t.Fatal(err)
	}
	if err := EnsureSchema(db); err != nil {
		t.Fatal(err)
	}
	return NewRepos(db)
}

func newTaskTestReposWithQuota(t *testing.T, quota nodeAgentTaskQuota) (ports.Repos, *gorm.DB) {
	t.Helper()
	db, err := openTestDB(t)
	if err != nil {
		t.Fatal(err)
	}
	if err := EnsureSchema(db); err != nil {
		t.Fatal(err)
	}
	repos := NewRepos(db)
	repos.NodeAgentTask = newNodeAgentTaskRepo(db, quota)
	return repos, db
}

func createTaskTestAgent(t *testing.T, repos ports.Repos, agentID string, panelID int64) {
	t.Helper()
	agent := &domain.NodeAgent{
		AgentID: agentID, PanelID: panelID,
		CredentialSHA256: nodeprotocol.ComputeTaskInputSHA256(agentID, nil),
	}
	if err := repos.NodeAgent.Create(context.Background(), agent); err != nil {
		t.Fatal(err)
	}
}

func newTask(taskID, agentID, kind string, args []byte) *domain.NodeAgentTask {
	return &domain.NodeAgentTask{
		TaskID: taskID, AgentID: agentID, Kind: kind,
		Args: args, InputSHA256: nodeprotocol.ComputeTaskInputSHA256(kind, args),
	}
}
