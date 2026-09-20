package version_test

import (
	"testing"

	"github.com/KazuhaHub/passwall-sub-panel/internal/version"
)

// THE PROJECT'S RELEASE ORDER, held to the released vectors.
//
// THIS IS NOT SemVer and NOT x/mod/semver, for two reasons that were both
// checked rather than assumed:
//
//   - SemVer compares a prerelease IDENTIFIER character by character, so
//     `v0.0.1-beta11` ranks BELOW `v0.0.1-beta9`. This project published
//     beta1..beta11 and its release order is numeric, so the SemVer answer puts
//     the older release first wherever the order matters — the upgrade list,
//     where the first entry is the one presented as recommended.
//   - x/mod/semver returns ZERO for anything it cannot parse. Verified:
//     `semver.Compare("4.0.0", "4.99.99")` is 0, as is `("4.0.0", "4.0.0")`.
//     Zero means "equal", so a comparison of two bare product versions built on
//     that package answers "equal" for every pair.
//
// WHERE THAT MATTERS, AND WHERE IT DOES NOT — this was checked too. The compat
// range lookups in compat_remote.go pass through canonicalPSPSemver first, which
// PREFIXES A v when the string has none, so they never hand a bare version to
// semver.Compare and are correct as written. upgrade.go does not: it compares
// the request's version against the node's expected version directly, so once
// the guard there accepts a product version, that comparison is the next thing
// to get wrong.
//
// The authority is releaseid in github.com/KazuhaHub/passwall-node, the same
// source the shape rule names; this is a local implementation until the module
// this repository pins carries it.
func TestTheReleaseOrderFromTheVectors(t *testing.T) {
	vectors := loadReleaseVectors(t)
	for _, tc := range vectors.Order {
		t.Run("product "+tc.A+" vs "+tc.B, func(t *testing.T) {
			if got := version.CompareRelease(tc.A, tc.B); got != tc.Cmp {
				t.Errorf("CompareRelease(%q, %q) = %d, want %d", tc.A, tc.B, got, tc.Cmp)
			}
			if got := version.CompareRelease(tc.B, tc.A); got != -tc.Cmp {
				t.Errorf("CompareRelease(%q, %q) = %d, want %d (antisymmetry)", tc.B, tc.A, got, -tc.Cmp)
			}
		})
	}
	for _, tc := range vectors.LegacyOrder {
		t.Run("legacy "+tc.A+" vs "+tc.B, func(t *testing.T) {
			if got := version.CompareRelease(tc.A, tc.B); got != tc.Cmp {
				t.Errorf("CompareRelease(%q, %q) = %d, want %d", tc.A, tc.B, got, tc.Cmp)
			}
			if got := version.CompareRelease(tc.B, tc.A); got != -tc.Cmp {
				t.Errorf("CompareRelease(%q, %q) = %d, want %d (antisymmetry)", tc.B, tc.A, got, -tc.Cmp)
			}
		})
	}
}

// The defect the vector above pins, stated as itself so a regression is read as
// one: SemVer orders these two the other way round, and that is the whole reason
// this function exists.
func TestTheDotlessPrereleaseIsNotComparedAsText(t *testing.T) {
	if got := version.CompareRelease("v0.0.1-beta11", "v0.0.1-beta9"); got <= 0 {
		t.Fatalf("beta11 vs beta9 = %d; a text comparison puts beta9 first", got)
	}
	if got := version.CompareRelease("v0.0.1-beta9", "v0.0.1-beta11"); got >= 0 {
		t.Fatalf("beta9 vs beta11 = %d", got)
	}
	// And a release outranks its own prereleases, which every scheme needs.
	if got := version.CompareRelease("v0.0.1", "v0.0.1-beta11"); got <= 0 {
		t.Fatalf("a release must outrank its prereleases, got %d", got)
	}
}

// A range check has to be able to say OUTSIDE. The primitive this replaced
// answers zero for a bare product version, and zero is "equal", so a check built
// directly on it would report every build as inside every range.
func TestARangeCheckCanTellABuildIsInsideOrOutside(t *testing.T) {
	inRange := func(v, lo, hi string) bool {
		return version.CompareRelease(v, lo) >= 0 && version.CompareRelease(v, hi) <= 0
	}
	if !inRange("4.0.0", "4.0.0", "4.99.99") {
		t.Fatal("4.0.0 must be inside 4.0.0–4.99.99")
	}
	if !inRange("4.99.99", "4.0.0", "4.99.99") {
		t.Fatal("the ceiling is inclusive")
	}
	if inRange("5.0.0", "4.0.0", "4.99.99") {
		t.Fatal("5.0.0 must be OUTSIDE 4.0.0–4.99.99; this is the answer a zero comparison gets wrong")
	}
	if inRange("3.9.2", "4.0.0", "4.99.99") {
		t.Fatal("3.9.2 must be outside")
	}
	// The same, in the legacy scheme, where the panel's own builds live today.
	if !inRange("v4.0.0-beta.25", "v4.0.0-beta.1", "v4.99.99") {
		t.Fatal("a prerelease inside its own line must be inside")
	}
	if inRange("v5.0.0", "v4.0.0", "v4.99.99") {
		t.Fatal("v5.0.0 must be outside")
	}
}

// Segments far past any int still compare correctly. This was asserted at a
// request-level guard before the shape rule bounded a segment, and the property
// belongs here: the comparator orders digits by length first, so it cannot
// overflow the way parsing both into integers would.
func TestVeryLargeSegmentsStillCompare(t *testing.T) {
	if got := version.CompareRelease("v100000000000000000000.0.0", "v99999999999999999999.0.0"); got <= 0 {
		t.Fatalf("a 21-digit major must be above a 20-digit one, got %d", got)
	}
	if got := version.CompareRelease("v99999999999999999999.0.0", "v100000000000000000000.0.0"); got >= 0 {
		t.Fatalf("and below it in the other direction, got %d", got)
	}
}
