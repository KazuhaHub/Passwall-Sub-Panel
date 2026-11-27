package render

import (
	"reflect"
	"testing"
)

// TestBuildSingBoxRouteRules_ClashRuleOptions pins how the trailing Clash
// options (`no-resolve`, `src`) are consumed. Before this, a line such as
// `IP-CIDR,1.2.3.0/24,DIRECT,src` was compiled with the outbound "src" — a tag
// that does not exist — because only `no-resolve` was skipped.
func TestBuildSingBoxRouteRules_ClashRuleOptions(t *testing.T) {
	tests := []struct {
		name     string
		line     string
		wantKey  string // key expected in the route rule; "" means the line is dropped
		wantVal  string
		wantOut  string
		notWants []string
	}{
		{"src selects source ip", "IP-CIDR,1.2.3.0/24,DIRECT,src", "source_ip_cidr", "1.2.3.0/24", "direct", []string{"ip_cidr"}},
		{"src ip6 then no-resolve", "IP-CIDR6,2001:db8::/32,Proxy,src,no-resolve", "source_ip_cidr", "2001:db8::/32", "Proxy", []string{"ip_cidr"}},
		{"no-resolve then src", "IP-CIDR6,2001:db8::/32,Proxy,no-resolve,src", "source_ip_cidr", "2001:db8::/32", "Proxy", []string{"ip_cidr"}},
		{"SRC is not src (Mihomo compares options exactly)", "IP-CIDR,1.2.3.0/24,DIRECT,SRC", "ip_cidr", "1.2.3.0/24", "direct", []string{"source_ip_cidr"}},
		{"no option keeps destination ip", "IP-CIDR,1.2.3.0/24,DIRECT", "ip_cidr", "1.2.3.0/24", "direct", []string{"source_ip_cidr"}},
		{"no-resolve alone keeps destination ip", "IP-CIDR,1.2.3.0/24,REJECT,no-resolve", "ip_cidr", "1.2.3.0/24", "block", []string{"source_ip_cidr"}},
		// Mihomo only reads `src` for the IP-type rules; DOMAIN-SUFFIX ignores
		// the parameter, so the rule still applies with the real outbound.
		{"src ignored on domain rule", "DOMAIN-SUFFIX,example.com,Proxy,src", "domain_suffix", "example.com", "Proxy", nil},
		// Mihomo's GEOIP honours `src` (source country). sing-box rule_sets
		// match the destination only, so compiling it as plain GEOIP would
		// silently change which traffic matches; the line is dropped instead.
		{"geoip src is unsupported", "GEOIP,CN,DIRECT,src", "", "", "", nil},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			rules, _ := buildSingBoxRouteRules("- " + tc.line + "\n- MATCH,DIRECT\n")
			if tc.wantKey == "" {
				if len(rules) != 1 {
					t.Fatalf("want only the sniff rule, got %#v", rules)
				}
				return
			}
			if len(rules) != 2 {
				t.Fatalf("want sniff + one route rule, got %#v", rules)
			}
			got := rules[1]
			if !reflect.DeepEqual(got[tc.wantKey], []string{tc.wantVal}) {
				t.Fatalf("%s = %#v, want [%s] (rule %#v)", tc.wantKey, got[tc.wantKey], tc.wantVal, got)
			}
			if got["outbound"] != tc.wantOut {
				t.Fatalf("outbound = %#v, want %q", got["outbound"], tc.wantOut)
			}
			for _, key := range tc.notWants {
				if _, present := got[key]; present {
					t.Fatalf("unexpected %s in %#v", key, got)
				}
			}
		})
	}
}

// TestSingBoxAndMihomoAgreeOnRuleTarget guards against the two rule parsers
// drifting apart again: both must pick the same field as the outbound.
func TestSingBoxAndMihomoAgreeOnRuleTarget(t *testing.T) {
	lines := []string{
		"DOMAIN,example.com,Proxy",
		"IP-CIDR,1.2.3.0/24,DIRECT",
		"IP-CIDR,1.2.3.0/24,DIRECT,no-resolve",
		"IP-CIDR,1.2.3.0/24,DIRECT,src",
		"IP-CIDR,1.2.3.0/24,DIRECT,SRC",
		"IP-CIDR6,2001:db8::/32,Proxy,src,no-resolve",
		"IP-CIDR6,2001:db8::/32,Proxy,no-resolve,src",
		`DOMAIN-SUFFIX,example.com,"🚀 节点选择"`,
		"AND,((NETWORK,UDP),(DST-PORT,443)),⚡ QUIC控制",
		"MATCH,DIRECT",
	}
	for _, line := range lines {
		t.Run(line, func(t *testing.T) {
			fields := splitRuleFields(line)
			want := ruleOutboundTarget(fields)
			_, _, got, ok := parseClashRuleLine(line)
			if !ok || got != want {
				t.Fatalf("sing-box target = %q (ok=%v), mihomo target = %q", got, ok, want)
			}
		})
	}
}
