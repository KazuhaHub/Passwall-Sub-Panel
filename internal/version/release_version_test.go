package version_test

import (
	"testing"

	"github.com/KazuhaHub/passwall-sub-panel/internal/version"
)

// A release version, and the release line it belongs to.
//
// A VERSION IS THREE INTEGERS, optionally followed by a BUILD component, with no
// prefix and no suffix. A tag is not a version: `release/4.0.0` is an address,
// `4.0.0` is the thing, and the rule is strict about the difference because the
// same predicate guards a GitHub API URL path — anything that could be read as a
// path separator is refused rather than escaped.
//
// THE LEGACY SHAPE IS REFUSED, and it is refused HERE rather than in a comment.
// `v0.0.1-beta11` and the whole published history it names used to be accepted by
// this predicate, because Node releases in the field were all of that shape and
// the panel had to read them. The scheme was removed — nothing is deployed, so
// nothing needed migrating — and the rows below are the record of what that
// costs: every string this project has ever published under is now something the
// panel will not act on, and it says so in one place.
func TestReleaseVersions(t *testing.T) {
	for _, tc := range []struct {
		value string
		major int
		ok    bool
		why   string
	}{
		// Three integers, no prefix.
		{"4.0.0", 4, true, ""},
		{"102.1.0", 102, true, ""},
		{"4.0.1", 4, true, ""},
		{"1.0.0", 1, true, "the product line starts at one"},
		// The optional BUILD component. A version is never shorthand, so the
		// fourth segment is the only short form that is not one.
		{"4.0.0.1", 4, true, "a rebuild of 4.0.0"},
		{"4.0.1.12", 4, true, ""},
		// Near misses, and each of these was reachable before.
		{"", 0, false, ""},
		{"latest", 0, false, ""},
		{"main", 0, false, ""},
		{"4.0", 0, false, "three segments"},
		{"4", 0, false, "three segments"},
		{"4.0.0.1.2", 0, false, "three segments, or four with the build component"},
		{"04.0.0", 0, false, "leading zeroes"},
		{"4.04.0", 0, false, "leading zeroes"},
		{"0.0.0", 0, false, "the product line starts at one"},
		{"0.1.0", 0, false, "the product line starts at one"},
		{"4.0.0.0", 0, false, "a literal zero build is another spelling of the three-segment version"},
		{"4.0.0+build", 0, false, "build metadata"},
		{"4.0.0.1+build", 0, false, "build metadata"},
		{"4.0.0-beta.1", 0, false, "a candidate is a CHANNEL, not a spelling of the version"},
		// THE LEGACY SHAPE, in every form it was ever published under.
		{"v0.0.1-beta11", 0, false, "the shape every published legacy Node release had"},
		{"v0.0.1-beta.11", 0, false, "the dotted prerelease"},
		{"v1.0.0", 0, false, ""},
		{"v4.0.0", 0, false, "a PSP build stamp of the legacy line"},
		{"v4.0.0-beta.25", 0, false, "a PSP build stamp of the legacy line"},
		{"v102.1.0", 0, false, ""},
		{"v0.0.1", 0, false, ""},
		{"v1.0.0-alpha.01", 0, false, ""},
		// The path-injection cases the API URL guard depends on.
		{"v4.0.0/../../PRIVATE_RESPONSE", 0, false, "a path separator"},
		{"4.0.0/../../PRIVATE_RESPONSE", 0, false, "a path separator"},
		{"..", 0, false, ""},
		{"4.0.0/", 0, false, ""},
		{"release/4.0.0", 0, false, "a tag is not a version"},
		{"999999999999999999999999999.0.0", 0, false, "out of range"},
	} {
		t.Run(tc.value, func(t *testing.T) {
			if got := version.IsReleaseVersion(tc.value); got != tc.ok {
				t.Errorf("IsReleaseVersion(%q) = %v, want %v (%s)", tc.value, got, tc.ok, tc.why)
			}
			// The major and the predicate are one decision, so they cannot
			// disagree: a string that is a version has a major, and one that is
			// not has none. A caller that read the major without the predicate
			// would act on the major of a path-injection attempt.
			major, ok := version.MajorOfRelease(tc.value)
			if ok != tc.ok {
				t.Fatalf("MajorOfRelease(%q) accepted = %v but IsReleaseVersion = %v", tc.value, ok, tc.ok)
			}
			if tc.ok && major != tc.major {
				t.Errorf("MajorOfRelease(%q) = %d, want %d", tc.value, major, tc.major)
			}
		})
	}
}

// The inverse: the tag a release with this version is published under.
//
// The tag is what a URL and a git ref are addressed by, and it is NEVER the
// version — the namespace exists so a product tag cannot be mistaken for a Go
// module version, and it is part of the ADDRESS rather than part of the version.
// A caller that put the version where the tag belongs asks GitHub for a release
// that does not exist, and the 404 reads as "no such release" rather than "wrong
// identity". Deriving it rather than storing it keeps one field in the reviewed
// records and makes the two impossible to disagree about.
func TestTheTagAVersionIsPublishedUnder(t *testing.T) {
	for _, tc := range []struct {
		version string
		tag     string
		why     string
	}{
		{"4.0.0", "release/4.0.0", "the product namespace is part of the address, not of the version"},
		{"102.1.0", "release/102.1.0", ""},
		{"4.0.0.1", "release/4.0.0.1", "the build component travels with the version it names"},
	} {
		t.Run(tc.version, func(t *testing.T) {
			tag, ok := version.ReleaseTagFor(tc.version)
			if !ok {
				t.Fatalf("ReleaseTagFor(%q) refused a release version", tc.version)
			}
			if tag != tc.tag {
				t.Errorf("ReleaseTagFor(%q) = %q, want %q (%s)", tc.version, tag, tc.tag, tc.why)
			}
			// The derived tag has to be one this package would accept as a tag,
			// or a caller would be building a URL out of something the tag rule
			// itself refuses.
			if !version.IsReleaseTag(tag) {
				t.Errorf("ReleaseTagFor(%q) produced %q, which is not a release tag", tc.version, tag)
			}
		})
	}

	// A string that is not a release version has no tag. Deriving one from a
	// malformed input is how a path gets built out of something that was never
	// an identity — and a legacy version has none, because there is no address
	// this project publishes it at any more.
	for _, value := range []string{
		"", "latest", "4.0", "04.0.0", "release/4.0.0", "v4.0.0/../../PRIVATE_RESPONSE",
		"4.0.0-beta.1", "v0.0.1-beta11", "v1.0.0", "4.0.0.0",
	} {
		if tag, ok := version.ReleaseTagFor(value); ok {
			t.Errorf("ReleaseTagFor(%q) = %q, want a refusal", value, tag)
		}
	}
	// And a tag that is not one of ours is not read as a release. The middle four
	// are the ones a path-injection attempt produces.
	for _, tag := range []string{
		"", "4.0.0", "v4", "release/", "release/4.0", "release/v4.0.0", "release/4.0.0/../../x", "nightly",
		"v4.0.0", "v0.0.1-beta11", "release/4.0.0.0",
	} {
		if version.IsReleaseTag(tag) {
			t.Errorf("IsReleaseTag(%q) = true, want false", tag)
		}
	}
}

// The other inverse: the version a tag names.
//
// Needed wherever a TAG arrives from outside — GitHub reports a release's
// tag_name, and the update nudge compares that against this build's version. The
// two are never the same string, so a comparison that skips this step is
// comparing an address to a name and silently finding no update.
func TestTheVersionATagNames(t *testing.T) {
	for _, tc := range []struct {
		tag     string
		version string
	}{
		{"release/4.0.0", "4.0.0"},
		{"release/102.1.0", "102.1.0"},
		{"release/4.0.0.1", "4.0.0.1"},
	} {
		t.Run(tc.tag, func(t *testing.T) {
			got, ok := version.VersionOfReleaseTag(tc.tag)
			if !ok {
				t.Fatalf("VersionOfReleaseTag(%q) refused a release tag", tc.tag)
			}
			if got != tc.version {
				t.Errorf("VersionOfReleaseTag(%q) = %q, want %q", tc.tag, got, tc.version)
			}
			// And the round trip, so a caller cannot address one release and
			// compare against another.
			back, ok := version.ReleaseTagFor(got)
			if !ok || back != tc.tag {
				t.Errorf("ReleaseTagFor(%q) = %q, %v; the round trip must be lossless", got, back, ok)
			}
		})
	}
	// A LEGACY TAG IS NOT A TAG THIS PROJECT HAS. Its namespace is the version
	// itself, which is the property that made it unrecognisable as an address the
	// moment the version shape narrowed.
	for _, tag := range []string{
		"", "4.0.0", "v4", "release/4.0", "release/v4.0.0", "nightly",
		"v0.0.1-beta11", "v1.0.0", "v4.0.0", "release/4.0.0.0",
	} {
		if got, ok := version.VersionOfReleaseTag(tag); ok {
			t.Errorf("VersionOfReleaseTag(%q) = %q, want a refusal", tag, got)
		}
	}
}
