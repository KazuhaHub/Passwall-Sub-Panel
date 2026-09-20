package version

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
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

// mergedPolicy builds the document a fetch would have applied: the base
// manifest with its range overlay folded in, exactly as fetchAndApply does
// before it installs anything.
func mergedPolicy(t *testing.T) remoteCompatPayload {
	t.Helper()
	base := readCompatJSONForMajor(t, 4)
	overlay := readCompatRangeOverlay(t)
	base.SchemaVersion = overlay.SchemaVersion
	base.Entries = overlay.Entries
	base.SUIEntries = overlay.SUIEntries
	return base
}

// The snapshot stores the DOCUMENT, not a conclusion. These cases exercise what
// that buys: boot re-runs the same applicability test a fetch would, so a cached
// document installs only where it applies — and, because the merged document
// carries prerelease-aware ranges, it lands on a DIFFERENT row for a beta than
// for the stable line.
func TestPolicySnapshotOnlyInstallsWhereTheDocumentApplies(t *testing.T) {
	manifest := mergedPolicy(t)

	for _, tc := range []struct {
		name       string
		current    string
		wantMax    string
		wantErr    bool
		wantUnread bool
	}{
		{name: "the pre-beta.9 range", current: "v4.0.0-beta.1", wantMax: "3.7.0"},
		{name: "the beta.9 range", current: "v4.0.0-beta.9", wantMax: "3.8.5"},
		{name: "the stable line", current: "v4.0.0", wantMax: "3.8.5"},
		{name: "another major", current: "v3.9.2", wantErr: true, wantUnread: true},
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
	isolatedCompatCache(t, "v4.0.0")
	// A FILE where the directory should be. MkdirAll cannot succeed, so the write
	// fails the way a read-only data directory does — and, unlike a chmod, it fails
	// for root too, so the case means the same thing wherever it runs.
	blocker := filepath.Join(t.TempDir(), "not-a-dir")
	if err := os.WriteFile(blocker, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	SetCacheDir(filepath.Join(blocker, "data"))

	raw, err := os.ReadFile(filepath.Join("..", "..", "docs", "compat", "panel-ranges-v1.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := applyPanelRangesDocument(raw, time.Now().UTC()); err != nil {
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
				edited := bytes.Replace(snapshot.Payload, []byte(`"major":4`), []byte(`"major":3`), 1)
				snapshot.Payload = edited // digest deliberately NOT recomputed
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
				snapshot.Payload = bytes.Replace(snapshot.Payload, []byte(`"major":4`), []byte(`"major":3`), 1)
				sum := sha256.Sum256(snapshot.Payload)
				snapshot.Digest = hex.EncodeToString(sum[:])
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
				delete(snapshot, "payload")
				out, err := json.Marshal(snapshot)
				if err != nil {
					t.Fatal(err)
				}
				return out
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := isolatedCompatCache(t, "v4.0.0")
			writeSnapshot(t, dir, mergedPolicy(t))
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
	dir := isolatedCompatCache(t, "v4.0.0")
	snapshot := writeSnapshot(t, dir, mergedPolicy(t))

	if snapshot.SnapshotSchema != policySnapshotSchema {
		t.Fatalf("snapshot format = %d", snapshot.SnapshotSchema)
	}
	if snapshot.Revision == "" || snapshot.Source == "" || snapshot.FetchedAt.IsZero() || snapshot.Digest == "" {
		t.Fatalf("snapshot lost its provenance: %#v", snapshot)
	}
	if len(snapshot.Payload) == 0 {
		t.Fatal("snapshot stored no document")
	}

	// Replay installs, then a different major does not — without the file
	// having changed between the two.
	if err := LoadPolicySnapshot(); err != nil || ActiveMaxTestedXUI() != "3.8.5" {
		t.Fatalf("same-major replay: active=%q error=%v", ActiveMaxTestedXUI(), err)
	}
	SetActiveMaxTestedXUI("")
	Version = "v3.9.2"
	if err := LoadPolicySnapshot(); err == nil || ActiveMaxTestedXUI() != "" {
		t.Fatalf("cross-major replay: active=%q error=%v", ActiveMaxTestedXUI(), err)
	}
}

func TestPolicySnapshotMissingOrDisabledIsNotAnError(t *testing.T) {
	isolatedCompatCache(t, "v4.0.0")
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
	dir := isolatedCompatCache(t, "v4.0.0")
	writeSnapshot(t, dir, mergedPolicy(t))

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
	Version = "v4.0.0-beta.1"
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
	dir := isolatedCompatCache(t, "v4.0.0")
	writeSnapshot(t, dir, mergedPolicy(t))
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
