package version

import "testing"

func TestPSPBehindStable(t *testing.T) {
	cases := []struct {
		current, latest string
		want            bool
	}{
		// The latest is a TAG and the current is a VERSION, and they are never the
		// same string. Comparing them as they arrive finds nothing newer every
		// time, so the nudge silently never appears.
		{"4.0.0", "release/4.0.1", true},   // behind a newer release
		{"4.0.0", "release/4.0.0", false},  // same release
		{"4.1.0", "release/4.0.0", false},  // ahead of it
		{"4.0.0", "release/102.1.0", true}, // behind across a release line
		{"1.0.0", "release/4.0.0", true},   // behind on the first segment
		// A BUILD COMPONENT IS PART OF THE ORDER. This is why the comparison is
		// the project's own rule rather than a three-integer parse: the parse
		// refuses the string outright, so a build stamped with a fourth segment
		// would never be nudged at all — silently, and only for the builds the
		// reordering work exists to distinguish.
		{"4.0.0.1", "release/4.0.0", false}, // ahead of its base
		{"4.0.0", "release/4.0.0.1", true},  // behind a rebuilt base
		// NOTHING TO COMPARE, OR NOTHING TO COMPARE IT TO.
		{"dev", "release/4.0.0", false}, // a source build is never nagged
		{"4.0.0", "", false},            // no latest yet
		{"", "release/4.0.0", false},    // unknown current
		{"dev-abc123", "release/4.0.0", false},
	}
	for _, c := range cases {
		if got := pspBehindStable(c.current, c.latest); got != c.want {
			t.Errorf("pspBehindStable(%q, %q) = %v, want %v", c.current, c.latest, got, c.want)
		}
	}
}

// A LATEST THAT IS NOT ONE OF OUR TAGS IS NOT A LATEST. Two shapes reach here
// from outside and neither is an address this project publishes: a bare version
// (`4.0.0` — a version, not a path) and the legacy v-prefixed tag. Accepting
// either would build a nudge around a release the panel cannot then describe.
func TestPSPBehindStableRefusesALatestThatIsNotAReleaseTag(t *testing.T) {
	for _, latest := range []string{"4.0.0", "v4.0.0", "v3.7.0", "release/4.0", "release/latest", "latest", "release/"} {
		if pspBehindStable("4.0.0", latest) {
			t.Errorf("pspBehindStable(4.0.0, %q) claimed this build is behind it", latest)
		}
	}
}

func TestAcceptLatestPSPStable(t *testing.T) {
	cases := []struct {
		tag        string
		prerelease bool
		wantTag    string
		wantOK     bool
	}{
		{"release/4.0.0", false, "release/4.0.0", true},
		{"release/102.1.0", false, "release/102.1.0", true},
		{"release/4.0.0", true, "", false}, // GitHub flagged it prerelease
		// THE ADDRESS IS REQUIRED. A bare version is a version, not a tag, and a
		// caller that took one would ask GitHub for a release at a path no
		// publication writes to.
		{"4.0.0", false, "", false},
		{"4.0.0", true, "", false},
		// THE LEGACY TAG IS GONE, flag or no flag. This is the one that used to be
		// load-bearing the other way: `v3.7.0-beta.16` arriving with the flag
		// unset had to be caught by the tag text.
		{"v3.7.0", false, "", false},
		{"v3.7.0-beta.16", false, "", false},
		{"v3.7.0-beta.16", true, "", false},
		// SHORTHAND AND NONSENSE.
		{"release/4.0", false, "", false},
		{"release/4.0.0.0", false, "", false},
		{"", false, "", false},
		{"garbage", false, "", false},
		{"latest", false, "", false},
	}
	for _, c := range cases {
		gotTag, gotOK := acceptLatestPSPStable(c.tag, c.prerelease)
		if gotTag != c.wantTag || gotOK != c.wantOK {
			t.Errorf("acceptLatestPSPStable(%q, %v) = (%q, %v), want (%q, %v)",
				c.tag, c.prerelease, gotTag, gotOK, c.wantTag, c.wantOK)
		}
	}
}

// THE FLAG DECIDES, BECAUSE THERE IS NO SECOND SIGNAL LEFT TO READ. The hyphen
// test that used to sit beside it existed for one historical gap: a beta cut
// before the workflow set the prerelease flag would arrive without it. A product
// tag is `release/MAJOR.MINOR.PATCH` — three integers in an explicit namespace,
// no hyphen anywhere — so the test found nothing to reject there, and the code
// carried an exemption to keep it from being applied where it was meaningless.
// With that shape gone there is no gap and no exemption: the flag is the whole
// decision, and the tag only has to be one of ours.
func TestStableSelectionIsTheFlagAndNothingElse(t *testing.T) {
	for _, tc := range []struct {
		name       string
		tag        string
		prerelease bool
		want       bool
	}{
		{name: "a testing candidate", tag: "release/4.0.0", prerelease: true, want: false},
		{name: "a released candidate", tag: "release/4.0.0", prerelease: false, want: true},
		{name: "a candidate the flag calls released", tag: "release/4.0.1", prerelease: false, want: true},
		{name: "nothing at all", tag: "", prerelease: false, want: false},
		{name: "not ours", tag: "4.0.0", prerelease: false, want: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, ok := acceptLatestPSPStable(tc.tag, tc.prerelease); ok != tc.want {
				t.Fatalf("acceptLatestPSPStable(%q, %v) = %v, want %v", tc.tag, tc.prerelease, ok, tc.want)
			}
		})
	}
}
