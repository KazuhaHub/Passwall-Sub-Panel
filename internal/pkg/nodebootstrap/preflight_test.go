package nodebootstrap

import (
	"regexp"
	"strings"
	"testing"

	"github.com/KazuhaHub/passwall-node/deployment"
)

// The published module, not a copied checklist or sibling checkout, is the
// installation contract. A future SDK bump must not silently defer a missing
// prerequisite until after stopping the old service and converting PSP.
func TestMigrationPreflightCoversPublishedInstallerTools(t *testing.T) {
	installer, err := deployment.RenderLinux(deployment.Options{
		Endpoint: "https://panel.example/v1/node/sync", AgentID: "agt_test-node",
		Credential: "pspn_" + strings.Repeat("a", 40), Version: "v0.0.1-beta3",
	})
	if err != nil {
		t.Fatal(err)
	}
	toolList := regexp.MustCompile(`(?m)^for tool in ([a-z0-9 -]+); do$`)
	required := toolList.FindStringSubmatch(installer)
	preflight := toolList.FindStringSubmatch(linuxMigrationTemplate)
	if len(required) != 2 || len(preflight) != 2 {
		t.Fatal("published installer or wrapper tool contract changed; review the preflight")
	}
	have := map[string]bool{}
	for _, tool := range strings.Fields(preflight[1]) {
		have[tool] = true
	}
	for _, tool := range strings.Fields(required[1]) {
		if !have[tool] {
			t.Errorf("formal installer prerequisite %s is missing before conversion", tool)
		}
	}
	assertBootstrapOrder(t, linuxMigrationTemplate,
		"for tool in ", "timeout 90 systemctl stop x-ui.service", "http_result=$(curl ")
}

func TestMigrationPreflightCPUAndIdleSystemdJobContracts(t *testing.T) {
	for _, required := range []string{
		`case "$(uname -m)" in`,
		"x86_64|amd64|aarch64|arm64) ;;",
		`--property=Job --value`,
		`[[ "$job" == '' || "$job" == 0 ]]`,
	} {
		if !strings.Contains(linuxMigrationTemplate, required) {
			t.Errorf("missing CPU or idle systemd job contract: %s", required)
		}
	}
	assertBootstrapOrder(t, linuxMigrationTemplate,
		`case "$(uname -m)" in`, "for tool in ", `--property=Job --value`,
		"timeout 90 systemctl stop x-ui.service", "http_result=$(curl ")
}
