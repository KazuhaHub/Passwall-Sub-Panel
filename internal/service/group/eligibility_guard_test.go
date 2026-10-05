package group

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestGroupMatchingCannotBypassUnifiedEligibility(t *testing.T) {
	root := filepath.Join("..", "..", "..")
	for _, subdir := range []string{"internal/service", "internal/transport", "internal/app"} {
		err := filepath.WalkDir(filepath.Join(root, subdir), func(path string, entry fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if entry.IsDir() {
				if filepath.ToSlash(path) == filepath.ToSlash(filepath.Join(root, "internal/service/group")) {
					return filepath.SkipDir
				}
				return nil
			}
			if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			set := token.NewFileSet()
			file, err := parser.ParseFile(set, path, nil, 0)
			if err != nil {
				return err
			}
			aliases := map[string]bool{}
			for _, spec := range file.Imports {
				importPath, _ := strconv.Unquote(spec.Path.Value)
				if importPath == "github.com/KazuhaHub/passwall-sub-panel/internal/service/group" {
					alias := "group"
					if spec.Name != nil {
						alias = spec.Name.Name
					}
					if alias == "." {
						t.Errorf("dot group import can bypass eligibility at %s", path)
					}
					aliases[alias] = true
				}
			}
			ast.Inspect(file, func(node ast.Node) bool {
				selector, ok := node.(*ast.SelectorExpr)
				if !ok {
					return true
				}
				pkg, ok := selector.X.(*ast.Ident)
				if ok && aliases[pkg.Name] && selector.Sel.Name == "Matches" {
					t.Errorf("raw tag matching bypasses eligibility at %s", set.Position(selector.Pos()))
				}
				return true
			})
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
}
