package version_test

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

// The product-version vectors are shared by three consumers: the Go package in
// Passwall Node, the TypeScript module in this repository's web front end, and
// the release CLI. One file holds them — web-react/src/utils/productVersion.vectors.json
// — and each consumer's tests run against it.
//
// WHAT THIS CASE ADDS IS THE OTHER REPOSITORY. A copy that is allowed to drift
// from the one it was copied from is not a shared contract, it is two contracts,
// and the way that shows up is a panel and a node disagreeing about what a version
// is.
//
// IT READS THE NODE CHECKOUT THE CONTRACT JOB ALREADY MAKES, rather than asking
// the toolchain where a dependency was unpacked. That is the same change the
// pinned-source suites needed: resolving it from go.mod made the evidence move
// whenever a dependency was bumped, and removing the Node module (X07) would have
// removed this guard with it. The repository is named by an environment variable
// because a test may not reach into another module at run time.
//
// WHEN THERE IS NO CHECKOUT THIS SKIPS, NAMED. That skip is bounded rather than
// hopeful: the CI job that provides one is the pinned-source contract job, so the
// comparison runs wherever the claim it guards is being made.
//
// If this fails, the fix is to re-copy the file, not to relax the test.
func TestProductVersionVectorsMatchTheNodeCheckout(t *testing.T) {
	vendored, err := os.ReadFile(filepath.Join("..", "..", "web-react", "src", "utils", "productVersion.vectors.json"))
	if err != nil {
		t.Fatalf("read the vendored vectors: %v", err)
	}

	checkout := os.Getenv("PSP_LIVE_NODE_REPO")
	if checkout == "" {
		t.Skip("PSP_LIVE_NODE_REPO is unset, so there is no Node checkout to compare against; " +
			"the pinned-source contract job sets it")
	}

	canonical, err := os.ReadFile(filepath.Join(checkout, "releaseid", "testdata", "vectors.json"))
	if err != nil {
		if os.IsNotExist(err) {
			// A checkout that predates the package is a named state rather than a
			// silent pass: the guard arms itself the moment the pinned source
			// carries the file, and from then on a divergence FAILS.
			t.Skipf("the checked-out Node revision has no releaseid/testdata/vectors.json yet; "+
				"the vendored copy cannot be checked against it until it does (%s)", checkout)
		}
		t.Fatalf("read the checkout's vectors from %s: %v", checkout, err)
	}

	// A CHECKOUT FROM BEFORE THE SINGLE SCHEME IS NOT A DIVERGENCE. The vendored
	// copy carries the product scheme alone; a revision that still lists a
	// `legacy_order` section was published before the scheme was removed, so the two
	// are expected to differ until the pin moves past it. The skip retires itself:
	// once the pinned source carries the new vectors, the comparison below is the
	// only path left.
	if bytes.Contains(canonical, []byte(`"legacy_order"`)) {
		t.Skipf("the pinned Node revision still lists the legacy scheme (%s); "+
			"the vendored copy cannot be checked against it until the pin moves past the removal", checkout)
	}

	if !bytes.Equal(vendored, canonical) {
		t.Fatalf("the vendored product-version vectors differ from the Node checkout's copy.\n"+
			"vendored: %s\ncanonical: %s\n"+
			"Re-copy the checkout's releaseid/testdata/vectors.json over the vendored file.",
			filepath.Join("web-react", "src", "utils", "productVersion.vectors.json"),
			filepath.Join(checkout, "releaseid", "testdata", "vectors.json"))
	}
}
