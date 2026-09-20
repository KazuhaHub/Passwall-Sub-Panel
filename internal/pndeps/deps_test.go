package pndeps

import (
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// The residual Passwall Node dependency surface, pinned so it can only SHRINK.
//
// X07's acceptance is that PSP production no longer depends on the Node module,
// verified with `go list -deps` and imports. A check performed once is a check
// that stops being true: during a migration the surface grows easily, one
// convenient import at a time, and each addition is invisible until the removal
// step — which is the step where it is most expensive to discover, because by
// then code has been written against it.
//
// So the allowed set is written down with the reason each entry is still there,
// and anything else fails. The list failing when it is merely STALE is also
// intended: an allowlist that outlives its entries is a description of a
// repository that no longer exists, and it is what the next reader would trust.
//
// Deleting entries from this map IS the migration. Each one has a named
// successor:
//
//	protocol     → github.com/KazuhaHub/passwall-protocol (extracted, X01–X06)
//	deployment   → the installation contract PSP consumes as an adapter (X07)
//	corecatalog  → the dynamically reviewed release policy (X07)
type residualDependency struct {
	// successor is what will replace this dependency, so the reason for the
	// entry is not "it is still here", which is not a reason.
	successor string
	// files is how many production files import it today. A dependency that
	// grows from 5 files to 40 is a different decision from one that stays at 5,
	// and the count is cheap to keep true.
	files int
}

var allowedResidual = map[string]residualDependency{
	"github.com/KazuhaHub/passwall-node/protocol": {
		successor: "github.com/KazuhaHub/passwall-protocol",
		files:     18,
	},
	"github.com/KazuhaHub/passwall-node/deployment": {
		successor: "a pinned, signed installation template consumed by a PSP adapter",
		files:     2,
	},
	"github.com/KazuhaHub/passwall-node/corecatalog": {
		successor: "the dynamically reviewed release policy",
		files:     5,
	},
	// Reached only through the Node packages above, never imported by PSP
	// directly: `deployment` uses it to render the connection environment. It
	// leaves when they do, and it cannot be removed on its own. It is listed
	// because `go list -deps` reports it, and a guard that ignored what the
	// tool reports would be describing a different dependency graph than the
	// one that exists.
	//
	// The surface has SHRUNK twice since this list was written: the Node release
	// catalog and the agent-upgrade service each stopped reaching for a rule the
	// installer owns, and both times this guard failed on the stale count before
	// anything else noticed. That is the property worth keeping — the number is
	// not documentation, it is a tripwire.
	"github.com/KazuhaHub/passwall-node/internal/nodeconfig": {
		successor: "leaves with deployment; not separately removable",
		files:     0,
	},
}

const nodeModulePrefix = "github.com/KazuhaHub/passwall-node"

func TestProductionDependsOnNoNewNodePackages(t *testing.T) {
	actual := productionNodeDependencies(t)

	for pkg := range actual {
		if _, ok := allowedResidual[pkg]; !ok {
			t.Errorf("production code imports a new Node package: %s", pkg)
		}
	}
	for pkg, entry := range allowedResidual {
		if _, ok := actual[pkg]; !ok {
			t.Errorf("%s is in the allowed residual set but is no longer imported; remove the entry so the list describes the repository that exists (successor: %s)", pkg, entry.successor)
		}
	}
	if t.Failed() {
		t.Log("the residual Node dependency surface may only shrink while the migration runs; a new import needs a decision, not an entry in the list")
	}
}

// The import-site counts are the part that catches growth INSIDE an allowed
// package. The package list stays the same while a codebase spreads its use,
// and "we only depend on three packages" stops being a useful statement.
func TestProductionImportSitesDoNotGrow(t *testing.T) {
	for pkg, entry := range allowedResidual {
		if entry.files == 0 {
			// Transitive entries have no import sites of their own.
			continue
		}
		got := countImportSites(t, pkg)
		if got > entry.files {
			t.Errorf("production import sites for %s grew to %d (was %d); the surface may only shrink. Successor: %s", pkg, got, entry.files, entry.successor)
		}
		if got < entry.files {
			t.Errorf("production import sites for %s dropped to %d (was %d); lower the number so it keeps meaning something", pkg, got, entry.files)
		}
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

// countImportSites counts PRODUCTION FILES that import the package.
//
// Files rather than packages, deliberately: spreading an existing dependency
// across more of an already-importing package is exactly the growth that leaves
// the package list unchanged, and "we depend on three packages" stops being a
// useful statement once each of them is everywhere.
func countImportSites(t *testing.T, pkg string) int {
	t.Helper()
	root := moduleRoot(t)
	count := 0
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			switch entry.Name() {
			case ".git", "vendor", "node_modules":
				return fs.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		file, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.ImportsOnly)
		if err != nil {
			// A file this cannot parse is a file the compiler cannot build, so
			// it is a real failure and not a reason to undercount.
			return err
		}
		for _, spec := range file.Imports {
			if strings.Trim(spec.Path.Value, `"`) == pkg {
				count++
				break
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("scanning production files for %s: %v", pkg, err)
	}
	return count
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
