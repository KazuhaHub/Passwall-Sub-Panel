package version

import (
	"strconv"
	"strings"
)

// RELEASE VERSIONS AND THEIR MAJOR.
//
// A version here is a RELEASE version in either scheme: the legacy v-prefixed
// form Node has published for its whole history, and the product form of three
// integers. A tag is not a version — `release/4.0.0` is an address, `4.0.0` is
// the thing — and neither is a channel name or a branch.
//
// THIS IS A LOCAL IMPLEMENTATION OF A SHARED RULE. The authority is releaseid
// in github.com/KazuhaHub/passwall-node; PSP adopts it directly once the version
// this module pins carries it, and this file then disappears. It exists because
// the rule PSP could reach was the INSTALLER's, which answers "can an installer
// place this version" and knows only the legacy shape — so it refused every
// three-integer version, and the refusals were silent: a Node release catalog
// that returns nil, and a reviewed release list that filters to empty.
//
// STRICT ON PURPOSE. One caller guards a GitHub API URL path, so a string that
// could be read as a path separator must be refused rather than escaped.

// MaxVersionSegmentBounds the segments. It matches the ceiling every other
// implementation uses; a version beyond it is a different format, not a longer
// one.
//
// IT APPLIES TO BOTH SCHEMES HERE, WHICH IS NARROWER THAN THE AUTHORITY. The
// released rule checks the legacy shape with a regex, and a regex does not do
// arithmetic — so it accepts a legacy version whose major is larger than any int
// while the product rule refuses one past the ceiling. PSP cannot copy that: it
// returns a MAJOR, and a major it cannot represent is not a version it can act
// on. The divergence is named here and pinned by a test rather than left for a
// reader to discover from two implementations that disagree about one absurd
// string. The property the old behaviour was protecting — that comparing very
// large segments does not overflow — belongs to the comparator and is asserted
// there.
const MaxVersionSegmentBounds = 2147483647

// MajorOfRelease returns the release line of a release version, and whether the
// string is one at all.
//
// The two answers are one decision. A caller that read a major out of a string
// it had not checked would be acting on the major of a path attempt or of a tag.
func MajorOfRelease(value string) (int, bool) {
	body := value
	legacy := strings.HasPrefix(body, "v")
	if legacy {
		body = strings.TrimPrefix(body, "v")
	}
	// A prerelease exists in the legacy scheme only. The product scheme
	// distinguishes a candidate by its CHANNEL, so a product version carrying a
	// prerelease is not a version this project publishes.
	base := body
	if index := strings.IndexByte(body, '-'); index >= 0 {
		if !legacy {
			return 0, false
		}
		base = body[:index]
		if !canonicalPrerelease(body[index+1:]) {
			return 0, false
		}
	}
	segments := strings.Split(base, ".")
	if len(segments) != 3 {
		return 0, false
	}
	parsed := make([]int, 3)
	for i, segment := range segments {
		// Anything that is not a plain run of ASCII digits is refused here:
		// this is what keeps `+build`, a slash, and a non-ASCII digit out.
		n, ok := canonicalSegment(segment)
		if !ok {
			return 0, false
		}
		parsed[i] = n
	}
	// The legacy history begins at v0.0.1, so a zero major is a version there.
	// The product scheme starts at 1: no product release ever had a zero
	// release line, and accepting one would grant it a compatibility it never
	// earned.
	if !legacy && parsed[0] < 1 {
		return 0, false
	}
	return parsed[0], true
}

// IsReleaseVersion reports whether the string is a release version.
func IsReleaseVersion(value string) bool {
	_, ok := MajorOfRelease(value)
	return ok
}

func canonicalSegment(segment string) (int, bool) {
	if segment == "" {
		return 0, false
	}
	for i := 0; i < len(segment); i++ {
		if segment[i] < '0' || segment[i] > '9' {
			return 0, false
		}
	}
	// "01" is not a shorter way to write 1; it is a different string that a
	// published identity never contains.
	if len(segment) > 1 && segment[0] == '0' {
		return 0, false
	}
	n, err := strconv.Atoi(segment)
	if err != nil || n > MaxVersionSegmentBounds {
		return 0, false
	}
	return n, true
}

// canonicalPrerelease checks the legacy prerelease, segment by segment.
//
// A numeric segment must not carry a redundant leading zero — that is the same
// refusal the segments themselves get, applied to the identifiers, and it is
// what separates `v1.0.0-beta.1` from `v1.0.0-beta.01` at the point where they
// would otherwise be read as the same release.
func canonicalPrerelease(prerelease string) bool {
	if prerelease == "" {
		return false
	}
	for _, segment := range strings.Split(prerelease, ".") {
		if segment == "" {
			return false
		}
		numeric := true
		for i := 0; i < len(segment); i++ {
			if segment[i] < '0' || segment[i] > '9' {
				numeric = false
				break
			}
		}
		if numeric && len(segment) > 1 && segment[0] == '0' {
			return false
		}
	}
	return true
}

// ReleaseTagFor is the inverse: the tag a release with this version is
// published under.
//
// IT IS NOT ALWAYS THE VERSION. A legacy release is published under its version
// unchanged; a product release is published under `release/` + its version,
// because the namespace is what keeps a product tag from being mistaken for a
// Go module version — it is part of the ADDRESS, not part of the version.
//
// Deriving the tag instead of storing it beside the version keeps the reviewed
// records to one field and makes the two impossible to disagree about. A caller
// that put a version where a tag belongs would ask GitHub for a release that
// does not exist, and the 404 reads as "no such release" rather than "wrong
// identity".
func ReleaseTagFor(version string) (string, bool) {
	if strings.HasPrefix(version, "v") {
		if !IsReleaseVersion(version) {
			return "", false
		}
		return version, true
	}
	if !IsReleaseVersion(version) {
		return "", false
	}
	return ProductTagNamespace + version, true
}

// IsReleaseTag reports whether the string is a tag one of this project's
// releases could be published under.
//
// It refuses a v INSIDE the product namespace. That is not a legacy tag someone
// forgot to convert and not a product tag with a stray letter: it is a string
// that would be read as a product tag by one rule and a legacy one by another,
// and the two readings differ about which release it names.
func IsReleaseTag(tag string) bool {
	if strings.HasPrefix(tag, ProductTagNamespace) {
		body := strings.TrimPrefix(tag, ProductTagNamespace)
		if strings.HasPrefix(body, "v") {
			return false
		}
		return IsReleaseVersion(body)
	}
	return strings.HasPrefix(tag, "v") && IsReleaseVersion(tag)
}

// VersionOfReleaseTag is the other inverse: the version a tag names.
//
// A TAG ARRIVES FROM OUTSIDE. GitHub reports a release's tag_name, and callers
// here compare that against this build's version — which is a version. In the
// legacy scheme the two are the same string; in the product scheme they are
// not, and a comparison that skips this step is comparing a tag to a version
// and concluding there is nothing new. That failure is silent: the update nudge
// simply never appears.
func VersionOfReleaseTag(tag string) (string, bool) {
	if !IsReleaseTag(tag) {
		return "", false
	}
	// The namespace is an address, so it comes off. A legacy tag has none and
	// is its own version, which is what the historical scheme always meant.
	return strings.TrimPrefix(tag, ProductTagNamespace), true
}
