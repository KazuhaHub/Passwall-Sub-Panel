//go:build destination_acceptance && linux

package app

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/KazuhaHub/passwall-protocol/protocol"
	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
	"github.com/KazuhaHub/passwall-sub-panel/internal/service/destpolicy"
)

type nativeFleetPeer struct {
	panelID int64
	node    *domain.Node
	agent   *domain.NodeAgent
	capable bool
	inbound int
	probe   *nativeLegacyProbe
	gateway *nativePanelGateway
	process *exec.Cmd
	dir     string
}

// Every profile runs two independent immutable native daemons, then adds both
// real third-party adapters to the same production App.Run and database. All
// administrator mutations use production HTTP and all traffic is raw VLESS.
func TestNativeDestinationFleet(t *testing.T) {
	capabilities, valid := nativeFleetProfile(os.Getenv("PSP_NATIVE_FLEET_PROFILE"))
	if !valid {
		t.Fatal("native fleet requires an exact reviewed cohort")
	}
	binaries := map[bool]string{false: os.Getenv("PSP_NATIVE_FLEET_OLD_BIN"), true: os.Getenv("PSP_NATIVE_FLEET_NEW_BIN")}
	for capable, binary := range binaries {
		want := "destination-fleet-old"
		if capable {
			want = "destination-fleet-new"
		}
		info, err := os.Stat(binary)
		if err != nil || !filepath.IsAbs(binary) || filepath.Base(binary) != want || !info.Mode().IsRegular() || info.Mode().Perm()&0022 != 0 {
			t.Fatal("native fleet candidate is absent or mutable")
		}
	}
	f := buildNativeDestinationPolicyFixture(t) // also enforces disposable/root/closed SQL guards
	a := f.a
	adminToken := destinationRefreshAdminToken(t, a)
	settings, err := a.settings.Load(t.Context(), ports.UISettings{})
	nativeMust(t, err, "read native fleet cadence")
	settings.NodePollSeconds, settings.FullReportSeconds = 2, 0
	nativeMust(t, a.settings.Save(t.Context(), settings), "save native fleet cadence")
	openGroup := &domain.Group{Slug: "fleet-canary", Name: "Fleet canary", TagFilter: domain.TagFilter{All: true}}
	scratchGroup := &domain.Group{Slug: "fleet-provision", Name: "Fleet provision", TagFilter: domain.TagFilter{All: true}}
	for _, group := range []*domain.Group{openGroup, scratchGroup} {
		nativeMust(t, a.repos.Group.Create(t.Context(), group), "create fleet fixture group")
	}
	canary := &domain.User{UPN: "fleet-canary@example.test", Email: "fleet-canary@example.test", SSOProvider: domain.SSOProviderLocal, SSOSubject: "fleet-canary@example.test", Role: domain.RoleUser, Enabled: true, GroupID: openGroup.ID, UUID: "33333333-3333-4333-8333-333333333333", SubToken: "fixture-fleet-canary-token"}
	nativeMust(t, a.repos.User.Create(t.Context(), canary), "create independent fleet control principal")
	credential := "fixture-fleet-second-credential-with-enough-entropy"
	digest := sha256.Sum256([]byte(credential))
	agent := &domain.NodeAgent{AgentID: "agt_native_fleet_second", Epoch: 1, CredentialSHA256: hex.EncodeToString(digest[:]), DesiredCoreEngine: domain.NodeCoreXray, DesiredCoreVersion: "26.6.27"}
	panel := &domain.Panel{Name: "Fleet second", Kind: domain.PanelKindPSP, URL: "psp://" + agent.AgentID}
	nativeMust(t, a.repos.NativeAgentProvisioning.Create(t.Context(), panel, agent), "create independent second fleet panel")
	nativeMust(t, a.xuiPool.Add(panel), "register independent second fleet adapter")
	second := &domain.Node{PanelID: panel.ID, InboundID: 1, DisplayName: "Fleet second", ServerAddress: "fleet.example.test", DesiredProtocol: "vless", ObservedProtocol: "vless", InboundListen: "127.0.0.1", InboundSettings: `{"decryption":"none"}`, StreamSettings: `{"network":"tcp","security":"none"}`, Sniffing: `{}`, Allocate: `{}`, Enabled: true, ConfigSyncState: domain.ConfigSyncPending}
	second.DesiredPort, second.ObservedPort = nativePort(t), 0
	second.ObservedPort = second.DesiredPort
	nativeMust(t, a.repos.Node.Create(t.Context(), second), "create independent second fleet inbound")
	f.node.DesiredPort, f.node.ObservedPort = nativePort(t), 0
	f.node.ObservedPort, f.node.InboundListen = f.node.DesiredPort, "127.0.0.1"
	nativeMust(t, a.repos.Node.Update(t.Context(), f.node), "configure owned primary fleet inbound")
	peers := []*nativeFleetPeer{{panelID: f.agent.PanelID, node: f.node, agent: f.agent, capable: capabilities[0], inbound: f.node.DesiredPort}, {panelID: panel.ID, node: second, agent: agent, capable: capabilities[1], inbound: second.DesiredPort}}
	if peers[0].inbound == peers[1].inbound {
		t.Fatal("fleet peers borrowed the same listener")
	}
	// Run all ordinary production loops, not only the audit worker or explicit callbacks.
	a.server.Addr = net.JoinHostPort("127.0.0.1", strconv.Itoa(nativePort(t)))
	runDone := make(chan error, 1)
	go func() { runDone <- a.Run() }()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		nativeMust(t, a.Shutdown(ctx), "drain complete fleet App.Run")
		select {
		case err := <-runDone:
			if err != nil && err != http.ErrServerClosed {
				t.Error("fleet App.Run ended unexpectedly")
			}
		case <-ctx.Done():
			t.Error("fleet App.Run did not stop")
		}
	})
	nativeWait(t, 30*time.Second, "complete fleet production server", func() bool {
		client := &http.Client{Timeout: time.Second}
		response, err := client.Get("http://" + a.server.Addr + "/health")
		if err != nil {
			return false
		}
		defer response.Body.Close()
		return response.StatusCode == 200
	})
	admin := httptest.NewTLSServer(a.server.Handler)
	t.Cleanup(admin.Close)
	adminClient := admin.Client()
	adminClient.Timeout = 30 * time.Second
	caseTest := t
	request := func(method, path string, body any, codes []int, result any) {
		data, err := json.Marshal(body)
		nativeMust(caseTest, err, "encode fleet administrator input")
		r, err := http.NewRequestWithContext(caseTest.Context(), method, admin.URL+"/api/admin/"+path, bytes.NewReader(data))
		nativeMust(caseTest, err, "create fleet administrator request")
		r.Header.Set("Authorization", "Bearer "+adminToken)
		r.Header.Set("Content-Type", "application/json")
		response, err := adminClient.Do(r)
		nativeMust(caseTest, err, "call actual fleet administrator HTTP")
		defer response.Body.Close()
		matched := false
		for _, code := range codes {
			matched = matched || response.StatusCode == code
		}
		if !matched {
			caseTest.Fatalf("fleet administrator returned HTTP %d; body withheld", response.StatusCode)
		}
		if result != nil {
			nativeMust(caseTest, json.NewDecoder(response.Body).Decode(result), "decode fleet administrator response")
		} else {
			_, _ = io.Copy(io.Discard, response.Body)
		}
	}
	resync := func(user *domain.User, groupID int64) {
		// A real group change drives production membership reconciliation.
		request("PUT", fmt.Sprintf("users/%d", user.ID), map[string]any{"group_id": scratchGroup.ID}, []int{200}, nil)
		request("PUT", fmt.Sprintf("users/%d", user.ID), map[string]any{"group_id": groupID}, []int{200}, nil)
	}
	for i, peer := range peers {
		peer.probe = &nativeLegacyProbe{listener: protocol.NewListenerKey(peer.node.ID)}
		peer.gateway = &nativePanelGateway{}
		peer.gateway.replace(a.server.Handler)
		server := httptest.NewTLSServer(peer.probe.wrap(peer.gateway))
		t.Cleanup(server.Close)
		peer.dir = t.TempDir()
		ca, key := filepath.Join(peer.dir, "ca.pem"), filepath.Join(peer.dir, "credential")
		nativeMust(t, os.WriteFile(ca, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw}), 0600), "write fleet peer scoped CA")
		secret := f.credential
		if i == 1 {
			secret = credential
		}
		nativeMust(t, os.WriteFile(key, []byte(secret), 0600), "write private individual fleet credential")
		peer.process = exec.Command(binaries[peer.capable], "--endpoint", server.URL+"/v1/node/sync", "--agent-id", peer.agent.AgentID, "--credential-file", key, "--data-dir", filepath.Join(peer.dir, "node"), "--xray-api-listen", net.JoinHostPort("127.0.0.1", strconv.Itoa(nativePort(t))))
		peer.process.Env = append(os.Environ(), "SSL_CERT_FILE="+ca)
		output := &nativePrivateOutput{}
		peer.process.Stdout, peer.process.Stderr = output, output
		nativeMust(t, peer.process.Start(), "start immutable individual fleet daemon")
		done := make(chan error, 1)
		go func(process *exec.Cmd) { done <- process.Wait() }(peer.process)
		t.Cleanup(func() {
			_ = peer.process.Process.Signal(syscall.SIGTERM)
			select {
			case err := <-done:
				nativeMust(t, err, "drain fleet peer daemon")
			case <-time.After(15 * time.Second):
				_ = peer.process.Process.Kill()
				<-done
				t.Error("fleet peer did not drain")
			}
		})
	}
	resync(f.user, f.group.ID)
	resync(canary, openGroup.ID)
	_, targetAddress, targetPort, targetNonce, _ := nativeTarget(t)
	_, controlAddress, controlPort, controlNonce, _ := nativeTarget(t)
	_, unlistedAddress, unlistedPort, unlistedNonce, _ := nativeTarget(t)
	policyActive, paused, modeName, stage := false, false, "open", ""
	offlinePanelID := int64(0)
	thirdPanels := []*domain.Panel{}
	thirdNodes := []*domain.Node{}
	memberClients, canaryClients := map[int64]*domain.PSPClient{}, map[int64]*domain.PSPClient{}
	waitPeers := func(after time.Time) {
		facts := map[string]bool{}
		defer func() {
			if caseTest.Failed() {
				for _, category := range []string{"fleet_peer_0_observation", "fleet_peer_1_observation", "fleet_peer_0_streams", "fleet_peer_1_streams", "fleet_peer_0_policy", "fleet_peer_1_policy"} {
					caseTest.Logf("native diagnostic: %s=%t", category, facts[category])
				}
			}
		}()
		nativeWait(caseTest, 120*time.Second, "every individual fleet receipt", func() bool {
			state, err := a.destDefinitions.State(caseTest.Context())
			if err != nil || state.Paused != paused {
				return false
			}
			for index, peer := range peers {
				prefix := fmt.Sprintf("fleet_peer_%d_", index)
				s := peer.probe.snapshot()
				observed, err := a.repos.NodeAgent.GetByAgentID(caseTest.Context(), peer.agent.AgentID)
				if err != nil || observed.LastSeen == nil || !observed.LastSeen.After(after) || observed.Epoch != peer.agent.Epoch || slicesContainsNative(observed.ObservedCapabilities, protocol.CapabilityDestinationPolicy) != peer.capable || s.PolicyCapable != peer.capable || s.CoreEngine != "xray" || s.CoreVersion != "26.6.27" || s.CoreState != "running" || !s.ListenerApplied || !nativePanelStreamsApplied(s, 1) || !peer.capable && s.AgentVersion != "v4.0.1.8" {
					facts[prefix+"observation"] = false
					return false
				}
				facts[prefix+"observation"] = true
				for _, name := range []domain.NodeAgentStreamName{domain.NodeAgentStreamConfig, domain.NodeAgentStreamRoster} {
					stream, err := a.repos.NodeAgent.GetStream(caseTest.Context(), peer.agent.AgentID, name)
					if err != nil || stream == nil || !stream.Converged() || stream.AppliedEpoch != observed.Epoch {
						facts[prefix+"streams"] = false
						return false
					}
				}
				facts[prefix+"streams"] = true
				runtime, err := a.repos.DestAgentPolicy.Get(caseTest.Context(), peer.agent.AgentID, true)
				if err != nil {
					return false
				}
				if !peer.capable {
					if !nativeLegacyPolicyAbsent(runtime) {
						facts[prefix+"policy"] = false
						return false
					}
					facts[prefix+"policy"] = true
					continue
				}
				kind := domain.DestCandidateEmpty
				if policyActive {
					kind = domain.DestCandidateDesired
				}
				if paused {
					kind = domain.DestCandidatePaused
				}
				if runtime == nil || runtime.MintedKind != kind || runtime.MintedGeneration != state.PublishedGeneration || runtime.ReportedAt == nil || runtime.ReportedAt.Before(after) || runtime.ReportedState != "applied" || runtime.ReportedIssue != "" || runtime.ReportedSHA256 != runtime.MintedSHA256 || runtime.FallbackExhausted || runtime.FallbackReason != "" {
					facts[prefix+"policy"] = false
					return false
				}
				if kind == domain.DestCandidateDesired {
					var body *protocol.DestinationPolicy
					if runtime.MintedSHA256 == "" || runtime.AppliedSHA256 != runtime.MintedSHA256 || json.Unmarshal(runtime.MintedBody, &body) != nil || body == nil || protocol.ValidateDestinationPolicy(body) != nil || protocol.PolicyDigest(body) != runtime.MintedSHA256 || !nativeFleetPolicyRules(body, f.group.ID, f.user.ID, targetPort, controlAddress, stage) {
						facts[prefix+"policy"] = false
						return false
					}
				}
				facts[prefix+"policy"] = true
			}
			return true
		})
	}
	traffic := func(inbound int, user *domain.User, address string, port int, nonce string, allowed bool) {
		ctx, cancel := context.WithTimeout(caseTest.Context(), 5*time.Second)
		defer cancel()
		err := nativeVLESSHost(ctx, inbound, address, port, user.UUID, nonce, "fleet.example.test")
		if (err == nil) != allowed {
			caseTest.Fatal("fleet real traffic did not match individual membership and policy")
		}
	}
	assertTraffic := func() {
		var status destpolicy.DestinationStatus
		request("GET", "dest/status", nil, []int{200}, &status)
		rows := make(map[int64]destpolicy.DestinationNodeStatus)
		if len(status.Nodes) != 2+len(thirdPanels) || status.Paused != paused {
			caseTest.Fatal("fleet public status omitted or borrowed an owned peer")
		}
		for _, row := range status.Nodes {
			if row.PanelID <= 0 || rows[row.PanelID].PanelID != 0 {
				caseTest.Fatal("fleet public status duplicated a peer")
			}
			rows[row.PanelID] = row
		}
		for _, peer := range peers {
			row := rows[peer.panelID]
			wantState, rules := "unsupported_version", 0
			if peer.capable {
				wantState = "none"
				if policyActive {
					wantState, rules = "applied", 1
					if modeName == "allowlist" {
						rules = 3
					}
				}
				if paused {
					wantState, rules = "paused", 0
				}
				if offlinePanelID == peer.panelID {
					wantState = "offline"
				}
			}
			if row.Kind != domain.PanelKindPSP || row.Supports.Policy != peer.capable || row.State != wantState || row.AppliedRules != rules {
				caseTest.Fatal("fleet public status borrowed another node's execution")
			}
			delete(rows, peer.panelID)
		}
		for _, panel := range thirdPanels {
			row := rows[panel.ID]
			if row.Kind != panel.Kind || row.State != "unsupported_kind" || row.Supports.Policy || row.Supports.Hits || row.AppliedRules != 0 || row.Collecting {
				caseTest.Fatal("third-party fleet status falsely acquired native execution")
			}
			delete(rows, panel.ID)
		}
		if len(rows) != 0 {
			caseTest.Fatal("fleet public status included an unrelated peer")
		}
		for _, peer := range peers {
			allowed := modeName != "allowlist" || peer.capable
			traffic(peer.inbound, canary, controlAddress, controlPort, controlNonce, true)
			traffic(peer.inbound, f.user, controlAddress, controlPort, controlNonce, allowed)
			traffic(peer.inbound, f.user, targetAddress, targetPort, targetNonce, allowed && (!peer.capable || !policyActive || paused))
			traffic(peer.inbound, f.user, unlistedAddress, unlistedPort, unlistedNonce, allowed && (modeName != "allowlist" || stage != "enforce" || paused))
			traffic(peer.inbound, canary, controlAddress, controlPort, controlNonce, true)
		}
		for _, node := range thirdNodes {
			traffic(node.DesiredPort, canary, controlAddress, controlPort, controlNonce, true)
			traffic(node.DesiredPort, f.user, controlAddress, controlPort, controlNonce, modeName == "open")
			traffic(node.DesiredPort, f.user, targetAddress, targetPort, targetNonce, modeName == "open")
			traffic(node.DesiredPort, canary, controlAddress, controlPort, controlNonce, true)
		}
	}
	captureClients := func(user *domain.User, out map[int64]*domain.PSPClient) {
		clients, err := a.repos.PSPClient.ListByUser(caseTest.Context(), user.ID)
		nativeMust(caseTest, err, "bind stable fleet client identities")
		if len(clients) != 4 {
			caseTest.Fatal("fleet did not provision exactly four independent panel clients")
		}
		for _, client := range clients {
			if client.UUID != user.UUID || out[client.PanelID] != nil {
				caseTest.Fatal("fleet client identity is duplicate or belongs to another principal")
			}
			out[client.PanelID] = client
		}
	}
	waitMembership := func() {
		nativeWait(caseTest, 120*time.Second, "actual fleet member removal and restoration", func() bool {
			for _, peer := range peers {
				snapshot, err := a.repos.NativeDesired.Load(caseTest.Context(), peer.panelID)
				if err != nil || snapshot == nil {
					return false
				}
				stream, err := a.repos.NodeAgent.GetStream(caseTest.Context(), peer.agent.AgentID, domain.NodeAgentStreamRoster)
				var roster protocol.RosterBody
				if err != nil || stream == nil || !stream.Converged() || json.Unmarshal(stream.DesiredBody, &roster) != nil || !nativeAllowlistMembershipMatches(memberClients[peer.panelID], snapshot.Clients, roster, modeName == "open" || peer.capable) || !nativeAllowlistMembershipMatches(canaryClients[peer.panelID], snapshot.Clients, roster, true) {
					return false
				}
			}
			for i, panel := range thirdPanels {
				adapter, err := a.xuiPool.Get(panel.ID)
				if err != nil {
					return false
				}
				members, err := adapter.ListClientInbounds(caseTest.Context())
				if err != nil {
					return false
				}
				canary := canaryClients[panel.ID]
				member := memberClients[panel.ID]
				if canary == nil || member == nil || len(members[canary.Email]) != 1 || members[canary.Email][0] != thirdNodes[i].InboundID {
					return false
				}
				if modeName == "open" {
					if len(members[member.Email]) != 1 || members[member.Email][0] != thirdNodes[i].InboundID {
						return false
					}
				} else if len(members[member.Email]) != 0 {
					return false
				}
			}
			return true
		})
	}
	assertCoverage := func() {
		var detail destpolicy.GroupAccessDetail
		request("GET", fmt.Sprintf("dest/groups/%d", f.group.ID), nil, []int{200}, &detail)
		expected := map[int64]string{thirdPanels[0].ID: "xui", thirdPanels[1].ID: "sui"}
		eligible := 0
		for _, peer := range peers {
			if peer.capable {
				eligible++
			} else {
				expected[peer.panelID] = "capability"
			}
		}
		if detail.Mode != modeName || detail.Stage != stage || detail.TotalNodes != 4 || detail.EligibleNodes != eligible || len(detail.Ineligible) != len(expected) {
			caseTest.Fatal("fleet public coverage borrowed capability from another panel")
		}
		for _, row := range detail.Ineligible {
			if expected[row.PanelID] != row.Reason || row.Reason == "" {
				caseTest.Fatal("fleet public coverage lost an individual exclusion")
			}
			delete(expected, row.PanelID)
		}
		if len(expected) != 0 {
			caseTest.Fatal("fleet public coverage omitted an owned peer")
		}
	}
	step := func(name string, fn func()) bool { return t.Run(name, func(test *testing.T) { caseTest = test; fn() }) }
	publish := func() { request("POST", "dest/publish", nil, []int{200}, nil) }
	if !step("native_ready", func() {
		waitPeers(time.Now())
		assertTraffic()
		for _, peer := range peers {
			wrong := *f.user
			wrong.UUID = "66666666-6666-4666-8666-666666666666"
			traffic(peer.inbound, &wrong, controlAddress, controlPort, controlNonce, false)
		}
	}) {
		return
	}
	if !step("native_publication", func() {
		after := time.Now()
		input := destinationPolicyInput("Fleet port deny", "block")
		input["inline"] = map[string]any{"ports": strconv.Itoa(targetPort)}
		request("POST", "dest/policies", input, []int{201}, nil)
		publish()
		policyActive = true
		waitPeers(after)
		assertTraffic()
	}) {
		return
	}
	for _, change := range []struct {
		name   string
		paused bool
	}{{"native_pause", true}, {"native_resume", false}} {
		if !step(change.name, func() {
			after := time.Now()
			request("PUT", "dest/pause", map[string]any{"paused": change.paused}, []int{200}, nil)
			paused = change.paused
			waitPeers(after)
			assertTraffic()
		}) {
			return
		}
	}
	if !step("third_party_ready", func() {
		backend := nativeStartFleetBackends(caseTest, t)
		for i, selection := range []struct{ kind, endpoint, token, version string }{{"3xui", backend.xuiURL, backend.xuiToken, "3.9.0"}, {"sui", backend.suiURL, backend.suiToken, "1.6.3"}} {
			var result struct {
				ID int64 `json:"id"`
			}
			request("POST", "servers", map[string]any{"panel_type": selection.kind, "name": "Fleet " + selection.kind, "url": selection.endpoint, "api_token": selection.token, "auth_method": "token"}, []int{201}, &result)
			panel, err := a.repos.XUIPanel.GetByID(caseTest.Context(), result.ID)
			nativeMust(caseTest, err, "read real enrolled fleet panel")
			adapter, err := a.xuiPool.Get(panel.ID)
			nativeMust(caseTest, err, "read production fleet adapter")
			status, err := adapter.GetServerStatus(caseTest.Context())
			nativeMust(caseTest, err, "verify real third-party panel version")
			if strings.TrimPrefix(status.PanelVersion, "v") != selection.version {
				caseTest.Fatal("fleet panel is not the exact reviewed version")
			}
			name, port := "Fleet "+selection.kind+" inbound", 24445+i
			request("POST", "nodes", map[string]any{"panel_id": panel.ID, "display_name": name, "server_address": backend.address, "region": "fixture", "inbound": map[string]any{"remark": name, "enable": true, "listen": "127.0.0.1", "port": port, "protocol": "vless", "settings": `{"clients":[],"decryption":"none"}`, "stream_settings": `{"network":"tcp","security":"none"}`, "sniffing": `{}`, "allocate": `{}`}}, []int{201, 202}, nil)
			var created *domain.Node
			nativeWait(caseTest, 120*time.Second, "real third-party owned inbound", func() bool {
				nodes, err := a.repos.Node.List(caseTest.Context())
				if err != nil {
					return false
				}
				for _, node := range nodes {
					if node.PanelID == panel.ID && node.DisplayName == name && node.DesiredPort == port && node.ConfigSyncState == domain.ConfigSyncSynced {
						created = node
						return true
					}
				}
				return false
			})
			thirdPanels, thirdNodes = append(thirdPanels, panel), append(thirdNodes, created)
		}
		resync(f.user, f.group.ID)
		resync(canary, openGroup.ID)
		waitPeers(time.Now())
		captureClients(f.user, memberClients)
		captureClients(canary, canaryClients)
		waitMembership()
		assertTraffic()
		for _, node := range thirdNodes {
			wrong := *f.user
			wrong.UUID = "66666666-6666-4666-8666-666666666666"
			traffic(node.DesiredPort, &wrong, controlAddress, controlPort, controlNonce, false)
		}
	}) {
		return
	}
	var mode destinationGroupModeAPIResult
	groupPath := fmt.Sprintf("dest/groups/%d", f.group.ID)
	if !step("allowlist_trial", func() {
		after := time.Now()
		request("PUT", groupPath, map[string]any{"mode": "allowlist", "stage": "trial", "list_ids": []int64{}, "updated_at": int64(0), "base_hosts": []string{}}, []int{200}, &mode)
		request("POST", fmt.Sprintf("dest/lists/%d/entries", mode.ExtraListID), map[string]any{"add": []string{controlAddress + "/128"}}, []int{200}, nil)
		publish()
		modeName, stage = "allowlist", "trial"
		waitPeers(after)
		waitMembership()
		assertCoverage()
		assertTraffic()
	}) {
		return
	}
	if !step("allowlist_enforce", func() {
		after := time.Now()
		request("PUT", groupPath, map[string]any{"mode": "allowlist", "stage": "enforce", "list_ids": []int64{}, "updated_at": mode.UpdatedAt}, []int{200}, &mode)
		publish()
		stage = "enforce"
		waitPeers(after)
		waitMembership()
		assertCoverage()
		assertTraffic()
	}) {
		return
	}
	if !step("one_peer_offline", func() {
		index := 0
		if !peers[index].capable && peers[1].capable {
			index = 1
		}
		offline, online := peers[index], peers[1-index]
		live := nativeBindLiveCore(caseTest, offline.process.Process.Pid, filepath.Join(offline.dir, "node"), "xray")
		partition := &nativeSyncPartition{}
		offline.gateway.partitionSync(partition)
		offlinePanelID = offline.panelID
		defer func() { offline.gateway.partitionSync(nil); offlinePanelID = 0 }()
		// The first failed serial sync drains any prior successful request.
		nativeWait(caseTest, 15*time.Second, "fleet sync partition barrier", func() bool { attempts, _ := partition.snapshot(); return attempts >= 1 })
		firstBlocked, _ := partition.snapshot()
		before, err := a.repos.NodeAgent.GetByAgentID(caseTest.Context(), offline.agent.AgentID)
		nativeMust(caseTest, err, "bind post-barrier offline peer heartbeat")
		after := time.Now()
		nativeWait(caseTest, 45*time.Second, "one isolated peer and independently fresh peer", func() bool {
			attempts, _ := partition.snapshot()
			old, err := a.repos.NodeAgent.GetByAgentID(caseTest.Context(), offline.agent.AgentID)
			if err != nil {
				return false
			}
			fresh, err := a.repos.NodeAgent.GetByAgentID(caseTest.Context(), online.agent.AgentID)
			live()
			return err == nil && attempts >= firstBlocked+3 && old.LastSeen != nil && before.LastSeen != nil && !old.LastSeen.After(*before.LastSeen) && fresh.LastSeen != nil && fresh.LastSeen.After(after)
		})
		assertTraffic()
		assertCoverage()
		live()
		offline.gateway.partitionSync(nil)
		offlinePanelID = 0
		waitPeers(time.Now())
		waitMembership()
		assertTraffic()
	}) {
		return
	}
	if !step("allowlist_pause", func() {
		after := time.Now()
		request("PUT", "dest/pause", map[string]any{"paused": true}, []int{200}, nil)
		paused = true
		waitPeers(after)
		waitMembership()
		assertCoverage()
		assertTraffic()
	}) {
		return
	}
	step("allowlist_reopen", func() {
		after := time.Now()
		request("PUT", groupPath, map[string]any{"mode": "open", "stage": "", "list_ids": []int64{}, "updated_at": mode.UpdatedAt}, []int{200}, &mode)
		request("PUT", "dest/pause", map[string]any{"paused": false}, []int{200}, nil)
		publish()
		modeName, stage, paused = "open", "", false
		waitPeers(after)
		waitMembership()
		assertTraffic()
		for _, principal := range []struct {
			user     *domain.User
			original map[int64]*domain.PSPClient
		}{{f.user, memberClients}, {canary, canaryClients}} {
			current := make(map[int64]*domain.PSPClient)
			captureClients(principal.user, current)
			for panelID, before := range principal.original {
				after := current[panelID]
				if after == nil || after.ID != before.ID || after.UUID != before.UUID || !after.CreatedAt.Equal(before.CreatedAt) || after.LifetimeUpBytes < before.LifetimeUpBytes || after.LifetimeDownBytes < before.LifetimeDownBytes || after.LifetimeTotalBytes < before.LifetimeTotalBytes {
					caseTest.Fatal("fleet reopen replaced a retired client identity or counters")
				}
			}
		}
	})
}
