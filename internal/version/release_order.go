package version

import "strings"

// THE PROJECT'S RELEASE ORDER.
//
// It is NOT SemVer, and the difference is not cosmetic: SemVer compares a
// prerelease IDENTIFIER character by character, so `v0.0.1-beta11` ranks BELOW
// `v0.0.1-beta9`. This project published beta1..beta11, so the SemVer answer puts
// the older release first wherever the order matters — the upgrade list, where
// the first entry is the one presented as recommended.
//
// It is also NOT golang.org/x/mod/semver, which is the mistake this file was
// written to undo: that package returns ZERO for anything it cannot parse, and a
// product version has no v prefix. Zero means "equal", so a range check built on
// it answers "inside this range" for every range — a policy reaching a build it
// was never reviewed for. That is a fail-OPEN answer to the one question the
// policy exists to answer.
//
// The authority is releaseid in github.com/KazuhaHub/passwall-node, the same
// source the shape rule names; this is a local implementation until the module
// this repository pins carries it, and the released vectors are what hold it in
// place.

// CompareRelease orders two release versions, -1 / 0 / +1.
//
// The scheme is read from the strings, and the two rules are kept separate
// because they are not the same rule with a flag: a product version is three
// integers and cannot carry a prerelease, while a legacy version always could.
// Merging them would make one scheme inherit an assumption only ever true of the
// other.
func CompareRelease(a, b string) int {
	if !strings.HasPrefix(a, "v") && !strings.HasPrefix(b, "v") {
		return compareProductSegments(a, b)
	}
	return compareLegacyVersions(a, b)
}

// compareProductSegments orders two product versions: plain integer segments,
// never compared as text (0.0.10 is above 0.0.9).
//
// A MISSING TRAILING SEGMENT IS ZERO, which is what the released vectors require
// and what the migration plan states — comparison allows padding, while a
// RELEASE is always three segments. The two are different questions: a version
// that is not an identity still has to be ordered against one, and answering
// "these differ" for `102.1` against `102.1.0` would be an order nobody asked
// for.
func compareProductSegments(a, b string) int {
	left, right := strings.Split(a, "."), strings.Split(b, ".")
	width := len(left)
	if len(right) > width {
		width = len(right)
	}
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

// compareLegacyVersions orders two historical versions: numeric segments, then a
// release above its own prereleases, then the prerelease identifiers.
//
// An identifier with no dot is the shape this project actually published —
// "beta11" is ONE identifier, not "beta.11" — so the digits at its end are
// compared NUMERICALLY. Comparing "beta9" and "beta11" as text puts beta9 above
// beta11, which is how a real upgrade gets refused as a downgrade.
func compareLegacyVersions(a, b string) int {
	left, leftPre, _ := strings.Cut(strings.TrimPrefix(a, "v"), "-")
	right, rightPre, _ := strings.Cut(strings.TrimPrefix(b, "v"), "-")

	leftSegments, rightSegments := strings.Split(left, "."), strings.Split(right, ".")
	for i := 0; i < len(leftSegments) && i < len(rightSegments); i++ {
		if c := compareNumericDigits(leftSegments[i], rightSegments[i]); c != 0 {
			return c
		}
	}
	if len(leftSegments) != len(rightSegments) {
		return sign(len(leftSegments) - len(rightSegments))
	}

	switch {
	case leftPre == rightPre:
		return 0
	case leftPre == "":
		return 1 // a release outranks its own prereleases
	case rightPre == "":
		return -1
	}

	leftIDs, rightIDs := strings.Split(leftPre, "."), strings.Split(rightPre, ".")
	for i := 0; i < len(leftIDs) && i < len(rightIDs); i++ {
		leftNumeric, rightNumeric := allDigits(leftIDs[i]), allDigits(rightIDs[i])
		switch {
		case leftNumeric && rightNumeric:
			if c := compareNumericDigits(leftIDs[i], rightIDs[i]); c != 0 {
				return c
			}
		case leftNumeric != rightNumeric:
			// A numeric identifier ranks below an alphanumeric one.
			if leftNumeric {
				return -1
			}
			return 1
		default:
			if c := comparePrereleaseIdentifier(leftIDs[i], rightIDs[i]); c != 0 {
				return c
			}
		}
	}
	return sign(len(leftIDs) - len(rightIDs))
}

// comparePrereleaseIdentifier orders two alphanumeric identifiers the way a
// reader would: the shared leading text first, then the number after it as a
// number.
func comparePrereleaseIdentifier(a, b string) int {
	aPrefix, aDigits := splitTrailingDigits(a)
	bPrefix, bDigits := splitTrailingDigits(b)
	if c := strings.Compare(aPrefix, bPrefix); c != 0 {
		return c
	}
	if aDigits == "" || bDigits == "" {
		// One has a numeric suffix the other lacks; comparing whole identifiers
		// keeps "alpha" below "alpha1".
		return strings.Compare(a, b)
	}
	return compareNumericDigits(aDigits, bDigits)
}

func splitTrailingDigits(s string) (string, string) {
	i := len(s)
	for i > 0 && s[i-1] >= '0' && s[i-1] <= '9' {
		i--
	}
	return s[:i], s[i:]
}

func allDigits(s string) bool { return s != "" && strings.Trim(s, "0123456789") == "" }

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
