package destpolicy

import (
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/KazuhaHub/passwall-protocol/protocol"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
)

// THE TEST TOOL NEVER DOES DNS. PSP's resolver is not the node's, so a lookup
// would look authoritative and would not be. Under on_demand it takes the
// addresses the admin says the name resolves to, and without them says the
// answer is conditional.

func resolveTestHarness(t *testing.T, mode domain.DestResolve, caps ...string) *adminHarness {
	t.Helper()
	ah := newAdminHarness(t)
	ah.policy("lan", domain.DestActionBlock, domain.DestStateOn, func(p *domain.DestPolicy) {
		p.Inline = domain.DestPolicyInline{Private: true}
	})
	ah.publishNow()
	ah.panels.panels[9].DestResolve = mode
	ah.admin.Agents.(fakeAgents).agents[0].ObservedCapabilities = caps
	return ah
}

func TestTest_AsIsIsUnchangedByResolvedIPs(t *testing.T) {
	ah := resolveTestHarness(t, domain.DestResolveAsIs, policyCap, resolveCap)
	res, err := ah.admin.Test(ah.ctx, TestInput{Target: "rebind.example.com", PanelID: 9})
	if err != nil {
		t.Fatal(err)
	}
	if res.Verdict != "default" || !slices.Contains(res.Notes, string(protocol.NoteHostnameSkipsIPRules)) {
		t.Fatalf("as_is: %s %v", res.Verdict, res.Notes)
	}
}

func TestTest_OnDemandWithoutAddressesIsConditional(t *testing.T) {
	ah := resolveTestHarness(t, domain.DestResolveOnDemand, policyCap, resolveCap)
	res, err := ah.admin.Test(ah.ctx, TestInput{Target: "rebind.example.com", PanelID: 9})
	if err != nil {
		t.Fatal(err)
	}
	if !res.Conditional || !slices.Contains(res.Notes, string(protocol.NoteHostnameNeedsResolution)) {
		t.Fatalf("result = %+v", res)
	}
}

func TestTest_OnDemandUsesTheGivenAddresses(t *testing.T) {
	ah := resolveTestHarness(t, domain.DestResolveOnDemand, policyCap, resolveCap)
	res, err := ah.admin.Test(ah.ctx, TestInput{Target: "rebind.example.com", PanelID: 9, ResolvedIPs: []string{"93.184.216.34", "10.0.0.5"}})
	if err != nil {
		t.Fatal(err)
	}
	if res.Verdict != "block" || res.Conditional {
		t.Fatalf("any resolved address in a blocked range blocks: %+v", res)
	}
	res, _ = ah.admin.Test(ah.ctx, TestInput{Target: "rebind.example.com", PanelID: 9, ResolvedIPs: []string{"93.184.216.34"}})
	if res.Verdict != "default" || res.Conditional || slices.Contains(res.Notes, string(protocol.NoteHostnameNeedsResolution)) {
		t.Fatalf("a public address: %+v", res)
	}
}

// An empty list is a failed lookup, which is not the same as no list.
func TestTest_AnEmptyListIsAFailedResolution(t *testing.T) {
	ah := resolveTestHarness(t, domain.DestResolveOnDemand, policyCap, resolveCap)
	res, err := ah.admin.Test(ah.ctx, TestInput{Target: "nxdomain.example.com", PanelID: 9, ResolvedIPs: []string{}})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(res.Notes, string(protocol.NoteResolutionFailedEngineDependent)) {
		t.Fatalf("notes = %v", res.Notes)
	}
}

// The effective mode is the node's: a panel set to on_demand whose node cannot
// run it, or whose inbound would substitute the name, is tested as as_is.
func TestTest_UsesTheModeTheNodeWouldRun(t *testing.T) {
	old := resolveTestHarness(t, domain.DestResolveOnDemand, policyCap)
	res, err := old.admin.Test(old.ctx, TestInput{Target: "rebind.example.com", PanelID: 9, ResolvedIPs: []string{"10.0.0.5"}})
	if err != nil {
		t.Fatal(err)
	}
	if res.Verdict != "default" {
		t.Fatalf("a node without the capability runs as_is, so the IP rule never judges the name: %+v", res)
	}

	ro := resolveTestHarness(t, domain.DestResolveOnDemand, policyCap, resolveCap)
	ro.nodes.nodes[2].Sniffing = routeOnlySniffing
	res, err = ro.admin.Test(ro.ctx, TestInput{Target: "rebind.example.com", PanelID: 9, ResolvedIPs: []string{"10.0.0.5"}})
	if err != nil {
		t.Fatal(err)
	}
	if res.Verdict != "default" {
		t.Fatalf("a routeOnly inbound holds the node at as_is: %+v", res)
	}
}

func TestTest_RefusesBadResolvedIPs(t *testing.T) {
	ah := resolveTestHarness(t, domain.DestResolveOnDemand, policyCap, resolveCap)
	tooMany := make([]string, 17)
	for i := range tooMany {
		tooMany[i] = "10.0.0.1"
	}
	for name, ips := range map[string][]string{
		"hostname":  {"example.com"},
		"cidr":      {"10.0.0.0/8"},
		"empty":     {""},
		"junk":      {"999.1.1.1"},
		"with port": {"10.0.0.1:80"},
		"too many":  tooMany,
	} {
		_, err := ah.admin.Test(ah.ctx, TestInput{Target: "x.example.com", PanelID: 9, ResolvedIPs: ips})
		var ve *ValidationError
		if !errors.As(err, &ve) || ve.Code != CodeTestInvalid || ve.Field != "resolved_ips" {
			t.Errorf("%s: err = %v, want a resolved_ips validation error", name, err)
		}
	}
	// Sixteen, IPv6 included, is fine; spaces around an entry are not an address.
	ok := append(slices.Clone(tooMany[:15]), "2001:db8::1")
	if _, err := ah.admin.Test(ah.ctx, TestInput{Target: "x.example.com", PanelID: 9, ResolvedIPs: ok}); err != nil {
		t.Fatalf("16 literals: %v", err)
	}
	if _, err := ah.admin.Test(ah.ctx, TestInput{Target: "x.example.com", PanelID: 9, ResolvedIPs: []string{" 10.0.0.1"}}); err == nil || !strings.Contains(err.Error(), "resolved_ips") {
		t.Fatalf("a padded literal: %v", err)
	}
}
