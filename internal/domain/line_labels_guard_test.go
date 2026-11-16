package domain_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"go/ast"
	"go/importer"
	"go/parser"
	"go/token"
	"go/types"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

const modulePath = "github.com/KazuhaHub/passwall-sub-panel"

// lineLabelReaders lists the only production code allowed to touch a line's
// own Region / Tags (ADR 0040 invariant 1, amended by ADR 0041): the domain
// model, subscription rendering, line storage and backfill, the node API's line
// DTOs, the VLESS route line editor, and the one file that narrows which lines
// a group's subscriptions publish. Credentials and mounting, allowlist
// eligibility, traffic, risk, 3X-UI routing rules and the Passwall Node
// protocol must keep reading the node's labels only; so must the rest of the
// group package, which is why only the line-visibility file is listed.
var lineLabelReaders = []string{
	"internal/domain/",
	"internal/service/render/",
	"internal/service/vlessroute/",
	"internal/adapters/sqlstore/",
	"internal/transport/http/handler/admin_node.go",
	"internal/service/group/line_visibility.go",
}

type listedPackage struct {
	ImportPath string
	Dir        string
	GoFiles    []string
	Export     string
	Module     *struct{ Path string }
}

func TestLineLabelsAreReadOnlyByDisplayCode(t *testing.T) {
	if testing.Short() {
		t.Skip("type-checks the whole module")
	}
	goBin, err := exec.LookPath("go")
	if err != nil {
		t.Fatalf("the guard needs the go command: %v", err)
	}
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(goBin, "list", "-export", "-deps", "-json", "./...")
	cmd.Dir = root
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("go list: %v\n%s", err, stderr.String())
	}
	exports := map[string]string{}
	var own []listedPackage
	decoder := json.NewDecoder(bytes.NewReader(out))
	for {
		var p listedPackage
		if err := decoder.Decode(&p); errors.Is(err, io.EOF) {
			break
		} else if err != nil {
			t.Fatal(err)
		}
		exports[p.ImportPath] = p.Export
		if p.Module != nil && p.Module.Path == modulePath {
			own = append(own, p)
		}
	}
	fset := token.NewFileSet()
	imp := importer.ForCompiler(fset, "gc", func(path string) (io.ReadCloser, error) {
		file, ok := exports[path]
		if !ok || file == "" {
			return nil, fmt.Errorf("no export data for %s", path)
		}
		return os.Open(file)
	})
	domainPkg, err := imp.Import(modulePath + "/internal/domain")
	if err != nil {
		t.Fatal(err)
	}
	guarded := map[types.Object]string{}
	for _, typeName := range []string{"RelayLine", "VlessRouteLine"} {
		st := domainPkg.Scope().Lookup(typeName).Type().Underlying().(*types.Struct)
		for i := 0; i < st.NumFields(); i++ {
			if name := st.Field(i).Name(); name == "Region" || name == "Tags" {
				guarded[st.Field(i)] = typeName + "." + name
			}
		}
	}
	if len(guarded) != 4 {
		t.Fatalf("expected Region/Tags on both line types, found %d fields", len(guarded))
	}
	var offenders []string
	checked := 0
	for _, p := range own {
		if p.ImportPath == modulePath+"/internal/domain" {
			continue
		}
		var files []*ast.File
		for _, name := range p.GoFiles {
			f, err := parser.ParseFile(fset, filepath.Join(p.Dir, name), nil, 0)
			if err != nil {
				t.Fatal(err)
			}
			files = append(files, f)
		}
		info := &types.Info{Selections: map[*ast.SelectorExpr]*types.Selection{}, Uses: map[*ast.Ident]types.Object{}}
		conf := types.Config{Importer: imp}
		if _, err := conf.Check(p.ImportPath, fset, files, info); err != nil {
			t.Fatalf("type-check %s: %v", p.ImportPath, err)
		}
		checked++
		report := func(pos token.Pos, field string) {
			position := fset.Position(pos)
			rel, _ := filepath.Rel(root, position.Filename)
			rel = filepath.ToSlash(rel)
			for _, allowed := range lineLabelReaders {
				if strings.HasPrefix(rel, allowed) {
					return
				}
			}
			offenders = append(offenders, fmt.Sprintf("%s:%d %s", rel, position.Line, field))
		}
		for sel, selection := range info.Selections {
			if field, ok := guarded[selection.Obj()]; ok {
				report(sel.Sel.Pos(), field)
			}
		}
		// Composite literal keys (RelayLine{Region: ...}) are recorded as uses.
		for ident, obj := range info.Uses {
			if field, ok := guarded[obj]; ok {
				report(ident.Pos(), field)
			}
		}
	}
	if checked < 10 {
		t.Fatalf("only %d packages type-checked; the guard is not scanning the module", checked)
	}
	sort.Strings(offenders)
	offenders = compactStrings(offenders)
	if len(offenders) > 0 {
		t.Fatalf("line Region/Tags used outside display code (ADR 0040 invariant 1, ADR 0041):\n  %s", strings.Join(offenders, "\n  "))
	}
}

// compactStrings drops adjacent duplicates: a selector is recorded both as a
// selection and as a use of its field identifier.
func compactStrings(sorted []string) []string {
	out := sorted[:0]
	for i, s := range sorted {
		if i == 0 || s != sorted[i-1] {
			out = append(out, s)
		}
	}
	return out
}
