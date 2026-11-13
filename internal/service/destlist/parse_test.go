package destlist

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
)

func entriesOf(t *testing.T, p *Parsed) []string {
	t.Helper()
	if len(p.Entries) == 0 {
		return nil
	}
	return strings.Split(string(p.Entries), "\n")
}

func parseDest(t *testing.T, text string) *Parsed {
	t.Helper()
	p, err := ParseCustom([]byte(text), domain.DestSideDest)
	if err != nil {
		t.Fatalf("ParseCustom(%q): %v", text, err)
	}
	return p
}

// One table per format of P2 #1. Each row is one input line (or a few) and the
// canonical entries that must come out, sorted, once each.
func TestParseCustom_Formats(t *testing.T) {
	for _, c := range []struct {
		name string
		in   string
		want []string
	}{
		// plain domains and the wildcard spellings
		{"plain domain", "example.com", []string{"domain:example.com"}},
		{"uppercase", "Example.COM", []string{"domain:example.com"}},
		{"trailing dot", "example.com.", []string{"domain:example.com"}},
		{"idn", "例子.cn", []string{"domain:xn--fsqu00a.cn"}},
		{"idn uts46 mapping", "Bücher.Example", []string{"domain:xn--bcher-kva.example"}},
		{"idn sharp s is kept", "faß.de", []string{"domain:xn--fa-hia.de"}},
		{"private suffix is not broad", "github.io", []string{"domain:github.io"}},
		{"unlisted single label is not broad", "intranet", []string{"domain:intranet"}},
		{"star dot", "*.example.com", []string{"domain:example.com"}},
		{"leading dot", ".example.com", []string{"domain:example.com"}},
		{"clash plus dot", "+.example.com", []string{"domain:example.com"}},
		{"surrounding space", "   example.com\t", []string{"domain:example.com"}},
		{"crlf", "example.com\r\nexample.org\r\n", []string{"domain:example.com", "domain:example.org"}},
		{"bom", bomChar + "example.com", []string{"domain:example.com"}},
		{"underscore label", "_dmarc.example.com", []string{"domain:_dmarc.example.com"}},

		// explicit prefixes are kept (and normalised)
		{"domain prefix", "domain:Example.com", []string{"domain:example.com"}},
		{"full prefix", "full:WWW.Example.com.", []string{"full:www.example.com"}},
		{"keyword prefix", "keyword:Torrent", []string{"keyword:torrent"}},
		{"keyword hostname chars", "keyword:ab_c-d.e", []string{"keyword:ab_c-d.e"}},
		{"keyword non-ascii is refused", "keyword:例子网站", nil},
		{"keyword with a space is refused", "keyword:ab cd", nil},
		{"regexp prefix", `regexp:^ads?\d+\.example\.com$`, []string{`regexp:^ads?\d+\.example\.com$`}},

		// Clash / Mihomo names (routing-rule-dsl §4.1); the policy name is ignored
		{"clash suffix", "DOMAIN-SUFFIX,example.com,REJECT", []string{"domain:example.com"}},
		{"clash domain", "DOMAIN,www.example.com,DIRECT", []string{"full:www.example.com"}},
		{"clash keyword", "DOMAIN-KEYWORD,tracker,REJECT", []string{"keyword:tracker"}},
		{"clash regex", `DOMAIN-REGEX,^x\d+\.example\.com$,REJECT`, []string{`regexp:^x\d+\.example\.com$`}},
		{"clash ip-cidr", "IP-CIDR,203.0.113.0/24,REJECT,no-resolve", []string{"203.0.113.0/24"}},
		{"clash ip-cidr6", "IP-CIDR6,2001:db8::/32,REJECT", []string{"2001:db8::/32"}},
		{"clash lower case name", "domain-suffix,example.com", []string{"domain:example.com"}},
		// commas belong to the regexp: split on the FIRST comma only, and an
		// optional trailing target only when what remains still compiles
		{"clash regex quantifier", `DOMAIN-REGEX,^a{1,3}\.example\.com$`, []string{`regexp:^a{1,3}\.example\.com$`}},
		{"clash regex quantifier and target", `DOMAIN-REGEX,^a{1,3}\.example\.com$,REJECT`, []string{`regexp:^a{1,3}\.example\.com$`}},
		{"clash regex class with comma", `DOMAIN-REGEX,^[a,b]x\.example$,REJECT`, []string{`regexp:^[a,b]x\.example$`}},
		{"clash regex class with comma and no target", `DOMAIN-REGEX,^[a,b]x\.example$`, []string{`regexp:^[a,b]x\.example$`}},
		{"clash regex two quantifiers", `DOMAIN-REGEX,^a{1,3}b{2,4}\.example$`, []string{`regexp:^a{1,3}b{2,4}\.example$`}},
		{"clash spaces around commas", "DOMAIN-SUFFIX , example.com , REJECT", []string{"domain:example.com"}},

		// hosts files: several names per line, localhost ignored
		{"hosts 0.0.0.0", "0.0.0.0 ads.example.com", []string{"full:ads.example.com"}},
		{"hosts 127.0.0.1 several", "127.0.0.1 a.example.com b.example.com", []string{"full:a.example.com", "full:b.example.com"}},
		{"hosts ipv6", ":: v6.example.com", []string{"full:v6.example.com"}},
		{"hosts inline comment", "0.0.0.0 ads.example.com # tracker", []string{"full:ads.example.com"}},
		{"hosts tab comment", "0.0.0.0\tads.example.com\t# tracker", []string{"full:ads.example.com"}},
		{"hosts several names then comment", "127.0.0.1 a.example.com b.example.com # c.example.com", []string{"full:a.example.com", "full:b.example.com"}},
		{"hosts comment without space is part of the name", "0.0.0.0 ads.example.com#x", nil},
		{"hosts localhost only", "127.0.0.1 localhost", nil},
		{"hosts localhost and a name", "127.0.0.1 localhost real.example.com", []string{"full:real.example.com"}},

		// AdGuard
		{"adguard", "||example.com^", []string{"domain:example.com"}},
		{"adguard no caret", "||example.com", []string{"domain:example.com"}},

		// IP addresses and CIDRs
		{"cidr", "198.51.100.0/24", []string{"198.51.100.0/24"}},
		{"cidr6", "2001:db8::/32", []string{"2001:db8::/32"}},
		{"bare ipv4", "203.0.113.9", []string{"203.0.113.9/32"}},
		{"bare ipv6", "2001:db8::1", []string{"2001:db8::1/128"}},
		{"unaligned cidr is masked", "198.51.100.77/24", []string{"198.51.100.0/24"}},
		{"4in6 is unmapped", "::ffff:203.0.113.9", []string{"203.0.113.9/32"}},

		// references expanded later, same path as a category list
		{"geosite", "GEOSITE,category-ads-all", []string{"geosite:category-ads-all"}},
		{"geosite attr", "GEOSITE,Google@CN", []string{"geosite:google@cn"}},
		{"geosite attrs sorted", "GEOSITE,steam@cn@ads", []string{"geosite:steam@ads@cn"}},
		{"geosite negated attr", "GEOSITE,category-scholar@!cn", []string{"geosite:category-scholar@!cn"}},
		{"xray geosite", "geosite:category-pt", []string{"geosite:category-pt"}},
		{"geoip", "GEOIP,cn", []string{"geoip:CN"}},
		{"geoip no-resolve", "GEOIP,CN,REJECT,no-resolve", []string{"geoip:CN"}},
		{"xray geoip", "geoip:ru", []string{"geoip:RU"}},

		// comments and blanks are not lines
		{"comments", "# a\n! b\n\n   \nexample.com\n", []string{"domain:example.com"}},
		{"adguard header", "[Adblock Plus 2.0]\n||example.com^", []string{"domain:example.com"}},
	} {
		t.Run(c.name, func(t *testing.T) {
			got := entriesOf(t, parseDest(t, c.in))
			if !slices.Equal(got, c.want) {
				t.Fatalf("entries = %q, want %q", got, c.want)
			}
		})
	}
}

func TestParseCustom_DeduplicatesAndSorts(t *testing.T) {
	// Three spellings of one entry, and an order the output must not keep.
	p := parseDest(t, "b.example.com\nExample.COM\nexample.com.\n*.example.com\nDOMAIN-SUFFIX,example.com\na.example.com")
	want := []string{"domain:a.example.com", "domain:b.example.com", "domain:example.com"}
	if got := entriesOf(t, p); !slices.Equal(got, want) {
		t.Fatalf("entries = %q, want %q", got, want)
	}
	if p.EntryCount != 3 {
		t.Fatalf("entry count = %d", p.EntryCount)
	}
	// The digest is of the canonical set, so any order or spelling of the same
	// set has the same one: a remote list that only reshuffles must not
	// advance the generation.
	q := parseDest(t, "a.example.com\nexample.com\nb.example.com")
	if p.SHA256 != q.SHA256 || len(p.SHA256) != 64 {
		t.Fatalf("digests differ for one set: %s vs %s", p.SHA256, q.SHA256)
	}
	r := parseDest(t, "a.example.com\nexample.com")
	if r.SHA256 == p.SHA256 {
		t.Fatal("different sets share a digest")
	}
}

func TestParseCustom_Report(t *testing.T) {
	text := strings.Join([]string{
		"example.com",                    // 1 accepted
		"198.51.100.77/24",               // 2 accepted + rewritten (masked)
		"0.0.0.0 a.example.com",          // 3 accepted
		"127.0.0.1 localhost",            // 4 ignored: localhost
		"||ads.example.com^$third-party", // 5 ignored: modifier
		"@@||ok.example.com^",            // 6 ignored: exception
		"PROCESS-NAME,curl,DIRECT",       // 7 ignored: not applicable on a node
		"DST-PORT,443",                   // 8 ignored: belongs to the policy's inline match
		"MATCH,DIRECT",                   // 9 ignored
		"not a valid line at all",        // 10 ignored: unrecognised
		"example.com",                    // 11 duplicate
		"IP-CIDR,not-a-cidr",             // 12 ignored: bad CIDR
	}, "\n")
	p := parseDest(t, text)
	r := p.Report
	if r.Accepted != 3 || r.Rewritten != 1 || r.Ignored != 8 || r.Duplicates != 1 {
		t.Fatalf("report = %+v", r)
	}
	reasons := map[int]string{}
	for _, s := range r.Samples {
		reasons[s.Line] = s.Reason
	}
	for line, want := range map[int]string{
		2:  ReasonRewrittenToNetwork,
		4:  ReasonLocalhost,
		5:  ReasonAdguardModifier,
		6:  ReasonAdguardException,
		7:  ReasonNotApplicableOnNode,
		8:  ReasonNotApplicableOnNode,
		9:  ReasonNotApplicableOnNode,
		10: ReasonUnrecognized,
		12: ReasonInvalidCIDR,
	} {
		if reasons[line] != want {
			t.Errorf("line %d reason = %q, want %q (samples %+v)", line, reasons[line], want, r.Samples)
		}
	}
}

func TestParseCustom_SamplesAreCapped(t *testing.T) {
	var b strings.Builder
	for i := 0; i < 100; i++ {
		fmt.Fprintf(&b, "PROCESS-NAME,p%d\n", i)
	}
	p := parseDest(t, b.String()+"example.com")
	if p.Report.Ignored != 100 || len(p.Report.Samples) != MaxReportSamples {
		t.Fatalf("ignored %d samples %d, want 100 and %d", p.Report.Ignored, len(p.Report.Samples), MaxReportSamples)
	}
}

// A regexp that does not compile is a hard error carrying the line number, and
// every such line is reported, not just the first.
func TestParseCustom_RegexpCompileErrorsCarryLineNumbers(t *testing.T) {
	_, err := ParseCustom([]byte("example.com\nregexp:(unclosed\nfine.example.org\nDOMAIN-REGEX,[bad,REJECT\n"), domain.DestSideDest)
	var perr *ParseError
	if !errors.As(err, &perr) || !errors.Is(err, domain.ErrValidation) {
		t.Fatalf("err = %v, want a *ParseError wrapping ErrValidation", err)
	}
	var lines []int
	for _, e := range perr.Lines {
		lines = append(lines, e.Line)
		if e.Code != ErrCodeRegexpInvalid {
			t.Errorf("line %d code = %q", e.Line, e.Code)
		}
	}
	if !slices.Equal(lines, []int{2, 4}) {
		t.Fatalf("error lines = %v, want [2 4]", lines)
	}
}

func TestParseCustom_RegexpWarnings(t *testing.T) {
	long := "regexp:^" + strings.Repeat("a", 260) + `\.example\.com$`
	p := parseDest(t, strings.Join([]string{
		`regexp:^ads\.example\.com$`, // anchored: no warning
		`regexp:tracker\.example`,    // unanchored
		`regexp:.*foo.*`,             // unanchored
		`regexp:(^a|b$)\.example`,    // alternation anchored at each side: no warning
		long,                         // long
		`regexp:(?i)^x\.example$`,    // flag group then anchor: no warning
	}, "\n"))
	got := map[string][]int{}
	for _, w := range p.Report.Warnings {
		got[w.Code] = append(got[w.Code], w.Line)
	}
	if !slices.Equal(got[WarnRegexpNoAnchor], []int{2, 3}) {
		t.Errorf("no-anchor warnings on lines %v, want [2 3] (all %+v)", got[WarnRegexpNoAnchor], p.Report.Warnings)
	}
	if !slices.Equal(got[WarnRegexpLong], []int{5}) {
		t.Errorf("long warnings on lines %v, want [5]", got[WarnRegexpLong])
	}
	if p.RegexpCount != 6 {
		t.Errorf("regexp count = %d, want 6", p.RegexpCount)
	}
	// Warnings never stop the save.
	if len(p.Entries) == 0 {
		t.Fatal("a warned list lost its entries")
	}
}

func TestParseCustom_SideChecks(t *testing.T) {
	// Source lines in a destination list are an error, with the line.
	_, err := ParseCustom([]byte("example.com\nSRC-GEOIP,CN\nSRC-IP-CIDR,198.51.100.0/24"), domain.DestSideDest)
	var perr *ParseError
	if !errors.As(err, &perr) || len(perr.Lines) != 2 || perr.Lines[0].Line != 2 || perr.Lines[0].Code != ErrCodeSourceLineInDestList {
		t.Fatalf("source lines in a destination list: %v", err)
	}

	// A source list takes source forms and bare addresses.
	p, err := ParseCustom([]byte("# deny\nSRC-GEOIP,cn\nSRC-IP-CIDR,198.51.100.0/24\n203.0.113.9\n2001:db8::/32"), domain.DestSideSource)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"198.51.100.0/24", "2001:db8::/32", "203.0.113.9/32", "geoip:CN"}
	if got := entriesOf(t, p); !slices.Equal(got, want) {
		t.Fatalf("source entries = %q, want %q", got, want)
	}

	// Destination forms in a source list are an error, not silently dropped:
	// a source list that quietly lost its domains would deny nothing.
	_, err = ParseCustom([]byte("example.com\nGEOIP,CN\nDOMAIN-SUFFIX,x.example\nGEOSITE,cn\n198.51.100.0/24"), domain.DestSideSource)
	if !errors.As(err, &perr) || len(perr.Lines) != 4 {
		t.Fatalf("destination lines in a source list: %v", err)
	}
	for _, e := range perr.Lines {
		if e.Code != ErrCodeDestLineInSourceList {
			t.Errorf("line %d code %q", e.Line, e.Code)
		}
	}
	if _, err := ParseCustom([]byte("x"), "both"); !errors.Is(err, domain.ErrValidation) {
		t.Fatalf("unknown side: %v", err)
	}
}

// Probes: custom lists DROP broad entries and say so; fetched lists FAIL.
func TestParseCustom_BroadEntriesAreDroppedAndReported(t *testing.T) {
	text := strings.Join([]string{
		"example.com",
		`regexp:.*`,        // matches every probe
		`regexp:^[a-z.]+$`, // matches every probe
		"keyword:ab",       // < 4 characters
		"domain:com",       // a public suffix
		"full:co.uk",       // a public suffix
		"0.0.0.0/7",        // IPv4 shorter than /8
		"2000::/15",        // IPv6 shorter than /16
		"keyword:abcd",     // exactly 4: fine
		"10.0.0.0/8",       // exactly /8: fine
		"2000::/16",        // exactly /16: fine
		`regexp:^ads\d+\.example\.com$`,
	}, "\n")
	p := parseDest(t, text)
	want := []string{"10.0.0.0/8", "2000::/16", "domain:example.com", "keyword:abcd", `regexp:^ads\d+\.example\.com$`}
	if got := entriesOf(t, p); !slices.Equal(got, want) {
		t.Fatalf("entries = %q, want %q", got, want)
	}
	reasons := map[int]string{}
	for _, s := range p.Report.Samples {
		reasons[s.Line] = s.Reason
	}
	for line, reason := range map[int]string{
		2: ReasonBroadRegexp, 3: ReasonBroadRegexp, 4: ReasonBroadKeyword, 5: ReasonBroadSuffix, 6: ReasonBroadSuffix,
		7: ReasonBroadCIDR, 8: ReasonBroadCIDR,
	} {
		if reasons[line] != reason {
			t.Errorf("line %d reason = %q, want %q", line, reasons[line], reason)
		}
	}
	if p.Report.Ignored != 7 {
		t.Errorf("ignored = %d, want 7", p.Report.Ignored)
	}
}

func TestParseFetched_BroadEntryFailsTheParse(t *testing.T) {
	_, err := parseLines(splitLines([]byte("example.com\ndomain:com\nkeyword:ab")), parseOptions{side: domain.DestSideDest, dropBroad: false})
	var broad *BroadEntryError
	if !errors.As(err, &broad) || broad.Entry != "domain:com" {
		t.Fatalf("err = %v, want a *BroadEntryError for domain:com", err)
	}
}

func TestParseCustom_SizeLimits(t *testing.T) {
	// Bytes.
	_, err := ParseCustom([]byte(strings.Repeat("a", MaxCustomTextBytes+1)), domain.DestSideDest)
	var big *TooLargeError
	if !errors.As(err, &big) || big.Kind != "bytes" || !errors.Is(err, ErrListTooLarge) {
		t.Fatalf("oversize text: %v", err)
	}
	// Entries.
	var b strings.Builder
	for i := 0; i <= MaxListEntries; i++ {
		fmt.Fprintf(&b, "h%d.example.com\n", i)
	}
	_, err = ParseCustom([]byte(b.String()), domain.DestSideDest)
	if !errors.As(err, &big) || big.Kind != "entries" || big.Limit != MaxListEntries {
		t.Fatalf("too many entries: %v", err)
	}
	// Exactly at the limit is fine.
	b.Reset()
	for i := 0; i < MaxListEntries; i++ {
		fmt.Fprintf(&b, "h%d.example.com\n", i)
	}
	if _, err := ParseCustom([]byte(b.String()), domain.DestSideDest); err != nil {
		t.Fatalf("a list at the limit: %v", err)
	}
	// Regexps.
	b.Reset()
	for i := 0; i <= MaxListRegexps; i++ {
		fmt.Fprintf(&b, "regexp:^a%d\\.example\\.com$\n", i)
	}
	_, err = ParseCustom([]byte(b.String()), domain.DestSideDest)
	if !errors.As(err, &big) || big.Kind != "regexps" || big.Limit != MaxListRegexps {
		t.Fatalf("too many regexps: %v", err)
	}
}

func TestParseCustom_NormalisationOfDomains(t *testing.T) {
	for _, bad := range []string{"a..b.com", "-.com", strings.Repeat("a", 64) + ".com", strings.Repeat("a.", 130) + "com", "exa mple.com", "ex%ample.com"} {
		p := parseDest(t, bad)
		if len(p.Entries) != 0 || p.Report.Ignored != 1 {
			t.Errorf("%q was accepted: %q (report %+v)", bad, p.Entries, p.Report)
		}
	}
}

func TestParseCustom_EmptyInput(t *testing.T) {
	p := parseDest(t, "# nothing here\n\n")
	if p.EntryCount != 0 || len(p.Entries) != 0 {
		t.Fatalf("empty list produced %+v", p)
	}
}

func TestParseCustom_DoesNotShareBackingWithInput(t *testing.T) {
	in := []byte("example.com")
	p := parseDest(t, string(in))
	in[0] = 'X'
	if string(p.Entries) != "domain:example.com" {
		t.Fatalf("entries changed with the input: %q", p.Entries)
	}
}

func TestParseRemote_ClashRuleProviderYAML(t *testing.T) {
	yaml := "payload:\n  - '+.example.com'\n  - '.example.org'\n  - 'full.example.net'\n  - 'DOMAIN-SUFFIX,suffix.example'\n  - \"198.51.100.0/24\"\n"
	p, err := ParseRemote([]byte(yaml), domain.DestSideDest)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"198.51.100.0/24", "domain:example.com", "domain:example.org", "domain:full.example.net", "domain:suffix.example"}
	if got := entriesOf(t, p); !slices.Equal(got, want) {
		t.Fatalf("entries = %q, want %q", got, want)
	}
	// A line is a payload item: errors carry its 1-based position.
	_, err = ParseRemote([]byte("payload:\n  - 'ok.example.com'\n  - 'regexp:(bad'\n"), domain.DestSideDest)
	var perr *ParseError
	if !errors.As(err, &perr) || perr.Lines[0].Line != 2 {
		t.Fatalf("yaml regexp error: %v", err)
	}
	// Malformed YAML is an error, not an empty list.
	if _, err := ParseRemote([]byte("payload: [unclosed"), domain.DestSideDest); !errors.Is(err, domain.ErrValidation) {
		t.Fatalf("malformed yaml: %v", err)
	}
	// A payload that is not a list of strings is refused.
	if _, err := ParseRemote([]byte("payload:\n  - {a: 1}\n"), domain.DestSideDest); !errors.Is(err, domain.ErrValidation) {
		t.Fatalf("non-string payload item: %v", err)
	}
}

func TestParseRemote_PlainTextBehavesLikeCustomButFailsOnBroad(t *testing.T) {
	p, err := ParseRemote([]byte("example.com\n0.0.0.0 ads.example.org\n"), domain.DestSideDest)
	if err != nil {
		t.Fatal(err)
	}
	if got := entriesOf(t, p); !slices.Equal(got, []string{"domain:example.com", "full:ads.example.org"}) {
		t.Fatalf("entries = %q", got)
	}
	_, err = ParseRemote([]byte("example.com\ndomain:co.uk\n"), domain.DestSideDest)
	var broad *BroadEntryError
	if !errors.As(err, &broad) {
		t.Fatalf("a fetched list with a broad entry: %v", err)
	}
}

func TestBroadEntries_FindsThemInStoredEntries(t *testing.T) {
	got := BroadEntries([]byte("domain:example.com\ndomain:com\nregexp:.*\n10.0.0.0/8\n0.0.0.0/4\nkeyword:abc\ngeosite:cn\ngeoip:CN"))
	want := []string{"domain:com", "regexp:.*", "0.0.0.0/4", "keyword:abc"}
	if !slices.Equal(got, want) {
		t.Fatalf("broad = %q, want %q", got, want)
	}
}

// When a trailing field is dropped as a policy name the report says so, as a
// sample (the line is accepted, not ignored); a regexp read whole says nothing.
func TestParseCustom_ReportsADroppedTrailingField(t *testing.T) {
	p := parseDest(t, "DOMAIN-REGEX,^a{1,3}\\.example\\.com$,REJECT\nDOMAIN-REGEX,^b{1,3}\\.example\\.com$")
	var lines []int
	for _, s := range p.Report.Samples {
		if s.Reason == ReasonTrailingFieldDropped {
			lines = append(lines, s.Line)
		}
	}
	if !slices.Equal(lines, []int{1}) || p.Report.Ignored != 0 || p.EntryCount != 2 {
		t.Fatalf("samples %+v ignored %d entries %d", p.Report.Samples, p.Report.Ignored, p.EntryCount)
	}
}
