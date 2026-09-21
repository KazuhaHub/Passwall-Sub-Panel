package app

import "testing"

// THE CATALOG IS THE RELEASES THIS PROJECT PUBLISHED, so it takes no version and
// no stamp can disable it.
//
// This file used to be about which REVIEWED SET a build selected by its own major,
// and about a stamp with no canonical identity leaving the panel with no catalog
// at all. Both questions are gone with the reviewed set: the answer is the same for
// every build, and the only thing left to hold is that building the catalog does
// no work — the panel boot must not depend on GitHub being reachable.
func TestNodeReleaseCatalogConstructsWithoutNetwork(t *testing.T) {
	catalog, err := newNodeReleaseCatalog()
	if err != nil {
		t.Fatalf("local catalog construction failed: %v", err)
	}
	if catalog == nil {
		t.Fatal("the catalog is nil; the panel would report its release list as unavailable rather than empty")
	}
	// Construction is local. A List WOULD reach the network, which is why nothing
	// here calls it: the handler does, per request, under its own cache.
}
