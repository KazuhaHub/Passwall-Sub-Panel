package version

import "strings"

// Release order compares numeric segments, then beta.N < rc.N < stable.
// The historical fourth segment sorts above its three-segment base and below
// the next patch. Comparisons do not overflow on large numeric inputs.

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
	abase, apre, _ := strings.Cut(a, "-")
	bbase, bpre, _ := strings.Cut(b, "-")
	left, right := strings.Split(abase, "."), strings.Split(bbase, ".")
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
	if apre == bpre {
		return 0
	}
	if apre == "" {
		return 1
	}
	if bpre == "" {
		return -1
	}
	astage, anum, _ := strings.Cut(apre, ".")
	bstage, bnum, _ := strings.Cut(bpre, ".")
	if astage != bstage {
		return strings.Compare(astage, bstage)
	}
	return compareNumericDigits(anum, bnum)
}

func validPrerelease(pre string) bool {
	stage, number, ok := strings.Cut(pre, ".")
	if !ok || (stage != "beta" && stage != "rc") {
		return false
	}
	n, ok := canonicalSegment(number)
	return ok && n > 0
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
