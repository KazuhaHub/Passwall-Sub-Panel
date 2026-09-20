package version

import (
	"strings"
	"testing"
)

// The compatibility major used to be read from the first integer of whatever the
// build called itself. That was fine while every version was v-prefixed and the
// first integer WAS the major. Under the product scheme the first integer is a
// release line, and the two are not the same number: 102.1.0 is the
// hundred-and-second release line, and there is no v102.json.
func TestOnlyALegacyBuildHasACompatibilityMajor(t *testing.T) {
	for _, tc := range []struct {
		version string
		major   int
		ok      bool
	}{
		{"v4.0.0-beta.25", 4, true},
		{"v3.6.0", 3, true},
		{"v10.0.0", 10, true},
		// Product-scheme versions: no prefix, and no derivable major.
		{"4.0.0", 0, false},
		{"102.1.0", 0, false},
		{"102", 0, false},
		// Neither scheme.
		{"dev", 0, false},
		{"", 0, false},
		{"v", 0, false},
		{"v.4.0", 0, false},
	} {
		major, ok := pspMajor(tc.version)
		if ok != tc.ok || major != tc.major {
			t.Errorf("pspMajor(%q) = (%d, %v), want (%d, %v)", tc.version, major, ok, tc.major, tc.ok)
		}
	}
}

func TestAProductVersionHasNoPerMajorManifestURL(t *testing.T) {
	previous := Version
	t.Cleanup(func() { Version = previous })

	// A legacy build still gets its per-major manifest.
	Version = "v4.0.0-beta.25"
	url, err := defaultURLForCurrentVersion()
	if err != nil {
		t.Fatalf("a legacy build must derive its manifest URL: %v", err)
	}
	if !strings.HasSuffix(url, "/v4.json") {
		t.Fatalf("legacy URL = %q, want it to end in /v4.json", url)
	}

	// A product-scheme build does not, and the refusal says why: the first
	// segment is a release line, so deriving v102.json would fetch a file that
	// either does not exist or means something else.
	Version = "102.1.0"
	url, err = defaultURLForCurrentVersion()
	if err == nil {
		t.Fatalf("a product version must not derive a per-major URL, got %q", url)
	}
	for _, want := range []string{"release line", "not a compatibility major"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not explain %q", err, want)
		}
	}
	if strings.Contains(url, "v102") {
		t.Fatal("a v102.json path was produced")
	}
}
