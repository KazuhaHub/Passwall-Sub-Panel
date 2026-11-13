// Package destlist owns the lists the destination policy matches against:
// custom text the admin pastes, remote https lists, and v2fly geosite
// categories — their parsing and normalisation, their refresh, and the
// expansion of GeoIP countries into address ranges.
//
// A list's canonical form is a set of entry lines, sorted and unique, in the
// spelling the wire policy uses:
//
//	domain:x  full:x  keyword:x  regexp:x    (destination lists)
//	1.2.3.0/24  2001:db8::/32                (destination CIDRs; source CIDRs in a source list)
//	geosite:category[@attr...]  geoip:CC     (references, expanded when the policy is compiled)
//
// Sorted and unique so that the digest of the entries depends only on the SET:
// a remote list that reshuffles or respells its lines must not advance the
// policy generation, because every advance restarts cores (F13).
package destlist

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/netip"
	"regexp"
	"regexp/syntax"
	"slices"
	"strings"
	"unicode/utf8"

	"golang.org/x/net/idna"
	"golang.org/x/net/publicsuffix"
	"gopkg.in/yaml.v3"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
)

// Size limits. MaxListEntries and MaxListRegexps are the policy document's own
// caps (protocol.MaxPolicyDomains and MaxPolicyRegexps) applied to one list: a
// list that alone exceeds them could never be published.
const (
	MaxCustomTextBytes = 4 << 20
	MaxListEntries     = 50000
	MaxListRegexps     = 256
	// MaxRegexpBytes is the wire's bound on one regexp value.
	MaxRegexpBytes = 1024
	// LongRegexpBytes is where a regexp earns the "long" warning.
	LongRegexpBytes = 256
	// MaxReportSamples bounds the report's samples (and its warnings).
	MaxReportSamples = 20
	// maxLineErrors bounds how many failing lines one ParseError lists.
	maxLineErrors = 50
)

// Reasons a line is ignored or rewritten, in the report.
const (
	ReasonRewrittenToNetwork    = "rewritten_to_network"
	ReasonTrailingFieldDropped  = "trailing_field_dropped"
	ReasonLocalhost             = "localhost"
	ReasonAdguardModifier       = "adguard_modifier"
	ReasonAdguardException      = "adguard_exception"
	ReasonAdguardUnsupported    = "adguard_unsupported"
	ReasonNotApplicableOnNode   = "not_applicable_on_node"
	ReasonUnrecognized          = "unrecognized"
	ReasonInvalidDomain         = "invalid_domain"
	ReasonInvalidCIDR           = "invalid_cidr"
	ReasonInvalidCountry        = "invalid_country"
	ReasonInvalidGeositeName    = "invalid_geosite"
	ReasonBroadRegexp           = "broad_regexp"
	ReasonBroadKeyword          = "broad_keyword"
	ReasonBroadSuffix           = "broad_suffix"
	ReasonBroadCIDR             = "broad_cidr"
	ErrCodeRegexpInvalid        = "regexp_invalid"
	ErrCodeRegexpTooLong        = "regexp_too_long"
	ErrCodeSourceLineInDestList = "source_line_in_dest_list"
	ErrCodeDestLineInSourceList = "dest_line_in_source_list"
	WarnRegexpNoAnchor          = "regexp_no_anchor"
	WarnRegexpLong              = "regexp_long"
)

// bomChar is a UTF-8 byte order mark, which editors on some platforms put at
// the start of a pasted file.
const bomChar = "\xef\xbb\xbf"

// Sample is one line the report mentions.
type Sample struct {
	Line   int    `json:"line"`
	Text   string `json:"text"`
	Reason string `json:"reason"`
}

// Warning is a line that was accepted but deserves a look (it never stops the
// save): an unanchored regexp, an unusually long one.
type Warning struct {
	Line int    `json:"line"`
	Text string `json:"text"`
	Code string `json:"code"`
}

// ParseReport is what the editor shows after a parse. Accepted is the number
// of distinct entries kept; Ignored the lines (or hosts) left out, each with a
// reason in Samples (at most MaxReportSamples of them, rewritten lines too).
type ParseReport struct {
	Accepted     int       `json:"accepted"`
	Ignored      int       `json:"ignored"`
	Rewritten    int       `json:"rewritten"`
	Duplicates   int       `json:"duplicates"`
	Samples      []Sample  `json:"samples"`
	Warnings     []Warning `json:"warnings"`
	WarningCount int       `json:"warning_count"`
}

// Parsed is a list's canonical content and its report.
type Parsed struct {
	// Entries are the canonical lines joined by "\n", sorted, without a
	// trailing newline.
	Entries     []byte
	EntryCount  int
	RegexpCount int
	// SHA256 is the digest of Entries.
	SHA256 string
	Report ParseReport
}

// LineError is one line that made the whole parse fail.
type LineError struct {
	Line int    `json:"line"`
	Text string `json:"text"`
	Code string `json:"code"`
	// Detail is the compiler's message for a regexp.
	Detail string `json:"detail,omitempty"`
}

// ParseError is a parse that failed on specific lines: a regexp that does not
// compile, a line on the wrong side of a list. It is a validation error
// (HTTP 400 dest_list_parse_failed) and lists every failing line up to a cap,
// so an admin fixes them in one pass.
type ParseError struct {
	Lines []LineError
}

func (e *ParseError) Error() string {
	first := e.Lines[0]
	return fmt.Sprintf("%v: dest list: line %d: %s", domain.ErrValidation, first.Line, first.Code)
}
func (e *ParseError) Unwrap() error { return domain.ErrValidation }

// ErrListTooLarge classifies a list over a size limit (dest_list_too_large).
var ErrListTooLarge = fmt.Errorf("%w: dest list too large", domain.ErrValidation)

// TooLargeError says which limit a list exceeded.
type TooLargeError struct {
	Kind  string // "bytes", "entries" or "regexps"
	Used  int
	Limit int
}

func (e *TooLargeError) Error() string {
	return fmt.Sprintf("%v: %s %d over the limit %d", ErrListTooLarge, e.Kind, e.Used, e.Limit)
}
func (e *TooLargeError) Unwrap() error { return ErrListTooLarge }

// BroadEntryError is a fetched list that carries an entry broad enough to
// match nearly anything. A custom list drops such an entry and says so; a
// fetched one is refused whole and keeps its previous entries, because nobody
// is looking at its report.
type BroadEntryError struct {
	Entry  string
	Reason string
}

func (e *BroadEntryError) Error() string { return "broad_entry: " + e.Entry }

type parseOptions struct {
	side domain.DestListSide
	// dropBroad: a broad entry is left out and reported (custom). Otherwise it
	// fails the parse (fetched).
	dropBroad bool
}

// ParseCustom parses text an admin pasted. A line that cannot be used is left
// out with a reason; a regexp that does not compile, or a line that belongs on
// the other side, fails the whole parse (ParseError); a list over a limit
// fails it too (TooLargeError).
func ParseCustom(text []byte, side domain.DestListSide) (*Parsed, error) {
	if !side.Valid() {
		return nil, fmt.Errorf("%w: dest list: unknown side %q", domain.ErrValidation, side)
	}
	if len(text) > MaxCustomTextBytes {
		return nil, &TooLargeError{Kind: "bytes", Used: len(text), Limit: MaxCustomTextBytes}
	}
	return parseLines(splitLines(text), parseOptions{side: side, dropBroad: true})
}

// ParseRemote parses a fetched body: a Clash rule-provider YAML (payload:) or
// plain text in any custom-list format. Unlike ParseCustom a broad entry fails
// the parse.
func ParseRemote(body []byte, side domain.DestListSide) (*Parsed, error) {
	if !side.Valid() {
		return nil, fmt.Errorf("%w: dest list: unknown side %q", domain.ErrValidation, side)
	}
	opts := parseOptions{side: side}
	if isClashProviderYAML(body) {
		var doc struct {
			Payload []any `yaml:"payload"`
		}
		if err := yaml.Unmarshal(body, &doc); err != nil {
			return nil, fmt.Errorf("%w: dest list: the rule-provider YAML does not parse: %v", domain.ErrValidation, err)
		}
		lines := make([]string, 0, len(doc.Payload))
		for i, item := range doc.Payload {
			s, ok := item.(string)
			if !ok {
				return nil, fmt.Errorf("%w: dest list: payload item %d is not a string", domain.ErrValidation, i+1)
			}
			lines = append(lines, s)
		}
		return parseLines(lines, opts)
	}
	return parseLines(splitLines(body), opts)
}

// isClashProviderYAML reports whether body opens with a payload: key (after
// comments and blank lines): the rule-provider shape.
func isClashProviderYAML(body []byte) bool {
	for _, line := range strings.Split(string(body), "\n") {
		s := strings.TrimSpace(strings.TrimPrefix(line, bomChar))
		if s == "" || strings.HasPrefix(s, "#") {
			continue
		}
		return strings.HasPrefix(s, "payload:")
	}
	return false
}

func splitLines(b []byte) []string {
	b = bytes.TrimPrefix(b, []byte("\xef\xbb\xbf"))
	if len(b) == 0 {
		return nil
	}
	return strings.Split(string(b), "\n")
}

type parser struct {
	opts     parseOptions
	entries  map[string]struct{}
	regexps  int
	report   ParseReport
	errs     []LineError
	broad    *BroadEntryError
	accepted int // pre-dedupe
}

func parseLines(lines []string, opts parseOptions) (*Parsed, error) {
	p := &parser{opts: opts, entries: map[string]struct{}{}}
	for i, raw := range lines {
		p.line(i+1, raw)
		if p.broad != nil {
			return nil, p.broad
		}
	}
	if len(p.errs) > 0 {
		return nil, &ParseError{Lines: p.errs}
	}
	if p.regexps > MaxListRegexps {
		return nil, &TooLargeError{Kind: "regexps", Used: p.regexps, Limit: MaxListRegexps}
	}
	if len(p.entries) > MaxListEntries {
		return nil, &TooLargeError{Kind: "entries", Used: len(p.entries), Limit: MaxListEntries}
	}
	out := make([]string, 0, len(p.entries))
	for e := range p.entries {
		out = append(out, e)
	}
	slices.Sort(out)
	body := []byte(strings.Join(out, "\n"))
	sum := sha256.Sum256(body)
	p.report.Accepted = len(out)
	p.report.Duplicates = p.accepted - len(out)
	return &Parsed{
		Entries: body, EntryCount: len(out), RegexpCount: p.regexps,
		SHA256: hex.EncodeToString(sum[:]), Report: p.report,
	}, nil
}

func (p *parser) ignore(n int, text, reason string) {
	p.report.Ignored++
	if len(p.report.Samples) < MaxReportSamples {
		p.report.Samples = append(p.report.Samples, Sample{Line: n, Text: clip(text), Reason: reason})
	}
}

// note adds a sample for a line that was accepted but not read whole, which is
// not an ignored line: the admin can see what was dropped from it.
func (p *parser) note(n int, text, reason string) {
	if len(p.report.Samples) < MaxReportSamples {
		p.report.Samples = append(p.report.Samples, Sample{Line: n, Text: clip(text), Reason: reason})
	}
}

func (p *parser) lineError(n int, text, code, detail string) {
	if len(p.errs) < maxLineErrors {
		p.errs = append(p.errs, LineError{Line: n, Text: clip(text), Code: code, Detail: detail})
	}
}

func (p *parser) warn(n int, text, code string) {
	p.report.WarningCount++
	if len(p.report.Warnings) < MaxReportSamples {
		p.report.Warnings = append(p.report.Warnings, Warning{Line: n, Text: clip(text), Code: code})
	}
}

func clip(s string) string {
	if len(s) <= 120 {
		return s
	}
	cut := s[:120]
	for !utf8.ValidString(cut) {
		cut = cut[:len(cut)-1]
	}
	return cut + "…"
}

// kind is what a recognised line asks for, before it is checked against the
// list's side.
type kind int

const (
	kDomain kind = iota // domain:/full:/keyword:/regexp: and the references to destinations
	kDestCIDR
	kDestGeoIP
	kDestGeosite
	kSrcCIDR
	kSrcGeoIP
	kBareCIDR // an address with no side of its own: whichever list it is in
)

// add checks a recognised entry against the list's side and the probes and
// keeps it. It reports whether the entry was new: a repeat is counted as a
// duplicate, not accepted twice.
func (p *parser) add(n int, text string, k kind, entry string) bool {
	source := p.opts.side == domain.DestSideSource
	switch {
	case !source && (k == kSrcCIDR || k == kSrcGeoIP):
		p.lineError(n, text, ErrCodeSourceLineInDestList, "")
		return false
	case source && k != kSrcCIDR && k != kSrcGeoIP && k != kBareCIDR:
		p.lineError(n, text, ErrCodeDestLineInSourceList, "")
		return false
	}
	if reason := broadReason(entry); reason != "" {
		if !p.opts.dropBroad {
			p.broad = &BroadEntryError{Entry: entry, Reason: reason}
			return false
		}
		p.ignore(n, text, reason)
		return false
	}
	p.accepted++
	if _, seen := p.entries[entry]; seen {
		return false
	}
	p.entries[entry] = struct{}{}
	if strings.HasPrefix(entry, "regexp:") {
		p.regexps++
	}
	return true
}

// clashNames maps the Mihomo rule names this parser understands.
var clashKinds = map[string]kind{
	"DOMAIN": kDomain, "DOMAIN-SUFFIX": kDomain, "DOMAIN-KEYWORD": kDomain, "DOMAIN-REGEX": kDomain,
	"IP-CIDR": kDestCIDR, "IP-CIDR6": kDestCIDR, "SRC-IP-CIDR": kSrcCIDR,
	"GEOSITE": kDestGeosite, "GEOIP": kDestGeoIP, "SRC-GEOIP": kSrcGeoIP,
}

// clashNotApplicable are rule names that are real Clash/Mihomo syntax with no
// meaning on a node: they describe the client (process, uid), the listener, a
// logical combination, or fields the policy carries as its own inline match
// (ports, network). A line that uses one is left out, with this reason, rather
// than refused: lists are routinely copied out of client configs.
var clashNotApplicable = map[string]bool{
	"DST-PORT": true, "SRC-PORT": true, "IN-PORT": true, "IN-NAME": true, "IN-USER": true, "IN-TYPE": true,
	"PROCESS-NAME": true, "PROCESS-PATH": true, "PROCESS-PATH-REGEX": true, "PROCESS-NAME-REGEX": true,
	"PROCESS-PATH-WILDCARD": true, "PROCESS-NAME-WILDCARD": true, "UID": true, "NETWORK": true, "DSCP": true,
	"RULE-SET": true, "SUB-RULE": true, "AND": true, "OR": true, "NOT": true, "MATCH": true,
	"DOMAIN-WILDCARD": true, "IP-ASN": true, "SRC-IP-ASN": true, "IP-SUFFIX": true, "SRC-IP-SUFFIX": true,
}

var adguardHeader = regexp.MustCompile(`^\[.*\]$`)

func (p *parser) line(n int, raw string) {
	s := strings.TrimSpace(strings.TrimPrefix(raw, bomChar))
	if s == "" || strings.HasPrefix(s, "#") || strings.HasPrefix(s, "!") || strings.HasPrefix(s, "//") || adguardHeader.MatchString(s) {
		return
	}
	lower := strings.ToLower(s)
	isRegexpLine := strings.HasPrefix(lower, "regexp:") || strings.HasPrefix(lower, "domain-regex")
	if !isRegexpLine {
		// An inline comment: whitespace then '#'. Not for a regexp, which may
		// legitimately contain one.
		for _, sep := range []string{" #", "\t#"} {
			if i := strings.Index(s, sep); i >= 0 {
				s = strings.TrimSpace(s[:i])
			}
		}
	}

	// Explicit prefixes (Xray / v2fly spelling).
	if i := strings.IndexByte(s, ':'); i > 0 && isWord(s[:i]) {
		if p.prefixed(n, s, strings.ToLower(s[:i]), s[i+1:]) {
			return
		}
	}
	// Clash / Mihomo rules.
	if i := strings.IndexByte(s, ','); i > 0 {
		name := strings.ToUpper(strings.TrimSpace(s[:i]))
		if k, ok := clashKinds[name]; ok {
			p.clash(n, s, name, k)
			return
		}
		if clashNotApplicable[name] {
			p.ignore(n, s, ReasonNotApplicableOnNode)
			return
		}
	}
	// AdGuard.
	if strings.HasPrefix(s, "||") || strings.HasPrefix(s, "@@") || strings.HasPrefix(s, "|") {
		p.adguard(n, s)
		return
	}
	fields := strings.Fields(s)
	// hosts files: a loopback or unspecified address, then names.
	if len(fields) >= 2 {
		if addr, err := netip.ParseAddr(fields[0]); err == nil && (addr.IsLoopback() || addr.IsUnspecified()) {
			p.hosts(n, s, fields[1:])
			return
		}
		p.ignore(n, s, ReasonUnrecognized)
		return
	}
	// One token: an address, a CIDR, or a domain.
	token := fields[0]
	if strings.ContainsAny(token, "/") || isAddr(token) {
		p.cidr(n, s, token, kBareCIDR)
		return
	}
	p.plainDomain(n, s, token, "domain:")
}

func isWord(s string) bool {
	if s == "" {
		return false
	}
	for _, c := range s {
		if (c < 'a' || c > 'z') && (c < 'A' || c > 'Z') {
			return false
		}
	}
	return true
}

func isAddr(s string) bool {
	_, err := netip.ParseAddr(s)
	return err == nil
}

// prefixed handles name:value. It returns false when name is not a prefix this
// parser knows, so the line is tried as something else (an IPv6 address has
// colons too, but its head is not a word).
func (p *parser) prefixed(n int, line, name, value string) bool {
	value = strings.TrimSpace(value)
	switch name {
	case "domain":
		p.plainDomain(n, line, value, "domain:")
	case "full":
		p.plainDomain(n, line, value, "full:")
	case "keyword":
		p.keyword(n, line, value)
	case "regexp":
		p.regexp(n, line, value)
	case "geosite":
		p.geosite(n, line, value)
	case "geoip":
		p.country(n, line, value, kDestGeoIP)
	case "ext", "ext-ip", "ext-domain", "include", "dotless", "keyword-regexp":
		p.ignore(n, line, ReasonNotApplicableOnNode)
	default:
		return false
	}
	return true
}

func (p *parser) clash(n int, line, name string, k kind) {
	parts := strings.Split(line, ",")
	if len(parts) < 2 {
		p.ignore(n, line, ReasonUnrecognized)
		return
	}
	value := strings.TrimSpace(parts[1])
	if name == "DOMAIN-REGEX" {
		var dropped bool
		value, dropped = clashRegexValue(line)
		if dropped {
			p.note(n, line, ReasonTrailingFieldDropped)
		}
	}
	switch name {
	case "DOMAIN":
		p.plainDomain(n, line, value, "full:")
	case "DOMAIN-SUFFIX":
		p.plainDomain(n, line, value, "domain:")
	case "DOMAIN-KEYWORD":
		p.keyword(n, line, value)
	case "DOMAIN-REGEX":
		p.regexp(n, line, value)
	case "IP-CIDR", "IP-CIDR6", "SRC-IP-CIDR":
		p.cidr(n, line, value, k)
	case "GEOSITE":
		p.geosite(n, line, value)
	case "GEOIP", "SRC-GEOIP":
		p.country(n, line, value, k)
	}
}

// clashRegexValue extracts the regexp of a DOMAIN-REGEX line. Clash puts it in
// the second field and an optional policy name in the third, but a regexp may
// itself contain commas ({1,3}, [a,b]), so the line is NOT split on every
// comma: only the first comma is structural. A trailing field is dropped as the
// policy name only when it looks like a name (no regexp metacharacters) AND
// what remains still compiles; otherwise the whole remainder is the regexp.
func clashRegexValue(line string) (value string, dropped bool) {
	rest := strings.TrimSpace(line[strings.IndexByte(line, ',')+1:])
	if i := strings.LastIndexByte(rest, ','); i >= 0 {
		head, tail := strings.TrimSpace(rest[:i]), strings.TrimSpace(rest[i+1:])
		if head != "" && !strings.ContainsAny(tail, `\^$*+?{}[]()|`) {
			if _, err := regexp.Compile(head); err == nil {
				return head, true
			}
		}
	}
	return rest, false
}

func (p *parser) adguard(n int, line string) {
	switch {
	case strings.HasPrefix(line, "@@"):
		p.ignore(n, line, ReasonAdguardException)
		return
	case !strings.HasPrefix(line, "||"):
		p.ignore(n, line, ReasonAdguardUnsupported)
		return
	case strings.Contains(line, "$"):
		p.ignore(n, line, ReasonAdguardModifier)
		return
	}
	host := strings.TrimSuffix(strings.TrimPrefix(line, "||"), "^")
	if strings.ContainsAny(host, "/^*|?") {
		p.ignore(n, line, ReasonAdguardUnsupported)
		return
	}
	p.plainDomain(n, line, host, "domain:")
}

// localhostNames are the names a hosts file lists for the machine itself.
var localhostNames = map[string]bool{
	"localhost": true, "localhost.localdomain": true, "local": true, "broadcasthost": true,
	"ip6-localhost": true, "ip6-loopback": true, "ip6-localnet": true, "ip6-mcastprefix": true,
	"ip6-allnodes": true, "ip6-allrouters": true, "ip6-allhosts": true, "0.0.0.0": true,
}

func (p *parser) hosts(n int, line string, names []string) {
	for _, name := range names {
		if localhostNames[strings.ToLower(name)] {
			p.ignore(n, line, ReasonLocalhost)
			continue
		}
		p.plainDomain(n, line, name, "full:")
	}
}

func (p *parser) plainDomain(n int, line, value, prefix string) {
	value = strings.TrimSpace(value)
	if prefix == "domain:" {
		// The wildcard spellings of "this domain and its subdomains".
		for _, lead := range []string{"+.", "*.", "."} {
			if strings.HasPrefix(value, lead) {
				value = strings.TrimPrefix(value, lead)
				break
			}
		}
	}
	if localhostNames[strings.ToLower(value)] {
		p.ignore(n, line, ReasonLocalhost)
		return
	}
	norm, ok := NormalizeDomain(value)
	if !ok {
		p.ignore(n, line, ReasonInvalidDomain)
		return
	}
	p.add(n, line, kDomain, prefix+norm)
}

// NormalizeDomain lower-cases a host, drops one trailing dot, converts IDN
// labels to punycode and checks the result is a hostname the wire accepts:
// letters, digits, hyphen and underscore, labels of 1..63 bytes that do not
// start or end with a hyphen, at most 253 bytes in all.
func NormalizeDomain(s string) (string, bool) {
	s = strings.ToLower(strings.TrimSpace(s))
	s = strings.TrimSuffix(s, ".")
	if s == "" {
		return "", false
	}
	for _, r := range s {
		if r >= utf8.RuneSelf {
			ascii, err := idna.Lookup.ToASCII(s)
			if err != nil {
				return "", false
			}
			s = ascii
			break
		}
	}
	if len(s) > 253 {
		return "", false
	}
	for _, label := range strings.Split(s, ".") {
		if label == "" || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return "", false
		}
		for i := 0; i < len(label); i++ {
			c := label[i]
			if (c < 'a' || c > 'z') && (c < '0' || c > '9') && c != '-' && c != '_' {
				return "", false
			}
		}
	}
	return s, true
}

func (p *parser) keyword(n int, line, value string) {
	value = strings.ToLower(strings.TrimSpace(value))
	if !validKeyword(value) {
		p.ignore(n, line, ReasonInvalidDomain)
		return
	}
	p.add(n, line, kDomain, "keyword:"+value)
}

// validKeyword: a keyword is matched as a substring of the hostname the node
// sees, which is ASCII (IDN hosts arrive as punycode), so it is held to
// hostname characters. A non-ASCII keyword could never match and is refused
// rather than silently accepted: converting a fragment of a word to punycode
// would not mean what it says.
func validKeyword(v string) bool {
	if v == "" || len(v) > 253 {
		return false
	}
	for i := 0; i < len(v); i++ {
		c := v[i]
		if (c < 'a' || c > 'z') && (c < '0' || c > '9') && c != '-' && c != '_' && c != '.' {
			return false
		}
	}
	return true
}

// regexpProbes are fixed hostnames of assorted shapes. A regexp that matches
// ALL of them matches nearly every hostname, which on a block list is an
// outage and on an allow list a bypass of every rule. Letters and dots only,
// so a class like [a-z.] cannot slip past by the probes carrying digits.
var regexpProbes = []string{
	"qzxkvj.com", "mail.example.org", "www.wikipedia.org", "cdn.fplqmw.io",
	"abc.def.co.uk", "yvnhbtre.net", "static.rwkxpd.dev", "kjhgfd.example.cn",
}

func (p *parser) regexp(n int, line, value string) {
	value = strings.TrimSpace(value)
	if value == "" {
		p.ignore(n, line, ReasonUnrecognized)
		return
	}
	if len(value) > MaxRegexpBytes {
		p.lineError(n, line, ErrCodeRegexpTooLong, fmt.Sprintf("%d bytes, over %d", len(value), MaxRegexpBytes))
		return
	}
	// Go's regexp (RE2) is what both Xray and sing-box compile with, so a
	// pattern that compiles here compiles on the node.
	if _, err := regexp.Compile(value); err != nil {
		p.lineError(n, line, ErrCodeRegexpInvalid, err.Error())
		return
	}
	if !p.add(n, line, kDomain, "regexp:"+value) {
		return
	}
	if !anchored(value) {
		p.warn(n, line, WarnRegexpNoAnchor)
	}
	if len(value) > LongRegexpBytes {
		p.warn(n, line, WarnRegexpLong)
	}
}

// anchored reports whether every way the pattern can match is pinned to the
// start or the end of the host. An unanchored regexp is matched anywhere in
// the host, which is rarely what a rule for "ads.example.com" means.
func anchored(pattern string) bool {
	re, err := syntax.Parse(pattern, syntax.Perl)
	if err != nil {
		return false
	}
	return hasAnchor(re)
}

func hasAnchor(re *syntax.Regexp) bool {
	switch re.Op {
	case syntax.OpBeginText, syntax.OpEndText, syntax.OpBeginLine, syntax.OpEndLine:
		return true
	case syntax.OpConcat:
		return len(re.Sub) > 0 && (hasAnchor(re.Sub[0]) || hasAnchor(re.Sub[len(re.Sub)-1]))
	case syntax.OpCapture:
		return len(re.Sub) > 0 && hasAnchor(re.Sub[0])
	case syntax.OpAlternate:
		for _, s := range re.Sub {
			if !hasAnchor(s) {
				return false
			}
		}
		return len(re.Sub) > 0
	}
	return false
}

func (p *parser) cidr(n int, line, value string, k kind) {
	value = strings.TrimSpace(value)
	var prefix netip.Prefix
	if strings.Contains(value, "/") {
		pf, err := netip.ParsePrefix(value)
		if err != nil {
			p.ignore(n, line, ReasonInvalidCIDR)
			return
		}
		prefix = pf
	} else {
		addr, err := netip.ParseAddr(value)
		if err != nil {
			p.ignore(n, line, ReasonInvalidCIDR)
			return
		}
		prefix = netip.PrefixFrom(addr, addr.BitLen())
	}
	if prefix.Addr().Zone() != "" {
		p.ignore(n, line, ReasonInvalidCIDR)
		return
	}
	// ::ffff:a.b.c.d/n is the IPv4 prefix a.b.c.d/(n-96).
	if prefix.Addr().Is4In6() && prefix.Bits() >= 96 {
		prefix = netip.PrefixFrom(prefix.Addr().Unmap(), prefix.Bits()-96)
	}
	if masked := prefix.Masked(); masked != prefix {
		prefix = masked
		p.report.Rewritten++
		if len(p.report.Samples) < MaxReportSamples {
			p.report.Samples = append(p.report.Samples, Sample{Line: n, Text: clip(line), Reason: ReasonRewrittenToNetwork})
		}
	}
	p.add(n, line, k, prefix.String())
}

func (p *parser) geosite(n int, line, value string) {
	name, attrs, ok := splitGeosite(value)
	if !ok {
		p.ignore(n, line, ReasonInvalidGeositeName)
		return
	}
	p.add(n, line, kDestGeosite, FormatGeositeRef(name, attrs))
}

// splitGeosite splits category@attr@attr into a lower-case category and its
// attribute names, which are kept literally ("cn" and "!cn" are two different
// attributes, F49).
func splitGeosite(value string) (name string, attrs []string, ok bool) {
	parts := strings.Split(strings.TrimSpace(value), "@")
	name = strings.ToLower(strings.TrimSpace(parts[0]))
	if name == "" || strings.ContainsAny(name, " \t,:") {
		return "", nil, false
	}
	for _, a := range parts[1:] {
		a = strings.ToLower(strings.TrimSpace(a))
		if a == "" || strings.ContainsAny(a, " \t,:@") {
			return "", nil, false
		}
		attrs = append(attrs, a)
	}
	return name, attrs, true
}

// FormatGeositeRef spells a geosite reference canonically: attributes sorted
// and unique.
func FormatGeositeRef(category string, attrs []string) string {
	attrs = slices.Compact(slices.Sorted(slices.Values(attrs)))
	out := "geosite:" + category
	for _, a := range attrs {
		out += "@" + a
	}
	return out
}

func (p *parser) country(n int, line, value string, k kind) {
	cc := strings.ToUpper(strings.TrimSpace(value))
	if _, ok := domain.NormalizeDestCountries([]string{cc}); !ok {
		p.ignore(n, line, ReasonInvalidCountry)
		return
	}
	p.add(n, line, k, "geoip:"+cc)
}

// ---- probes ----

// broadReason says why an entry is too broad to keep, "" when it is not. The
// probes are the same for every list: a regexp matching all of a fixed set of
// hostnames, a keyword under four characters, a domain that IS a public
// suffix, an IPv4 prefix shorter than /8 or an IPv6 one shorter than /16.
func broadReason(entry string) string { return broadReasonFor(entry, false) }

// broadReasonFor is broadReason with the public-suffix probe optional. A geosite
// category is curated upstream data, and its entries include suffixes ON
// PURPOSE: the brand TLD of a bank (domain:hsbc), the adult TLD, the registry
// edu.cn that category-education-cn exists to cover. Measured on release
// 20261112220449, the probe fires on 1893 entries in 137 of 1556 categories —
// and on seven of the catalog's own templates — and on nothing else: the
// regexp, keyword and CIDR probes find none. Failing every such refresh would
// make the catalog unusable, so geosite lists skip this one probe; an
// admin-typed or fetched list keeps it, where "domain:com" is a typo.
func broadReasonFor(entry string, trustSuffix bool) string {
	switch {
	case strings.HasPrefix(entry, "regexp:"):
		re, err := regexp.Compile(strings.TrimPrefix(entry, "regexp:"))
		if err != nil {
			return ""
		}
		for _, probe := range regexpProbes {
			if !re.MatchString(probe) {
				return ""
			}
		}
		return ReasonBroadRegexp
	case strings.HasPrefix(entry, "keyword:"):
		if len(strings.TrimPrefix(entry, "keyword:")) < 4 {
			return ReasonBroadKeyword
		}
	case strings.HasPrefix(entry, "domain:"), strings.HasPrefix(entry, "full:"):
		name := entry[strings.IndexByte(entry, ':')+1:]
		if suffix, icann := publicsuffix.PublicSuffix(name); suffix == name && icann && !trustSuffix {
			return ReasonBroadSuffix
		}
	case strings.HasPrefix(entry, "geosite:"), strings.HasPrefix(entry, "geoip:"):
	default:
		if pf, err := netip.ParsePrefix(entry); err == nil {
			if (pf.Addr().Is4() && pf.Bits() < 8) || (pf.Addr().Is6() && pf.Bits() < 16) {
				return ReasonBroadCIDR
			}
		}
	}
	return ""
}

// BroadEntries lists the entries of a stored list that the probes call broad,
// in stored order. A list's own parse never stores one; this is for a policy
// that references a list written before a probe existed, or by a path that
// did not probe.
func BroadEntries(entries []byte) []string { return broadEntries(entries, false) }

// BroadEntriesOf is BroadEntries for a stored list of a known kind: a geosite
// list is judged without the public-suffix probe (see broadReasonFor).
func BroadEntriesOf(l *domain.DestList) []string {
	return broadEntries(l.Entries, l.Kind == domain.DestListGeosite)
}

func broadEntries(entries []byte, trustSuffix bool) []string {
	var out []string
	for _, e := range strings.Split(string(entries), "\n") {
		if e != "" && broadReasonFor(e, trustSuffix) != "" {
			out = append(out, e)
		}
	}
	return out
}
