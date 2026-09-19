package version

import "testing"

// THE VECTORS BELOW ARE THE NODE SIDE'S. Passwall Node's
// TestUpgradeVersionOrder walks the same list, and the two implementations must
// agree on every pair: the node refuses an upgrade whose target it ranks at or
// below the source, so a disagreement is either a request PSP will not send or a
// request the node will reject.
//
// The list is ordered ascending, so the expected result of every pair is the
// sign of (i - j) — no separate expectations to drift from the data.
var nodeReleaseOrderVectors = []string{
	"v0.0.1-beta1",
	"v0.0.1-beta2",
	"v0.0.1-beta3",
	"v0.0.1-beta9",
	"v0.0.1-beta11",
	"v0.0.1",
	"v0.0.2-alpha.1",
	"v0.0.2-alpha.2",
	"v0.0.2-alpha.10",
	"v0.0.2-beta.1",
	"v0.0.2",
	"v1.0.0",
	"v10.0.0",
	"v9999999999999999999999.0.0",
}

func TestCompareNodeReleaseMatchesTheNodeRule(t *testing.T) {
	for i, a := range nodeReleaseOrderVectors {
		for j, b := range nodeReleaseOrderVectors {
			want := sign(i - j)
			if got := sign(CompareNodeRelease(a, b)); got != want {
				t.Errorf("CompareNodeRelease(%s, %s) = %d, want %d", a, b, got, want)
			}
			// The relation must be antisymmetric, or the node and the panel can
			// each be right about opposite orderings.
			if got := sign(CompareNodeRelease(b, a)); got != -want {
				t.Errorf("CompareNodeRelease(%s, %s) = %d, want %d", b, a, got, -want)
			}
		}
	}
}

func TestCompareNodeReleaseRanksThePublishedBetaTagsByNumber(t *testing.T) {
	// The dotless form is what this project publishes. beta9 and beta11 are real
	// tags, and a lexical or semver comparison puts beta11 first — the bug this
	// ordering exists to prevent.
	for _, tc := range []struct {
		target, current string
		want            int
	}{
		{"v0.0.1-beta11", "v0.0.1-beta9", 1},
		{"v0.0.1-beta9", "v0.0.1-beta11", -1},
		{"v0.0.1-beta10", "v0.0.1-beta9", 1},
		{"v0.0.1-beta9", "v0.0.1-beta9", 0},
		{"v0.0.1-beta9", "v0.0.1-beta8", 1},
		// A release outranks its own prereleases.
		{"v0.0.1", "v0.0.1-beta11", 1},
	} {
		if got := sign(CompareNodeRelease(tc.target, tc.current)); got != tc.want {
			t.Errorf("CompareNodeRelease(%s, %s) = %d, want %d", tc.target, tc.current, got, tc.want)
		}
	}
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
