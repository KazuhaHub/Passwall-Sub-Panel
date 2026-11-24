package nodesync

import (
	"strings"
	"testing"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

// A node whose CDN lines cannot work any more raises cdn_landing_unfit naming
// the lines and the reason; a fit node, a node without CDN lines and a node
// whose transport is not known raise nothing.
func TestCDNLandingUnfitIssue(t *testing.T) {
	cdn := []domain.RelayLine{{ID: "c", Name: "CF", Mode: domain.RelayModeCDN, Domain: "cdn.example.com", Enabled: true}, {ID: "f", Address: "x", Enabled: true}}
	node := func(id int64, stream string, relays []domain.RelayLine) *domain.Node {
		return &domain.Node{ID: id, DisplayName: "Tokyo", DesiredProtocol: "vless", StreamSettings: stream, Relays: relays}
	}
	snapshot := &ports.NativeDesiredSnapshot{Nodes: []*domain.Node{
		node(1, `{"network":"tcp","security":"reality"}`, cdn),
		node(2, `{"network":"ws","security":"tls"}`, cdn),
		node(3, `{"network":"tcp","security":"reality"}`, nil),
		node(4, "", cdn),
	}}
	got := cdnUnfitIssues(snapshot)
	if len(got) != 1 || got[0].Code != IssueCDNLandingUnfit || !strings.Contains(got[0].Detail, `"CF"`) || !strings.Contains(got[0].Detail, "REALITY") || !strings.Contains(got[0].Detail, "Tokyo") {
		t.Fatalf("issues = %+v", got)
	}
}

// A line withheld for its client TLS setting on a gRPC or Trojan landing raises
// cdn_line_client_tls with the reason code.
func TestCDNLineClientTLSIssue(t *testing.T) {
	off := false
	line := []domain.RelayLine{{ID: "c", Name: "CF", Mode: domain.RelayModeCDN, Domain: "cdn.example.com", ClientTLS: &off, Enabled: true}}
	snapshot := &ports.NativeDesiredSnapshot{Nodes: []*domain.Node{
		{ID: 1, DisplayName: "Tokyo", DesiredProtocol: "vless", StreamSettings: `{"network":"grpc","security":"tls","tlsSettings":{}}`, Relays: line},
		{ID: 2, DisplayName: "Osaka", DesiredProtocol: "vless", StreamSettings: `{"network":"ws","security":"tls","tlsSettings":{}}`, Relays: line},
	}}
	got := cdnUnfitIssues(snapshot)
	if len(got) != 1 || got[0].Code != IssueCDNLineClientTLS || !strings.Contains(got[0].Detail, domain.CodeCDNClientTLSGRPC) || !strings.Contains(got[0].Detail, "Tokyo") {
		t.Fatalf("issues = %+v", got)
	}
}
