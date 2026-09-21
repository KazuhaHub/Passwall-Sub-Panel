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

	nodeprotocol "github.com/KazuhaHub/passwall-protocol/protocol"

	"github.com/KazuhaHub/passwall-sub-panel/internal/adapters/sqlstore"
	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/pkg/idgen"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
	"github.com/KazuhaHub/passwall-sub-panel/internal/version"
)

type upgradeFixture struct {
	service *Service
	repos   ports.Repos
	panel   *domain.XUIPanel
	agent   *domain.NodeAgent
	now     time.Time
}

func newUpgradeFixture(t *testing.T) *upgradeFixture {
	return newUpgradeFixtureWithObservation(t, true)
}

func newUpgradeFixtureWithObservation(t *testing.T, observed bool) *upgradeFixture {
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
	observedAt := time.Now().UTC().Truncate(time.Millisecond)
	agent := &domain.NodeAgent{
		AgentID: "agt_upgrade", CredentialSHA256: strings.Repeat("a", 64),
	}
	if observed {
		agent.ObservedProtocolVersion = nodeprotocol.ProtocolVersion1
		agent.ObservedCapabilities = nodeprotocol.AgentUpgradeCapabilities()
		agent.ProtocolObservedAt = &observedAt
	}
	// THE EDGE IS PART OF THE FIXTURE, because it is now part of the question.
	// Admission requires the requested from→to path to be a VERIFIED one, so a
	// fixture that asks for an upgrade must also say which edge it is asking
	// along — otherwise every case here would be measuring the refusal, which
	// has its own case below.
	//
	// NO EDGES TO INSTALL. The fixture used to record two, because the key-reuse
	// case needs a SECOND request that IS admitted and nothing was admitted along
	// a pair nobody had walked. Admission follows the decision now, so a
	// compatible target is admitted without anything recorded here.

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

var validUpgradeRequest = Request{Version: "4.0.1", ExpectedVersion: "4.0.0"}

const upgradeRequestKey = "upgrade-request-0001"

// THE TARGET IS AN EXACT RELEASE; THE VERSION IT REPLACES IS WHATEVER THE NODE
// REPORTS.
//
// The request used to require BOTH to parse as product versions, and to require
// the target to order above the current one. Those two rules were about the
// version SCHEME rather than about the upgrade: they existed because this project
// deleted its legacy naming, so a node still reporting `v0.0.1-beta9` handed the
// panel a string it could not parse — and a node whose own version the panel
// cannot read became impossible to move. Both are gone.
//
// WHAT REPLACES THEM IS NOT A WEAKER VERSION RULE BUT A DIFFERENT ONE. The target
// must be a release version, because the node derives its download address from it
// and the checksum manifest names it; the expected version is an OPAQUE IDENTITY
// that has to match what the node believes it is, which the node checks itself.
// That is also why order is no longer checked: an operator picking an older
// release is making an explicit choice, and the rules that remain — the artifact's
// signature, its checksum, and the downloaded binary reporting exactly the
// requested version — are what stop that choice from installing something else.
func TestTheTargetIsExactAndTheReplacedVersionIsOpaque(t *testing.T) {
	for _, tc := range []struct {
		target   string
		expected string
		valid    bool
	}{
		{"4.0.1", "4.0.0", true},
		{"4.0.0.1", "4.0.0", true}, // a rebuild of the running release is an upgrade
		{"102.1.0", "102.0.3", true},
		{"4.1.0", "4.0.0.9", true},
		// THE POINT OF REMOVING THE TWO RULES: a node on the replaced scheme has a
		// version this build cannot parse, and it must still be movable.
		{"4.0.1", "v0.0.1-beta9", true},
		{"4.0.1", "v0.0.1-beta12", true},
		{"4.0.1", "v3.9.2", true},
		{"4.0.0", "v4.0.0", true},
		// AND NOT-NEWER IS NO LONGER A REFUSAL. This is deliberate: the panel does
		// not decide what an operator may install, and the ordering rule answered
		// wrongly for exactly the nodes it could not parse.
		{"4.0.0", "4.0.1", true},
		{"4.0.0", "4.99.99", true},
		// A NO-OP IS STILL REFUSED, by string identity rather than by ordering: the
		// node would download, verify and reinstall the release it is already on.
		{"4.0.0", "4.0.0", false},
		{"v0.0.1-beta9", "v0.0.1-beta9", false},
		// THE TARGET IS STILL EXACT. It has to be: the node builds its download
		// address from it, so a target that is not a release version cannot be
		// fetched at all.
		{"4.0", "4.0.0", false},
		{"04.0.0", "4.0.0", false},
		{"4.0.0+build", "4.0.0", false},
		{"latest", "4.0.0", false},
		{"release/4.0.0", "4.0.0", false},
		{"v0.0.1-beta3", "v0.0.1-beta2", false},
		{"v0.0.1-beta.10", "v0.0.1-beta.9", false},
		{"v0.0.1", "v0.0.1-beta3", false},
		{"v4.0.0", "4.0.0", false},
		{"v1.0.0", "v0.0.1", false},
		// An expected version that is absent cannot match what the node believes it
		// is, so the request is refused here rather than at the node.
		{"4.0.1", "", false},
	} {
		err := validateRequest(Request{Version: tc.target, ExpectedVersion: tc.expected})
		if tc.valid && err != nil || !tc.valid && !errors.Is(err, domain.ErrValidation) {
			t.Fatalf("target=%s expected=%s valid=%t error=%v", tc.target, tc.expected, tc.valid, err)
		}
	}
}

func TestUpgradeRequestRequiresDurableCompatibleAgentObservation(t *testing.T) {
	for _, test := range []struct {
		name         string
		protocol     int
		capabilities []string
		observed     bool
		want         string
	}{
		{name: "never observed", want: "has not been observed"},
		{name: "base sync only", protocol: 1, capabilities: []string{nodeprotocol.CapabilityTaskExecutionV1, nodeprotocol.CapabilityTaskExpiryV1}, observed: true, want: nodeprotocol.TaskCapability(nodeprotocol.TaskKindAgentUpgradeV1)},
		{name: "upgrade kind without task expiry", protocol: 1, capabilities: []string{nodeprotocol.CapabilityTaskExecutionV1, nodeprotocol.TaskCapability(nodeprotocol.TaskKindAgentUpgradeV1)}, observed: true, want: nodeprotocol.CapabilityTaskExpiryV1},
	} {
		t.Run(test.name, func(t *testing.T) {
			f := newUpgradeFixtureWithObservation(t, test.observed)
			if test.observed {
				err := f.repos.NodeAgent.UpdateProtocolObservation(context.Background(), f.agent.AgentID, test.protocol, test.capabilities, f.now)
				if err != nil {
					t.Fatal(err)
				}
			}
			_, _, err := f.service.Request(context.Background(), f.panel.ID, validUpgradeRequest, upgradeRequestKey)
			if !errors.Is(err, domain.ErrValidation) || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("request error = %v, want validation containing %q", err, test.want)
			}
		})
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
	changed.Version = "4.0.2"
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

// AN UPGRADE NO LONGER NEEDS A SEPARATELY REVIEWED EDGE.
//
// It used to, and this fixture measured exactly that: the cases above install a
// beta2→beta3 edge so admission could be measured at all, and this one removed it
// to measure the refusal — including that a NEIGHBOURING edge must not admit a
// different path. Both directions are gone with the gate.
//
// PSP is the source of truth for what is supported. A per-pair review was a second
// gate whose refusal an operator could neither see coming from the row — which read
// "compatible" — nor satisfy without editing a policy document.
func TestUpgradeIsAdmittedForACompatiblePeer(t *testing.T) {
	f := newUpgradeFixture(t)
	// (the edge gate was removed: readiness and admission follow the decision)

	_, created, err := f.service.Request(context.Background(), f.panel.ID, validUpgradeRequest, upgradeRequestKey)
	if err != nil {
		t.Fatalf("a compatible peer was refused for the absence of an edge: %v", err)
	}
	if !created {
		t.Fatal("no task was created for a compatible peer")
	}

	// AND THE SAME HOLDS FOR A PATH NO EDGE EVER DESCRIBED. The old gate refused
	// 4.0.0→4.0.2 because only 4.0.0→4.0.1 was recorded; what decides now is that
	// 4.0.2 is a version this panel's judgement accepts.
	other := Request{Version: "4.0.2", ExpectedVersion: "4.0.0"}
	if _, created, err := f.service.Request(context.Background(), f.panel.ID, other, upgradeRequestKey+"-other-version"); err != nil || !created {
		t.Fatalf("a compatible target was refused for want of a recorded edge: err=%v created=%v", err, created)
	}
}

// A policy in force decides which targets may be requested, and it must decide
// for BOTH entry points: the API handler reaches the service through
// validateRequest, and DecodeRequest — used when a task is read back — goes
// through the same function. A gate placed on one of them would be a gate a
// caller can walk around.
func TestAPolicyInForceRefusesATargetItDoesNotOffer(t *testing.T) {
	// A policy only applies to a build it names, and a `go test` build calls
	// itself "dev" — so the build identity is stamped here the way the release
	// binary stamps it, and restored afterwards.
	previousVersion := version.Version
	t.Cleanup(func() { version.Version = previousVersion })
	version.Version = "4.0.0"

	previousEnforcement := version.PolicyEnforcing()
	t.Cleanup(func() { version.SetPolicyEnforcement(previousEnforcement) })
	install := func(releases ...string) {
		t.Helper()
		// Loading a policy is not the same as letting it decide; this case is
		// about the gate, so it switches enforcement on.
		version.SetPolicyEnforcement(true)
		entries := make([]version.PolicyRelease, 0, len(releases))
		for _, r := range releases {
			entries = append(entries, version.PolicyRelease{
				Version: r, ReleaseTag: version.ProductTagNamespace + r, Scheme: "product", Evidence: []string{"test"},
			})
		}
		version.SetActiveReleasesPolicy(&version.ReleasesPolicy{
			SchemaVersion: 1,
			Revision:      1,
			IssuedAt:      time.Now().UTC().Add(-time.Hour),
			ExpiresAt:     time.Now().UTC().Add(time.Hour),
			AppliesToPSP:  version.PolicyPSPRange{Min: version.Version, Max: version.Version},
			Releases:      entries,
		})
	}
	t.Cleanup(func() { version.SetActiveReleasesPolicy(nil) })

	// No policy: the pre-policy state, unchanged.
	version.SetActiveReleasesPolicy(nil)
	if err := validateRequest(validUpgradeRequest); err != nil {
		t.Fatalf("without a policy the request must still validate: %v", err)
	}

	// A policy that does not list the target refuses it.
	install("4.0.0")
	if err := validateRequest(validUpgradeRequest); !errors.Is(err, domain.ErrValidation) || !strings.Contains(err.Error(), "policy in force offers") {
		t.Fatalf("a policy must refuse a target it does not offer: %v", err)
	}
	// ...including through the decode path, which is the other way in.
	payload, err := json.Marshal(nodeprotocol.AgentUpgradeArgs{Version: validUpgradeRequest.Version, ExpectedVersion: validUpgradeRequest.ExpectedVersion})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := DecodeRequest(payload); !errors.Is(err, domain.ErrValidation) {
		t.Fatalf("DecodeRequest must apply the same gate: %v", err)
	}

	// A policy that lists the target lets it through, so the gate is a filter
	// and not a blanket refusal.
	install("4.0.0", validUpgradeRequest.Version)
	if err := validateRequest(validUpgradeRequest); err != nil {
		t.Fatalf("a policy that offers the target must allow it: %v", err)
	}
}

// A policy reviewed for a different build is installed but not in force here, so
// it must not start refusing requests this build was always allowed to make.
func TestAPolicyForAnotherBuildDoesNotGateThisOne(t *testing.T) {
	previousVersion := version.Version
	t.Cleanup(func() { version.Version = previousVersion })
	version.Version = "4.0.0"

	version.SetActiveReleasesPolicy(&version.ReleasesPolicy{
		SchemaVersion: 1,
		Revision:      1,
		IssuedAt:      time.Now().UTC().Add(-time.Hour),
		ExpiresAt:     time.Now().UTC().Add(time.Hour),
		AppliesToPSP:  version.PolicyPSPRange{Min: "9.0.0", Max: "9.99.99"},
	})
	t.Cleanup(func() { version.SetActiveReleasesPolicy(nil) })

	if err := validateRequest(validUpgradeRequest); err != nil {
		t.Fatalf("a policy for another build must not gate this one: %v", err)
	}
}
