package group

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"strings"
	"testing"
)

// Eligible is the one test of "does this group include this node". A call to
// group.Matches anywhere else would decide membership by tags alone and let an
// allowlist group's members have a client on a node that cannot enforce it, so
// this scans the code that could grow such a call: the services, the
// transport and the application wiring. Only this package may call Matches.
func TestNoGroupMatchesOutsideGroupPackage(t *testing.T) {
	roots := []string{"../../service", "../../transport", "../../app"}
	fset := token.NewFileSet()
	var offenders []string
	for _, root := range roots {
		err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			if strings.HasSuffix(filepath.ToSlash(filepath.Dir(path)), "internal/service/group") {
				return nil
			}
			f, err := parser.ParseFile(fset, path, nil, 0)
			if err != nil {
				return err
			}
			offenders = append(offenders, groupMatchesCalls(fset, f)...)
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	if len(offenders) > 0 {
		t.Fatalf("group.Matches called outside the group package; use Eligible (or NodesFor):\n  %s", strings.Join(offenders, "\n  "))
	}
}

const groupPackagePath = "internal/service/group"

// groupMatchesCalls returns the positions of calls to Matches on the group
// package in f, under whatever name f imports it as (an alias included).
func groupMatchesCalls(fset *token.FileSet, f *ast.File) []string {
	local := ""
	for _, imp := range f.Imports {
		path := strings.Trim(imp.Path.Value, `"`)
		if path != groupPackagePath && !strings.HasSuffix(path, "/"+groupPackagePath) {
			continue
		}
		local = "group"
		if imp.Name != nil {
			local = imp.Name.Name
		}
	}
	if local == "" || local == "_" {
		return nil
	}
	var out []string
	ast.Inspect(f, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok || sel.Sel.Name != "Matches" {
			return true
		}
		if x, ok := sel.X.(*ast.Ident); ok && x.Name == local {
			out = append(out, fset.Position(call.Pos()).String())
		}
		return true
	})
	return out
}

// The guard itself must be able to fail, including through an aliased import,
// and must not flag another package's Matches.
func TestNoGroupMatchesGuardSeesCalls(t *testing.T) {
	parse := func(src string) []string {
		fset := token.NewFileSet()
		f, err := parser.ParseFile(fset, "x.go", src, 0)
		if err != nil {
			t.Fatal(err)
		}
		return groupMatchesCalls(fset, f)
	}
	const path = `"github.com/KazuhaHub/passwall-sub-panel/internal/service/group"`
	if got := parse("package x\nimport " + path + "\nfunc f() { _ = group.Matches(nil, nil) }\n"); len(got) != 1 {
		t.Fatalf("plain import: %v", got)
	}
	if got := parse("package x\nimport g " + path + "\nfunc f() { _ = g.Matches(nil, nil) }\n"); len(got) != 1 {
		t.Fatalf("aliased import: %v", got)
	}
	if got := parse("package x\nimport \"regexp\"\nfunc f() { var group regexp.Regexp; _ = group.Matches }\n"); len(got) != 0 {
		t.Fatalf("another package named group flagged: %v", got)
	}
	if got := parse("package x\nimport g " + path + "\nfunc f() { var group T; group.Matches(1) }\n"); len(got) != 0 {
		t.Fatalf("a local variable named group flagged: %v", got)
	}
}
