package version

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// TestMinXUIConstMatchesCompatJSON is the "don't forget to sync" guard: the
// compiled MinXUI backstop and each shipped major's operational min_xui are
// TWO places describing the same floor, and the v3.6.2 bug was editing one
// (the JSON) while forgetting the other (the const), leaving 3.1.0 panels
// un-flagged. This test fails the build the moment they drift, so editing the
// JSON without bumping the const (or vice versa) is caught by `go test` before
// release — correctness no longer relies on remembering.
//
// It checks the entry covering the NEWEST PSP versions (largest psp_max) — the
// one the latest build resolves to, hence the one whose min_xui must equal the
// compiled floor. Older entries (e.g. the v3.6.0–v3.6.1 baseline) keep their
// own historical min_xui and are intentionally not checked here.
func TestMinXUIConstMatchesCompatJSON(t *testing.T) {
	for _, major := range []int{3, 4} {
		t.Run(fmt.Sprintf("v%d", major), func(t *testing.T) {
			payload := readCompatJSONForMajor(t, major)
			if len(payload.Entries) == 0 {
				t.Fatal("compat manifest has no entries")
			}

			// Historical entries intentionally keep their own older floor.
			newest := payload.Entries[0]
			newestMax, ok := parseSemver(newest.PSPMax)
			if !ok {
				t.Fatalf("entry [%s..%s] has unparseable psp_max", newest.PSPMin, newest.PSPMax)
			}
			for _, e := range payload.Entries[1:] {
				hi, ok := parseSemver(e.PSPMax)
				if !ok {
					t.Fatalf("entry [%s..%s] has unparseable psp_max", e.PSPMin, e.PSPMax)
				}
				if cmpSemver(hi, newestMax) > 0 {
					newest, newestMax = e, hi
				}
			}
			if newest.MinXUI != MinXUI {
				t.Fatalf("DRIFT: MinXUI=%q but v%d.json newest entry [%s..%s] min_xui=%q; keep the compiled backstop and active-major manifest in step",
					MinXUI, major, newest.PSPMin, newest.PSPMax, newest.MinXUI)
			}
		})
	}
}

func readCompatJSONForMajor(t *testing.T, major int) remoteCompatPayload {
	t.Helper()
	path := filepath.Join("..", "..", "docs", "compat", fmt.Sprintf("v%d.json", major))
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v (this guard must be able to read the compat JSON)", path, err)
	}
	var payload remoteCompatPayload
	if err := json.Unmarshal(raw, &payload); err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	if payload.SchemaVersion != schemaVersion || payload.Major != major {
		t.Fatalf("%s declares schema=%d major=%d, want schema=%d major=%d",
			path, payload.SchemaVersion, payload.Major, schemaVersion, major)
	}
	return payload
}
