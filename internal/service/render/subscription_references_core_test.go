package render

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
)

// Optional local, offline data-plane acceptance. No panel, production state or
// external destination is touched. The normal suite does not require binaries.
func TestReferencedSubscriptionRealCores(t *testing.T) {
	xray := os.Getenv("PSP_REFERENCE_XRAY")
	mihomo := os.Getenv("PSP_REFERENCE_MIHOMO")
	singbox := os.Getenv("PSP_REFERENCE_SINGBOX")
	if xray == "" || mihomo == "" || singbox == "" {
		t.Skip("set PSP_REFERENCE_XRAY, PSP_REFERENCE_MIHOMO and PSP_REFERENCE_SINGBOX for offline core acceptance")
	}
	for _, scenario := range []string{"vless_route", "legacy_direct", "legacy_relay"} {
		t.Run(scenario, func(t *testing.T) {
			testReferencedCoreScenario(t, xray, mihomo, singbox, scenario)
		})
	}
}

func testReferencedCoreScenario(t *testing.T, xray, mihomo, singbox, scenario string) {
	t.Helper()
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "referenced-route-ok")
	}))
	defer target.Close()
	serverPort := referenceFreePort(t)
	allowedRoute := "1001"
	if scenario != "vless_route" {
		allowedRoute = "19132" // 0x4abc, the unchanged original UUID's route.
	}
	serverConfig := map[string]any{
		"log": map[string]any{"loglevel": "debug"},
		"inbounds": []any{map[string]any{
			"listen": "127.0.0.1", "port": serverPort, "protocol": "vless", "tag": "original",
			"settings": map[string]any{"decryption": "none", "clients": []any{map[string]any{"id": "12345678-1234-4abc-8def-123456789abc"}}},
		}},
		// No direct fallback: only the scenario's expected UUID route can reach
		// the fixture. Other routes use the first (blocked) outbound.
		"outbounds": []any{
			map[string]any{"protocol": "blackhole", "tag": "blocked"},
			map[string]any{"protocol": "freedom", "tag": "allowed", "settings": map[string]any{
				// Permit only the fixture's loopback HTTP endpoint through
				// Xray's server-side private-address safety policy.
				"finalRules": []any{map[string]any{"action": "allow", "network": "tcp", "ip": []string{"127.0.0.1/32"}, "port": target.Listener.Addr().(*net.TCPAddr).Port}},
			}},
		},
		"routing": map[string]any{"rules": []any{map[string]any{"type": "field", "inboundTag": []string{"original"}, "vlessRoute": allowedRoute, "outboundTag": "allowed"}}},
	}
	raw, err := json.Marshal(serverConfig)
	if err != nil {
		t.Fatal(err)
	}
	serverFile := referenceCoreFile(t, "server.json", raw)
	referenceStartCore(t, xray, []string{"run", "-config", serverFile}, serverPort)
	for _, ct := range []domain.ClientType{domain.ClientMihomo, domain.ClientSingBox} {
		t.Run(string(ct), func(t *testing.T) {
			s, node, user, rule := referenceRenderFixture(t)
			node.ServerAddress, node.DesiredPort, node.Flow = "127.0.0.1", serverPort, ""
			node.StreamSettings = `{"network":"tcp","security":"none"}`
			node.RouteState.Binding = domain.RouteBinding("https://panel.example", node)
			rule.Content = "- MATCH,OnlyRoute"
			rule.ProxyGroupMembers["OnlyRoute"] = rule.ProxyGroupMembers["国外媒体"]
			delete(rule.ProxyGroupMembers, "国外媒体")
			expectedName, expectedUUID := "A B", "12345678-1234-03e9-8def-123456789abc"
			if scenario != "vless_route" {
				// No VLESS route state or precise-line members in main fixtures.
				node.RouteState = domain.NodeRouteState{}
				rule.ProxyGroupMembers["OnlyRoute"] = []domain.ProxyGroupMember{{Kind: "node", NodeID: node.ID}}
				expectedName, expectedUUID = "A", "12345678-1234-4abc-8def-123456789abc"
				if scenario == "legacy_relay" {
					node.HideDirect = true
					node.Relays = []domain.RelayLine{{ID: "relay", Name: "R", Address: "127.0.0.1", Port: serverPort, Enabled: true}}
					expectedName = "A R"
				}
			}
			clientPort := referenceFreePort(t)
			template := referenceMihomoTemplate
			binary, args := mihomo, []string{"-d", t.TempDir(), "-f"}
			if ct == domain.ClientMihomo {
				template = fmt.Sprintf("mixed-port: %d\nmode: rule\nlog-level: debug\n", clientPort) + template
			} else {
				template = strings.Replace(referenceSingBoxTemplate, "{", fmt.Sprintf(`{"log":{"level":"debug"},"inbounds":[{"type":"mixed","listen":"127.0.0.1","listen_port":%d}],`, clientPort), 1)
				binary, args = singbox, []string{"run", "-c"}
			}
			s.repos.Template = referenceTemplateRepo{content: map[domain.ClientType]string{ct: template}}
			output, err := s.RenderForUser(context.Background(), user, ct)
			if err != nil {
				t.Fatal(err)
			}
			// Require the actual expected proxy, not merely an HTTP success via
			// a DIRECT fallback after an unavailable member was filtered out.
			foundRoute := false
			for _, proxy := range subscriptionProxyMaps(t, output, ct) {
				if scenario != "legacy_direct" && (proxy["name"] == "A" || proxy["tag"] == "A") {
					t.Fatal("original A was emitted")
				}
				if proxy["name"] == expectedName || proxy["tag"] == expectedName {
					foundRoute = proxy["uuid"] == expectedUUID
				}
			}
			if !foundRoute {
				t.Fatalf("required %s was not emitted; refusing a DIRECT fallback false positive", expectedName)
			}
			file := referenceCoreFile(t, "subscription.json", output.Body)
			args = append(args, file)
			referenceStartCore(t, binary, args, clientPort)
			proxyURL, err := url.Parse("http://127.0.0.1:" + strconv.Itoa(clientPort))
			if err != nil {
				t.Fatal(err)
			}
			transport := &http.Transport{Proxy: http.ProxyURL(proxyURL)}
			defer transport.CloseIdleConnections()
			client := &http.Client{Transport: transport, Timeout: 2 * time.Second}
			deadline := time.Now().Add(5 * time.Second)
			for {
				// The mixed listener may open before Mihomo's provider starts.
				// Require a real successful response, not just an open port.
				response, requestErr := client.Get(target.URL)
				status, result := 0, ""
				if requestErr == nil {
					status = response.StatusCode
					body, readErr := io.ReadAll(response.Body)
					_ = response.Body.Close()
					result, requestErr = string(body), readErr
					if requestErr == nil && status == http.StatusOK && result == "referenced-route-ok" {
						return
					}
				}
				if time.Now().After(deadline) {
					t.Fatalf("referenced %s could not connect: %v status=%d body=%q", expectedName, requestErr, status, result)
				}
				time.Sleep(25 * time.Millisecond)
			}
		})
	}
}

func referenceFreePort(t *testing.T) int {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	return port
}

func referenceCoreFile(t *testing.T, name string, content []byte) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, content, 0600); err != nil {
		t.Fatal(err)
	}
	return path
}

func referenceStartCore(t *testing.T, binary string, args []string, port int) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	logPath := filepath.Join(t.TempDir(), "core.log")
	log, err := os.Create(logPath)
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	command := exec.CommandContext(ctx, binary, args...)
	command.Stdout, command.Stderr = log, log
	command.Env = append(os.Environ(), "ENABLE_DEPRECATED_SPECIAL_OUTBOUNDS=true")
	if err := command.Start(); err != nil {
		cancel()
		_ = log.Close()
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- command.Wait() }()
	stopped := false
	t.Cleanup(func() {
		cancel()
		if !stopped {
			<-done
		}
		_ = log.Close()
		if t.Failed() {
			content, _ := os.ReadFile(logPath)
			t.Logf("%s log:\n%s", filepath.Base(binary), content)
		}
	})
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		select {
		case err := <-done:
			stopped = true
			t.Fatalf("%s exited before readiness: %v", filepath.Base(binary), err)
		default:
		}
		connection, err := net.DialTimeout("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)), 100*time.Millisecond)
		if err == nil {
			_ = connection.Close()
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatalf("%s did not become ready", filepath.Base(binary))
}
