package nodebootstrap

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// THE PUBLISHED INSTALLER IS THE CONTRACT, and this compares the migration
// wrapper's prerequisites against its. A change there that is not mirrored here
// defers a missing tool until after the old service has been stopped and the
// server converted, which is the worst moment to discover it.
//
// IT READS THE NODE CHECKOUT THE CONTRACT JOB ALREADY MAKES. It used to render the
// template through the Node module, which meant this repository compiled that
// module to read one line of it — and that is what kept the module in go.mod, the
// dependency X07 removes. The release publishes THIS FILE, copied into dist/ under
// the asset name, so reading it from the checkout is the same bytes; and the line
// grepped below carries no placeholder, so nothing is lost by not rendering it.
func TestMigrationPreflightCoversPublishedInstallerTools(t *testing.T) {
	installer := publishedInstaller(t)
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

func TestMigrationPreflightRefusesFilesystemRemappingBeforeBackup(t *testing.T) {
	const properties = "RootDirectory RootImage BindPaths BindReadOnlyPaths TemporaryFileSystem MountImages ExtensionImages ExtensionDirectories JoinsNamespaceOf"
	for _, required := range []string{
		"for property in " + properties + "; do",
		`mapping=$(systemctl show x-ui.service --property="$property" --value 2>/dev/null) || fail`,
		`[[ "$mapping" == '' ]] || fail`,
	} {
		if !strings.Contains(linuxMigrationTemplate, required) {
			t.Errorf("missing fail-closed filesystem namespace inspection: %s", required)
		}
	}
	assertBootstrapOrder(t, linuxMigrationTemplate,
		"for property in "+properties+"; do", "has_old=true",
		"timeout 120 sqlite3 /etc/x-ui/x-ui.db", "timeout 90 systemctl stop x-ui.service", "http_result=$(curl ")
}

// publishedInstaller reads the installer a release publishes, from the Node
// checkout the pinned-source contract job provides.
//
// WHEN THERE IS NO CHECKOUT THIS SKIPS, NAMED. That skip is bounded rather than
// hopeful: the CI job that provides the checkout is the one that runs this
// package, so the comparison happens wherever the claim it guards is being made.
func publishedInstaller(t *testing.T) string {
	t.Helper()
	checkout := os.Getenv("PSP_LIVE_NODE_REPO")
	if checkout == "" {
		t.Skip("PSP_LIVE_NODE_REPO is unset, so there is no Node checkout to read the published installer from")
	}
	raw, err := os.ReadFile(filepath.Join(checkout, "deployment", "install.sh"))
	if err != nil {
		t.Fatalf("read the published installer from %s: %v", checkout, err)
	}
	return string(raw)
}
