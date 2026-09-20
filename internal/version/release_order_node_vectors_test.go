package version_test

import (
	"testing"

	"github.com/KazuhaHub/passwall-sub-panel/internal/version"
)

// THE VECTORS BELOW ARE THE NODE SIDE'S, and they are here because PSP and
// Passwall Node must rank the same pair the same way.
//
// The node validates the ordering again when an upgrade request reaches it
// (releaseid.CompareProductVersion, walked by PN's own vectors), and a
// disagreement is one of two failures: a request PSP will not send, or one the
// node rejects. The two implementations cannot share code — the node's
// comparator is internal to its module — so the agreement is asserted rather
// than assumed, and that is the only thing holding them together.
//
// THE LIST IS ASCENDING, so the expected result of every pair is the sign of
// (i - j). There are no separate expectations to drift from the data: a pair that
// moves in the list changes both sides of the assertion at once.
//
// IT USED TO BE THE LEGACY BETA LINE. Those vectors were the released history —
// beta1, beta2, beta3, beta9, beta11, the alpha/beta identifiers — and they
// pinned the dotless-prerelease rule that made beta11 rank above beta9. The
// scheme is gone, so what is pinned now is the order a product version obeys:
// plain integer segments, a missing trailing segment read as zero, and the
// optional fourth BUILD segment falling between its own base and the next patch.
var nodeReleaseOrderVectors = []string{
	"1.0.0",
	"4.0.0",
	"4.0.0.1", // a rebuild of 4.0.0, above it and below the next patch
	"4.0.1",
	"4.1.0",
	"4.1.0.1",
	"102.0.3",
	"102.1.0",
	"99999999999999999999.0.0", // past any representable integer: length, not value
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
