package app

import (
	"go/ast"
	"go/parser"
	"go/token"
	"slices"
	"strings"
	"testing"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/service/cert"
	"github.com/KazuhaHub/passwall-sub-panel/internal/service/mailer"
	"github.com/KazuhaHub/passwall-sub-panel/internal/service/node"
	"github.com/KazuhaHub/passwall-sub-panel/internal/service/user"
)

// ListDue is filtered by type, so a SyncTaskType that no processor lists is a
// task that is enqueued and never run — silently, forever. Every declared type
// must be owned by exactly one processor. The declared set is read from the
// source (not a hand-kept list) so adding a constant without an owner fails.
func TestEverySyncTaskTypeHasExactlyOneProcessor(t *testing.T) {
	declared := declaredSyncTaskTypes(t)
	if len(declared) == 0 {
		t.Fatal("found no SyncTaskType constants; the source scan is broken")
	}
	owners := map[string][]string{}
	for name, types := range map[string][]domain.SyncTaskType{
		"user":   user.OwnedTaskTypes(),
		"node":   node.OwnedTaskTypes(),
		"cert":   cert.OwnedTaskTypes(),
		"mailer": mailer.OwnedTaskTypes(),
	} {
		for _, typ := range types {
			owners[string(typ)] = append(owners[string(typ)], name)
		}
	}
	for _, typ := range declared {
		if got := owners[typ]; len(got) != 1 {
			t.Errorf("sync task type %q is owned by %v, want exactly one processor", typ, got)
		}
	}
	for typ := range owners {
		if !slices.Contains(declared, typ) {
			t.Errorf("processor owns %q, which is not a declared SyncTaskType", typ)
		}
	}
}

func declaredSyncTaskTypes(t *testing.T) []string {
	t.Helper()
	f, err := parser.ParseFile(token.NewFileSet(), "../domain/enums.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, d := range f.Decls {
		gd, ok := d.(*ast.GenDecl)
		if !ok || gd.Tok != token.CONST {
			continue
		}
		for _, spec := range gd.Specs {
			vs := spec.(*ast.ValueSpec)
			id, ok := vs.Type.(*ast.Ident)
			if !ok || id.Name != "SyncTaskType" {
				continue
			}
			for _, v := range vs.Values {
				if lit, ok := v.(*ast.BasicLit); ok {
					out = append(out, strings.Trim(lit.Value, `"`))
				}
			}
		}
	}
	return out
}
