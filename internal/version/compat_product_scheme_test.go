package version

import (
	"strings"
	"testing"
)

// A BUILD DERIVES ITS OWN DOCUMENT NAMES, AND THE MAJOR IN THEM IS THE PANEL'S.
//
// A legacy build reads ONE per-major manifest named after the COMPATIBILITY
// major: v3.x reads v3.json. A product build reads ONE DOCUMENT PER PRODUCT,
// named after the PANEL MAJOR its own version carries: 4.0.0 reads
// 3x-ui-v4.json and sui-v4.json.
//
// DERIVING IS SAFE BECAUSE THE NAME IS NOT THE CLAIM. The document states the
// builds it applies to in `applies_to_psp`, and the apply path checks that
// window before installing anything — so a document that is not about this
// build installs nothing even though its name was derived from this build's own
// version. The name decides WHERE to look; the window decides whether what was
// found counts.
//
// THE FAILURE MODE IS A 404 RATHER THAN A REFUSAL. A line whose documents have
// not been published, or a hundred-and-second line that never will be, gets no
// ceiling — and empty is the conservative answer: a probed panel is reported as
// untested rather than as supported. A version with no identity at all is a
// refusal instead, because there is no window it could be matched against and
// nothing useful to say about where to look.
func TestABuildDerivesItsOwnDocumentNames(t *testing.T) {
	previous := Version
	t.Cleanup(func() { Version = previous })

	for _, tc := range []struct {
		version string
		want    []string
		why     string
	}{
		{"v3.6.0", []string{"v3.json"}, "the legacy form names a compatibility major"},
		{"v3.6.0-beta.7", []string{"v3.json"}, "a prerelease does not change the major"},
		{"v4.0.0-beta.25", []string{"v4.json"}, "the legacy form is what the beta line carried"},
		{"4.0.0", []string{"3x-ui-v4.json", "sui-v4.json"}, "a product build reads one document per product"},
		{"4.1.7", []string{"3x-ui-v4.json", "sui-v4.json"}, "the document follows the major, not the release line's patch"},
		{"102.1.0", []string{"3x-ui-v102.json", "sui-v102.json"}, "102 IS the panel major of that line, and the name follows it"},
		{"0.1.0", nil, "a zero release line is not a released identity"},
		{"dev", nil, ""},
		{"", nil, ""},
	} {
		t.Run(tc.version, func(t *testing.T) {
			Version = tc.version
			sources, err := compatDocumentSources()
			if tc.want == nil {
				if err == nil {
					t.Fatalf("Version=%q derived %v; it has no identity to match a window against (%s)", tc.version, sources, tc.why)
				}
				// The message must state the consequence — an operator reading it
				// must not be told a range was configured.
				if !strings.Contains(err.Error(), "stays unknown") {
					t.Fatalf("the refusal must say why; it said %q", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("Version=%q: %v", tc.version, err)
			}
			got := make([]string, 0, len(sources))
			for _, source := range sources {
				if !strings.HasSuffix(source.URL, "/docs/compat/"+source.Name) {
					t.Errorf("URL %q does not address %q", source.URL, source.Name)
				}
				got = append(got, source.Name)
			}
			if len(got) != len(tc.want) {
				t.Fatalf("Version=%q derived %v, want %v (%s)", tc.version, got, tc.want, tc.why)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Fatalf("Version=%q derived %v, want %v (%s)", tc.version, got, tc.want, tc.why)
				}
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
