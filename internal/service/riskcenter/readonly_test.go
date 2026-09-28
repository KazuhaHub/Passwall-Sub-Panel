package riskcenter

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

// THE RISK CENTER LOOKS; IT NEVER ACTS.
//
// It shows who is connected from where, what the detector recorded and what
// each account did — and nothing it shows may reach an account: not its
// service axis, not its panels, not a mail, not a verdict. The only thing it
// may cause is a fresh reading of the panels' live connections
// (RefreshLiveConnections), which reads and writes nothing but the in-memory
// snapshot it is shown from. That is enforced by what it is HANDED: every
// dependency is a narrow interface, pinned here to an allowlist. Widen Users
// to ports.UserRepo and the view could suspend people; this fails. The
// queue's reads are no exception: the review rows are read with Get and
// List, never Save — dismissing and trusting are riskreview's, a separate
// package precisely so that nothing here can write them — and the service
// holds are listed, never written.
//
// Reflection cannot see a type assertion — d.Users.(ports.UserRepo) would
// recover the writer from the very value this test approved — so the
// package's source is scanned for any type assertion or type switch, and
// for imports beyond what a pure observer needs: no adapter (which could
// open its own writer), no other service (which would bring its writers),
// and nothing that calls a method by name (reflect, unsafe, templates,
// net/rpc), the same holes TestRiskServiceCannotWriteServiceState closes for
// the risk worker.
func TestRiskCenterCannotWriteServiceState(t *testing.T) {
	allowed := map[string][]string{
		"Live":     {"LiveSnapshot", "RefreshLiveConnections"},
		"Settings": {"Load"},
		"Users":    {"GetByID", "ListByIDs"},
		"Panels":   {"List"},
		"Fetches":  {"RecentForUsers"},
		"History":  {"List"},
		"Flags":    {"LatestByUsers", "List", "StepsSince"},
		"Geo":      {"AttentionLevels", "CountFreshUnknown", "ListByUsers"},
		"Signals":  {"AttentionLevels", "ListByUsers"},
		"Reviews":  {"Get", "List"},
		"Holds":    {"ListServiceHolds"},
		"Groups":   {"List"},
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
		module + "internal/domain":      true,
		module + "internal/ports":       true,
		module + "internal/pkg/log":     true,
		module + "internal/pkg/metrics": true,
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
				t.Errorf("%s imports %s: the risk center may import only domain, ports, pkg/log and pkg/metrics", name, path)
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
