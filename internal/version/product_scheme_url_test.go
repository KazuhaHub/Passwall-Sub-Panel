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

	// A product-scheme build does NOT derive a per-major URL: its first segment
	// is a release line, so v102.json would be a file that either does not exist
	// or means something else.
	//
	// IT USED TO DERIVE NOTHING AT ALL, and this test used to assert the refusal.
	// That was the honest description of a gap — the build reached no ranges
	// whatsoever. It now reaches a document that is NAMED rather than derived and
	// states the builds it applies to, so the assertion becomes: not a per-major
	// path, and a document that carries its own window.
	Version = "102.1.0"
	url, err = defaultURLForCurrentVersion()
	if err != nil {
		t.Fatalf("a product build must reach the named document: %v", err)
	}
	if strings.Contains(url, "v102") || strings.HasSuffix(url, "/v4.json") {
		t.Fatalf("a per-major path was produced from an unprefixed version: %q", url)
	}
	if !strings.HasSuffix(url, "/"+panelRangesDocumentName) {
		t.Fatalf("URL = %q, want the named panel ranges document", url)
	}

	// And a version that is neither scheme still derives nothing, because there
	// is no window it could be matched against.
	for _, unidentifiable := range []string{"dev", "", "0.1.0"} {
		Version = unidentifiable
		url, err = defaultURLForCurrentVersion()
		if err == nil {
			t.Fatalf("Version=%q derived %q; it has no release identity to match a window against", unidentifiable, url)
		}
		if !strings.Contains(err.Error(), "stays unknown") {
			t.Errorf("the refusal must state the consequence; it said %q", err)
		}
	}
}
