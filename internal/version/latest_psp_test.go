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

// TestAcceptLatestPSPStableForMajor pins the gate that keeps a V3 panel from
// advertising a V4 release as its own update.
//
// The v4.0.1 row is the whole point. GitHub's "Latest release" is one repo-wide
// pointer shared by two unrelated release lines, so a V4 stable taking it is not
// a hypothetical — it is the normal outcome of V4 shipping. Before the gate that
// tag parsed cleanly, compared 3.9.4 < 4.0.1, and every deployed V3 panel would
// have offered an upgrade across a database-model boundary.
//
// The release/4.0.1 and v4.0.1.10 rows record WHY this was not already broken:
// both fail to parse (a "release/" prefix is not a version; parseSemver rejects
// more than three segments), so today's V4 tags are discarded before any
// comparison. That is incidental, not a defense — these rows exist so a future
// change to parseSemver that made them parseable would fail here rather than in
// the field.
func TestAcceptLatestPSPStableForMajor(t *testing.T) {
	const v3 = 3
	cases := []struct {
		name       string
		tag        string
		prerelease bool
		major      int
		wantTag    string
		wantOK     bool
	}{
		{"same major stable is accepted", "v3.9.5", false, v3, "v3.9.5", true},
		{"same major, older, still accepted (caller compares)", "v3.9.0", false, v3, "v3.9.0", true},
		{"V4 stable is REFUSED for a V3 build", "v4.0.1", false, v3, "", false},
		{"V4 minor bump is REFUSED for a V3 build", "v4.1.0", false, v3, "", false},
		{"V4 four-segment tag does not parse", "v4.0.1.10", false, v3, "", false},
		{"V4 product-namespace tag does not parse", "release/4.0.1", false, v3, "", false},
		{"prerelease flag refuses even a matching major", "v3.9.5", true, v3, "", false},
		{"prerelease suffix refuses even without the flag", "v3.9.5-beta.1", false, v3, "", false},
		{"empty tag", "", false, v3, "", false},
		{"major 0 disables the gate (dev build)", "v4.0.1", false, 0, "v4.0.1", true},
	}
	for _, c := range cases {
		gotTag, gotOK := acceptLatestPSPStableForMajor(c.tag, c.prerelease, c.major)
		if gotTag != c.wantTag || gotOK != c.wantOK {
			t.Errorf("%s: acceptLatestPSPStableForMajor(%q, %v, %d) = (%q, %v), want (%q, %v)",
				c.name, c.tag, c.prerelease, c.major, gotTag, gotOK, c.wantTag, c.wantOK)
		}
	}
}

// TestPickLatestStableForMajor covers the list fallback, which only runs once a
// V4 stable owns /releases/latest and a V3 panel has to find its own line by
// scanning instead.
//
// The ordering case is the one worth having: GitHub returns releases by creation
// date, so a backported v3.8.9 cut AFTER v3.9.3 sits earlier in the list while
// being the older version. Taking the first match would install it as "latest"
// and tell every v3.9.x panel it is up to date forever.
func TestPickLatestStableForMajor(t *testing.T) {
	list := []pspReleaseEntry{
		{TagName: "v4.0.1", Prerelease: false},              // newer major — must be ignored
		{TagName: "v4.0.2-beta.3", Prerelease: true},        // V4 beta
		{TagName: "v3.8.9", Prerelease: false},              // backport, created most recently of the v3 rows
		{TagName: "v3.9.4", Prerelease: false},              // the real answer
		{TagName: "v3.9.5-beta.1", Prerelease: true},        // v3 beta — not stable
		{TagName: "v3.9.3", Prerelease: false},              // older stable
		{TagName: "release/4.0.0", Prerelease: false},       // unparseable
		{TagName: "v3.9.9", Prerelease: false, Draft: true}, // draft must not win
	}
	gotTag, gotOK := pickLatestStableForMajor(list, 3)
	if !gotOK || gotTag != "v3.9.4" {
		t.Errorf("pickLatestStableForMajor = (%q, %v), want (\"v3.9.4\", true) — "+
			"highest v3 STABLE, not the first match and not a draft", gotTag, gotOK)
	}

	if _, ok := pickLatestStableForMajor(nil, 3); ok {
		t.Error("empty list must yield no answer, so a transient miss cannot erase a known-good tag")
	}
	if _, ok := pickLatestStableForMajor([]pspReleaseEntry{{TagName: "v4.0.1"}}, 3); ok {
		t.Error("a list holding only another-major releases must yield no answer")
	}
}
