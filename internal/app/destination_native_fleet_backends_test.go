//go:build destination_acceptance && linux

package app

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"net"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/KazuhaHub/passwall-sub-panel/internal/pkg/safehttp"
)

type nativeFleetBackends struct{ address, xuiURL, xuiToken, suiURL, suiToken string }

// The existing launcher still owns token minting and panel readiness. This
// fixture supplies a dedicated host network and never substitutes a permissive
// HTTP adapter. Tokens and backend output stay private inside this process.
func nativeStartFleetBackends(t, owner *testing.T) nativeFleetBackends {
	t.Helper()
	if os.Geteuid() != 0 || os.Getenv("GITHUB_ACTIONS") != "true" || os.Getenv("RUNNER_ENVIRONMENT") != "github-hosted" || os.Getenv("GITHUB_REPOSITORY") != "KazuhaHub/Passwall-Sub-Panel" {
		t.Fatal("fleet backends require the disposable hosted Linux job")
	}
	nativeMust(t, exec.Command("docker", "info").Run(), "verify owned fleet container runtime")
	for _, name := range []string{"psp-compat-3xui", "psp-compat-sui"} {
		if exec.Command("docker", "container", "inspect", name).Run() == nil {
			t.Fatal("fleet fixture container name is already owned by another process")
		}
	}
	for _, port := range []int{2053, 2095, 24445, 24446} {
		listener, err := net.Listen("tcp4", net.JoinHostPort("0.0.0.0", strconv.Itoa(port)))
		nativeMust(t, err, "reserve unused fleet backend port")
		nativeMust(t, listener.Close(), "release owned fleet port probe")
	}
	var random [2]byte
	_, err := rand.Read(random[:])
	nativeMust(t, err, "create private fleet address nonce")
	address := "10.254." + strconv.Itoa(int(random[0])%254+1) + "." + strconv.Itoa(int(random[1])%254+1)
	if !safehttp.AllowsIP(net.ParseIP(address)) {
		t.Fatal("owned panel address is refused by the production HTTP guard")
	}
	data, err := exec.Command("ip", "-j", "-4", "addr", "show", "dev", "lo").Output()
	nativeMust(t, err, "read existing fleet addresses")
	var interfaces []struct {
		AddrInfo []struct {
			Local string `json:"local"`
		} `json:"addr_info"`
	}
	nativeMust(t, json.Unmarshal(data, &interfaces), "decode existing fleet addresses")
	for _, iface := range interfaces {
		for _, ip := range iface.AddrInfo {
			if net.ParseIP(ip.Local).Equal(net.ParseIP(address)) {
				t.Fatal("fleet address was already present")
			}
		}
	}
	nativeMust(t, exec.Command("ip", "-4", "addr", "add", address+"/32", "dev", "lo", "scope", "host").Run(), "add exact owned fleet panel address")
	owner.Cleanup(func() {
		nativeMust(owner, exec.Command("ip", "-4", "addr", "del", address+"/32", "dev", "lo").Run(), "remove exact owned fleet panel address")
	})
	data, err = exec.Command("ip", "-j", "-4", "route", "get", address).Output()
	nativeMust(t, err, "verify owned fleet local route")
	var routes []struct{ Type, Dev, Dst string }
	nativeMust(t, json.Unmarshal(data, &routes), "decode fleet local route")
	if len(routes) != 1 || routes[0].Type != "local" || routes[0].Dev != "lo" || !net.ParseIP(routes[0].Dst).Equal(net.ParseIP(address)) {
		t.Fatal("fleet panel address is not the exact local fixture route")
	}
	workdir := filepath.Join(owner.TempDir(), "backends")
	profiles := []struct{ name, image, source, destination string }{
		{"psp-compat-3xui", nativeFleetXUIImage, filepath.Join(workdir, "3xui"), "/etc/x-ui"},
		{"psp-compat-sui", nativeFleetSUIImage, filepath.Join(workdir, "sui"), "/app/db"},
	}
	boundIDs := make(map[string]string)
	owner.Cleanup(func() {
		for _, profile := range profiles {
			data, err := exec.Command("docker", "container", "inspect", profile.name).Output()
			if err != nil {
				continue // an incomplete launcher may not have created both
			}
			var infos []nativeFleetContainer
			if json.Unmarshal(data, &infos) != nil || len(infos) != 1 || !nativeFleetContainerOwned(infos[0], profile.name, profile.image, profile.source, profile.destination) {
				owner.Error("fleet cleanup refused changed container ownership")
				continue
			}
			if bound := boundIDs[profile.name]; bound != "" && infos[0].ID != bound {
				owner.Error("fleet cleanup refused a replaced container identity")
				continue
			}
			nativeMust(owner, exec.Command("docker", "rm", "-f", infos[0].ID).Run(), "remove exact owned fleet container")
		}
	})
	output := &nativePrivateOutput{}
	command := exec.Command("bash", "deploy/compat/backends/third-party.sh", "up")
	command.Env = append(os.Environ(), "PSP_CONTAINER_RUNTIME=docker", "PSP_BACKEND_WORKDIR="+workdir, "PSP_BACKEND_HOST="+address, "PSP_BACKEND_NETWORK=host", "PSP_LIVE_XUI_PORT=2053", "PSP_LIVE_SUI_PORT=2095", "PSP_LIVE_3XUI_IMAGE="+nativeFleetXUIImage, "PSP_LIVE_SUI_IMAGE="+nativeFleetSUIImage)
	command.Stdout, command.Stderr = output, output
	startErr := command.Run()
	if startErr != nil {
		private := output.text()
		// Inspect only this fixture's named container; retain booleans, never
		// its logs (which may include the minted administrator password).
		suiInfo, _ := exec.Command("docker", "container", "inspect", "psp-compat-sui").Output()
		var suiContainers []nativeFleetContainer
		suiOwned := json.Unmarshal(suiInfo, &suiContainers) == nil && len(suiContainers) == 1 && nativeFleetContainerOwned(suiContainers[0], "psp-compat-sui", nativeFleetSUIImage, filepath.Join(workdir, "sui"), "/app/db")
		suiRunning, suiPrivate := false, ""
		if suiOwned {
			suiRunning = suiContainers[0].State.Running
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			suiLogs, _ := exec.CommandContext(ctx, "docker", "logs", "--tail", "500", suiContainers[0].ID).CombinedOutput()
			cancel()
			suiPrivate = strings.ToLower(string(suiLogs))
		}
		for category, present := range map[string]bool{
			"fleet_backend_xui_ready":         strings.Contains(private, "3X-UI ready"),
			"fleet_backend_sui_ready":         strings.Contains(private, "S-UI ready"),
			"fleet_backend_xui_pull":          strings.Contains(private, "pulling "+nativeFleetXUIImage),
			"fleet_backend_sui_pull":          strings.Contains(private, "pulling "+nativeFleetSUIImage),
			"fleet_backend_auth_error":        strings.Contains(private, "curl: (22)") || strings.Contains(private, "KeyError:"),
			"fleet_backend_timeout":           strings.Contains(private, "timed out") || strings.Contains(private, "never created") || strings.Contains(private, "never published"),
			"fleet_backend_registry_error":    strings.Contains(private, "manifest unknown") || strings.Contains(private, "unauthorized") || strings.Contains(private, "denied"),
			"fleet_backend_sql_error":         strings.Contains(private, "sqlite3.OperationalError") || strings.Contains(private, "sqlite3.IntegrityError"),
			"fleet_backend_sui_running":       suiRunning,
			"fleet_backend_sui_bind_error":    strings.Contains(suiPrivate, "address already in use") || strings.Contains(suiPrivate, "bind: permission denied"),
			"fleet_backend_sui_storage_error": strings.Contains(suiPrivate, "database is locked") || strings.Contains(suiPrivate, "readonly database") || strings.Contains(suiPrivate, "permission denied"),
			"fleet_backend_sui_start_error":   strings.Contains(suiPrivate, "failed to start") || strings.Contains(suiPrivate, "start failed") || strings.Contains(suiPrivate, "panic:"),
		} {
			t.Logf("native diagnostic: %s=%t", category, present)
		}
	}
	nativeMust(t, startErr, "start immutable real third-party fleet backends")
	for _, profile := range profiles {
		data, err := exec.Command("docker", "container", "inspect", profile.name).Output()
		nativeMust(t, err, "read actual fleet container provenance")
		var infos []nativeFleetContainer
		nativeMust(t, json.Unmarshal(data, &infos), "decode actual fleet container provenance")
		if len(infos) != 1 || !infos[0].State.Running || !nativeFleetContainerOwned(infos[0], profile.name, profile.image, profile.source, profile.destination) {
			t.Fatal("fleet backend changed its reviewed image, owned mount or host network")
		}
		boundIDs[profile.name] = infos[0].ID
	}
	read := func(name string) string {
		data, err := os.ReadFile(filepath.Join(workdir, name))
		nativeMust(t, err, "read private minted fleet credential")
		value := strings.TrimSpace(string(data))
		if value == "" {
			t.Fatal("real fleet backend did not mint its required credential")
		}
		return value
	}
	backend := nativeFleetBackends{address: address, xuiURL: "http://" + address + ":2053", xuiToken: read("3xui.token"), suiToken: read("sui.token"), suiURL: read("sui.base")}
	parsed, err := url.Parse(backend.suiURL)
	if err != nil || parsed.Scheme != "http" || parsed.Host != net.JoinHostPort(address, "2095") || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		t.Fatal("minted S-UI endpoint escaped the owned container")
	}
	client := safehttp.NewClient(10 * time.Second)
	for _, endpoint := range []string{backend.xuiURL + "/", backend.suiURL + "/"} {
		response, err := client.Get(endpoint)
		nativeMust(t, err, "reach real backend through the production HTTP guard")
		nativeMust(t, response.Body.Close(), "close guarded backend readiness response")
		if response.StatusCode != 200 {
			t.Fatal("real fleet backend is not ready through the production HTTP guard")
		}
	}
	return backend
}
