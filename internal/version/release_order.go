package version

import "strings"

// THE PROJECT'S RELEASE ORDER.
//
// It is NOT golang.org/x/mod/semver, which is the mistake this file was written
// to undo: that package returns ZERO for anything it cannot parse, and a product
// version has no v prefix. Zero means "equal", so a range check built on it
// answers "inside this range" for every range — a policy reaching a build it
// was never reviewed for. That is a fail-OPEN answer to the one question the
// policy exists to answer.
//
// SemVer is not the rule either, and the reason this file once gave for that is
// gone. It compared prerelease identifiers character by character, so
// `v0.0.1-beta11` ranked BELOW `v0.0.1-beta9` — and this project published
// beta1..beta12, so the SemVer answer put the older release first. Nothing
// publishes that shape now: a version is three integers with no suffix, and a
// candidate release is a CHANNEL rather than a spelling. What remains is plain
// integer comparison.

// CompareRelease orders two release versions, -1 / 0 / +1.
//
// A MISSING TRAILING SEGMENT IS ZERO, which the released vectors require and the
// migration plan states — comparison allows padding, while a RELEASE is always
// three segments. The two are different questions: a version that is not an
// identity still has to be ordered against one, and answering "these differ" for
// `102.1` against `102.1.0` would be an order nobody asked for.
//
// Segments are never compared as text (0.0.10 is above 0.0.9).
func CompareRelease(a, b string) int {
	left, right := strings.Split(a, "."), strings.Split(b, ".")
	width := max(len(left), len(right))
	for i := 0; i < width; i++ {
		leftSegment, rightSegment := "0", "0"
		if i < len(left) {
			leftSegment = left[i]
		}
		if i < len(right) {
			rightSegment = right[i]
		}
		if c := compareNumericDigits(leftSegment, rightSegment); c != 0 {
			return c
		}
	}
	return 0
}

// compareNumericDigits orders two digit strings by value. Length first, so a
// number too large for an int compares correctly instead of overflowing.
func compareNumericDigits(a, b string) int {
	if len(a) != len(b) {
		return sign(len(a) - len(b))
	}
	return strings.Compare(a, b)
}

func sign(n int) int {
	switch {
	case n < 0:
		return -1
	case n > 0:
		return 1
	default:
		return 0
	}
}
