package version

import "testing"

// AN EDGE IS A PATH, NOT A RANGE.
//
// The whole reason the edge model exists is that "the target is a release this
// panel lists" and "somebody checked this particular upgrade" are different
// claims, and only the second can be made. These cases pin that difference,
// because the tempting shortcut — "any edge whose From matches" or "any edge
// into this target" — passes every test written against a single edge.
func TestUpgradeEdgeMatchesBothEndsExactly(t *testing.T) {
	prior := ActiveUpgradeEdges()
	t.Cleanup(func() { SetActiveUpgradeEdges(prior) })

	SetActiveUpgradeEdges([]UpgradeEdge{
		{ID: "a", From: "v0.0.1-beta2", To: "v0.0.1-beta3"},
		{ID: "b", From: "v0.0.1-beta3", To: "v0.0.1-beta4"},
	})

	for _, tc := range []struct {
		name     string
		from, to string
		want     bool
	}{
		{"the edge itself", "v0.0.1-beta2", "v0.0.1-beta3", true},
		{"the other edge", "v0.0.1-beta3", "v0.0.1-beta4", true},
		// A path THROUGH a verified pair is not itself verified. Composing two
		// edges into beta2→beta4 would claim a route nobody walked.
		{"composed across two edges", "v0.0.1-beta2", "v0.0.1-beta4", false},
		// The same ends reversed are a different direction of travel.
		{"reversed", "v0.0.1-beta3", "v0.0.1-beta2", false},
		{"a source with no edge out", "v0.0.1-beta5", "v0.0.1-beta4", false},
		{"a target with no edge in", "v0.0.1-beta2", "v0.0.1-beta9", false},
		// Exact means exact: a spelling that is not the one published is not
		// silently matched, or the check would answer a question the reviewer
		// never answered.
		{"a non-canonical spelling", "0.0.1-beta2", "v0.0.1-beta3", false},
		{"empty source", "", "v0.0.1-beta3", false},
		{"empty target", "v0.0.1-beta2", "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := UpgradeEdgeVerified(tc.from, tc.to); got != tc.want {
				t.Fatalf("UpgradeEdgeVerified(%q, %q) = %t, want %t", tc.from, tc.to, got, tc.want)
			}
		})
	}
}

// The weaker question a LIST asks, and why it must be weaker.
func TestHasUpgradeEdgeFromAnswersOnlyTheSourceEnd(t *testing.T) {
	prior := ActiveUpgradeEdges()
	t.Cleanup(func() { SetActiveUpgradeEdges(prior) })

	SetActiveUpgradeEdges([]UpgradeEdge{{ID: "a", From: "v0.0.1-beta2", To: "v0.0.1-beta3"}})

	if !HasUpgradeEdgeFrom("v0.0.1-beta2") {
		t.Fatal("a source with a verified edge out must report one")
	}
	if HasUpgradeEdgeFrom("v0.0.1-beta3") {
		t.Fatal("a source with no edge out reported one")
	}
	if HasUpgradeEdgeFrom("") {
		t.Fatal("an empty source reported an edge")
	}
}

// NO EDGES PUBLISHED IS THE ANSWER "NONE ARE VERIFIED", not an error and not a
// default that lets everything through. Every manifest published so far is in
// this state, so it is the one worth pinning.
func TestNoPublishedEdgesVerifiesNothing(t *testing.T) {
	prior := ActiveUpgradeEdges()
	t.Cleanup(func() { SetActiveUpgradeEdges(prior) })

	SetActiveUpgradeEdges(nil)
	if UpgradeEdgeVerified("v0.0.1-beta2", "v0.0.1-beta3") {
		t.Fatal("an empty edge list verified a pair")
	}
	if HasUpgradeEdgeFrom("v0.0.1-beta2") {
		t.Fatal("an empty edge list offered a path out of a version")
	}
}

// The installed slice is COPIED, so a caller cannot mutate what is in force.
func TestInstalledEdgesAreCopied(t *testing.T) {
	prior := ActiveUpgradeEdges()
	t.Cleanup(func() { SetActiveUpgradeEdges(prior) })

	edges := []UpgradeEdge{{ID: "a", From: "v0.0.1-beta2", To: "v0.0.1-beta3"}}
	SetActiveUpgradeEdges(edges)
	edges[0].To = "v0.0.1-beta9"

	if UpgradeEdgeVerified("v0.0.1-beta2", "v0.0.1-beta9") {
		t.Fatal("mutating the caller's slice changed the edges in force")
	}
	if !UpgradeEdgeVerified("v0.0.1-beta2", "v0.0.1-beta3") {
		t.Fatal("the installed edge was lost")
	}
}
