package version

import (
	"bytes"
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
	oldVersion, oldDir, oldMax := Version, getCacheDir(), ActiveMaxTestedXUI()
	dir := t.TempDir()
	Version = version
	SetCacheDir(dir)
	SetActiveMaxTestedXUI("")
	t.Cleanup(func() {
		Version = oldVersion
		SetCacheDir(oldDir)
		SetActiveMaxTestedXUI(oldMax)
	})
	return dir
}

func TestLoadCompatCacheIsolatesPSPMajors(t *testing.T) {
	for _, tc := range []struct {
		name, current, cached, max string
		want                       string
		wantError                  bool
	}{
		{name: "same release", current: "v4.0.0-beta.1", cached: "v4.0.0-beta.1", max: "3.7.0", want: "3.7.0"},
		{name: "same major patch", current: "v4.0.2", cached: "v4.0.1", max: "3.7.0", want: "3.7.0"},
		{name: "same major minor", current: "v4.1.0", cached: "v4.0.0-beta.1", max: "3.7.0", want: "3.7.0"},
		{name: "stable after beta", current: "4.0.0", cached: "v4.0.0-beta.1", max: "3.7.0", want: "3.7.0"},
		{name: "v3 to v4", current: "v4.0.0-beta.1", cached: "v3.9.2-beta.20", max: "3.7.0"},
		{name: "v4 to v3", current: "v3.9.2-beta.20", cached: "v4.0.0-beta.1", max: "3.7.0"},
		{name: "irrelevant malformed range", current: "v4.0.0", cached: "v3.9.2", max: "corrupt"},
		{name: "missing cached identity", current: "v4.0.0", max: "3.7.0"},
		{name: "unknown cached identity", current: "v4.0.0", cached: "dev", max: "3.7.0"},
		{name: "malformed cached identity", current: "v4.0.0", cached: "v4.invalid", max: "3.7.0"},
		{name: "unknown current identity", current: "dev", cached: "v4.0.0", max: "3.7.0"},
		{name: "invalid current identity", current: "v4.invalid", cached: "v4.0.0", max: "3.7.0"},
		{name: "zero major", current: "v0.0.0", cached: "v0.0.0", max: "3.7.0"},
		{name: "same major corrupt range", current: "v4.0.0", cached: "v4.0.1", max: "corrupt", wantError: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := isolatedCompatCache(t, tc.current)
			payload, err := json.Marshal(compatCachePayload{
				MaxTestedXUI: tc.max, CachedAt: time.Now().UTC(), PSPVersion: tc.cached,
			})
			if err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(dir, compatCacheFile)
			if err := os.WriteFile(path, payload, 0o600); err != nil {
				t.Fatal(err)
			}
			err = LoadCompatCache()
			if (err != nil) != tc.wantError {
				t.Fatalf("load error=%v, wantError=%v", err, tc.wantError)
			}
			if got := ActiveMaxTestedXUI(); got != tc.want {
				t.Fatalf("active range=%q, want %q", got, tc.want)
			}
			if tc.want == "" && CheckXUI("3.7.0") != CompatUnknown {
				t.Fatal("ignored cache must not establish a supported range")
			}
			after, err := os.ReadFile(path)
			if err != nil || !bytes.Equal(after, payload) {
				t.Fatalf("loader must not mutate the cache file: %v", err)
			}
		})
	}
}

func TestSaveCompatCacheBindsCurrentMajor(t *testing.T) {
	dir := isolatedCompatCache(t, "v4.0.0-beta.1")
	if err := saveCompatCache("3.7.0"); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(dir, compatCacheFile))
	if err != nil {
		t.Fatal(err)
	}
	var payload compatCachePayload
	if err := json.Unmarshal(raw, &payload); err != nil {
		t.Fatal(err)
	}
	if payload.PSPVersion != Version || payload.CachedAt.IsZero() {
		t.Fatalf("cache lost build provenance: %#v", payload)
	}
	Version = "v4.0.1"
	if err := LoadCompatCache(); err != nil || ActiveMaxTestedXUI() != "3.7.0" {
		t.Fatalf("same-major round trip: active=%q error=%v", ActiveMaxTestedXUI(), err)
	}
	SetActiveMaxTestedXUI("")
	Version = "v3.9.2"
	if err := LoadCompatCache(); err != nil || ActiveMaxTestedXUI() != "" {
		t.Fatalf("different-major round trip: active=%q error=%v", ActiveMaxTestedXUI(), err)
	}
}

func TestLoadCompatCacheMissingOrDisabled(t *testing.T) {
	isolatedCompatCache(t, "v4.0.0")
	if err := LoadCompatCache(); err != nil {
		t.Fatalf("missing optional cache: %v", err)
	}
	SetCacheDir("")
	if err := LoadCompatCache(); err != nil {
		t.Fatalf("disabled optional cache: %v", err)
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
