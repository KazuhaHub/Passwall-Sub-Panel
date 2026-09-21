package pndeps

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// PSP PRODUCTION DEPENDS ON NO PACKAGE OF THE NODE MODULE.
//
// X07's acceptance is exactly this, verified against the dependency closure rather
// than against go.mod, because a require line with no import behind it is not a
// dependency and an import satisfied by a local replace is not a buildable one.
//
// THIS STARTED AS A LIST THAT COULD ONLY SHRINK, and the list is gone rather than
// emptied. It held five packages across four removals — `protocol`, the Node
// release catalog, the agent-upgrade service, the installation contract with the
// two packages reachable only through it, and last the core catalog. Each time
// this guard failed on the stale entries before anything else noticed, which is
// what it was for. An empty allowlist and no allowlist are the same statement, and
// only one of them invites the next reader to add a line back.
//
// WHAT REPLACED THE DEPENDENCIES, so that a future reader knows where to look
// rather than reaching for the module again:
//
//	installation template  → a signed release asset, read by internal/adapters/pninstall
//	core catalog           → a signed release asset, read by internal/adapters/corecatalogdoc
//	release numbering rule → internal/version, checked against shared vectors
//	the wire contract      → github.com/KazuhaHub/passwall-protocol
const nodeModulePrefix = "github.com/KazuhaHub/passwall-node"

func TestProductionDependsOnNoNodePackage(t *testing.T) {
	for pkg := range productionNodeDependencies(t) {
		t.Errorf("production code imports a Node module package: %s", pkg)
	}
}

// A local `replace` would make every check in this file describe a different
// build than the one consumers get: the imports resolve here and nowhere else,
// and the residual surface is measured against a working copy. The plan names
// this directly — the module must build from a published revision with no local
// replace.
func TestTheNodeModuleIsNotReplacedLocally(t *testing.T) {
	text, err := os.ReadFile(filepath.Join(moduleRoot(t), "go.mod"))
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(string(text), "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "replace") && !strings.Contains(line, "=>") {
			continue
		}
		if strings.Contains(line, "passwall-node") {
			t.Fatalf("go.mod replaces the Node module locally, so nothing here describes a buildable published dependency: %s", line)
		}
	}
}

// productionNodeDependencies is the closure of PSP's production packages that
// reach the Node module.
//
// -test=false is what makes this a statement about what SHIPS. Test files may
// import the Node module freely — the pinned-source compatibility suites do,
// and that is a supported entry point rather than a dependency.
func productionNodeDependencies(t *testing.T) map[string]bool {
	t.Helper()
	// ./... rather than the two directories that exist today, so a new
	// top-level Go package cannot join the build outside this guard's view.
	out := runGoList(t, "list", "-deps", "-test=false", "./...")
	seen := map[string]bool{}
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, nodeModulePrefix) {
			seen[line] = true
		}
	}
	return seen
}

func runGoList(t *testing.T, args ...string) string {
	t.Helper()
	cmd := exec.Command("go", args...)
	// Package patterns are module-relative, and a test runs in its own package
	// directory. Asking the toolchain from the wrong directory asks about a
	// repository that is not this one.
	cmd.Dir = moduleRoot(t)
	// A workspace would let a local replace satisfy an import the published
	// module cannot, which is the difference between what this repository
	// builds and what a consumer of it builds.
	cmd.Env = append(cmd.Environ(), "GOWORK=off")
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("go %s: %v\n%s", strings.Join(args, " "), err, output)
	}
	return string(output)
}

// moduleRoot finds the module by walking up for go.mod rather than counting
// directory levels, which would silently point somewhere else if this package
// moved.
func moduleRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("no go.mod above the test directory; the dependency guard cannot describe this repository")
		}
		dir = parent
	}
}
