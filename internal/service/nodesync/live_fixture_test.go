package nodesync_test

import (
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// hostArgMarker is the placeholder a fixture carries where the Node's
// includeHost argument belongs. It is written as a comment so a fixture that
// was never rendered still parses as Go.
const hostArgMarker = "/*psp:host*/"

// The path the fixtures are WRITTEN in. What they must NAME is read from the
// revision under test — see nodeModulePath.
const writtenModulePath = "github.com/KazuhaHub/passwall-node"

// nodeFixture renders a fixture harness for the Node revision under test.
//
// THE HARNESS IS COMPILED AGAINST EVERY REVISION THE GATE EXERCISES, which is
// every published tag as well as the revision PSP pins. Two things about the Node
// have moved while those releases were being cut, and a fixture written for one
// side of either change does not build at the other:
//
//   - Synchronizer.SyncOnce took (ctx, partial) through beta9 and takes
//     (ctx, partial, includeHost) from the release that added host telemetry, so a
//     hard-coded arity fails to build at one end or the other.
//   - The MODULE PATH carries the product major from the release that arranged it
//     (`github.com/KazuhaHub/passwall-node/v4`), so a fixture importing the node's
//     own packages by the bare path resolves nothing at that revision and later.
//
// The two ends are checked by two different jobs, which means no single literal can
// satisfy both, and the failures read as a harness problem rather than as a node's.
// So both are read from the revision's own source rather than assumed. An
// unrecognised shape is a failure rather than a guess: picking the wrong one
// silently would turn a build error into a gate that proves nothing.
func nodeFixture(t *testing.T, nodeRepo, fixture string) string {
	t.Helper()
	fixture = strings.ReplaceAll(fixture, writtenModulePath+"/", nodeModulePath(t, nodeRepo)+"/")
	arg, err := syncHostArg(nodeRepo)
	if err != nil {
		t.Fatalf("render the Node fixture for %s: %v", nodeRepo, err)
	}
	return strings.ReplaceAll(fixture, hostArgMarker, arg)
}

// nodeModulePath reads the module path the revision declares.
//
// From its go.mod rather than from a constant, because the constant would be a
// second copy of a decision the module makes about itself — and the one that
// hard-coded the bare path is what this replaces.
func nodeModulePath(t *testing.T, nodeRepo string) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(nodeRepo, "go.mod"))
	if err != nil {
		t.Fatalf("read the Node module path from %s: %v", nodeRepo, err)
	}
	for _, line := range strings.Split(string(raw), "\n") {
		if path, found := strings.CutPrefix(strings.TrimSpace(line), "module "); found {
			if path = strings.TrimSpace(path); path != "" {
				return path
			}
		}
	}
	t.Fatalf("the Node checkout at %s declares no module path", nodeRepo)
	return ""
}

// syncHostArg reports the argument to append at hostArgMarker: ", false" for a
// revision whose SyncOnce takes the host flag, or the empty string for one that
// predates it.
func syncHostArg(nodeRepo string) (string, error) {
	dir := filepath.Join(nodeRepo, "internal", "agent")
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", fmt.Errorf("read the Node agent package: %w", err)
	}
	set := token.NewFileSet()
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".go" {
			continue
		}
		// A file that does not parse is skipped rather than fatal: the package
		// may carry platform-specific files that only build elsewhere, and the
		// method this looks for lives in a portable one.
		parsed, err := parser.ParseFile(set, filepath.Join(dir, entry.Name()), nil, 0)
		if err != nil {
			continue
		}
		for _, decl := range parsed.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Name.Name != "SyncOnce" || fn.Recv == nil || len(fn.Recv.List) != 1 {
				continue
			}
			if !isSynchronizerReceiver(fn.Recv.List[0].Type) {
				continue
			}
			switch params := parameterCount(fn); params {
			case 2:
				return "", nil
			case 3:
				return ", false", nil
			default:
				return "", fmt.Errorf("SyncOnce takes %d parameters; the fixture knows only the two- and three-parameter shapes", params)
			}
		}
	}
	return "", errors.New("the Node agent package declares no Synchronizer.SyncOnce")
}

func isSynchronizerReceiver(expr ast.Expr) bool {
	if star, ok := expr.(*ast.StarExpr); ok {
		expr = star.X
	}
	ident, ok := expr.(*ast.Ident)
	return ok && ident.Name == "Synchronizer"
}

// parameterCount counts declared names rather than fields, because a Go
// parameter list may group several names under one type:
// (ctx context.Context, partial, includeHost bool) is three parameters in two
// fields.
func parameterCount(fn *ast.FuncDecl) int {
	if fn.Type.Params == nil {
		return 0
	}
	count := 0
	for _, field := range fn.Type.Params.List {
		if len(field.Names) == 0 {
			count++
			continue
		}
		count += len(field.Names)
	}
	return count
}

// The probe decides whether a fixture compiles at all, so the shape it accepts
// and the shape it refuses are both worth pinning down.
func TestSyncHostArgReadsTheRevisionSignature(t *testing.T) {
	cases := []struct {
		name      string
		signature string
		want      string
		wantErr   bool
	}{
		{
			name:      "the released two-argument shape adds nothing",
			signature: "func (s Synchronizer) SyncOnce(ctx context.Context, partial bool) (SyncResult, error)",
			want:      "",
		},
		{
			name:      "the host-aware shape adds the flag",
			signature: "func (s Synchronizer) SyncOnce(ctx context.Context, partial, includeHost bool) (SyncResult, error)",
			want:      ", false",
		},
		{
			name:      "a pointer receiver is still the synchronizer",
			signature: "func (s *Synchronizer) SyncOnce(ctx context.Context, partial, includeHost bool) (SyncResult, error)",
			want:      ", false",
		},
		{
			name:      "an unknown shape is refused rather than guessed",
			signature: "func (s Synchronizer) SyncOnce(ctx context.Context, partial, includeHost, extra bool) (SyncResult, error)",
			wantErr:   true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			repo := writeNodeStub(t, tc.signature)
			got, err := syncHostArg(repo)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("a four-parameter SyncOnce was accepted with %q", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("syncHostArg: %v", err)
			}
			if got != tc.want {
				t.Fatalf("argument = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestSyncHostArgRefusesARepositoryWithoutTheMethod(t *testing.T) {
	repo := t.TempDir()
	if err := os.MkdirAll(filepath.Join(repo, "internal", "agent"), 0o750); err != nil {
		t.Fatal(err)
	}
	if _, err := syncHostArg(repo); err == nil {
		t.Fatal("a package with no SyncOnce produced an argument instead of an error")
	}
}

// writeNodeStub lays out the minimum a probe reads: one source file in the
// package it looks through, holding the given signature.
func writeNodeStub(t *testing.T, signature string) string {
	t.Helper()
	repo := t.TempDir()
	dir := filepath.Join(repo, "internal", "agent")
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatal(err)
	}
	source := "package agent\n\nimport \"context\"\n\ntype Synchronizer struct{}\n\ntype SyncResult struct{}\n\n" +
		signature + " { return SyncResult{}, nil }\n"
	if err := os.WriteFile(filepath.Join(dir, "sync.go"), []byte(source), 0o600); err != nil {
		t.Fatal(err)
	}
	return repo
}
