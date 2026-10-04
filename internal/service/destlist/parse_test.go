package destlist

import (
	"bytes"
	"fmt"
	"strings"
	"testing"
)

func TestParseClashRegexpCSVNeverTruncatesAtPatternComma(t *testing.T) {
	parsed, err := ParseCustom([]byte("DOMAIN-REGEX,\"^x{1,2}[.]example[.]com$\",Proxy\nDOMAIN-REGEX,^x{1,2}[.]example[.]com$,Proxy\nregexp:^a{1,2}[.]example[.]com$\n"))
	want := "regexp:^a{1,2}[.]example[.]com$\nregexp:^x{1,2}[.]example[.]com$\n"
	if err != nil || string(parsed.Entries) != want || parsed.Report.Ignored != 1 || len(parsed.Report.Samples) == 0 {
		t.Fatalf("CSV pattern truncated or ambiguous record accepted: %+v / %v", parsed, err)
	}
}

func TestParseCustomFormats(t *testing.T) {
	for _, tt := range []struct{ input, want string }{
		{"Example.COM.", "domain:example.com\n"},
		{"domain:EXAMPLE.com", "domain:example.com\n"},
		{"full:Example.COM", "full:example.com\n"},
		{"*.example.com\n.example.com", "domain:example.com\n"},
		{"例子.cn\nxn--fsqu00a.cn", "domain:xn--fsqu00a.cn\n"},
		{"xn--wcvs22d1m.hk", "domain:xn--wcvs22d1m.hk\n"},
		{"DOMAIN-SUFFIX,Example.com,Proxy", "domain:example.com\n"},
		{"DOMAIN,Example.com,Proxy", "full:example.com\n"},
		{"DOMAIN-KEYWORD,Example,Proxy", "keyword:example\n"},
		{"DOMAIN-REGEX,^EXAMPLE[.]com$,Proxy", "regexp:^EXAMPLE[.]com$\n"},
		{"IP-CIDR,1.2.3.4/24,no-resolve", "1.2.3.0/24\n"},
		{"IP-CIDR6,2001:db8::1/32,Proxy", "2001:db8::/32\n"},
		{"0.0.0.0 Example.com Example.org localhost\n127.0.0.1 x.example.com # comment", "domain:example.com\ndomain:example.org\ndomain:x.example.com\n"},
		{"||Example.com^", "domain:example.com\n"},
		{"1.2.3.4\n2001:db8::1", "1.2.3.4/32\n2001:db8::1/128\n"},
		{"keyword:EXAMPLE\nregexp:^(A|a)[.]example[.]com$", "keyword:example\nregexp:^(A|a)[.]example[.]com$\n"},
	} {
		t.Run(tt.input, func(t *testing.T) {
			parsed, err := ParseCustom([]byte(tt.input))
			if err != nil || string(parsed.Entries) != tt.want {
				t.Fatalf("entries=%q want=%q error=%v", parsed.Entries, tt.want, err)
			}
			if parsed.EntryCount != strings.Count(tt.want, "\n") || parsed.Report.Accepted != parsed.EntryCount {
				t.Fatalf("report count mismatch: %+v", parsed)
			}
		})
	}
}

func TestParseDigestDependsOnlyOnNormalizedUniqueContent(t *testing.T) {
	a, err := ParseCustom([]byte("# heading\nExample.COM.\nfull:x.example.com\nexample.com\n"))
	if err != nil {
		t.Fatal(err)
	}
	b, err := ParseCustom([]byte("full:x.example.com\ndomain:example.com\n# changed comment\n"))
	if err != nil || a.ContentSHA256 == "" || a.ContentSHA256 != b.ContentSHA256 || !bytes.Equal(a.Entries, b.Entries) {
		t.Fatalf("comments/order/duplicate affected digest: %+v / %+v / %v", a, b, err)
	}
}

func TestCustomBroadAndUnsupportedLinesAreReportedWithOriginalLineNumbers(t *testing.T) {
	input := "# heading\nexample.com\nregexp:.*\nkeyword:abc\ndomain:co.uk\nfull:com\n0.0.0.0/0\n::/0\n||example.org^$third-party\n@@||example.net^\nhttps://example.org/path\nregexp:[\n"
	parsed, err := ParseCustom([]byte(input))
	if err != nil || string(parsed.Entries) != "domain:example.com\n" || parsed.Report.Ignored < 10 || parsed.Report.Rewritten == 0 {
		t.Fatalf("dangerous entries accepted or missing report: %+v / %v", parsed, err)
	}
	found := false
	for _, sample := range parsed.Report.Samples {
		if sample.Line == 3 && sample.Reason == "broad_entry" {
			found = true
		}
	}
	if !found {
		t.Fatalf("broad expression lacks original line report: %+v", parsed.Report)
	}
}

func TestRemoteBroadEntriesFailTheWholeRefresh(t *testing.T) {
	for _, entry := range []string{"regexp:.*", "keyword:abc", "domain:co.uk", "0.0.0.0/0", "::/0"} {
		parsed, err := ParseRemote([]byte("example.com\n" + entry))
		if err == nil || !strings.Contains(err.Error(), "broad_entry") || len(parsed.Entries) != 0 {
			t.Fatalf("partial list returned after broad entry %q: %+v / %v", entry, parsed, err)
		}
	}
}

func TestRemoteClashPayloadKeepsLineNumbersAndDoesNotAcceptUnstructuredYAML(t *testing.T) {
	metadata, metaErr := ParseRemote([]byte("behavior: domain\npayload:\n  - 'Example.com'\n"))
	if metaErr != nil || string(metadata.Entries) != "domain:example.com\n" {
		t.Fatalf("payload after metadata ignored: %+v / %v", metadata, metaErr)
	}
	parsed, err := ParseRemote([]byte("# rules\npayload:\n  - '+.Example.com'\n  - '.example.org'\n  - 'DOMAIN,x.example.net,DIRECT'\n  - 'IP-CIDR,1.2.3.4/24'\n"))
	if err != nil || string(parsed.Entries) != "1.2.3.0/24\ndomain:example.com\ndomain:example.org\nfull:x.example.net\n" {
		t.Fatalf("payload not parsed: %+v / %v", parsed, err)
	}
	parsed, err = ParseRemote([]byte("payload:\n  - 'regexp:.*'\n"))
	if err == nil || len(parsed.Entries) != 0 {
		t.Fatalf("YAML bypassed broad check: %+v / %v", parsed, err)
	}
	for _, invalid := range []string{"payload: [", "payload: {host: example.com}", "payload: [123]", "payload: []\n---\npayload: [example.com]"} {
		if _, err := ParseRemote([]byte(invalid)); err == nil {
			t.Fatalf("invalid payload accepted: %s", invalid)
		}
	}
}

func TestParseRejectsOversizeWithoutReturningTruncatedEntries(t *testing.T) {
	if p, err := ParseCustom(bytes.Repeat([]byte("#"), MaxCustomBytes+1)); err == nil || len(p.Entries) != 0 {
		t.Fatalf("oversized text accepted: %v", err)
	}
	if p, err := ParseRemote(bytes.Repeat([]byte("#"), MaxRemoteBytes+1)); err == nil || len(p.Entries) != 0 {
		t.Fatalf("oversized remote text accepted: %v", err)
	}
	var text strings.Builder
	for i := 0; i <= MaxEntries; i++ {
		fmt.Fprintf(&text, "x%d.example.com\n", i)
	}
	if p, err := ParseCustom([]byte(text.String())); err == nil || len(p.Entries) != 0 {
		t.Fatalf("oversized entry count accepted: %d / %v", p.EntryCount, err)
	}
	if _, err := ParseCustom([]byte{0xff, 0xfe}); err == nil {
		t.Fatal("invalid UTF-8 accepted")
	}
}

func TestParseHandlesLargeLinesAndBoundsReportSamples(t *testing.T) {
	input := "#" + strings.Repeat("x", 70000) + "\nexample.com\n" + strings.Repeat("||x.example.com^$third-party\n", 40)
	p, err := ParseCustom([]byte(input))
	if err != nil || string(p.Entries) != "domain:example.com\n" || len(p.Report.Samples) != MaxSamples {
		t.Fatalf("large line truncated parse or unbounded report: %+v / %v", p, err)
	}
	for _, sample := range p.Report.Samples {
		if len(sample.Text) > 512 {
			t.Fatalf("unbounded sample: %d", len(sample.Text))
		}
	}
}
