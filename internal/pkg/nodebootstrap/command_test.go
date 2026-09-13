package nodebootstrap

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
)

func TestInstallCommandShortSingleLineAndPrivateContracts(t *testing.T) {
	url := "https://panel.example/private-panel/node-bootstrap/" + strings.Repeat("a", 43)
	command, err := InstallCommand(url)
	if err != nil {
		t.Fatal(err)
	}
	if len(command) > 320 || strings.ContainsAny(command, "\r\n\x00") {
		t.Fatal("one-click command is not a short single line")
	}
	for _, required := range []string{"set +a +x", "umask 077", "unset s", "curl -qf", "--proto =https", "-m 30", "--max-filesize 1048576", "&& [[ $s ]]", `bash -n <<<"$s" 2>/dev/null`, `&& bash <<<"$s"`} {
		if !strings.Contains(command, required) {
			t.Errorf("missing command contract: %s", required)
		}
	}
	for _, forbidden := range []string{"mktemp", "trap ", "--location", "--insecure", "--verbose", "--trace", "| bash", "pspn_"} {
		if strings.Contains(command, forbidden) {
			t.Errorf("unsafe or unnecessarily complex command: %s", forbidden)
		}
	}
}

func TestInstallCommandRejectsUnsafeURLs(t *testing.T) {
	for _, url := range []string{"", "http://panel.example/node-bootstrap/x", "https://user:secret@panel.example/x", "https://panel.example/x?q=secret", "https://panel.example/x#fragment", "https://panel.example:0/x", "https://panel.example:65536/x", "https://panel.example/x\nwhoami", "https://panel.example/x\\y", "https://panel.example/x y", "https://panel.example/x\x00y"} {
		if _, err := InstallCommand(url); !errors.Is(err, domain.ErrValidation) {
			t.Errorf("unsafe URL was accepted: %q", url)
		}
	}
}

// Only a fixture curl and disposable fixture files are used. No real network,
// installer, service, identity, or system directory is involved.
func TestInstallCommandBufferedRuntime(t *testing.T) {
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("bash is unavailable on this platform")
	}
	for _, scenario := range []struct {
		name    string
		payload string
		status  int
		wantOK  bool
		wantRun bool
	}{
		{"success", "printf ran > \"$HARNESS_ROOT/executed\"\nprintf '%s' \"${s-unexported}\" > \"$HARNESS_ROOT/child-variable\"\n", 0, true, true},
		{"inherited allexport stays private", "printf ran > \"$HARNESS_ROOT/executed\"\nprintf '%s' \"${s-unexported}\" > \"$HARNESS_ROOT/child-variable\"\n", 0, true, true},
		{"failed transfer with valid executable prefix", "printf ran > \"$HARNESS_ROOT/executed\"\n", 18, false, false},
		{"HTTP failure", "printf ran > \"$HARNESS_ROOT/executed\"\n", 22, false, false},
		{"timeout after body", "printf ran > \"$HARNESS_ROOT/executed\"\n", 28, false, false},
		{"oversized transfer", "printf ran > \"$HARNESS_ROOT/executed\"\n", 63, false, false},
		{"empty successful response", "", 0, false, false},
		{"invalid script does not execute valid prefix or echo secrets", "printf ran > \"$HARNESS_ROOT/executed\"\ncredential='pspn_PRIVATE_SYNTAX_FIXTURE", 0, false, false},
		{"installer failure is preserved", "exit 42\n", 0, false, false},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			root := t.TempDir()
			bin := filepath.Join(root, "bin")
			mustMkdir(t, bin)
			mustWrite(t, filepath.Join(root, "response"), scenario.payload, 0600)
			// The first option must disable curlrc. Require all bounds and an
			// exact positional URL, and prove an inherited exported s is gone.
			curl := "#!" + bash + "\n" + `[[ ${s-} == '' ]] || exit 91
[[ $1 == -qf && $2 == --proto && $3 == =https && $4 == -m && $5 == 30 && $6 == --max-filesize && $7 == 1048576 && $8 == "$HARNESS_URL" && $# == 8 ]] || exit 92
while IFS= read -r line || [[ -n $line ]]; do printf '%s\n' "$line"; done < "$HARNESS_ROOT/response"
exit "$HARNESS_STATUS"
`
			mustWrite(t, filepath.Join(bin, "curl"), curl, 0700)
			// Reuse the actual Bash, not a payload interpreter substitute.
			if err := os.Symlink(bash, filepath.Join(bin, "bash")); err != nil {
				t.Fatal(err)
			}
			url := "https://panel.invalid/node-bootstrap/" + strings.Repeat("a", 43)
			command, err := InstallCommand(url)
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, bash, "--noprofile", "--norc", "-c", command)
			cmd.Env = []string{"PATH=" + bin, "LANG=C", "HARNESS_ROOT=" + root, "HARNESS_URL=" + url, "HARNESS_STATUS=" + strconv.Itoa(scenario.status), "s=INHERITED_EXPORTED_FIXTURE"}
			if scenario.name == "inherited allexport stays private" {
				cmd.Env = append(cmd.Env, "SHELLOPTS=allexport")
			}
			output, err := cmd.CombinedOutput()
			if (err == nil) != scenario.wantOK || exists(filepath.Join(root, "executed")) != scenario.wantRun {
				t.Fatalf("success=%v executed=%v output=%s", err == nil, exists(filepath.Join(root, "executed")), output)
			}
			if strings.Contains(string(output), "pspn_") || strings.Contains(string(output), "INHERITED_EXPORTED_FIXTURE") {
				t.Fatal("private script or inherited variable leaked into diagnostics")
			}
			if scenario.wantOK {
				value, err := os.ReadFile(filepath.Join(root, "child-variable"))
				if err != nil || string(value) != "unexported" {
					t.Fatal("private script was exported to the child process")
				}
			}
		})
	}
}
