package vlessroute

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"golang.org/x/net/proxy"
	"gopkg.in/yaml.v3"
)

// This test mutates only an explicitly opted-in, empty local acceptance build.
// It goes through PSP's HTTP API and actual rendered subscriptions, not an
// in-memory renderer or a reimplementation of the route compiler.
func TestLive_VlessRouteV2HTTPAndDataPlane(t *testing.T) {
	if os.Getenv("PSP_V2_LIVE_TEST") != "isolated-empty-fixture" {
		t.Skip("requires dedicated PSP/3X-UI acceptance containers and real clients")
	}
	panel := liveV2Base(t, "PSP_V2_PANEL_URL")
	xui := liveV2Base(t, "PSP_V2_XUI_URL")
	password := os.Getenv("PSP_V2_ADMIN_PASSWORD")
	if password == "" {
		t.Fatal("PSP_V2_ADMIN_PASSWORD is required")
	}
	container := os.Getenv("PSP_V2_XUI_CONTAINER")
	if !strings.HasPrefix(container, "psp-v2-xui-") {
		t.Fatal("PSP_V2_XUI_CONTAINER must name the dedicated acceptance backend")
	}
	for _, port := range []int{8080, 8082} {
		nonce := fmt.Sprintf("fixture-control-%d", port)
		out, err := exec.Command("docker", "exec", container, "curl", "--noproxy", "*", "--max-time", "3", "-fsS", fmt.Sprintf("http://11.188.90.3:%d/%s", port, nonce)).CombinedOutput()
		if err != nil || string(out) != nonce {
			t.Fatalf("controlled target %d unavailable: %v", port, err)
		}
	}
	f := &liveV2Fixture{t: t, panel: panel, xui: xui, http: &http.Client{Timeout: 15 * time.Second}}
	var build struct {
		Version string `json:"version"`
		Commit  string `json:"commit"`
	}
	f.request("GET", panel+"/api/version", nil, "", &build)
	if build.Version != "vlessroute-v2-acceptance" {
		t.Fatal("refusing to mutate a non-acceptance PSP build")
	}
	f.request("POST", panel+"/api/auth/local/login", map[string]any{"upn": "admin", "password": password}, "", &structToken{Target: &f.token})
	for _, resource := range []string{"servers", "nodes"} {
		var list struct {
			Items []json.RawMessage `json:"items"`
		}
		f.api("GET", "/admin/"+resource, nil, &list)
		if len(list.Items) != 0 {
			t.Fatal("requires an EMPTY acceptance fixture")
		}
	}
	jar, _ := cookiejar.New(nil)
	f.xhttp = &http.Client{Jar: jar, Timeout: 15 * time.Second}
	// Cookie auth is exercised independently of the PSP-to-XUI bearer adapter.
	f.xrequest("GET", "/", nil, nil)
	var csrf struct {
		Obj string `json:"obj"`
	}
	f.xrequest("GET", "/csrf-token", nil, &csrf)
	f.csrf = csrf.Obj
	var login struct {
		Success bool `json:"success"`
	}
	f.xrequest("POST", "/login", url.Values{"username": {"admin"}, "password": {"admin"}}, &login)
	if !login.Success {
		t.Fatal("fixture 3X-UI login failed")
	}
	f.xrequest("GET", "/csrf-token", nil, &csrf)
	f.csrf = csrf.Obj
	var key struct {
		Success bool `json:"success"`
		Obj     struct {
			Token string `json:"token"`
		} `json:"obj"`
	}
	f.xrequest("POST", "/panel/api/setting/apiTokens/create", map[string]any{"name": "psp-v2-acceptance", "scope": "admin"}, &key)
	if !key.Success || key.Obj.Token == "" {
		t.Fatal("fixture API token creation failed")
	}
	var server struct {
		ID int64 `json:"id"`
	}
	f.api("POST", "/admin/servers", map[string]any{"panel_type": "3xui", "name": "v2-fixture", "url": "http://xui:2053", "api_token": key.Obj.Token, "auth_method": "token"}, &server)
	// Configure EXISTING outbounds and user rules directly in 3X-UI. PSP may
	// only read them and append its own routing rules from this point onward.
	wrapper := f.template()
	root := wrapper.config
	outbounds := root["outbounds"].([]any)
	for _, entry := range outbounds {
		outbound := entry.(map[string]any)
		if outbound["protocol"] == "freedom" {
			f.direct = outbound["tag"].(string)
		}
		if outbound["protocol"] == "blackhole" {
			f.block = outbound["tag"].(string)
		}
	}
	if f.direct == "" || f.block == "" {
		t.Fatal("fresh fixture must already have freedom and blackhole outbounds")
	}
	routing := root["routing"].(map[string]any)
	rules, _ := routing["rules"].([]any)
	routing["rules"] = append(rules, map[string]any{"type": "field", "ip": []string{"11.188.90.3"}, "port": "8082", "network": "tcp", "outboundTag": f.block})
	f.writeTemplate(wrapper)
	f.outboundsBefore, _ = json.Marshal(outbounds)
	f.waitCore(server.ID)
	for _, vision := range []bool{false, true} {
		t.Run(fmt.Sprintf("vision=%t", vision), func(t *testing.T) {
			f.t = t
			port := 25453
			if vision {
				port++
			}
			stream := map[string]any{"network": "tcp", "security": "none"}
			flow := ""
			pin := ""
			if vision {
				stream["security"] = "tls"
				var tls map[string]any
				tls, pin = liveV2TLS(t)
				stream["tlsSettings"] = tls
				flow = "xtls-rprx-vision"
			}
			streamJSON, _ := json.Marshal(stream)
			name := fmt.Sprintf("A-%t", vision)
			var node struct {
				ID int64 `json:"id"`
			}
			f.api("POST", "/admin/nodes", map[string]any{"panel_id": server.ID, "display_name": name, "server_address": "127.0.0.1", "region": "HK", "flow": flow, "inbound": map[string]any{"remark": name, "enable": true, "port": port, "protocol": "vless", "settings": `{"clients":[],"decryption":"none"}`, "stream_settings": string(streamJSON), "sniffing": `{"enabled":false}`}}, &node)
			f.waitCore(server.ID)
			// Start traffic measurement only after synchronous account setup on
			// an existing listener, not while node-create warmup is still queued.
			var account struct {
				User struct {
					SubURL string `json:"sub_url"`
				} `json:"user"`
			}
			f.api("POST", "/admin/users", map[string]any{"upn": fmt.Sprintf("route-tester-%t", vision), "password": "V2-fixture-only-2026!", "group_id": 1}, &account)
			f.subURL = account.User.SubURL
			f.waitCore(server.ID)
			var state View
			f.api("GET", fmt.Sprintf("/admin/nodes/%d/vless-route-lines", node.ID), nil, &state)
			f.api("GET", fmt.Sprintf("/admin/nodes/%d/vless-route-outbounds", node.ID), nil, &struct {
				Items []json.RawMessage `json:"items"`
			}{})
			f.api("PUT", fmt.Sprintf("/admin/nodes/%d/vless-route-lines", node.ID), SaveRequest{Revision: state.State.Revision, Lines: []LineInput{{Name: "BLOCK", OutboundTag: f.block, Enabled: true}, {Name: "DIRECT", OutboundTag: f.direct, Enabled: true}}}, &state)
			if state.State.Status != "applied" {
				t.Fatalf("route save not confirmed: %s %s", state.State.Status, state.State.Error)
			}
			var retiredEntry map[string]any
			for _, format := range []string{"mihomo", "sing-box", "uri-list"} {
				t.Run(format, func(t *testing.T) {
					proxies := f.waitProxies(t, format, name)
					if format == "uri-list" {
						retiredEntry = proxies[name+" BLOCK"]
					}
					for _, suffix := range []string{"", " BLOCK", " DIRECT"} {
						entry := proxies[name+suffix]
						if entry == nil {
							t.Fatalf("missing %s proxy %s", format, name+suffix)
						}
						address, stop := liveV2Client(t, format, entry, pin)
						nonce := fmt.Sprintf("v2-%d", time.Now().UnixNano())
						pass := suffix != " BLOCK"
						liveV2TCP(t, address, "http://11.188.90.3:8080/"+nonce, nonce, pass)
						liveV2UDP(t, address, nonce, pass)
						if pass {
							liveV2TCP(t, address, "http://11.188.90.3:8082/"+nonce, nonce, false)
						}
						stop()
					}
					t.Log("TCP/UDP: A=direct B=block C=direct; preceding custom block still wins")
				})
			}
			f.t = t
			// Candidate selection is read through the real rule-set inspector.
			var inspection struct {
				Lines []struct {
					NodeID int64  `json:"node_id"`
					LineID string `json:"line_id"`
				} `json:"lines"`
				Groups []struct {
					Name    string   `json:"name"`
					Preview []string `json:"preview"`
				} `json:"groups"`
			}
			f.api("POST", "/admin/rules/inspect-proxy-groups", map[string]any{"content": "- MATCH,Pick", "proxy_group_members": map[string]any{"Pick": []domain.ProxyGroupMember{{Kind: "line", NodeID: node.ID, LineID: state.State.Lines[0].ID}}}}, &inspection)
			found := false
			for _, group := range inspection.Groups {
				if group.Name == "Pick" {
					found = len(group.Preview) == 1 && group.Preview[0] == name+" BLOCK"
				}
			}
			if !found {
				t.Fatal("specific-line inspector did not resolve a single route entry")
			}
			// Disabling/deleting removes owned rules and does not create a new
			// remote client or listener. Retired configuration is not republished.
			f.api("PUT", fmt.Sprintf("/admin/nodes/%d/vless-route-lines", node.ID), SaveRequest{Revision: state.State.Revision, Lines: []LineInput{{ID: state.State.Lines[1].ID, Name: "DIRECT", OutboundTag: f.direct, Enabled: true}}}, &state)
			if state.State.Status != "applied" || len(state.State.Lines) != 1 {
				t.Fatal("delete did not confirm")
			}
			proxies := f.proxies(t, "mihomo")
			if proxies[name+" BLOCK"] != nil {
				t.Fatal("deleted route remains in subscription")
			}
			address, stop := liveV2Client(t, "uri-list", retiredEntry, pin)
			nonce := fmt.Sprintf("retired-%d", time.Now().UnixNano())
			liveV2TCP(t, address, "http://11.188.90.3:8080/"+nonce, nonce, true)
			liveV2UDP(t, address, nonce, true)
			liveV2TCP(t, address, "http://11.188.90.3:8082/"+nonce, nonce, false)
			stop()
			t.Log("retired B still authenticates and follows existing/default routing, not a credential revocation")
			if bytes.Contains(f.templateBytes(), []byte(`"user"`)) {
				t.Fatal("unexpected user-specific route match")
			}
		})
		if t.Failed() {
			break
		}
	}
	f.t = t
	after, _ := json.Marshal(f.template().config["outbounds"])
	if !bytes.Equal(f.outboundsBefore, after) {
		t.Fatal("PSP changed existing outbound configuration")
	}
	t.Log("fixture retained for UI inspection; outbound configuration unchanged")
}

type liveV2Fixture struct {
	t                                              *testing.T
	panel, xui, token, csrf, direct, block, subURL string
	http, xhttp                                    *http.Client
	outboundsBefore                                []byte
}
type structToken struct{ Target *string }

func (s *structToken) UnmarshalJSON(raw []byte) error {
	var result struct {
		Token string `json:"access_token"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		return err
	}
	if result.Token == "" {
		return fmt.Errorf("acceptance login failed")
	}
	*s.Target = result.Token
	return nil
}

func liveV2Base(t *testing.T, key string) string {
	t.Helper()
	value := os.Getenv(key)
	u, err := url.Parse(value)
	if err != nil || u.Scheme != "http" || u.Hostname() != "127.0.0.1" || u.Path != "" {
		t.Fatalf("%s must be an explicit loopback HTTP acceptance URL", key)
	}
	return strings.TrimRight(value, "/")
}
func (f *liveV2Fixture) request(method, endpoint string, body any, token string, result any) {
	f.t.Helper()
	var reader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			f.t.Fatal(err)
		}
		reader = bytes.NewReader(raw)
	}
	req, err := http.NewRequest(method, endpoint, reader)
	if err != nil {
		f.t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	response, err := f.http.Do(req)
	if err != nil {
		f.t.Fatal(err)
	}
	defer response.Body.Close()
	raw, _ := io.ReadAll(response.Body)
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		f.t.Fatalf("%s %s: HTTP %d", method, req.URL.Path, response.StatusCode)
	}
	if method == "POST" && req.URL.Path == "/api/admin/users" && response.Header.Get("X-Sync-Pending") == "1" {
		f.t.Fatal("fixture account provisioning was queued; not ready for dataplane measurement")
	}
	if result != nil {
		if err := json.Unmarshal(raw, result); err != nil {
			f.t.Fatalf("decode %s: %v", req.URL.Path, err)
		}
	}
}
func (f *liveV2Fixture) api(method, path string, body, result any) {
	f.t.Helper()
	f.request(method, f.panel+"/api"+path, body, f.token, result)
}
func (f *liveV2Fixture) xrequest(method, path string, body, result any) {
	f.t.Helper()
	var reader io.Reader
	contentType := "application/json"
	if form, ok := body.(url.Values); ok {
		reader = strings.NewReader(form.Encode())
		contentType = "application/x-www-form-urlencoded"
	} else if body != nil {
		raw, _ := json.Marshal(body)
		reader = bytes.NewReader(raw)
	}
	req, _ := http.NewRequest(method, f.xui+path, reader)
	req.Header.Set("Content-Type", contentType)
	if f.csrf != "" {
		req.Header.Set("X-CSRF-Token", f.csrf)
	}
	response, err := f.xhttp.Do(req)
	if err != nil {
		f.t.Fatal(err)
	}
	defer response.Body.Close()
	raw, _ := io.ReadAll(response.Body)
	if response.StatusCode != 200 {
		f.t.Fatalf("fixture 3X-UI %s: HTTP %d", path, response.StatusCode)
	}
	if result != nil {
		if err := json.Unmarshal(raw, result); err != nil {
			f.t.Fatalf("decode fixture response: %v", err)
		}
	}
}

type liveV2Template struct {
	config  map[string]any
	testURL string
}

func (f *liveV2Fixture) template() liveV2Template {
	f.t.Helper()
	var response struct {
		Success bool            `json:"success"`
		Obj     json.RawMessage `json:"obj"`
	}
	f.xrequest("POST", "/panel/api/xray/", url.Values{}, &response)
	var encoded string
	if json.Unmarshal(response.Obj, &encoded) == nil {
		response.Obj = json.RawMessage(encoded)
	}
	var wrapper struct {
		Config  map[string]any `json:"xraySetting"`
		TestURL string         `json:"outboundTestUrl"`
	}
	if json.Unmarshal(response.Obj, &wrapper) != nil || wrapper.Config == nil {
		f.t.Fatal("invalid fixture template")
	}
	return liveV2Template{config: wrapper.Config, testURL: wrapper.TestURL}
}
func (f *liveV2Fixture) templateBytes() []byte {
	raw, _ := json.Marshal(f.template().config)
	return raw
}
func (f *liveV2Fixture) writeTemplate(template liveV2Template) {
	raw, _ := json.Marshal(template.config)
	var response struct {
		Success bool `json:"success"`
	}
	f.xrequest("POST", "/panel/api/xray/update", url.Values{"xraySetting": {string(raw)}, "outboundTestUrl": {template.testURL}}, &response)
	if !response.Success {
		f.t.Fatal("fixture setup template update failed")
	}
}
func (f *liveV2Fixture) waitCore(serverID int64) {
	f.t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		var probe struct {
			OK    bool   `json:"ok"`
			State string `json:"xray_state"`
		}
		f.api("POST", "/admin/servers/probe", map[string]any{"id": serverID}, &probe)
		if probe.OK && probe.State == "running" {
			return
		}
		time.Sleep(150 * time.Millisecond)
	}
	f.t.Fatal("fixture core did not become ready")
}

func (f *liveV2Fixture) proxies(t *testing.T, format string) map[string]map[string]any {
	t.Helper()
	endpoint, err := url.Parse(f.subURL)
	if err != nil {
		t.Fatal(err)
	}
	base, _ := url.Parse(f.panel)
	endpoint.Scheme = base.Scheme
	endpoint.Host = base.Host
	query := endpoint.Query()
	query.Set("client", format)
	endpoint.RawQuery = query.Encode()
	req, _ := http.NewRequest("GET", endpoint.String(), nil)
	req.Header.Set("User-Agent", map[string]string{"mihomo": "Clash.Meta/1.19.30", "sing-box": "sing-box/1.14.1", "uri-list": "v2rayN/7.10.5"}[format])
	response, err := f.http.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	raw, _ := io.ReadAll(response.Body)
	if response.StatusCode != 200 {
		t.Fatalf("subscription %s: HTTP %d", format, response.StatusCode)
	}
	entries := map[string]map[string]any{}
	switch format {
	case "mihomo":
		var document struct {
			Proxies []map[string]any `yaml:"proxies"`
		}
		if err := yaml.Unmarshal(raw, &document); err != nil {
			t.Fatal(err)
		}
		for _, entry := range document.Proxies {
			if entry["type"] == "vless" {
				entries[entry["name"].(string)] = entry
			}
		}
	case "sing-box":
		var document struct {
			Outbounds []map[string]any `json:"outbounds"`
		}
		if err := json.Unmarshal(raw, &document); err != nil {
			t.Fatal(err)
		}
		for _, entry := range document.Outbounds {
			if entry["type"] == "vless" {
				entries[entry["tag"].(string)] = entry
			}
		}
	default:
		decoded, err := base64.StdEncoding.DecodeString(string(raw))
		if err != nil {
			t.Fatal(err)
		}
		for _, line := range strings.Fields(string(decoded)) {
			u, err := url.Parse(line)
			if err != nil || u.Scheme != "vless" {
				continue
			}
			port, _ := strconv.Atoi(u.Port())
			entries[u.Fragment] = map[string]any{"uuid": u.User.Username(), "server": u.Hostname(), "server_port": port, "flow": u.Query().Get("flow"), "tls": u.Query().Get("security") == "tls"}
		}
	}
	return entries
}
func (f *liveV2Fixture) waitProxies(t *testing.T, format, name string) map[string]map[string]any {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	var names []string
	for time.Now().Before(deadline) {
		entries := f.proxies(t, format)
		names = nil
		for label := range entries {
			names = append(names, label)
		}
		sort.Strings(names)
		if entries[name] != nil && entries[name+" BLOCK"] != nil && entries[name+" DIRECT"] != nil {
			return entries
		}
		time.Sleep(time.Second)
	}
	t.Fatalf("subscription %s did not publish confirmed A/B/C for %s; observed labels=%v", format, name, names)
	return nil
}

func liveV2TLS(t *testing.T) (map[string]any, string) {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	cert := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "localhost"}, DNSNames: []string{"localhost"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), KeyUsage: x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	der, err := x509.CreateCertificate(rand.Reader, cert, cert, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	// Never put client verification options in the actual server TLS config.
	return map[string]any{"serverName": "localhost", "certificates": []any{map[string]any{"certificate": strings.Split(strings.TrimSpace(string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}))), "\n"), "key": strings.Split(strings.TrimSpace(string(pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)}))), "\n")}}}, fmt.Sprintf("%x", sha256.Sum256(der))
}

func liveV2Client(t *testing.T, format string, entry map[string]any, pin string) (string, func()) {
	t.Helper()
	// Only the test client's trust of our generated self-signed certificate is
	// adapted. Protocol, endpoint, flow and credential come from the subscription.
	copy := make(map[string]any, len(entry))
	for k, v := range entry {
		copy[k] = v
	}
	entry = copy
	if pin != "" {
		if format == "mihomo" {
			entry["skip-cert-verify"] = true
		}
		if format == "sing-box" {
			if tls, ok := entry["tls"].(map[string]any); ok {
				trust := make(map[string]any, len(tls))
				for k, v := range tls {
					trust[k] = v
				}
				trust["insecure"] = true
				entry["tls"] = trust
			}
		}
	}
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	_ = listener.Close()
	dir := t.TempDir()
	path := filepath.Join(dir, "client.json")
	var config any
	var binary string
	var args []string
	switch format {
	case "mihomo":
		binary = os.Getenv("PSP_V2_MIHOMO")
		args = []string{"-d", dir, "-f", path}
		config = map[string]any{"mixed-port": port, "bind-address": "127.0.0.1", "mode": "rule", "log-level": "error", "proxies": []any{entry}, "rules": []string{"MATCH," + entry["name"].(string)}}
	case "sing-box":
		binary = os.Getenv("PSP_V2_SINGBOX")
		args = []string{"run", "-c", path}
		config = map[string]any{"log": map[string]any{"level": "error"}, "inbounds": []any{map[string]any{"type": "socks", "listen": "127.0.0.1", "listen_port": port}}, "outbounds": []any{entry}, "route": map[string]any{"final": entry["tag"]}}
	default:
		binary = os.Getenv("PSP_V2_XRAY")
		args = []string{"run", "-c", path}
		stream := map[string]any{"network": "tcp", "security": "none"}
		if entry["tls"] == true {
			stream["security"] = "tls"
			stream["tlsSettings"] = map[string]any{"serverName": "localhost", "pinnedPeerCertSha256": pin}
		}
		config = map[string]any{"log": map[string]any{"loglevel": "error"}, "inbounds": []any{map[string]any{"listen": "127.0.0.1", "port": port, "protocol": "socks", "settings": map[string]any{"udp": true}}}, "outbounds": []any{map[string]any{"protocol": "vless", "settings": map[string]any{"vnext": []any{map[string]any{"address": entry["server"], "port": entry["server_port"], "users": []any{map[string]any{"id": entry["uuid"], "encryption": "none", "flow": entry["flow"]}}}}}, "streamSettings": stream}}}
	}
	if binary == "" {
		t.Fatal("all three real client executable paths are required")
	}
	raw, _ := json.Marshal(config)
	if err := os.WriteFile(path, raw, 0600); err != nil {
		t.Fatal(err)
	}
	logs, err := os.Create(filepath.Join(dir, "client.log"))
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(binary, args...)
	cmd.Stdout = logs
	cmd.Stderr = logs
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	var once sync.Once
	stop := func() {
		once.Do(func() {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
			_ = logs.Close()
			if t.Failed() {
				raw, _ := os.ReadFile(filepath.Join(dir, "client.log"))
				t.Logf("%s client: %s", format, raw)
			}
		})
	}
	t.Cleanup(stop)
	address := fmt.Sprintf("127.0.0.1:%d", port)
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("tcp", address, 100*time.Millisecond)
		if err == nil {
			_ = conn.Close()
			return address, stop
		}
		time.Sleep(25 * time.Millisecond)
	}
	stop()
	t.Fatal("real client did not start")
	return "", stop
}

func liveV2TCP(t *testing.T, address, target, nonce string, pass bool) {
	t.Helper()
	dialer, err := proxy.SOCKS5("tcp", address, nil, &net.Dialer{Timeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	client := &http.Client{Timeout: 2 * time.Second, Transport: &http.Transport{DisableKeepAlives: true, DialContext: func(_ context.Context, network, address string) (net.Conn, error) {
		return dialer.Dial(network, address)
	}}}
	deadline := time.Now().Add(4 * time.Second)
	for {
		response, err := client.Get(target)
		got := ""
		if err == nil {
			body, _ := io.ReadAll(response.Body)
			_ = response.Body.Close()
			got = string(body)
		}
		if (got == nonce) == pass {
			return
		}
		if !pass || time.Now().After(deadline) {
			t.Fatalf("TCP allowed=%t want=%t: %v", got == nonce, pass, err)
		}
		time.Sleep(50 * time.Millisecond)
	}
}
func liveV2UDP(t *testing.T, address, nonce string, pass bool) {
	t.Helper()
	control, err := net.DialTimeout("tcp", address, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer control.Close()
	_ = control.SetDeadline(time.Now().Add(2 * time.Second))
	_, _ = control.Write([]byte{5, 1, 0})
	hello := make([]byte, 2)
	if _, err := io.ReadFull(control, hello); err != nil {
		t.Fatal(err)
	}
	_, _ = control.Write([]byte{5, 3, 0, 1, 0, 0, 0, 0, 0, 0})
	header := make([]byte, 4)
	if _, err := io.ReadFull(control, header); err != nil || header[1] != 0 || header[3] != 1 {
		t.Fatal("SOCKS UDP associate failed")
	}
	bound := make([]byte, 6)
	if _, err := io.ReadFull(control, bound); err != nil {
		t.Fatal(err)
	}
	conn, err := net.DialUDP("udp4", nil, &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: int(binary.BigEndian.Uint16(bound[4:]))})
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(time.Second))
	packet := []byte{0, 0, 0, 1, 11, 188, 90, 3, byte(8081 >> 8), byte(8081 & 255)}
	packet = append(packet, nonce...)
	_, _ = conn.Write(packet)
	reply := make([]byte, 2048)
	n, err := conn.Read(reply)
	ok := err == nil && bytes.HasSuffix(reply[:n], []byte(nonce))
	if ok != pass {
		t.Fatalf("UDP allowed=%t want=%t: %v", ok, pass, err)
	}
}
