package version_test

import (
	"testing"

	"github.com/KazuhaHub/passwall-sub-panel/internal/version"
)

// A release version, in either scheme, and the major it belongs to.
//
// THE RULE COMES FROM PASSWALL NODE and this is a local implementation of it,
// which the extraction plan permits as long as the single source is named: the
// authority is releaseid in github.com/KazuhaHub/passwall-node, and PSP adopts
// it directly once the version this module pins carries it. Until then this
// exists because PSP cannot use the installer's rule for it — that rule answers
// "can an installer place this version", and it knows only the historical
// v-prefixed shape, so it refuses the product scheme's three integers. Every
// caller here asks "is this a version", which is a different question.
//
// THE MAJOR IS ZERO-OR-MORE, NOT ONE-OR-MORE. Every published Node release in
// the field is `v0.0.1-*`, so a rule that demanded a release line of at least
// one would refuse the entire history it exists to read. Where a caller needs
// more than the shape — a PSP build stamp must name a release line that exists
// — it says so itself rather than making the shape rule stricter for everyone.
//
// A tag is not a version, and this is strict about it: the same predicate
// guards a GitHub API URL path, so anything that could be read as a path
// separator is refused rather than escaped.
func TestReleaseVersionsInBothSchemes(t *testing.T) {
	for _, tc := range []struct {
		value string
		major int
		ok    bool
		why   string
	}{
		// The legacy scheme: a v, three segments, an optional prerelease. Node
		// releases in the field are all of this shape.
		{"v0.0.1-beta11", 0, true, "the shape every published Node release has"},
		{"v0.0.1-beta.11", 0, true, "the dotted prerelease"},
		{"v1.0.0", 1, true, ""},
		{"v4.0.0-beta.25", 4, true, "a PSP build stamp"},
		{"v102.1.0", 102, true, ""},
		// The product scheme: three integers, no prefix, no prerelease. A
		// candidate is distinguished by its CHANNEL, not by its version.
		{"4.0.0", 4, true, ""},
		{"102.1.0", 102, true, ""},
		{"4.0.1", 4, true, ""},
		// Near misses, and each of these was reachable before.
		{"", 0, false, ""},
		{"latest", 0, false, ""},
		{"main", 0, false, ""},
		{"v4", 0, false, "three segments"},
		{"4.0", 0, false, "three segments"},
		{"v04.0.0", 0, false, "leading zeroes"},
		{"04.0.0", 0, false, "leading zeroes"},
		{"0.0.0", 0, false, "the product scheme starts at a release line of one"},
		{"v4.0.0+build", 0, false, "build metadata"},
		{"4.0.0+build", 0, false, "build metadata"},
		{"4.0.0-beta.1", 0, false, "the product scheme has no prerelease"},
		{"v1.0.0-alpha.01", 0, false, "a redundant leading zero in a numeric prerelease segment"},
		// The path-injection cases the API URL guard depends on.
		{"v4.0.0/../../PRIVATE_RESPONSE", 0, false, "a path separator"},
		{"4.0.0/../../PRIVATE_RESPONSE", 0, false, "a path separator"},
		{"..", 0, false, ""},
		{"v4.0.0/", 0, false, ""},
		{"release/4.0.0", 0, false, "a tag is not a version"},
		{"v999999999999999999999999999.0.0", 0, false, "out of range"},
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
// The tag is what a URL and a git ref are addressed by, and it is NOT the
// version under the product scheme — the namespace exists so a product tag
// cannot be mistaken for a Go module version, and it is part of the ADDRESS
// rather than part of the version. A caller that put the version where the tag
// belongs asks GitHub for a release that does not exist, and the 404 reads as
// "no such release" rather than "wrong identity".
//
// Deriving it rather than storing it keeps one field in the reviewed records
// and makes the two impossible to disagree about.
func TestTheTagAVersionIsPublishedUnder(t *testing.T) {
	for _, tc := range []struct {
		version string
		tag     string
		why     string
	}{
		{"4.0.0", "release/4.0.0", "the product namespace is part of the address, not of the version"},
		{"102.1.0", "release/102.1.0", ""},
		{"v0.0.1-beta11", "v0.0.1-beta11", "a legacy release is published under its version, unchanged"},
		{"v1.0.0", "v1.0.0", ""},
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
	// an identity.
	for _, value := range []string{
		"", "latest", "4.0", "04.0.0", "release/4.0.0", "v4.0.0/../../PRIVATE_RESPONSE", "4.0.0-beta.1",
	} {
		if tag, ok := version.ReleaseTagFor(value); ok {
			t.Errorf("ReleaseTagFor(%q) = %q, want a refusal", value, tag)
		}
	}
	// And a tag that is not one of ours is not read as a release. The last four
	// are the ones a path-injection attempt produces.
	for _, tag := range []string{
		"", "4.0.0", "v4", "release/", "release/4.0", "release/v4.0.0", "release/4.0.0/../../x", "nightly",
	} {
		if version.IsReleaseTag(tag) {
			t.Errorf("IsReleaseTag(%q) = true, want false", tag)
		}
	}
}
