package version

import "strings"

// CompareNodeRelease orders two released Passwall Node versions and returns
// -1, 0 or +1.
//
// IT MUST AGREE WITH THE NODE SIDE. Passwall Node validates the same pair when
// the upgrade request reaches it (internal/upgrade.CompareVersions), and the two
// implementations cannot share code — that package is internal to the node
// module. A disagreement is a request PSP submits and the node refuses, or the
// reverse, so both sides implement one rule; the vectors shared with the node
// live in TestCompareNodeReleaseMatchesTheNodeRule.
//
// The rule is NOT plain semver. Semver compares a prerelease identifier
// character by character, so "beta11" ranks BELOW "beta9" ('1' < '9'), and this
// project publishes dotless prerelease tags. Comparing with semver refused
// v0.0.1-beta9 -> v0.0.1-beta11 as "target must be newer than its expected
// current version", while the project's own release order puts beta11 above
// beta9. The digit run after a shared alphabetic prefix is compared NUMERICALLY.
//
// Numeric identifiers are compared by length before content so a number no int
// can hold cannot overflow the comparison.
//
// Inputs are assumed canonical tags already checked by
// deployment.ValidReleaseVersion; this is an ordering, not a validator.
func CompareNodeRelease(a, b string) int {
	left, lp, _ := strings.Cut(strings.TrimPrefix(a, "v"), "-")
	right, rp, _ := strings.Cut(strings.TrimPrefix(b, "v"), "-")
	ls, rs := strings.Split(left, "."), strings.Split(right, ".")
	for i := 0; i < len(ls) && i < len(rs); i++ {
		if c := compareNumeric(ls[i], rs[i]); c != 0 {
			return c
		}
	}
	if len(ls) != len(rs) {
		if len(ls) < len(rs) {
			return -1
		}
		return 1
	}
	// A release sorts above its own prereleases ("v1.0.0" > "v1.0.0-beta1").
	if lp == rp {
		return 0
	}
	if lp == "" {
		return 1
	}
	if rp == "" {
		return -1
	}
	lpParts, rpParts := strings.Split(lp, "."), strings.Split(rp, ".")
	for i := 0; i < len(lpParts) && i < len(rpParts); i++ {
		ln, rn := allDigits(lpParts[i]), allDigits(rpParts[i])
		if ln != rn {
			// A numeric identifier always ranks below an alphanumeric one.
			if ln {
				return -1
			}
			return 1
		}
		var c int
		if ln {
			c = compareNumeric(lpParts[i], rpParts[i])
		} else {
			c = comparePrereleaseIdentifier(lpParts[i], rpParts[i])
		}
		if c != 0 {
			return c
		}
	}
	switch {
	case len(lpParts) < len(rpParts):
		return -1
	case len(lpParts) > len(rpParts):
		return 1
	}
	return 0
}

// comparePrereleaseIdentifier orders two prerelease identifiers the way a reader
// would: a shared alphabetic prefix first, then the number after it compared
// NUMERICALLY.
//
// A plain strings.Compare gets this backwards for the identifiers this project
// actually publishes — "beta9" against "beta11" compares '9' > '1' and returns
// one, putting the older release above the newer. The dotted form ("alpha.10")
// never showed it, because splitting on "." leaves "10" fully numeric and it
// takes the numeric path.
func comparePrereleaseIdentifier(a, b string) int {
	ap, an := splitTrailingDigits(a)
	bp, bn := splitTrailingDigits(b)
	if c := strings.Compare(ap, bp); c != 0 {
		return c
	}
	if an == "" || bn == "" {
		// One carries a numeric suffix the other does not. Comparing the whole
		// identifiers keeps "alpha" below "alpha1", the same rule the caller
		// applies to a shorter identifier list.
		return strings.Compare(a, b)
	}
	return compareNumeric(an, bn)
}

// splitTrailingDigits separates an identifier into its leading text and its
// trailing run of digits. An identifier with no trailing digits returns the whole
// string and an empty suffix.
func splitTrailingDigits(s string) (string, string) {
	i := len(s)
	for i > 0 && s[i-1] >= '0' && s[i-1] <= '9' {
		i--
	}
	return s[:i], s[i:]
}

func allDigits(s string) bool { return s != "" && strings.Trim(s, "0123456789") == "" }

// compareNumeric orders two numeric strings by value, length first so a number
// too large for int64 still compares correctly.
func compareNumeric(a, b string) int {
	if len(a) != len(b) {
		if len(a) < len(b) {
			return -1
		}
		return 1
	}
	return strings.Compare(a, b)
}
