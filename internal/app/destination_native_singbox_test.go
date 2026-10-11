//go:build destination_acceptance && linux

package app

import (
	"bytes"
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

// This separate gate exercises sing-box execution with actual Node reports.
// It also records the pre-hotfix private-address behavior required by N0-S.
func TestNativeSingBoxDestinationTraffic(t *testing.T) {
	if os.Geteuid() != 0 || os.Getenv("GITHUB_ACTIONS") != "true" || os.Getenv("RUNNER_ENVIRONMENT") != "github-hosted" || os.Getenv("GITHUB_REPOSITORY") != "KazuhaHub/Passwall-Sub-Panel" {
		t.Fatal("native destination acceptance requires the disposable hosted Linux workflow")
	}
	binary := os.Getenv("PSP_NATIVE_NODE_BIN")
	if !filepath.IsAbs(binary) || !strings.HasPrefix(filepath.Base(binary), "destination-node") || os.Getenv("PSP_NATIVE_CORE_VERSION") != "1.14.0" {
		t.Fatal("missing reviewed candidate or exact core selection")
	}
	info, err := os.Stat(binary)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0022 != 0 {
		t.Fatal("candidate executable is absent or writable by other users")
	}
	f := buildDestinationPolicyFixture(t)
	a := f.a
	token := destinationRefreshAdminToken(t, a)
	nativeMust(t, a.repos.NodeAgent.UpdateCoreSelection(t.Context(), f.agent.AgentID, domain.NodeCoreSingBox, "1.14.0", false), "select reviewed sing-box")
	settings, err := a.settings.Load(t.Context(), ports.UISettings{})
	nativeMust(t, err, "read cadence")
	settings.NodePollSeconds, settings.FullReportSeconds = 2, 0
	nativeMust(t, a.settings.Save(t.Context(), settings), "save live cadence")
	inbound, api := nativePort(t), nativePort(t)
	f.node.DesiredPort, f.node.ObservedPort, f.node.InboundListen = inbound, inbound, "127.0.0.1"
	f.node.Sniffing = `{"enabled":true,"destOverride":["http","tls","quic"],"routeOnly":true}`
	nativeMust(t, a.repos.Node.Update(t.Context(), f.node), "configure disposable listener")
	a.startDestinationAudit()
	probe := &nativeRestartProbe{}
	server := httptest.NewTLSServer(probe.wrap(a.server.Handler))
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
		cmd = exec.Command(binary, "--endpoint", server.URL+"/v1/node/sync", "--agent-id", f.agent.AgentID, "--credential-file", credential, "--data-dir", filepath.Join(dir, "node"), "--sing-box-api-listen", net.JoinHostPort("127.0.0.1", strconv.Itoa(api)))
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
		for _, private := range []string{f.credential, f.user.Email, "destination@psp.local", nonce, " accepted ", " rejected "} {
			if strings.Contains(output.text(), private) {
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
		nativeMust(caseTest, err, "perform administrator request")
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
	count := func(table string) int64 {
		caseTest.Helper()
		var n int64
		// The table argument is a fixed fixture literal, never an HTTP input.
		nativeMust(caseTest, a.database.QueryRowContext(caseTest.Context(), "SELECT COUNT(*) FROM "+table).Scan(&n), "read private count")
		return n
	}
	noCollection := func() {
		caseTest.Helper()
		candidate, err := a.repos.DestAgentPolicy.Get(caseTest.Context(), f.agent.AgentID, true)
		nativeMust(caseTest, err, "read sing-box collection candidate")
		var compiled *protocol.DestinationPolicy
		nativeMust(caseTest, json.Unmarshal(candidate.MintedBody, &compiled), "decode sing-box collection candidate")
		if compiled != nil && (protocol.ValidateDestinationPolicy(compiled) != nil || compiled.Collect != "" || protocol.PolicyDigest(compiled) != candidate.MintedSHA256) {
			caseTest.Fatal("sing-box execution falsely claimed collection")
		}
		var status destpolicy.DestinationStatus
		request("GET", "dest/status", nil, 200, &status)
		if len(status.Nodes) != 1 || status.Nodes[0].Engine == nil || *status.Nodes[0].Engine != "sing-box" || !status.Nodes[0].Supports.Policy || status.Nodes[0].Supports.Hits || status.Nodes[0].Supports.Usage || status.Nodes[0].Collecting || status.Nodes[0].CollectEffective != "" {
			caseTest.Fatal("sing-box execution falsely claimed collection")
		}
		if count("dest_hits") != 0 || count("dest_usage_hourly") != 0 {
			caseTest.Fatal("sing-box persisted destination audit data")
		}
	}
	waitApplied := func() {
		caseTest.Helper()
		defer func() {
			if caseTest.Failed() {
				for _, category := range []string{"download", "checksum", "timeout", "rejected", "unsupported", "permission", "bind", "certificate"} {
					caseTest.Logf("native diagnostic: %s=%t", category, strings.Contains(strings.ToLower(output.text()), category))
				}
			}
		}()
		nativeWait(caseTest, 120*time.Second, "current sing-box policy receipt", func() bool {
			state, err := a.destDefinitions.State(caseTest.Context())
			if err != nil {
				return false
			}
			r, err := a.repos.DestAgentPolicy.Get(caseTest.Context(), f.agent.AgentID, true)
			if err != nil {
				return false
			}
			agent, err := a.repos.NodeAgent.GetByAgentID(caseTest.Context(), f.agent.AgentID)
			if err != nil || agent.ObservedCoreEngine != domain.NodeCoreSingBox || !nativeReceiptMatches(*r, state.PublishedGeneration, agent.LastSeen, receiptAfter) {
				return false
			}
			for _, name := range []domain.NodeAgentStreamName{domain.NodeAgentStreamConfig, domain.NodeAgentStreamRoster} {
				stream, err := a.repos.NodeAgent.GetStream(caseTest.Context(), f.agent.AgentID, name)
				if err != nil || stream.AppliedEpoch != agent.Epoch || !stream.Converged() {
					return false
				}
			}
			listener, err := a.repos.Node.GetByID(caseTest.Context(), f.node.ID)
			return err == nil && listener.ConfigSyncState == domain.ConfigSyncSynced
		})
		noCollection()
	}
	traffic := func(host string, allowed bool) {
		caseTest.Helper()
		before := requests.Load()
		err := nativeVLESSHost(caseTest.Context(), inbound, address, targetPort, f.user.UUID, nonce, host)
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
	literalHost := "[" + address + "]"
	run := func(name string, work func()) {
		if !t.Run(name, func(child *testing.T) { caseTest = child; defer func() { caseTest = t }(); work() }) {
			t.FailNow()
		}
	}
	var policy destinationPolicyAPIResult
	input := destinationPolicyInput("Sing-box traffic fixture", "observe")
	input["inline"] = map[string]any{"ports": strconv.Itoa(targetPort)}
	run("empty", func() { waitApplied(); traffic(literalHost, true) })
	run("watch", func() {
		request("POST", "dest/policies", input, 201, &policy)
		publish()
		waitApplied()
		traffic(literalHost, true)
	})
	run("block", func() {
		input["action"], input["updated_at"] = "block", policy.UpdatedAt
		request("PUT", fmt.Sprintf("dest/policies/%d", policy.ID), input, 200, &policy)
		publish()
		waitApplied()
		traffic(literalHost, false)
	})
	run("paused", func() {
		request("PUT", "dest/pause", map[string]any{"paused": true}, 200, nil)
		waitApplied()
		traffic(literalHost, true)
		request("PUT", "dest/pause", map[string]any{"paused": false}, 200, nil)
		waitApplied()
		traffic(literalHost, false)
		request("DELETE", fmt.Sprintf("dest/policies/%d", policy.ID), nil, 204, nil)
		publish()
		waitApplied()
	})
	run("domain", func() {
		var list struct {
			ID int64 `json:"id"`
		}
		request("POST", "dest/lists", map[string]any{"name": "Sing-box domain fixture", "kind": "custom", "text": "full:fallback.example.test\n"}, 201, &list)
		input = destinationPolicyInput("Sing-box domain fixture", "block")
		input["inline"], input["list_ids"] = map[string]any{}, []int64{list.ID}
		request("POST", "dest/policies", input, 201, &policy)
		publish()
		waitApplied()
		traffic(literalHost, true)
		traffic("fallback.example.test", false)
		request("DELETE", fmt.Sprintf("dest/policies/%d", policy.ID), nil, 204, nil)
		publish()
		waitApplied()
	})
	groupPath := fmt.Sprintf("dest/groups/%d", f.group.ID)
	var mode destinationGroupModeAPIResult
	run("trial", func() {
		request("PUT", groupPath, map[string]any{"mode": "allowlist", "stage": "trial", "list_ids": []int64{}, "updated_at": int64(0), "base_hosts": []string{}}, 200, &mode)
		publish()
		waitApplied()
		traffic(literalHost, true)
	})
	run("enforce", func() {
		request("PUT", groupPath, map[string]any{"mode": "allowlist", "stage": "enforce", "list_ids": []int64{}, "updated_at": mode.UpdatedAt}, 200, &mode)
		publish()
		waitApplied()
		traffic(literalHost, false)
	})
	run("extra", func() {
		request("POST", fmt.Sprintf("dest/lists/%d/entries", mode.ExtraListID), map[string]any{"add": []string{address + "/128"}}, 200, nil)
		publish()
		waitApplied()
		traffic(literalHost, true)
	})
	run("restart", func() {
		prior, err := a.repos.DestAgentPolicy.Get(caseTest.Context(), f.agent.AgentID, true)
		nativeMust(caseTest, err, "read confirmed sing-box policy")
		stopDaemon(caseTest)
		probe.arm()
		startDaemon(caseTest)
		nativeWait(caseTest, 60*time.Second, "first sing-box restart report", func() bool { _, seen, _ := probe.first(); return seen })
		first, seen, accepted := probe.first()
		if !seen || !accepted || first == nil || first.State != "applied" || first.Digest != prior.MintedSHA256 || first.IssueCode != "" {
			caseTest.Fatal("sing-box restart lost its applied policy receipt")
		}
		waitApplied()
		traffic(literalHost, true)
	})
	run("collection_off", func() {
		request("PUT", fmt.Sprintf("servers/%d", f.agent.PanelID), map[string]any{"audit_collect": "hits_and_usage"}, 200, nil)
		waitApplied()
		traffic(literalHost, true)
		time.Sleep(7 * time.Second)
		noCollection()
		request("PUT", fmt.Sprintf("servers/%d", f.agent.PanelID), map[string]any{"audit_collect": "off"}, 200, nil)
		waitApplied()
		traffic(literalHost, true)
		time.Sleep(7 * time.Second)
		noCollection()
		request("PUT", groupPath, map[string]any{"mode": "open", "stage": "", "list_ids": []int64{}, "updated_at": mode.UpdatedAt}, 200, &mode)
		publish()
		waitApplied()
	})
	run("private_baseline", func() {
		// This is a disposable local target, never a cloud metadata endpoint.
		privateTarget := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, nonce) }))
		defer privateTarget.Close()
		port := privateTarget.Listener.Addr().(*net.TCPAddr).Port
		for _, destination := range []string{"127.0.0.1", "localhost"} {
			nativeMust(caseTest, nativeVLESSHost(caseTest.Context(), inbound, destination, port, f.user.UUID, nonce, destination), "pre-hotfix sing-box private destination")
		}
		noCollection()
	})
}
