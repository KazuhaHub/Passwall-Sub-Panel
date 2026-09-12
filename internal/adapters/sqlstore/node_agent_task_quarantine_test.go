package sqlstore

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"
	"sync"
	"testing"
	"time"

	nodeprotocol "github.com/KazuhaHub/passwall-node/protocol"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

func newQuarantineTestRepos(t *testing.T, quota nodeAgentTaskQuarantineQuota) (ports.Repos, *gorm.DB) {
	t.Helper()
	repos, db := newTaskTestReposWithQuota(t, defaultNodeAgentTaskQuota())
	repos.NodeAgentTask.(*nodeAgentTaskRepo).quarantineQuota = quota
	return repos, db
}

func quarantineTestResult(task *domain.NodeAgentTask) domain.NodeAgentTaskResult {
	return domain.NodeAgentTaskResult{TaskID: task.TaskID, Kind: task.Kind, InputSHA256: task.InputSHA256, OK: true, Result: []byte{0, 255, 'x'}}
}

func TestNodeAgentTaskQuarantinePreservesCanonicalCompleteWireEvidence(t *testing.T) {
	repos := newTaskTestRepos(t)
	ctx := context.Background()
	const agentID = "agt_quarantine_wire"
	createTaskTestAgent(t, repos, agentID, 901)
	first := time.Date(2026, 9, 11, 12, 0, 0, 0, time.UTC)
	for i, result := range []domain.NodeAgentTaskResult{
		quarantineTestResult(newTask("task-orphan-success", agentID, "reality_probe.v1", []byte("input"))),
		{TaskID: "task-orphan-failed", Kind: "reality_probe.v1", InputSHA256: strings.Repeat("a", 64), ErrorCode: "probe_failed", Error: "private error 完整"},
		{TaskID: "task-orphan-indeterminate", Kind: "agent_upgrade.v1", InputSHA256: strings.Repeat("b", 64), Indeterminate: true, ErrorCode: "execution_interrupted", Error: "private outcome unknown"},
	} {
		if err := repos.NodeAgentTask.CompleteBatch(ctx, agentID, []domain.NodeAgentTaskResult{result}, first); err != nil {
			t.Fatal(err)
		}
		stored, err := repos.NodeAgentTask.GetQuarantinedResult(ctx, agentID, result.TaskID)
		if err != nil {
			t.Fatal(err)
		}
		payload, _ := json.Marshal(taskResultToWire(result))
		hash := sha256.Sum256(payload)
		if stored.AgentID != agentID || stored.TaskID != result.TaskID || stored.Reason != domain.NodeAgentTaskQuarantineUnknownTask ||
			!bytes.Equal(stored.Payload, payload) || stored.PayloadSHA256 != hex.EncodeToString(hash[:]) ||
			!stored.FirstSeenAt.Equal(first) || !stored.LastSeenAt.Equal(first) ||
			stored.Result.TaskID != result.TaskID || stored.Result.Kind != result.Kind || stored.Result.InputSHA256 != result.InputSHA256 ||
			stored.Result.OK != result.OK || stored.Result.Indeterminate != result.Indeterminate ||
			!bytes.Equal(stored.Result.Result, result.Result) || stored.Result.ErrorCode != result.ErrorCode || stored.Result.Error != result.Error {
			t.Fatalf("canonical evidence %d did not round trip", i)
		}
		stored.Payload[0] = '!'
		if len(stored.Result.Result) != 0 {
			stored.Result.Result[0] = 'z'
		}
		last := first.Add(time.Hour)
		if err := repos.NodeAgentTask.CompleteBatch(ctx, agentID, []domain.NodeAgentTaskResult{result}, last); err != nil {
			t.Fatal(err)
		}
		if err := repos.NodeAgentTask.CompleteBatch(ctx, agentID, []domain.NodeAgentTaskResult{result}, first.Add(-time.Hour)); err != nil {
			t.Fatal(err)
		}
		again, err := repos.NodeAgentTask.GetQuarantinedResult(ctx, agentID, result.TaskID)
		if err != nil || !bytes.Equal(again.Payload, payload) || !again.FirstSeenAt.Equal(first) || !again.LastSeenAt.Equal(last) {
			t.Fatalf("replay changed immutable evidence or regressed time: %v", err)
		}
		if _, err := repos.NodeAgentTask.GetByTaskID(ctx, result.TaskID); !errors.Is(err, domain.ErrNotFound) {
			t.Fatalf("unknown receipt created a task: %v", err)
		}
	}
	for _, legacy := range []domain.NodeAgentTaskResult{{TaskID: "task-legacy", OK: true}, {TaskID: "task-missing-hash", Kind: "reality_probe.v1", OK: true}} {
		if err := repos.NodeAgentTask.CompleteBatch(ctx, agentID, []domain.NodeAgentTaskResult{legacy}, first); !errors.Is(err, domain.ErrValidation) {
			t.Fatalf("incomplete evidence accepted: %v", err)
		}
	}
}

func TestNodeAgentTaskQueuedReceiptClosesDispatchWithoutReleasingActiveQuota(t *testing.T) {
	repos, _ := newTaskTestReposWithQuota(t, nodeAgentTaskQuota{MaxActiveTasks: 1, MaxActiveArgBytes: 32})
	ctx := context.Background()
	const agentID = "agt_quarantine_queued"
	createTaskTestAgent(t, repos, agentID, 902)
	task := newTask("task-never-offered", agentID, "reality_probe.v1", []byte("input"))
	key := strings.Repeat("a", 64)
	task.IdempotencyKeySHA256 = &key
	if _, _, err := repos.NodeAgentTask.CreateOrGet(ctx, task); err != nil {
		t.Fatal(err)
	}
	seen := time.Now().UTC().Truncate(time.Millisecond)
	if err := repos.NodeAgentTask.CompleteBatch(ctx, agentID, []domain.NodeAgentTaskResult{quarantineTestResult(task)}, seen); err != nil {
		t.Fatal(err)
	}
	stored, err := repos.NodeAgentTask.GetByTaskID(ctx, task.TaskID)
	if err != nil || stored.Status != domain.NodeAgentTaskQueued || stored.DispatchClosedAt == nil || !stored.DispatchClosedAt.Equal(seen) ||
		stored.DispatchClosedReason != "never_offered" || stored.ResultOK != nil || stored.CompletedAt != nil || stored.OfferCount != 0 {
		t.Fatalf("queued receipt unexpectedly completed/reopened task: (%+v, %v)", stored, err)
	}
	q, err := repos.NodeAgentTask.GetQuarantinedResult(ctx, agentID, task.TaskID)
	if err != nil || q.Reason != domain.NodeAgentTaskQuarantineNeverOffered {
		t.Fatalf("queued evidence reason: %v", err)
	}
	offered, err := repos.NodeAgentTask.Offer(ctx, agentID, ports.NodeAgentTaskOfferSupport{EligibleKinds: []string{task.Kind}}, 64, int(nodeprotocol.MaxSyncBodyBytes), seen.Add(time.Second))
	if err != nil || len(offered) != 0 {
		t.Fatalf("closed task offered: (%d, %v)", len(offered), err)
	}
	if _, _, err := repos.NodeAgentTask.CreateOrGet(ctx, newTask("task-after-closed", agentID, task.Kind, []byte("x"))); !errors.Is(err, domain.ErrResourceExhausted) {
		t.Fatalf("closed queued task released active quota: %v", err)
	}
	if replay, created, err := repos.NodeAgentTask.CreateOrGet(ctx, task); err != nil || created || replay.DispatchClosedAt == nil {
		t.Fatalf("existing replay fenced: (%v,%v)", created, err)
	}
	alias := *task
	alias.TaskID = "task-idempotency-alias"
	if replay, created, err := repos.NodeAgentTask.CreateOrGet(ctx, &alias); err != nil || created || replay.TaskID != task.TaskID {
		t.Fatalf("idempotency replay fenced: (%v,%v)", created, err)
	}
	for _, closed := range []domain.NodeAgentTask{{DispatchClosedAt: &seen}, {DispatchClosedReason: "never_offered"}} {
		pristine := *newTask("task-preset-close", agentID, task.Kind, nil)
		pristine.DispatchClosedAt, pristine.DispatchClosedReason = closed.DispatchClosedAt, closed.DispatchClosedReason
		if _, _, err := repos.NodeAgentTask.CreateOrGet(ctx, &pristine); !errors.Is(err, domain.ErrValidation) {
			t.Fatalf("preclosed new task accepted: %v", err)
		}
	}
}

func TestNodeAgentTaskQuarantineFenceIsAgentLocal(t *testing.T) {
	repos := newTaskTestRepos(t)
	ctx := context.Background()
	createTaskTestAgent(t, repos, "agt_quarantine_a", 903)
	createTaskTestAgent(t, repos, "agt_quarantine_b", 904)
	task := newTask("task-predictable-next", "agt_quarantine_a", "reality_probe.v1", []byte("a"))
	result := quarantineTestResult(task)
	if err := repos.NodeAgentTask.CompleteBatch(ctx, task.AgentID, []domain.NodeAgentTaskResult{result}, time.Now()); err != nil {
		t.Fatal(err)
	}
	if _, _, err := repos.NodeAgentTask.CreateOrGet(ctx, task); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("same-agent orphan ID reused: %v", err)
	}
	foreign := *task
	foreign.AgentID = "agt_quarantine_b"
	if _, created, err := repos.NodeAgentTask.CreateOrGet(ctx, &foreign); err != nil || !created {
		t.Fatalf("foreign quarantine preoccupied legitimate ID: %v", err)
	}
	if err := repos.NodeAgentTask.CompleteBatch(ctx, task.AgentID, []domain.NodeAgentTaskResult{result}, time.Now()); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("known foreign ownership ignored on orphan replay: %v", err)
	}
	// Two agents may independently report the same still-unknown ID. It is not
	// a global quarantine primary key, even when their payloads disagree.
	result.TaskID = "task-shared-unknown"
	if err := repos.NodeAgentTask.CompleteBatch(ctx, task.AgentID, []domain.NodeAgentTaskResult{result}, time.Now()); err != nil {
		t.Fatal(err)
	}
	other := result
	other.Result = []byte("different private evidence")
	if err := repos.NodeAgentTask.CompleteBatch(ctx, foreign.AgentID, []domain.NodeAgentTaskResult{other}, time.Now()); err != nil {
		t.Fatal(err)
	}
	for _, agentID := range []string{task.AgentID, foreign.AgentID} {
		if _, err := repos.NodeAgentTask.GetQuarantinedResult(ctx, agentID, result.TaskID); err != nil {
			t.Fatal(err)
		}
	}
}

func TestNodeAgentTaskQuarantineRestoredRowsNeverPromoteEvidence(t *testing.T) {
	repos, db := newQuarantineTestRepos(t, defaultNodeAgentTaskQuarantineQuota())
	ctx := context.Background()
	const agentID = "agt_quarantine_restore"
	createTaskTestAgent(t, repos, agentID, 905)
	task := newTask("task-restored-offered", agentID, "reality_probe.v1", []byte("original"))
	result := quarantineTestResult(task)
	first := time.Now().UTC().Truncate(time.Millisecond)
	if err := repos.NodeAgentTask.CompleteBatch(ctx, agentID, []domain.NodeAgentTaskResult{result}, first); err != nil {
		t.Fatal(err)
	}
	row := nodeAgentTaskFromDomain(task)
	row.Status, row.OfferCount, row.FirstOfferedAt = "offered", 1, &first
	if err := db.Create(row).Error; err != nil {
		t.Fatal(err)
	}
	if offered, err := repos.NodeAgentTask.Offer(ctx, agentID, ports.NodeAgentTaskOfferSupport{EligibleKinds: []string{task.Kind}}, 64, int(nodeprotocol.MaxSyncBodyBytes), first.Add(time.Second)); err != nil || len(offered) != 0 {
		t.Fatalf("restored row dispatched despite existing evidence fence: %v", err)
	}
	changed := result
	changed.Result = []byte("changed")
	if err := repos.NodeAgentTask.CompleteBatch(ctx, agentID, []domain.NodeAgentTaskResult{changed}, first.Add(time.Second)); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("restored row bypassed evidence conflict: %v", err)
	}
	if err := repos.NodeAgentTask.CompleteBatch(ctx, agentID, []domain.NodeAgentTaskResult{result}, first.Add(2*time.Second)); err != nil {
		t.Fatal(err)
	}
	stored, err := repos.NodeAgentTask.GetByTaskID(ctx, task.TaskID)
	if err != nil || stored.Status != domain.NodeAgentTaskOffered || stored.DispatchClosedAt == nil || stored.DispatchClosedReason != "unknown_task" || stored.ResultOK != nil || stored.CompletedAt != nil {
		t.Fatalf("restored offered evidence promoted: (%+v, %v)", stored, err)
	}
	q, err := repos.NodeAgentTask.GetQuarantinedResult(ctx, agentID, task.TaskID)
	if err != nil || q.Reason != domain.NodeAgentTaskQuarantineUnknownTask || !q.FirstSeenAt.Equal(first) {
		t.Fatalf("restored row rewrote evidence: %v", err)
	}
	if offered, err := repos.NodeAgentTask.Offer(ctx, agentID, ports.NodeAgentTaskOfferSupport{EligibleKinds: []string{task.Kind}}, 64, int(nodeprotocol.MaxSyncBodyBytes), first.Add(3*time.Second)); err != nil || len(offered) != 0 {
		t.Fatalf("restored closed row dispatched: %v", err)
	}
	ok := true
	if err := db.Model(&nodeAgentTaskRow{}).Where("task_id = ?", task.TaskID).Updates(map[string]any{"status": "succeeded", "result_ok": &ok, "result": []byte("conflicting terminal")}).Error; err != nil {
		t.Fatal(err)
	}
	if err := repos.NodeAgentTask.CompleteBatch(ctx, agentID, []domain.NodeAgentTaskResult{result}, first.Add(4*time.Second)); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("stored terminal conflict ignored due to evidence: %v", err)
	}
}

func TestNodeAgentTaskQuarantineQuotaAndWholeBatchAtomicity(t *testing.T) {
	for _, byteBound := range []bool{false, true} {
		t.Run(fmt.Sprint("bytes=", byteBound), func(t *testing.T) {
			ctx := context.Background()
			const agentID = "agt_quarantine_quota"
			orphan := quarantineTestResult(newTask("task-quota-orphan-1", agentID, "reality_probe.v1", nil))
			encoded, _ := json.Marshal(taskResultToWire(orphan))
			quota := nodeAgentTaskQuarantineQuota{MaxRows: 1, MaxPayloadBytes: 1 << 20}
			if byteBound {
				quota = nodeAgentTaskQuarantineQuota{MaxRows: 100, MaxPayloadBytes: int64(len(encoded))}
			}
			repos, _ := newQuarantineTestRepos(t, quota)
			createTaskTestAgent(t, repos, agentID, 906)
			first := time.Now().UTC().Truncate(time.Millisecond)
			if err := repos.NodeAgentTask.CompleteBatch(ctx, agentID, []domain.NodeAgentTaskResult{orphan}, first); err != nil {
				t.Fatal(err)
			}
			if err := repos.NodeAgentTask.CompleteBatch(ctx, agentID, []domain.NodeAgentTaskResult{orphan}, first.Add(time.Second)); err != nil {
				t.Fatalf("full replay rejected: %v", err)
			}
			changed := orphan
			changed.Result = []byte("new")
			if err := repos.NodeAgentTask.CompleteBatch(ctx, agentID, []domain.NodeAgentTaskResult{changed}, first); !errors.Is(err, domain.ErrConflict) {
				t.Fatalf("quota hid evidence conflict: %v", err)
			}
			offered := newTask("task-quota-offered", agentID, "reality_probe.v1", []byte("offered"))
			queued := newTask("task-quota-queued", agentID, "reality_probe.v1", []byte("queued"))
			if _, _, err := repos.NodeAgentTask.CreateOrGet(ctx, offered); err != nil {
				t.Fatal(err)
			}
			if _, err := repos.NodeAgentTask.Offer(ctx, agentID, ports.NodeAgentTaskOfferSupport{EligibleKinds: []string{offered.Kind}}, 1, int(nodeprotocol.MaxSyncBodyBytes), first); err != nil {
				t.Fatal(err)
			}
			if _, _, err := repos.NodeAgentTask.CreateOrGet(ctx, queued); err != nil {
				t.Fatal(err)
			}
			for _, neighbour := range []domain.NodeAgentTaskResult{quarantineTestResult(queued), {TaskID: "task-quota-orphan-2", Kind: orphan.Kind, InputSHA256: orphan.InputSHA256, OK: true}} {
				if err := repos.NodeAgentTask.CompleteBatch(ctx, agentID, []domain.NodeAgentTaskResult{quarantineTestResult(offered), orphan, neighbour}, first.Add(2*time.Second)); !errors.Is(err, domain.ErrResourceExhausted) {
					t.Fatalf("new evidence beyond cap: %v", err)
				}
				old, _ := repos.NodeAgentTask.GetByTaskID(ctx, offered.TaskID)
				qtask, _ := repos.NodeAgentTask.GetByTaskID(ctx, queued.TaskID)
				q, _ := repos.NodeAgentTask.GetQuarantinedResult(ctx, agentID, orphan.TaskID)
				if old.Status != domain.NodeAgentTaskOffered || qtask.DispatchClosedAt != nil || !q.LastSeenAt.Equal(first.Add(time.Second)) {
					t.Fatal("quota failure partially committed neighbour completion, dispatch closure, or replay timestamp")
				}
				if _, err := repos.NodeAgentTask.GetQuarantinedResult(ctx, agentID, neighbour.TaskID); !errors.Is(err, domain.ErrNotFound) {
					t.Fatalf("failed batch retained new evidence: %v", err)
				}
			}
			// A known conflict also rolls back a new orphan and queued closure.
			bad := quarantineTestResult(offered)
			bad.InputSHA256 = strings.Repeat("0", 64)
			if err := repos.NodeAgentTask.CompleteBatch(ctx, agentID, []domain.NodeAgentTaskResult{bad, quarantineTestResult(queued)}, first); !errors.Is(err, domain.ErrConflict) {
				t.Fatalf("quota hid known identity conflict: %v", err)
			}
		})
	}
}

func TestNodeAgentTaskQuarantineConcurrentAdmissionAndAgentIsolation(t *testing.T) {
	for _, byteBound := range []bool{false, true} {
		t.Run(fmt.Sprint("bytes=", byteBound), func(t *testing.T) {
			const count = 8
			const agentA, agentB = "agt_quarantine_concurrent_a", "agt_quarantine_concurrent_b"
			sample := quarantineTestResult(newTask("task-concurrent-00", agentA, "reality_probe.v1", nil))
			payload, _ := json.Marshal(taskResultToWire(sample))
			quota := nodeAgentTaskQuarantineQuota{MaxRows: count, MaxPayloadBytes: 1 << 20}
			if byteBound {
				quota = nodeAgentTaskQuarantineQuota{MaxRows: 100, MaxPayloadBytes: int64(count * len(payload))}
			}
			repos, db := newQuarantineTestRepos(t, quota)
			createTaskTestAgent(t, repos, agentA, 907)
			createTaskTestAgent(t, repos, agentB, 908)
			start := make(chan struct{})
			outcomes := make(chan error, 64)
			var wg sync.WaitGroup
			for _, agentID := range []string{agentA, agentB} {
				for i := 0; i < 32; i++ {
					wg.Add(1)
					go func(agentID string, index int) {
						defer wg.Done()
						<-start
						result := sample
						result.TaskID = fmt.Sprintf("task-concurrent-%02d", index)
						outcomes <- repos.NodeAgentTask.CompleteBatch(context.Background(), agentID, []domain.NodeAgentTaskResult{result}, time.Now())
					}(agentID, i)
				}
			}
			close(start)
			wg.Wait()
			close(outcomes)
			successes := 0
			for err := range outcomes {
				if err == nil {
					successes++
				} else if !errors.Is(err, domain.ErrResourceExhausted) {
					t.Fatal(err)
				}
			}
			if successes != 2*count {
				t.Fatalf("concurrent admitted %d, want %d", successes, 2*count)
			}
			for _, agentID := range []string{agentA, agentB} {
				var usage nodeAgentTaskQuarantineUsage
				if err := db.Model(&nodeAgentTaskResultQuarantineRow{}).Select("COUNT(*) AS quarantine_rows, COALESCE(SUM(LENGTH(payload)),0) AS quarantine_payload_bytes").Where("agent_id = ?", agentID).Scan(&usage).Error; err != nil {
					t.Fatal(err)
				}
				if usage.Rows != count || usage.PayloadBytes != int64(count*len(payload)) {
					t.Fatalf("agent %s exceeded/underused cap: %+v", agentID, usage)
				}
			}
		})
	}
}

func TestNodeAgentTaskQuarantineConcurrentReplayNeverOverwritesEvidence(t *testing.T) {
	repos, db := newQuarantineTestRepos(t, nodeAgentTaskQuarantineQuota{MaxRows: 1, MaxPayloadBytes: 1 << 20})
	const agentID = "agt_quarantine_replay_race"
	createTaskTestAgent(t, repos, agentID, 916)
	result := quarantineTestResult(newTask("task-replay-race", agentID, "reality_probe.v1", nil))
	start := make(chan struct{})
	outcomes := make(chan error, 32)
	for i := 0; i < 32; i++ {
		incoming := result
		if i%2 != 0 {
			incoming.Result = []byte("other immutable evidence")
		}
		go func() {
			<-start
			outcomes <- repos.NodeAgentTask.CompleteBatch(context.Background(), agentID, []domain.NodeAgentTaskResult{incoming}, time.Now())
		}()
	}
	close(start)
	successes, conflicts := 0, 0
	for i := 0; i < 32; i++ {
		err := <-outcomes
		switch {
		case err == nil:
			successes++
		case errors.Is(err, domain.ErrConflict):
			conflicts++
		default:
			t.Fatalf("concurrent replay unexpected error: %v", err)
		}
	}
	if successes != 16 || conflicts != 16 {
		t.Fatalf("immutable race outcomes=%d success/%d conflict", successes, conflicts)
	}
	var count int64
	if err := db.Model(&nodeAgentTaskResultQuarantineRow{}).Where("agent_id = ?", agentID).Count(&count).Error; err != nil || count != 1 {
		t.Fatalf("replay duplicated evidence: %d/%v", count, err)
	}
	if _, err := repos.NodeAgentTask.GetQuarantinedResult(context.Background(), agentID, result.TaskID); err != nil {
		t.Fatal(err)
	}
}

func TestNodeAgentTaskQuarantineQuotaDefaultsAndOverflowFailClosed(t *testing.T) {
	quota := defaultNodeAgentTaskQuarantineQuota()
	if quota.MaxRows != 256 || quota.MaxPayloadBytes != 16<<20 {
		t.Fatalf("compiled cap changed: %+v", quota)
	}
	for _, test := range []struct {
		quota       nodeAgentTaskQuarantineQuota
		usage       nodeAgentTaskQuarantineUsage
		rows, bytes int64
	}{
		{nodeAgentTaskQuarantineQuota{}, nodeAgentTaskQuarantineUsage{}, 1, 1},
		{quota, nodeAgentTaskQuarantineUsage{Rows: math.MaxInt64}, 1, 1},
		{quota, nodeAgentTaskQuarantineUsage{PayloadBytes: math.MaxInt64}, 1, 1},
		{nodeAgentTaskQuarantineQuota{MaxRows: math.MaxInt64, MaxPayloadBytes: math.MaxInt64}, nodeAgentTaskQuarantineUsage{Rows: math.MaxInt64 - 1}, 2, 1},
		{nodeAgentTaskQuarantineQuota{MaxRows: math.MaxInt64, MaxPayloadBytes: math.MaxInt64}, nodeAgentTaskQuarantineUsage{PayloadBytes: math.MaxInt64 - 1}, 1, 2},
	} {
		if err := checkNodeAgentTaskQuarantineQuota(test.quota, test.usage, test.rows, test.bytes); !errors.Is(err, domain.ErrResourceExhausted) {
			t.Fatalf("unsafe quota admitted: %v", err)
		}
	}
	if err := checkNodeAgentTaskQuarantineQuota(quota, nodeAgentTaskQuarantineUsage{Rows: -1}, 1, 1); err == nil {
		t.Fatal("negative usage admitted")
	}
}

func TestNodeAgentTaskQuarantineGetterFailsClosedOnCorruptionWithoutPrivateLogs(t *testing.T) {
	repos, db := newQuarantineTestRepos(t, defaultNodeAgentTaskQuarantineQuota())
	ctx := context.Background()
	const agentID = "agt_quarantine_corrupt"
	const secret = "private-sentinel-do-not-log"
	createTaskTestAgent(t, repos, agentID, 909)
	result := domain.NodeAgentTaskResult{TaskID: "task-corrupt-evidence", Kind: "reality_probe.v1", InputSHA256: strings.Repeat("a", 64), ErrorCode: "probe_failed", Error: secret}
	first := time.Now().UTC().Truncate(time.Millisecond)
	if err := repos.NodeAgentTask.CompleteBatch(ctx, agentID, []domain.NodeAgentTaskResult{result}, first); err != nil {
		t.Fatal(err)
	}
	var original nodeAgentTaskResultQuarantineRow
	if err := db.Where("agent_id = ? AND task_id = ?", agentID, result.TaskID).First(&original).Error; err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name   string
		mutate func(*nodeAgentTaskResultQuarantineRow)
	}{
		{"sha", func(q *nodeAgentTaskResultQuarantineRow) { q.PayloadSHA256 = strings.Repeat("0", 64) }},
		{"malformed", func(q *nodeAgentTaskResultQuarantineRow) { q.Payload = []byte(secret) }},
		{"noncanonical", func(q *nodeAgentTaskResultQuarantineRow) { q.Payload = append(q.Payload, ' ') }},
		{"extra-field", func(q *nodeAgentTaskResultQuarantineRow) {
			q.Payload = append(q.Payload[:len(q.Payload)-1], []byte(`,"extra":"`+secret+`"}`)...)
		}},
		{"mismatched-id", func(q *nodeAgentTaskResultQuarantineRow) {
			wire := taskResultToWire(result)
			wire.ID = "task-other-identity"
			q.Payload, _ = json.Marshal(wire)
		}},
		{"invalid-result", func(q *nodeAgentTaskResultQuarantineRow) {
			wire := taskResultToWire(result)
			wire.OK = true
			q.Payload, _ = json.Marshal(wire)
		}},
		{"legacy", func(q *nodeAgentTaskResultQuarantineRow) {
			q.Payload = []byte(`{"id":"task-corrupt-evidence","kind":"","input_sha256":"","ok":true}`)
		}},
		{"time", func(q *nodeAgentTaskResultQuarantineRow) { q.LastSeenAt = q.FirstSeenAt.Add(-time.Second) }},
	} {
		t.Run(test.name, func(t *testing.T) {
			q := original
			q.Payload = append([]byte(nil), original.Payload...)
			test.mutate(&q)
			if test.name != "sha" {
				hash := sha256.Sum256(q.Payload)
				q.PayloadSHA256 = hex.EncodeToString(hash[:])
			}
			if err := db.Model(&nodeAgentTaskResultQuarantineRow{}).Where("agent_id = ? AND task_id = ?", agentID, result.TaskID).
				Updates(map[string]any{"payload": q.Payload, "payload_sha256": q.PayloadSHA256, "last_seen_at": q.LastSeenAt}).Error; err != nil {
				t.Fatal(err)
			}
			if _, err := repos.NodeAgentTask.GetQuarantinedResult(ctx, agentID, result.TaskID); err == nil || strings.Contains(err.Error(), secret) {
				t.Fatalf("corrupt evidence accepted or leaked: %v", err)
			}
			if err := repos.NodeAgentTask.CompleteBatch(ctx, agentID, []domain.NodeAgentTaskResult{result}, first.Add(time.Hour)); err == nil || strings.Contains(err.Error(), secret) {
				t.Fatalf("corrupt replay accepted or leaked: %v", err)
			}
		})
	}
	// Guard helpers also keep driver causes available without logging them.
	cause := errors.New(secret)
	safe := safeNodeAgentTaskResultStorageError(cause)
	if strings.Contains(safe.Error(), secret) || !errors.Is(safe, cause) {
		t.Fatal("driver diagnostic sanitization lost privacy or cause")
	}
	if err := db.Model(&nodeAgentTaskResultQuarantineRow{}).Where("agent_id = ? AND task_id = ?", agentID, result.TaskID).
		Updates(map[string]any{"payload": original.Payload, "payload_sha256": original.PayloadSHA256, "last_seen_at": original.LastSeenAt}).Error; err != nil {
		t.Fatal(err)
	}
	for _, identity := range [][2]string{{strings.ToUpper(agentID), result.TaskID}, {agentID, strings.ToUpper(result.TaskID)}} {
		if _, err := repos.NodeAgentTask.GetQuarantinedResult(ctx, identity[0], identity[1]); !errors.Is(err, domain.ErrNotFound) {
			t.Fatalf("collation folded quarantine byte identity: %v", err)
		}
	}
}

func TestNodeAgentTaskQuarantineSchemaRejectsInvalidReasonAndNullPayload(t *testing.T) {
	repos, db := newQuarantineTestRepos(t, defaultNodeAgentTaskQuarantineQuota())
	const agentID = "agt_quarantine_constraints"
	createTaskTestAgent(t, repos, agentID, 910)
	result := quarantineTestResult(newTask("task-constraints", agentID, "reality_probe.v1", nil))
	if err := repos.NodeAgentTask.CompleteBatch(context.Background(), agentID, []domain.NodeAgentTaskResult{result}, time.Now()); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		column string
		value  any
	}{{"reason", "arbitrary_reason"}, {"payload", nil}} {
		if err := db.Model(&nodeAgentTaskResultQuarantineRow{}).Where("agent_id = ? AND task_id = ?", agentID, result.TaskID).Update(test.column, test.value).Error; err == nil {
			t.Fatalf("invalid quarantine %s accepted", test.column)
		}
	}
}

func TestNodeAgentTaskQuarantineMixedValidReceiptAndKnownConflictAreAtomic(t *testing.T) {
	repos, db := newQuarantineTestRepos(t, defaultNodeAgentTaskQuarantineQuota())
	ctx := context.Background()
	const agentA, agentB = "agt_quarantine_atomic_a", "agt_quarantine_atomic_b"
	createTaskTestAgent(t, repos, agentA, 911)
	createTaskTestAgent(t, repos, agentB, 912)
	offered := newTask("task-atomic-offered", agentA, "reality_probe.v1", nil)
	queued := newTask("task-atomic-queued", agentA, "reality_probe.v1", nil)
	foreign := newTask("task-atomic-foreign", agentB, "reality_probe.v1", nil)
	for _, task := range []*domain.NodeAgentTask{offered, foreign} {
		if _, _, err := repos.NodeAgentTask.CreateOrGet(ctx, task); err != nil {
			t.Fatal(err)
		}
		if _, err := repos.NodeAgentTask.Offer(ctx, task.AgentID, ports.NodeAgentTaskOfferSupport{EligibleKinds: []string{task.Kind}}, 1, int(nodeprotocol.MaxSyncBodyBytes), time.Now()); err != nil {
			t.Fatal(err)
		}
	}
	if _, _, err := repos.NodeAgentTask.CreateOrGet(ctx, queued); err != nil {
		t.Fatal(err)
	}
	orphan := quarantineTestResult(newTask("task-atomic-unknown", agentA, offered.Kind, nil))
	for _, bad := range []domain.NodeAgentTaskResult{quarantineTestResult(foreign), {TaskID: queued.TaskID, Kind: queued.Kind, InputSHA256: strings.Repeat("0", 64), OK: true}} {
		batch := []domain.NodeAgentTaskResult{quarantineTestResult(offered), orphan, bad}
		if err := repos.NodeAgentTask.CompleteBatch(ctx, agentA, batch, time.Now()); !errors.Is(err, domain.ErrConflict) {
			t.Fatalf("known conflict accepted: %v", err)
		}
		var count int64
		db.Model(&nodeAgentTaskResultQuarantineRow{}).Count(&count)
		stored, _ := repos.NodeAgentTask.GetByTaskID(ctx, offered.TaskID)
		if count != 0 || stored.Status != domain.NodeAgentTaskOffered {
			t.Fatal("known conflict partially acknowledged neighbours")
		}
	}
	if err := repos.NodeAgentTask.CompleteBatch(ctx, agentA, []domain.NodeAgentTaskResult{quarantineTestResult(offered), quarantineTestResult(queued), orphan}, time.Now()); err != nil {
		t.Fatal(err)
	}
	stored, _ := repos.NodeAgentTask.GetByTaskID(ctx, offered.TaskID)
	if stored.Status != domain.NodeAgentTaskSucceeded {
		t.Fatal("valid offered neighbour did not complete")
	}
	for _, id := range []string{queued.TaskID, orphan.TaskID} {
		if _, err := repos.NodeAgentTask.GetQuarantinedResult(ctx, agentA, id); err != nil {
			t.Fatal(err)
		}
	}
}

func TestNativeAgentQuarantineBlocksDeletionAndPreservesEvidence(t *testing.T) {
	repos, db := newQuarantineTestRepos(t, defaultNodeAgentTaskQuarantineQuota())
	ctx := context.Background()
	panel := &domain.XUIPanel{Kind: domain.PanelKindPSP, Name: "quarantine-delete", URL: "psp://agt_quarantine_delete"}
	agent := &domain.NodeAgent{AgentID: "agt_quarantine_delete", CredentialSHA256: strings.Repeat("a", 64)}
	if err := repos.NativeAgentProvisioning.Create(ctx, panel, agent); err != nil {
		t.Fatal(err)
	}
	first := time.Now().UTC().Truncate(time.Millisecond)
	stream, _, err := repos.NodeAgent.MintStream(ctx, agent.AgentID, domain.NodeAgentStreamConfig, []byte(`{"listeners":[],"coverage":{}}`), first)
	if err != nil {
		t.Fatal(err)
	}
	if err := repos.NodeAgent.RecordApplied(ctx, agent.AgentID, domain.NodeAgentStreamConfig, agent.Epoch, stream.DesiredVersion, stream.DesiredETag, first); err != nil {
		t.Fatal(err)
	}
	orphan := quarantineTestResult(newTask("task-delete-evidence", agent.AgentID, "reality_probe.v1", nil))
	if err := repos.NodeAgentTask.CompleteBatch(ctx, agent.AgentID, []domain.NodeAgentTaskResult{orphan}, first); err != nil {
		t.Fatal(err)
	}
	if err := repos.NativeAgentProvisioning.DeleteConverged(ctx, panel.ID); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("quarantined evidence did not block deletion: %v", err)
	}
	if _, err := repos.NodeAgent.GetByAgentID(ctx, agent.AgentID); err != nil {
		t.Fatal("agent deleted despite evidence")
	}
	q, err := repos.NodeAgentTask.GetQuarantinedResult(ctx, agent.AgentID, orphan.TaskID)
	if err != nil || !q.FirstSeenAt.Equal(first) {
		t.Fatal("evidence deleted/changed by failed deletion")
	}
	var count int64
	db.Model(&nodeAgentTaskRow{}).Where("agent_id = ?", agent.AgentID).Count(&count)
	if count != 0 {
		t.Fatal("orphan evidence was silently promoted to task")
	}
}

func TestNativeAgentQuarantineReceiptAndDeletionCannotProduceAnOrphan(t *testing.T) {
	repos, _ := newQuarantineTestRepos(t, defaultNodeAgentTaskQuarantineQuota())
	ctx := context.Background()
	for i := 0; i < 16; i++ {
		agentID := fmt.Sprintf("agt_quarantine_delete_race_%02d", i)
		panel := &domain.XUIPanel{Kind: domain.PanelKindPSP, Name: agentID, URL: "psp://" + agentID}
		agent := &domain.NodeAgent{AgentID: agentID, CredentialSHA256: nodeprotocol.ComputeTaskInputSHA256(agentID, nil)}
		if err := repos.NativeAgentProvisioning.Create(ctx, panel, agent); err != nil {
			t.Fatal(err)
		}
		first := time.Now().UTC().Truncate(time.Millisecond)
		stream, _, err := repos.NodeAgent.MintStream(ctx, agentID, domain.NodeAgentStreamConfig, []byte(`{"listeners":[],"coverage":{}}`), first)
		if err != nil {
			t.Fatal(err)
		}
		if err := repos.NodeAgent.RecordApplied(ctx, agentID, domain.NodeAgentStreamConfig, agent.Epoch, stream.DesiredVersion, stream.DesiredETag, first); err != nil {
			t.Fatal(err)
		}
		result := quarantineTestResult(newTask(fmt.Sprintf("task-delete-race-%02d", i), agentID, "reality_probe.v1", nil))
		start := make(chan struct{})
		receipt := make(chan error, 1)
		deletion := make(chan error, 1)
		go func() {
			<-start
			receipt <- repos.NodeAgentTask.CompleteBatch(ctx, agentID, []domain.NodeAgentTaskResult{result}, first)
		}()
		go func() { <-start; deletion <- repos.NativeAgentProvisioning.DeleteConverged(ctx, panel.ID) }()
		close(start)
		receiptErr, deleteErr := <-receipt, <-deletion
		switch {
		case receiptErr == nil && errors.Is(deleteErr, domain.ErrConflict):
			if _, err := repos.NodeAgentTask.GetQuarantinedResult(ctx, agentID, result.TaskID); err != nil {
				t.Fatal(err)
			}
			if _, err := repos.NodeAgent.GetByAgentID(ctx, agentID); err != nil {
				t.Fatal(err)
			}
		case deleteErr == nil && errors.Is(receiptErr, domain.ErrNotFound):
			if _, err := repos.NodeAgentTask.GetQuarantinedResult(ctx, agentID, result.TaskID); !errors.Is(err, domain.ErrNotFound) {
				t.Fatalf("orphan quarantine created after agent deletion: %v", err)
			}
		default:
			t.Fatalf("receipt/delete race invalid outcome: %v / %v", receiptErr, deleteErr)
		}
	}
}

func TestNodeAgentTaskForeignSupersedesCannotTakeCrossOwnerLocks(t *testing.T) {
	repos := newTaskTestRepos(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	const agentA, agentB = "agt_supersedes_a", "agt_supersedes_b"
	createTaskTestAgent(t, repos, agentA, 913)
	createTaskTestAgent(t, repos, agentB, 914)
	one := newTask("task-supersedes-a", agentA, "reality_probe.v1", nil)
	two := newTask("task-supersedes-b", agentB, "reality_probe.v1", nil)
	for _, task := range []*domain.NodeAgentTask{one, two} {
		if _, _, err := repos.NodeAgentTask.CreateOrGet(ctx, task); err != nil {
			t.Fatal(err)
		}
	}
	start := make(chan struct{})
	outcomes := make(chan error, 32)
	for i := 0; i < 16; i++ {
		for _, pair := range [][2]*domain.NodeAgentTask{{one, two}, {two, one}} {
			incoming := newTask(fmt.Sprintf("task-cross-supersedes-%s-%02d", pair[0].AgentID, i), pair[0].AgentID, pair[0].Kind, nil)
			incoming.SupersedesTaskID = pair[1].TaskID
			go func() { <-start; _, _, err := repos.NodeAgentTask.CreateOrGet(ctx, incoming); outcomes <- err }()
		}
	}
	close(start)
	for i := 0; i < 32; i++ {
		if err := <-outcomes; !errors.Is(err, domain.ErrConflict) {
			t.Fatalf("cross-owner supersedes did not fail closed promptly: %v", err)
		}
	}
}

type nodeAgentTaskSQLSpy struct{ messages []string }

func (s *nodeAgentTaskSQLSpy) LogMode(logger.LogLevel) logger.Interface { return s }
func (s *nodeAgentTaskSQLSpy) Info(_ context.Context, message string, args ...any) {
	s.messages = append(s.messages, fmt.Sprintf(message, args...))
}
func (s *nodeAgentTaskSQLSpy) Warn(_ context.Context, message string, args ...any) {
	s.messages = append(s.messages, fmt.Sprintf(message, args...))
}
func (s *nodeAgentTaskSQLSpy) Error(_ context.Context, message string, args ...any) {
	s.messages = append(s.messages, fmt.Sprintf(message, args...))
}
func (s *nodeAgentTaskSQLSpy) Trace(_ context.Context, _ time.Time, sql func() (string, int64), err error) {
	statement, _ := sql()
	s.messages = append(s.messages, statement)
	if err != nil {
		s.messages = append(s.messages, err.Error())
	}
}

func TestNodeAgentTaskCompleteBatchNeverLogsOpaqueSQLValues(t *testing.T) {
	repos, db := newQuarantineTestRepos(t, defaultNodeAgentTaskQuarantineQuota())
	ctx := context.Background()
	const agentID = "agt_quarantine_no_sql_logs"
	const secret = "private-driver-sentinel-do-not-log"
	createTaskTestAgent(t, repos, agentID, 915)
	first := time.Now().UTC().Truncate(time.Millisecond)
	// Prepare ordinary offered tasks through the normal repo before attaching
	// the spy, so only the sensitive receipt transaction is under observation.
	one := newTask("task-log-terminal-1", agentID, "reality_probe.v1", nil)
	two := newTask("task-log-terminal-2", agentID, "reality_probe.v1", nil)
	for _, task := range []*domain.NodeAgentTask{one, two} {
		if _, _, err := repos.NodeAgentTask.CreateOrGet(ctx, task); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := repos.NodeAgentTask.Offer(ctx, agentID, ports.NodeAgentTaskOfferSupport{EligibleKinds: []string{one.Kind}}, 2, int(nodeprotocol.MaxSyncBodyBytes), first); err != nil {
		t.Fatal(err)
	}
	spy := &nodeAgentTaskSQLSpy{}
	privateRepo := newNodeAgentTaskRepo(db.Session(&gorm.Session{Logger: spy}), defaultNodeAgentTaskQuota())
	orphan := quarantineTestResult(newTask("task-log-orphan-1", agentID, one.Kind, nil))
	orphan.Result = []byte(secret)
	terminal := quarantineTestResult(one)
	terminal.Result = []byte(secret)
	if err := privateRepo.CompleteBatch(ctx, agentID, []domain.NodeAgentTaskResult{terminal, orphan}, first); err != nil {
		t.Fatal(err)
	}
	if len(spy.messages) != 0 {
		t.Fatalf("receipt transaction emitted SQL tracing: %v", spy.messages)
	}
	// Inject a driver-like error that echoes the private value. The SQL trace
	// stays suppressed, the returned message is safe, and Unwrap retains cause.
	cause := errors.New(secret)
	if err := db.Callback().Create().Before("gorm:create").Register("test:private_receipt_error", func(tx *gorm.DB) {
		if tx.Statement.Table == (nodeAgentTaskResultQuarantineRow{}).TableName() {
			_ = tx.AddError(cause)
		}
	}); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Callback().Create().Remove("test:private_receipt_error") }()
	orphan.TaskID = "task-log-orphan-2"
	err := privateRepo.CompleteBatch(ctx, agentID, []domain.NodeAgentTaskResult{orphan}, first)
	if !errors.Is(err, cause) || strings.Contains(err.Error(), secret) || len(spy.messages) != 0 {
		t.Fatal("failed quarantine insert leaked evidence or lost cause")
	}
	if err := db.Callback().Update().Before("gorm:update").Register("test:private_terminal_error", func(tx *gorm.DB) {
		if tx.Statement.Table == (nodeAgentTaskRow{}).TableName() {
			_ = tx.AddError(cause)
		}
	}); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Callback().Update().Remove("test:private_terminal_error") }()
	terminal = quarantineTestResult(two)
	terminal.Result = []byte(secret)
	err = privateRepo.CompleteBatch(ctx, agentID, []domain.NodeAgentTaskResult{terminal}, first)
	if !errors.Is(err, cause) || strings.Contains(err.Error(), secret) || len(spy.messages) != 0 {
		t.Fatal("failed terminal update leaked evidence or lost cause")
	}
}
