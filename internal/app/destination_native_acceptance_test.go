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

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
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
	settings, err := a.settings.Load(t.Context(), ports.UISettings{})
	nativeMust(t, err, "read cadence")
	settings.NodePollSeconds, settings.FullReportSeconds = 2, 0
	nativeMust(t, a.settings.Save(t.Context(), settings), "save live cadence")
	// Provisioning uses the same reviewed catalog check as ordinary native nodes.
	if version != f.agent.DesiredCoreVersion {
		nativeMust(t, a.repos.NodeAgent.UpdateCoreSelection(t.Context(), f.agent.AgentID, domain.NodeCoreXray, version, false), "select reviewed core")
	}
	inbound, api := nativePort(t), nativePort(t)
	f.node.DesiredPort, f.node.ObservedPort = inbound, inbound
	f.node.InboundListen = "127.0.0.1"
	f.node.Sniffing = `{"enabled":true,"destOverride":["http","tls"]}`
	nativeMust(t, a.repos.Node.Update(t.Context(), f.node), "configure disposable listener")
	a.startDestinationAudit()
	server := httptest.NewTLSServer(a.server.Handler)
	t.Cleanup(server.Close)
	dir := t.TempDir()
	ca, credential := filepath.Join(dir, "ca.pem"), filepath.Join(dir, "credential")
	nativeMust(t, os.WriteFile(ca, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw}), 0600), "write scoped fixture CA")
	nativeMust(t, os.WriteFile(credential, []byte(f.credential), 0600), "write private fixture credential")
	_, address, targetPort, nonce, requests := nativeTarget(t)
	output := &nativePrivateOutput{}
	cmd := exec.Command(binary, "--endpoint", server.URL+"/v1/node/sync", "--agent-id", f.agent.AgentID, "--credential-file", credential, "--data-dir", filepath.Join(dir, "node"), "--xray-api-listen", net.JoinHostPort("127.0.0.1", strconv.Itoa(api)))
	cmd.Env = append(os.Environ(), "SSL_CERT_FILE="+ca)
	cmd.Stdout, cmd.Stderr = output, output
	receiptAfter := time.UnixMilli(time.Now().UnixMilli())
	nativeMust(t, cmd.Start(), "start candidate Node")
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	t.Cleanup(func() {
		_ = cmd.Process.Signal(syscall.SIGTERM)
		select {
		case <-done:
		case <-time.After(15 * time.Second):
			_ = cmd.Process.Kill()
			<-done
			t.Error("candidate Node did not drain its core and collectors")
		}
		text := output.text()
		for _, private := range []string{f.credential, f.user.Email, "destination@psp.local", " accepted ", " rejected "} {
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
	waitApplied := func() {
		caseTest.Helper()
		nativeWait(caseTest, 90*time.Second, "current policy receipt", func() bool {
			state, err := a.destDefinitions.State(caseTest.Context())
			if err != nil {
				return false
			}
			r, err := a.repos.DestAgentPolicy.Get(caseTest.Context(), f.agent.AgentID, true)
			if err != nil {
				return false
			}
			agent, err := a.repos.NodeAgent.GetByAgentID(caseTest.Context(), f.agent.AgentID)
			return err == nil && nativeReceiptMatches(*r, state.PublishedGeneration, agent.LastSeen, receiptAfter)
		})
	}
	traffic := func(allowed bool) {
		caseTest.Helper()
		before := requests.Load()
		err := nativeVLESS(caseTest.Context(), inbound, address, targetPort, f.user.UUID, nonce)
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

func nativeVLESS(ctx context.Context, inbound int, address string, port int, uuid, nonce string) error {
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
	header = append(header, []byte("GET / HTTP/1.1\r\nHost: ["+address+"]\r\nConnection: close\r\n\r\n")...)
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
