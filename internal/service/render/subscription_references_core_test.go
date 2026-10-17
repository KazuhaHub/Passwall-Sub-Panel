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
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"gopkg.in/yaml.v3"

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
	for _, scenario := range []string{"vless_route", "hidden_direct_route", "collision_both", "collision_single", "same_type_routes", "direct_special", "direct_quoted", "direct_outer_quoted", "legacy_direct", "legacy_relay"} {
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
	if strings.HasPrefix(scenario, "legacy_") {
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
			node.HideDirect = scenario == "hidden_direct_route"
			node.ServerAddress, node.DesiredPort, node.Flow = "127.0.0.1", serverPort, ""
			node.StreamSettings = `{"network":"tcp","security":"none"}`
			node.RouteState.Binding = domain.RouteBinding("https://panel.example", node)
			rule.Content = "- MATCH,OnlyRoute"
			rule.ProxyGroupMembers["OnlyRoute"] = rule.ProxyGroupMembers["国外媒体"]
			delete(rule.ProxyGroupMembers, "国外媒体")
			expectedName, expectedUUID := "A B", "12345678-1234-03e9-8def-123456789abc"
			if scenario == "same_type_routes" {
				line := &node.RouteState.Lines[0]
				line.Name, line.OnlyLineName, line.ID = "same", true, "52787c21-62ab-4496-94fb-6f4b4fb51312"
				other := *line
				other.ID, other.Route = "6b752549-4a51-4d6a-bd6c-81930ee5e715", 1002
				node.RouteState.Lines = append(node.RouteState.Lines, other)
				rule.ProxyGroupMembers["OnlyRoute"] = []domain.ProxyGroupMember{{Kind: "line", NodeID: node.ID, LineID: line.ID}, {Kind: "line", NodeID: node.ID, LineID: other.ID}}
				expectedName = "same [VLESS 路由:52787c21]"
			}
			if strings.HasPrefix(scenario, "collision_") {
				node.Relays = []domain.RelayLine{{ID: "relay", Name: "same", OnlyLineName: true, Address: "127.0.0.1", Port: serverPort, Enabled: true}}
				node.RouteState.Lines[0].Name, node.RouteState.Lines[0].OnlyLineName = "same", true
				expectedName = "same"
				if scenario == "collision_both" {
					rule.ProxyGroupMembers["OnlyRoute"] = append(rule.ProxyGroupMembers["OnlyRoute"], domain.ProxyGroupMember{Kind: "line", NodeID: node.ID, LineID: "relay"})
					expectedName = "same [VLESS 路由]"
				}
			}
			if scenario == "direct_special" {
				node.RouteState.Lines[0].Name, node.RouteState.Lines[0].OnlyLineName = `same, "quoted"`, true
				expectedName = `same, "quoted"`
				rawRules, err := yaml.Marshal([]string{`MATCH,"same, ""quoted"" [7:route-B]"`})
				if err != nil {
					t.Fatal(err)
				}
				rule.Content = string(rawRules)
			}
			if scenario == "direct_quoted" {
				node.RouteState.Lines[0].Name, node.RouteState.Lines[0].OnlyLineName = `line "quoted"`, true
				expectedName = `line "quoted"`
				rawRules, err := yaml.Marshal([]string{`MATCH,line "quoted" [7:route-B]`})
				if err != nil {
					t.Fatal(err)
				}
				rule.Content = string(rawRules)
			}
			if scenario == "direct_outer_quoted" {
				node.RouteState.Lines[0].Name, node.RouteState.Lines[0].OnlyLineName = `"quoted"`, true
				expectedName = `"quoted"`
				rawRules, err := yaml.Marshal([]string{`MATCH,"quoted" [7:route-B]`})
				if err != nil {
					t.Fatal(err)
				}
				rule.Content = string(rawRules)
			}
			if strings.HasPrefix(scenario, "legacy_") {
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
			if scenario == "direct_special" && ct == domain.ClientMihomo {
				if err == nil || !strings.Contains(err.Error(), "through a proxy group") {
					t.Fatalf("Mihomo unrepresentable inline policy must fail explicitly: %v", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if ct == domain.ClientMihomo {
				preview, err := s.PreviewMihomoGroup(context.Background(), 1, rule)
				if err != nil {
					t.Fatal(err)
				}
				var config struct {
					Groups []struct {
						Name    string   `yaml:"name"`
						Members []string `yaml:"proxies"`
					} `yaml:"proxy-groups"`
				}
				if err := yaml.Unmarshal(output.Body, &config); err != nil {
					t.Fatal(err)
				}
				if len(config.Groups) != len(preview.Groups) {
					t.Fatal("core fixture preview group count differs")
				}
				for i, group := range config.Groups {
					if preview.Groups[i].Name != group.Name || !reflect.DeepEqual(preview.Groups[i].Members, group.Members) {
						t.Fatalf("preview does not match core configuration: %+v vs %+v", preview.Groups, config.Groups)
					}
				}
			}
			// Require the actual expected proxy, not merely an HTTP success via
			// a DIRECT fallback after an unavailable member was filtered out.
			foundRoute := false
			vlessCount := 0
			for _, proxy := range subscriptionProxyMaps(t, output, ct) {
				if proxy["type"] == "vless" {
					vlessCount++
				}
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
			if (scenario == "collision_both" || scenario == "same_type_routes") && vlessCount != 2 || scenario == "collision_single" && vlessCount != 1 {
				t.Fatalf("collision transition emitted %d VLESS proxies", vlessCount)
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
