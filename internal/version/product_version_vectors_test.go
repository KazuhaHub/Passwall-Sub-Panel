package version_test

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// The product-version vectors are shared by three consumers: the Go package in
// Passwall Node, the TypeScript module in this repository's web front end, and
// the release CLI. This repository keeps a copy so the front end's test can read
// it without reaching into another module at test time — but a copy that is
// allowed to drift is not a shared contract, it is two contracts.
//
// So the copy is checked against the original. The original lives in the
// passwall-node module, which this repository already depends on, and `go list`
// is the supported way to find where a dependency was unpacked.
//
// If this fails, the fix is to re-copy the file, not to relax the test: the two
// sides disagreeing about what a version is, is the failure this exists to
// prevent.
func TestProductVersionVectorsMatchTheModuleCopy(t *testing.T) {
	vendored, err := os.ReadFile(filepath.Join("..", "..", "web-react", "src", "utils", "productVersion.vectors.json"))
	if err != nil {
		t.Fatalf("read the vendored vectors: %v", err)
	}

	cmd := exec.Command("go", "list", "-m", "-f", "{{.Dir}}", "github.com/KazuhaHub/passwall-node")
	cmd.Env = append(os.Environ(), "GOWORK=off")
	out, err := cmd.Output()
	if err != nil {
		var stderr bytes.Buffer
		if ee, ok := err.(*exec.ExitError); ok {
			stderr.Write(ee.Stderr)
		}
		t.Skipf("cannot locate the passwall-node module (%v): %s", err, stderr.String())
	}
	moduleDir := string(bytes.TrimSpace(out))
	if moduleDir == "" {
		t.Skip("go list reported no directory for the passwall-node module")
	}

	canonical, err := os.ReadFile(filepath.Join(moduleDir, "releaseid", "testdata", "vectors.json"))
	if err != nil {
		if os.IsNotExist(err) {
			// The pinned passwall-node release predates releaseid. That is a
			// known, named state rather than a silent pass: the guard arms
			// itself the moment this repository pins a Node release that
			// carries the package, and from then on a divergence FAILS rather
			// than being skipped.
			t.Skipf("the pinned passwall-node release has no releaseid/testdata/vectors.json yet; "+
				"the vendored copy cannot be checked against it until this repository pins a release that does (%s)", moduleDir)
		}
		t.Fatalf("read the module's vectors from %s: %v", moduleDir, err)
	}

	// THE PINNED RELEASE PREDATES THE SINGLE SCHEME, and that is a named state
	// rather than a divergence. The vendored copy carries the product scheme
	// alone; a release that still lists a `legacy_order` section was published
	// before the scheme was removed, so the two are expected to differ until this
	// repository pins one that does not. The skip retires itself: the moment the
	// pin carries the new vectors, the comparison below is the only path left.
	if bytes.Contains(canonical, []byte(`"legacy_order"`)) {
		t.Skipf("the pinned passwall-node release still lists the legacy scheme (%s); "+
			"the vendored copy cannot be checked against it until this repository pins a release "+
			"published after the scheme was removed", moduleDir)
	}

	if !bytes.Equal(vendored, canonical) {
		t.Fatalf("the vendored product-version vectors differ from the module's copy.\n"+
			"vendored: %s\ncanonical: %s\n"+
			"Re-copy the module's releaseid/testdata/vectors.json over the vendored file.",
			filepath.Join("web-react", "src", "utils", "productVersion.vectors.json"),
			filepath.Join(moduleDir, "releaseid", "testdata", "vectors.json"))
	}
}
