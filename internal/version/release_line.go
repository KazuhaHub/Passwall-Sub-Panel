package version

import (
	"errors"
	"fmt"
	"strings"
)

// ALLOCATING A RELEASE NUMBER ON A LINE.
//
// THE RULE IS PORTED, NOT INVENTED. PSP used to run Passwall Node's allocator for
// this — `go run github.com/KazuhaHub/passwall-node/deployment/cmd/allocate-release-tag`
// in its release workflow — which made PSP's publication path a consumer of the
// Node module. Removing that dependency is what this file is for.
//
// IT IS NOT IN THE SHARED PROTOCOL MODULE, and that is a decision rather than an
// oversight: the protocol repository holds wire data and pure wire functions, and
// a numbering rule is neither. The shared TEST VECTORS remain the contract between
// this implementation, the Node one, and the front end.
//
// THE RULE ITSELF: a number, once bound to a source revision, is never reused; a
// failed build may leave a gap; an incremental fix takes the FOURTH segment, so
// the patch advances only when somebody names a new one; and a line's first
// release is NAMED rather than derived, because nothing in a repository knows
// whether the next release belongs on 4.0 or 4.1.
var (
	// ErrNotAReleaseLine means the string is not MAJOR.MINOR — including a string
	// that is a version, which is a different thing being passed where a line
	// belongs.
	ErrNotAReleaseLine = errors.New("release line: not MAJOR.MINOR")
	// ErrLineHasNoRelease means nothing on the line can be counted from.
	ErrLineHasNoRelease = errors.New("release line: no release to count from")
	// ErrVersionNotAllocated means no tag on the line points at this revision, so
	// there is no number to resume and one has to be allocated.
	ErrVersionNotAllocated = errors.New("release line: no number is bound to this revision")
	// ErrAmbiguousLineRevision means two tags on the line point at this revision.
	ErrAmbiguousLineRevision = errors.New("release line: two numbers are bound to this revision")
	// ErrLineSegmentRange means the build segment is at its ceiling, so the next
	// number does not exist.
	ErrLineSegmentRange = errors.New("release line: the build segment cannot go past its maximum")
)

// Line is a release line: the major and minor a maintainer names. It is NOT a
// version — a version is a published identity, and this is the naming decision
// that precedes one.
type Line struct {
	Major int
	Minor int
}

func (l Line) String() string { return fmt.Sprintf("%d.%d", l.Major, l.Minor) }

// ParseReleaseLine reads a release line in the one form it has: MAJOR.MINOR.
//
// A PATCH IS REJECTED RATHER THAN IGNORED. Someone passing "4.0.1" is naming a
// version, and quietly reading it as the line 4.0 would allocate a number nobody
// asked for. The segments go through the same rule a version's do, because a line
// is what every version on it starts with.
func ParseReleaseLine(value string) (Line, error) {
	parts := strings.Split(value, ".")
	if len(parts) != 2 {
		return Line{}, fmt.Errorf("%w: %q is not a release line, which is MAJOR.MINOR", ErrNotAReleaseLine, value)
	}
	major, ok := canonicalSegment(parts[0])
	if !ok {
		return Line{}, fmt.Errorf("%w: %q is not a release-line segment", ErrNotAReleaseLine, parts[0])
	}
	minor, ok := canonicalSegment(parts[1])
	if !ok {
		return Line{}, fmt.Errorf("%w: %q is not a release-line segment", ErrNotAReleaseLine, parts[1])
	}
	return Line{Major: major, Minor: minor}, nil
}

// AllocateVersion returns the next version on a line, given every tag that
// already names a release.
//
// A TAG IT CANNOT READ TAKES NO NUMBER. `release/4.0.oops` is not a release
// version, so it is not a release on this line either: it is skipped. Refusing
// the whole allocation instead would let one stray tag on the remote freeze every
// future release.
//
// ANOTHER LINE'S TAGS ARE IGNORED, because they have their own numbering.
//
// THE NEXT NUMBER IS A BUILD ON THE HIGHEST RELEASE, NOT A NEW PATCH — see the
// package note above. Counting fixes into the patch would make every fix look like
// a new patch release, which is the attribution the fourth segment exists to keep.
func AllocateVersion(line Line, existing []string) (string, error) {
	var highest [4]int
	found := false
	for _, raw := range existing {
		released, ok := VersionOfReleaseTag(raw)
		if !ok {
			continue
		}
		segments, ok := releaseSegments(released)
		if !ok {
			continue
		}
		if segments[0] != line.Major || segments[1] != line.Minor {
			continue
		}
		if !found || compareSegments(segments, highest) > 0 {
			highest, found = segments, true
		}
	}
	if !found {
		return "", fmt.Errorf("%w: line %s has no release on it, and the first version of a line is named rather than allocated", ErrLineHasNoRelease, line)
	}
	next := highest[3] + 1
	if next > MaxVersionSegmentBounds {
		return "", fmt.Errorf("%w: line %s is at %s and %d is past the maximum", ErrLineSegmentRange, line, versionString(highest), MaxVersionSegmentBounds)
	}
	return versionString([4]int{highest[0], highest[1], highest[2], next}), nil
}

// ResumeVersion returns the number already bound to this source revision, if one
// is.
//
// A RERUN CONTINUES ITS OWN NUMBER rather than taking another, or every retry
// burns a number and "a failed build may leave a gap" becomes an avalanche of
// them. What records the number is the revision itself: a tag points at a commit,
// so a rerun asks which tags point at the commit it is about to build.
//
// TWO CANDIDATES IS REFUSED RATHER THAN RESOLVED. Two tags on one commit, on one
// line, are two releases given the same source, and choosing between them would be
// this deciding which of somebody's releases does not count.
func ResumeVersion(line Line, onCommit []string) (string, error) {
	var candidates []string
	for _, raw := range onCommit {
		released, ok := VersionOfReleaseTag(raw)
		if !ok {
			continue
		}
		segments, ok := releaseSegments(released)
		if !ok {
			continue
		}
		if segments[0] != line.Major || segments[1] != line.Minor {
			continue
		}
		candidates = append(candidates, released)
	}
	switch len(candidates) {
	case 0:
		return "", fmt.Errorf("%w: nothing on line %s points at this revision", ErrVersionNotAllocated, line)
	case 1:
		return candidates[0], nil
	default:
		return "", fmt.Errorf("%w: %s all point at this revision on line %s", ErrAmbiguousLineRevision, strings.Join(candidates, ", "), line)
	}
}

// releaseSegments parses a release version into its four segments, the fourth
// being zero when it is absent. THE ONE PARSER: MajorOfRelease reads through it.
func releaseSegments(value string) ([4]int, bool) {
	parts := strings.Split(value, ".")
	if len(parts) != 3 && len(parts) != 4 {
		return [4]int{}, false
	}
	var parsed [4]int
	for i, part := range parts {
		n, ok := canonicalSegment(part)
		if !ok {
			return [4]int{}, false
		}
		parsed[i] = n
	}
	// The product line starts at 1: no product release ever had a zero release
	// line, and accepting one would grant it a compatibility it never earned.
	if parsed[0] < 1 {
		return [4]int{}, false
	}
	// A LITERAL ZERO FOURTH IS ANOTHER SPELLING OF THE THREE-SEGMENT VERSION.
	if len(parts) == 4 && parsed[3] == 0 {
		return [4]int{}, false
	}
	return parsed, true
}

// versionString renders segments back as a version, dropping a zero fourth.
func versionString(segments [4]int) string {
	if segments[3] == 0 {
		return fmt.Sprintf("%d.%d.%d", segments[0], segments[1], segments[2])
	}
	return fmt.Sprintf("%d.%d.%d.%d", segments[0], segments[1], segments[2], segments[3])
}

func compareSegments(a, b [4]int) int {
	for i := 0; i < 4; i++ {
		if a[i] != b[i] {
			if a[i] < b[i] {
				return -1
			}
			return 1
		}
	}
	return 0
}
