package acceptance_test

import (
	"context"
	"encoding/json"
	"errors"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

var nodePhasePrefix = regexp.MustCompile(`(?m)^Passwall Node \[([1-6])/6\] (ERROR\b)?`)
var migrationPhasePrefix = regexp.MustCompile(`(?m)^Passwall Node migration: \[([1-4])/4\] `)
var curlErrorPrefix = regexp.MustCompile(`(?m)^curl: \(([0-9]{1,3})\)`)
var curlHTTPErrorPrefix = regexp.MustCompile(`(?m)^curl: \(22\) The requested URL returned error: ([0-9]{3})\b`)

// The only information allowed out of a captured private command is numeric
// stage/status/exit identity and boolean matches of known error text. Neither
// raw output nor an arbitrary error string is retained in this classification.
func classifyCommandFailure(output []byte, commandErr error) map[string]any {
	result := map[string]any{
		"output_present": len(output) != 0, "exit_code": -1,
		"context_canceled": errors.Is(commandErr, context.Canceled),
		"context_deadline": errors.Is(commandErr, context.DeadlineExceeded),
		"node_last_phase":  0, "node_error_phase": 0,
		"migration_last_phase": 0, "curl_exit_code": 0, "curl_http_status": 0,
	}
	var exitErr *exec.ExitError
	if errors.As(commandErr, &exitErr) {
		result["exit_code"] = exitErr.ExitCode()
	}
	for _, match := range nodePhasePrefix.FindAllSubmatch(output, -1) {
		phase, _ := strconv.Atoi(string(match[1]))
		result["node_last_phase"] = phase
		if len(match[2]) != 0 {
			result["node_error_phase"] = phase
		}
	}
	for _, match := range migrationPhasePrefix.FindAllSubmatch(output, -1) {
		result["migration_last_phase"], _ = strconv.Atoi(string(match[1]))
	}
	if match := curlErrorPrefix.FindSubmatch(output); match != nil {
		result["curl_exit_code"], _ = strconv.Atoi(string(match[1]))
	}
	if match := curlHTTPErrorPrefix.FindSubmatch(output); match != nil {
		result["curl_http_status"], _ = strconv.Atoi(string(match[1]))
	}
	text := string(output)
	for name, knownMessages := range map[string][]string{
		"node_lock_busy":             {"another installation is running; inspect the installation lock manually"},
		"node_identity_conflict":     {"existing identity, endpoint, credential or version differs; manual migration is required"},
		"node_incomplete_or_foreign": {"existing installation is incomplete", "existing installation requires manual inspection", "existing installation directories require manual inspection", "existing binary requires manual repair"},
		"node_unit_conflict":         {"existing systemd unit differs; manual migration is required", "existing systemd unit has no matching installation; manual migration is required"},
		"node_download_failed":       {"checksum manifest download failed; no installation was published", "release archive download failed; no installation was published"},
		"node_verification_failed":   {"release checksum verification failed", "release binary cannot execute", "release binary version does not match the selected release", "release archive has a missing or duplicate required member"},
		"node_account_refused":       {"existing service account is not dedicated to this installation", "service account must have a non-root numeric UID", "service account must not have an interactive shell"},
		"node_helper_setup_failed":   {"remote upgrade setup failed; installed identity and data were retained"},
		"node_systemd_failed":        {"systemd reload failed or timed out", "service startup failed or timed out"},
		"node_readiness_failed":      {"agent did not reach active/running with a nonzero PID within 30s"},
		"node_unexpected_failure":    {"installation stopped; inspect this phase before retrying"},
		"shell_input_or_syntax":      {"syntax error", "unexpected end of file", "unbound variable"},
	} {
		matched := false
		for _, known := range knownMessages {
			matched = matched || strings.Contains(text, known)
		}
		result[name] = matched
	}
	return result
}

func classifyUnitState(property string, output []byte) string {
	state := strings.TrimSpace(string(output))
	for _, known := range map[string][]string{
		"LoadState":   {"loaded", "not-found", "error", "masked", "stub", "merged"},
		"ActiveState": {"active", "inactive", "activating", "deactivating", "failed", "reloading", "maintenance", "refreshing"},
	}[property] {
		if state == known {
			return known
		}
	}
	return "unrecognized"
}

func TestCommandFailureClassificationKeepsOnlyKnownStagesAndNumbers(t *testing.T) {
	output := []byte("Passwall Node migration: [3/4] Confirming PSP conversion...\n" +
		"Passwall Node [1/6] Check platform and prerequisites\n" +
		"Passwall Node [5/6] Publish installation\n" +
		"Passwall Node [5/6] ERROR (Publish installation): remote upgrade setup failed; installed identity and data were retained\n" +
		"curl: (22) The requested URL returned error: 409\n")
	result := classifyCommandFailure(output, context.DeadlineExceeded)
	for name, want := range map[string]any{
		"node_last_phase": 5, "node_error_phase": 5, "migration_last_phase": 3,
		"curl_exit_code": 22, "curl_http_status": 409, "node_helper_setup_failed": true,
		"context_deadline": true, "node_download_failed": false,
	} {
		if result[name] != want {
			t.Fatalf("safe classification field %s did not match expectation", name)
		}
	}
}

func TestCommandFailureClassificationNeverRetainsPrivateDiagnostics(t *testing.T) {
	private := "pspn_private_classifier_credential https://private.invalid/node-bootstrap/private-ticket"
	result := classifyCommandFailure([]byte("unknown failure "+private+"\n"), errors.New(private))
	encoded, err := json.Marshal(result)
	if err != nil {
		t.Fatal("safe classification did not encode")
	}
	for _, forbidden := range []string{"pspn_", "private.invalid", "private-ticket", "unknown failure"} {
		if strings.Contains(string(encoded), forbidden) {
			t.Fatal("classification retained private output or error text")
		}
	}
	for _, value := range result {
		switch value.(type) {
		case bool, int:
		default:
			t.Fatal("classification emitted a non-boolean, non-numeric value")
		}
	}
}

func TestCommandFailureUnitStateClassificationUsesOnlyKnownEnums(t *testing.T) {
	for _, test := range []struct{ property, output, want string }{
		{"LoadState", "loaded\n", "loaded"},
		{"ActiveState", "failed\n", "failed"},
		{"ActiveState", "active\npspn_private https://private.invalid", "unrecognized"},
		{"LoadState", "pspn_private", "unrecognized"},
		{"unknown-property", "active", "unrecognized"},
	} {
		if classifyUnitState(test.property, []byte(test.output)) != test.want {
			t.Fatal("unit state diagnostic did not use the known safe enum")
		}
	}
}
