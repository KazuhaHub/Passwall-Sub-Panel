package nodebootstrap

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// The harness rewrites every deployment/proc path to a fresh test directory
// and replaces systemctl before executing anything. It never changes a real
// service, process, installation, database or network configuration.
func TestLinuxMigrationStubRuntime(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix filesystem stub harness; Windows compilation is supported")
	}
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash is not available")
	}
	realChmod, err := exec.LookPath("chmod")
	if err != nil {
		t.Skip("chmod is not available")
	}
	for _, scenario := range []struct {
		name        string
		deployment  string
		http        string
		residual    bool
		core        bool
		symlink     bool
		container   bool
		arch        string
		job         string
		missingTool string
		wantOK      bool
		wantStop    bool
		wantCurl    bool
	}{
		{name: "clean system", deployment: "not-found", http: "200", wantOK: true, wantCurl: true},
		{name: "HTTP failure never executes payload", deployment: "not-found", http: "500", wantCurl: true},
		{name: "redirect never executes payload", deployment: "not-found", http: "302", wantCurl: true},
		{name: "unknown unit", deployment: "error", http: "200"},
		{name: "partial installation", deployment: "not-found", residual: true, http: "200"},
		{name: "clean node unknown core", deployment: "not-found", core: true, http: "200"},
		{name: "container", deployment: "not-found", container: true, http: "200"},
		{name: "standard old installation", deployment: "loaded", http: "200", wantOK: true, wantStop: true, wantCurl: true},
		{name: "standard installation HTTP failure", deployment: "loaded", http: "503", wantStop: true, wantCurl: true},
		{name: "standard installation lingering core", deployment: "loaded", core: true, http: "200", wantStop: true},
		{name: "symlinked configuration", deployment: "loaded", symlink: true, http: "200"},
		{name: "unsupported old host 32-bit CPU", deployment: "loaded", arch: "i686", http: "200"},
		{name: "unsupported clean host armv7", deployment: "not-found", arch: "armv7l", http: "200"},
		{name: "supported clean arm64", deployment: "not-found", arch: "aarch64", http: "200", wantOK: true, wantCurl: true},
		{name: "old host has pending systemd job", deployment: "loaded", job: "123", http: "200"},
		{name: "standard idle Job property empty", deployment: "loaded", job: "idle-empty", http: "200", wantOK: true, wantStop: true, wantCurl: true},
		{name: "missing installer useradd on old host", deployment: "loaded", missingTool: "useradd", http: "200"},
		{name: "missing installer checksum tool on clean host", deployment: "not-found", missingTool: "sha256sum", http: "200"},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			root, err := filepath.EvalSymlinks(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			bin := filepath.Join(root, "commands")
			mustMkdir(t, bin)
			for _, relative := range []string{"run/systemd/system", "var/backups", "proc"} {
				mustMkdir(t, filepath.Join(root, relative))
			}
			if scenario.residual || scenario.deployment == "loaded" {
				mustMkdir(t, filepath.Join(root, "usr/local/x-ui"))
			}
			if scenario.deployment == "loaded" {
				mustMkdir(t, filepath.Join(root, "etc/systemd/system"))
				mustMkdir(t, filepath.Join(root, "etc/x-ui"))
				mustWrite(t, filepath.Join(root, "etc/systemd/system/x-ui.service"), "[Service]\nExecStart=/usr/local/x-ui/x-ui\n", 0600)
				mustWrite(t, filepath.Join(root, "usr/local/x-ui/x-ui"), "old binary fixture", 0700)
				mustWrite(t, filepath.Join(root, "etc/x-ui/x-ui.db"), "old SQLite fixture", 0600)
				mustWrite(t, filepath.Join(root, "etc/x-ui/certificate.key"), "old secret configuration fixture", 0600)
			}
			if scenario.symlink {
				mustWrite(t, filepath.Join(root, "foreign-key"), "foreign fixture", 0600)
				if err := os.Symlink(filepath.Join(root, "foreign-key"), filepath.Join(root, "etc/x-ui/linked.key")); err != nil {
					t.Fatal(err)
				}
			}
			if scenario.core {
				mustMkdir(t, filepath.Join(root, "proc/123"))
				mustWrite(t, filepath.Join(root, "xray-linux-amd64"), "core fixture", 0700)
				mustWrite(t, filepath.Join(root, "proc/123/comm"), "xray-linux-amd6\n", 0600)
				if err := os.Symlink(filepath.Join(root, "xray-linux-amd64"), filepath.Join(root, "proc/123/exe")); err != nil {
					t.Fatal(err)
				}
			}
			writeCommand := func(name, body string) {
				mustWrite(t, filepath.Join(bin, name), "#!/usr/bin/env bash\nset -euo pipefail\n"+body, 0700)
			}
			writeCommand("id", "printf '0\\n'\n")
			writeCommand("uname", "if [[ \"$1\" == -s ]]; then printf 'Linux\\n'; else printf '%s\\n' \"$HARNESS_ARCH\"; fi\n")
			if scenario.container {
				writeCommand("systemd-detect-virt", "exit 0\n")
			} else {
				writeCommand("systemd-detect-virt", "exit 1\n")
			}
			writeCommand("stat", "case \"$2\" in\n'%u %a') printf '0 700\\n';;\n'%a') printf '700\\n';;\n'%s') wc -c < \"${@: -1}\";;\n*) exit 91;;\nesac\n")
			writeCommand("flock", "exit 0\n")
			// These formal Node installer prerequisites are checked but are
			// not invoked by this wrapper or its minimal stub payload.
			for _, tool := range []string{"sha256sum", "awk", "getent", "useradd", "chown", "cmp", "mv", "mkdir"} {
				writeCommand(tool, "exit 0\n")
			}
			// Linux coreutils accepts `chmod MODE -- FILE`; the macOS test
			// host does not. Preserve actual permission changes in this shim.
			writeCommand("chmod", "mode=$1\nshift\nif [[ ${1-} == -- ]]; then shift; fi\nexec "+shellQuote(realChmod)+" \"$mode\" \"$@\"\n")
			writeCommand("timeout", "shift\nexec \"$@\"\n")
			writeCommand("tar", "while [[ $# -gt 0 ]]; do\nif [[ \"$1\" == -cf ]]; then printf 'config archive fixture' > \"$2\"; exit 0; fi\nshift\ndone\nexit 92\n")
			writeCommand("sqlite3", "if [[ \"$2\" == 'PRAGMA quick_check;' ]]; then printf 'ok\\n'; exit 0; fi\ndestination=${2#.backup }\ndestination=${destination#\\'}\ndestination=${destination%\\'}\ncp -- \"$1\" \"$destination\"\n")
			writeCommand("systemctl", `printf '%s\n' "$*" >> "$HARNESS_ROOT/systemctl.log"
if [[ "$1" == stop || "$1" == disable ]]; then exit 0; fi
[[ "$1" == show && "$2" == x-ui.service ]] || exit 93
case "$3" in
--property=LoadState) printf '%s\n' "$HARNESS_DEPLOYMENT";;
--property=Job) if [[ "$HARNESS_JOB" == idle-empty ]]; then printf '\n'; else printf '%s\n' "$HARNESS_JOB"; fi;;
--property=FragmentPath) printf '%s\n' "$HARNESS_ROOT/etc/systemd/system/x-ui.service";;
--property=DropInPaths) printf '\n';;
--property=KillMode) printf 'control-group\n';;
--property=WorkingDirectory) printf '%s\n' "$HARNESS_ROOT/usr/local/x-ui/";;
--property=ExecStart) printf '{ path=%s/usr/local/x-ui/x-ui ; argv[]=%s/usr/local/x-ui/x-ui ; }\n' "$HARNESS_ROOT" "$HARNESS_ROOT";;
--property=ExecStartPre|--property=ExecStartPost|--property=ExecStop|--property=ExecStopPost) printf '\n';;
--property=EnvironmentFiles) printf '\n';;
--property=Environment) printf 'XRAY_VMESS_AEAD_FORCED=false\n';;
--property=MainPID|--property=ControlPID) printf '0\n';;
--property=ActiveState) printf 'inactive\n';;
*) exit 94;;
esac
`)
			writeCommand("curl", `printf 'called\n' >> "$HARNESS_ROOT/curl.log"
[[ "$1" == --disable ]] || exit 95
output=''; authorization=''; body=''
while [[ $# -gt 0 ]]; do
case "$1" in
--output) output=$2; shift 2;;
--header) if [[ "$2" == @* ]]; then authorization=${2#@}; fi; shift 2;;
--data) body=$2; shift 2;;
--location|-L) exit 96;;
*) [[ "$1" != *"${HARNESS_TICKET}"* ]] || exit 97; shift;;
esac
done
[[ "$body" == '{"old_backend_stopped":true}' ]] || exit 98
[[ "$(cat "$authorization")" == "Authorization: Bearer $HARNESS_TICKET" ]] || exit 99
[[ -f "$authorization" && ! -L "$authorization" ]] || exit 100
[[ "$(ls -l "$authorization")" == -rw-------* ]] || exit 101
[[ "$(ls -l "$output")" == -rw-------* ]] || exit 102
printf '#!/usr/bin/env bash\nprintf installed > %s\n' "'$HARNESS_ROOT/executed'" > "$output"
printf '%s text/plain; charset=utf-8' "$HARNESS_HTTP"
`)
			token := strings.Repeat("x", 32)
			script, err := RenderLinuxMigration(Options{CompletionURL: "https://psp.example/api/node-bootstrap/complete", Token: token})
			if err != nil {
				t.Fatal(err)
			}
			var replacements []string
			for _, prefix := range []string{"/run/systemd/system", "/.dockerenv", "/run/.containerenv", "/opt/passwall-node", "/usr/local/x-ui", "/usr/local/bin/x-ui", "/usr/bin/x-ui", "/etc/systemd/system", "/etc/x-ui", "/etc/default/x-ui", "/var/backups", "/var", "/proc"} {
				replacements = append(replacements, prefix, root+prefix)
			}
			script = strings.NewReplacer(replacements...).Replace(script)
			// Override only the shell's lookup result, never a real host tool.
			// This allows testing missing tools that happen to exist on CI.
			script = strings.Replace(script, "umask 077\n", "umask 077\ncommand() { if [[ \"${1-}\" == -v && \"${2-}\" == \"${HARNESS_MISSING_TOOL:-}\" ]]; then return 1; fi; builtin command \"$@\"; }\n", 1)
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			command := exec.CommandContext(ctx, "bash")
			command.Stdin = strings.NewReader(script)
			arch := scenario.arch
			if arch == "" {
				arch = "x86_64"
			}
			job := scenario.job
			if job == "" {
				job = "0"
			}
			command.Env = append(os.Environ(), "PATH="+bin+string(os.PathListSeparator)+os.Getenv("PATH"), "HARNESS_ROOT="+root, "HARNESS_DEPLOYMENT="+scenario.deployment, "HARNESS_HTTP="+scenario.http, "HARNESS_TICKET="+token, "HARNESS_ARCH="+arch, "HARNESS_JOB="+job, "HARNESS_MISSING_TOOL="+scenario.missingTool)
			output, err := command.CombinedOutput()
			if (err == nil) != scenario.wantOK {
				t.Fatalf("success=%v, want %v: %s", err == nil, scenario.wantOK, output)
			}
			if strings.Contains(string(output), token) {
				t.Fatal("authorization leaked to stdout/stderr")
			}
			if exists(filepath.Join(root, "executed")) != scenario.wantOK {
				t.Fatalf("untrusted/failing response executed=%v", exists(filepath.Join(root, "executed")))
			}
			if exists(filepath.Join(root, "curl.log")) != scenario.wantCurl {
				t.Fatalf("completion called=%v, want %v: %s", exists(filepath.Join(root, "curl.log")), scenario.wantCurl, output)
			}
			calls, _ := os.ReadFile(filepath.Join(root, "systemctl.log"))
			if strings.Contains(string(calls), "stop x-ui.service") != scenario.wantStop || strings.Contains(string(calls), "disable x-ui.service") != scenario.wantStop {
				t.Fatalf("unexpected old service mutation: %s", calls)
			}
			if scenario.wantStop {
				matches, err := filepath.Glob(filepath.Join(root, "var/backups/passwall-node-migration/backup.*"))
				if err != nil || len(matches) != 1 {
					t.Fatalf("backup missing: %v", err)
				}
				for _, name := range []string{"x-ui.service", "x-ui.db", "etc-x-ui.tar"} {
					info, err := os.Stat(filepath.Join(matches[0], name))
					if err != nil || info.Mode().Perm() != 0600 {
						t.Fatalf("private backup %s missing/unsafe: %v", name, err)
					}
				}
				if !scenario.core {
					for _, name := range []string{"final-x-ui.db", "final-etc-x-ui.tar"} {
						if !exists(filepath.Join(matches[0], name)) {
							t.Fatalf("final stopped-state backup missing: %s", name)
						}
					}
				}
			}
			privateWork, _ := filepath.Glob(filepath.Join(root, "var/backups/passwall-node-migration/.bootstrap.*"))
			for _, path := range privateWork {
				if !strings.HasSuffix(path, ".lock") {
					t.Fatalf("private authorization/install staging was not removed: %s", path)
				}
			}
		})
	}
}

func mustMkdir(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(path, 0700); err != nil {
		t.Fatal(err)
	}
}

func mustWrite(t *testing.T, path, content string, mode os.FileMode) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), mode); err != nil {
		t.Fatal(err)
	}
}

func exists(path string) bool {
	_, err := os.Lstat(path)
	return err == nil
}
