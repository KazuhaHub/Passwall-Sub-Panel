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
			ast.Inspect(f, func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok {
					return true
				}
				sel, ok := call.Fun.(*ast.SelectorExpr)
				if !ok || sel.Sel.Name != "Matches" {
					return true
				}
				if x, ok := sel.X.(*ast.Ident); ok && x.Name == "group" {
					offenders = append(offenders, fset.Position(call.Pos()).String())
				}
				return true
			})
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

// The guard itself must be able to fail: a scan that matches nothing proves
// nothing.
func TestNoGroupMatchesGuardSeesCalls(t *testing.T) {
	src := "package x\nimport \"g/group\"\nfunc f() { _ = group.Matches(nil, nil) }\n"
	f, err := parser.ParseFile(token.NewFileSet(), "x.go", src, 0)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	ast.Inspect(f, func(n ast.Node) bool {
		if call, ok := n.(*ast.CallExpr); ok {
			if sel, ok := call.Fun.(*ast.SelectorExpr); ok && sel.Sel.Name == "Matches" {
				found = true
			}
		}
		return true
	})
	if !found {
		t.Fatal("the pattern the guard looks for does not parse as a selector call")
	}
}
