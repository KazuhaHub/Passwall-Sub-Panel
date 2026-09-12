package nodeagentupgrade

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/KazuhaHub/passwall-sub-panel/internal/adapters/sqlstore"
	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/pkg/idgen"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

type upgradeFixture struct {
	service *Service
	repos   ports.Repos
	panel   *domain.XUIPanel
	agent   *domain.NodeAgent
	now     time.Time
}

func newUpgradeFixture(t *testing.T) *upgradeFixture {
	t.Helper()
	db, err := sqlstore.Open("sqlite", filepath.Join(t.TempDir(), "upgrade.db"))
	if err != nil {
		t.Fatal(err)
	}
	connection, _ := db.DB()
	t.Cleanup(func() { _ = connection.Close() })
	if err := sqlstore.EnsureSchema(db); err != nil {
		t.Fatal(err)
	}
	repos := sqlstore.NewRepos(db)
	panel := &domain.XUIPanel{Kind: domain.PanelKindPSP, Name: "upgrade fixture", URL: "psp://agt_upgrade"}
	agent := &domain.NodeAgent{AgentID: "agt_upgrade", CredentialSHA256: strings.Repeat("a", 64)}
	if err := repos.NativeAgentProvisioning.Create(context.Background(), panel, agent); err != nil {
		t.Fatal(err)
	}
	ids, err := idgen.NewTaskIDMinter()
	if err != nil {
		t.Fatal(err)
	}
	fixture := &upgradeFixture{repos: repos, panel: panel, agent: agent, now: time.Now().UTC().Truncate(time.Millisecond)}
	fixture.service, err = New(Options{Panels: repos.XUIPanel, Agents: repos.NodeAgent, Tasks: repos.NodeAgentTask, Settings: repos.Settings, IDs: ids, Now: func() time.Time { return fixture.now }})
	if err != nil {
		t.Fatal(err)
	}
	return fixture
}

var validUpgradeRequest = Request{Version: "v0.0.1-beta3", ExpectedVersion: "v0.0.1-beta2"}

const upgradeRequestKey = "upgrade-request-0001"

func TestUpgradeRequestRequiresAnExactNewerRelease(t *testing.T) {
	for _, tc := range []struct {
		target   string
		expected string
		valid    bool
	}{
		{"v0.0.1-beta3", "v0.0.1-beta2", true},
		{"v0.0.1-beta.10", "v0.0.1-beta.9", true},
		{"v0.0.1", "v0.0.1-beta3", true},
		{"v0.0.2-beta.1", "v0.0.1", true},
		{"v100000000000000000000.0.0", "v99999999999999999999.0.0", true},
		{"v0.0.1-beta2", "v0.0.1-beta2", false},
		{"v0.0.1-beta2", "v0.0.1-beta3", false},
		{"v0.0.1-beta.9", "v0.0.1-beta.10", false},
		{"v0.0.1-beta3", "v0.0.1", false},
		{"v0.0.1", "v0.0.2-beta.1", false},
		{"v0.1", "v0.0.1", false},
		{"v0.0.2+build", "v0.0.1", false},
		{"latest", "v0.0.1", false},
	} {
		err := (Request{Version: tc.target, ExpectedVersion: tc.expected}).Validate()
		if tc.valid && err != nil || !tc.valid && !errors.Is(err, domain.ErrValidation) {
			t.Fatalf("target=%s expected=%s valid=%t error=%v", tc.target, tc.expected, tc.valid, err)
		}
	}
}

func TestUpgradeRequestFreezesPolicyDeadlineAndReplaysAtCapacity(t *testing.T) {
	f := newUpgradeFixture(t)
	ctx := context.Background()
	status, created, err := f.service.Request(ctx, f.panel.ID, validUpgradeRequest, upgradeRequestKey)
	if err != nil || !created || status.Status != domain.NodeAgentTaskQueued || status.UpgradeState != "queued" || status.NotAfterMS != f.now.Add(10*time.Minute).UnixMilli() {
		t.Fatalf("request status=%+v created=%v err=%v", status, created, err)
	}
	original, err := f.repos.NodeAgentTask.GetByTaskID(ctx, status.TaskID)
	if err != nil {
		t.Fatal(err)
	}
	if original.Lifecycle.Policy != domain.DefaultNodeTaskLifecyclePolicy() || original.IdempotencyKeySHA256 == nil || *original.IdempotencyKeySHA256 == upgradeRequestKey {
		t.Fatalf("unfrozen policy or raw idempotency key: %+v", original)
	}
	settings, err := f.repos.Settings.Load(ctx, ports.UISettings{})
	if err != nil {
		t.Fatal(err)
	}
	settings.NodeTaskOfflineReconcileDays = 45
	settings.NodeTaskBackupRestoreDays = 60
	settings.NodeTaskResultRetentionDays = 120
	if err := f.repos.Settings.Save(ctx, settings); err != nil {
		t.Fatal(err)
	}
	f.now = f.now.Add(11 * time.Minute)
	agentSupport := ports.NodeAgentTaskOfferSupport{EligibleKinds: []string{Kind}, SupportsExpiry: true}
	if offered, err := f.repos.NodeAgentTask.Offer(ctx, f.agent.AgentID, agentSupport, 64, 1<<20, f.now); err != nil || len(offered) != 0 {
		t.Fatalf("expired authorization offered: %v %v", offered, err)
	}
	for i := 1; i < 256; i++ {
		key := strings.Repeat("f", 16) + time.Duration(i).String()
		if _, _, err := f.service.Request(ctx, f.panel.ID, validUpgradeRequest, key); err != nil {
			t.Fatalf("fill quota at %d: %v", i, err)
		}
	}
	if _, _, err := f.service.Request(ctx, f.panel.ID, validUpgradeRequest, "upgrade-over-quota-0001"); !errors.Is(err, domain.ErrResourceExhausted) {
		t.Fatalf("quota error=%v", err)
	}
	replayed, created, err := f.service.Request(ctx, f.panel.ID, validUpgradeRequest, upgradeRequestKey)
	if err != nil || created || replayed.TaskID != status.TaskID || replayed.NotAfterMS != status.NotAfterMS || replayed.UpgradeState != "dispatch_closed" {
		t.Fatalf("replay changed authorization: %+v %v %v", replayed, created, err)
	}
	after, err := f.repos.NodeAgentTask.GetByTaskID(ctx, status.TaskID)
	if err != nil || *after.Lifecycle != *original.Lifecycle || after.Status != domain.NodeAgentTaskQueued || after.DispatchClosedReason != "task_authorization_expired" {
		t.Fatalf("policy/deadline/replay drift: %+v %v", after, err)
	}
	changed := validUpgradeRequest
	changed.Version = "v0.0.1-beta4"
	if _, _, err := f.service.Request(ctx, f.panel.ID, changed, upgradeRequestKey); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("key reuse error=%v", err)
	}
}

func TestUpgradeRequestConcurrentSameKeyProducesOneImmutableTask(t *testing.T) {
	f := newUpgradeFixture(t)
	var wait sync.WaitGroup
	type outcome struct {
		status  *Status
		created bool
		err     error
	}
	results := make(chan outcome, 32)
	for i := 0; i < 32; i++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			status, created, err := f.service.Request(context.Background(), f.panel.ID, validUpgradeRequest, upgradeRequestKey)
			results <- outcome{status, created, err}
		}()
	}
	wait.Wait()
	close(results)
	id := ""
	createdCount := 0
	for result := range results {
		if result.err != nil {
			t.Fatal(result.err)
		}
		if id == "" {
			id = result.status.TaskID
		}
		if result.status.TaskID != id {
			t.Fatal("same request minted multiple stored identities")
		}
		if result.created {
			createdCount++
		}
	}
	if createdCount != 1 {
		t.Fatalf("created tasks=%d", createdCount)
	}
}

func TestUpgradeStatusRequiresTypedReceiptAndFreshPostReceiptObservation(t *testing.T) {
	f := newUpgradeFixture(t)
	ctx := context.Background()
	status, _, err := f.service.Request(ctx, f.panel.ID, validUpgradeRequest, upgradeRequestKey)
	if err != nil {
		t.Fatal(err)
	}
	task, _ := f.repos.NodeAgentTask.GetByTaskID(ctx, status.TaskID)
	task.Status = domain.NodeAgentTaskSucceeded
	ok := true
	task.ResultOK = &ok
	completed := f.now
	task.CompletedAt = &completed
	panel := *f.panel
	agent := *f.agent
	seen := f.now
	agent.LastSeen = &seen
	panel.VersionCheckedAt = &seen
	panel.PanelVersion = validUpgradeRequest.Version + " (abcdef0)"
	validResult := Result{Version: validUpgradeRequest.Version, PreviousVersion: validUpgradeRequest.ExpectedVersion, BinarySHA256: strings.Repeat("b", 64), Restarted: true}
	encode := func(result Result) []byte { raw, _ := json.Marshal(result); return raw }
	task.Result = encode(validResult)
	for _, tc := range []struct {
		name, want string
		change     func(*domain.NodeAgentTask, *domain.XUIPanel, *domain.NodeAgent)
	}{
		{"verified", "verified", func(*domain.NodeAgentTask, *domain.XUIPanel, *domain.NodeAgent) {}},
		{"missing receipt", "manual_attention", func(task *domain.NodeAgentTask, _ *domain.XUIPanel, _ *domain.NodeAgent) { task.Result = nil }},
		{"previous mismatch", "manual_attention", func(task *domain.NodeAgentTask, _ *domain.XUIPanel, _ *domain.NodeAgent) {
			result := validResult
			result.PreviousVersion = "v0.0.1-beta1"
			task.Result = encode(result)
		}},
		{"no restart", "manual_attention", func(task *domain.NodeAgentTask, _ *domain.XUIPanel, _ *domain.NodeAgent) {
			result := validResult
			result.Restarted = false
			task.Result = encode(result)
		}},
		{"bad digest", "manual_attention", func(task *domain.NodeAgentTask, _ *domain.XUIPanel, _ *domain.NodeAgent) {
			result := validResult
			result.BinarySHA256 = strings.Repeat("B", 64)
			task.Result = encode(result)
		}},
		{"unknown receipt fields", "manual_attention", func(task *domain.NodeAgentTask, _ *domain.XUIPanel, _ *domain.NodeAgent) {
			task.Result = append(task.Result[:len(task.Result)-1], []byte(`,"secret":"private"}`)...)
		}},
		{"duplicate receipt fields", "manual_attention", func(task *domain.NodeAgentTask, _ *domain.XUIPanel, _ *domain.NodeAgent) {
			task.Result = append(task.Result[:len(task.Result)-1], []byte(`,"restarted":true}`)...)
		}},
		{"uppercase receipt fields", "manual_attention", func(task *domain.NodeAgentTask, _ *domain.XUIPanel, _ *domain.NodeAgent) {
			task.Result = []byte(strings.Replace(string(task.Result), `"restarted"`, `"RESTARTED"`, 1))
		}},
		{"observed old version", "awaiting_observation", func(_ *domain.NodeAgentTask, panel *domain.XUIPanel, _ *domain.NodeAgent) {
			panel.PanelVersion = validUpgradeRequest.ExpectedVersion
		}},
		{"stale heartbeat", "awaiting_observation", func(_ *domain.NodeAgentTask, _ *domain.XUIPanel, agent *domain.NodeAgent) {
			old := f.now.Add(-121 * time.Second)
			agent.LastSeen = &old
		}},
		{"stale version", "awaiting_observation", func(_ *domain.NodeAgentTask, panel *domain.XUIPanel, _ *domain.NodeAgent) {
			old := f.now.Add(-121 * time.Second)
			panel.VersionCheckedAt = &old
		}},
		{"future observation", "awaiting_observation", func(_ *domain.NodeAgentTask, panel *domain.XUIPanel, agent *domain.NodeAgent) {
			future := f.now.Add(time.Millisecond)
			agent.LastSeen = &future
			panel.VersionCheckedAt = &future
		}},
		{"before receipt", "awaiting_observation", func(_ *domain.NodeAgentTask, panel *domain.XUIPanel, agent *domain.NodeAgent) {
			old := f.now.Add(-time.Millisecond)
			agent.LastSeen = &old
			panel.VersionCheckedAt = &old
		}},
		{"indeterminate", "manual_attention", func(task *domain.NodeAgentTask, _ *domain.XUIPanel, _ *domain.NodeAgent) {
			task.Status = domain.NodeAgentTaskIndeterminate
		}},
		{"failed", "failed", func(task *domain.NodeAgentTask, _ *domain.XUIPanel, _ *domain.NodeAgent) {
			task.Status = domain.NodeAgentTaskFailed
			task.ResultError = "private raw error"
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			copyTask := *task
			copyTask.Result = append([]byte(nil), task.Result...)
			copyPanel := panel
			copyAgent := agent
			tc.change(&copyTask, &copyPanel, &copyAgent)
			got, err := f.service.status(&copyTask, &copyPanel, &copyAgent, f.now)
			if err != nil || got.UpgradeState != tc.want {
				t.Fatalf("state=%+v err=%v", got, err)
			}
			wire, _ := json.Marshal(got)
			if strings.Contains(string(wire), "private") {
				t.Fatal("status leaked private evidence")
			}
		})
	}
}

func TestUpgradeRequestStrictShapeAndOwnerIsolation(t *testing.T) {
	for _, payload := range []string{`null`, `[]`, `{}`, `{"version":"latest","expected_version":"v0.0.1-beta2"}`, `{"version":"v0.0.1-beta3","expected_version":"v0.0.1-beta2","url":"https://evil.test"}`, `{"version":"v0.0.1-beta3","version":"v0.0.1-beta4","expected_version":"v0.0.1-beta2"}`, `{"VERSION":"v0.0.1-beta3","expected_version":"v0.0.1-beta2"}`, `{"version":"v0.0.1-beta3","expected_version":null}`, `{"version":"v0.0.1-beta.01","expected_version":"v0.0.1-beta2"}`, `{"version":"v0.0.1-beta3","expected_version":"v0.0.1-beta2"} {}`} {
		if _, err := DecodeRequest([]byte(payload)); !errors.Is(err, domain.ErrValidation) {
			t.Fatalf("accepted shape %s: %v", payload, err)
		}
	}
	f := newUpgradeFixture(t)
	ctx := context.Background()
	status, _, err := f.service.Request(ctx, f.panel.ID, validUpgradeRequest, upgradeRequestKey)
	if err != nil {
		t.Fatal(err)
	}
	otherPanel := &domain.XUIPanel{Kind: domain.PanelKindPSP, Name: "other", URL: "psp://agt_other"}
	otherAgent := &domain.NodeAgent{AgentID: "agt_other", CredentialSHA256: strings.Repeat("c", 64)}
	if err := f.repos.NativeAgentProvisioning.Create(ctx, otherPanel, otherAgent); err != nil {
		t.Fatal(err)
	}
	if _, err := f.service.Get(ctx, otherPanel.ID, status.TaskID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("foreign task read error=%v", err)
	}
	if _, _, err := f.service.Request(ctx, f.panel.ID, validUpgradeRequest, "secret key with whitespace"); !errors.Is(err, domain.ErrValidation) {
		t.Fatalf("bad key error=%v", err)
	}
	broken, _ := f.repos.NodeAgentTask.GetByTaskID(ctx, status.TaskID)
	broken.InputSHA256 = strings.Repeat("d", 64)
	if _, err := f.service.status(broken, f.panel, f.agent, f.now); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("corrupt identity accepted: %v", err)
	}
}
