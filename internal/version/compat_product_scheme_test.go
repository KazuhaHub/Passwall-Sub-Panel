package version

import (
	"strings"
	"testing"
)

// A PRODUCT-SCHEME BUILD READS NO PER-MAJOR MANIFEST, and that is a decision
// rather than a gap in the code.
//
// The per-major files are named after the COMPATIBILITY major: v3.x reads
// v3.json. Under the product scheme the first segment is a RELEASE LINE — 102.1.0
// is the hundred-and-second release line, not "compat major 102" — so deriving a
// path from it would fetch `v102.json`, which does not exist, or worse, exists
// and describes a different panel. A product build takes its range from the
// release policy instead, which states the range explicitly rather than
// inferring one from a number.
//
// The consequence is FAIL-CLOSED and worth pinning as such: with no manifest
// loaded, the max-tested ceiling is empty, so a probed panel is reported as
// untested rather than as supported. An empty answer is the conservative one;
// the failure this guards against is a product build quietly deriving a major
// and inheriting a range nobody published for it.
//
// IT IS A GAP, NOT A DESIGN, and this file does not pretend otherwise: nothing
// carries the compat ranges in a policy-shaped document yet, so a product build
// has no source for them at all. The error message says that rather than naming a
// document that does not exist.
func TestAProductBuildDerivesNoPerMajorManifest(t *testing.T) {
	previous := Version
	t.Cleanup(func() { Version = previous })

	for _, tc := range []struct {
		version string
		url     string
		why     string
	}{
		{"v3.6.0", "v3.json", "the legacy form names a compatibility major"},
		{"v3.6.0-beta.7", "v3.json", "a prerelease does not change the major"},
		{"v4.0.0", "v4.json", ""},
		// The product scheme, and the release lines it can reach.
		{"3.6.0", "", "no prefix, so no major to derive"},
		{"102.1.0", "", "102 is a release LINE; there is no v102.json"},
		{"0.1.0", "", "and a zero line is not a released identity"},
		{"dev", "", ""},
		{"", "", ""},
	} {
		t.Run(tc.version, func(t *testing.T) {
			Version = tc.version
			url, err := defaultURLForCurrentVersion()
			if tc.url == "" {
				if err == nil {
					t.Fatalf("Version=%q derived %q; it must not read a per-major manifest (%s)", tc.version, url, tc.why)
				}
				// The message names the cause and states the consequence — an
				// operator reading it must not be told a range was configured.
				if !strings.Contains(err.Error(), "release line") || !strings.Contains(err.Error(), "stays unknown") {
					t.Fatalf("the refusal must say why; it said %q", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("Version=%q: %v", tc.version, err)
			}
			if !strings.HasSuffix(url, tc.url) {
				t.Fatalf("Version=%q derived %q, want it to end in %q", tc.version, url, tc.url)
			}
		})
	}
}

// With nothing loaded, the ceiling is empty — and empty is what makes the
// admission path conservative. It is asserted rather than assumed because the
// opposite (a compiled ceiling) would silently widen every product build.
func TestNoManifestMeansNoCeilingRatherThanAPermissiveOne(t *testing.T) {
	previous := ActiveMaxTestedXUI()
	t.Cleanup(func() { SetActiveMaxTestedXUI(previous) })
	SetActiveMaxTestedXUI("")
	if got := ActiveMaxTestedXUI(); got != "" {
		t.Fatalf("max tested = %q with no manifest loaded", got)
	}
}
