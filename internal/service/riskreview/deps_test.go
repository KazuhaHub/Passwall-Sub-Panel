package riskreview

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"
)

// THE REVIEW ACTIONS WRITE ONE ROW AND LIFT ONE HOLD — NOTHING ELSE.
//
// An admin dismissing an account's signals or trusting the account changes
// the review row and appends its record; the only thing it may do to the
// account itself is lift the location detector's OWN suspension when the
// admin trusts the account and asks for it. It must never suspend anyone,
// never touch a person's hold, never reach a panel or a mail on its own. That
// is enforced by what it is HANDED: every dependency is a narrow interface,
// pinned here to an allowlist. Widen Users to ports.UserRepo, or Resumer to
// *user.Service, and a review could pause people; this fails.
//
// Reflection cannot see a type assertion — an assertion on d.Resumer to an
// interface with SuspendServiceIfClear would recover the suspender from the
// very value this test approved — so the package's source is scanned for any
// type assertion or type switch, and for imports beyond what it needs: no adapter
// (which could open its own writer), no other service (which would bring its
// writers), and nothing that calls a method by name (reflect, unsafe,
// templates, net/rpc), the holes TestRiskCenterCannotWriteServiceState closes
// for the read side.
func TestRiskReviewDepsAreNarrow(t *testing.T) {
	allowed := map[string][]string{
		"Store":     {"Get", "Save"},
		"Attention": {"UserAttention"},
		"Users":     {"GetByID"},
		"Resumer":   {"ResumeGeoAutoIfHeld"},
	}
	deps := reflect.TypeFor[Deps]()
	seen := map[string]bool{}
	for f := range deps.Fields() {
		switch f.Type.Kind() {
		case reflect.Interface:
			want, ok := allowed[f.Name]
			if !ok {
				t.Fatalf("Deps.%s (%s) is an interface with no allowlist entry: add it here, with the narrowest method set", f.Name, f.Type)
			}
			var got []string
			for m := range f.Type.Methods() {
				got = append(got, m.Name)
			}
			slices.Sort(got)
			if !slices.Equal(got, want) {
				t.Fatalf("Deps.%s (%s) exposes %v, want exactly %v", f.Name, f.Type, got, want)
			}
			seen[f.Name] = true
		case reflect.Func:
			// A function value carries no method set to widen (the clock).
		default:
			t.Fatalf("Deps.%s is a %s (%s): a struct or pointer hands over every method it has — declare a narrow interface", f.Name, f.Type.Kind(), f.Type)
		}
	}
	for name := range allowed {
		if !seen[name] {
			t.Fatalf("the allowlist names Deps.%s, which is not an interface field any more", name)
		}
	}

	const module = "github.com/KazuhaHub/passwall-sub-panel/"
	importsAllowed := map[string]bool{
		module + "internal/domain":         true,
		module + "internal/ports":          true,
		module + "internal/pkg/keyedmutex": true,
		module + "internal/pkg/log":        true,
	}
	importsDenied := map[string]string{
		"reflect":       "it calls a method by name on the value it is handed",
		"unsafe":        "it reinterprets a value's memory, and //go:linkname needs it",
		"text/template": "a template calls exported methods by name, through reflect",
		"html/template": "a template calls exported methods by name, through reflect",
		"net/rpc":       "a registered receiver's exported methods are called by name, through reflect",
	}
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	scanned := 0
	fset := token.NewFileSet()
	for _, name := range files {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, name, nil, parser.SkipObjectResolution)
		if err != nil {
			t.Fatal(err)
		}
		scanned++
		for _, imp := range f.Imports {
			path, _ := strconv.Unquote(imp.Path.Value)
			if strings.HasPrefix(path, module) && !importsAllowed[path] {
				t.Errorf("%s imports %s: the review actions may import only domain, ports, pkg/keyedmutex and pkg/log", name, path)
			}
			if why, denied := importsDenied[path]; denied {
				t.Errorf("%s imports %s: %s, so it could reach a writer through a narrow dependency", name, path, why)
			}
		}
		// A type switch's x.(type) is a TypeAssertExpr too.
		ast.Inspect(f, func(n ast.Node) bool {
			if ta, ok := n.(*ast.TypeAssertExpr); ok {
				t.Errorf("%s: type assertion at %s — it could recover a writer from a narrow dependency", name, fset.Position(ta.Pos()))
			}
			return true
		})
	}
	if scanned == 0 {
		t.Fatal("scanned no source files: the guard is looking in the wrong directory")
	}
}
