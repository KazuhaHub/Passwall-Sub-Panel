//go:build destination_acceptance && linux

package app

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
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
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/KazuhaHub/passwall-protocol/protocol"
	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
	"github.com/KazuhaHub/passwall-sub-panel/internal/service/destpolicy"
)

// This driver launches the real candidate daemon against Build's production
// TLS sync handler. Neither reports nor applied receipts are synthesized.
// The only network mutation is one nonce-owned documentation /128 on loopback.
func TestNativeDestinationTraffic(t *testing.T) {
	if os.Geteuid() != 0 || os.Getenv("GITHUB_ACTIONS") != "true" || os.Getenv("RUNNER_ENVIRONMENT") != "github-hosted" || os.Getenv("GITHUB_REPOSITORY") != "KazuhaHub/Passwall-Sub-Panel" {
		t.Fatal("native destination acceptance requires the disposable hosted Linux workflow")
	}
	binary, version := os.Getenv("PSP_NATIVE_NODE_BIN"), os.Getenv("PSP_NATIVE_CORE_VERSION")
	if !filepath.IsAbs(binary) || !strings.HasPrefix(filepath.Base(binary), "destination-node") || !slicesContainsNative([]string{"26.6.27", "26.7.28", "26.9.9"}, version) {
		t.Fatal("missing reviewed candidate or exact core selection")
	}
	info, err := os.Stat(binary)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0022 != 0 {
		t.Fatal("candidate executable is absent or writable by other users")
	}
	f := buildDestinationPolicyFixture(t)
	a := f.a
	token := destinationRefreshAdminToken(t, a)
	// A second real principal distinguishes successful account revocation from
	// an unavailable core. Both attachments are provisioned before the daemon.
	canary := &domain.User{UPN: "canary@example.test", Email: "canary@example.test", SSOProvider: domain.SSOProviderLocal, SSOSubject: "canary@example.test", Role: domain.RoleUser, Enabled: true, GroupID: f.group.ID, UUID: "33333333-3333-4333-8333-333333333333", SubToken: "fixture-native-canary-token"}
	nativeMust(t, a.repos.User.Create(t.Context(), canary), "create canary principal")
	canaryClient := &domain.PSPClient{UserID: canary.ID, PanelID: f.agent.PanelID, Email: "canary@psp.local", UUID: canary.UUID, Password: "fixture-native-canary-password"}
	canaryClient.SetDesiredLifecycle(domain.UserLifecycle{Enable: true})
	canaryClient.ID, err = a.repos.PSPClient.Create(t.Context(), canaryClient)
	nativeMust(t, err, "create canary client")
	nativeMust(t, a.repos.PSPClient.SetInbounds(t.Context(), canaryClient.ID, []domain.PSPClientInbound{{ClientID: canaryClient.ID, NodeID: f.node.ID, State: domain.ClientApplyPending}}), "attach canary client")
	settings, err := a.settings.Load(t.Context(), ports.UISettings{})
	nativeMust(t, err, "read cadence")
	settings.NodePollSeconds, settings.FullReportSeconds = 2, 0
	nativeMust(t, a.settings.Save(t.Context(), settings), "save live cadence")
	// Provisioning uses the same reviewed catalog check as ordinary native nodes.
	if version != f.agent.DesiredCoreVersion {
		// The reviewed 26.9.9 row requires explicit acknowledgement even for
		// this plain VLESS listener. Never weaken the production catalog gate.
		nativeMust(t, a.repos.NodeAgent.UpdateCoreSelection(t.Context(), f.agent.AgentID, domain.NodeCoreXray, version, version == "26.9.9"), "select reviewed core")
	}
	inbound, api := nativePort(t), nativePort(t)
	f.node.DesiredPort, f.node.ObservedPort = inbound, inbound
	f.node.InboundListen = "127.0.0.1"
	const fullSniffing = `{"enabled":true,"destOverride":["http","tls","quic"],"routeOnly":true}`
	const metadataSniffing = `{"enabled":true,"destOverride":["http","tls","quic"],"routeOnly":true,"metadataOnly":true}`
	const nativeDomain = "fallback.example.test"
	f.node.Sniffing = fullSniffing
	nativeMust(t, a.repos.Node.Update(t.Context(), f.node), "configure disposable listener")
	a.startDestinationAudit()
	probe := &nativeRestartProbe{}
	gateway := &nativePanelGateway{}
	gateway.replace(a.server.Handler)
	server := httptest.NewTLSServer(probe.wrap(gateway))
	t.Cleanup(server.Close)
	dir := t.TempDir()
	ca, credential := filepath.Join(dir, "ca.pem"), filepath.Join(dir, "credential")
	nativeMust(t, os.WriteFile(ca, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw}), 0600), "write scoped fixture CA")
	nativeMust(t, os.WriteFile(credential, []byte(f.credential), 0600), "write private fixture credential")
	_, address, targetPort, nonce, requests := nativeTarget(t)
	output := &nativePrivateOutput{}
	var cmd *exec.Cmd
	var done chan error
	startDaemon := func(test *testing.T) {
		cmd = exec.Command(binary, "--endpoint", server.URL+"/v1/node/sync", "--agent-id", f.agent.AgentID, "--credential-file", credential, "--data-dir", filepath.Join(dir, "node"), "--xray-api-listen", net.JoinHostPort("127.0.0.1", strconv.Itoa(api)))
		cmd.Env = append(os.Environ(), "SSL_CERT_FILE="+ca)
		cmd.Stdout, cmd.Stderr = output, output
		nativeMust(test, cmd.Start(), "start candidate Node")
		done = make(chan error, 1)
		go func(process *exec.Cmd, completion chan error) { completion <- process.Wait() }(cmd, done)
	}
	stopDaemon := func(test *testing.T) {
		if cmd == nil {
			return
		}
		_ = cmd.Process.Signal(syscall.SIGTERM)
		select {
		case <-done:
		case <-time.After(15 * time.Second):
			_ = cmd.Process.Kill()
			<-done
			test.Error("candidate Node did not drain its core and collectors")
		}
		cmd = nil
	}
	receiptAfter := time.UnixMilli(time.Now().UnixMilli())
	startDaemon(t)
	t.Cleanup(func() {
		stopDaemon(t)
		text := output.text()
		for _, private := range []string{f.credential, f.user.Email, canary.Email, canaryClient.Email, "destination@psp.local", " accepted ", " rejected "} {
			if strings.Contains(text, private) {
				t.Error("private access data escaped into Node output; content withheld")
			}
		}
	})
	client := server.Client()
	client.Timeout = 10 * time.Second
	caseTest := t
	request := func(method, path string, body any, want int, result any) {
		caseTest.Helper()
		encoded, err := json.Marshal(body)
		nativeMust(caseTest, err, "encode administrator input")
		r, err := http.NewRequestWithContext(caseTest.Context(), method, server.URL+"/api/admin/"+path, bytes.NewReader(encoded))
		nativeMust(caseTest, err, "create administrator request")
		r.Header.Set("Authorization", "Bearer "+token)
		r.Header.Set("Content-Type", "application/json")
		started := time.UnixMilli(time.Now().UnixMilli())
		response, err := client.Do(r)
		nativeMust(caseTest, err, "call real administrator HTTP")
		defer response.Body.Close()
		if response.StatusCode != want {
			caseTest.Fatalf("administrator route %s returned HTTP %d, expected %d; body withheld", path, response.StatusCode, want)
		}
		if method != "GET" && path != "dest/publish" {
			receiptAfter = started
		}
		if result != nil {
			nativeMust(caseTest, json.NewDecoder(response.Body).Decode(result), "decode administrator response")
		} else {
			_, _ = io.Copy(io.Discard, response.Body)
		}
	}
	publish := func() { request("POST", "dest/publish", nil, 200, nil) }
	setInbound := func(sniffing, stream string) {
		request("PUT", fmt.Sprintf("nodes/%d/inbound", f.node.ID), map[string]any{"remark": "Native acceptance", "enable": true, "listen": "127.0.0.1", "port": inbound, "protocol": "vless", "settings": f.node.InboundSettings, "stream_settings": stream, "sniffing": sniffing, "allocate": f.node.Allocate}, 204, nil)
	}
	setSniffing := func(sniffing string) { setInbound(sniffing, f.node.StreamSettings) }
	streamsReady := func(agent *domain.NodeAgent) bool {
		for _, name := range []domain.NodeAgentStreamName{domain.NodeAgentStreamConfig, domain.NodeAgentStreamRoster} {
			stream, err := a.repos.NodeAgent.GetStream(caseTest.Context(), f.agent.AgentID, name)
			if err != nil || stream.AppliedEpoch != agent.Epoch || !stream.Converged() {
				return false
			}
		}
		return true
	}
	waitCandidate := func(kind domain.DestCandidateKind, exhausted, fault bool, after time.Time) {
		caseTest.Helper()
		defer func() {
			if caseTest.Failed() {
				for _, category := range []string{"download", "checksum", "timeout", "rejected", "unsupported", "permission", "bind", "certificate"} {
					caseTest.Logf("native diagnostic: %s=%t", category, strings.Contains(strings.ToLower(output.text()), category))
				}
			}
		}()
		nativeWait(caseTest, 120*time.Second, "current policy receipt", func() bool {
			state, err := a.destDefinitions.State(caseTest.Context())
			if err != nil {
				return false
			}
			r, err := a.repos.DestAgentPolicy.Get(caseTest.Context(), f.agent.AgentID, true)
			if err != nil {
				return false
			}
			agent, err := a.repos.NodeAgent.GetByAgentID(caseTest.Context(), f.agent.AgentID)
			if err != nil {
				return false
			}
			if fault {
				if !nativeFaultReceiptMatches(*r, state.PublishedGeneration, agent.LastSeen, after, kind, exhausted) || len(r.PrecheckListeners) != 1 || r.PrecheckListeners[0] != string(protocol.NewListenerKey(f.node.ID)) {
					return false
				}
			} else if !nativeReceiptMatches(*r, state.PublishedGeneration, agent.LastSeen, after) {
				return false
			}
			// An empty policy may be acknowledged before the core download and
			// roster installation. Policy receipt alone cannot prove traffic ready.
			return streamsReady(agent)
		})
	}
	waitApplied := func() { waitCandidate("", false, false, receiptAfter) }
	waitFault := func(kind domain.DestCandidateKind, exhausted bool) {
		waitCandidate(kind, exhausted, true, receiptAfter)
	}
	trafficUserHost := func(uuid, host string, allowed bool) {
		caseTest.Helper()
		before := requests.Load()
		err := nativeVLESSHost(caseTest.Context(), inbound, address, targetPort, uuid, nonce, host)
		if allowed && err != nil {
			caseTest.Fatal("real VLESS traffic did not reach its nonce target; private diagnostics withheld")
		}
		if !allowed && err == nil {
			caseTest.Fatal("real VLESS traffic bypassed a deny rule")
		}
		want := before
		if allowed {
			want++
		}
		if requests.Load() != want {
			caseTest.Fatal("deny/allow result disagreed with actual target request count")
		}
	}
	trafficUser := func(uuid string, allowed bool) { trafficUserHost(uuid, "["+address+"]", allowed) }
	traffic := func(allowed bool) { trafficUser(f.user.UUID, allowed) }
	trafficDomain := func(allowed bool) { trafficUserHost(f.user.UUID, nativeDomain, allowed) }
	count := func(query string, args ...any) int64 {
		caseTest.Helper()
		var n int64
		nativeMust(caseTest, a.database.QueryRowContext(caseTest.Context(), query, args...).Scan(&n), "read private count")
		return n
	}
	waitHit := func(source, action string, user int64, port int) {
		caseTest.Helper()
		nativeWait(caseTest, 30*time.Second, "real hit ingestion", func() bool {
			return count("SELECT COALESCE(SUM(count),0) FROM dest_hits WHERE panel_id=? AND user_id=? AND source=? AND action=? AND port=?", f.agent.PanelID, user, source, action, port) > 0
		})
	}
	var policy destinationPolicyAPIResult
	var mode destinationGroupModeAPIResult
	verifyCandidate := func(wantRules int, keepLKG string, portSource int64) *domain.DestAgentPolicy {
		caseTest.Helper()
		r, err := a.repos.DestAgentPolicy.Get(caseTest.Context(), f.agent.AgentID, true)
		nativeMust(caseTest, err, "read exact fault candidate")
		var compiled *protocol.DestinationPolicy
		nativeMust(caseTest, json.Unmarshal(r.MintedBody, &compiled), "decode exact fault candidate")
		if compiled == nil || protocol.ValidateDestinationPolicy(compiled) != nil || protocol.PolicyDigest(compiled) != r.MintedSHA256 || len(compiled.Rules) != wantRules || compiled.Collect != protocol.CollectHitsAndUsage {
			caseTest.Fatal("fault candidate did not retain its exact rules and collection mode")
		}
		canonical, err := json.Marshal(compiled)
		nativeMust(caseTest, err, "encode canonical fault candidate")
		if !bytes.Equal(canonical, r.MintedBody) || keepLKG != "" && r.AppliedSHA256 != keepLKG {
			caseTest.Fatal("fault candidate changed its canonical body or historical LKG")
		}
		var status destpolicy.DestinationStatus
		request("GET", "dest/status", nil, 200, &status)
		if len(status.Nodes) != 1 {
			caseTest.Fatal("status API did not expose the exact native node")
		}
		n := status.Nodes[0]
		wantState := "applied"
		if r.FallbackReason == "sniffing" {
			wantState = "sniffing"
		}
		if r.MintedKind == domain.DestCandidatePaused {
			wantState = "paused"
		}
		if n.PanelID != f.agent.PanelID || n.MintedKind != r.MintedKind || n.FallbackReason != r.FallbackReason || n.FallbackExhausted != r.FallbackExhausted || n.AppliedRules != wantRules || n.PendingSince != nil || n.AppliedAt == nil || n.State != wantState || status.Paused != (r.MintedKind == domain.DestCandidatePaused) {
			caseTest.Fatal("status API disagreed with the currently acknowledged candidate")
		}
		if portSource > 0 {
			if len(compiled.Rules) != 1 {
				caseTest.Fatal("port fallback contains unexpected rules")
			}
			rule := compiled.Rules[0]
			source, _, _ := strings.Cut(rule.ID, "x")
			if source != fmt.Sprintf("p%d", portSource) || rule.Action != protocol.RuleBlock || rule.Ports != strconv.Itoa(targetPort) || len(rule.Domains) != 0 || len(rule.Protocols) != 0 || len(rule.CIDRs) != 0 || rule.CatchAll {
				caseTest.Fatal("fallback did not execute the confirmed port-only rule")
			}
		}
		return r
	}
	run := func(name string, work func()) {
		if !t.Run(name, func(child *testing.T) { caseTest = child; defer func() { caseTest = t }(); work() }) {
			t.FailNow()
		}
	}
	run("empty", func() {
		waitApplied()
		traffic(true)
		if count("SELECT COUNT(*) FROM dest_hits") != 0 || count("SELECT COUNT(*) FROM dest_usage_hourly") != 0 {
			caseTest.Fatal("empty policy collected private history")
		}
	})
	input := destinationPolicyInput("Native traffic fixture", "observe")
	input["inline"] = map[string]any{"ports": strconv.Itoa(targetPort)}
	run("watch", func() {
		request("POST", "dest/policies", input, 201, &policy)
		publish()
		waitApplied()
		traffic(true)
		waitHit(fmt.Sprintf("p%d", policy.ID), "observe", f.user.ID, targetPort)
	})
	run("block", func() {
		input["action"], input["updated_at"] = "block", policy.UpdatedAt
		request("PUT", fmt.Sprintf("dest/policies/%d", policy.ID), input, 200, &policy)
		publish()
		waitApplied()
		traffic(false)
		waitHit(fmt.Sprintf("p%d", policy.ID), "block", f.user.ID, targetPort)
	})
	run("paused", func() {
		request("PUT", "dest/pause", map[string]any{"paused": true}, 200, nil)
		waitApplied()
		traffic(true)
		request("DELETE", fmt.Sprintf("dest/policies/%d", policy.ID), nil, 204, nil)
		request("PUT", "dest/pause", map[string]any{"paused": false}, 200, nil)
		publish()
		waitApplied()
	})
	groupPath := fmt.Sprintf("dest/groups/%d", f.group.ID)
	run("trial", func() {
		request("PUT", groupPath, map[string]any{"mode": "allowlist", "stage": "trial", "list_ids": []int64{}, "updated_at": int64(0), "base_hosts": []string{}}, 200, &mode)
		publish()
		waitApplied()
		traffic(true)
		waitHit(fmt.Sprintf("g%d", f.group.ID), "observe", 0, 0)
		if count("SELECT COUNT(*) FROM dest_hits WHERE source=? AND action='observe' AND (user_id<>0 OR port<>0 OR dest<>'(ip)')", fmt.Sprintf("g%d", f.group.ID)) != 0 {
			caseTest.Fatal("real trial report lost anonymity")
		}
	})
	run("enforce", func() {
		request("PUT", groupPath, map[string]any{"mode": "allowlist", "stage": "enforce", "list_ids": []int64{}, "updated_at": mode.UpdatedAt}, 200, &mode)
		publish()
		waitApplied()
		traffic(false)
		waitHit(fmt.Sprintf("g%d", f.group.ID), "block", f.user.ID, targetPort)
	})
	run("extra", func() {
		request("POST", fmt.Sprintf("dest/lists/%d/entries", mode.ExtraListID), map[string]any{"add": []string{address + "/128"}}, 200, nil)
		publish()
		waitApplied()
		traffic(true)
	})
	run("listener_rejected", func() {
		prior, err := a.repos.DestAgentPolicy.Get(caseTest.Context(), f.agent.AgentID, true)
		nativeMust(caseTest, err, "read policy before listener failure")
		confirmed, err := a.repos.NodeAgent.GetStream(caseTest.Context(), f.agent.AgentID, domain.NodeAgentStreamConfig)
		nativeMust(caseTest, err, "read config before listener failure")
		if !confirmed.Converged() {
			caseTest.Fatal("listener failure fixture has no confirmed config")
		}
		members := func() map[int64]int64 {
			caseTest.Helper()
			keys := map[int64]int64{}
			for _, user := range []*domain.User{f.user, canary} {
				clients, err := a.repos.PSPClient.ListByUser(caseTest.Context(), user.ID)
				nativeMust(caseTest, err, "read real allowlist member")
				for _, c := range clients {
					if c.PanelID != f.agent.PanelID {
						continue
					}
					attachments, err := a.repos.PSPClient.ListInbounds(caseTest.Context(), c.ID)
					nativeMust(caseTest, err, "read real member attachments")
					if !c.DesiredEnable || c.UUID != user.UUID || len(attachments) != 1 || attachments[0].NodeID != f.node.ID || keys[user.ID] != 0 {
						caseTest.Fatal("listener failure changed allowlist client membership")
					}
					keys[user.ID] = c.ID
				}
				if keys[user.ID] == 0 {
					caseTest.Fatal("listener failure retired an allowlist member")
				}
			}
			return keys
		}
		before := members()
		// Valid JSON passes the production listener compiler. Real Xray's
		// -test rejects ALPN's numeric type, both with and without the policy.
		setInbound(fullSniffing, `{"network":"tcp","security":"tls","tlsSettings":{"alpn":5}}`)
		failedAfter := receiptAfter
		observed := map[string]bool{}
		defer func() {
			if caseTest.Failed() {
				for _, category := range []string{"listener_issue", "listener_core_rejected", "listener_agent_fresh", "listener_policy_ready", "listener_policy_unchanged", "listener_epoch", "listener_config_changed", "listener_config_preserved", "listener_config_fresh"} {
					caseTest.Logf("native diagnostic: %s=%t", category, observed[category])
				}
			}
		}()
		nativeWait(caseTest, 120*time.Second, "real listener rejection", func() bool {
			issues, _, err := a.repos.NodeAgentIssue.List(caseTest.Context(), ports.NodeAgentIssueFilter{AgentID: f.agent.AgentID, Code: "core_convergence_failed", Pagination: ports.Pagination{Page: 1, PageSize: 50}})
			if err != nil {
				return false
			}
			coreRejected := false
			for _, issue := range issues {
				observed["listener_issue"] = observed["listener_issue"] || issue.LastSeenAt.After(failedAfter)
				coreRejected = coreRejected || issue.LastSeenAt.After(failedAfter) && strings.Contains(issue.Detail, "core candidate rejected")
			}
			observed["listener_core_rejected"] = coreRejected
			current, err := a.repos.DestAgentPolicy.Get(caseTest.Context(), f.agent.AgentID, true)
			if err != nil {
				return false
			}
			cfg, err := a.repos.NodeAgent.GetStream(caseTest.Context(), f.agent.AgentID, domain.NodeAgentStreamConfig)
			if err != nil {
				return false
			}
			agent, err := a.repos.NodeAgent.GetByAgentID(caseTest.Context(), f.agent.AgentID)
			if err != nil {
				return false
			}
			observed["listener_agent_fresh"] = agent.LastSeen != nil && agent.LastSeen.After(failedAfter)
			observed["listener_policy_ready"] = nativeReceiptMatches(*current, prior.MintedGeneration, agent.LastSeen, *prior.MintedAt)
			observed["listener_policy_unchanged"] = current.MintedSHA256 == prior.MintedSHA256 && current.DesiredSHA256 == prior.DesiredSHA256 && current.AppliedSHA256 == prior.AppliedSHA256 && current.ReportedSHA256 == prior.ReportedSHA256 && bytes.Equal(current.MintedBody, prior.MintedBody) && bytes.Equal(current.AppliedBody, prior.AppliedBody)
			observed["listener_epoch"] = cfg.AppliedEpoch == agent.Epoch
			observed["listener_config_changed"] = cfg.DesiredETag != "" && cfg.DesiredETag != confirmed.DesiredETag
			observed["listener_config_preserved"] = cfg.AppliedETag == confirmed.DesiredETag && !cfg.Converged()
			observed["listener_config_fresh"] = cfg.LastSeen != nil && cfg.LastSeen.After(failedAfter)
			return coreRejected && nativeListenerFaultReceiptMatches(*prior, *current, *cfg, confirmed.DesiredETag, agent.Epoch, agent.LastSeen, failedAfter)
		})
		for user, key := range members() {
			if before[user] != key {
				caseTest.Fatal("listener failure replaced an allowlist client")
			}
		}
		var status destpolicy.DestinationStatus
		request("GET", "dest/status", nil, 200, &status)
		if len(status.Nodes) != 1 || status.Nodes[0].State != "applied" || status.Nodes[0].FallbackReason != "" || status.Nodes[0].FallbackExhausted || status.Nodes[0].MintedKind != domain.DestCandidateDesired || status.Nodes[0].PendingSince != nil {
			caseTest.Fatal("listener failure was presented as a policy fault")
		}
		var inbox struct {
			Items []domain.NodeAgentIssue `json:"items"`
		}
		request("GET", "node-issues?agent_id="+f.agent.AgentID+"&code=core_convergence_failed", nil, 200, &inbox)
		if len(inbox.Items) == 0 {
			caseTest.Fatal("real listener rejection did not reach the administrator inbox")
		}
		traffic(true)
		trafficUser(canary.UUID, true)
		setSniffing(fullSniffing)
		// A/B/A restores the original bytes. The policy timestamp need not
		// change; require a fresh accepted report and current stream convergence.
		repairedAfter := receiptAfter
		nativeWait(caseTest, 120*time.Second, "listener repair application", func() bool {
			agent, err := a.repos.NodeAgent.GetByAgentID(caseTest.Context(), f.agent.AgentID)
			return err == nil && agent.LastSeen != nil && agent.LastSeen.After(repairedAfter) && streamsReady(agent)
		})
		waitCandidate("", false, false, *prior.MintedAt)
		traffic(true)
	})
	run("collection_off", func() {
		request("PUT", fmt.Sprintf("servers/%d", f.agent.PanelID), map[string]any{"audit_collect": "off"}, 200, nil)
		waitApplied()
		before := count("SELECT COALESCE(SUM(count),0) FROM dest_hits")
		traffic(true)
		// Three real poll cycles allow any unexpected batch to enter the queue.
		time.Sleep(7 * time.Second)
		if count("SELECT COALESCE(SUM(count),0) FROM dest_hits") != before || count("SELECT COUNT(*) FROM dest_usage_hourly") != 0 {
			caseTest.Fatal("off admitted new audit data or erased history")
		}
	})
	run("usage", func() {
		request("PUT", fmt.Sprintf("servers/%d", f.agent.PanelID), map[string]any{"audit_collect": "hits_and_usage"}, 200, nil)
		waitApplied()
		traffic(true)
		nativeWait(caseTest, 30*time.Second, "real usage ingestion", func() bool {
			return count("SELECT COALESCE(SUM(count),0) FROM dest_usage_hourly WHERE panel_id=? AND user_id=? AND site='(ip)'", f.agent.PanelID, f.user.ID) > 0
		})
		var usage domain.DestUsagePage
		request("GET", fmt.Sprintf("dest/usage?user_id=%d", f.user.ID), nil, 200, &usage)
		if usage.TotalCount == 0 || usage.TotalSites != 1 || len(usage.Items) != 1 || usage.Items[0].Site != "(ip)" {
			caseTest.Fatal("usage HTTP did not expose the real bounded observation")
		}
		if count("SELECT COUNT(*) FROM audit_log WHERE action='dest.usage.read'") == 0 {
			caseTest.Fatal("real private usage read did not leave an audit record")
		}
	})
	run("restart", func() {
		request("PUT", groupPath, map[string]any{"mode": "open", "stage": "", "list_ids": []int64{}, "updated_at": mode.UpdatedAt}, 200, &mode)
		request("POST", "dest/policies", destinationPolicyInput("Restart user mapping", "observe"), 201, &policy)
		input = destinationPolicyInput("Restart user mapping", "observe")
		input["inline"], input["updated_at"] = map[string]any{"ports": strconv.Itoa(targetPort)}, policy.UpdatedAt
		request("PUT", fmt.Sprintf("dest/policies/%d", policy.ID), input, 200, &policy)
		publish()
		waitApplied()
		traffic(true)
		source := fmt.Sprintf("p%d", policy.ID)
		waitHit(source, "observe", f.user.ID, targetPort)
		before := count("SELECT COALESCE(SUM(count),0) FROM dest_hits WHERE user_id=? AND source=? AND action='observe' AND port=?", f.user.ID, source, targetPort)
		runtime, err := a.repos.DestAgentPolicy.Get(caseTest.Context(), f.agent.AgentID, false)
		nativeMust(caseTest, err, "read confirmed restart candidate")
		stopDaemon(caseTest)
		probe.arm()
		startDaemon(caseTest)
		nativeWait(caseTest, 30*time.Second, "first restarted report", func() bool { _, seen, _ := probe.first(); return seen })
		first, _, accepted := probe.first()
		if !accepted || first == nil || first.State != "applied" || first.Digest != runtime.MintedSHA256 || first.IssueCode != "" || len(first.Listeners) != 0 {
			caseTest.Fatal("first restarted report did not restore the confirmed policy")
		}
		waitApplied()
		traffic(true)
		nativeWait(caseTest, 30*time.Second, "restarted user hit ingestion", func() bool {
			return count("SELECT COALESCE(SUM(count),0) FROM dest_hits WHERE user_id=? AND source=? AND action='observe' AND port=?", f.user.ID, source, targetPort) > before
		})
	})
	var domainPolicy destinationPolicyAPIResult
	var portLKG, domainLKG string
	run("fallback_port", func() {
		input["action"], input["updated_at"] = "block", policy.UpdatedAt
		request("PUT", fmt.Sprintf("dest/policies/%d", policy.ID), input, 200, &policy)
		publish()
		waitApplied()
		traffic(false)
		portLKG = verifyCandidate(1, "", policy.ID).AppliedSHA256
		setSniffing(metadataSniffing)
		waitApplied() // Port-only policy remains valid with metadataOnly.
		var list struct {
			ID int64 `json:"id"`
		}
		request("POST", "dest/lists", map[string]any{"name": "Sniffing prerequisite", "kind": "custom", "text": "full:" + nativeDomain + "\n"}, 201, &list)
		domainInput := destinationPolicyInput("Domain prerequisite", "block")
		domainInput["inline"], domainInput["list_ids"] = map[string]any{}, []int64{list.ID}
		request("POST", "dest/policies", domainInput, 201, &domainPolicy)
		publish()
		waitFault(domain.DestCandidateFallback, false)
		verifyCandidate(1, portLKG, policy.ID)
		traffic(false)
	})
	run("fallback_pause", func() {
		request("PUT", "dest/pause", map[string]any{"paused": true}, 200, nil)
		waitFault(domain.DestCandidatePaused, false)
		verifyCandidate(0, portLKG, 0)
		traffic(true)
		request("PUT", "dest/pause", map[string]any{"paused": false}, 200, nil)
		waitFault(domain.DestCandidateFallback, false)
		verifyCandidate(1, portLKG, policy.ID)
		traffic(false)
	})
	run("fallback_prune", func() {
		input["enabled"], input["updated_at"] = false, policy.UpdatedAt
		request("PUT", fmt.Sprintf("dest/policies/%d", policy.ID), input, 200, &policy)
		publish()
		waitFault(domain.DestCandidateEmpty, false)
		verifyCandidate(0, portLKG, 0)
		traffic(true)
	})
	run("sniff_exhaustion", func() {
		setSniffing(fullSniffing)
		waitApplied()
		verifyCandidate(1, "", 0) // The domain rule is now executable.
		traffic(true)             // Literal-IP Host does not match the domain.
		domainLKG = verifyCandidate(1, "", 0).AppliedSHA256
		trafficDomain(false)
		waitHit(fmt.Sprintf("p%d", domainPolicy.ID), "block", f.user.ID, targetPort)
		setSniffing(metadataSniffing)
		waitFault(domain.DestCandidateEmpty, true)
		verifyCandidate(0, domainLKG, 0)
		trafficDomain(true)
	})
	run("exhausted_roster", func() {
		mintedAfter := receiptAfter
		before := verifyCandidate(0, domainLKG, 0).MintedSHA256
		request("POST", fmt.Sprintf("users/%d/set-enabled", f.user.ID), map[string]any{"enabled": false}, 204, nil)
		disabledAfter := receiptAfter
		nativeWait(caseTest, 120*time.Second, "exhausted roster application", func() bool {
			agent, err := a.repos.NodeAgent.GetByAgentID(caseTest.Context(), f.agent.AgentID)
			if err != nil || agent.LastSeen == nil || !agent.LastSeen.After(disabledAfter) || !streamsReady(agent) {
				return false
			}
			// Convergence of the old roster is insufficient. Require the current
			// acknowledged body to revoke this subject while retaining the canary.
			stream, err := a.repos.NodeAgent.GetStream(caseTest.Context(), f.agent.AgentID, domain.NodeAgentStreamRoster)
			var roster protocol.RosterBody
			if err != nil || stream.AppliedEpoch != agent.Epoch || !stream.Converged() || json.Unmarshal(stream.DesiredBody, &roster) != nil {
				return false
			}
			revoked, active := 0, 0
			for _, c := range roster.Clients {
				if c.Subject == protocol.NewSubjectKey(f.user.ID) {
					if c.Enabled {
						return false
					}
					revoked++
				}
				if c.Key == protocol.NewClientKey(canaryClient.ID) && c.Enabled {
					active++
				}
			}
			return revoked == 1 && active == 1
		})
		waitCandidate(domain.DestCandidateEmpty, true, true, mintedAfter)
		if verifyCandidate(0, domainLKG, 0).MintedSHA256 != before {
			caseTest.Fatal("roster update rearmed an exhausted fallback")
		}
		traffic(false)
		trafficUser(canary.UUID, true)
	})
	run("exhausted_panel_restart", func() {
		prior := verifyCandidate(0, domainLKG, 0)
		cfg := *a.cfg
		gateway.replace(nil) // Drain actual HTTP requests before closing the pool.
		closeContext, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		err := a.Shutdown(closeContext)
		cancel()
		nativeMust(caseTest, err, "stop exhausted PSP app")
		a, err = Build(caseTest.Context(), &cfg) // Fresh SQL pool, services and caches.
		nativeMust(caseTest, err, "reopen exhausted PSP app")
		t.Cleanup(func() {
			stopDaemon(t)
			gateway.replace(nil)
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			nativeMust(t, a.Shutdown(ctx), "close restored PSP app")
		})
		a.startDestinationAudit()
		reopenedAt := time.UnixMilli(time.Now().UnixMilli())
		gateway.replace(a.server.Handler)
		nativeWait(caseTest, 120*time.Second, "restored PSP sync", func() bool {
			agent, err := a.repos.NodeAgent.GetByAgentID(caseTest.Context(), f.agent.AgentID)
			return err == nil && agent.LastSeen != nil && agent.LastSeen.After(reopenedAt) && streamsReady(agent)
		})
		waitCandidate(domain.DestCandidateEmpty, true, true, *prior.MintedAt)
		current := verifyCandidate(0, domainLKG, 0)
		if current.MintedSHA256 != prior.MintedSHA256 || current.MintedGeneration != prior.MintedGeneration || current.DesiredSHA256 != prior.DesiredSHA256 || current.AppliedSHA256 != prior.AppliedSHA256 {
			caseTest.Fatal("PSP reopen rearmed an exhausted fallback or lost its history")
		}
		traffic(false)
		trafficUser(canary.UUID, true)
	})
}

func slicesContainsNative(values []string, value string) bool {
	for _, candidate := range values {
		if candidate == value {
			return true
		}
	}
	return false
}
func nativeMust(t *testing.T, err error, phase string) {
	t.Helper()
	if err != nil {
		t.Fatalf("%s failed; private diagnostics withheld", phase)
	}
}
func nativeWait(t *testing.T, duration time.Duration, phase string, ready func() bool) {
	t.Helper()
	deadline := time.Now().Add(duration)
	for time.Now().Before(deadline) {
		if ready() {
			return
		}
		select {
		case <-t.Context().Done():
			t.Fatal("acceptance cancelled")
		case <-time.After(100 * time.Millisecond):
		}
	}
	t.Fatalf("timed out waiting for %s; private diagnostics withheld", phase)
}

type nativePrivateOutput struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (w *nativePrivateOutput) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.buf.Write(p)
}
func (w *nativePrivateOutput) text() string { w.mu.Lock(); defer w.mu.Unlock(); return w.buf.String() }
func nativePort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	nativeMust(t, err, "reserve loopback port")
	port := l.Addr().(*net.TCPAddr).Port
	_ = l.Close()
	return port
}

func nativeTarget(t *testing.T) (*httptest.Server, string, int, string, *atomic.Int64) {
	t.Helper()
	var random [8]byte
	_, err := rand.Read(random[:])
	nativeMust(t, err, "create target nonce")
	nonce := hex.EncodeToString(random[:])
	ip := net.ParseIP("2001:db8:0:0:" + nonce[:4] + ":" + nonce[4:8] + ":" + nonce[8:12] + ":" + nonce[12:]).String()
	data, err := exec.Command("ip", "-j", "-6", "addr", "show", "dev", "lo").Output()
	nativeMust(t, err, "read owned target addresses")
	var existing []struct {
		AddrInfo []struct {
			Local string `json:"local"`
		} `json:"addr_info"`
	}
	nativeMust(t, json.Unmarshal(data, &existing), "decode owned addresses")
	for _, d := range existing {
		for _, a := range d.AddrInfo {
			if net.ParseIP(a.Local).Equal(net.ParseIP(ip)) {
				t.Fatal("target address already exists")
			}
		}
	}
	nativeMust(t, exec.Command("ip", "-6", "addr", "add", ip+"/128", "dev", "lo", "scope", "host", "nodad").Run(), "add exact owned target /128")
	t.Cleanup(func() {
		nativeMust(t, exec.Command("ip", "-6", "addr", "del", ip+"/128", "dev", "lo").Run(), "remove exact owned target /128")
	})
	data, err = exec.Command("ip", "-j", "-6", "route", "get", ip).Output()
	nativeMust(t, err, "verify local target route")
	var routes []struct {
		Type string `json:"type"`
		Dev  string `json:"dev"`
		Dst  string `json:"dst"`
	}
	nativeMust(t, json.Unmarshal(data, &routes), "decode local target route")
	if len(routes) != 1 || routes[0].Type != "local" || routes[0].Dev != "lo" || !net.ParseIP(routes[0].Dst).Equal(net.ParseIP(ip)) {
		t.Fatal("target does not have its exact local loopback route")
	}
	l, err := net.Listen("tcp6", net.JoinHostPort(ip, "0"))
	nativeMust(t, err, "bind exact owned target")
	requests := &atomic.Int64{}
	server := &httptest.Server{Listener: l, Config: &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { requests.Add(1); _, _ = io.WriteString(w, nonce) })}}
	server.Start()
	t.Cleanup(server.Close)
	return server, ip, l.Addr().(*net.TCPAddr).Port, nonce, requests
}

func nativeVLESSHost(ctx context.Context, inbound int, address string, port int, uuid, nonce, host string) error {
	c, err := (&net.Dialer{Timeout: 3 * time.Second}).DialContext(ctx, "tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(inbound)))
	if err != nil {
		return err
	}
	defer c.Close()
	_ = c.SetDeadline(time.Now().Add(5 * time.Second))
	id, err := hex.DecodeString(strings.ReplaceAll(uuid, "-", ""))
	if err != nil {
		return err
	}
	header := append([]byte{0}, id...)
	header = append(header, 0, 1, byte(port>>8), byte(port), 3)
	header = append(header, net.ParseIP(address).To16()...)
	header = append(header, []byte("GET / HTTP/1.1\r\nHost: "+host+"\r\nConnection: close\r\n\r\n")...)
	if _, err = c.Write(header); err != nil {
		return err
	}
	var response [2]byte
	if _, err = io.ReadFull(c, response[:]); err != nil {
		return err
	}
	if response[0] != 0 {
		return fmt.Errorf("unexpected VLESS response version")
	}
	if _, err = io.CopyN(io.Discard, c, int64(response[1])); err != nil {
		return err
	}
	httpResponse, err := http.ReadResponse(bufio.NewReader(c), nil)
	if err != nil {
		return err
	}
	defer httpResponse.Body.Close()
	body, err := io.ReadAll(io.LimitReader(httpResponse.Body, 1024))
	if err != nil {
		return err
	}
	if httpResponse.StatusCode != 200 || string(body) != nonce {
		return fmt.Errorf("unexpected target response")
	}
	return nil
}
