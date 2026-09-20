package version

import (
	"strconv"
	"strings"
)

// RELEASE VERSIONS AND THEIR MAJOR.
//
// A version here is a PRODUCT release version: three integers, optionally
// followed by a fourth BUILD component, with no prefix and no suffix. A tag is
// not a version — `release/4.0.0` is an address, `4.0.0` is the thing — and
// neither is a channel name or a branch. A candidate release is distinguished by
// the CHANNEL it was published on, which is release metadata rather than a
// spelling of the version.
//
// THE LEGACY SCHEME IS GONE FROM THIS FILE. It used to answer two shapes at once:
// the v-prefixed form Node published for its whole history, and the product form.
// Nothing is deployed, so nothing has to be migrated off the first one, and a
// rule that answers two shapes is a rule that has to keep answering both — with
// the prerelease comparison, the leading-v branch and the "a zero major is a
// version there" exception all carried for an audience of zero.
//
// STRICT ON PURPOSE. One caller guards a GitHub API URL path, so a string that
// could be read as a path separator must be refused rather than escaped.

// MaxVersionSegmentBounds the segments. It matches the ceiling every other
// implementation uses; a version beyond it is a different format, not a longer
// one.
//
// The property an absurdly long segment must not break — that comparing very
// large segments does not overflow — belongs to the comparator and is asserted
// there.
const MaxVersionSegmentBounds = 2147483647

// MajorOfRelease returns the release line of a release version, and whether the
// string is one at all.
//
// The two answers are one decision. A caller that read a major out of a string
// it had not checked would be acting on the major of a path attempt or of a tag.
func MajorOfRelease(value string) (int, bool) {
	segments := strings.Split(value, ".")
	// A VERSION IS NEVER SHORTHAND: three segments, or four with the optional
	// BUILD component. Short forms are padded for COMPARISON, never accepted as
	// an identity.
	if len(segments) != 3 && len(segments) != 4 {
		return 0, false
	}
	parsed := make([]int, len(segments))
	for i, segment := range segments {
		// Anything that is not a plain run of ASCII digits is refused here: this
		// is what keeps a leading `v`, `+build`, a slash, a hyphen and a
		// non-ASCII digit out — one check rather than a list of shapes to reject.
		n, ok := canonicalSegment(segment)
		if !ok {
			return 0, false
		}
		parsed[i] = n
	}
	// The product line starts at 1: no product release ever had a zero release
	// line, and accepting one would grant it a compatibility it never earned.
	if parsed[0] < 1 {
		return 0, false
	}
	// A LITERAL ZERO FOURTH IS ANOTHER SPELLING OF THE THREE-SEGMENT VERSION.
	// Accepting it would make this copy accept a string the authority rejects — a
	// divergence in the direction that lets PSP admit a version the publisher
	// cannot.
	if len(parsed) == 4 && parsed[3] == 0 {
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

// ReleaseTagFor is the inverse: the tag a release with this version is
// published under.
//
// IT IS NEVER THE VERSION. `release/` is part of the ADDRESS, not part of the
// name: the namespace is what keeps a product tag from being mistaken for a Go
// module version. Deriving the tag instead of storing it beside the version
// keeps the reviewed records to one field and makes the two impossible to
// disagree about. A caller that put a version where a tag belongs would ask
// GitHub for a release that does not exist, and the 404 reads as "no such
// release" rather than "wrong identity".
func ReleaseTagFor(version string) (string, bool) {
	if !IsReleaseVersion(version) {
		return "", false
	}
	return ProductTagNamespace + version, true
}

// IsReleaseTag reports whether the string is a tag this project could have
// published a release under.
//
// A TAG OUTSIDE THE NAMESPACE IS NOT ONE OF OURS, whatever it looks like. A bare
// `4.0.0` is a version, not an address, and accepting it here would let a
// caller ask for a release at a path no publication ever writes to.
func IsReleaseTag(tag string) bool {
	body, found := strings.CutPrefix(tag, ProductTagNamespace)
	if !found {
		return false
	}
	return IsReleaseVersion(body)
}

// VersionOfReleaseTag is the other inverse: the version a tag names.
//
// A TAG ARRIVES FROM OUTSIDE. GitHub reports a release's tag_name, and callers
// here compare that against this build's version — which is a version. The two
// are never the same string, and a comparison that skips this step is comparing
// an address to a name and concluding there is nothing new. That failure is
// silent: the update nudge simply never appears.
func VersionOfReleaseTag(tag string) (string, bool) {
	body, found := strings.CutPrefix(tag, ProductTagNamespace)
	if !found || !IsReleaseVersion(body) {
		return "", false
	}
	return body, true
}
