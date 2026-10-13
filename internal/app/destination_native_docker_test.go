//go:build destination_acceptance && linux

package app

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/KazuhaHub/passwall-protocol/protocol"
	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
	"github.com/KazuhaHub/passwall-sub-panel/internal/service/nodeagentupgrade"
	"golang.org/x/sys/unix"
)

const nativeDockerImage = "ghcr.io/kazuhahub/passwall-node:4.0.1.9"

// The daemon, updater, Compose template and registry pull remain production
// paths. This candidate is explicitly unpublished; only the target image is a
// release. Neither a task result nor a readiness document is written by the test.
func TestNativePublishedDockerRollback(t *testing.T) {
	nativeDockerGuard(t)
	f := buildDestinationPolicyFixture(t)
	a, token := f.a, destinationRefreshAdminToken(t, f.a)
	settings, err := a.settings.Load(t.Context(), ports.UISettings{})
	nativeMust(t, err, "read Docker fixture cadence")
	settings.NodePollSeconds, settings.FullReportSeconds = 2, 0
	nativeMust(t, a.settings.Save(t.Context(), settings), "save Docker fixture cadence")
	inbound := nativePort(t)
	f.node.DesiredPort, f.node.ObservedPort, f.node.InboundListen = inbound, inbound, "127.0.0.1"
	nativeMust(t, a.repos.Node.Update(t.Context(), f.node), "configure actual Docker listener")
	a.startDestinationAudit()
	probe := &nativeLegacyProbe{listener: protocol.NewListenerKey(f.node.ID)}
	fault := &nativeRemoteHealthFault{}
	server := httptest.NewTLSServer(fault.wrap(probe.wrap(a.server.Handler)))
	t.Cleanup(server.Close)
	_, address, port, nonce, requests := nativeTarget(t)
	_, controlAddress, controlPort, controlNonce, _ := nativeTarget(t)
	if port == controlPort {
		t.Fatal("Docker control target overlaps the blocked target")
	}
	m := newNativeDockerInstallation(t, server, token, &f)
	caseTest := t
	run := func(name string, work func()) {
		if !t.Run(name, func(child *testing.T) { caseTest = child; defer func() { caseTest = t }(); work() }) {
			t.FailNow()
		}
	}
	request := func(method, path, key string, body, result any, want int) {
		nativeDockerAdmin(caseTest, server, token, method, path, key, body, result, want)
	}
	fold := func() {
		nativeMust(caseTest, a.operationGate.RunRead(caseTest.Context(), a.traffic.PollOnce), "fold Docker traffic through production collector")
	}
	traffic := func(allowed bool) {
		live := m.liveCore(caseTest)
		nativeMust(caseTest, nativeVLESSHost(caseTest.Context(), inbound, controlAddress, controlPort, f.user.UUID, controlNonce, "["+controlAddress+"]"), "Docker independent control before traffic")
		before := requests.Load()
		err := nativeVLESSHost(caseTest.Context(), inbound, address, port, f.user.UUID, nonce, "["+address+"]")
		want := before
		if allowed {
			want++
		}
		if (allowed && err != nil) || (!allowed && err == nil) || requests.Load() != want {
			caseTest.Fatal("Docker real traffic disagreed with actual policy execution")
		}
		nativeMust(caseTest, nativeVLESSHost(caseTest.Context(), inbound, controlAddress, controlPort, f.user.UUID, controlNonce, "["+controlAddress+"]"), "Docker independent control after traffic")
		live()
	}
	var policy destinationPolicyAPIResult
	var originalPolicy *domain.DestAgentPolicy
	var originalAgent *domain.NodeAgent
	var originalClient *domain.PSPClient
	var before nativeRemotePreserved
	var source nativeDockerContainer
	var sourceDigest string
	key := "native-docker-" + m.nonce
	upgradePath := fmt.Sprintf("servers/%d/upgrade-node-agent", f.agent.PanelID)
	input := protocol.AgentUpgradeArgs{Version: nativeRemotePublished, ExpectedVersion: nativeRemoteCandidate}
	converged := func(version string, after time.Time) bool {
		agent, err := a.repos.NodeAgent.GetByAgentID(caseTest.Context(), f.agent.AgentID)
		if err != nil || agent.LastSeen == nil || !agent.LastSeen.After(after) || (originalAgent != nil && (agent.Epoch != originalAgent.Epoch || agent.CredentialSHA256 != originalAgent.CredentialSHA256)) {
			return false
		}
		for _, name := range []domain.NodeAgentStreamName{domain.NodeAgentStreamConfig, domain.NodeAgentStreamRoster, domain.NodeAgentStreamDirectives} {
			stream, err := a.repos.NodeAgent.GetStream(caseTest.Context(), agent.AgentID, name)
			if err != nil || stream == nil || !stream.Converged() || stream.AppliedEpoch != agent.Epoch {
				return false
			}
		}
		s := probe.snapshot()
		if s.CoreState != "running" || !s.ListenerApplied || !s.ConfigBodyKnown {
			return false
		}
		current, err := a.repos.DestAgentPolicy.Get(caseTest.Context(), agent.AgentID, true)
		if err != nil || current == nil {
			return false
		}
		if version == nativeRemoteCandidateIdentity {
			return s.AgentVersion == version && s.ConfigPolicy && slicesContainsNative(agent.ObservedCapabilities, protocol.CapabilityDestinationPolicy) && current.MintedSHA256 != "" && current.ReportedState == "applied" && current.ReportedSHA256 == current.MintedSHA256 && current.AppliedSHA256 == current.MintedSHA256 && (originalPolicy == nil || (current.MintedSHA256 == originalPolicy.MintedSHA256 && bytes.Equal(current.AppliedBody, originalPolicy.AppliedBody)))
		}
		return s.AgentVersion == version && !s.ConfigPolicy && !s.PolicyPresent && !s.PolicyCapable && !slicesContainsNative(agent.ObservedCapabilities, protocol.CapabilityDestinationPolicy) && current.MintedKind == domain.DestCandidateEmpty && current.MintedSHA256 == "" && current.AppliedSHA256 == originalPolicy.AppliedSHA256 && bytes.Equal(current.AppliedBody, originalPolicy.AppliedBody)
	}
	stableClient := func() {
		clients, err := a.repos.PSPClient.ListByUser(caseTest.Context(), f.user.ID)
		nativeMust(caseTest, err, "read preserved Docker client")
		if len(clients) != 1 || clients[0].ID != originalClient.ID || clients[0].UUID != originalClient.UUID || !clients[0].CreatedAt.Equal(originalClient.CreatedAt) || clients[0].LifetimeUpBytes < originalClient.LifetimeUpBytes || clients[0].LifetimeDownBytes < originalClient.LifetimeDownBytes || clients[0].LifetimeTotalBytes < originalClient.LifetimeTotalBytes {
			caseTest.Fatal("Docker replacement changed stable client identity or cumulative counters")
		}
	}
	run("candidate_policy", func() {
		body := destinationPolicyInput("Docker rollback policy", "block")
		body["inline"] = map[string]any{"ports": strconv.Itoa(port)}
		request("POST", "dest/policies", "", body, &policy, 201)
		request("POST", "dest/publish", "", nil, nil, 200)
		nativeWait(caseTest, 3*time.Minute, "Docker current candidate policy", func() bool { return converged(nativeRemoteCandidateIdentity, time.Time{}) })
		originalAgent, err = a.repos.NodeAgent.GetByAgentID(caseTest.Context(), f.agent.AgentID)
		nativeMust(caseTest, err, "read original Docker identity")
		originalPolicy, err = a.repos.DestAgentPolicy.Get(caseTest.Context(), f.agent.AgentID, true)
		nativeMust(caseTest, err, "read original Docker policy")
		var wire protocol.DestinationPolicy
		if json.Unmarshal(originalPolicy.AppliedBody, &wire) != nil || protocol.ValidateDestinationPolicy(&wire) != nil || protocol.PolicyDigest(&wire) != originalPolicy.AppliedSHA256 || len(wire.Rules) != 1 || wire.Rules[0].ID != fmt.Sprintf("p%d", policy.ID) || wire.Rules[0].Ports != strconv.Itoa(port) || wire.Rules[0].Action != protocol.RuleBlock {
			caseTest.Fatal("Docker candidate did not apply the exact nonempty policy")
		}
		fold()
		traffic(false)
		nativeWait(caseTest, 30*time.Second, "Docker raw traffic baseline", func() bool {
			fold()
			clients, err := a.repos.PSPClient.ListByUser(caseTest.Context(), f.user.ID)
			return err == nil && len(clients) == 1 && clients[0].LastCounterEpoch != 0 && clients[0].LastRawUpBytes+clients[0].LastRawDownBytes > 0
		})
		traffic(false)
		nativeWait(caseTest, 30*time.Second, "Docker nonzero cumulative traffic", func() bool {
			fold()
			clients, err := a.repos.PSPClient.ListByUser(caseTest.Context(), f.user.ID)
			if err != nil || len(clients) != 1 || clients[0].LifetimeUpBytes+clients[0].LifetimeDownBytes == 0 {
				return false
			}
			originalClient = clients[0]
			return true
		})
		before, source, sourceDigest = m.preserved(caseTest), m.profile(caseTest, m.agentName, false), m.digest(caseTest)
		m.assertPrivacy(caseTest, nonce, controlNonce, f.user.Email, "destination@psp.local")
	})
	run("health_failure_restore", func() {
		fault.enabled.Store(true)
		defer fault.enabled.Store(false)
		exited := m.holdCoreExit(caseTest)
		var task nodeagentupgrade.Status
		authorizedAt := time.Now().UTC()
		request("POST", upgradePath, key+"-health", input, &task, 202)
		if task.TaskID == "" || task.AgentID != f.agent.AgentID || task.Version != nativeRemotePublished || task.ExpectedVersion != nativeRemoteCandidate || task.NotAfterMS <= authorizedAt.UnixMilli() {
			caseTest.Fatal("Docker health failure did not authorize the exact published target")
		}
		var activated nativeRemoteReceipt
		var target nativeDockerContainer
		nativeWait(caseTest, 3*time.Minute, "Docker rejected target activation", func() bool {
			data, err := os.ReadFile(filepath.Join(m.root, "upgrades", "receipts", task.TaskID+".json"))
			if err != nil || json.Unmarshal(data, &activated) != nil || activated.Phase != "activated" || fault.rejected.Load() == 0 {
				return false
			}
			target = m.profile(caseTest, m.agentName, false)
			if activated.Request.Task.ID != task.TaskID || activated.Request.Task.NotAfterMS != task.NotAfterMS || len(activated.ActivationNonce) != 32 || activated.Result == nil || activated.Result.Version != nativeRemotePublished || activated.Result.PreviousVersion != nativeRemoteCandidate || activated.Result.Restarted || target.ID == source.ID || target.Image == source.Image || m.digest(caseTest) == sourceDigest {
				caseTest.Fatal("Docker target activation did not bind the real replacement")
			}
			return exited()
		})
		readyPath := filepath.Join(m.root, "upgrades", "requests", task.TaskID+".ready.json")
		if _, err := os.Lstat(readyPath); !errors.Is(err, os.ErrNotExist) {
			caseTest.Fatal("Docker rejected target produced authenticated readiness")
		}
		running := m.execution(caseTest, task.TaskID)
		if running.state != "running" || running.delivered != 0 || running.claim == "" || running.started <= 0 || running.digest != activated.Request.Task.InputSHA256 || running.deadline != task.NotAfterMS {
			caseTest.Fatal("Docker failed activation lost its original durable claim")
		}
		var restoredAt time.Time
		nativeWait(caseTest, 4*time.Minute, "Docker automatic health restoration", func() bool {
			data, err := os.ReadFile(filepath.Join(m.root, "upgrades", "receipts", task.TaskID+".json"))
			var receipt nativeRemoteReceipt
			if err != nil || json.Unmarshal(data, &receipt) != nil || receipt.Phase != "failed" {
				return false
			}
			if restoredAt.IsZero() {
				restoredAt = time.Now().UTC()
			}
			stored, err := a.repos.NodeAgentTask.GetByTaskID(caseTest.Context(), task.TaskID)
			if err != nil || stored == nil || !stored.Status.Terminal() {
				return false
			}
			if stored.Status != domain.NodeAgentTaskFailed || stored.ResultOK == nil || *stored.ResultOK || stored.ResultIndeterminate {
				caseTest.Fatal("Docker health failure did not return a determinate failed result")
			}
			return converged(nativeRemoteCandidateIdentity, restoredAt)
		})
		// The panel accepts the result before the agent consumes its response and
		// commits the ordinary local acknowledgement. Do not sample between them.
		nativeWait(caseTest, 30*time.Second, "Docker failed result acknowledgement", func() bool {
			return m.execution(caseTest, task.TaskID).delivered == 1
		})
		receipt, completed := m.receipt(caseTest, task.TaskID), m.execution(caseTest, task.TaskID)
		if _, err := os.Lstat(readyPath); !errors.Is(err, os.ErrNotExist) {
			caseTest.Fatal("Docker restored failure retained authenticated readiness")
		}
		if receipt.ErrorCode != "agent_upgrade_failed" || receipt.Result != nil || receipt.ActivationNonce != activated.ActivationNonce || !reflect.DeepEqual(receipt.Request.Task, activated.Request.Task) {
			caseTest.Fatal("Docker automatic restoration changed original receipt identity")
		}
		if completed.state != "failed" || completed.delivered != 1 || completed.digest != running.digest || completed.claim != running.claim || completed.started != running.started || completed.deadline != running.deadline {
			caseTest.Fatal("Docker automatic restoration changed original execution identity")
		}
		if m.inspect(caseTest, m.agentName).ID != source.ID || m.pid(caseTest) == source.State.Pid || m.digest(caseTest) != sourceDigest {
			caseTest.Fatal("Docker automatic restoration changed original installation identity")
		}
		fold()
		traffic(false)
		m.assertPreserved(caseTest, before)
		stableClient()
		m.assertPrivacy(caseTest, nonce, controlNonce, f.user.Email, "destination@psp.local")
		before, source = m.preserved(caseTest), m.inspect(caseTest, m.agentName)
	})
	var task nodeagentupgrade.Status
	var successful nativeRemoteExecution
	var receiptDigest [32]byte
	var helper nativeDockerContainer
	run("published_rollback", func() {
		exited := m.holdCoreExit(caseTest)
		after := time.Now().UTC()
		request("POST", upgradePath, key, input, &task, 202)
		if task.TaskID == "" || task.AgentID != f.agent.AgentID || task.Version != nativeRemotePublished || task.ExpectedVersion != nativeRemoteCandidate || task.NotAfterMS <= after.UnixMilli() {
			caseTest.Fatal("Docker HTTP did not authorize the exact published target")
		}
		nativeWait(caseTest, 5*time.Minute, "Docker published helper completion", func() bool {
			stored, err := a.repos.NodeAgentTask.GetByTaskID(caseTest.Context(), task.TaskID)
			if err != nil || stored == nil || !stored.Status.Terminal() {
				return false
			}
			if stored.Status != domain.NodeAgentTaskSucceeded || stored.ResultOK == nil || !*stored.ResultOK || stored.ResultIndeterminate {
				caseTest.Fatal("Docker official rollback task failed; private result withheld")
			}
			return converged(nativeRemotePublishedIdentity, after)
		})
		request("GET", fmt.Sprintf("servers/%d/node-agent-upgrades/%s", f.agent.PanelID, task.TaskID), "", nil, &task, 200)
		if task.UpgradeState != "verified" || task.ObservedVersion != nativeRemotePublished || task.Status != domain.NodeAgentTaskSucceeded || task.BinarySHA256 != m.digest(caseTest) || !exited() || m.inspect(caseTest, m.agentName).ID == source.ID {
			caseTest.Fatal("Docker public status did not verify the actual installed release")
		}
		receipt := m.receipt(caseTest, task.TaskID)
		if receipt.Phase != "succeeded" || receipt.Result == nil || !receipt.Result.Restarted || receipt.Result.BinarySHA256 != task.BinarySHA256 || receipt.Request.Task.ID != task.TaskID || receipt.Request.Task.NotAfterMS != task.NotAfterMS {
			caseTest.Fatal("Docker success receipt did not retain the authorized execution")
		}
		var ready struct {
			TaskID          string `json:"task_id"`
			InputSHA256     string `json:"input_sha256"`
			Version         string `json:"version"`
			BinarySHA256    string `json:"binary_sha256"`
			ActivationNonce string `json:"activation_nonce"`
			PID             int    `json:"pid"`
		}
		data, err := os.ReadFile(filepath.Join(m.root, "upgrades", "requests", task.TaskID+".ready.json"))
		nativeMust(caseTest, err, "read actual Docker authenticated readiness")
		nativeMust(caseTest, json.Unmarshal(data, &ready), "decode Docker readiness")
		// Docker's agent is PID 1 in its namespace; bind that to the real host
		// process and image rather than comparing its PID to the host's number.
		if ready.TaskID != task.TaskID || ready.InputSHA256 != receipt.Request.Task.InputSHA256 || ready.Version != nativeRemotePublished || ready.BinarySHA256 != task.BinarySHA256 || ready.ActivationNonce != receipt.ActivationNonce || len(ready.ActivationNonce) != 32 || ready.PID != 1 {
			caseTest.Fatal("Docker readiness did not bind the current authorized activation")
		}
		m.assertPublishedImage(caseTest, task.TaskID)
		nativeWait(caseTest, 30*time.Second, "Docker successful result acknowledgement", func() bool {
			return m.execution(caseTest, task.TaskID).delivered == 1
		})
		successful = m.execution(caseTest, task.TaskID)
		if successful.state != "succeeded" || successful.delivered != 1 || successful.digest != receipt.Request.Task.InputSHA256 || successful.deadline != task.NotAfterMS || successful.claim == "" || successful.started <= 0 {
			caseTest.Fatal("Docker target did not recover and acknowledge its original claim")
		}
		data, err = os.ReadFile(filepath.Join(m.root, "upgrades", "receipts", task.TaskID+".json"))
		nativeMust(caseTest, err, "read immutable Docker completed receipt")
		receiptDigest = sha256.Sum256(data)
		fold()
		traffic(true)
		m.assertPreserved(caseTest, before)
		stableClient()
		m.assertPrivacy(caseTest)
		helper, before = m.profile(caseTest, m.updaterName, true), m.preserved(caseTest)
	})
	run("restart_recovery", func() {
		after := time.Now().UTC()
		nativeDockerCommand(caseTest, "restart", m.inspect(caseTest, m.agentName).ID)
		nativeWait(caseTest, 3*time.Minute, "Docker restart production convergence", func() bool { return converged(nativeRemotePublishedIdentity, after) })
		if m.execution(caseTest, task.TaskID) != successful {
			caseTest.Fatal("Docker restart changed the completed immutable execution")
		}
		fold()
		traffic(true)
		m.assertPreserved(caseTest, before)
		stableClient()
	})
	run("idempotent_replay", func() {
		live := m.liveCore(caseTest)
		current := m.inspect(caseTest, m.agentName)
		var replay nodeagentupgrade.Status
		request("POST", upgradePath, key, input, &replay, 200)
		if replay.TaskID != task.TaskID || replay.NotAfterMS != task.NotAfterMS || replay.Status != domain.NodeAgentTaskSucceeded {
			caseTest.Fatal("Docker idempotent replay created or extended another execution")
		}
		timer := time.NewTimer(7 * time.Second)
		defer timer.Stop()
		select {
		case <-caseTest.Context().Done():
			caseTest.Fatal("Docker replay observation interrupted")
		case <-timer.C:
		}
		data, err := os.ReadFile(filepath.Join(m.root, "upgrades", "receipts", task.TaskID+".json"))
		nativeMust(caseTest, err, "read replayed Docker receipt")
		updater, agent := m.profile(caseTest, m.updaterName, true), m.inspect(caseTest, m.agentName)
		if sha256.Sum256(data) != receiptDigest || m.execution(caseTest, task.TaskID) != successful || updater.ID != helper.ID || updater.State.Pid != helper.State.Pid || updater.RestartCount != helper.RestartCount || agent.ID != current.ID || agent.State.Pid != current.State.Pid {
			caseTest.Fatal("Docker replay reran the updater or changed the completed receipt")
		}
		traffic(true)
		live()
		m.assertPrivacy(caseTest)
	})
}

type nativeDockerContainer struct {
	ID, Name, Image string
	State           struct {
		Running bool
		Pid     int
	}
	RestartCount int
	Config       struct {
		Env    []string
		Labels map[string]string
	}
	HostConfig struct {
		NetworkMode, PidMode         string
		ReadonlyRootfs, Privileged   bool
		CapAdd, CapDrop, SecurityOpt []string
		Binds                        []string
		Tmpfs                        map[string]string
	}
}

type nativeDockerInstallation struct {
	root, nonce, agentID, credential, agentName, updaterName string
	compose                                                  []string
}

func nativeDockerCommand(t *testing.T, args ...string) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "docker", args...)
	var data []byte
	var err error
	if len(args) > 0 && args[0] == "logs" {
		data, err = cmd.CombinedOutput()
	} else {
		cmd.Stderr = io.Discard
		data, err = cmd.Output()
	}
	if err != nil {
		t.Fatal("Docker acceptance command failed; private command and output withheld")
	}
	if len(data) > 8<<20 {
		t.Fatal("Docker acceptance output exceeded its review bound; content withheld")
	}
	return string(data)
}

func nativeDockerGuard(t *testing.T) {
	t.Helper()
	if os.Geteuid() != 0 || os.Getenv("GITHUB_ACTIONS") != "true" || os.Getenv("RUNNER_ENVIRONMENT") != "github-hosted" || os.Getenv("GITHUB_REPOSITORY") != "KazuhaHub/Passwall-Sub-Panel" || os.Getenv("PSP_NATIVE_DOCKER_IMAGE") != nativeDockerImage {
		t.Fatal("Docker rollback requires the reviewed image on a disposable hosted Linux job")
	}
	release, err := os.ReadFile("/etc/os-release")
	if err != nil || !strings.Contains(string(release), "\nID=ubuntu\n") || !strings.Contains(string(release), "\nVERSION_ID=\"24.04\"\n") || strings.TrimSpace(nativeDockerCommand(t, "ps", "--all", "--quiet")) != "" {
		t.Fatal("Docker rollback refuses an existing container installation or other host")
	}
	var images []struct {
		Os, Architecture string
		Config           struct{ Labels map[string]string }
	}
	if json.Unmarshal([]byte(nativeDockerCommand(t, "image", "inspect", nativeDockerImage)), &images) != nil || len(images) != 1 || images[0].Os != "linux" || images[0].Architecture != runtime.GOARCH || images[0].Config.Labels["org.opencontainers.image.version"] != nativeRemoteCandidate || images[0].Config.Labels["io.kazuhahub.passwall-node.state-schema"] != "9" || images[0].Config.Labels["io.kazuhahub.passwall-node.upgrade-contract"] != "1" {
		t.Fatal("Docker candidate is not the reviewed same-schema production image")
	}
}

func nativeDockerAdmin(t *testing.T, server *httptest.Server, token, method, path, key string, body, result any, want int) {
	t.Helper()
	data, err := json.Marshal(body)
	nativeMust(t, err, "encode Docker administrator input")
	req, err := http.NewRequestWithContext(t.Context(), method, server.URL+"/api/admin/"+path, bytes.NewReader(data))
	nativeMust(t, err, "create Docker administrator request")
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	if key != "" {
		req.Header.Set("Idempotency-Key", key)
	}
	client := server.Client()
	client.Timeout = 10 * time.Second
	response, err := client.Do(req)
	nativeMust(t, err, "call Docker administrator HTTP")
	defer response.Body.Close()
	if response.StatusCode != want {
		t.Fatalf("Docker administrator returned HTTP %d, expected %d; body withheld", response.StatusCode, want)
	}
	if result != nil {
		nativeMust(t, json.NewDecoder(response.Body).Decode(result), "decode Docker administrator response")
	} else {
		_, _ = io.Copy(io.Discard, response.Body)
	}
}

func newNativeDockerInstallation(t *testing.T, server *httptest.Server, token string, f *destinationPolicyFixture) *nativeDockerInstallation {
	t.Helper()
	var random [16]byte
	_, err := rand.Read(random[:])
	nativeMust(t, err, "mint Docker deployment ownership marker")
	root, err := os.MkdirTemp("", "psp-native-docker-")
	nativeMust(t, err, "create private Docker deployment root")
	m := &nativeDockerInstallation{root: root, nonce: hex.EncodeToString(random[:]), agentID: f.agent.AgentID, agentName: fmt.Sprintf("passwall-node-server-%d-agent", f.agent.PanelID), updaterName: fmt.Sprintf("passwall-node-server-%d-updater", f.agent.PanelID)}
	nativeMust(t, os.WriteFile(filepath.Join(root, ".owner"), []byte(m.nonce), 0600), "protect Docker ownership marker")
	t.Cleanup(func() { m.cleanup(t) })
	// The existing fixture is a digest-only enrollment. Use the normal admin
	// rotation before installation so generated materials use a recoverable secret.
	var enrollment struct {
		AgentID    string `json:"agent_id"`
		Credential string `json:"credential"`
		Endpoint   string `json:"endpoint"`
	}
	nativeDockerAdmin(t, server, token, "POST", fmt.Sprintf("servers/%d/rotate-node-credential", f.agent.PanelID), "", nil, &enrollment, 200)
	if enrollment.AgentID != f.agent.AgentID || enrollment.Credential == "" || enrollment.Endpoint != server.URL+"/v1/node/sync" {
		t.Fatal("Docker enrollment did not retain the fixture identity and TLS endpoint")
	}
	m.credential, f.credential = enrollment.Credential, enrollment.Credential
	f.agent, err = f.a.repos.NodeAgent.GetByAgentID(t.Context(), f.agent.AgentID)
	nativeMust(t, err, "read installed Docker enrollment epoch")
	var material struct {
		Files []struct{ Name, Destination, Content string }
	}
	nativeDockerAdmin(t, server, token, "POST", fmt.Sprintf("servers/%d/node-installation-files", f.agent.PanelID), "", map[string]any{"method": "docker", "version": nativeRemoteCandidate, "os": "linux", "docker_remote_upgrade": true}, &material, 200)
	if len(material.Files) != 2 {
		t.Fatal("Docker installation must return the production Compose and credential")
	}
	for _, name := range []string{"config", "data", "upgrades"} {
		nativeMust(t, os.Mkdir(filepath.Join(root, name), 0700), "create private Docker deployment volume")
	}
	for _, file := range material.Files {
		if (file.Name == "compose.yaml" && file.Destination == "compose.yaml") || (file.Name == "node-credential.txt" && file.Destination == "config/node-credential.txt" && file.Content == m.credential+"\n") {
			nativeMust(t, os.WriteFile(filepath.Join(root, file.Destination), []byte(file.Content), 0600), "write normal private Docker installation material")
		} else {
			t.Fatal("Docker installation returned an unexpected private file")
		}
	}
	ca := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw})
	nativeMust(t, os.WriteFile(filepath.Join(root, "fixture-ca.crt"), ca, 0644), "write public Docker fixture TLS CA")
	// TLS trust is the sole override; entrypoints, services, networks, security
	// options and engine socket isolation are unchanged. The public CA has its
	// own read-only mount; the private credential directory remains mode 0700.
	override := "services:\n  passwall-node:\n    environment:\n      SSL_CERT_FILE: /etc/ssl/certs/psp-fixture-ca.crt\n    volumes:\n      - ./fixture-ca.crt:/etc/ssl/certs/psp-fixture-ca.crt:ro\n"
	nativeMust(t, os.WriteFile(filepath.Join(root, "tls.yaml"), []byte(override), 0600), "write scoped Docker TLS trust")
	m.compose = []string{"compose", "--project-name", "psp-native-" + m.nonce, "--file", filepath.Join(root, "compose.yaml"), "--file", filepath.Join(root, "tls.yaml")}
	nativeDockerCommand(t, append(append([]string{}, m.compose...), "up", "--detach", "--pull", "never")...)
	m.profile(t, m.agentName, false)
	m.profile(t, m.updaterName, true)
	return m
}

func (m *nativeDockerInstallation) inspect(t *testing.T, name string) nativeDockerContainer {
	t.Helper()
	var containers []nativeDockerContainer
	if json.Unmarshal([]byte(nativeDockerCommand(t, "container", "inspect", name)), &containers) != nil || len(containers) != 1 || containers[0].Config.Labels["com.docker.compose.project"] != "psp-native-"+m.nonce {
		t.Fatal("Docker container is not owned by this deployment")
	}
	return containers[0]
}

func (m *nativeDockerInstallation) profile(t *testing.T, name string, updater bool) nativeDockerContainer {
	t.Helper()
	c := m.inspect(t, name)
	network := "host"
	wantCapabilities := []string{"CHOWN", "DAC_OVERRIDE", "FOWNER", "SETGID", "SETUID"}
	if updater {
		network, wantCapabilities = "none", []string{"CHOWN", "DAC_READ_SEARCH", "FOWNER"}
	}
	caps := append([]string{}, c.HostConfig.CapAdd...)
	for index := range caps {
		caps[index] = strings.TrimPrefix(caps[index], "CAP_")
	}
	noNewPrivileges := slicesContainsNative(c.HostConfig.SecurityOpt, "no-new-privileges:true") || slicesContainsNative(c.HostConfig.SecurityOpt, "no-new-privileges")
	if !c.HostConfig.ReadonlyRootfs || c.HostConfig.Privileged || c.HostConfig.PidMode != "" || c.HostConfig.NetworkMode != network || !reflect.DeepEqual(c.HostConfig.CapDrop, []string{"ALL"}) || !reflect.DeepEqual(caps, wantCapabilities) || !noNewPrivileges {
		t.Fatal("Docker deployment changed the production isolation profile")
	}
	for _, bind := range c.HostConfig.Binds {
		if !updater && strings.Contains(bind, "docker.sock") {
			t.Fatal("Docker agent acquired the engine socket")
		}
	}
	if updater {
		mounts := map[string]string{}
		for _, bind := range c.HostConfig.Binds {
			parts := strings.Split(bind, ":")
			if len(parts) < 2 || len(parts) > 3 || (len(parts) == 3 && parts[2] != "rw") || mounts[parts[1]] != "" {
				t.Fatal("Docker updater changed its production mount isolation")
			}
			mounts[parts[1]] = parts[0]
		}
		if len(mounts) != 2 || mounts["/var/run/docker.sock"] != "/var/run/docker.sock" || mounts["/run/passwall-node-upgrades"] != filepath.Join(m.root, "upgrades") || !reflect.DeepEqual(c.HostConfig.Tmpfs, map[string]string{"/tmp": "size=4m,mode=1777"}) {
			t.Fatal("Docker updater changed its production mount isolation")
		}
	}
	return c
}

func (m *nativeDockerInstallation) pid(t *testing.T) int {
	t.Helper()
	c := m.profile(t, m.agentName, false)
	if !c.State.Running || c.State.Pid <= 1 {
		t.Fatal("Docker agent is not running")
	}
	status, err := os.ReadFile(fmt.Sprintf("/proc/%d/status", c.State.Pid))
	nativeMust(t, err, "read actual Docker agent process identity")
	if !bytes.Contains(status, []byte("Uid:\t10001\t10001\t10001\t10001\n")) {
		t.Fatal("Docker agent did not drop to its production non-root identity")
	}
	return c.State.Pid
}

func (m *nativeDockerInstallation) liveCore(t *testing.T) func() {
	t.Helper()
	parent, pid, fd := m.pid(t), 0, -1
	var executable os.FileInfo
	for _, child := range nativeCoreChildren(parent) {
		args := nativeCoreArgs(child)
		if !nativeCoreHasParent(child, parent) || !nativeOwnedLiveCoreCommand(args, "xray", "/var/lib/passwall-node/runtime/xray") || !strings.HasPrefix(filepath.Clean(args[0]), "/var/lib/passwall-node/cores/") {
			continue
		}
		file, err := os.Stat(filepath.Join("/proc", strconv.Itoa(child), "root", args[0]))
		live, liveErr := os.Stat(filepath.Join("/proc", strconv.Itoa(child), "exe"))
		if err != nil || liveErr != nil || !file.Mode().IsRegular() || !os.SameFile(file, live) {
			continue
		}
		held, err := unix.PidfdOpen(child, 0)
		if err != nil {
			continue
		}
		if fd >= 0 {
			_ = unix.Close(held)
			_ = unix.Close(fd)
			t.Fatal("Docker agent has multiple matching owned cores")
		}
		pid, fd, executable = child, held, file
	}
	if fd < 0 {
		t.Fatal("Docker agent has no matching owned live core")
	}
	t.Cleanup(func() { _ = unix.Close(fd) })
	return func() {
		t.Helper()
		file, err := os.Stat(fmt.Sprintf("/proc/%d/exe", pid))
		if unix.PidfdSendSignal(fd, 0, nil, 0) != nil || err != nil || !os.SameFile(file, executable) || !nativeCoreHasParent(pid, parent) {
			t.Fatal("Docker traffic replaced or lost its original core")
		}
	}
}

func (m *nativeDockerInstallation) holdCoreExit(t *testing.T) func() bool {
	t.Helper()
	parent, fd := m.pid(t), -1
	for _, child := range nativeCoreChildren(parent) {
		args := nativeCoreArgs(child)
		if !nativeCoreHasParent(child, parent) || !nativeOwnedLiveCoreCommand(args, "xray", "/var/lib/passwall-node/runtime/xray") {
			continue
		}
		held, err := unix.PidfdOpen(child, 0)
		nativeMust(t, err, "hold original owned Docker core identity")
		if fd >= 0 {
			_ = unix.Close(held)
			_ = unix.Close(fd)
			t.Fatal("Docker activation has multiple matching original cores")
		}
		fd = held
	}
	if fd < 0 {
		t.Fatal("Docker activation cannot bind its original core")
	}
	t.Cleanup(func() { _ = unix.Close(fd) })
	return func() bool {
		poll := []unix.PollFd{{Fd: int32(fd), Events: unix.POLLIN}}
		count, err := unix.Poll(poll, 0)
		return err == nil && count == 1 && poll[0].Revents&unix.POLLIN != 0
	}
}

func (m *nativeDockerInstallation) assertPublishedImage(t *testing.T, id string) {
	t.Helper()
	var transaction struct {
		OldContainerID string `json:"old_container_id"`
		NewContainerID string `json:"new_container_id"`
		NewImageID     string `json:"new_image_id"`
		NewImage       string `json:"new_image"`
	}
	data, err := os.ReadFile(filepath.Join(m.root, "upgrades", "receipts", id+".docker.json"))
	nativeMust(t, err, "read production Docker replacement transaction")
	nativeMust(t, json.Unmarshal(data, &transaction), "decode Docker replacement transaction")
	current := m.profile(t, m.agentName, false)
	var images []struct {
		ID               string
		Os, Architecture string
		RepoDigests      []string
		Config           struct{ Labels map[string]string }
	}
	if json.Unmarshal([]byte(nativeDockerCommand(t, "image", "inspect", "ghcr.io/kazuhahub/passwall-node:"+nativeRemotePublished)), &images) != nil || len(images) != 1 || current.ID != transaction.NewContainerID || current.Image != transaction.NewImageID || transaction.NewImage != "ghcr.io/kazuhahub/passwall-node:"+nativeRemotePublished || images[0].ID != current.Image || images[0].Os != "linux" || images[0].Architecture != runtime.GOARCH || images[0].Config.Labels["org.opencontainers.image.version"] != nativeRemotePublished || len(images[0].RepoDigests) == 0 {
		t.Fatal("Docker installed image is not the actual official registry replacement")
	}
	registryBound := false
	for _, digest := range images[0].RepoDigests {
		const prefix = "ghcr.io/kazuhahub/passwall-node@sha256:"
		if strings.HasPrefix(digest, prefix) {
			decoded, err := hex.DecodeString(strings.TrimPrefix(digest, prefix))
			registryBound = registryBound || (err == nil && len(decoded) == sha256.Size)
		}
	}
	if !registryBound {
		t.Fatal("Docker installed image has no official registry digest")
	}
	status, err := os.ReadFile(fmt.Sprintf("/proc/%d/status", m.pid(t)))
	nativeMust(t, err, "read current Docker PID namespace identity")
	bound := false
	for _, line := range strings.Split(string(status), "\n") {
		if strings.HasPrefix(line, "NSpid:") {
			values := strings.Fields(line)
			bound = len(values) >= 3 && values[len(values)-1] == "1"
		}
	}
	if !bound {
		t.Fatal("Docker readiness namespace PID did not bind the current host process")
	}
}

func (m *nativeDockerInstallation) execution(t *testing.T, id string) nativeRemoteExecution {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+filepath.Join(m.root, "data", "state.db")+"?mode=ro&_pragma=query_only(1)&_pragma=busy_timeout(5000)")
	nativeMust(t, err, "open Docker task journal read-only")
	defer db.Close()
	var row nativeRemoteExecution
	nativeMust(t, db.QueryRowContext(t.Context(), "SELECT state,input_sha256,claim_token,started_at_ms,not_after_ms,result_delivered FROM task_executions WHERE task_id=?", id).Scan(&row.state, &row.digest, &row.claim, &row.started, &row.deadline, &row.delivered), "read Docker durable execution identity")
	return row
}

func (m *nativeDockerInstallation) digest(t *testing.T) string {
	t.Helper()
	data, err := os.ReadFile(fmt.Sprintf("/proc/%d/exe", m.pid(t)))
	nativeMust(t, err, "read owned live Docker executable")
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func (m *nativeDockerInstallation) receipt(t *testing.T, id string) nativeRemoteReceipt {
	t.Helper()
	var receipt nativeRemoteReceipt
	data, err := os.ReadFile(filepath.Join(m.root, "upgrades", "receipts", id+".json"))
	nativeMust(t, err, "read production Docker helper receipt")
	nativeMust(t, json.Unmarshal(data, &receipt), "decode Docker helper receipt")
	return receipt
}

func (m *nativeDockerInstallation) preserved(t *testing.T) nativeRemotePreserved {
	t.Helper()
	result := nativeRemotePreserved{hashes: map[string]string{}, inodes: map[string]uint64{}, uid: 10001, gid: 10001}
	for _, name := range []string{"config/node-credential.txt", "fixture-ca.crt", "compose.yaml", "tls.yaml"} {
		data, err := os.ReadFile(filepath.Join(m.root, name))
		nativeMust(t, err, "read retained Docker installation material")
		sum := sha256.Sum256(data)
		result.hashes[name] = hex.EncodeToString(sum[:])
	}
	for _, name := range []string{"config", "config/node-credential.txt", "data", "data/state.db"} {
		info, err := os.Lstat(filepath.Join(m.root, name))
		nativeMust(t, err, "read original Docker state inode")
		result.inodes[name] = info.Sys().(*syscall.Stat_t).Ino
	}
	db, err := sql.Open("sqlite", "file:"+filepath.Join(m.root, "data", "state.db")+"?mode=ro&_pragma=query_only(1)")
	nativeMust(t, err, "open Docker counter epoch read-only")
	defer db.Close()
	nativeMust(t, db.QueryRowContext(t.Context(), "SELECT counter_epoch FROM core_counter_epoch WHERE id=1").Scan(&result.epoch), "read Docker core counter epoch")
	var schema int
	nativeMust(t, db.QueryRowContext(t.Context(), "PRAGMA user_version").Scan(&schema), "read Docker state schema")
	if schema != 9 || len(result.epoch) != 8 {
		t.Fatal("Docker managed state is not schema9 with a durable counter epoch")
	}
	return result
}

func (m *nativeDockerInstallation) assertPreserved(t *testing.T, before nativeRemotePreserved) {
	t.Helper()
	current := m.preserved(t)
	material := reflect.DeepEqual(before.hashes, current.hashes)
	state := reflect.DeepEqual(before.inodes, current.inodes)
	epoch := binary.BigEndian.Uint64(current.epoch) > binary.BigEndian.Uint64(before.epoch)
	if !material || !state || !epoch {
		t.Logf("native diagnostic: docker_preserved_material=%t", material)
		t.Logf("native diagnostic: docker_preserved_state=%t", state)
		t.Logf("native diagnostic: docker_advanced_epoch=%t", epoch)
		t.Fatal("Docker replacement lost private material, original state inode or advancing counter epoch")
	}
}

func (m *nativeDockerInstallation) assertPrivacy(t *testing.T, private ...string) {
	t.Helper()
	private = append(private, m.credential)
	for _, name := range []string{m.agentName, m.updaterName} {
		output := nativeDockerCommand(t, "logs", "--tail", "2000", name)
		for _, value := range private {
			if value != "" && strings.Contains(output, value) {
				t.Fatal("Docker logs exposed private traffic or credentials; content withheld")
			}
		}
	}
	pid := m.pid(t)
	for _, name := range []string{"cmdline", "environ"} {
		data, err := os.ReadFile(filepath.Join("/proc", strconv.Itoa(pid), name))
		nativeMust(t, err, "read Docker credential exposure boundary")
		if bytes.Contains(data, []byte(m.credential)) {
			t.Fatal("Docker credential escaped into process arguments or environment")
		}
	}
}

func (m *nativeDockerInstallation) cleanup(t *testing.T) {
	t.Helper()
	rootInfo, rootErr := os.Lstat(m.root)
	markerInfo, markerErr := os.Lstat(filepath.Join(m.root, ".owner"))
	if rootErr != nil || markerErr != nil || !rootInfo.IsDir() || rootInfo.Mode()&os.ModeSymlink != 0 || rootInfo.Sys().(*syscall.Stat_t).Uid != 0 || !markerInfo.Mode().IsRegular() || markerInfo.Sys().(*syscall.Stat_t).Uid != 0 || markerInfo.Mode().Perm() != 0600 {
		t.Error("Docker cleanup refused changed ownership provenance")
		return
	}
	marker, err := os.ReadFile(filepath.Join(m.root, ".owner"))
	if err != nil || string(marker) != m.nonce {
		t.Error("Docker cleanup refused changed ownership provenance")
		return
	}
	ids := strings.Fields(nativeDockerCommand(t, "ps", "--all", "--quiet", "--filter", "label=com.docker.compose.project=psp-native-"+m.nonce))
	if len(ids) > 8 {
		t.Error("Docker cleanup exceeded its owned container bound")
		return
	}
	for _, id := range ids {
		c := m.inspect(t, id)
		owned := false
		for _, bind := range c.HostConfig.Binds {
			if strings.HasPrefix(bind, filepath.Join(m.root, "upgrades")+":/run/passwall-node-upgrades") {
				owned = true
			}
		}
		if !owned || (c.Config.Labels["com.docker.compose.service"] != "passwall-node" && c.Config.Labels["com.docker.compose.service"] != "passwall-node-updater") {
			t.Error("Docker cleanup refused a foreign container")
			return
		}
	}
	for _, id := range ids {
		nativeDockerCommand(t, "container", "rm", "--force", id)
	}
	var paths []string
	err = filepath.WalkDir(m.root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		info, err := os.Lstat(path)
		if err != nil {
			return err
		}
		// A force-stopped owned agent may leave its production Unix audit socket.
		// All owned containers are already removed. Accept only that exact path
		// and non-root production UID, never a link or another special entry.
		ownedSocket := path == filepath.Join(m.root, "data", "runtime", "audit.sock") && info.Mode()&os.ModeSocket != 0 && info.Sys().(*syscall.Stat_t).Uid == 10001
		if info.Mode()&os.ModeSymlink != 0 || (!info.IsDir() && !info.Mode().IsRegular() && !ownedSocket) || len(paths) >= 2048 {
			return errors.New("Docker cleanup refused linked, special or excessive entries")
		}
		uid := info.Sys().(*syscall.Stat_t).Uid
		if uid != 0 && uid != 10001 {
			return errors.New("Docker cleanup refused foreign file ownership")
		}
		paths = append(paths, path)
		return nil
	})
	if err != nil {
		t.Error("Docker cleanup provenance check failed; details withheld")
		return
	}
	for index := len(paths) - 1; index >= 0; index-- {
		nativeMust(t, os.Remove(paths[index]), "remove exact owned Docker deployment entry")
	}
}
