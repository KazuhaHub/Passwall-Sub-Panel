package version

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// APPLYING THE PER-PRODUCT RANGES DOCUMENTS, and replaying them from the cache.
//
// The interesting half is the SECOND one. A document that installs from a fetch
// and then fails to install from the cache leaves a range in force that the next
// boot silently drops — the panel would report a supported ceiling until it
// restarted and then stop. Both paths go through payloadApplies for that reason,
// and these tests drive both.
//
// EACH TEST DRIVES BOTH DOCUMENTS, because that is the shape the apply path has
// now: one document per product, so "the ranges" is two installs and a test that
// checked one would not notice the other no longer arriving.
func xuiPanelRangesJSON(min, max string) []byte {
	return []byte(fmt.Sprintf(`{
	  "schema_version": 1, "product": "3x-ui", "revision": 3,
	  "issued_at": "2026-09-19T00:00:00Z", "expires_at": "2027-09-19T00:00:00Z",
	  "applies_to_psp": {"min": %q, "max": %q},
	  "entries": [{"psp_min": %q, "psp_max": %q, "min_xui": "3.4.2", "max_tested_xui": "3.7.0", "notes": "reviewed"}]
	}`, min, max, min, max))
}

func suiPanelRangesJSON(min, max string) []byte {
	return []byte(fmt.Sprintf(`{
	  "schema_version": 1, "product": "sui", "revision": 3,
	  "issued_at": "2026-09-19T00:00:00Z", "expires_at": "2027-09-19T00:00:00Z",
	  "applies_to_psp": {"min": %q, "max": %q},
	  "sui_entries": [{"psp_min": %q, "psp_max": %q, "max_tested_sui": "1.6.3", "notes": "reviewed"}]
	}`, min, max, min, max))
}

var panelRangesApplyNow = time.Date(2026, 9, 20, 0, 0, 0, 0, time.UTC)

// applyTestDocuments installs the pair of documents a build at the given window
// would receive, through the two entry points the fetch uses.
func applyTestDocuments(t *testing.T, min, max string) error {
	t.Helper()
	if err := applyXUICompatDocument(xuiPanelRangesJSON(min, max), panelRangesApplyNow); err != nil {
		return err
	}
	return applySUICompatDocument(suiPanelRangesJSON(min, max), panelRangesApplyNow)
}

func TestTheRangesDocumentsInstallForTheBuildTheyName(t *testing.T) {
	// A PRODUCT version: three integers, no prefix. This is the build these
	// documents are written for, and the two ceilings arrive from two files.
	isolatedCompatCache(t, "4.0.0")
	if got := ActiveMaxTestedXUI(); got != "" {
		t.Fatalf("ceiling before install = %q", got)
	}
	if err := applyTestDocuments(t, "4.0.0", "4.99.99"); err != nil {
		t.Fatalf("the documents name this build: %v", err)
	}
	if got := ActiveMaxTestedXUI(); got != "3.7.0" {
		t.Fatalf("ceiling = %q, want 3.7.0", got)
	}
	if got := ActiveMaxTestedSUI(); got != "1.6.3" {
		t.Fatalf("sui ceiling = %q, want 1.6.3", got)
	}
}

func TestARangesDocumentIsRefusedForABuildItDoesNotName(t *testing.T) {
	isolatedCompatCache(t, "5.0.0")
	err := applyTestDocuments(t, "4.0.0", "4.99.99")
	if err == nil {
		t.Fatal("documents that name another window installed here")
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
func TestTheCachedRangesDocumentsReplayTheSameDecision(t *testing.T) {
	xui, err := ParsePanelRangesPolicy(xuiPanelRangesJSON("4.0.0", "4.99.99"), panelRangesApplyNow)
	if err != nil {
		t.Fatal(err)
	}
	sui, err := ParsePanelRangesPolicy(suiPanelRangesJSON("4.0.0", "4.99.99"), panelRangesApplyNow)
	if err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct {
		name    string
		build   string
		wantMax string
		wantErr bool
	}{
		{name: "the build the documents name", build: "4.0.0", wantMax: "3.7.0"},
		{name: "a later release line", build: "5.0.0", wantErr: true},
		{name: "outside the window", build: "3.9.2", wantErr: true},
		{name: "no release identity at all", build: "dev", wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := isolatedCompatCache(t, tc.build)
			// Written as the fetch would have written them: the converted payloads,
			// each carrying its window and its product, merged into one container.
			writeSnapshot(t, dir, shippedPayload(xui))
			writeSnapshot(t, dir, shippedPayload(sui))
			if got := ActiveMaxTestedXUI(); got != "" {
				t.Fatalf("ceiling before load = %q", got)
			}

			err := LoadPolicySnapshot()
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
	  "schema_version": 1, "product": "3x-ui", "revision": 3,
	  "issued_at": "2026-01-01T00:00:00Z", "expires_at": "2026-02-01T00:00:00Z",
	  "applies_to_psp": {"min": "4.0.0", "max": "4.99.99"},
	  "entries": [{"psp_min": "4.0.0", "psp_max": "4.99.99", "min_xui": "3.4.2", "max_tested_xui": "3.7.0"}]
	}`)
	err := applyXUICompatDocument(expired, panelRangesApplyNow)
	if err == nil || !strings.Contains(err.Error(), "expired") {
		t.Fatalf("error = %v, want an expiry refusal", err)
	}
	if got := ActiveMaxTestedXUI(); got != "" {
		t.Fatalf("an expired document established a ceiling: %q", got)
	}
}

// A build pointed at a PER-MAJOR manifest has no major to match it by, and the
// refusal has to say what such a build reads instead.
//
// That situation is one this design creates: a URL override can point anywhere,
// and a product build derives per-product document names rather than a
// compatibility major — so "cannot derive a major" is only half the answer, and
// the missing half is the actionable one.
func TestAMajorlessBuildPointedAtAManifestIsToldWhatToRead(t *testing.T) {
	isolatedCompatCache(t, "4.0.0")
	manifest := []byte(`{"schema_version": 2, "major": 4, "updated_at": "2026-09-19",
	  "entries": [{"psp_min": "v4.0.0", "psp_max": "v4.99.99", "min_xui": "3.4.2", "max_tested_xui": "3.7.0"}]}`)

	err := applyPerMajorManifest(t.Context(), manifest, "https://example.test/v4.json")
	if err == nil {
		t.Fatal("a build with no derivable major accepted a per-major manifest")
	}
	if !strings.Contains(err.Error(), "one document per product") {
		t.Fatalf("the refusal does not say what such a build reads: %v", err)
	}
	if got := ActiveMaxTestedXUI(); got != "" {
		t.Fatalf("a refused manifest established a ceiling: %q", got)
	}
}

// THE SHIPPED DOCUMENT YIELDS THE REVIEWED CEILING.
//
// The number is what a reviewer actually approved, and the failure this guards is
// not a crash: it is a panel reporting a range as supported while the reviewer
// called it untested. So this drives the shipped documents through the ordinary
// apply path and asserts the numbers.
func TestTheShippedDocumentsYieldTheReviewedCeilings(t *testing.T) {
	isolatedCompatCache(t, "4.0.0")
	for _, tc := range []struct {
		name    string
		apply   func([]byte, time.Time) error
		wantMax string
	}{
		{name: "3x-ui", apply: applyXUICompatDocument, wantMax: "3.8.5"},
		{name: "sui", apply: applySUICompatDocument, wantMax: "1.6.3"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			name := fmt.Sprintf(map[string]string{"3x-ui": xuiDocumentPattern, "sui": suiDocumentPattern}[tc.name], 4)
			raw, err := os.ReadFile(filepath.Join("..", "..", "docs", "compat", name))
			if err != nil {
				t.Fatal(err)
			}
			if err := tc.apply(raw, panelRangesApplyNow); err != nil {
				t.Fatalf("the shipped %s document does not apply to 4.0.0: %v", name, err)
			}
			got := ActiveMaxTestedXUI()
			if tc.name == "sui" {
				got = ActiveMaxTestedSUI()
			}
			if got != tc.wantMax {
				t.Fatalf("ceiling = %q, want the reviewed %s", got, tc.wantMax)
			}
		})
	}
	// AND THE ONE PRODUCT'S DOCUMENT DID NOT TOUCH THE OTHER'S STATE. The S-UI
	// installer clears the S-UI bounds when no row matches, so running it over a
	// 3X-UI document would erase a ceiling as a side effect of editing the other.
	isolatedCompatCache(t, "4.0.0")
	raw, err := os.ReadFile(filepath.Join("..", "..", "docs", "compat", fmt.Sprintf(xuiDocumentPattern, 4)))
	if err != nil {
		t.Fatal(err)
	}
	if err := applyXUICompatDocument(raw, panelRangesApplyNow); err != nil {
		t.Fatal(err)
	}
	if got := ActiveMaxTestedSUI(); got != "" {
		t.Fatalf("a 3X-UI document established an S-UI ceiling: %q", got)
	}
}

// The names the code fetches and the names the repository publishes must be the
// same files.
//
// Nothing else connects them: the pattern is a string and the documents are paths,
// and a rename on one side makes the fetch 404 — which lands as a build that
// reports its ceilings as unknown, with the cause several layers away from the
// symptom. Cheap to assert, and the alternative is discovering it in a release.
func TestTheFetchedDocumentNamesAreThePublishedOnes(t *testing.T) {
	previous := Version
	t.Cleanup(func() { Version = previous })
	Version = "4.0.0"

	sources, err := compatDocumentSources()
	if err != nil {
		t.Fatal(err)
	}
	if len(sources) != 2 {
		t.Fatalf("a product build reads %d documents, want one per product", len(sources))
	}
	for _, source := range sources {
		if _, err := os.Stat(filepath.Join("..", "..", "docs", "compat", source.Name)); err != nil {
			t.Fatalf("the code fetches %q but the repository publishes no such document: %v", source.Name, err)
		}
		// And the URL the code builds must point at it, not at a path that merely
		// looks similar.
		if !strings.HasSuffix(source.URL, "/docs/compat/"+source.Name) {
			t.Fatalf("URL = %q, want it to end in /docs/compat/%s", source.URL, source.Name)
		}
	}
}
