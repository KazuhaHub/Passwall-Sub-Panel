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

// A PRODUCT BUILD MUST NOT DERIVE A PER-MAJOR MANIFEST NAME.
//
// The per-major files (v3.json, v4.json) are named after a COMPATIBILITY major,
// and a product version's first segment is not one: 102.1.0 would derive
// v102.json, which either does not exist or describes a different panel. What a
// product build derives instead is one document per product, named after the
// panel major its own version carries.
//
// THIS IS THE SPECIFIC FILE NAME THAT MUST NOT APPEAR, which is the half worth
// keeping separate from the derivation itself: the danger is not a missing URL,
// it is a build quietly inheriting a range nobody published for it because a
// number it derived happened to name somebody else's file.
func TestAProductBuildDerivesNoPerMajorManifestName(t *testing.T) {
	previous := Version
	t.Cleanup(func() { Version = previous })

	for _, tc := range []struct{ version, forbidden string }{
		{"102.1.0", "v102.json"},
		{"4.0.0", "v4.json"},
		{"3.6.0", "v3.json"},
	} {
		Version = tc.version
		sources, err := compatDocumentSources()
		if err != nil {
			t.Fatalf("Version=%q: %v", tc.version, err)
		}
		for _, source := range sources {
			if strings.HasSuffix(source.URL, "/"+tc.forbidden) {
				t.Fatalf("Version=%q derived the per-major file %s (%s)", tc.version, tc.forbidden, source.URL)
			}
		}
	}

	// A LEGACY BUILD STILL GETS ITS PER-MAJOR MANIFEST. That route is frozen
	// rather than removed, and nothing here may reach it.
	Version = "v4.0.0-beta.25"
	sources, err := compatDocumentSources()
	if err != nil {
		t.Fatalf("a legacy build must derive its manifest URL: %v", err)
	}
	if len(sources) != 1 || !strings.HasSuffix(sources[0].URL, "/v4.json") {
		t.Fatalf("legacy sources = %v, want the single /v4.json manifest", sources)
	}
}

// A version that is neither scheme derives nothing, because there is no window
// it could be matched against.
func TestAnUnidentifiableVersionDerivesNothing(t *testing.T) {
	previous := Version
	t.Cleanup(func() { Version = previous })
	for _, unidentifiable := range []string{"dev", "", "0.1.0"} {
		Version = unidentifiable
		sources, err := compatDocumentSources()
		if err == nil {
			t.Fatalf("Version=%q derived %v; it has no release identity to match a window against", unidentifiable, sources)
		}
		if !strings.Contains(err.Error(), "stays unknown") {
			t.Errorf("the refusal must state the consequence; it said %q", err)
		}
	}
}
