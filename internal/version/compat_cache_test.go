package version

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// These cases intentionally do not run in parallel: Version and the active
// compat state are process-wide globals, just as they are during app boot.
func isolatedCompatCache(t *testing.T, version string) string {
	t.Helper()
	oldVersion, oldDir, oldMax, oldMin := Version, getCacheDir(), ActiveMaxTestedXUI(), ActiveMinXUI()
	oldSUI := ActiveMaxTestedSUI()
	dir := t.TempDir()
	Version = version
	SetCacheDir(dir)
	SetActiveMaxTestedXUI("")
	SetActiveMinXUI("")
	SetActiveMaxTestedSUI("")
	t.Cleanup(func() {
		Version = oldVersion
		SetCacheDir(oldDir)
		SetActiveMaxTestedXUI(oldMax)
		SetActiveMinXUI(oldMin)
		SetActiveMaxTestedSUI(oldSUI)
	})
	return dir
}

func writeSnapshot(t *testing.T, dir string, payload remoteCompatPayload) policySnapshot {
	t.Helper()
	if err := storePolicySnapshot(payload); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(dir, policySnapshotFile))
	if err != nil {
		t.Fatal(err)
	}
	var snapshot policySnapshot
	if err := json.Unmarshal(raw, &snapshot); err != nil {
		t.Fatal(err)
	}
	return snapshot
}

// snapshotDocument returns the 3X-UI document out of a container.
//
// It FAILS when there is none, so a mutation written for the old flat file cannot
// edit nothing and then pass: the container moved the document one level down, and
// an edit aimed at the top level would silently keep doing what it used to.
func snapshotDocument(t *testing.T, snapshot *policySnapshot) policySnapshotDocument {
	t.Helper()
	document, ok := snapshot.Documents[productXUI]
	if !ok {
		t.Fatalf("the snapshot holds no %s document: %#v", productXUI, snapshot.Documents)
	}
	return document
}

// The instant the shipped documents are read at. Fixed rather than taken from the
// clock: a document carries a window, and a test that moved with the wall clock
// would start failing on the day the window closes rather than on the day the
// document changes.
var compatCacheNow = time.Date(2026, 9, 20, 12, 0, 0, 0, time.UTC)

// shippedPolicy parses one of the published per-product documents.
func shippedPolicy(t *testing.T, name string) PanelRangesPolicy {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "..", "docs", "compat", name))
	if err != nil {
		t.Fatalf("read the shipped document %s: %v", name, err)
	}
	policy, err := ParsePanelRangesPolicy(raw, compatCacheNow)
	if err != nil {
		t.Fatalf("the shipped document %s does not parse: %v", name, err)
	}
	return policy
}

// shippedPayload converts a parsed document into the payload the apply path
// builds, which is what the snapshot stores.
func shippedPayload(policy PanelRangesPolicy) remoteCompatPayload {
	window := policy.AppliesToPSP
	return remoteCompatPayload{
		SchemaVersion: schemaVersion,
		Product:       policy.Product,
		UpdatedAt:     policy.IssuedAt.UTC().Format(time.RFC3339),
		Entries:       policy.Entries,
		SUIEntries:    policy.SUIEntries,
		Advisories:    policy.Advisories,
		SUIAdvisories: policy.SUIAdvisories,
		AppliesToPSP:  &window,
	}
}

// productPolicy is the 3X-UI document a PRODUCT build actually reads.
func productPolicy(t *testing.T) remoteCompatPayload {
	t.Helper()
	return shippedPayload(shippedPolicy(t, fmt.Sprintf(xuiDocumentPattern, 4)))
}

// The snapshot stores the DOCUMENT, not a conclusion. These cases exercise what
// that buys: boot re-runs the same applicability test a fetch would, so a cached
// document installs only where it applies — and, because the merged document
// carries prerelease-aware ranges, it lands on a DIFFERENT row for a beta than
// for the stable line.
func TestPolicySnapshotOnlyInstallsWhereTheDocumentApplies(t *testing.T) {
	manifest := productPolicy(t)

	for _, tc := range []struct {
		name       string
		current    string
		wantMax    string
		wantErr    bool
		wantUnread bool
	}{
		{name: "the released line", current: "4.0.0", wantMax: "3.8.5"},
		{name: "a later release on the same line", current: "4.0.1", wantMax: "3.8.5"},
		{name: "the top of the reviewed window", current: "4.99.99", wantMax: "3.8.5"},
		// A LEGACY STAMP IS NOT AN IDENTITY ANY MORE, so it cannot be matched
		// against a window either: the document does not apply, and no range is
		// established for it.
		{name: "a legacy stamp", current: "v4.0.0-beta.9", wantErr: true, wantUnread: true},
		{name: "below the line", current: "3.9.2", wantErr: true, wantUnread: true},
		{name: "above the window", current: "5.0.0", wantErr: true, wantUnread: true},
		{name: "unparseable identity", current: "dev", wantErr: true, wantUnread: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := isolatedCompatCache(t, tc.current)
			writeSnapshot(t, dir, manifest)

			err := LoadPolicySnapshot()

			if (err != nil) != tc.wantErr {
				t.Fatalf("load error=%v, wantError=%v", err, tc.wantErr)
			}
			if got := ActiveMaxTestedXUI(); got != tc.wantMax {
				t.Fatalf("active range=%q, want %q", got, tc.wantMax)
			}
			if tc.wantUnread && CheckXUI("3.7.0") != CompatUnknown {
				t.Fatal("a document that does not apply must not establish a supported range")
			}
		})
	}
}

// THE DOCUMENT IS APPLIED; THE SNAPSHOT IS A DIFFERENT THING.
//
// storePolicySnapshot persists what the NEXT boot replays when it cannot fetch.
// Its failure used to be returned as the apply's failure, so the panel reported
// "the most recent refresh failed" while it was running the newest policy — and
// showed the PREVIOUS ceiling beside that claim. An operator read a banner that
// contradicted itself, about a data directory this deployment had never needed
// before: the panel runs on MySQL, so no SQLite file writes there either.
//
// A snapshot that cannot be stored is a DEGRADATION — the next boot fetches
// instead of replaying — and not a refresh that did not happen.
func TestAnUnwritableSnapshotDirectoryDoesNotFailTheApply(t *testing.T) {
	isolatedCompatCache(t, "4.0.0")
	// A FILE where the directory should be. MkdirAll cannot succeed, so the write
	// fails the way a read-only data directory does — and, unlike a chmod, it fails
	// for root too, so the case means the same thing wherever it runs.
	blocker := filepath.Join(t.TempDir(), "not-a-dir")
	if err := os.WriteFile(blocker, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	SetCacheDir(filepath.Join(blocker, "data"))

	raw, err := os.ReadFile(filepath.Join("..", "..", "docs", "compat", fmt.Sprintf(xuiDocumentPattern, 4)))
	if err != nil {
		t.Fatal(err)
	}
	if err := applyXUICompatDocument(raw, time.Now().UTC()); err != nil {
		t.Fatalf("an unwritable snapshot directory failed the apply: %v", err)
	}
	if got := ActiveMaxTestedXUI(); got != "3.8.5" {
		t.Fatalf("active range=%q, want 3.8.5 — the document was validated and must have taken effect", got)
	}
}

func TestPolicySnapshotRefusesADocumentThatFailsItsOwnIntegrityCheck(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(t *testing.T, dir string, raw []byte) []byte
	}{
		{
			name: "payload edited after the digest was taken",
			mutate: func(t *testing.T, dir string, raw []byte) []byte {
				var snapshot policySnapshot
				if err := json.Unmarshal(raw, &snapshot); err != nil {
					t.Fatal(err)
				}
				// A FIELD THE NAMED DOCUMENT ACTUALLY CARRIES. This used to edit
				// `"major":4`, which is a per-major manifest field: a panel ranges
				// document has no major, so the edit changed nothing and the case
				// stopped testing what it says it tests. The window is the field
				// that decides whether the document applies at all.
				document := snapshotDocument(t, &snapshot)
				edited := bytes.Replace(document.Payload, []byte(`"max":"4.99.99"`), []byte(`"max":"9.99.99"`), 1)
				if bytes.Equal(edited, document.Payload) {
					t.Fatal("the fixture payload carries no window to edit; this case would pass vacuously")
				}
				document.Payload = edited // digest deliberately NOT recomputed
				snapshot.Documents[productXUI] = document
				out, err := json.Marshal(snapshot)
				if err != nil {
					t.Fatal(err)
				}
				return out
			},
		},
		{
			name: "digest field rewritten to match an edited payload",
			mutate: func(t *testing.T, dir string, raw []byte) []byte {
				var snapshot policySnapshot
				if err := json.Unmarshal(raw, &snapshot); err != nil {
					t.Fatal(err)
				}
				// THE WINDOW'S FLOOR, not its ceiling: this case recomputes the
				// digest, so the refusal has to come from the document's own
				// applicability. Raising the floor above the build is what makes
				// the document describe a release line this panel is not on —
				// which is the named-document form of "a document for another
				// major".
				document := snapshotDocument(t, &snapshot)
				document.Payload = bytes.Replace(document.Payload, []byte(`"min":"4.0.0"`), []byte(`"min":"9.0.0"`), 1)
				sum := sha256.Sum256(document.Payload)
				document.Digest = hex.EncodeToString(sum[:])
				snapshot.Documents[productXUI] = document
				out, err := json.Marshal(snapshot)
				if err != nil {
					t.Fatal(err)
				}
				return out
			},
		},
		{
			name: "truncated file",
			mutate: func(t *testing.T, dir string, raw []byte) []byte {
				return raw[:len(raw)/2]
			},
		},
		{
			name: "unknown snapshot format",
			mutate: func(t *testing.T, dir string, raw []byte) []byte {
				var snapshot map[string]any
				if err := json.Unmarshal(raw, &snapshot); err != nil {
					t.Fatal(err)
				}
				snapshot["snapshot_schema"] = policySnapshotSchema + 1
				out, err := json.Marshal(snapshot)
				if err != nil {
					t.Fatal(err)
				}
				return out
			},
		},
		{
			name: "no payload at all",
			mutate: func(t *testing.T, dir string, raw []byte) []byte {
				var snapshot map[string]any
				if err := json.Unmarshal(raw, &snapshot); err != nil {
					t.Fatal(err)
				}
				// THE DOCUMENT IS INSIDE A CONTAINER NOW, so removing the payload
				// from the file's top level would leave the container intact and
				// the case would pass without testing anything.
				documents, ok := snapshot["documents"].(map[string]any)
				if !ok {
					t.Fatalf("the snapshot is not a container: %s", raw)
				}
				document, ok := documents[productXUI].(map[string]any)
				if !ok {
					t.Fatalf("the container holds no %s document: %s", productXUI, raw)
				}
				delete(document, "payload")
				out, err := json.Marshal(snapshot)
				if err != nil {
					t.Fatal(err)
				}
				return out
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := isolatedCompatCache(t, "4.0.0")
			writeSnapshot(t, dir, productPolicy(t))
			path := filepath.Join(dir, policySnapshotFile)
			raw, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, tc.mutate(t, dir, raw), 0o600); err != nil {
				t.Fatal(err)
			}

			if err := LoadPolicySnapshot(); err == nil {
				t.Fatal("a snapshot that fails its own integrity check must be refused")
			}
			if ActiveMaxTestedXUI() != "" || CheckXUI("3.7.0") != CompatUnknown {
				t.Fatalf("a refused snapshot still established a range: %q", ActiveMaxTestedXUI())
			}
		})
	}
}

// The second mutation above rewrites the digest to match its edited payload, so
// it exercises the schema/major checks rather than the digest one. Both matter:
// a valid digest over a document for ANOTHER major is exactly what a
// snapshot from a different build looks like.
func TestPolicySnapshotRoundTripsAndKeepsItsProvenance(t *testing.T) {
	dir := isolatedCompatCache(t, "4.0.0")
	snapshot := writeSnapshot(t, dir, productPolicy(t))

	if snapshot.SnapshotSchema != policySnapshotSchema {
		t.Fatalf("snapshot format = %d", snapshot.SnapshotSchema)
	}
	document := snapshotDocument(t, &snapshot)
	if document.Revision == "" || document.Source == "" || document.FetchedAt.IsZero() || document.Digest == "" {
		t.Fatalf("snapshot lost its provenance: %#v", snapshot)
	}
	if len(document.Payload) == 0 {
		t.Fatal("snapshot stored no document")
	}

	// Replay installs, then a different major does not — without the file
	// having changed between the two.
	if err := LoadPolicySnapshot(); err != nil || ActiveMaxTestedXUI() != "3.8.5" {
		t.Fatalf("same-major replay: active=%q error=%v", ActiveMaxTestedXUI(), err)
	}
	SetActiveMaxTestedXUI("")
	Version = "3.9.2"
	if err := LoadPolicySnapshot(); err == nil || ActiveMaxTestedXUI() != "" {
		t.Fatalf("cross-major replay: active=%q error=%v", ActiveMaxTestedXUI(), err)
	}
}

func TestPolicySnapshotMissingOrDisabledIsNotAnError(t *testing.T) {
	isolatedCompatCache(t, "4.0.0")
	if err := LoadPolicySnapshot(); err != nil {
		t.Fatalf("missing optional snapshot: %v", err)
	}
	SetCacheDir("")
	if err := LoadPolicySnapshot(); err != nil {
		t.Fatalf("disabled optional snapshot: %v", err)
	}
}

// The writer must publish atomically: a reader either sees the previous
// snapshot or the new one, never a partial file, and the temporary file does not
// survive.
func TestPolicySnapshotWriteLeavesNoTemporaryBehind(t *testing.T) {
	dir := isolatedCompatCache(t, "4.0.0")
	writeSnapshot(t, dir, productPolicy(t))

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		names = append(names, e.Name())
	}
	if len(names) != 1 || names[0] != policySnapshotFile {
		t.Fatalf("cache dir holds %v, want only %s", names, policySnapshotFile)
	}
}

func TestLoadLatestXUICacheIsPSPMajorIndependent(t *testing.T) {
	isolatedCompatCache(t, "v3.9.2")
	old := LatestXUI()
	t.Cleanup(func() { SetLatestXUI(old) })
	if err := saveLatestXUICache("v3.7.0"); err != nil {
		t.Fatal(err)
	}
	Version = "3.0.0"
	SetLatestXUI("")
	if err := LoadLatestXUICache(); err != nil || LatestXUI() != "v3.7.0" {
		t.Fatalf("upstream latest tag must survive PSP major changes: tag=%q error=%v", LatestXUI(), err)
	}
}

// A snapshot that cannot be trusted must leave the instance exactly as it was.
// Clearing the active range on a bad cache would turn a corrupted file into a
// loss of the panel's working range — the failure would be worse than the
// problem.
func TestAFailedSnapshotLeavesTheActiveRangeAlone(t *testing.T) {
	dir := isolatedCompatCache(t, "4.0.0")
	writeSnapshot(t, dir, productPolicy(t))
	path := filepath.Join(dir, policySnapshotFile)
	if err := os.WriteFile(path, []byte("{ not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	SetActiveMaxTestedXUI("3.5.0")
	SetActiveMinXUI("3.4.2")

	if err := LoadPolicySnapshot(); err == nil {
		t.Fatal("a corrupt snapshot must be refused")
	}
	if got := ActiveMaxTestedXUI(); got != "3.5.0" {
		t.Fatalf("a refused snapshot changed the active ceiling to %q", got)
	}
	if got := ActiveMinXUI(); got != "3.4.2" {
		t.Fatalf("a refused snapshot changed the active floor to %q", got)
	}
}
