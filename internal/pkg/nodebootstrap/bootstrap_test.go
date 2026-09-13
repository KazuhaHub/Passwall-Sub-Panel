package nodebootstrap

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/url"
	"os/exec"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
)

const bootstrapTestCompletionURL = "https://panel.example/panel/api/node-bootstrap/job-test/complete"
const bootstrapTestToken = "bootstrap_test_012345678901234567890123456789"

func renderBootstrapTestScript(t *testing.T, options Options) string {
	t.Helper()
	script, err := RenderLinuxMigration(options)
	if err != nil {
		t.Fatalf("RenderLinuxMigration(valid options): %v", err)
	}
	if script == "" {
		t.Fatal("renderer returned an empty installation script")
	}
	return script
}

func TestRenderLinuxMigrationRejectsUnsafeCompletionURLs(t *testing.T) {
	for _, test := range []struct {
		name string
		url  string
	}{
		{"empty", ""},
		{"HTTP", "http://panel.example/api/complete"},
		{"relative", "/api/complete"},
		{"scheme relative", "//panel.example/api/complete"},
		{"opaque HTTPS", "https:panel.example/api/complete"},
		{"no host", "https:///api/complete"},
		{"empty authority", "https://"},
		{"missing endpoint path", "https://panel.example"},
		{"zero port", "https://panel.example:0/api/complete"},
		{"port out of range", "https://panel.example:65536/api/complete"},
		{"non numeric port", "https://panel.example:invalid/api/complete"},
		{"too long", "https://panel.example/" + strings.Repeat("a", 2048)},
		{"userinfo", "https://bootstrap-private-user:bootstrap-private-password@panel.example/api/complete"},
		{"username only", "https://bootstrap-private-user@panel.example/api/complete"},
		{"query", "https://panel.example/api/complete?credential=bootstrap-private-input"},
		{"empty query", "https://panel.example/api/complete?"},
		{"fragment", "https://panel.example/api/complete#bootstrap-private-input"},
		{"file scheme", "file:///api/complete"},
		{"javascript scheme", "javascript:bootstrap-private-input"},
		{"malformed escape", "https://panel.example/%zz/complete"},
		{"backslash authority", "https://panel.example\\untrusted.example/api/complete"},
		{"leading whitespace", " " + bootstrapTestCompletionURL},
		{"trailing whitespace", bootstrapTestCompletionURL + " "},
		{"newline shell injection", bootstrapTestCompletionURL + "\nprintf bootstrap-private-input"},
	} {
		t.Run(test.name, func(t *testing.T) {
			script, err := RenderLinuxMigration(Options{CompletionURL: test.url, Token: bootstrapTestToken})
			if err == nil || script != "" {
				t.Fatalf("unsafe URL accepted: script bytes=%d error=%v", len(script), err)
			}
			if !errors.Is(err, domain.ErrValidation) {
				t.Fatalf("unsafe URL error is not validation: %v", err)
			}
			if strings.Contains(err.Error(), bootstrapTestToken) || strings.Contains(err.Error(), "bootstrap-private-") {
				t.Fatal("validation error disclosed input credentials")
			}
		})
	}
}

func TestRenderLinuxMigrationRejectsLiteralURLControls(t *testing.T) {
	for value := byte(0); value <= 0x7f; value++ {
		if value >= 0x20 && value != 0x7f {
			continue
		}
		t.Run(fmt.Sprintf("ASCII_%02x", value), func(t *testing.T) {
			script, err := RenderLinuxMigration(Options{
				CompletionURL: "https://panel.example/prefix" + string([]byte{value}) + "/complete",
				Token:         bootstrapTestToken,
			})
			if err == nil || script != "" {
				t.Fatalf("URL control accepted: script bytes=%d error=%v", len(script), err)
			}
		})
	}
}

func TestRenderLinuxMigrationRejectsUnsafeTokens(t *testing.T) {
	for _, test := range []struct {
		name  string
		token string
	}{
		{"empty", ""},
		{"too short", strings.Repeat("a", 31)},
		{"too long", strings.Repeat("a", 513)},
		{"space", strings.Repeat("a", 31) + " "},
		{"single quote", strings.Repeat("a", 31) + "'"},
		{"double quote", strings.Repeat("a", 31) + `"`},
		{"dollar substitution", strings.Repeat("a", 32) + "$(printf bootstrap-private-input)"},
		{"backtick substitution", strings.Repeat("a", 32) + "`printf bootstrap-private-input`"},
		{"semicolon", strings.Repeat("a", 32) + ";printf bootstrap-private-input"},
		{"header newline", strings.Repeat("a", 32) + "\r\nAuthorization: bootstrap-private-input"},
		{"newline", strings.Repeat("a", 31) + "\n"},
		{"tab", strings.Repeat("a", 31) + "\t"},
		{"NUL", strings.Repeat("a", 31) + "\x00"},
		{"DEL", strings.Repeat("a", 31) + "\x7f"},
		{"non ASCII", strings.Repeat("a", 31) + "é"},
		{"slash", strings.Repeat("a", 31) + "/"},
		{"base64 padding", strings.Repeat("a", 31) + "="},
		{"dot", strings.Repeat("a", 31) + "."},
	} {
		t.Run(test.name, func(t *testing.T) {
			script, err := RenderLinuxMigration(Options{CompletionURL: bootstrapTestCompletionURL, Token: test.token})
			if err == nil || script != "" {
				t.Fatalf("unsafe token accepted: script bytes=%d error=%v", len(script), err)
			}
			if !errors.Is(err, domain.ErrValidation) {
				t.Fatalf("unsafe token error is not validation: %v", err)
			}
			if strings.Contains(err.Error(), "bootstrap-private-input") || (test.token != "" && strings.Contains(err.Error(), test.token)) {
				t.Fatal("validation error disclosed the token")
			}
		})
	}
}

func TestRenderLinuxMigrationAcceptsTokenBoundaries(t *testing.T) {
	for _, token := range []string{strings.Repeat("a", 32), strings.Repeat("Z", 512), strings.Repeat("aZ09_-", 8)} {
		script := renderBootstrapTestScript(t, Options{CompletionURL: bootstrapTestCompletionURL, Token: token})
		if !strings.Contains(script, token) {
			t.Fatal("valid token was not preserved in the private script")
		}
	}
}

func TestRenderLinuxMigrationQuotesEndpointAsData(t *testing.T) {
	for _, completionURL := range []string{
		bootstrapTestCompletionURL,
		"https://panel.example/prefix'$(printf-bootstrap-private-input)/complete",
		"https://panel.example/prefix`printf-bootstrap-private-input`/complete",
		"https://panel.example/prefix\"$BOOTSTRAP_UNTRUSTED/complete",
		"https://panel.example/@@TICKET@@/complete",
	} {
		t.Run(completionURL, func(t *testing.T) {
			endpoint, err := url.Parse(completionURL)
			if err != nil {
				t.Fatalf("invalid test URL: %v", err)
			}
			script := renderBootstrapTestScript(t, Options{CompletionURL: completionURL, Token: bootstrapTestToken})
			quotedEndpoint := "'" + strings.ReplaceAll(endpoint.String(), "'", "'\"'\"'") + "'"
			if !strings.Contains(script, "\n    "+quotedEndpoint+" 2>/dev/null)") {
				t.Fatal("completion endpoint is not a single, safely quoted curl argument")
			}
			headerWrite := "printf 'Authorization: Bearer %s\\n' '" + bootstrapTestToken + "' > \"$work/authorization\""
			if !strings.Contains(script, "\n"+headerWrite+"\n") || strings.Count(script, bootstrapTestToken) != 1 {
				t.Fatal("ticket must occur only in the shell builtin printf to the private header file")
			}
		})
	}
}

func TestRenderLinuxMigrationShellQuote(t *testing.T) {
	for _, test := range []struct {
		input string
		want  string
	}{
		{"", "''"},
		{"ordinary", "'ordinary'"},
		{"a'b", "'a'\"'\"'b'"},
		{"'", "''\"'\"''"},
		{"$(command);`command`$VARIABLE\"", "'$(command);`command`$VARIABLE\"'"},
	} {
		if got := shellQuote(test.input); got != test.want {
			t.Errorf("shellQuote(%q) = %q; want %q", test.input, got, test.want)
		}
	}
}

func TestRenderLinuxMigrationPrivateRequestContracts(t *testing.T) {
	script := renderBootstrapTestScript(t, Options{CompletionURL: bootstrapTestCompletionURL, Token: bootstrapTestToken})
	for _, required := range []string{
		"set +x\nset -euo pipefail\nunset ENV BASH_ENV CDPATH\numask 077",
		"install -d -m 0700 -- \"$backup_parent\"",
		"work=$(mktemp -d \"$backup_parent/.bootstrap.XXXXXXXXXX\")",
		"flock -n 9",
		"trap cleanup EXIT",
		"trap 'exit 1' HUP INT TERM",
		"chmod 0600 -- \"$work/authorization\"",
		": > \"$work/install.sh\"\nchmod 0600 -- \"$work/install.sh\"",
		"curl --disable --silent --show-error",
		"--proto '=https' --proto-redir '=https'",
		"--connect-timeout 10 --max-time 90 --max-filesize 1048576",
		"--request POST --header \"@$work/authorization\" --header 'Content-Type: application/json'",
		"--data '{\"old_backend_stopped\":true}' --output \"$work/install.sh\"",
		"--write-out '%{http_code} %{content_type}'",
		"[[ \"$status\" =~ ^2[0-9][0-9]$",
		"\"$content_type\" == text/plain || \"$content_type\" == text/plain\\;*",
		"[[ -s \"$work/install.sh\" && \"$(stat -c '%s' -- \"$work/install.sh\")\" -le 1048576 ]]",
		"bash -n \"$work/install.sh\" 2>/dev/null",
	} {
		if !strings.Contains(script, required) {
			t.Errorf("missing private bootstrap contract: %q", required)
		}
	}
	for _, forbidden := range []string{"--location", "--location-trusted", "--insecure", "--verbose", "--trace", "--header 'Authorization:", "export " + bootstrapTestToken} {
		if strings.Contains(script, forbidden) {
			t.Errorf("unsafe bootstrap request option: %q", forbidden)
		}
	}
	// Check the curl command itself: the only bearer-bearing argument belongs to
	// builtin printf, never an external curl process or an environment variable.
	curlStart := strings.Index(script, "http_result=$(curl ")
	if curlStart < 0 {
		t.Fatal("cannot locate completion request")
	}
	curlEnd := strings.Index(script[curlStart:], "\nstatus=")
	if curlEnd < 0 {
		t.Fatal("cannot locate bounded completion request")
	}
	curlCommand := script[curlStart : curlStart+curlEnd]
	if strings.Contains(curlCommand, bootstrapTestToken) || strings.Contains(curlCommand, "Bearer") || regexp.MustCompile(`(^|\s)-[A-Za-z]*[Lkv][A-Za-z]*(\s|$)`).MatchString(curlCommand) {
		t.Fatal("curl exposes the ticket, follows redirects, or enables unsafe diagnostics/TLS")
	}
	assertBootstrapOrder(t, script,
		"chmod 0600 -- \"$work/authorization\"",
		"http_result=$(curl ",
		"[[ \"$status\" =~ ^2[0-9][0-9]$",
		"[[ -s \"$work/install.sh\"",
		"bash -n \"$work/install.sh\"",
		"rm -f -- \"$work/authorization\"\nsay ",
		"\nbash \"$work/install.sh\" || fail ",
	)
}

func TestRenderLinuxMigrationPreservesOldInstallationContracts(t *testing.T) {
	script := renderBootstrapTestScript(t, Options{CompletionURL: bootstrapTestCompletionURL, Token: bootstrapTestToken})
	for _, required := range []string{
		"[[ ! -e /opt/passwall-node && ! -L /opt/passwall-node ]]",
		"[[ \"$start\" == '{ path=/usr/local/x-ui/x-ui ; argv[]=/usr/local/x-ui/x-ui ; '* && \"$start\" != *' } {'* ]]",
		"for property in ExecStartPre ExecStartPost ExecStop ExecStopPost; do",
		"[[ \"$extra_command\" == '' ]]",
		"[[ \"$(systemctl show x-ui.service --property=KillMode --value 2>/dev/null)\" == control-group ]]",
		"find -P /etc/x-ui -xdev -print0 > \"$work/config-paths\"",
		"(( count <= 10000 ))",
		"[[ -d \"$path\" || -f \"$path\" ]]",
		"chmod 0600 -- \"$backup/x-ui.service\" \"$backup/x-ui.db\" \"$backup/etc-x-ui.tar\"",
		"chmod 0600 -- \"$backup/final-x-ui.db\" \"$backup/final-etc-x-ui.tar\"",
	} {
		if !strings.Contains(script, required) {
			t.Errorf("missing old-installation safety contract: %q", required)
		}
	}
	assertBootstrapOrder(t, script,
		"cp -- /etc/systemd/system/x-ui.service \"$backup/x-ui.service\"",
		"timeout 120 sqlite3 /etc/x-ui/x-ui.db \".backup '$backup/x-ui.db'\"",
		"'PRAGMA quick_check;'",
		"timeout 90 systemctl stop x-ui.service",
		"timeout 30 systemctl disable x-ui.service",
		"--property=MainPID --value",
		"--property=ControlPID --value",
		"--property=ActiveState --value",
		"timeout 120 sqlite3 /etc/x-ui/x-ui.db \".backup '$backup/final-x-ui.db'\"",
		"http_result=$(curl ",
	)
	var commands []string
	for _, line := range strings.Split(script, "\n") {
		line = strings.TrimSpace(line)
		if line != "" && !strings.HasPrefix(line, "#") {
			commands = append(commands, line)
		}
		if strings.HasPrefix(line, "rm ") && line != `rm -f -- "$work/authorization" "$work/install.sh" "$work/config-paths"` && line != `rm -f -- "$work/authorization"` {
			t.Fatalf("cleanup removes something beyond freshly-created bootstrap files: %s", line)
		}
	}
	code := strings.Join(commands, "\n")
	for _, forbidden := range []string{
		`(^|[\s;(|&])eval([\s;)|&]|$)`,
		`(?m)^\s*(source|\.)\s`,
		`(?m)^\s*(kill|pkill|killall)\s`,
		`systemctl\s+(start|restart|try-restart|reload-or-restart|enable)\s+x-ui(\.service)?([\s;|&]|$)`,
		`(?m)^\s*(apt|apt-get|dnf|yum|apk)\s+.*\b(remove|purge|del|erase)\b`,
	} {
		if regexp.MustCompile(forbidden).MatchString(code) {
			t.Errorf("bootstrap executes eval/source, kills unknown processes, uninstalls, or rolls back: %q", forbidden)
		}
	}
}

func assertBootstrapOrder(t *testing.T, script string, fragments ...string) {
	t.Helper()
	previous := -1
	for _, fragment := range fragments {
		index := strings.Index(script, fragment)
		if index < 0 {
			t.Fatalf("missing ordered bootstrap operation: %q", fragment)
		}
		if index <= previous {
			t.Fatalf("bootstrap operation occurs out of order: %q", fragment)
		}
		previous = index
	}
}

// -n parses the complete rendered script without invoking any script command.
// In particular these tests never invoke real curl, systemd, or an installer.
func TestRenderLinuxMigrationBashSyntax(t *testing.T) {
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("bash is unavailable on this test platform")
	}
	for _, completionURL := range []string{
		bootstrapTestCompletionURL,
		"https://panel.example:8443/prefix%20space/api/complete",
		"https://[2001:db8::1]:8443/api/complete",
		"https://panel.example/prefix'$(printf-bootstrap-private-input)/complete",
		"https://panel.example/prefix`printf-bootstrap-private-input`/complete",
		"https://panel.example/prefix\"$BOOTSTRAP_UNTRUSTED/complete",
	} {
		t.Run(completionURL, func(t *testing.T) {
			script := renderBootstrapTestScript(t, Options{CompletionURL: completionURL, Token: bootstrapTestToken})
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			command := exec.CommandContext(ctx, bash, "--noprofile", "--norc", "-n")
			command.Env = []string{"PATH=/usr/bin:/bin", "LANG=C", "LC_ALL=C"}
			command.Stdin = strings.NewReader(script)
			var stderr bytes.Buffer
			command.Stderr = &stderr
			if err := command.Run(); err != nil {
				t.Fatalf("rendered script failed bash -n: %v; %s", err, stderr.String())
			}
		})
	}
}
