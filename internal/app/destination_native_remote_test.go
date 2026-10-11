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
	"os/user"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/KazuhaHub/passwall-protocol/protocol"
	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
	"github.com/KazuhaHub/passwall-sub-panel/internal/service/nodeagentupgrade"
)

const nativeRemoteRoot = "/opt/passwall-node"
const nativeRemoteCandidate = "4.0.1.9" // Unpublished source candidate, never presented as a released archive.
const nativeRemotePublished = "4.0.1.8"
const nativeRemotePublishedIdentity = "4.0.1.8 (8e5e3db)"

// No fetch seam, helper invocation, fabricated task result or receipt write:
// normal PSP HTTP creates the task; the non-root Node writes its request;
// the production path unit launches the privileged CLI; the CLI verifies and
// downloads the real signed v4.0.1.8 archive and recovers through production sync.
func TestNativePublishedRemoteRollback(t *testing.T) {
	nativeRemoteGuard(t)
	binary, source := os.Getenv("PSP_NATIVE_REMOTE_NODE_BIN"), os.Getenv("PSP_NATIVE_REMOTE_SOURCE")
	info, err := os.Stat(binary)
	if !filepath.IsAbs(binary) || filepath.Base(binary) != "destination-remote-node" || err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0022 != 0 || !filepath.IsAbs(source) {
		t.Fatal("missing reviewed remote candidate and production deployment assets")
	}
	f := buildDestinationPolicyFixture(t)
	a, token := f.a, destinationRefreshAdminToken(t, f.a)
	settings, err := a.settings.Load(t.Context(), ports.UISettings{})
	nativeMust(t, err, "read remote fixture cadence")
	settings.NodePollSeconds, settings.FullReportSeconds = 2, 0
	nativeMust(t, a.settings.Save(t.Context(), settings), "save remote fixture cadence")
	inbound := nativePort(t)
	f.node.DesiredPort, f.node.ObservedPort, f.node.InboundListen = inbound, inbound, "127.0.0.1"
	nativeMust(t, a.repos.Node.Update(t.Context(), f.node), "configure remote fixture listener")
	a.startDestinationAudit()
	probe := &nativeLegacyProbe{listener: protocol.NewListenerKey(f.node.ID)}
	server := httptest.NewTLSServer(probe.wrap(a.server.Handler))
	t.Cleanup(server.Close)
	_, address, port, nonce, requests := nativeTarget(t)
	_, controlAddress, controlPort, controlNonce, _ := nativeTarget(t)
	if controlPort == port {
		t.Fatal("remote control overlaps the blocked target port")
	}
	managed := newNativeRemoteInstallation(t, source, binary, server, f)
	caseTest := t
	run := func(name string, work func()) {
		if !t.Run(name, func(child *testing.T) { caseTest = child; defer func() { caseTest = t }(); work() }) {
			t.FailNow()
		}
	}
	client := server.Client()
	client.Timeout = 10 * time.Second
	request := func(method, path, key string, body, result any, want int) {
		data, err := json.Marshal(body)
		nativeMust(caseTest, err, "encode remote administrator input")
		req, err := http.NewRequestWithContext(caseTest.Context(), method, server.URL+"/api/admin/"+path, bytes.NewReader(data))
		nativeMust(caseTest, err, "create remote administrator request")
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Content-Type", "application/json")
		if key != "" {
			req.Header.Set("Idempotency-Key", key)
		}
		response, err := client.Do(req)
		nativeMust(caseTest, err, "call remote administrator HTTP")
		defer response.Body.Close()
		if response.StatusCode != want {
			caseTest.Fatalf("remote administrator returned HTTP %d, expected %d; body withheld", response.StatusCode, want)
		}
		if result != nil {
			nativeMust(caseTest, json.NewDecoder(response.Body).Decode(result), "decode remote administrator response")
		} else {
			_, _ = io.Copy(io.Discard, response.Body)
		}
	}
	traffic := func(allowed bool) {
		live := nativeBindLiveCore(caseTest, managed.pid(caseTest), nativeRemoteRoot+"/data", "xray")
		nativeMust(caseTest, nativeVLESSHost(caseTest.Context(), inbound, controlAddress, controlPort, f.user.UUID, controlNonce, "["+controlAddress+"]"), "remote independent control before traffic")
		before := requests.Load()
		err := nativeVLESSHost(caseTest.Context(), inbound, address, port, f.user.UUID, nonce, "["+address+"]")
		if (allowed && err != nil) || (!allowed && err == nil) {
			caseTest.Fatal("remote real traffic disagreed with actual execution")
		}
		want := before
		if allowed {
			want++
		}
		if requests.Load() != want {
			caseTest.Fatal("remote target nonce count disagreed with execution")
		}
		nativeMust(caseTest, nativeVLESSHost(caseTest.Context(), inbound, controlAddress, controlPort, f.user.UUID, controlNonce, "["+controlAddress+"]"), "remote independent control after traffic")
		live()
	}
	var policy destinationPolicyAPIResult
	var originalPolicy *domain.DestAgentPolicy
	var originalAgent *domain.NodeAgent
	var before nativeRemotePreserved
	var task nodeagentupgrade.Status
	var originalExecution nativeRemoteExecution
	var originalPID int
	var originalClient *domain.PSPClient
	var receiptDigest [32]byte
	var helperInvocation string
	key := "native-remote-" + managed.nonce
	upgradeInput := protocol.AgentUpgradeArgs{Version: nativeRemotePublished, ExpectedVersion: nativeRemoteCandidate}
	upgradePath := fmt.Sprintf("servers/%d/upgrade-node-agent", f.agent.PanelID)
	statusPath := func() string { return fmt.Sprintf("servers/%d/node-agent-upgrades/%s", f.agent.PanelID, task.TaskID) }
	streamsReady := func(agent *domain.NodeAgent) bool {
		for _, name := range []domain.NodeAgentStreamName{domain.NodeAgentStreamConfig, domain.NodeAgentStreamRoster, domain.NodeAgentStreamDirectives} {
			stream, err := a.repos.NodeAgent.GetStream(caseTest.Context(), f.agent.AgentID, name)
			if err != nil || stream == nil || !stream.Converged() || stream.AppliedEpoch != agent.Epoch {
				return false
			}
		}
		return true
	}
	foldTraffic := func() {
		nativeMust(caseTest, a.operationGate.RunRead(caseTest.Context(), a.traffic.PollOnce), "fold real remote traffic through production collector")
	}
	waitPublished := func(after time.Time) {
		nativeWait(caseTest, 4*time.Minute, "published remote rollback convergence", func() bool {
			agent, err := a.repos.NodeAgent.GetByAgentID(caseTest.Context(), f.agent.AgentID)
			if err != nil || agent.LastSeen == nil || !agent.LastSeen.After(after) || agent.Epoch != originalAgent.Epoch || agent.CredentialSHA256 != originalAgent.CredentialSHA256 || slicesContainsNative(agent.ObservedCapabilities, protocol.CapabilityDestinationPolicy) || !streamsReady(agent) {
				return false
			}
			s := probe.snapshot()
			return s.AgentVersion == nativeRemotePublishedIdentity && s.CoreState == "running" && !s.PolicyPresent && !s.PolicyCapable && s.ConfigBodyKnown && !s.ConfigPolicy && s.ListenerApplied
		})
		foldTraffic()
		traffic(true)
		managed.assertPreserved(caseTest, before)
		clients, err := a.repos.PSPClient.ListByUser(caseTest.Context(), f.user.ID)
		nativeMust(caseTest, err, "read retained remote client")
		if len(clients) != 1 || originalClient == nil || clients[0].ID != originalClient.ID || clients[0].UUID != originalClient.UUID || !clients[0].CreatedAt.Equal(originalClient.CreatedAt) || clients[0].LifetimeUpBytes < originalClient.LifetimeUpBytes || clients[0].LifetimeDownBytes < originalClient.LifetimeDownBytes || clients[0].LifetimeTotalBytes < originalClient.LifetimeTotalBytes {
			caseTest.Fatal("published remote rollback lost stable client identity or cumulative counters")
		}
		current, err := a.repos.DestAgentPolicy.Get(caseTest.Context(), f.agent.AgentID, true)
		nativeMust(caseTest, err, "read withdrawn remote policy")
		if current == nil || current.MintedKind != domain.DestCandidateEmpty || current.MintedSHA256 != "" || current.AppliedSHA256 != originalPolicy.AppliedSHA256 || !bytes.Equal(current.AppliedBody, originalPolicy.AppliedBody) {
			caseTest.Fatal("remote withdrawal lost policy history or retained a current executable candidate")
		}
		defs, err := a.destDefinitions.ReadDefinitions(caseTest.Context())
		nativeMust(caseTest, err, "read retained remote definitions")
		if len(defs.Policies) != 1 || defs.Policies[0].ID != policy.ID || defs.Policies[0].Action != domain.DestBlock || !defs.Policies[0].Enabled {
			caseTest.Fatal("published remote rollback altered PSP policy definitions")
		}
	}
	run("candidate_policy", func() {
		input := destinationPolicyInput("Remote rollback policy", "block")
		input["inline"] = map[string]any{"ports": strconv.Itoa(port)}
		request("POST", "dest/policies", "", input, &policy, 201)
		request("POST", "dest/publish", "", nil, nil, 200)
		nativeWait(caseTest, 3*time.Minute, "remote candidate nonempty policy", func() bool {
			agent, err := a.repos.NodeAgent.GetByAgentID(caseTest.Context(), f.agent.AgentID)
			if err != nil || agent.LastSeen == nil || probe.snapshot().AgentVersion != nativeRemoteCandidate+" (63c60f6)" || !slicesContainsNative(agent.ObservedCapabilities, protocol.TaskCapability(protocol.TaskKindAgentUpgradeV1)) || !slicesContainsNative(agent.ObservedCapabilities, protocol.CapabilityDestinationPolicy) || !streamsReady(agent) {
				return false
			}
			runtime, err := a.repos.DestAgentPolicy.Get(caseTest.Context(), f.agent.AgentID, true)
			if err != nil || runtime == nil || runtime.MintedSHA256 == "" || runtime.ReportedState != "applied" || runtime.MintedSHA256 != runtime.ReportedSHA256 || runtime.MintedSHA256 != runtime.AppliedSHA256 || runtime.AppliedRuleCount != 1 {
				return false
			}
			var body protocol.DestinationPolicy
			if json.Unmarshal(runtime.MintedBody, &body) != nil || protocol.ValidateDestinationPolicy(&body) != nil || protocol.PolicyDigest(&body) != runtime.MintedSHA256 || len(body.Rules) != 1 || body.Rules[0].ID != fmt.Sprintf("p%d", policy.ID) || body.Rules[0].Action != protocol.RuleBlock || body.Rules[0].Ports != strconv.Itoa(port) {
				return false
			}
			originalAgent, originalPolicy = agent, runtime
			return true
		})
		// Build does not launch the application's cron loop. Run its production
		// traffic callback explicitly: the first nonzero raw sample establishes
		// a reset-safe baseline; only a second traffic wave contributes lifetime.
		foldTraffic()
		traffic(false)
		nativeWait(caseTest, 30*time.Second, "remote candidate raw counters", func() bool {
			foldTraffic()
			clients, err := a.repos.PSPClient.ListByUser(caseTest.Context(), f.user.ID)
			return err == nil && len(clients) == 1 && clients[0].LastCounterEpoch != 0 && clients[0].LastRawUpBytes+clients[0].LastRawDownBytes > 0
		})
		traffic(false)
		nativeWait(caseTest, 30*time.Second, "remote candidate cumulative counters", func() bool {
			foldTraffic()
			clients, err := a.repos.PSPClient.ListByUser(caseTest.Context(), f.user.ID)
			if err != nil || len(clients) != 1 || clients[0].LifetimeUpBytes+clients[0].LifetimeDownBytes == 0 {
				return false
			}
			originalClient = clients[0]
			return true
		})
		originalPID = managed.pid(caseTest)
		before = managed.preserved(caseTest)
		managed.assertPrivacy(caseTest, []string{nonce, controlNonce, f.user.Email, "destination@psp.local"})
		if managed.command(caseTest, "/usr/bin/systemctl", "show", "passwall-node-upgrade.service", "--property=InvocationID", "--value") != "" {
			caseTest.Fatal("remote helper ran before any administrator authorization")
		}
	})
	run("published_rollback", func() {
		exited := nativePanelHoldCoreExit(caseTest, originalPID, nativeRemoteRoot+"/data")
		after := time.Now().UTC()
		request("POST", upgradePath, key, upgradeInput, &task, 202)
		if task.TaskID == "" || task.AgentID != f.agent.AgentID || task.Version != nativeRemotePublished || task.ExpectedVersion != nativeRemoteCandidate || task.NotAfterMS <= after.UnixMilli() {
			caseTest.Fatal("remote HTTP did not create the exact authorized task")
		}
		nativeWait(caseTest, 5*time.Minute, "published remote helper completion", func() bool {
			stored, err := a.repos.NodeAgentTask.GetByTaskID(caseTest.Context(), task.TaskID)
			if err != nil || stored == nil || !stored.Status.Terminal() {
				return false
			}
			if stored.Status != domain.NodeAgentTaskSucceeded || stored.ResultOK == nil || !*stored.ResultOK || stored.ResultIndeterminate {
				caseTest.Fatal("published remote helper task failed; private result withheld")
			}
			var result protocol.AgentUpgradeResult
			if json.Unmarshal(stored.Result, &result) != nil || !result.Restarted || result.PreviousVersion != nativeRemoteCandidate || result.Version != nativeRemotePublished || len(result.BinarySHA256) != 64 {
				caseTest.Fatal("published remote helper result identity invalid")
			}
			if managed.digest(caseTest, "bin/passwall-node") != result.BinarySHA256 {
				caseTest.Fatal("published remote helper installed digest differs from its acknowledged result")
			}
			return true
		})
		waitPublished(after)
		if !exited() || managed.pid(caseTest) == originalPID {
			caseTest.Fatal("published remote rollback did not retire the original agent/core")
		}
		request("GET", statusPath(), "", nil, &task, 200)
		if task.UpgradeState != "verified" || task.Status != domain.NodeAgentTaskSucceeded || task.ObservedVersion != nativeRemotePublished {
			caseTest.Fatal("remote public status did not confirm the actual published target")
		}
		var receipt struct {
			Phase           string `json:"phase"`
			ActivationNonce string `json:"activation_nonce"`
			Request         struct {
				Task protocol.Task `json:"task"`
			} `json:"request"`
			Result protocol.AgentUpgradeResult `json:"result"`
		}
		data := managed.read(caseTest, "upgrades/"+task.TaskID+".json")
		if json.Unmarshal(data, &receipt) != nil || receipt.Phase != "succeeded" || len(receipt.ActivationNonce) != 32 || receipt.Request.Task.ID != task.TaskID || receipt.Request.Task.NotAfterMS != task.NotAfterMS || receipt.Result.BinarySHA256 != task.BinarySHA256 {
			caseTest.Fatal("published helper receipt is not the completed authorized task")
		}
		var ready struct {
			TaskID          string `json:"task_id"`
			InputSHA256     string `json:"input_sha256"`
			Version         string `json:"version"`
			BinarySHA256    string `json:"binary_sha256"`
			ActivationNonce string `json:"activation_nonce"`
			PID             int    `json:"pid"`
		}
		// Use the production document names; its activation is authenticated by
		// the helper, then tied here to the same result and live installed process.
		if json.Unmarshal(managed.read(caseTest, "data/upgrades/"+task.TaskID+".ready.json"), &ready) != nil {
			caseTest.Fatal("remote target readiness document invalid")
		}
		if ready.TaskID != task.TaskID || ready.InputSHA256 != receipt.Request.Task.InputSHA256 || ready.Version != nativeRemotePublished || ready.BinarySHA256 != task.BinarySHA256 || ready.ActivationNonce != receipt.ActivationNonce || ready.PID != managed.pid(caseTest) {
			caseTest.Fatal("remote target readiness did not bind the current activation")
		}
		originalExecution = managed.execution(caseTest, task.TaskID)
		if originalExecution.state != "succeeded" || originalExecution.digest != receipt.Request.Task.InputSHA256 || originalExecution.deadline != task.NotAfterMS || originalExecution.started <= 0 || originalExecution.claim == "" || originalExecution.delivered != 1 {
			caseTest.Fatal("remote target did not durably recover and acknowledge the original execution")
		}
		receiptDigest = sha256.Sum256(data)
		helperInvocation = managed.command(caseTest, "/usr/bin/systemctl", "show", "passwall-node-upgrade.service", "--property=InvocationID", "--value")
		if len(helperInvocation) != 32 || managed.command(caseTest, "/usr/bin/systemctl", "show", "passwall-node-upgrade.service", "--property=Result", "--value") != "success" {
			caseTest.Fatal("production path watcher did not complete a real helper invocation")
		}
	})
	run("restart_recovery", func() {
		after := time.Now().UTC()
		prior := managed.pid(caseTest)
		managed.command(caseTest, "/usr/bin/systemctl", "restart", "passwall-node.service")
		waitPublished(after)
		if managed.pid(caseTest) == prior || managed.execution(caseTest, task.TaskID) != originalExecution || sha256.Sum256(managed.read(caseTest, "upgrades/"+task.TaskID+".json")) != receiptDigest {
			caseTest.Fatal("remote restart changed the immutable completed execution")
		}
	})
	run("idempotent_replay", func() {
		prior := managed.pid(caseTest)
		live := nativeBindLiveCore(caseTest, prior, nativeRemoteRoot+"/data", "xray")
		var replay nodeagentupgrade.Status
		request("POST", upgradePath, key, upgradeInput, &replay, 200)
		if replay.TaskID != task.TaskID || replay.NotAfterMS != task.NotAfterMS || replay.Status != task.Status || replay.BinarySHA256 != task.BinarySHA256 {
			caseTest.Fatal("remote administrator replay extended authorization or created another task")
		}
		time.Sleep(7 * time.Second)
		traffic(true)
		live()
		if managed.pid(caseTest) != prior || managed.execution(caseTest, task.TaskID) != originalExecution || sha256.Sum256(managed.read(caseTest, "upgrades/"+task.TaskID+".json")) != receiptDigest || managed.command(caseTest, "/usr/bin/systemctl", "show", "passwall-node-upgrade.service", "--property=InvocationID", "--value") != helperInvocation {
			caseTest.Fatal("remote completed task replay executed the helper again")
		}
		managed.assertPrivacy(caseTest, nil)
	})
}

type nativeRemoteExecution struct {
	state, digest, claim string
	started, deadline    int64
	delivered            int
}
type nativeRemotePreserved struct {
	hashes   map[string]string
	inodes   map[string]uint64
	uid, gid uint32
	epoch    []byte
}
type nativeRemoteInstallation struct {
	nonce, credential           string
	uid, gid                    uint32
	rootCreated, accountCreated bool
	units                       map[string]string
	createdUnits                map[string]bool
}

func nativeRemoteGuard(t *testing.T) {
	t.Helper()
	if os.Geteuid() != 0 || os.Getenv("GITHUB_ACTIONS") != "true" || os.Getenv("RUNNER_ENVIRONMENT") != "github-hosted" || os.Getenv("GITHUB_REPOSITORY") != "KazuhaHub/Passwall-Sub-Panel" {
		t.Fatal("remote rollback requires a disposable hosted Linux job")
	}
	release, err := os.ReadFile("/etc/os-release")
	init, initErr := os.ReadFile("/proc/1/comm")
	if err != nil || !strings.Contains(string(release), "\nID=ubuntu\n") || !strings.Contains(string(release), "\nVERSION_ID=\"24.04\"\n") || initErr != nil || strings.TrimSpace(string(init)) != "systemd" {
		t.Fatal("remote rollback requires Ubuntu 24.04 and real systemd PID 1")
	}
	for _, path := range []string{nativeRemoteRoot, "/opt/.passwall-node-install.lock", "/etc/systemd/system/passwall-node.service", "/etc/systemd/system/passwall-node-upgrade.service", "/etc/systemd/system/passwall-node-upgrade.path"} {
		if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
			t.Fatal("remote rollback refuses any pre-existing managed installation")
		}
	}
	for _, unit := range []string{"passwall-node.service", "passwall-node-upgrade.service", "passwall-node-upgrade.path"} {
		output, err := exec.Command("/usr/bin/systemctl", "show", unit, "--property=LoadState", "--value").Output()
		if err != nil || strings.TrimSpace(string(output)) != "not-found" {
			t.Fatal("remote rollback refuses a foreign or masked unit")
		}
	}
	command := exec.Command("/usr/bin/getent", "passwd", "passwall-node")
	command.Stdout, command.Stderr = io.Discard, io.Discard
	var status *exec.ExitError
	if err := command.Run(); !errors.As(err, &status) || status.ExitCode() != 2 {
		t.Fatal("remote rollback cannot prove the dedicated account is absent")
	}
}

func newNativeRemoteInstallation(t *testing.T, source, binary string, server *httptest.Server, f destinationPolicyFixture) *nativeRemoteInstallation {
	t.Helper()
	var random [16]byte
	_, err := rand.Read(random[:])
	nativeMust(t, err, "mint remote ownership marker")
	m := &nativeRemoteInstallation{nonce: hex.EncodeToString(random[:]), credential: f.credential, units: map[string]string{}, createdUnits: map[string]bool{}}
	t.Cleanup(func() { m.cleanup(t) })
	installer, err := os.ReadFile(filepath.Join(source, "deployment", "install.sh"))
	nativeMust(t, err, "read production managed service template")
	_, after, found := strings.Cut(strings.ReplaceAll(string(installer), "\r\n", "\n"), "cat > \"$stage/bundle/passwall-node.service\" <<'UNIT'\n")
	unit, _, closed := strings.Cut(after, "\nUNIT\n")
	if !found || !closed || !strings.HasPrefix(unit, "[Unit]\n") {
		t.Fatal("production managed service template is not the reviewed shape")
	}
	m.units["passwall-node.service"] = unit + "\n"
	for _, name := range []string{"passwall-node-upgrade.service", "passwall-node-upgrade.path"} {
		data, err := os.ReadFile(filepath.Join(source, "deployment", name))
		nativeMust(t, err, "read production remote upgrade unit")
		m.units[name] = string(data)
	}
	m.command(t, "/usr/sbin/useradd", "--system", "--user-group", "--home-dir", nativeRemoteRoot, "--no-create-home", "--shell", "/usr/sbin/nologin", "passwall-node")
	m.accountCreated = true
	account, err := user.Lookup("passwall-node")
	nativeMust(t, err, "read dedicated remote account")
	uid, err := strconv.ParseUint(account.Uid, 10, 32)
	nativeMust(t, err, "read remote UID")
	gid, err := strconv.ParseUint(account.Gid, 10, 32)
	nativeMust(t, err, "read remote GID")
	if uid == 0 || account.HomeDir != nativeRemoteRoot {
		t.Fatal("remote managed account identity invalid")
	}
	m.uid, m.gid = uint32(uid), uint32(gid)
	nativeMust(t, os.Mkdir(nativeRemoteRoot, 0755), "create exclusive remote root")
	m.rootCreated = true
	m.write(t, ".psp-native-remote-owner", []byte(m.nonce), 0600, 0, 0)
	for _, dir := range []string{"bin", "licenses", "config", "data", "data/upgrades", "upgrades"} {
		mode, owner, group := os.FileMode(0755), 0, 0
		if dir == "config" || dir == "data" || dir == "data/upgrades" {
			mode, owner, group = 0700, int(m.uid), int(m.gid)
		}
		if dir == "upgrades" {
			mode, group = 0750, int(m.gid)
		}
		nativeMust(t, os.Mkdir(filepath.Join(nativeRemoteRoot, dir), mode), "create owned remote directory")
		nativeMust(t, os.Chown(filepath.Join(nativeRemoteRoot, dir), owner, group), "assign remote directory ownership")
	}
	data, err := os.ReadFile(binary)
	nativeMust(t, err, "read pinned remote candidate")
	m.write(t, "bin/passwall-node", data, 0755, 0, 0)
	var build struct {
		Version         string `json:"version"`
		StateSchema     int    `json:"state_schema"`
		UpgradeContract int    `json:"upgrade_contract"`
	}
	if json.Unmarshal([]byte(m.command(t, nativeRemoteRoot+"/bin/passwall-node", "--upgrade-info")), &build) != nil || build.Version != nativeRemoteCandidate || build.StateSchema != 9 || build.UpgradeContract != 1 {
		t.Fatal("remote source candidate lacks its declared managed upgrade identity")
	}
	for _, name := range []string{"LICENSE", "NOTICE"} {
		data, err := os.ReadFile(filepath.Join(source, name))
		nativeMust(t, err, "read reviewed remote license")
		m.write(t, "licenses/"+name, data, 0644, 0, 0)
	}
	ca := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw})
	environment := "PSP_NODE_ENDPOINT=\"" + server.URL + "/v1/node/sync\"\nPSP_NODE_AGENT_ID=\"" + f.agent.AgentID + "\"\nSSL_CERT_FILE=\"/opt/passwall-node/config/fixture-ca.crt\"\n"
	for name, data := range map[string][]byte{"credential": []byte(m.credential + "\n"), "environment": []byte(environment), "fixture-ca.crt": ca, "version": []byte(nativeRemoteCandidate + "\n")} {
		m.write(t, "config/"+name, data, 0600, int(m.uid), int(m.gid))
	}
	m.write(t, "upgrades/enabled", []byte(protocol.TaskKindAgentUpgradeV1+"\n"), 0640, 0, int(m.gid))
	m.write(t, "passwall-node.service", []byte(m.units["passwall-node.service"]), 0644, 0, 0)
	for name, body := range m.units {
		file, err := os.OpenFile(filepath.Join("/etc/systemd/system", name), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0644)
		nativeMust(t, err, "create exclusive production remote unit")
		m.createdUnits[name] = true
		_, writeErr := io.WriteString(file, body)
		closeErr := file.Close()
		nativeMust(t, writeErr, "write production remote unit")
		nativeMust(t, closeErr, "close production remote unit")
	}
	m.command(t, "/usr/bin/systemctl", "daemon-reload")
	m.command(t, "/usr/bin/systemctl", "start", "passwall-node-upgrade.path", "passwall-node.service")
	if m.command(t, "/usr/bin/systemctl", "show", "passwall-node-upgrade.path", "--property=SubState", "--value") != "waiting" {
		t.Fatal("production remote watcher did not become ready")
	}
	return m
}

func (m *nativeRemoteInstallation) command(t *testing.T, tool string, args ...string) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, tool, args...)
	cmd.Env = []string{"PATH=/usr/bin:/bin", "LANG=C"}
	cmd.WaitDelay = time.Second
	data, err := cmd.CombinedOutput()
	if bytes.Contains(data, []byte(m.credential)) {
		t.Fatal("remote fixture credential escaped into command output; content withheld")
	}
	if err != nil {
		t.Fatalf("remote acceptance command failed (%s); private output withheld", filepath.Base(tool))
	}
	return strings.TrimSpace(string(data))
}
func (m *nativeRemoteInstallation) write(t *testing.T, name string, data []byte, mode fs.FileMode, uid, gid int) {
	t.Helper()
	file, err := os.OpenFile(filepath.Join(nativeRemoteRoot, name), os.O_CREATE|os.O_EXCL|os.O_WRONLY, mode)
	nativeMust(t, err, "write exclusive owned remote file")
	_, writeErr := file.Write(data)
	closeErr := file.Close()
	nativeMust(t, writeErr, "write owned remote bytes")
	nativeMust(t, closeErr, "close owned remote file")
	nativeMust(t, os.Chown(filepath.Join(nativeRemoteRoot, name), uid, gid), "assign remote file ownership")
}
func (m *nativeRemoteInstallation) read(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(nativeRemoteRoot, name))
	nativeMust(t, err, "read owned remote evidence")
	return data
}
func (m *nativeRemoteInstallation) digest(t *testing.T, name string) string {
	sum := sha256.Sum256(m.read(t, name))
	return hex.EncodeToString(sum[:])
}
func (m *nativeRemoteInstallation) pid(t *testing.T) int {
	t.Helper()
	pid, err := strconv.Atoi(m.command(t, "/usr/bin/systemctl", "show", "passwall-node.service", "--property=MainPID", "--value"))
	if err != nil || pid <= 1 {
		t.Fatal("remote managed process is not running")
	}
	info, err := os.Stat(fmt.Sprintf("/proc/%d/exe", pid))
	nativeMust(t, err, "read remote process identity")
	installed, err := os.Stat(nativeRemoteRoot + "/bin/passwall-node")
	nativeMust(t, err, "read installed remote identity")
	if !os.SameFile(info, installed) || info.Sys().(*syscall.Stat_t).Uid != 0 {
		t.Fatal("remote running executable does not match the installed release")
	}
	status, err := os.ReadFile(fmt.Sprintf("/proc/%d/status", pid))
	nativeMust(t, err, "read remote process UID")
	if !strings.Contains(string(status), fmt.Sprintf("Uid:\t%d\t%d\t%d\t%d\n", m.uid, m.uid, m.uid, m.uid)) {
		t.Fatal("remote managed process is not the dedicated non-root account")
	}
	return pid
}
func (m *nativeRemoteInstallation) execution(t *testing.T, id string) nativeRemoteExecution {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+nativeRemoteRoot+"/data/state.db?mode=ro&_pragma=query_only(1)&_pragma=busy_timeout(5000)")
	nativeMust(t, err, "open remote execution journal read-only")
	defer db.Close()
	var row nativeRemoteExecution
	nativeMust(t, db.QueryRowContext(t.Context(), "SELECT state,input_sha256,claim_token,started_at_ms,not_after_ms,result_delivered FROM task_executions WHERE task_id=?", id).Scan(&row.state, &row.digest, &row.claim, &row.started, &row.deadline, &row.delivered), "read remote durable execution")
	return row
}
func (m *nativeRemoteInstallation) preserved(t *testing.T) nativeRemotePreserved {
	t.Helper()
	result := nativeRemotePreserved{hashes: map[string]string{}, inodes: map[string]uint64{}, uid: m.uid, gid: m.gid}
	for _, name := range []string{"config/credential", "config/environment", "config/fixture-ca.crt", "passwall-node.service"} {
		result.hashes[name] = m.digest(t, name)
	}
	for _, name := range []string{"config", "data", "data/state.db", "config/credential", "config/environment"} {
		info, err := os.Lstat(filepath.Join(nativeRemoteRoot, name))
		nativeMust(t, err, "read preserved remote inode")
		result.inodes[name] = info.Sys().(*syscall.Stat_t).Ino
	}
	db, err := sql.Open("sqlite", "file:"+nativeRemoteRoot+"/data/state.db?mode=ro&_pragma=query_only(1)")
	nativeMust(t, err, "open remote epoch read-only")
	defer db.Close()
	nativeMust(t, db.QueryRowContext(t.Context(), "SELECT counter_epoch FROM core_counter_epoch WHERE id=1").Scan(&result.epoch), "read remote counter epoch")
	var schema int
	nativeMust(t, db.QueryRowContext(t.Context(), "PRAGMA user_version").Scan(&schema), "read remote schema")
	if schema != 9 || len(result.epoch) != 8 {
		t.Fatal("remote managed state is not schema9 with a durable counter epoch")
	}
	return result
}
func (m *nativeRemoteInstallation) assertPreserved(t *testing.T, before nativeRemotePreserved) {
	t.Helper()
	after := m.preserved(t)
	// A different supervisor must advance the durable counter epoch; retaining
	// the old process epoch would merge reset counters into the previous sample.
	if !reflect.DeepEqual(before.hashes, after.hashes) || !reflect.DeepEqual(before.inodes, after.inodes) || before.uid != after.uid || before.gid != after.gid || binary.BigEndian.Uint64(after.epoch) <= binary.BigEndian.Uint64(before.epoch) {
		t.Fatal("remote rollback replaced private identity, database inode or immutable unit/environment, or failed to advance the counter epoch")
	}
	account, err := user.Lookup("passwall-node")
	nativeMust(t, err, "read retained remote account")
	if account.HomeDir != nativeRemoteRoot || account.Uid != strconv.FormatUint(uint64(m.uid), 10) || account.Gid != strconv.FormatUint(uint64(m.gid), 10) {
		t.Fatal("remote rollback replaced the dedicated account")
	}
}
func (m *nativeRemoteInstallation) assertPrivacy(t *testing.T, private []string) {
	t.Helper()
	private = append(private, m.credential)
	for _, unit := range []string{"passwall-node.service", "passwall-node-upgrade.service"} {
		output := m.command(t, "/usr/bin/journalctl", "--unit", unit, "--no-pager", "--output=cat")
		for _, value := range private {
			if value != "" && strings.Contains(output, value) {
				t.Fatal("private remote traffic escaped into journal; content withheld")
			}
		}
	}
	pid := m.pid(t)
	for _, name := range []string{fmt.Sprintf("/proc/%d/cmdline", pid), fmt.Sprintf("/proc/%d/environ", pid)} {
		data, err := os.ReadFile(name)
		nativeMust(t, err, "read private remote process shape")
		if bytes.Contains(data, []byte(m.credential)) {
			t.Fatal("remote credential escaped into arguments or environment")
		}
	}
}

// Cleanup validates exact run provenance before touching a fixed path. Foreign
// units, changed account/credential, links, special files or foreign ownership
// stop removal. Every owned entry is removed individually, bottom-up.
func (m *nativeRemoteInstallation) cleanup(t *testing.T) {
	t.Helper()
	if m.rootCreated {
		info, err := os.Lstat(nativeRemoteRoot)
		if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || info.Sys().(*syscall.Stat_t).Uid != 0 || string(m.read(t, ".psp-native-remote-owner")) != m.nonce {
			t.Error("remote cleanup refused changed fixed-root provenance")
			return
		}
		if data, err := os.ReadFile(nativeRemoteRoot + "/config/credential"); err == nil && string(data) != m.credential+"\n" {
			t.Error("remote cleanup refused changed private identity")
			return
		}
		for _, name := range []string{"passwall-node-upgrade.path", "passwall-node-upgrade.service", "passwall-node.service"} {
			if !m.createdUnits[name] {
				continue
			}
			path := filepath.Join("/etc/systemd/system", name)
			verify := func() bool {
				info, err := os.Lstat(path)
				data, readErr := os.ReadFile(path)
				return err == nil && readErr == nil && info.Mode().IsRegular() && info.Sys().(*syscall.Stat_t).Uid == 0 && string(data) == m.units[name]
			}
			if !verify() {
				t.Error("remote cleanup refused a changed or foreign unit")
				return
			}
			m.command(t, "/usr/bin/systemctl", "stop", name)
			if !verify() {
				t.Error("remote cleanup unit provenance changed during stop")
				return
			}
			nativeMust(t, os.Remove(path), "remove exact owned remote unit")
		}
		m.command(t, "/usr/bin/systemctl", "daemon-reload")
		var paths []string
		err = filepath.WalkDir(nativeRemoteRoot, func(path string, entry fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			relative, err := filepath.Rel(nativeRemoteRoot, path)
			if err != nil || filepath.IsAbs(relative) || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
				return errors.New("remote cleanup path escaped the owned root")
			}
			info, err := os.Lstat(path)
			if err != nil || info.Mode()&os.ModeSymlink != 0 || (!info.IsDir() && !info.Mode().IsRegular()) {
				return errors.New("remote cleanup refused linked or special entries")
			}
			owner := info.Sys().(*syscall.Stat_t).Uid
			if owner != 0 && owner != m.uid {
				return errors.New("remote cleanup refused foreign ownership")
			}
			if len(paths) >= 2048 {
				return errors.New("remote cleanup exceeded its entry bound")
			}
			paths = append(paths, path)
			return nil
		})
		if err != nil {
			t.Error("remote cleanup provenance check failed; details withheld")
			return
		}
		for i := len(paths) - 1; i >= 0; i-- {
			nativeMust(t, os.Remove(paths[i]), "remove exact owned remote entry")
		}
		m.rootCreated = false
	}
	if m.accountCreated {
		account, err := user.Lookup("passwall-node")
		if err != nil || account.HomeDir != nativeRemoteRoot || account.Uid != strconv.FormatUint(uint64(m.uid), 10) || account.Gid != strconv.FormatUint(uint64(m.gid), 10) {
			t.Error("remote cleanup refused a changed service account")
			return
		}
		m.command(t, "/usr/sbin/userdel", "passwall-node")
		m.accountCreated = false
	}
}
