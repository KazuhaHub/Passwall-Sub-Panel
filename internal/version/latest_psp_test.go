package version

import "testing"

func TestPSPBehindStable(t *testing.T) {
	cases := []struct {
		current, latest string
		want            bool
	}{
		{"v3.6.4", "v3.7.0", true},          // older stable base
		{"v3.7.0", "v3.7.0", false},         // same stable
		{"v3.7.1", "v3.7.0", false},         // ahead
		{"v3.7.0-beta.16", "v3.7.0", true},  // beta behind its stable (the key case)
		{"v3.7.0-beta.16", "v3.6.4", false}, // beta ahead of latest stable
		{"v3.8.0-beta.1", "v3.7.0", false},  // newer base, even as a beta → not behind
		{"v3.6.4-beta.2", "v3.7.0", true},   // older base beta
		{"dev", "v3.7.0", false},            // dev build never nagged
		{"v3.7.0", "", false},               // no latest yet
		{"", "v3.7.0", false},               // unknown current
	}
	for _, c := range cases {
		if got := pspBehindStable(c.current, c.latest); got != c.want {
			t.Errorf("pspBehindStable(%q, %q) = %v, want %v", c.current, c.latest, got, c.want)
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
		{"v3.7.0", false, "v3.7.0", true},
		{"v3.7.0-beta.16", false, "", false}, // load-bearing: flag false but the tag is a beta
		{"v3.7.0-beta.16", true, "", false},
		{"v3.7.0", true, "", false}, // GitHub flagged it pre-release
		{"", false, "", false},
		{"garbage", false, "", false}, // not a parseable semver
	}
	for _, c := range cases {
		gotTag, gotOK := acceptLatestPSPStable(c.tag, c.prerelease)
		if gotTag != c.wantTag || gotOK != c.wantOK {
			t.Errorf("acceptLatestPSPStable(%q, %v) = (%q, %v), want (%q, %v)",
				c.tag, c.prerelease, gotTag, gotOK, c.wantTag, c.wantOK)
		}
	}
}

func TestIsPrerelease(t *testing.T) {
	for _, c := range []struct {
		v    string
		want bool
	}{
		{"v3.7.0", false},
		{"3.7.0", false},
		{"v3.7.0-beta.16", true},
		{"v3.7.0-rc.1", true},
		{"v3.7.0+build5", false}, // build metadata is not a pre-release
		{"dev", false},
	} {
		if got := IsPrerelease(c.v); got != c.want {
			t.Errorf("IsPrerelease(%q) = %v, want %v", c.v, got, c.want)
		}
	}
}

// The tag-text pre-release test is a LEGACY defence, and scoping it that way is
// the point: under the product scheme a version has no hyphen, so running the
// same test would find nothing to reject and would accept a testing candidate
// GitHub had correctly flagged.
func TestStableSelectionUsesTheFlagForNonLegacyTags(t *testing.T) {
	for _, tc := range []struct {
		name       string
		tag        string
		prerelease bool
		want       bool
		why        string
	}{
		{
			name: "a legacy beta the flag missed",
			tag:  "v4.0.0-beta.25", prerelease: false, want: false,
			why: "an older cut published before the workflow set the flag must still not be taken as stable",
		},
		{
			name: "a legacy beta the flag caught",
			tag:  "v4.0.0-beta.25", prerelease: true, want: false,
			why: "refused either way",
		},
		{
			name: "a legacy stable",
			tag:  "v4.0.0", prerelease: false, want: true,
			why: "the historical form it has always been",
		},
		{
			name: "an unprefixed version the flag marks testing",
			tag:  "4.0.0", prerelease: true, want: false,
			why: "no hyphen to find, so the explicit flag is what decides",
		},
		{
			name: "an unprefixed version the flag calls released",
			tag:  "4.0.0", prerelease: false, want: true,
			why: "the flag is the authority for a form that carries no hyphen",
		},
		{
			name: "a product tag",
			tag:  "release/4.0.0", prerelease: false, want: false,
			why: "the panel cannot identify a product-scheme release yet; refusing is honest, not a regression",
		},
		{
			// The exemption is scoped to the product NAMESPACE, not to "no v".
			// A tag in neither scheme keeps the fail-safe test, because for an
			// unrecognised form the characters are all there is to go on.
			name: "an rc in neither scheme",
			tag:  "4.0.0-rc.1", prerelease: false, want: false,
			why: "not the product namespace, so the hyphen still means pre-release",
		},
		{
			name: "nothing at all",
			tag:  "", prerelease: false, want: false,
			why: "an empty tag is never a stable release",
		},
		{
			name: "not a version",
			tag:  "latest", prerelease: false, want: false,
			why: "unparseable",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, ok := acceptLatestPSPStable(tc.tag, tc.prerelease)
			if ok != tc.want {
				t.Fatalf("acceptLatestPSPStable(%q, %v) = %v, want %v — %s", tc.tag, tc.prerelease, ok, tc.want, tc.why)
			}
		})
	}
}
