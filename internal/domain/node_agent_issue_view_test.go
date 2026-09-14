package domain

import (
	"strings"
	"testing"
)

func TestNodeAgentIssueDiagnosticClassificationIsAnExactStartupAllowlist(t *testing.T) {
	for _, signature := range NodeAgentIssueDiagnosticSignatures() {
		if !IsNodeAgentIssueDiagnostic(signature.Code, signature.Detail) {
			t.Fatalf("exact startup signature not diagnostic: %+v", signature)
		}
		for _, variant := range []NodeAgentIssueSignature{
			{Code: "unknown", Detail: signature.Detail},
			{Code: strings.ToUpper(signature.Code), Detail: signature.Detail},
			{Code: signature.Code, Detail: strings.ToUpper(signature.Detail)},
			{Code: signature.Code, Detail: signature.Detail + " "},
			{Code: signature.Code, Detail: signature.Detail + ": telemetry lost"},
			{Code: signature.Code, Detail: " " + signature.Detail},
		} {
			if IsNodeAgentIssueDiagnostic(variant.Code, variant.Detail) {
				t.Fatalf("near match incorrectly moved out of attention: %+v", variant)
			}
		}
	}
	for _, detail := range []string{
		"collect core telemetry: xray process is degraded",
		"collect core telemetry: sing-box process is degraded",
		"collect core telemetry: sing-box statistics snapshot is not ready",
		"unknown future failure",
	} {
		if IsNodeAgentIssueDiagnostic("core_telemetry_failed", detail) {
			t.Fatalf("failure incorrectly classified diagnostic: %q", detail)
		}
	}
	copy := NodeAgentIssueDiagnosticSignatures()
	copy[0].Code = "changed"
	if !IsNodeAgentIssueDiagnostic("core_telemetry_failed", "collect core telemetry: xray process is starting") {
		t.Fatal("mutating returned signatures changed global classification")
	}
}
