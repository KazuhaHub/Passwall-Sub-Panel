package handler

import (
	"encoding/json"
	"testing"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
)

// A CDN line is accepted without an address (it defaults to the domain, in the
// service), carries its domain, and comes back as mode "cdn" with the issues
// found against its landing.
func TestCDNLineRequestAndResponse(t *testing.T) {
	var input []relayLineDTO
	body := `[{"mode":"cdn","domain":" CDN.example.com ","name":"CF","enabled":true},` +
		`{"mode":"cdn","domain":"b.example.com","address":"104.16.1.1","port":8443,"sni":"b.example.com","host":"b.example.com","enabled":true}]`
	if err := json.Unmarshal([]byte(body), &input); err != nil {
		t.Fatal(err)
	}
	lines, err := parseRelays(input)
	if err != nil {
		t.Fatal(err)
	}
	if !lines[0].CDN() || lines[0].Domain != "CDN.example.com" || lines[0].Address != "" || lines[0].Port != 0 || lines[0].EntryPanelID != 0 {
		t.Fatalf("line 0 = %+v", lines[0])
	}
	if lines[1].Address != "104.16.1.1" || lines[1].Port != 8443 {
		t.Fatalf("line 1 = %+v", lines[1])
	}
	// The service settles them, then the response carries mode, domain, issues.
	if err := domain.SettleCDNLines(nil, lines); err != nil {
		t.Fatal(err)
	}
	landing := &domain.Node{ID: 1, DisplayName: "JP", DesiredProtocol: "vless", DesiredPort: 10443, Relays: lines,
		StreamSettings: `{"network":"ws","security":"tls","tlsSettings":{"serverName":"cdn.example.com"}}`}
	out := relayDTOsOn(landing, landing.Relays, false)
	if out[0].Mode == nil || *out[0].Mode != "cdn" || out[0].Domain != "cdn.example.com" || out[0].Address != "cdn.example.com" || out[0].Port != 443 {
		t.Fatalf("response = %+v", out[0])
	}
	var codes []string
	for _, issue := range out[0].CDNIssues {
		codes = append(codes, issue.Code)
	}
	if len(codes) != 3 || codes[0] != domain.CDNIssueCertUnverifiable || codes[1] != domain.CDNIssueOriginPort || codes[2] != domain.CDNIssuePortDiffers {
		t.Fatalf("issues = %v", codes)
	}
	// A line that is not a CDN line carries none, and the lines-only helper
	// (no landing) says nothing.
	if got := relayDTOsFor(lines, false); len(got[0].CDNIssues) != 0 {
		t.Fatalf("issues without a landing: %+v", got[0].CDNIssues)
	}
	// A forward line still needs its address.
	if _, err := parseRelays([]relayLineDTO{{Name: "x", Enabled: true}}); err == nil {
		t.Fatal("a forward line without an address must be refused")
	}
	if _, err := parseRelays([]relayLineDTO{{Mode: strPtr("bogus"), Address: "a"}}); err == nil {
		t.Fatal("unknown mode must be refused")
	}
}
