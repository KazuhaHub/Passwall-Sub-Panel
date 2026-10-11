//go:build destination_acceptance && linux

package app

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"encoding/pem"
	"net"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/KazuhaHub/passwall-protocol/protocol"
	"github.com/KazuhaHub/passwall-sub-panel/internal/config"
	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/service/destpolicy"
)

func TestNativeLegacyPSPUpgrade(t *testing.T) {
	if os.Geteuid() != 0 || os.Getenv("GITHUB_ACTIONS") != "true" || os.Getenv("RUNNER_ENVIRONMENT") != "github-hosted" || os.Getenv("GITHUB_REPOSITORY") != "KazuhaHub/Passwall-Sub-Panel" {
		t.Fatal("legacy upgrade requires the disposable hosted acceptance job")
	}
	bridge, nodeBinary := os.Getenv("PSP_NATIVE_LEGACY_PANEL_BIN"), os.Getenv("PSP_NATIVE_LEGACY_NODE_BIN")
	for _, executable := range []struct{ path, name string }{{bridge, "destination-legacy-panel.test"}, {nodeBinary, "destination-legacy-node"}} {
		info, err := os.Stat(executable.path)
		if err != nil || !filepath.IsAbs(executable.path) || filepath.Base(executable.path) != executable.name || !info.Mode().IsRegular() || info.Mode().Perm()&0022 != 0 {
			t.Fatal("missing immutable legacy executable")
		}
	}
	dir := t.TempDir()
	cfg := config.Config{Listen: "127.0.0.1:0", JWTSecret: strings.Repeat("j", 48), EncryptionKey: strings.Repeat("e", 48), ConfigDir: filepath.Join(dir, "panel-config"), DataDir: filepath.Join(dir, "panel-data")}
	encoded, err := json.Marshal(cfg)
	nativeMust(t, err, "encode owned legacy config")
	configPath := filepath.Join(dir, "legacy-config.json")
	nativeMust(t, os.WriteFile(configPath, encoded, 0600), "write owned legacy config")
	inbound, api := nativePort(t), nativePort(t)
	privateOutput := &nativePrivateOutput{}
	start := func(cmd *exec.Cmd) chan error {
		cmd.Stdout, cmd.Stderr = privateOutput, privateOutput
		nativeMust(t, cmd.Start(), "start immutable legacy process")
		done := make(chan error, 1)
		go func() { done <- cmd.Wait() }()
		return done
	}
	stop := func(cmd **exec.Cmd, done chan error) {
		if *cmd == nil {
			return
		}
		_ = (*cmd).Process.Signal(syscall.SIGTERM)
		select {
		case err := <-done:
			nativeMust(t, err, "drain immutable legacy process")
		case <-time.After(15 * time.Second):
			_ = (*cmd).Process.Kill()
			<-done
			t.Error("legacy process did not drain")
		}
		*cmd = nil
	}
	legacy := exec.Command(bridge, "-test.run=^TestNativeLegacyPanelBridge$", "-test.timeout=15m")
	legacy.Env = append(os.Environ(), "PSP_NATIVE_LEGACY_CONFIG="+configPath, "PSP_NATIVE_LEGACY_INBOUND="+strconv.Itoa(inbound))
	legacyDone := start(legacy)
	var node *exec.Cmd
	var nodeDone chan error
	var current *App
	var adminToken string
	var server *httptest.Server
	t.Cleanup(func() {
		stop(&node, nodeDone)
		if server != nil {
			server.Close()
		}
		stop(&legacy, legacyDone)
		if current != nil {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			nativeMust(t, current.Shutdown(ctx), "close upgraded panel")
		}
	})
	var ready struct {
		Address, AgentID                 string
		PanelID, NodeID, GroupID, UserID int64
	}
	nativeWait(t, 60*time.Second, "released panel ready", func() bool {
		data, err := os.ReadFile(filepath.Join(dir, "legacy-ready.json"))
		return err == nil && json.Unmarshal(data, &ready) == nil && ready.AgentID == "agt_native_legacy_upgrade" && ready.NodeID > 0 && ready.GroupID > 0 && ready.PanelID > 0 && ready.UserID > 0
	})
	host, _, err := net.SplitHostPort(ready.Address)
	if err != nil || host != "127.0.0.1" {
		t.Fatal("legacy listener escaped loopback")
	}
	// The old Build owns the fresh schema. Check before any candidate Build can
	// migrate it, rather than seeding new tables and calling that an upgrade.
	db, err := sql.Open("sqlite", "file:"+filepath.Join(cfg.DataDir, "panel.db")+"?mode=ro")
	nativeMust(t, err, "open legacy schema read-only")
	var destinationTables int
	err = db.QueryRowContext(t.Context(), "SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name GLOB 'dest_*'").Scan(&destinationTables)
	closeErr := db.Close()
	nativeMust(t, err, "read actual pre-upgrade schema")
	nativeMust(t, closeErr, "close read-only legacy schema")
	if destinationTables != 0 {
		t.Fatal("legacy database already contained destination schema")
	}
	upstream, err := url.Parse("http://" + ready.Address)
	nativeMust(t, err, "create loopback legacy handler")
	gateway := &nativePanelGateway{}
	gateway.replace(httputil.NewSingleHostReverseProxy(upstream))
	probe := &nativeLegacyProbe{listener: protocol.NewListenerKey(ready.NodeID)}
	server = httptest.NewTLSServer(probe.wrap(gateway))
	ca, credential := filepath.Join(dir, "ca.pem"), filepath.Join(dir, "credential")
	nativeMust(t, os.WriteFile(ca, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw}), 0600), "write scoped legacy CA")
	nativeMust(t, os.WriteFile(credential, []byte("fixture-native-legacy-credential-with-enough-entropy"), 0600), "write private legacy credential")
	_, address, targetPort, nonce, _ := nativeTarget(t)
	t.Cleanup(func() { stop(&node, nodeDone) })
	nodeDir := filepath.Join(dir, "node")
	node = exec.Command(nodeBinary, "--endpoint", server.URL+"/v1/node/sync", "--agent-id", ready.AgentID, "--credential-file", credential, "--data-dir", nodeDir, "--xray-api-listen", net.JoinHostPort("127.0.0.1", strconv.Itoa(api)))
	node.Env = append(os.Environ(), "SSL_CERT_FILE="+ca)
	nodeDone = start(node)
	traffic := func(test *testing.T) {
		test.Helper()
		ctx, cancel := context.WithTimeout(test.Context(), 5*time.Second)
		defer cancel()
		nativeMust(test, nativeVLESSHost(ctx, inbound, address, targetPort, "11111111-1111-4111-8111-111111111111", nonce, "legacy.example.test"), "legacy VLESS traffic")
	}
	nativeWait(t, 120*time.Second, "released node applied released panel", func() bool {
		s := probe.snapshot()
		return s.Sequence > 1 && s.AgentVersion == "v4.0.1.8" && s.CoreEngine == "xray" && s.CoreVersion == "26.6.27" && s.CoreState == "running" && !s.PolicyCapable && !s.PolicyPresent && s.ListenerApplied && s.HaveConfig.ETag != "" && s.HaveConfig.ETag == s.Config.ETag && s.HaveConfig.Applied == s.Config.Version && s.HaveRoster.ETag != "" && s.HaveRoster.ETag == s.Roster.ETag && s.HaveRoster.Applied == s.Roster.Version
	})
	before := probe.snapshot()
	configFile := filepath.Join(nodeDir, "runtime", "xray", "current.json")
	beforeConfig, err := os.ReadFile(configFile)
	nativeMust(t, err, "read legacy confirmed artifact")
	live := nativeBindLiveCore(t, node.Process.Pid, nodeDir, "xray")
	assertUnchanged := func(test *testing.T, cycles uint64) {
		test.Helper()
		sequence := probe.snapshot().Sequence
		nativeWait(test, 45*time.Second, "unchanged legacy upgrade sync", func() bool {
			live()
			s := probe.snapshot()
			if s.Sequence <= sequence {
				return false
			}
			if s.PolicyPresent || s.PolicyCapable || s.Config.ETag != before.Config.ETag || s.Config.Version != before.Config.Version || s.HaveConfig != before.HaveConfig || s.Roster.ETag != before.Roster.ETag || s.Roster.Version != before.Roster.Version || s.HaveRoster != before.HaveRoster {
				test.Fatal("PSP upgrade changed legacy stream validators or dispatched policy")
			}
			return s.Sequence >= sequence+cycles && s.ListenerApplied && s.CoreState == "running"
		})
		config, err := os.ReadFile(configFile)
		nativeMust(test, err, "read unchanged legacy artifact")
		if !bytes.Equal(config, beforeConfig) {
			test.Fatal("PSP upgrade rewrote the legacy artifact")
		}
		traffic(test)
		live()
	}
	if !t.Run("old_panel", func(test *testing.T) { traffic(test); assertUnchanged(test, 3) }) {
		return
	}
	if !t.Run("panel_upgrade", func(test *testing.T) {
		gateway.replace(nil)
		stop(&legacy, legacyDone)
		live()
		upgradedAfter := time.Now().UTC()
		current, err = Build(t.Context(), &cfg)
		nativeMust(test, err, "migrate original released database")
		gateway.replace(current.server.Handler)
		assertUnchanged(test, 3)
		nativeWait(test, 30*time.Second, "fresh legacy report on upgraded panel", func() bool {
			agent, err := current.repos.NodeAgent.GetByAgentID(test.Context(), ready.AgentID)
			return err == nil && agent.LastSeen != nil && agent.LastSeen.After(upgradedAfter) && agent.ObservedCoreEngine == domain.NodeCoreXray && !slicesContainsNative(agent.ObservedCapabilities, protocol.CapabilityDestinationPolicy)
		})
	}) {
		return
	}
	if !t.Run("legacy_publication", func(test *testing.T) {
		adminToken = destinationRefreshAdminToken(test, current)
		client := server.Client()
		request := func(method, path string, body any, want int, result any) {
			data, err := json.Marshal(body)
			nativeMust(test, err, "encode legacy upgrade admin input")
			r, err := http.NewRequestWithContext(test.Context(), method, server.URL+"/api/admin/"+path, bytes.NewReader(data))
			nativeMust(test, err, "create legacy upgrade admin request")
			r.Header.Set("Authorization", "Bearer "+adminToken)
			r.Header.Set("Content-Type", "application/json")
			response, err := client.Do(r)
			nativeMust(test, err, "call legacy upgrade admin HTTP")
			defer response.Body.Close()
			if response.StatusCode != want {
				test.Fatalf("legacy upgrade admin %s returned HTTP %d; body withheld", path, response.StatusCode)
			}
			if result != nil {
				nativeMust(test, json.NewDecoder(response.Body).Decode(result), "decode legacy upgrade admin response")
			}
		}
		input := destinationPolicyInput("Legacy port deny", "block")
		input["inline"] = map[string]any{"ports": strconv.Itoa(targetPort)}
		request("POST", "dest/policies", input, 201, nil)
		request("POST", "dest/publish", nil, 200, nil)
		assertUnchanged(test, 3)
		var status destpolicy.DestinationStatus
		request("GET", "dest/status", nil, 200, &status)
		if len(status.Nodes) != 1 || status.Nodes[0].PanelID != ready.PanelID || status.Nodes[0].State != "unsupported_version" || status.Nodes[0].Supports.Policy || status.Nodes[0].AppliedRules != 0 || status.PublishedGeneration == 0 {
			test.Fatal("legacy node coverage did not require an upgrade")
		}
		metadata, err := current.repos.DestAgentPolicy.Get(test.Context(), ready.AgentID, true)
		nativeMust(test, err, "read legacy empty policy metadata")
		if !nativeLegacyPolicyAbsent(metadata) {
			test.Fatal("legacy node received a policy runtime candidate")
		}
	}) {
		return
	}
	var mixedGateway *nativePanelGateway
	nativeMixedLegacyFleet(t, nativeMixedLegacyFixture{app: current, dir: dir, legacyPanelID: ready.PanelID, userID: ready.UserID, targetAddress: address, targetPort: targetPort, nonce: nonce, adminToken: adminToken, adminURL: server.URL, adminClient: server.Client(), legacyUnchanged: assertUnchanged, retainGateway: func(g *nativePanelGateway) { mixedGateway = g }})
	if t.Failed() {
		return
	}
	nativeLegacyNodeTransitions(t, nativeLegacyTransitionFixture{
		app: current, probe: probe, agentID: ready.AgentID, panelID: ready.PanelID, userID: ready.UserID,
		dataDir: nodeDir, inbound: inbound, targetAddress: address, targetPort: targetPort, nonce: nonce,
		originalConfig: beforeConfig, originalConfigETag: before.Config.ETag,
		adminToken: adminToken, adminURL: server.URL, adminClient: server.Client(), oldBinary: nodeBinary,
		replace: func(binary string) *exec.Cmd {
			stop(&node, nodeDone)
			node = exec.Command(binary, "--endpoint", server.URL+"/v1/node/sync", "--agent-id", ready.AgentID, "--credential-file", credential, "--data-dir", nodeDir, "--xray-api-listen", net.JoinHostPort("127.0.0.1", strconv.Itoa(api)))
			node.Env = append(os.Environ(), "SSL_CERT_FILE="+ca)
			nodeDone = start(node)
			return node
		},
		stop: func() { stop(&node, nodeDone) },
	})
	if t.Failed() {
		return
	}
	nativeLegacyPanelTransitions(t, nativeLegacyPanelFixture{
		app: current, probe: probe, gateway: gateway, dataDir: nodeDir, panelDB: filepath.Join(cfg.DataDir, "panel.db"),
		agentID: ready.AgentID, panelID: ready.PanelID, userID: ready.UserID, parent: node.Process.Pid,
		inbound: inbound, targetAddress: address, targetPort: targetPort, nonce: nonce, originalConfig: beforeConfig,
		adminURL: server.URL, adminToken: adminToken, adminClient: server.Client(),
		stopNode:   func() { stop(&node, nodeDone) },
		resumePeer: mixedGateway.replace,
		stopCandidate: func(test *testing.T) {
			gateway.replace(nil)
			mixedGateway.replace(nil)
			ctx, cancel := context.WithTimeout(test.Context(), 10*time.Second)
			defer cancel()
			nativeMust(test, current.Shutdown(ctx), "drain candidate before PSP downgrade")
			current = nil
		},
		startLegacy: func(test *testing.T) http.Handler {
			legacy = exec.Command(bridge, "-test.run=^TestNativeLegacyPanelBridge$", "-test.timeout=15m")
			legacy.Env = append(os.Environ(), "PSP_NATIVE_LEGACY_CONFIG="+configPath, "PSP_NATIVE_LEGACY_INBOUND="+strconv.Itoa(inbound), "PSP_NATIVE_LEGACY_REUSE=true")
			legacyDone = start(legacy)
			var restarted struct {
				Address, AgentID                 string
				PanelID, NodeID, GroupID, UserID int64
			}
			nativeWait(test, 60*time.Second, "released panel restart ready", func() bool {
				data, err := os.ReadFile(filepath.Join(dir, "legacy-ready-reuse.json"))
				return err == nil && json.Unmarshal(data, &restarted) == nil && restarted.AgentID == ready.AgentID && restarted.PanelID == ready.PanelID && restarted.NodeID == ready.NodeID && restarted.GroupID == ready.GroupID && restarted.UserID == ready.UserID
			})
			host, _, err := net.SplitHostPort(restarted.Address)
			if err != nil || host != "127.0.0.1" {
				test.Fatal("legacy restart escaped its owned listener")
			}
			upstream, err := url.Parse("http://" + restarted.Address)
			nativeMust(test, err, "create restarted legacy handler")
			return httputil.NewSingleHostReverseProxy(upstream)
		},
		stopLegacy: func(test *testing.T) { gateway.replace(nil); stop(&legacy, legacyDone) },
		startCandidate: func(test *testing.T) *App {
			current, err = Build(t.Context(), &cfg)
			nativeMust(test, err, "reopen original downgraded panel database")
			return current
		},
	})
}
