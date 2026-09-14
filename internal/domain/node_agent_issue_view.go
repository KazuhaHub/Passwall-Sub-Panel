package domain

// NodeAgentIssueView is a display filter, never a resolution or review state.
// The zero value retains the complete durable inbox for existing consumers.
type NodeAgentIssueView string

const (
	NodeAgentIssueViewAll        NodeAgentIssueView = "all"
	NodeAgentIssueViewAttention  NodeAgentIssueView = "attention"
	NodeAgentIssueViewDiagnostic NodeAgentIssueView = "diagnostic"
)

// NodeAgentIssueSignature deliberately excludes object/version identity: these
// exact startup observations have the same meaning for either core version.
type NodeAgentIssueSignature struct {
	Code   string
	Detail string
}

// NodeAgentIssueDiagnosticSignatures is the sole conservative allowlist for
// moving routine startup observations out of the operator's attention view.
// Return a value copy so callers cannot modify the classification globally.
// Degraded telemetry, unknown reports and historical errors are NOT diagnostics.
func NodeAgentIssueDiagnosticSignatures() [2]NodeAgentIssueSignature {
	return [2]NodeAgentIssueSignature{
		{Code: "core_telemetry_failed", Detail: "collect core telemetry: xray process is starting"},
		{Code: "core_telemetry_failed", Detail: "collect core telemetry: sing-box process is starting"},
	}
}

func IsNodeAgentIssueDiagnostic(code, detail string) bool {
	for _, signature := range NodeAgentIssueDiagnosticSignatures() {
		if code == signature.Code && detail == signature.Detail {
			return true
		}
	}
	return false
}
