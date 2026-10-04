package destlist

import (
	"compress/gzip"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"slices"
	"strings"
	"testing"
)

const geoFixture = `lists:
  - name: "finance"
    length: 4
    rules:
      - "domain:example.com:@cn,@ads"
      - "full:other.example.com:@!cn"
      - "regexp:^x[.]example[.]com$:@cn"
      - "domain:legacy.example.com:@cn:@ads"
  - name: "wide"
    length: 1
    rules:
      - "domain:com"
`

func geoChecksum(body string) []byte {
	return []byte(fmt.Sprintf("%x  dlc.dat_plain.yml\n", sha256.Sum256([]byte(body))))
}

func TestGeositeChecksumIsVerifiedBeforeYAML(t *testing.T) {
	if c, err := ParseGeosite([]byte(geoFixture), []byte(strings.Repeat("0", 64)+"  dlc.dat_plain.yml\n")); err == nil || c != nil {
		t.Fatalf("checksum mismatch accepted: %+v / %v", c, err)
	}
	for _, invalid := range []string{"", "abc", strings.Repeat("0", 64) + "  another-file.yml\n", strings.Repeat("0", 64) + "  dlc.dat_plain.yml\nextra"} {
		if c, err := ParseGeosite([]byte(geoFixture), []byte(invalid)); err == nil || c != nil {
			t.Fatalf("malformed checksum accepted: %q / %v", invalid, err)
		}
	}
}

func TestGeositeListsCategoriesAndLiteralAttributes(t *testing.T) {
	c, err := ParseGeosite([]byte(geoFixture), geoChecksum(geoFixture))
	if err != nil {
		t.Fatal(err)
	}
	categories := c.Categories()
	if len(categories) != 2 || categories[0].Name != "finance" || categories[0].Count != 4 || categories[0].RegexpCount != 1 || !slices.Equal(categories[0].Attrs, []string{"!cn", "ads", "cn"}) {
		t.Fatalf("bad category metadata: %+v", categories)
	}
	for _, tt := range []struct {
		attrs []string
		want  string
	}{
		{nil, "domain:example.com\ndomain:legacy.example.com\nfull:other.example.com\nregexp:^x[.]example[.]com$\n"},
		{[]string{"cn", "ads"}, "domain:example.com\ndomain:legacy.example.com\n"},
		{[]string{"!cn"}, "full:other.example.com\n"},
	} {
		p, err := c.Select("finance", tt.attrs)
		if err != nil || string(p.Entries) != tt.want || strings.Contains(string(p.Entries), "@") {
			t.Fatalf("attributes not stripped/matched: %+v / %v", p, err)
		}
	}
	if _, err := c.Select("missing", nil); err == nil {
		t.Fatal("missing category accepted")
	}
	if _, err := c.Select("finance", []string{"unknown"}); err == nil {
		t.Fatal("unknown attribute accepted")
	}
	if p, err := c.Select("wide", nil); err == nil || len(p.Entries) != 0 {
		t.Fatalf("broad category deployed: %+v / %v", p, err)
	}
	categories[0].Attrs[0] = "mutated"
	if c.Categories()[0].Attrs[0] != "!cn" {
		t.Fatal("category DTO aliases cache state")
	}
}

func TestGeositeRejectsMalformedCatalogAndInvalidAttributes(t *testing.T) {
	for _, body := range []string{
		"lists: [",
		"lists: [{name: x, length: 2, rules: [domain:example.com]}]",
		"lists: [{name: x, length: 1, rules: [domain:example.com]}, {name: x, length: 1, rules: [domain:example.org]}]",
		"lists: [{name: x, length: 1, rules: ['domain:example.com:@']} ]",
		"lists: [{name: x, length: 1, rules: ['domain:example.com:@cn,@bad attr']} ]",
		"lists: [{name: x, length: 1, rules: ['regexp:[']} ]",
		"lists: []\n---\nlists: []",
	} {
		if c, err := ParseGeosite([]byte(body), geoChecksum(body)); err == nil || c != nil {
			t.Fatalf("invalid catalog accepted: %s / %v", body, err)
		}
	}
}

func TestGeositeVerifiedReleaseFixture(t *testing.T) {
	file, err := os.Open("testdata/dlc-20261004053124.yml.gz")
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	reader, err := gzip.NewReader(file)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	raw, err := io.ReadAll(io.LimitReader(reader, MaxRemoteBytes+1))
	if err != nil {
		t.Fatal(err)
	}
	checksum, err := os.ReadFile("testdata/dlc-20261004053124.sha256sum")
	if err != nil {
		t.Fatal(err)
	}
	c, err := ParseGeosite(raw, checksum)
	if err != nil {
		t.Fatal(err)
	}
	p, err := c.Select("category-finance", nil)
	if err != nil || p.EntryCount == 0 {
		t.Fatalf("finance not ready: count=%d / %v", p.EntryCount, err)
	}
	if slices.Contains(strings.Split(string(p.Entries), "\n"), "domain:hsbc") || p.Report.Ignored == 0 {
		t.Fatal("unsafe finance entry retained or filtering not reported")
	}
	t.Logf("verified release: bytes=%d categories=%d finance_entries=%d regexps=%d digest=%s", len(raw), len(c.Categories()), p.EntryCount, p.RegexpCount, p.ContentSHA256)
}

func TestGeositeFiltersBroadEntriesAndKeepsSourceOrdinals(t *testing.T) {
	body := `lists:
  - name: mixed
    length: 7
    rules:
      - "domain:example.org:@other"
      - "domain:com:@cn"
      - "keyword:ab:@cn"
      - "regexp:.*:@cn"
      - "domain:Example.COM.:@cn"
      - "domain:example.com:@cn"
      - "full:ok.example.com:@cn"
`
	c, err := ParseGeosite([]byte(body), geoChecksum(body))
	if err != nil {
		t.Fatal(err)
	}
	p, err := c.Select("mixed", []string{"cn"})
	if err != nil || string(p.Entries) != "domain:example.com\nfull:ok.example.com\n" {
		t.Fatalf("safe subset unavailable: %+v / %v", p, err)
	}
	if p.Report.Accepted != 2 || p.Report.Ignored != 4 || p.Report.Rewritten != 1 {
		t.Fatalf("wrong accounting: %+v", p.Report)
	}
	encoded, _ := json.Marshal(p.Report)
	var report map[string]any
	_ = json.Unmarshal(encoded, &report)
	if report["ignored_broad"] != float64(3) {
		t.Fatalf("no broad count: %s", encoded)
	}
	if len(p.Report.Samples) != 5 || p.Report.Samples[0].Line != 2 || p.Report.Samples[0].Text != "domain:com:@cn" {
		t.Fatalf("source ordinals/text lost: %+v", p.Report.Samples)
	}
	encoded, _ = json.Marshal(c.Categories()[0])
	var category map[string]any
	_ = json.Unmarshal(encoded, &category)
	if category["count"] != float64(3) || category["source_count"] != float64(7) || category["ignored_broad_count"] != float64(3) || category["regexp_count"] != float64(0) {
		t.Fatalf("unsafe/source counts conflated: %s", encoded)
	}
	if _, err := ParseRemote([]byte("domain:example.com\ndomain:com\n")); err == nil {
		t.Fatal("remote URL broad protection relaxed")
	}
}

func TestGeositeEmptyFilterReturnsReportAndNoUsableContent(t *testing.T) {
	c, err := ParseGeosite([]byte(geoFixture), geoChecksum(geoFixture))
	if err != nil {
		t.Fatal(err)
	}
	p, err := c.Select("wide", nil)
	if err == nil || err.Error() != "dest_list_empty_after_filter" || p.EntryCount != 0 || len(p.Entries) != 0 || p.Report.Ignored != 1 {
		t.Fatalf("empty category falsely usable or report missing: %+v / %v", p, err)
	}
	p, err = c.Select("finance", []string{"ads", "!cn"})
	if err == nil || err.Error() != "dest_list_empty_after_filter" || p.EntryCount != 0 {
		t.Fatalf("empty attribute intersection ready: %+v / %v", p, err)
	}
}
