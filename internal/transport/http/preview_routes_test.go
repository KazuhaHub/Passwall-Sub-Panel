package http

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// readOnlyPreviewRoutes names every route exempt from audit (ADR 0042 §5). A
// preview computes from a draft and writes nothing; adding one here is the
// reviewed decision that it never will.
var readOnlyPreviewRoutes = []string{
	"/dest/lists/preview",
	"/dest/policies/preview",
	"/groups/:id/node-preview",
	"/groups/node-preview",
	"/nodes/:id/line-visibility",
	"/nodes/separator/group-preview",
	"/users/:id/group-preview",
}

// TestReadOnlyPreviewRoutesAreNamed reads the package source: every
// previewPOST call must use a literal path named above, and nothing outside
// previewPOST may attach the audit exemption directly.
func TestReadOnlyPreviewRoutesAreNamed(t *testing.T) {
	fset := token.NewFileSet()
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	var registered, direct []string
	for _, name := range files {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		src, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		f, err := parser.ParseFile(fset, name, src, 0)
		if err != nil {
			t.Fatal(err)
		}
		var helper *ast.FuncDecl
		for _, decl := range f.Decls {
			if fn, ok := decl.(*ast.FuncDecl); ok && fn.Name.Name == "previewPOST" {
				helper = fn
			}
		}
		ast.Inspect(f, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			switch fun := call.Fun.(type) {
			case *ast.Ident:
				if fun.Name == "previewPOST" {
					lit, ok := call.Args[1].(*ast.BasicLit)
					if !ok || lit.Kind != token.STRING {
						t.Errorf("%s: previewPOST needs a literal path", fset.Position(call.Pos()))
						return true
					}
					path, _ := strconv.Unquote(lit.Value)
					registered = append(registered, path)
				}
			case *ast.SelectorExpr:
				if fun.Sel.Name == "AuditReadOnly" && (helper == nil || call.Pos() < helper.Pos() || call.Pos() > helper.End()) {
					direct = append(direct, fset.Position(call.Pos()).String())
				}
			}
			return true
		})
	}
	sort.Strings(registered)
	want := append([]string(nil), readOnlyPreviewRoutes...)
	sort.Strings(want)
	if !reflect.DeepEqual(registered, want) {
		t.Fatalf("read-only previews changed:\n got  %v\n want %v\nname a new preview in readOnlyPreviewRoutes only if it truly writes nothing", registered, want)
	}
	if len(direct) > 0 {
		t.Fatalf("AuditReadOnly attached outside previewPOST: %v", direct)
	}
}
