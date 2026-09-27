package risk

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

// THE RISK SIGNALS OBSERVE; THEY NEVER ACT.
//
// Nothing about a risk verdict may reach an account: not its service axis,
// not its panels, not a mail, not the audit log. The only write this package
// is allowed is its own table. That is enforced by what it is HANDED — every
// dependency is a narrow interface, and the store's only writer is Save — so
// this test pins each interface field's method set to an allowlist. Widen
// Users to ports.UserRepo and the worker could suspend people; this fails.
//
// Reflection cannot see a type assertion: d.Users.(ports.UserRepo) would
// recover the writer from the very value this test approved. So the
// package's source is scanned too — no type assertion or type switch at all,
// and no import beyond the ones a pure observer needs (in particular no
// adapter, which could open its own writer, and no other service, which
// would bring its writers along).
//
// Nor may the package use reflection itself. The value behind Users is the
// composition root's full user repository, and
// reflect.ValueOf(d.Users).MethodByName("SetServiceState").Call(...) reaches
// its writer without a type assertion anywhere; unsafe reinterprets the value
// outright, and it is also what //go:linkname needs. text/template,
// html/template and net/rpc are the same hole one step removed: each calls
// exported methods by name, through reflect, on whatever it is handed.
func TestRiskServiceCannotWriteServiceState(t *testing.T) {
	allowed := map[string][]string{
		"Users":      {"List"},
		"Store":      {"PurgeOrphans", "Save"},
		"Settings":   {"Load", "LoadForGroup", "LoadForUser"},
		"Traffic":    {"ListHourlyByUser", "SumHourlyAllUsers"},
		"SubLogs":    {"ScanSince"},
		"Geo":        {"Available", "Lookup"},
		"AuthEvents": {"List"},
	}
	deps := reflect.TypeFor[Deps]()
	seen := map[string]bool{}
	for f := range deps.Fields() {
		switch f.Type.Kind() {
		case reflect.Interface:
			want, ok := allowed[f.Name]
			if !ok {
				t.Fatalf("Deps.%s (%s) is an interface with no allowlist entry: add it here, read-only methods only", f.Name, f.Type)
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
			// A function value carries no method set to widen; what it may
			// do is decided where it is built, in the composition root.
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
		module + "internal/domain":      true,
		module + "internal/ports":       true,
		module + "internal/pkg/log":     true,
		module + "internal/pkg/metrics": true,
		module + "internal/pkg/paneltz": true,
	}
	// Standard-library packages that call a method named at run time, or
	// reinterpret a value's memory. Elsewhere the standard library calls only
	// the fixed methods it documents (fmt's String, encoding/json's
	// MarshalJSON); plugin hands back an untyped symbol that is useless
	// without the type assertion the scan below already refuses.
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
				t.Errorf("%s imports %s: the risk service may import only domain, ports, pkg/log, pkg/metrics and pkg/paneltz", name, path)
			}
			if why, denied := importsDenied[path]; denied {
				t.Errorf("%s imports %s: %s, so it could reach a writer through a read-only dependency", name, path, why)
			}
		}
		ast.Inspect(f, func(n ast.Node) bool {
			if ta, ok := n.(*ast.TypeAssertExpr); ok {
				t.Errorf("%s: type assertion at %s — it could recover a writer from a read-only dependency", name, fset.Position(ta.Pos()))
			}
			return true
		})
	}
	if scanned == 0 {
		t.Fatal("scanned no source files: the guard is looking in the wrong directory")
	}
}
