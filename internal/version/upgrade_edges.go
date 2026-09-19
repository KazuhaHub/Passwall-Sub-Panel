package version

import "sync/atomic"

// UpgradeEdge is one VERIFIED Node upgrade edge: a claim that upgrading a Node
// from From to To has been checked, rather than that To happens to be a release
// this panel knows about.
//
// The same shape is validated by deploy/compat/plan.mjs in
// docs/compat/verification-v1.json, which is where the claim is made and
// reviewed. It is republished in the runtime manifest because that is the only
// document a running PSP can read at decision time.
type UpgradeEdge struct {
	ID   string `json:"id"`
	From string `json:"from"`
	To   string `json:"to"`
}

// activeUpgradeEdges holds the edges from the manifest currently in force.
//
// EMPTY IS A POSITION, NOT AN OVERSIGHT. It says no edge has been verified, so
// no upgrade is recommended — which is the honest answer and the one the
// remediation plan asks for. A panel that recommended a target merely because it
// is a release it happens to list would be asserting a path nobody walked.
var activeUpgradeEdges atomic.Value // []UpgradeEdge

// SetActiveUpgradeEdges installs the verified edges from an applied manifest.
// The slice is copied so a caller cannot mutate what is in force after the fact.
func SetActiveUpgradeEdges(edges []UpgradeEdge) {
	activeUpgradeEdges.Store(append([]UpgradeEdge(nil), edges...))
}

// ActiveUpgradeEdges returns the edges in force. Nil means none have been
// published, which is the same answer as an empty list and is deliberate.
func ActiveUpgradeEdges() []UpgradeEdge {
	edges, _ := activeUpgradeEdges.Load().([]UpgradeEdge)
	return edges
}

// UpgradeEdgeVerified reports whether from→to is one of the edges in force.
//
// Matching is EXACT, both ends. A prefix or a range comparison would answer a
// different question than the one the edge records: "some edge starting here" is
// not the claim a reviewer signed off on. Versions are compared as published, so
// a caller that passes a non-canonical spelling gets "no" rather than a guess.
func UpgradeEdgeVerified(from, to string) bool {
	if from == "" || to == "" {
		return false
	}
	for _, edge := range ActiveUpgradeEdges() {
		if edge.From == from && edge.To == to {
			return true
		}
	}
	return false
}

// HasUpgradeEdgeFrom reports whether ANY verified edge starts at from.
//
// This answers the question a LIST can ask. A list does not name a target, so it
// cannot ask whether one particular edge is verified — but it can ask whether a
// verified path leaves the node's current version at all, which is the weaker
// fact that keeps the list from offering an action the service will refuse. The
// two questions are different on purpose, and the DTO carries which one was
// answered rather than flattening them into one boolean.
func HasUpgradeEdgeFrom(from string) bool {
	if from == "" {
		return false
	}
	for _, edge := range ActiveUpgradeEdges() {
		if edge.From == from {
			return true
		}
	}
	return false
}
