// Package rulematch decides, for one connection, whether one Mihomo rule line
// matches it. It is pure (standard library only) so the service layer can walk
// an assembled rule list without touching adapters.
//
// The answer is three-valued. A browser or a panel cannot decide every rule:
// GEOSITE / GEOIP / RULE-SET need the client's data files, an address rule
// without no-resolve needs a DNS answer, and a regular expression is only
// evaluated for the subset on which RE2 and Mihomo's regexp2 agree. Such a line
// is Unknown, never No, and everything after it is uncertain.
//
// The semantics follow Mihomo (rules/parser.go, rules/common/*, rules/logic).
// The same fixture (web-react/src/utils/ruleset/rulematch.fixture.json) is read
// by this package's tests and by the SPA's matcher.
package rulematch

import (
	"net/netip"
	"strconv"
	"strings"
)

// Verdict is the three-valued answer for one rule.
type Verdict uint8

const (
	No Verdict = iota
	Yes
	Unknown
)

func (v Verdict) String() string {
	switch v {
	case No:
		return "no"
	case Yes:
		return "yes"
	}
	return "unknown"
}

// Reason says why a line is Unknown.
type Reason string

const (
	ReasonNone        Reason = ""
	ReasonClientData  Reason = "client_data" // GEOIP, GEOSITE, RULE-SET, IP-ASN: needs the client's database
	ReasonResolve     Reason = "resolve"     // needs the domain resolved over DNS
	ReasonInput       Reason = "input"       // the probe did not supply what the rule tests
	ReasonRegex       Reason = "regex"       // outside the subset on which RE2 and regexp2 agree
	ReasonSubRule     Reason = "sub_rule"    // SUB-RULE / REMATCH-NAME are not evaluated here
	ReasonUnsupported Reason = "unsupported" // unknown type or malformed payload
)

// Result is one rule's answer.
type Result struct {
	Verdict Verdict
	Reason  Reason
}

var (
	yes = Result{Verdict: Yes}
	no  = Result{Verdict: No}
)

func unknown(r Reason) Result { return Result{Verdict: Unknown, Reason: r} }
func of(b bool) Result {
	if b {
		return yes
	}
	return no
}

// Probe is one connection. Build it with NewProbe.
type Probe struct {
	// IsIP is true when the connection is to an address rather than a name; such
	// a connection has no domain, so every DOMAIN rule says no.
	IsIP    bool
	Host    string
	IP      netip.Addr // the destination when IsIP
	Port    int        // 0: not given
	Network string     // "tcp" or "udp"
	Process string
	// Resolved is the address a domain resolves to (optional).
	Resolved netip.Addr
	SrcIP    netip.Addr
	SrcPort  int // 0: not given

	// resolvedEarlier is set while walking a list: an earlier rule already made
	// Mihomo resolve the name, so the address is known from then on.
	resolvedEarlier bool
}

// ProbeInput is the probe as a form or JSON body supplies it.
type ProbeInput struct {
	Host    string
	IP      string // address the domain resolves to
	Port    int
	Network string
	Process string
	SrcIP   string
	SrcPort int
}

// ProbeError names the first invalid field (a stable code, not prose).
type ProbeError struct{ Field, Code string }

func (e *ProbeError) Error() string { return "probe " + e.Field + ": " + e.Code }

func parseAddr(s string) (netip.Addr, bool) {
	s = strings.TrimSpace(s)
	if s == "" || strings.ContainsRune(s, '%') {
		return netip.Addr{}, false
	}
	a, err := netip.ParseAddr(s)
	if err != nil {
		return netip.Addr{}, false
	}
	return a, true
}

// NewProbe validates and normalises a probe: the host is lower-cased, brackets
// and a trailing dot are removed, non-ASCII names are refused (enter the xn--
// form), and a host longer than 253 bytes is refused.
func NewProbe(in ProbeInput) (*Probe, error) {
	host := strings.ToLower(strings.TrimSpace(in.Host))
	if host == "" {
		return nil, &ProbeError{"host", "address_empty"}
	}
	for i := 0; i < len(host); i++ {
		if host[i] >= 0x80 {
			return nil, &ProbeError{"host", "address_idn"}
		}
	}
	if strings.HasPrefix(host, "[") && strings.HasSuffix(host, "]") {
		host = host[1 : len(host)-1]
	}
	if strings.HasSuffix(host, ".") && !strings.Contains(host, ":") {
		host = strings.TrimSuffix(host, ".")
	}
	if len(host) > 253 {
		return nil, &ProbeError{"host", "address_invalid"}
	}
	p := &Probe{Host: host, Network: strings.ToLower(strings.TrimSpace(in.Network)), Process: strings.TrimSpace(in.Process)}
	if p.Network == "" {
		p.Network = "tcp"
	}
	if p.Network != "tcp" && p.Network != "udp" {
		return nil, &ProbeError{"network", "network_invalid"}
	}
	if ip, ok := parseAddr(host); ok {
		p.IsIP, p.IP = true, ip
	} else if host == "" || strings.ContainsAny(host, " \t\r\n,/\\:@?#") {
		return nil, &ProbeError{"host", "address_invalid"}
	}
	if in.Port < 0 || in.Port > 65535 {
		return nil, &ProbeError{"port", "port_invalid"}
	}
	p.Port = in.Port
	if in.SrcPort < 0 || in.SrcPort > 65535 {
		return nil, &ProbeError{"src_port", "src_invalid"}
	}
	p.SrcPort = in.SrcPort
	if strings.TrimSpace(in.IP) != "" {
		ip, ok := parseAddr(in.IP)
		if !ok {
			return nil, &ProbeError{"ip", "resolved_invalid"}
		}
		p.Resolved = ip
	}
	if strings.TrimSpace(in.SrcIP) != "" {
		ip, ok := parseAddr(in.SrcIP)
		if !ok {
			return nil, &ProbeError{"src_ip", "src_invalid"}
		}
		p.SrcIP = ip
	}
	return p, nil
}

// Rule is one rule line read the way Mihomo reads it (ParseRulePayload in
// rules/common/base.go): the position of the target is fixed by the type, not
// searched for.
type Rule struct {
	Type    string // upper case
	Value   string
	Target  string
	Options []string
	// Valid is false for a line Mihomo would refuse to load (no payload).
	Valid bool
}

// logicPayloadTypes take everything between the type and the target as one
// payload: their payloads hold commas and need no options.
func payloadSpansCommas(tp string) bool {
	switch tp {
	case "NOT", "OR", "AND", "SUB-RULE", "DOMAIN-REGEX", "PROCESS-NAME-REGEX", "PROCESS-PATH-REGEX":
		return true
	}
	return false
}

func trimParts(s string) []string {
	parts := strings.Split(s, ",")
	for i := range parts {
		parts[i] = strings.Trim(parts[i], " ")
	}
	return parts
}

// ParseRule reads one rule entry (the YAML scalar, without the leading "- ").
func ParseRule(entry string) Rule {
	item := trimParts(strings.TrimSpace(entry))
	r := Rule{Type: strings.ToUpper(item[0])}
	if len(item) > 1 {
		switch {
		case r.Type == "MATCH":
			r.Target = item[1]
		case payloadSpansCommas(r.Type):
			n := len(item)
			r.Target = item[n-1]
			r.Value = strings.Join(item[1:n-1], ",")
		default:
			r.Value = item[1]
			if len(item) > 2 {
				r.Target = item[2]
				if len(item) > 3 {
					r.Options = item[3:]
				}
			}
		}
	}
	r.Valid = r.Type == "MATCH" || r.Value != ""
	return r
}

func hasOption(opts []string, name string) bool {
	for _, o := range opts {
		if o == name { // Mihomo compares exactly (rules/common/base.go ParseParams)
			return true
		}
	}
	return false
}

// Eval decides one parsed rule for the probe.
func Eval(r Rule, p *Probe) Result {
	return evalAtom(r.Type, r.Value, r.Options, p, 0)
}

const maxDepth = 6

func evalAtom(tp, value string, opts []string, p *Probe, depth int) Result {
	if tp != "MATCH" && value == "" {
		return unknown(ReasonUnsupported)
	}
	switch tp {
	case "MATCH":
		return yes
	case "DOMAIN":
		if p.IsIP {
			return no
		}
		return of(p.Host == strings.ToLower(value))
	case "DOMAIN-SUFFIX":
		if p.IsIP {
			return no
		}
		s := strings.ToLower(value)
		return of(p.Host == s || strings.HasSuffix(p.Host, "."+s))
	case "DOMAIN-KEYWORD":
		if p.IsIP {
			return no
		}
		return of(strings.Contains(p.Host, strings.ToLower(value)))
	case "DOMAIN-REGEX":
		if p.IsIP {
			return no
		}
		re := regexFor(value)
		if re == nil {
			return unknown(ReasonRegex)
		}
		return of(re.MatchString(p.Host))
	case "IP-CIDR", "IP-CIDR6":
		return addressRule(value, opts, hasOption(opts, "src"), p)
	case "SRC-IP-CIDR":
		return addressRule(value, opts, true, p)
	case "DST-PORT", "SRC-PORT":
		ranges, ok := parsePorts(value)
		if !ok {
			return unknown(ReasonUnsupported)
		}
		have := p.Port
		if tp == "SRC-PORT" {
			have = p.SrcPort
		}
		if have == 0 {
			return unknown(ReasonInput)
		}
		for _, r := range ranges {
			if have >= r[0] && have <= r[1] {
				return yes
			}
		}
		return no
	case "NETWORK":
		switch strings.ToUpper(value) {
		case "TCP":
			return of(p.Network == "tcp")
		case "UDP":
			return of(p.Network == "udp")
		}
		return unknown(ReasonUnsupported)
	case "PROCESS-NAME":
		if p.Process == "" {
			return unknown(ReasonInput)
		}
		// strings.EqualFold: rules/common/process.go.
		return of(strings.EqualFold(p.Process, value))
	case "AND", "OR", "NOT":
		return logical(tp, value, p, depth)
	case "SUB-RULE", "REMATCH-NAME":
		return unknown(ReasonSubRule)
	case "GEOIP", "GEOSITE", "RULE-SET", "IP-ASN", "SRC-GEOIP", "SRC-IP-ASN":
		return unknown(ReasonClientData)
	}
	return unknown(ReasonUnsupported)
}

func addressRule(value string, opts []string, source bool, p *Probe) Result {
	net, err := netip.ParsePrefix(strings.TrimSpace(value))
	if err != nil || net.Addr().Zone() != "" {
		return unknown(ReasonUnsupported)
	}
	if source {
		if !p.SrcIP.IsValid() {
			return unknown(ReasonInput)
		}
		return of(net.Contains(p.SrcIP))
	}
	if p.IsIP {
		return of(net.Contains(p.IP))
	}
	// A domain: its address is only known if it was resolved, and no-resolve is
	// the rule's promise not to resolve it.
	if hasOption(opts, "no-resolve") {
		if p.resolvedEarlier {
			if p.Resolved.IsValid() {
				return of(net.Contains(p.Resolved))
			}
			return unknown(ReasonResolve)
		}
		// A rule on its own cannot know whether an earlier one resolved the
		// name (a hosts entry or fake-IP mapping may have too); if the address
		// the caller gave would fit, that is undecided rather than a no.
		if p.Resolved.IsValid() && net.Contains(p.Resolved) {
			return unknown(ReasonResolve)
		}
		return no
	}
	if p.Resolved.IsValid() {
		return of(net.Contains(p.Resolved))
	}
	return unknown(ReasonResolve)
}

// parsePorts reads Mihomo's port syntax (common/utils/ranges.go): "/"-separated
// entries, each a port or a lo-hi range (a reversed range is swapped).
func parsePorts(value string) ([][2]int, bool) {
	value = strings.TrimSpace(strings.ReplaceAll(value, ",", "/"))
	if value == "" || value == "*" {
		return nil, false
	}
	list := strings.Split(value, "/")
	if len(list) > 28 {
		return nil, false
	}
	var out [][2]int
	for _, seg := range list {
		seg = strings.TrimSpace(seg)
		if seg == "" {
			continue
		}
		bounds := strings.Split(seg, "-")
		if len(bounds) > 2 {
			return nil, false
		}
		var n [2]int
		for i, b := range bounds {
			b = strings.Trim(b, "[ ]")
			v, err := strconv.ParseUint(b, 10, 16)
			if err != nil {
				return nil, false
			}
			n[i] = int(v)
		}
		if len(bounds) == 1 {
			n[1] = n[0]
		}
		if n[0] > n[1] {
			n[0], n[1] = n[1], n[0]
		}
		out = append(out, n)
	}
	return out, len(out) > 0
}

// topLevelChildren returns the (...) groups directly inside the outer
// parentheses of s, the way Mihomo's Logic.format/findSubRuleRange do.
func topLevelChildren(s string) ([]string, bool) {
	if !strings.HasPrefix(s, "(") || !strings.HasSuffix(s, ")") {
		return nil, false
	}
	var out []string
	depth, start := 0, 0
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '(':
			depth++
			if depth == 2 {
				start = i
			}
		case ')':
			if depth == 0 {
				return nil, false
			}
			if depth == 2 {
				out = append(out, s[start+1:i])
			}
			depth--
			if depth == 0 && i != len(s)-1 {
				return nil, false // the outer group closed early: not one "(...)"
			}
		}
	}
	if depth != 0 {
		return nil, false
	}
	return out, true
}

func logical(tp, value string, p *Probe, depth int) Result {
	text := strings.TrimSpace(value)
	if depth > maxDepth {
		return unknown(ReasonUnsupported)
	}
	children, ok := topLevelChildren(text)
	if !ok || len(children) == 0 {
		return unknown(ReasonUnsupported)
	}
	subs := make([]Result, 0, len(children))
	for _, child := range children {
		item := trimParts(child)
		if len(item) < 2 || item[0] == "" {
			return unknown(ReasonUnsupported)
		}
		st := strings.ToUpper(item[0])
		if st == "MATCH" || st == "SUB-RULE" {
			return unknown(ReasonUnsupported) // Mihomo refuses these inside a logic rule
		}
		var sv string
		var sopts []string
		if payloadSpansCommas(st) {
			sv = strings.Join(item[1:], ",")
		} else {
			sv = item[1]
			sopts = item[2:]
		}
		subs = append(subs, evalAtom(st, sv, sopts, p, depth+1))
	}
	var firstUnknown *Result
	for i := range subs {
		if subs[i].Verdict == Unknown {
			firstUnknown = &subs[i]
			break
		}
	}
	switch tp {
	case "NOT":
		if len(subs) != 1 {
			return unknown(ReasonUnsupported)
		}
		if subs[0].Verdict == Unknown {
			return subs[0]
		}
		return of(subs[0].Verdict == No)
	case "AND":
		for _, s := range subs {
			if s.Verdict == No {
				return no
			}
		}
		if firstUnknown != nil {
			return *firstUnknown
		}
		return yes
	}
	for _, s := range subs { // OR
		if s.Verdict == Yes {
			return yes
		}
	}
	if firstUnknown != nil {
		return *firstUnknown
	}
	return no
}

// resolves reports whether reaching this rule makes Mihomo resolve a domain:
// address rules that do not say no-resolve or src (rules/common/ipcidr.go).
func resolves(r Rule) bool {
	switch r.Type {
	case "IP-CIDR", "IP-CIDR6", "IP-SUFFIX", "IP-ASN", "GEOIP":
		return !hasOption(r.Options, "no-resolve") && !hasOption(r.Options, "src")
	}
	return false
}
