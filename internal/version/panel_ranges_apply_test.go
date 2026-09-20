package version

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// APPLYING A PANEL RANGES DOCUMENT, and replaying it from the cache.
//
// The interesting half is the SECOND one. A document that installs from a fetch
// and then fails to install from the cache leaves a range in force that the next
// boot silently drops — the panel would report a supported ceiling until it
// restarted and then stop. Both paths go through payloadApplies for that reason,
// and these tests drive both.
func panelRangesJSON(min, max string) []byte {
	return []byte(fmt.Sprintf(`{
	  "schema_version": 1, "revision": 3,
	  "issued_at": "2026-09-19T00:00:00Z", "expires_at": "2027-09-19T00:00:00Z",
	  "applies_to_psp": {"min": %q, "max": %q},
	  "entries": [{"psp_min": %q, "psp_max": %q, "min_xui": "3.4.2", "max_tested_xui": "3.7.0", "notes": "reviewed"}],
	  "sui_entries": [{"psp_min": %q, "psp_max": %q, "max_tested_sui": "1.6.3", "notes": "reviewed"}]
	}`, min, max, min, max, min, max))
}

var panelRangesApplyNow = time.Date(2026, 9, 20, 0, 0, 0, 0, time.UTC)

func TestAPanelRangesDocumentInstallsForTheBuildItNames(t *testing.T) {
	// A PRODUCT version: three integers, no prefix. This is the build that could
	// reach no ranges at all before the document existed.
	isolatedCompatCache(t, "4.0.0")
	if got := ActiveMaxTestedXUI(); got != "" {
		t.Fatalf("ceiling before install = %q", got)
	}
	if err := applyPanelRangesDocument(panelRangesJSON("4.0.0", "4.99.99"), panelRangesApplyNow); err != nil {
		t.Fatalf("the document names this build: %v", err)
	}
	if got := ActiveMaxTestedXUI(); got != "3.7.0" {
		t.Fatalf("ceiling = %q, want 3.7.0", got)
	}
	if got := ActiveMaxTestedSUI(); got != "1.6.3" {
		t.Fatalf("sui ceiling = %q, want 1.6.3", got)
	}
}

func TestAPanelRangesDocumentIsRefusedForABuildItDoesNotName(t *testing.T) {
	isolatedCompatCache(t, "5.0.0")
	err := applyPanelRangesDocument(panelRangesJSON("4.0.0", "4.99.99"), panelRangesApplyNow)
	if err == nil {
		t.Fatal("a document that names another window installed here")
	}
	// The message has to say which window, or an operator cannot tell a
	// mispublished document from a broken one.
	for _, want := range []string{"4.0.0", "4.99.99", "5.0.0"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("refusal %q does not mention %q", err, want)
		}
	}
	if got := ActiveMaxTestedXUI(); got != "" {
		t.Fatalf("a refused document established a ceiling: %q", got)
	}
}

// The cache must reach the SAME conclusion the fetch did. A document cached by
// one build and read by another is the case that separates the two paths.
func TestTheCachedRangesDocumentReplaysTheSameDecision(t *testing.T) {
	raw := panelRangesJSON("4.0.0", "4.99.99")

	for _, tc := range []struct {
		name    string
		build   string
		wantMax string
		wantErr bool
	}{
		{name: "the build the document names", build: "4.0.0", wantMax: "3.7.0"},
		{name: "a later release line", build: "5.0.0", wantErr: true},
		{name: "outside the window", build: "3.9.2", wantErr: true},
		{name: "no release identity at all", build: "dev", wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := isolatedCompatCache(t, tc.build)
			// Written as the fetch would have written it: the converted payload,
			// carrying its window.
			policy, err := ParsePanelRangesPolicy(raw, panelRangesApplyNow)
			if err != nil {
				t.Fatal(err)
			}
			window := policy.AppliesToPSP
			writeSnapshot(t, dir, remoteCompatPayload{
				SchemaVersion: schemaVersion, UpdatedAt: "2026-09-19T00:00:00Z",
				Entries: policy.Entries, SUIEntries: policy.SUIEntries, AppliesToPSP: &window,
			})
			if got := ActiveMaxTestedXUI(); got != "" {
				t.Fatalf("ceiling before load = %q", got)
			}

			err = LoadPolicySnapshot()
			if (err != nil) != tc.wantErr {
				t.Fatalf("load error = %v, wantError = %v", err, tc.wantErr)
			}
			if got := ActiveMaxTestedXUI(); got != tc.wantMax {
				t.Fatalf("ceiling = %q, want %q", got, tc.wantMax)
			}
		})
	}
}

// An expired document is refused by the strict validator before anything is
// installed, which is the reason the document is parsed by its own rules rather
// than as a payload: the payload shape has no expiry to check.
func TestAnExpiredRangesDocumentInstallsNothing(t *testing.T) {
	isolatedCompatCache(t, "4.0.0")
	expired := []byte(`{
	  "schema_version": 1, "revision": 3,
	  "issued_at": "2026-01-01T00:00:00Z", "expires_at": "2026-02-01T00:00:00Z",
	  "applies_to_psp": {"min": "4.0.0", "max": "4.99.99"},
	  "entries": [{"psp_min": "4.0.0", "psp_max": "4.99.99", "min_xui": "3.4.2", "max_tested_xui": "3.7.0"}]
	}`)
	err := applyPanelRangesDocument(expired, panelRangesApplyNow)
	if err == nil || !strings.Contains(err.Error(), "expired") {
		t.Fatalf("error = %v, want an expiry refusal", err)
	}
	if got := ActiveMaxTestedXUI(); got != "" {
		t.Fatalf("an expired document established a ceiling: %q", got)
	}
}

// A build pointed at a PER-MAJOR manifest has no major to match it by, and the
// refusal has to say what it should read instead.
//
// That situation is one this design creates: a URL override can point anywhere,
// and before the named document existed there was nothing to point a major-less
// build at — so "cannot derive a major" was the whole truth. It is now half of
// it, and the missing half is the actionable one.
func TestAMajorlessBuildPointedAtAManifestIsToldWhatToRead(t *testing.T) {
	isolatedCompatCache(t, "4.0.0")
	manifest := []byte(`{"schema_version": 2, "major": 4, "updated_at": "2026-09-19",
	  "entries": [{"psp_min": "v4.0.0", "psp_max": "v4.99.99", "min_xui": "3.4.2", "max_tested_xui": "3.7.0"}]}`)

	err := applyPerMajorManifest(t.Context(), manifest, "https://example.test/v4.json")
	if err == nil {
		t.Fatal("a build with no derivable major accepted a per-major manifest")
	}
	if !strings.Contains(err.Error(), panelRangesDocumentName) {
		t.Fatalf("the refusal does not name the document that would work: %v", err)
	}
	if got := ActiveMaxTestedXUI(); got != "" {
		t.Fatalf("a refused manifest established a ceiling: %q", got)
	}
}

// THE SAME BUILD MUST GET THE SAME CEILING WHICHEVER DOCUMENT IT REACHES.
//
// A legacy build folds v4.json's range overlay in; a product build reads the
// named document. Both must land on the ceiling the reviewer actually approved,
// and the failure of that is not a crash: it is one panel reporting a range as
// supported while the other calls it untested. So this drives the shipped
// document through the ordinary apply path and asserts the number.
func TestTheShippedDocumentYieldsTheReviewedCeiling(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "docs", "compat", "panel-ranges-v1.json"))
	if err != nil {
		t.Fatal(err)
	}
	isolatedCompatCache(t, "4.0.0")
	if err := applyPanelRangesDocument(raw, panelRangesApplyNow); err != nil {
		t.Fatalf("the shipped document does not apply to 4.0.0: %v", err)
	}
	// 3.8.5 is the ceiling in docs/compat/v4-ranges.json for the stable line. If
	// this reads 3.7.0 the document was published from the base entries and is
	// understating the review.
	if got := ActiveMaxTestedXUI(); got != "3.8.5" {
		t.Fatalf("ceiling = %q, want the reviewed 3.8.5", got)
	}
}

// The name the code fetches and the name the repository publishes must be the
// same file.
//
// Nothing else connects them: the constant is a string and the document is a path,
// and a rename on one side makes the fetch 404 — which lands as a build that
// reports its ceilings as unknown, with the cause several layers away from the
// symptom. Cheap to assert, and the alternative is discovering it in a release.
func TestTheFetchedDocumentNameIsThePublishedOne(t *testing.T) {
	path := filepath.Join("..", "..", "docs", "compat", panelRangesDocumentName)
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("the code fetches %q but the repository publishes no such document: %v", panelRangesDocumentName, err)
	}
	// And the URL the code builds must point at it, not at a path that merely
	// looks similar.
	previous := Version
	t.Cleanup(func() { Version = previous })
	Version = "4.0.0"
	url, err := defaultURLForCurrentVersion()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(url, "/docs/compat/"+panelRangesDocumentName) {
		t.Fatalf("URL = %q, want it to end in /docs/compat/%s", url, panelRangesDocumentName)
	}
}
