package sqlstore

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"

	nodeprotocol "github.com/KazuhaHub/passwall-node/protocol"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

func expiryTestTask(t *testing.T, id, agentID string, deadline int64) *domain.NodeAgentTask {
	t.Helper()
	task := newTask(id, agentID, "reality_probe.v1", []byte("input"))
	task.CreatedAt = time.UnixMilli(1000).UTC()
	if deadline != 0 {
		task.Lifecycle = taskLifecycleSnapshot(t, 1000, deadline, domain.DefaultNodeTaskLifecyclePolicy())
	}
	return task
}

func expiryTestResult(task *domain.NodeAgentTask) domain.NodeAgentTaskResult {
	result := quarantineTestResult(task)
	if task.Lifecycle != nil {
		result.NotAfterMS = task.Lifecycle.NotAfterMS
	}
	return result
}

func TestNodeAgentTaskExpiryDeadlineAndCapabilityBoundaries(t *testing.T) {
	const nowMS int64 = 10_000
	for _, test := range []struct {
		name           string
		deadline       int64
		supportsExpiry bool
		wantOffered    bool
		wantClosed     bool
	}{
		{"one_ms_before", nowMS - 1, true, false, true},
		{"exact_deadline", nowMS, true, false, true},
		{"one_ms_after", nowMS + 1, true, true, false},
		{"missing_expiry_capability", nowMS + 1, false, false, false},
		{"legacy_without_expiry_capability", 0, false, true, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			repos, _ := newTaskTestReposWithQuota(t, nodeAgentTaskQuota{MaxActiveTasks: 1, MaxActiveArgBytes: 1024})
			const agentID = "agt_expiry_boundary"
			createTaskTestAgent(t, repos, agentID, 960)
			task := expiryTestTask(t, "task-expiry-boundary", agentID, test.deadline)
			if _, _, err := repos.NodeAgentTask.CreateOrGet(t.Context(), task); err != nil {
				t.Fatal(err)
			}
			support := ports.NodeAgentTaskOfferSupport{EligibleKinds: []string{task.Kind}, SupportsExpiry: test.supportsExpiry}
			at := time.UnixMilli(nowMS).UTC()
			offered, err := repos.NodeAgentTask.Offer(t.Context(), agentID, support, 64, int(nodeprotocol.MaxSyncBodyBytes), at)
			if err != nil || (len(offered) == 1) != test.wantOffered {
				t.Fatalf("offer = (%+v,%v), want offered %v", offered, err, test.wantOffered)
			}
			stored, err := repos.NodeAgentTask.GetByTaskID(t.Context(), task.TaskID)
			if err != nil || (stored.DispatchClosedAt != nil) != test.wantClosed || stored.Status.Terminal() || stored.CompletedAt != nil || stored.ResultOK != nil {
				t.Fatalf("closure must not create a terminal result: (%+v,%v)", stored, err)
			}
			if test.wantClosed && (stored.DispatchClosedReason != "task_authorization_expired" || !stored.DispatchClosedAt.Equal(at) || stored.OfferCount != 0) {
				t.Fatalf("expiry closure metadata = %+v", stored)
			}
			if task.Lifecycle != nil && *stored.Lifecycle != *task.Lifecycle {
				t.Fatal("offer/expiry rewrote original lifecycle")
			}
			if _, _, err := repos.NodeAgentTask.CreateOrGet(t.Context(), expiryTestTask(t, "task-expiry-extra", agentID, 0)); !errors.Is(err, domain.ErrResourceExhausted) {
				t.Fatalf("nonterminal closed task released quota: %v", err)
			}
		})
	}
}

func TestNodeAgentTaskExpiryScansPastResponseWindow(t *testing.T) {
	const nowMS int64 = 10_000
	for _, mode := range []string{"expired_prefix", "missing_expiry_prefix", "unsupported_kind_prefix", "full_response_then_expired"} {
		t.Run(mode, func(t *testing.T) {
			repos := newTaskTestRepos(t)
			const agentID = "agt_expiry_scan"
			createTaskTestAgent(t, repos, agentID, 961)
			support := ports.NodeAgentTaskOfferSupport{EligibleKinds: []string{"reality_probe.v1"}, SupportsExpiry: mode != "missing_expiry_prefix"}
			for i := 0; i < nodeprotocol.MaxTasksPerResponse; i++ {
				deadline := nowMS + 5000
				if mode == "expired_prefix" {
					deadline = nowMS
				}
				if mode == "full_response_then_expired" || mode == "unsupported_kind_prefix" {
					deadline = 0
				}
				task := expiryTestTask(t, fmt.Sprintf("task-a-prefix-%03d", i), agentID, deadline)
				if mode == "unsupported_kind_prefix" {
					task.Kind = "agent_upgrade.v1"
					task.InputSHA256 = nodeprotocol.ComputeTaskInputSHA256(task.Kind, task.Args)
				}
				if _, _, err := repos.NodeAgentTask.CreateOrGet(t.Context(), task); err != nil {
					t.Fatal(err)
				}
			}
			deadline := nowMS + 5000
			if mode == "missing_expiry_prefix" {
				deadline = 0
			} else if mode == "full_response_then_expired" {
				deadline = nowMS
			}
			last := expiryTestTask(t, "task-z-tail", agentID, deadline)
			if _, _, err := repos.NodeAgentTask.CreateOrGet(t.Context(), last); err != nil {
				t.Fatal(err)
			}
			offered, err := repos.NodeAgentTask.Offer(t.Context(), agentID, support, nodeprotocol.MaxTasksPerResponse, int(nodeprotocol.MaxSyncBodyBytes), time.UnixMilli(nowMS))
			if err != nil {
				t.Fatal(err)
			}
			if mode == "full_response_then_expired" {
				stored, err := repos.NodeAgentTask.GetByTaskID(t.Context(), last.TaskID)
				if err != nil || len(offered) != nodeprotocol.MaxTasksPerResponse || stored.DispatchClosedAt == nil || stored.Status != domain.NodeAgentTaskQueued {
					t.Fatalf("filled response stopped later expiry scan = (%+v,%v), offered %d", stored, err, len(offered))
				}
			} else if len(offered) != 1 || offered[0].TaskID != last.TaskID {
				t.Fatalf("prefix starved eligible tail = %+v", offered)
			}
		})
	}
}

func TestNodeAgentTaskExpiryClosesWithoutCapabilitiesOrWireSpaceAndAcceptsLateResults(t *testing.T) {
	repos := newTaskTestRepos(t)
	const agentID = "agt_expiry_late"
	createTaskTestAgent(t, repos, agentID, 962)
	offered := expiryTestTask(t, "task-expiry-a-offered", agentID, 10_000)
	queued := expiryTestTask(t, "task-expiry-b-queued", agentID, 10_000)
	for _, task := range []*domain.NodeAgentTask{offered, queued} {
		if _, _, err := repos.NodeAgentTask.CreateOrGet(t.Context(), task); err != nil {
			t.Fatal(err)
		}
	}
	support := ports.NodeAgentTaskOfferSupport{EligibleKinds: []string{offered.Kind}, SupportsExpiry: true}
	if got, err := repos.NodeAgentTask.Offer(t.Context(), agentID, support, 1, int(nodeprotocol.MaxSyncBodyBytes), time.UnixMilli(9000)); err != nil || len(got) != 1 || got[0].TaskID != offered.TaskID {
		t.Fatalf("initial offer = (%+v,%v)", got, err)
	}
	at := time.UnixMilli(10_000).UTC()
	if got, err := repos.NodeAgentTask.Offer(t.Context(), agentID, ports.NodeAgentTaskOfferSupport{}, 64, 0, at); err != nil || len(got) != 0 {
		t.Fatalf("capless zero-budget expiry = (%+v,%v)", got, err)
	}
	for _, task := range []*domain.NodeAgentTask{offered, queued} {
		stored, err := repos.NodeAgentTask.GetByTaskID(t.Context(), task.TaskID)
		if err != nil || stored.DispatchClosedAt == nil || !stored.DispatchClosedAt.Equal(at) || stored.Status.Terminal() {
			t.Fatalf("open task not closed at deadline = (%+v,%v)", stored, err)
		}
	}
	late := time.UnixMilli(20_000).UTC()
	results := []domain.NodeAgentTaskResult{expiryTestResult(offered), expiryTestResult(queued)}
	if err := repos.NodeAgentTask.CompleteBatch(t.Context(), agentID, results, late); err != nil {
		t.Fatalf("late durable receipt rejected: %v", err)
	}
	storedOffered, _ := repos.NodeAgentTask.GetByTaskID(t.Context(), offered.TaskID)
	storedQueued, _ := repos.NodeAgentTask.GetByTaskID(t.Context(), queued.TaskID)
	if storedOffered.Status != domain.NodeAgentTaskSucceeded || storedOffered.DispatchClosedAt == nil || !storedOffered.CompletedAt.Equal(late) || storedQueued.Status != domain.NodeAgentTaskQueued || storedQueued.ResultOK != nil {
		t.Fatalf("late outcomes = offered %+v, queued %+v", storedOffered, storedQueued)
	}
	q, err := repos.NodeAgentTask.GetQuarantinedResult(t.Context(), agentID, queued.TaskID)
	if err != nil || q.Reason != domain.NodeAgentTaskQuarantineNeverOffered || q.Result.NotAfterMS != queued.Lifecycle.NotAfterMS || !bytes.Contains(q.Payload, []byte(`"not_after_ms":10000`)) {
		t.Fatalf("late never-offered evidence lost deadline: (%+v,%v)", q, err)
	}
	if err := repos.NodeAgentTask.CompleteBatch(t.Context(), agentID, results, late.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	replayed, _ := repos.NodeAgentTask.GetByTaskID(t.Context(), offered.TaskID)
	if !replayed.CompletedAt.Equal(late) || *replayed.Lifecycle != *offered.Lifecycle {
		t.Fatal("terminal replay altered completion or immutable snapshot")
	}
}

func TestNodeAgentTaskExpiryResultDeadlineConflictIsWholeBatchAtomic(t *testing.T) {
	for _, originalDeadline := range []int64{0, 10_000} {
		t.Run(fmt.Sprintf("original_%d", originalDeadline), func(t *testing.T) {
			repos := newTaskTestRepos(t)
			const agentID = "agt_expiry_identity"
			createTaskTestAgent(t, repos, agentID, 963)
			offered := expiryTestTask(t, "task-a-offered", agentID, originalDeadline)
			queued := expiryTestTask(t, "task-b-queued", agentID, 10_000)
			for _, task := range []*domain.NodeAgentTask{offered, queued} {
				if _, _, err := repos.NodeAgentTask.CreateOrGet(t.Context(), task); err != nil {
					t.Fatal(err)
				}
			}
			support := ports.NodeAgentTaskOfferSupport{EligibleKinds: []string{offered.Kind}, SupportsExpiry: true}
			if _, err := repos.NodeAgentTask.Offer(t.Context(), agentID, support, 1, int(nodeprotocol.MaxSyncBodyBytes), time.UnixMilli(9000)); err != nil {
				t.Fatal(err)
			}
			orphan := expiryTestResult(expiryTestTask(t, "task-c-orphan", agentID, 10_000))
			for _, mismatch := range []int64{0, 9999, 10_001} {
				if mismatch == originalDeadline {
					continue
				}
				bad := expiryTestResult(offered)
				bad.NotAfterMS = mismatch
				if err := repos.NodeAgentTask.CompleteBatch(t.Context(), agentID, []domain.NodeAgentTaskResult{bad, expiryTestResult(queued), orphan}, time.UnixMilli(20_000)); !errors.Is(err, domain.ErrConflict) {
					t.Fatalf("deadline %d mismatch accepted: %v", mismatch, err)
				}
				stored, _ := repos.NodeAgentTask.GetByTaskID(t.Context(), offered.TaskID)
				neighbor, _ := repos.NodeAgentTask.GetByTaskID(t.Context(), queued.TaskID)
				if stored.Status != domain.NodeAgentTaskOffered || neighbor.DispatchClosedAt != nil {
					t.Fatal("deadline conflict partially completed or closed neighboring task")
				}
				for _, id := range []string{queued.TaskID, orphan.TaskID} {
					if _, err := repos.NodeAgentTask.GetQuarantinedResult(t.Context(), agentID, id); !errors.Is(err, domain.ErrNotFound) {
						t.Fatalf("deadline conflict retained evidence %q: %v", id, err)
					}
				}
			}
			good := expiryTestResult(offered)
			if err := repos.NodeAgentTask.CompleteBatch(t.Context(), agentID, []domain.NodeAgentTaskResult{good}, time.UnixMilli(20_000)); err != nil {
				t.Fatal(err)
			}
			good.NotAfterMS = originalDeadline + 1
			if err := repos.NodeAgentTask.CompleteBatch(t.Context(), agentID, []domain.NodeAgentTaskResult{good}, time.UnixMilli(20_001)); !errors.Is(err, domain.ErrConflict) {
				t.Fatalf("terminal replay ignored original deadline: %v", err)
			}
		})
	}
}

func TestNodeAgentTaskExpiryRestoredQuarantineStillBindsDeadline(t *testing.T) {
	repos, db := newTaskTestReposWithQuota(t, defaultNodeAgentTaskQuota())
	const agentID = "agt_expiry_restore"
	createTaskTestAgent(t, repos, agentID, 964)
	original := expiryTestTask(t, "task-expiry-restored", agentID, 10_000)
	result := expiryTestResult(original)
	first := time.UnixMilli(5000).UTC()
	if err := repos.NodeAgentTask.CompleteBatch(t.Context(), agentID, []domain.NodeAgentTaskResult{result}, first); err != nil {
		t.Fatal(err)
	}
	restored := expiryTestTask(t, original.TaskID, agentID, 10_001)
	restored.Status, restored.OfferCount, restored.FirstOfferedAt = domain.NodeAgentTaskOffered, 1, &first
	if err := db.Create(nodeAgentTaskFromDomain(restored)).Error; err != nil {
		t.Fatal(err)
	}
	support := ports.NodeAgentTaskOfferSupport{EligibleKinds: []string{original.Kind}, SupportsExpiry: true}
	if offered, err := repos.NodeAgentTask.Offer(t.Context(), agentID, support, 64, int(nodeprotocol.MaxSyncBodyBytes), time.UnixMilli(6000)); err != nil || len(offered) != 0 {
		t.Fatalf("evidence fence allowed restored deadline to dispatch: (%+v,%v)", offered, err)
	}
	for _, incoming := range []domain.NodeAgentTaskResult{result, expiryTestResult(restored)} {
		if err := repos.NodeAgentTask.CompleteBatch(t.Context(), agentID, []domain.NodeAgentTaskResult{incoming}, time.UnixMilli(6000)); !errors.Is(err, domain.ErrConflict) {
			t.Fatalf("restored input/evidence deadline mismatch accepted: %v", err)
		}
	}
	stored, _ := repos.NodeAgentTask.GetByTaskID(t.Context(), original.TaskID)
	q, err := repos.NodeAgentTask.GetQuarantinedResult(t.Context(), agentID, original.TaskID)
	if stored.Status != domain.NodeAgentTaskOffered || stored.DispatchClosedAt != nil || err != nil || q.Result.NotAfterMS != original.Lifecycle.NotAfterMS || !q.LastSeenAt.Equal(first) {
		t.Fatalf("conflict mutated restored task/evidence = %+v, (%+v,%v)", stored, q, err)
	}
}

func TestNodeAgentTaskExpiryExactWireJSONBudgetIncludesMilliseconds(t *testing.T) {
	repos := newTaskTestRepos(t)
	const agentID = "agt_expiry_json"
	createTaskTestAgent(t, repos, agentID, 965)
	task := expiryTestTask(t, "task-expiry-json", agentID, 1_700_000_000_123)
	if _, _, err := repos.NodeAgentTask.CreateOrGet(t.Context(), task); err != nil {
		t.Fatal(err)
	}
	wire := nodeprotocol.Task{ID: task.TaskID, Kind: task.Kind, InputSHA256: task.InputSHA256, Args: task.Args, NotAfterMS: task.Lifecycle.NotAfterMS}
	encoded, err := json.Marshal([]nodeprotocol.Task{wire})
	if err != nil {
		t.Fatal(err)
	}
	support := ports.NodeAgentTaskOfferSupport{EligibleKinds: []string{task.Kind}, SupportsExpiry: true}
	for _, budget := range []int{len(encoded) - 1, len(encoded)} {
		offered, err := repos.NodeAgentTask.Offer(t.Context(), agentID, support, 64, budget, time.UnixMilli(5000))
		if err != nil || (len(offered) == 1) != (budget == len(encoded)) {
			t.Fatalf("exact deadline JSON budget %d = (%+v,%v), encoded %d", budget, offered, err, len(encoded))
		}
		stored, _ := repos.NodeAgentTask.GetByTaskID(t.Context(), task.TaskID)
		if budget < len(encoded) && (stored.Status != domain.NodeAgentTaskQueued || stored.OfferCount != 0) {
			t.Fatal("task marked offered before complete deadline JSON fit")
		}
	}
}

func TestNodeAgentTaskExpiryLegacyQuarantinePreservesOriginalBytesAndHash(t *testing.T) {
	repos, db := newTaskTestReposWithQuota(t, defaultNodeAgentTaskQuota())
	const agentID = "agt_expiry_old_q"
	createTaskTestAgent(t, repos, agentID, 966)
	// This is the pre-expiry canonical durable schema, deliberately not encoded
	// through today's shared helper: adding omitempty NotAfterMS must not alter it.
	const payload = `{"id":"task-legacy-evidence","kind":"reality_probe.v1","input_sha256":"7578595b14afff1b40892f0b8a74b1b6c271fa08fd26cd90f2f46250a9bdb920","ok":true,"result":"AP94"}`
	hash := sha256.Sum256([]byte(payload))
	first := time.UnixMilli(5000).UTC()
	row := nodeAgentTaskResultQuarantineRow{AgentID: agentID, TaskID: "task-legacy-evidence", Payload: []byte(payload), PayloadSHA256: hex.EncodeToString(hash[:]), Reason: "unknown_task", FirstSeenAt: first, LastSeenAt: first}
	if err := db.Create(&row).Error; err != nil {
		t.Fatal(err)
	}
	q, err := repos.NodeAgentTask.GetQuarantinedResult(t.Context(), agentID, row.TaskID)
	if err != nil || q.Result.NotAfterMS != 0 || string(q.Payload) != payload || q.PayloadSHA256 != row.PayloadSHA256 {
		t.Fatalf("pre-expiry quarantine changed = (%+v,%v)", q, err)
	}
	if err := repos.NodeAgentTask.CompleteBatch(t.Context(), agentID, []domain.NodeAgentTaskResult{q.Result}, first.Add(time.Second)); err != nil {
		t.Fatalf("old evidence exact replay rejected: %v", err)
	}
	replayed, _ := repos.NodeAgentTask.GetQuarantinedResult(t.Context(), agentID, row.TaskID)
	if string(replayed.Payload) != payload || replayed.PayloadSHA256 != row.PayloadSHA256 || !replayed.FirstSeenAt.Equal(first) {
		t.Fatal("legacy evidence replay rewrote original disk payload/hash")
	}
}

func TestNodeAgentTaskExpiryCorruptOverLimitBacklogFailsClosed(t *testing.T) {
	repos, db := newTaskTestReposWithQuota(t, defaultNodeAgentTaskQuota())
	const agentID = "agt_expiry_overlimit"
	createTaskTestAgent(t, repos, agentID, 967)
	// Direct corruption/restore bypasses admission; never silently scan an
	// unbounded backlog or partially close it while hiding the excess rows.
	for i := 0; i <= int(defaultMaxActiveNodeAgentTasks); i++ {
		task := expiryTestTask(t, fmt.Sprintf("task-overlimit-%03d", i), agentID, 10_000)
		task.Status = domain.NodeAgentTaskQueued
		if err := db.Create(nodeAgentTaskFromDomain(task)).Error; err != nil {
			t.Fatal(err)
		}
	}
	if offered, err := repos.NodeAgentTask.Offer(t.Context(), agentID, ports.NodeAgentTaskOfferSupport{}, 64, 0, time.UnixMilli(10_000)); err == nil || offered != nil {
		t.Fatalf("corrupt oversized backlog did not fail closed: (%+v,%v)", offered, err)
	}
	var closed int64
	if err := db.Model(&nodeAgentTaskRow{}).Where("agent_id = ? AND dispatch_closed_at IS NOT NULL", agentID).Count(&closed).Error; err != nil || closed != 0 {
		t.Fatalf("over-limit failure partially closed %d tasks: %v", closed, err)
	}
}

func TestNodeAgentTaskExpiryOfferRequiresPositiveControlPlaneTime(t *testing.T) {
	repos := newTaskTestRepos(t)
	const agentID = "agt_expiry_bad_time"
	createTaskTestAgent(t, repos, agentID, 968)
	task := expiryTestTask(t, "task-expiry-time", agentID, 10_000)
	if _, _, err := repos.NodeAgentTask.CreateOrGet(t.Context(), task); err != nil {
		t.Fatal(err)
	}
	for _, at := range []time.Time{{}, time.UnixMilli(-1), time.UnixMilli(0)} {
		if offered, err := repos.NodeAgentTask.Offer(t.Context(), agentID, ports.NodeAgentTaskOfferSupport{}, 64, 0, at); !errors.Is(err, domain.ErrValidation) || offered != nil {
			t.Fatalf("nonpositive offered time accepted: (%+v,%v)", offered, err)
		}
	}
	stored, err := repos.NodeAgentTask.GetByTaskID(t.Context(), task.TaskID)
	if err != nil || stored.DispatchClosedAt != nil || stored.Status != domain.NodeAgentTaskQueued || stored.OfferCount != 0 {
		t.Fatalf("invalid control-plane time altered task = (%+v,%v)", stored, err)
	}
}
