package rulematch

import "testing"

func mustProbe(t *testing.T, in ProbeInput) *Probe {
	t.Helper()
	p, err := NewProbe(in)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func TestNewProbeNormalisesAndRejects(t *testing.T) {
	cases := []struct {
		name string
		in   ProbeInput
		code string // "" = accepted
		host string
		isIP bool
	}{
		{"lowercase and trailing dot", ProbeInput{Host: " Claude.AI. "}, "", "claude.ai", false},
		{"bracketed v6", ProbeInput{Host: "[2001:db8::1]"}, "", "2001:db8::1", true},
		{"v4 literal", ProbeInput{Host: "8.8.8.8"}, "", "8.8.8.8", true},
		{"empty", ProbeInput{Host: " "}, "address_empty", "", false},
		{"idn", ProbeInput{Host: "例え.jp"}, "address_idn", "", false},
		{"url", ProbeInput{Host: "https://claude.ai/"}, "address_invalid", "", false},
		{"zone id", ProbeInput{Host: "fe80::1%eth0"}, "address_invalid", "", false},
		{"too long", ProbeInput{Host: string(make([]byte, 0)) + repeat("a", 254)}, "address_invalid", "", false},
		{"bad port", ProbeInput{Host: "a.b", Port: 70000}, "port_invalid", "", false},
		{"bad network", ProbeInput{Host: "a.b", Network: "icmp"}, "network_invalid", "", false},
		{"bad resolved ip", ProbeInput{Host: "a.b", IP: "nope"}, "resolved_invalid", "", false},
		{"bad src ip", ProbeInput{Host: "a.b", SrcIP: "nope"}, "src_invalid", "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p, err := NewProbe(tc.in)
			if tc.code != "" {
				pe, ok := err.(*ProbeError)
				if !ok || pe.Code != tc.code {
					t.Fatalf("err = %v, want code %s", err, tc.code)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if p.Host != tc.host || p.IsIP != tc.isIP || p.Network != "tcp" {
				t.Fatalf("probe = %+v", p)
			}
		})
	}
}

func repeat(s string, n int) string {
	out := ""
	for i := 0; i < n; i++ {
		out += s
	}
	return out
}

func TestParseRuleFollowsMihomoPositions(t *testing.T) {
	cases := []struct {
		line, tp, value, target string
		opts                    []string
	}{
		{"DOMAIN-SUFFIX, claude.ai , Proxy", "DOMAIN-SUFFIX", "claude.ai", "Proxy", nil},
		{"IP-CIDR,1.0.0.0/8,Proxy,no-resolve", "IP-CIDR", "1.0.0.0/8", "Proxy", []string{"no-resolve"}},
		{"MATCH,Final", "MATCH", "", "Final", nil},
		{"AND,((NETWORK,UDP),(DST-PORT,443)),Quic", "AND", "((NETWORK,UDP),(DST-PORT,443))", "Quic", nil},
		{"DOMAIN-REGEX,^a{1,3}$,Proxy", "DOMAIN-REGEX", "^a{1,3}$", "Proxy", nil},
		{"SUB-RULE,(NETWORK,UDP),sub", "SUB-RULE", "(NETWORK,UDP)", "sub", nil},
	}
	for _, tc := range cases {
		r := ParseRule(tc.line)
		if r.Type != tc.tp || r.Value != tc.value || r.Target != tc.target || len(r.Options) != len(tc.opts) {
			t.Errorf("%s -> %+v", tc.line, r)
		}
	}
}

func TestWalkThreeValuedRoute(t *testing.T) {
	p := mustProbe(t, ProbeInput{Host: "claude.ai", Port: 443})
	cont := func(target string, udp bool) bool { return target == "Switch" }
	entries := []string{
		"DOMAIN,other.com,A",
		"GEOSITE,cn,CN",                // 1: undecided, above the first hit
		"DOMAIN-SUFFIX,claude.ai,PASS", // 2: PASS is skipped
		"DOMAIN-KEYWORD,claude,Switch", // 3: first hit, may continue
		"DOMAIN-SUFFIX,ai,Later",       // 4
		"MATCH,Final",                  // 5
		"NOT-A-RULE",                   // no outbound: skipped
	}
	r := Walk(entries, p, cont)
	if r.First == nil || r.First.Index != 3 || r.Final {
		t.Fatalf("first = %+v final=%v", r.First, r.Final)
	}
	if r.MaybeTotal != 1 || r.Maybe[0].Index != 1 || r.Maybe[0].Reason != ReasonClientData {
		t.Fatalf("maybe = %+v", r.Maybe)
	}
	if len(r.Passed) != 1 || r.Passed[0].Index != 2 {
		t.Fatalf("passed = %+v", r.Passed)
	}
	if r.LaterTotal != 2 || r.Later[0].Index != 4 || r.Later[1].Index != 5 {
		t.Fatalf("later = %+v", r.Later)
	}
}

func TestWalkNoResolveAfterEarlierResolvingRule(t *testing.T) {
	// The first address rule has no no-resolve, so Mihomo resolves the name when
	// it reaches it; the later no-resolve rule then sees the address.
	p := mustProbe(t, ProbeInput{Host: "example.com", IP: "1.2.3.4"})
	r := Walk([]string{"IP-CIDR,9.9.9.0/24,A", "IP-CIDR,1.2.3.0/24,B,no-resolve"}, p, nil)
	if r.First == nil || r.First.Index != 1 || !r.Final || r.MaybeTotal != 0 {
		t.Fatalf("route = %+v", r)
	}
	// Without the earlier resolving rule the second one is undecided (the
	// address the caller gave would fit).
	r = Walk([]string{"IP-CIDR,1.2.3.0/24,B,no-resolve"}, p, nil)
	if r.First != nil || r.MaybeTotal != 1 {
		t.Fatalf("route = %+v", r)
	}
}

func TestSafeRegexSubset(t *testing.T) {
	ok := []string{`^a\.b$`, `^(?:a|b)+\.c$`, `[a-z0-9-]+`, `a{2}`, `a{2,}`, `a{2,5}`, `a|b`, `\d\w\s`}
	for _, s := range ok {
		if safeRegex(s) == nil {
			t.Errorf("%q should be accepted", s)
		}
	}
	bad := []string{``, `(?<n>a)`, `(?=a)`, `\bx`, `a*?`, `a+?`, `a++`, `(?i)a`, `[[:alpha:]]`, `[]`, `a{,3}`, `a{3,1}`, `a{`, `}`, `*a`, `\A`, `(a`, `a)`, `[a-\d]`, `\p{L}`}
	for _, s := range bad {
		if safeRegex(s) != nil {
			t.Errorf("%q should be refused", s)
		}
	}
}

func TestWalkNoResolveWithoutAddressIsABranch(t *testing.T) {
	p := mustProbe(t, ProbeInput{Host: "example.com"})
	r := Walk([]string{"IP-CIDR,1.2.3.0/24,A,no-resolve", "MATCH,B"}, p, nil)
	if r.MaybeTotal != 1 || r.Maybe[0].Reason != ReasonInput || r.First == nil || r.First.Index != 1 {
		t.Fatalf("route = %+v", r)
	}
}
