package version

import (
	"strconv"
	"strings"
)

// RELEASE VERSIONS AND THEIR MAJOR.
//
// A version here is a PRODUCT release version: three integers, optionally
// followed by a fourth BUILD component, with no prefix and no suffix. A tag is
// not a version — `v4.0.0` is an address, `4.0.0` is the thing — and neither is a
// channel name or a branch. A candidate release is distinguished by the CHANNEL
// it was published on, which is release metadata rather than a spelling of the
// version.
//
// THE ADDRESS WEARS A `v` NOW, AND READING IS WIDER THAN WRITING. Releases were
// published under `release/MAJOR.MINOR.PATCH[.BUILD]`, a namespace chosen so a
// product tag could not be read as a Go module version. Four were published that
// way — `release/4.0.0`, `release/4.0.1`, `release/4.0.1.1`, `release/4.0.1.2` —
// and a published tag is immutable, so those four addresses are fixed forever and
// still have to be read. Every release from here on is `vMAJOR.MINOR.PATCH[.BUILD]`,
// the shape the projects this one talks to have always used.
//
// SO A VERSION NO LONGER DETERMINES ONE ADDRESS. Deriving gives the CURRENT
// address: right for a release about to be published, wrong for one of the four
// that already were. Where an address has to be exact, it is carried — the panel
// states `release_tag` for every published release it lists
// (internal/ports.NodeReleaseCatalogEntry) rather than letting a caller re-derive
// it, and the release workflow reads the tag it was handed rather than building
// one from a version.
//
// STRICT ON PURPOSE. One caller guards a GitHub API URL path, so a string that
// could be read as a path separator must be refused rather than escaped — which
// is the historical namespace's doing, and the reason it stays a closed set of
// four rather than something new releases keep using.

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
	// READ THROUGH THE ONE PARSER. Splitting the segments here as well would be a
	// second place that decides what a version is, and the two would eventually
	// disagree about a shape neither was written for — the asymmetry that matters,
	// because this copy refusing what the publisher accepts would drop a release
	// from the panel's own view of itself.
	segments, ok := releaseSegments(value)
	if !ok {
		return 0, false
	}
	return segments[0], true
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

// ReleaseTagFor is the inverse: the tag a release with this version is published
// under.
//
// IT IS NEVER THE VERSION. `v` is part of the ADDRESS, not part of the name.
// Deriving the tag instead of storing it beside the version keeps the reviewed
// records to one field and makes the two impossible to disagree about. A caller
// that put a version where a tag belongs would ask GitHub for a release that does
// not exist, and the 404 reads as "no such release" rather than "wrong identity".
//
// IT ANSWERS FOR RELEASES THIS BUILD COULD PUBLISH, not for every release that
// exists: the four published under the historical namespace do not come back from
// here, and a caller addressing one of those has to carry its tag. Nothing about
// this function can tell the two apart — `4.0.1.2` is a version either way — so
// the choice belongs to the caller that knows which release it means.
func ReleaseTagFor(version string) (string, bool) {
	if !IsReleaseVersion(version) {
		return "", false
	}
	return ProductTagNamespace + version, true
}

// IsReleaseTag reports whether the string is a tag this project could have
// published a release under.
//
// A TAG OUTSIDE BOTH NAMESPACES IS NOT ONE OF OURS, whatever it looks like. A
// bare `4.0.0` is a version, not an address, and accepting it here would let a
// caller ask for a release at a path no publication ever writes to.
func IsReleaseTag(tag string) bool {
	_, ok := releaseTagBody(tag)
	return ok
}

// VersionOfReleaseTag is the other inverse: the version a tag names.
//
// A TAG ARRIVES FROM OUTSIDE. GitHub reports a release's tag_name, and callers
// here compare that against this build's version — which is a version. The two
// are never the same string, and a comparison that skips this step is comparing
// an address to a name and concluding there is nothing new. That failure is
// silent: the update nudge simply never appears.
func VersionOfReleaseTag(tag string) (string, bool) {
	return releaseTagBody(tag)
}

// releaseTagBody reads the version out of a tag under either namespace, and
// reports whether the tag is one of this project's at all.
//
// BOTH, BECAUSE HISTORY IS NOT NEGOTIABLE. A published tag cannot be moved, so the
// four under the historical namespace are read for as long as this project reads
// its own releases. The two namespaces cannot be confused for one another —
// neither is a prefix of the other — so the order they are tried in is not a
// decision.
//
// IT IS ALSO WHERE THE LEGACY BETA SHAPE IS REFUSED, without naming it: the body
// goes through the one version parser, and `0.0.1-beta9` is not a version — a
// hyphen is not a digit and a zero release line was never published.
func releaseTagBody(tag string) (string, bool) {
	for _, namespace := range []string{ProductTagNamespace, HistoricalTagNamespace} {
		body, found := strings.CutPrefix(tag, namespace)
		if !found {
			continue
		}
		if !IsReleaseVersion(body) {
			return "", false
		}
		return body, true
	}
	return "", false
}
