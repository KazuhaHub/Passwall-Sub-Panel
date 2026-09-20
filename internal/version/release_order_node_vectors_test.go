package version_test

import (
	"testing"

	"github.com/KazuhaHub/passwall-sub-panel/internal/version"
)

// THE VECTORS BELOW ARE THE NODE SIDE'S, and they are here because PSP and
// Passwall Node must rank the same pair the same way.
//
// The node validates the ordering again when an upgrade request reaches it
// (PN's TestUpgradeVersionOrder walks this same list), and a disagreement is one
// of two failures: a request PSP will not send, or one the node rejects. The two
// implementations cannot share code — the node's comparator is internal to its
// module — so the agreement is asserted rather than assumed, and that is the only
// thing holding them together.
//
// THE LIST IS ASCENDING, so the expected result of every pair is the sign of
// (i - j). There are no separate expectations to drift from the data: a pair that
// moves in the list changes both sides of the assertion at once.
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

func TestTheReleaseOrderAgreesWithTheNodesRule(t *testing.T) {
	for i, a := range nodeReleaseOrderVectors {
		for j, b := range nodeReleaseOrderVectors {
			want := i - j
			got := version.CompareRelease(a, b)
			if (got < 0) != (want < 0) || (got > 0) != (want > 0) {
				t.Errorf("CompareRelease(%s, %s) = %d, want the sign of %d", a, b, got, want)
			}
			// Antisymmetric, or the node and the panel can each be right about
			// opposite orderings.
			if back := version.CompareRelease(b, a); (back < 0) != (got > 0) || (back > 0) != (got < 0) {
				t.Errorf("CompareRelease(%s, %s) = %d but the reverse is %d", a, b, got, back)
			}
		}
	}
}
