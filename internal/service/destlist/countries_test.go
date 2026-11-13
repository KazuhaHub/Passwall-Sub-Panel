package destlist

import (
	"context"
	"errors"
	"net"
	"net/netip"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/maxmind/mmdbwriter"
	"github.com/maxmind/mmdbwriter/mmdbtype"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
)

type fixedLocator struct{ path string }

func (f *fixedLocator) ActiveMMDBPath(context.Context) string { return f.path }

// buildMMDB writes a real .mmdb of the given networks, each mapped to the
// record rec(cc), and returns its path. mmdbwriter treats reserved networks as
// skippable, hence IncludeReservedNetworks. The same technique as authcore's
// geoip fixtures: a database built in the test, never a binary asset.
func buildMMDB(t *testing.T, dir, name string, nets map[string]string, rec func(cc string) mmdbtype.DataType) string {
	t.Helper()
	w, err := mmdbwriter.New(mmdbwriter.Options{DatabaseType: "Test-Country", IncludeReservedNetworks: true, RecordSize: 24})
	if err != nil {
		t.Fatal(err)
	}
	// Sorted insertion keeps the fixture deterministic.
	keys := make([]string, 0, len(nets))
	for cidr := range nets {
		keys = append(keys, cidr)
	}
	slices.Sort(keys)
	for _, cidr := range keys {
		_, n, err := net.ParseCIDR(cidr)
		if err != nil {
			t.Fatal(err)
		}
		if err := w.Insert(n, rec(nets[cidr])); err != nil {
			t.Fatal(err)
		}
	}
	path := filepath.Join(dir, name)
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if _, err := w.WriteTo(f); err != nil {
		t.Fatal(err)
	}
	return path
}

func maxmindRecord(cc string) mmdbtype.DataType {
	return mmdbtype.Map{"country": mmdbtype.Map{
		"iso_code": mmdbtype.String(cc), "names": mmdbtype.Map{"en": mmdbtype.String("Somewhere")},
	}}
}

func flatRecord(cc string) mmdbtype.DataType {
	return mmdbtype.Map{"country": mmdbtype.String("Somewhere"), "country_code": mmdbtype.String(cc)}
}

func prefixes(t *testing.T, in ...string) []netip.Prefix {
	t.Helper()
	out := make([]netip.Prefix, 0, len(in))
	for _, s := range in {
		out = append(out, netip.MustParsePrefix(s))
	}
	return out
}

func TestMergePrefixes(t *testing.T) {
	for _, c := range []struct {
		name string
		in   []string
		want []string
	}{
		{"adjacent siblings merge", []string{"1.0.0.0/24", "1.0.1.0/24"}, []string{"1.0.0.0/23"}},
		{"four /24s become a /22", []string{"1.0.3.0/24", "1.0.0.0/24", "1.0.2.0/24", "1.0.1.0/24"}, []string{"1.0.0.0/22"}},
		{"adjacent but not siblings stay apart", []string{"1.0.1.0/24", "1.0.2.0/24"}, []string{"1.0.1.0/24", "1.0.2.0/24"}},
		{"a gap stays a gap", []string{"1.0.0.0/24", "1.0.2.0/24"}, []string{"1.0.0.0/24", "1.0.2.0/24"}},
		{"contained is dropped", []string{"10.0.0.0/8", "10.1.0.0/16", "10.0.0.0/8"}, []string{"10.0.0.0/8"}},
		{"a merge can enable the next", []string{"10.0.0.0/25", "10.0.0.128/26", "10.0.0.192/26"}, []string{"10.0.0.0/24"}},
		{"cascade to a /16", []string{"192.168.0.0/17", "192.168.128.0/18", "192.168.192.0/19", "192.168.224.0/19"}, []string{"192.168.0.0/16"}},
		{"ipv6 siblings", []string{"2400::/33", "2400:0:8000::/33"}, []string{"2400::/32"}},
		{"families do not mix", []string{"1.0.0.0/24", "2400::/32", "1.0.1.0/24"}, []string{"1.0.0.0/23", "2400::/32"}},
		{"unmasked input is masked", []string{"1.0.0.9/24"}, []string{"1.0.0.0/24"}},
		{"empty", nil, nil},
		{"a single address", []string{"203.0.113.9/32"}, []string{"203.0.113.9/32"}},
		{"everything collapses to /0", []string{"0.0.0.0/1", "128.0.0.0/1"}, []string{"0.0.0.0/0"}},
	} {
		t.Run(c.name, func(t *testing.T) {
			got := MergePrefixes(prefixes(t, c.in...))
			var s []string
			for _, p := range got {
				s = append(s, p.String())
			}
			if !slices.Equal(s, c.want) {
				t.Fatalf("got %v, want %v", s, c.want)
			}
		})
	}
}

func TestCountryExpander_ExpandsAndMerges(t *testing.T) {
	dir := t.TempDir()
	path := buildMMDB(t, dir, "geo.mmdb", map[string]string{
		"1.0.0.0/24": "CN", "1.0.1.0/24": "CN", "1.0.2.0/24": "CN", "1.0.3.0/24": "CN", "1.0.8.0/24": "CN",
		"2400::/32": "CN", "2400:1::/32": "CN",
		"5.0.0.0/16": "RU", "8.8.8.0/24": "US", "9.9.9.0/24": "us",
	}, maxmindRecord)
	e := NewCountryExpander(&fixedLocator{path: path})

	got, err := e.CIDRs(context.Background(), []string{"cn"})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"1.0.0.0/22", "1.0.8.0/24", "2400::/31"}
	if !slices.Equal(got, want) {
		t.Fatalf("CN = %v, want %v", got, want)
	}

	// Several countries in one answer, merged together, sorted bytewise (the
	// order the wire validates), codes case-insensitive and repeats ignored.
	got, err = e.CIDRs(context.Background(), []string{"RU", "ru", "US"})
	if err != nil {
		t.Fatal(err)
	}
	// The fixture stores 9.9.9.0/24 as "us": the record's code is upper-cased.
	if want := []string{"5.0.0.0/16", "8.8.8.0/24", "9.9.9.0/24"}; !slices.Equal(got, want) {
		t.Fatalf("RU+US = %v, want %v", got, want)
	}
	if !slices.IsSorted(got) {
		t.Fatalf("not bytewise sorted: %v", got)
	}

	// A country the database does not hold is an empty set, not an error.
	got, err = e.CIDRs(context.Background(), []string{"ZZ"})
	if err != nil || len(got) != 0 {
		t.Fatalf("ZZ = %v, %v", got, err)
	}
	// Nothing asked, nothing read.
	got, err = e.CIDRs(context.Background(), nil)
	if err != nil || got != nil {
		t.Fatalf("no countries = %v, %v", got, err)
	}
	if _, err := e.CIDRs(context.Background(), []string{"CHN"}); !errors.Is(err, domain.ErrValidation) {
		t.Fatalf("a bad country code: %v", err)
	}
}

func TestCountryExpander_ReadsTheFlatSchemaToo(t *testing.T) {
	path := buildMMDB(t, t.TempDir(), "ipinfo.mmdb", map[string]string{"1.0.0.0/24": "CN", "1.0.1.0/24": "cn", "5.0.0.0/16": "RU"}, flatRecord)
	got, err := NewCountryExpander(&fixedLocator{path: path}).CIDRs(context.Background(), []string{"CN"})
	if err != nil || !slices.Equal(got, []string{"1.0.0.0/23"}) {
		t.Fatalf("flat schema CN = %v, %v", got, err)
	}
}

func TestCountryExpander_UnavailableWithoutADatabase(t *testing.T) {
	for name, loc := range map[string]MMDBLocator{
		"no path":      &fixedLocator{},
		"missing file": &fixedLocator{path: filepath.Join(t.TempDir(), "absent.mmdb")},
		"nil locator":  nil,
		"not an mmdb":  &fixedLocator{path: writeFile(t, "junk.mmdb", "this is not a database")},
		"empty file":   &fixedLocator{path: writeFile(t, "empty.mmdb", "")},
	} {
		e := NewCountryExpander(loc)
		if _, err := e.CIDRs(context.Background(), []string{"CN"}); !errors.Is(err, ErrGeoIPUnavailable) {
			t.Errorf("%s: err = %v, want ErrGeoIPUnavailable", name, err)
		}
	}
	if !errors.Is(ErrGeoIPUnavailable, domain.ErrUnavailable) {
		t.Error("ErrGeoIPUnavailable must classify as ErrUnavailable (HTTP 503)")
	}
	// The digest of a database that is not there is also "unavailable".
	if _, err := NewCountryExpander(&fixedLocator{}).DatabaseSHA256(context.Background()); !errors.Is(err, ErrGeoIPUnavailable) {
		t.Errorf("digest without a database: %v", err)
	}
}

func writeFile(t *testing.T, name, content string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

// The cache is keyed by (database digest, countries): asking again does not
// read the database again, and a replaced database — a different digest, even
// at the same path — is read afresh.
func TestCountryExpander_CachesByDatabaseDigestAndCountries(t *testing.T) {
	dir := t.TempDir()
	path := buildMMDB(t, dir, "geo.mmdb", map[string]string{"1.0.0.0/24": "CN"}, maxmindRecord)
	e := NewCountryExpander(&fixedLocator{path: path})
	ctx := context.Background()

	first, err := e.CIDRs(ctx, []string{"CN"})
	if err != nil || e.Expansions() != 1 {
		t.Fatalf("first: %v %v expansions %d", first, err, e.Expansions())
	}
	if _, err := e.CIDRs(ctx, []string{"cn", "CN"}); err != nil || e.Expansions() != 1 {
		t.Fatalf("the same set again re-read the database (expansions %d, err %v)", e.Expansions(), err)
	}
	if _, err := e.CIDRs(ctx, []string{"CN", "RU"}); err != nil || e.Expansions() != 2 {
		t.Fatalf("a different set must be expanded (expansions %d, err %v)", e.Expansions(), err)
	}
	sum1, err := e.DatabaseSHA256(ctx)
	if err != nil || len(sum1) != 64 {
		t.Fatalf("digest = %q, %v", sum1, err)
	}

	// Replace the database at the same path with different content.
	buildMMDB(t, dir, "geo.mmdb", map[string]string{"1.0.0.0/24": "CN", "1.0.1.0/24": "CN", "7.0.0.0/8": "CN"}, maxmindRecord)
	now := e.Expansions()
	got, err := e.CIDRs(ctx, []string{"CN"})
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"1.0.0.0/23", "7.0.0.0/8"}; !slices.Equal(got, want) {
		t.Fatalf("after the replacement CN = %v, want %v", got, want)
	}
	if e.Expansions() != now+1 {
		t.Fatalf("a replaced database was served from the cache")
	}
	if sum2, _ := e.DatabaseSHA256(ctx); sum2 == sum1 {
		t.Fatal("the digest did not change with the file")
	}
}

func TestCountryExpander_ReturnsACopy(t *testing.T) {
	path := buildMMDB(t, t.TempDir(), "geo.mmdb", map[string]string{"1.0.0.0/24": "CN"}, maxmindRecord)
	e := NewCountryExpander(&fixedLocator{path: path})
	a, _ := e.CIDRs(context.Background(), []string{"CN"})
	a[0] = "tampered"
	b, _ := e.CIDRs(context.Background(), []string{"CN"})
	if b[0] == "tampered" {
		t.Fatal("the cache hands out its own backing array")
	}
}
