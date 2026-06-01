package geo

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"strings"
	"testing"
	"time"

	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

func gz(b []byte) []byte {
	var buf bytes.Buffer
	w := gzip.NewWriter(&buf)
	_, _ = w.Write(b)
	_ = w.Close()
	return buf.Bytes()
}

func targz(name string, content []byte) []byte {
	var buf bytes.Buffer
	gw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gw)
	_ = tw.WriteHeader(&tar.Header{Name: name, Mode: 0o644, Size: int64(len(content)), Typeflag: tar.TypeReg})
	_, _ = tw.Write(content)
	_ = tw.Close()
	_ = gw.Close()
	return buf.Bytes()
}

func TestExtractMMDB(t *testing.T) {
	// Plain bytes pass through.
	if got, err := extractMMDB([]byte("RAW-MMDB")); err != nil || string(got) != "RAW-MMDB" {
		t.Fatalf("plain: got %q err %v", got, err)
	}
	// gzip'd .mmdb (DB-IP / ipinfo .gz) → inner bytes.
	if got, err := extractMMDB(gz([]byte("INNER-MMDB"))); err != nil || string(got) != "INNER-MMDB" {
		t.Fatalf("gzip: got %q err %v", got, err)
	}
	// MaxMind .tar.gz with the .mmdb nested in a dated folder → inner bytes.
	blob := targz("GeoLite2-City_20260601/GeoLite2-City.mmdb", []byte("TAR-MMDB"))
	if got, err := extractMMDB(blob); err != nil || string(got) != "TAR-MMDB" {
		t.Fatalf("tar.gz: got %q err %v", got, err)
	}
	// tar.gz with a non-.mmdb entry → falls back to treating gunzipped data as
	// the file (here it's a tar, no .mmdb found → returns the tar bytes, which a
	// later geoip.Open would reject — acceptable; the point is no crash).
	if _, err := extractMMDB(targz("README.txt", []byte("hi"))); err != nil {
		t.Fatalf("tar.gz without mmdb should not error at extract stage: %v", err)
	}
}

func TestCandidateURLs(t *testing.T) {
	// maxmind requires a license key.
	if _, _, err := candidateURLs(ports.UISettings{GeoIPUpdateSource: "maxmind"}); err == nil {
		t.Fatal("maxmind without token must error")
	}
	urls, target, err := candidateURLs(ports.UISettings{
		GeoIPUpdateSource: "maxmind", GeoIPUpdateToken: "LIC123", GeoIPUpdateEdition: "GeoLite2-City",
	})
	if err != nil || len(urls) != 1 ||
		!strings.Contains(urls[0], "download.maxmind.com") ||
		!strings.Contains(urls[0], "edition_id=GeoLite2-City") ||
		!strings.Contains(urls[0], "license_key=LIC123") {
		t.Fatalf("maxmind url = %v (%v)", urls, err)
	}
	if target != "GeoLite2-City.mmdb" {
		t.Fatalf("maxmind target = %q", target)
	}

	// ipinfo requires a token.
	if _, _, err := candidateURLs(ports.UISettings{GeoIPUpdateSource: "ipinfo"}); err == nil {
		t.Fatal("ipinfo without token must error")
	}
	urls, target, _ = candidateURLs(ports.UISettings{GeoIPUpdateSource: "ipinfo", GeoIPUpdateToken: "tok"})
	if len(urls) != 1 || !strings.Contains(urls[0], "ipinfo.io/data/ipinfo_lite.mmdb") || target != "ipinfo_lite.mmdb" {
		t.Fatalf("ipinfo url/target = %v / %q", urls, target)
	}
	// Paid IPinfo product via the edition field → URL + target follow it.
	urls, target, _ = candidateURLs(ports.UISettings{GeoIPUpdateSource: "ipinfo", GeoIPUpdateToken: "tok", GeoIPUpdateEdition: "standard_location"})
	if len(urls) != 1 || !strings.Contains(urls[0], "ipinfo.io/data/standard_location.mmdb") || target != "standard_location.mmdb" {
		t.Fatalf("ipinfo paid url/target = %v / %q", urls, target)
	}

	// dbip returns current + previous month (month-stamped, no stable latest).
	urls, target, _ = candidateURLs(ports.UISettings{GeoIPUpdateSource: "dbip"})
	if len(urls) != 2 || target != "dbip-city-lite.mmdb" {
		t.Fatalf("dbip urls/target = %v / %q", urls, target)
	}

	// custom requires a URL; target derived from basename.
	if _, _, err := candidateURLs(ports.UISettings{GeoIPUpdateSource: "custom"}); err == nil {
		t.Fatal("custom without url must error")
	}
	urls, target, _ = candidateURLs(ports.UISettings{GeoIPUpdateSource: "custom", GeoIPUpdateURL: "https://x.test/my-geo.mmdb"})
	if len(urls) != 1 || target != "my-geo.mmdb" {
		t.Fatalf("custom urls/target = %v / %q", urls, target)
	}

	// unknown source errors.
	if _, _, err := candidateURLs(ports.UISettings{GeoIPUpdateSource: "bogus"}); err == nil {
		t.Fatal("unknown source must error")
	}
}

// TestUpdateSourcesNoDrift is the drift guard between the API whitelist
// (IsValidUpdateSource, used by the settings handler) and the downloader
// (candidateURLs). Every source the API accepts MUST be one the downloader can
// actually fetch — the dbip regression (UI offered it, API 400'd it) was
// exactly this drift. minCreds supplies the least input each source needs so we
// assert candidateURLs returns NO error (not merely "not the unknown error").
func TestUpdateSourcesNoDrift(t *testing.T) {
	minCreds := map[string]ports.UISettings{
		"maxmind": {GeoIPUpdateToken: "LIC"},
		"ipinfo":  {GeoIPUpdateToken: "tok"},
		"dbip":    {},
		"custom":  {GeoIPUpdateURL: "https://x.test/geo.mmdb"},
	}
	for _, src := range knownUpdateSources {
		if !IsValidUpdateSource(src) {
			t.Errorf("knownUpdateSources has %q but IsValidUpdateSource rejects it", src)
		}
		set, ok := minCreds[src]
		if !ok {
			t.Fatalf("test missing minimal creds for source %q — add them", src)
		}
		set.GeoIPUpdateSource = src
		if _, _, err := candidateURLs(set); err != nil {
			t.Errorf("candidateURLs(%q) errored with minimal creds: %v — source in whitelist but downloader can't fetch it", src, err)
		}
	}
	// Empty string is accepted (defaults to maxmind inside candidateURLs).
	if !IsValidUpdateSource("") {
		t.Error(`IsValidUpdateSource("") must be true (defaults to maxmind)`)
	}
	// A bogus source is rejected by BOTH the whitelist and the downloader.
	if IsValidUpdateSource("bogus") {
		t.Error("IsValidUpdateSource(bogus) must be false")
	}
	if _, _, err := candidateURLs(ports.UISettings{GeoIPUpdateSource: "bogus"}); err == nil {
		t.Error("candidateURLs(bogus) must error")
	}
}

func TestCustomTarget(t *testing.T) {
	cases := map[string]string{
		"https://x.test/foo.mmdb":          "foo.mmdb",
		"https://x.test/foo.mmdb.gz":       "foo.mmdb",
		"https://x.test/bar.tar.gz":        "custom.mmdb", // stripped to "bar" → not .mmdb
		"https://x.test/db.mmdb?token=abc": "db.mmdb",
		"https://x.test/":                  "custom.mmdb",
	}
	for in, want := range cases {
		if got := customTarget(in); got != want {
			t.Errorf("customTarget(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestDBIPURL(t *testing.T) {
	got := dbipURL(time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC))
	if got != "https://download.db-ip.com/free/dbip-city-lite-2026-06.mmdb.gz" {
		t.Fatalf("dbipURL = %q", got)
	}
}

// TestPrevMonthOf locks the month-end fix: the DB-IP fallback must land in the
// genuinely previous month even on a 29th/30th/31st, where t.AddDate(0,-1,0)
// would normalise back into the current (or a wrong) month.
func TestPrevMonthOf(t *testing.T) {
	cases := map[time.Time]string{ // input → expected "2006-01" of the prev month
		time.Date(2026, 3, 31, 0, 0, 0, 0, time.UTC): "2026-02", // 31st: AddDate would give "2026-03"
		time.Date(2026, 5, 31, 0, 0, 0, 0, time.UTC): "2026-04",
		time.Date(2026, 1, 31, 0, 0, 0, 0, time.UTC): "2025-12", // year rollover
		time.Date(2026, 6, 15, 0, 0, 0, 0, time.UTC): "2026-05", // mid-month, easy case
		time.Date(2024, 3, 30, 0, 0, 0, 0, time.UTC): "2024-02", // leap-year Feb
	}
	for in, want := range cases {
		if got := prevMonthOf(in).Format("2006-01"); got != want {
			t.Errorf("prevMonthOf(%s) = %s, want %s", in.Format("2006-01-02"), got, want)
		}
	}
}
