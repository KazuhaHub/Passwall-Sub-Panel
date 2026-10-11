//go:build destination_acceptance && linux

package app

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"syscall"
	"testing"
	"time"

	"github.com/KazuhaHub/passwall-protocol/protocol"
	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/service/destpolicy"
)

type nativeMixedLegacyFixture struct {
	app                         *App
	dir                         string
	legacyPanelID, userID       int64
	targetAddress               string
	targetPort                  int
	nonce, adminToken, adminURL string
	adminClient                 *http.Client
	legacyUnchanged             func(*testing.T, uint64)
	retainGateway               func(*nativePanelGateway)
}

func nativeMixedLegacyFleet(t *testing.T, f nativeMixedLegacyFixture) {
	t.Helper()
	binary := os.Getenv("PSP_NATIVE_MIXED_NODE_BIN")
	info, err := os.Stat(binary)
	if err != nil || !filepath.IsAbs(binary) || filepath.Base(binary) != "destination-mixed-node" || !info.Mode().IsRegular() || info.Mode().Perm()&0022 != 0 {
		t.Fatal("missing immutable mixed-fleet candidate")
	}
	a := f.app
	credential := "fixture-native-mixed-credential-with-enough-entropy"
	digest := sha256.Sum256([]byte(credential))
	agent := &domain.NodeAgent{AgentID: "agt_native_mixed_candidate", Epoch: 1, CredentialSHA256: hex.EncodeToString(digest[:]), DesiredCoreEngine: domain.NodeCoreXray, DesiredCoreVersion: "26.6.27"}
	panel := &domain.Panel{Name: "Native mixed candidate", Kind: domain.PanelKindPSP, URL: "psp://" + agent.AgentID}
	nativeMust(t, a.repos.NativeAgentProvisioning.Create(t.Context(), panel, agent), "create actual mixed candidate")
	nativeMust(t, a.xuiPool.Add(panel), "register mixed candidate")
	inbound, api := nativePort(t), nativePort(t)
	node := &domain.Node{PanelID: panel.ID, InboundID: 1, DisplayName: "Native mixed", ServerAddress: "node.example.test", DesiredProtocol: "vless", DesiredPort: inbound, ObservedProtocol: "vless", ObservedPort: inbound, InboundListen: "127.0.0.1", InboundSettings: `{"decryption":"none"}`, StreamSettings: `{"network":"tcp","security":"none"}`, Sniffing: `{}`, Allocate: `{}`, Enabled: true, ConfigSyncState: domain.ConfigSyncPending}
	nativeMust(t, a.repos.Node.Create(t.Context(), node), "create mixed listener")
	user, err := a.repos.User.GetByID(t.Context(), f.userID)
	nativeMust(t, err, "read original mixed principal")
	client := &domain.PSPClient{UserID: user.ID, PanelID: panel.ID, Email: "mixed@psp.local", UUID: user.UUID, Password: "fixture-native-mixed-password"}
	client.SetDesiredLifecycle(domain.UserLifecycle{Enable: true})
	client.ID, err = a.repos.PSPClient.Create(t.Context(), client)
	nativeMust(t, err, "create second actual attachment")
	nativeMust(t, a.repos.PSPClient.SetInbounds(t.Context(), client.ID, []domain.PSPClientInbound{{ClientID: client.ID, NodeID: node.ID, State: domain.ClientApplyPending}}), "attach second actual listener")
	a.startDestinationAudit()
	// Both TLS gateways terminate at the same production App and SQL pool. Only
	// this gateway is partitioned; legacy-node sync continues on its own socket.
	gateway := &nativePanelGateway{}
	gateway.replace(a.server.Handler)
	if f.retainGateway != nil {
		f.retainGateway(gateway)
	}
	server := httptest.NewTLSServer(gateway)
	t.Cleanup(server.Close)
	ca, key := filepath.Join(f.dir, "mixed-ca.pem"), filepath.Join(f.dir, "mixed-credential")
	nativeMust(t, os.WriteFile(ca, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw}), 0600), "write scoped mixed CA")
	nativeMust(t, os.WriteFile(key, []byte(credential), 0600), "write private mixed credential")
	_, controlAddress, controlPort, controlNonce, _ := nativeTarget(t)
	dataDir := filepath.Join(f.dir, "mixed-node")
	cmd := exec.Command(binary, "--endpoint", server.URL+"/v1/node/sync", "--agent-id", agent.AgentID, "--credential-file", key, "--data-dir", dataDir, "--xray-api-listen", net.JoinHostPort("127.0.0.1", strconv.Itoa(api)))
	cmd.Env = append(os.Environ(), "SSL_CERT_FILE="+ca)
	output := &nativePrivateOutput{}
	cmd.Stdout, cmd.Stderr = output, output
	nativeMust(t, cmd.Start(), "start actual mixed candidate daemon")
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	t.Cleanup(func() {
		_ = cmd.Process.Signal(syscall.SIGTERM)
		select {
		case err := <-done:
			nativeMust(t, err, "drain mixed candidate")
		case <-time.After(15 * time.Second):
			_ = cmd.Process.Kill()
			<-done
			t.Error("mixed candidate did not drain")
		}
	})
	caseTest := t
	request := func(method, path string, body any, result any) {
		data, err := json.Marshal(body)
		nativeMust(caseTest, err, "encode mixed admin input")
		r, err := http.NewRequestWithContext(caseTest.Context(), method, f.adminURL+"/api/admin/"+path, bytes.NewReader(data))
		nativeMust(caseTest, err, "create mixed admin request")
		r.Header.Set("Authorization", "Bearer "+f.adminToken)
		r.Header.Set("Content-Type", "application/json")
		response, err := f.adminClient.Do(r)
		nativeMust(caseTest, err, "call mixed admin HTTP")
		defer response.Body.Close()
		if response.StatusCode != 200 {
			caseTest.Fatalf("mixed admin %s returned HTTP %d; body withheld", path, response.StatusCode)
		}
		if result != nil {
			nativeMust(caseTest, json.NewDecoder(response.Body).Decode(result), "decode mixed admin response")
		} else {
			_, _ = io.Copy(io.Discard, response.Body)
		}
	}
	traffic := func(address string, port int, nonce string, allow bool) {
		caseTest.Helper()
		ctx, cancel := context.WithTimeout(caseTest.Context(), 5*time.Second)
		defer cancel()
		err := nativeVLESSHost(ctx, inbound, address, port, user.UUID, nonce, "mixed.example.test")
		if (err == nil) != allow {
			caseTest.Fatal("mixed candidate real traffic did not match its execution state")
		}
	}
	assertExecution := func(blocked bool) {
		// Independent allowed traffic rules out a dead listener/core as the
		// explanation for a denied connection to the policy's nonce target.
		traffic(controlAddress, controlPort, controlNonce, true)
		traffic(f.targetAddress, f.targetPort, f.nonce, !blocked)
		traffic(controlAddress, controlPort, controlNonce, true)
	}
	readStatus := func() (destpolicy.DestinationStatus, destpolicy.DestinationNodeStatus, destpolicy.DestinationNodeStatus) {
		var status destpolicy.DestinationStatus
		request("GET", "dest/status", nil, &status)
		var legacy, candidate destpolicy.DestinationNodeStatus
		for _, row := range status.Nodes {
			if row.PanelID == f.legacyPanelID {
				legacy = row
			}
			if row.PanelID == panel.ID {
				candidate = row
			}
		}
		if len(status.Nodes) != 2 || legacy.PanelID == 0 || candidate.PanelID == 0 || legacy.State != "unsupported_version" || legacy.Supports.Policy || legacy.AppliedRules != 0 {
			caseTest.Fatal("mixed coverage borrowed a peer's policy capability or execution")
		}
		return status, legacy, candidate
	}
	waitApplied := func(paused bool) {
		after := time.Now().UTC()
		nativeWait(caseTest, 120*time.Second, "mixed current candidate application", func() bool {
			currentAgent, err := a.repos.NodeAgent.GetByAgentID(caseTest.Context(), agent.AgentID)
			if err != nil || currentAgent.LastSeen == nil || !currentAgent.LastSeen.After(after) || !slicesContainsNative(currentAgent.ObservedCapabilities, protocol.CapabilityDestinationPolicy) {
				return false
			}
			for _, name := range []domain.NodeAgentStreamName{domain.NodeAgentStreamConfig, domain.NodeAgentStreamRoster} {
				stream, err := a.repos.NodeAgent.GetStream(caseTest.Context(), agent.AgentID, name)
				if err != nil || !stream.Converged() || stream.AppliedEpoch != currentAgent.Epoch {
					return false
				}
			}
			state, err := a.destDefinitions.State(caseTest.Context())
			if err != nil || state.Paused != paused {
				return false
			}
			policy, err := a.repos.DestAgentPolicy.Get(caseTest.Context(), agent.AgentID, true)
			kind := domain.DestCandidateDesired
			if paused {
				kind = domain.DestCandidatePaused
			}
			return err == nil && policy.MintedKind == kind && policy.MintedGeneration == state.PublishedGeneration && policy.MintedAt != nil && policy.ReportedAt != nil && policy.ReportedState == "applied" && policy.ReportedSHA256 == policy.MintedSHA256 && !policy.FallbackExhausted && (paused || policy.MintedSHA256 != "" && policy.AppliedSHA256 == policy.MintedSHA256 && policy.AppliedRuleCount == 1)
		})
	}
	var candidateLive func()
	if !t.Run("mixed_fleet", func(test *testing.T) {
		caseTest = test
		waitApplied(false)
		candidateLive = nativeBindLiveCore(t, cmd.Process.Pid, dataDir, "xray")
		candidateLive()
		assertExecution(true)
		f.legacyUnchanged(test, 3)
		_, _, candidate := readStatus()
		if !candidate.Supports.Policy || candidate.State != "applied" || candidate.AppliedRules != 1 || candidate.PendingSince != nil {
			test.Fatal("mixed candidate did not confirm its own applied rule")
		}
	}) {
		return
	}
	t.Run("mixed_offline_pause", func(test *testing.T) {
		caseTest = test
		prior, err := a.repos.DestAgentPolicy.Get(test.Context(), agent.AgentID, true)
		nativeMust(test, err, "read mixed pre-partition candidate")
		partition := &nativeSyncPartition{}
		gateway.partitionSync(partition)
		defer gateway.partitionSync(nil)
		request("PUT", "dest/pause", map[string]any{"paused": true}, nil)
		nativeWait(test, 45*time.Second, "mixed single-node offline pause", func() bool {
			status, _, candidate := readStatus()
			attempts, _ := partition.snapshot()
			candidateLive()
			return attempts >= 3 && status.Paused && candidate.State == "offline" && candidate.AppliedRules == 1 && !candidate.Collecting
		})
		current, err := a.repos.DestAgentPolicy.Get(test.Context(), agent.AgentID, true)
		nativeMust(test, err, "read mixed historical offline candidate")
		if current.MintedSHA256 != prior.MintedSHA256 || current.MintedKind != prior.MintedKind || current.AppliedSHA256 != prior.AppliedSHA256 {
			test.Fatal("mixed offline node falsely stopped execution")
		}
		assertExecution(true)
		f.legacyUnchanged(test, 3)
		candidateLive()
		gateway.partitionSync(nil)
		waitApplied(true)
		assertExecution(false)
		_, _, candidate := readStatus()
		if candidate.AppliedRules != 0 || candidate.PendingSince != nil {
			test.Fatal("mixed reconnected node did not apply the pause")
		}
		request("PUT", "dest/pause", map[string]any{"paused": false}, nil)
		waitApplied(false)
		assertExecution(true)
		f.legacyUnchanged(test, 3)
		metadata, err := a.repos.DestAgentPolicy.Get(test.Context(), "agt_native_legacy_upgrade", true)
		nativeMust(test, err, "read mixed legacy empty metadata")
		if !nativeLegacyPolicyAbsent(metadata) {
			test.Fatal("mixed legacy node acquired policy execution material")
		}
	})
}
