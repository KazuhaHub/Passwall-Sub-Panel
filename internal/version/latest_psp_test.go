package version

import "testing"

func TestPSPBehindStable(t *testing.T) {
	cases := []struct {
		current, latest string
		want            bool
	}{
		{"v3.6.4", "v3.7.0", true},             // older stable base
		{"v3.7.0", "v3.7.0", false},            // same stable
		{"v3.7.1", "v3.7.0", false},            // ahead
		{"v3.7.0-beta.16", "v3.7.0", true},     // beta behind its stable (the key case)
		{"v3.7.0-beta.16", "v3.6.4", false},    // beta ahead of latest stable
		{"v3.8.0-beta.1", "v3.7.0", false},     // newer base, even as a beta → not behind
		{"v3.6.4-beta.2", "v3.7.0", true},      // older base beta
		{"dev", "v3.7.0", false},               // dev build never nagged
		{"v3.7.0", "", false},                  // no latest yet
		{"", "v3.7.0", false},                  // unknown current
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
