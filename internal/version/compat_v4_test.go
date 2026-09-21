package version

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"testing"
)

// THE PER-MAJOR MANIFEST IS THE LEGACY BUILD'S ROUTE, AND ONE IS LEFT.
//
// v3.json is the only per-major manifest this repository publishes: the v4 files
// it used to sit beside were replaced by one document per product, which a product
// build derives from its own major. v3.json is NOT part of that change — builds
// already in the field fetch it by a name their own version derives, so it stays
// readable and validated here.
func TestShippedCompatManifestRangesAndAdvisories(t *testing.T) {
	for _, major := range []int{3} {
		t.Run(fmt.Sprintf("v%d", major), func(t *testing.T) {
			payload := readCompatJSONForMajor(t, major)
			if payload.UpdatedAt == "" || len(payload.Entries) == 0 {
				t.Fatal("manifest must carry its update date and XUI range")
			}
			checkRange := func(label, min, max string) {
				t.Helper()
				lo, loOK := parseSemver(min)
				hi, hiOK := parseSemver(max)
				if !loOK || !hiOK || lo[0] != major || hi[0] != major || cmpSemver(lo, hi) > 0 {
					t.Errorf("%s invalid/cross-major range [%s..%s]", label, min, max)
				}
			}
			for i, e := range payload.Entries {
				checkRange(fmt.Sprintf("entries[%d]", i), e.PSPMin, e.PSPMax)
				lo, loOK := parseSemver(e.MinXUI)
				hi, hiOK := parseSemver(e.MaxTestedXUI)
				if !loOK || !hiOK || cmpSemver(lo, hi) > 0 {
					t.Errorf("entries[%d] unusable XUI bounds [%s..%s]", i, e.MinXUI, e.MaxTestedXUI)
				}
			}
			for i, e := range payload.SUIEntries {
				checkRange(fmt.Sprintf("sui_entries[%d]", i), e.PSPMin, e.PSPMax)
				hi, hiOK := parseSemver(e.MaxTestedSUI)
				if !hiOK {
					t.Errorf("sui_entries[%d] unusable SUI ceiling %q", i, e.MaxTestedSUI)
				}
				if e.MinSUI != "" {
					lo, loOK := parseSemver(e.MinSUI)
					if !loOK || cmpSemver(lo, hi) > 0 {
						t.Errorf("sui_entries[%d] unusable SUI floor %q", i, e.MinSUI)
					}
				}
			}
			for label, advisories := range map[string]map[string]XUIAdvisory{
				"xui_advisories": payload.Advisories, "sui_advisories": payload.SUIAdvisories,
			} {
				for key, advisory := range advisories {
					if _, ok := canonSemverKey(key); !ok || advisory.Text == "" || (advisory.Severity != "warning" && advisory.Severity != "info") {
						t.Errorf("%s[%q] invalid advisory", label, key)
					}
				}
			}
			sui, ok := lookupSUIForPSPVersion(payload, fmt.Sprintf("v%d.9.2", major))
			if !ok || sui.MaxTestedSUI != "1.6.3" || sui.MinSUI != "" {
				t.Fatalf("SUI 1.6.3 ceiling missing or an unverified floor was introduced: %#v found=%v", sui, ok)
			}
			if !payload.SUIAdvisories["1.6.0"].AffectsXray {
				t.Fatal("SUI 1.6.0 core migration warning must remain available")
			}
			for _, key := range []string{"1.6.1", "1.6.2"} {
				advisory, ok := payload.SUIAdvisories[key]
				if !ok || advisory.Severity != "info" || advisory.AffectsXray || advisory.Text == "" {
					t.Fatalf("SUI %s must retain its unchanged-core upgrade information", key)
				}
			}
			if advisory, ok := payload.SUIAdvisories["1.6.3"]; !ok || advisory.Severity != "info" || !advisory.AffectsXray || advisory.Text == "" {
				t.Fatal("SUI 1.6.3 must disclose its sing-box upgrade")
			}
			// The schema-v2 base remains conservative for historical binaries.
			// Current v4 builds opt into the prerelease-aware overlay below.
			xui, ok := lookupForPSPVersion(payload, fmt.Sprintf("v%d.9.2", major))
			if !ok || xui.MaxTestedXUI != "3.7.0" {
				t.Fatalf("unpatched release ceiling changed: %#v found=%v", xui, ok)
			}
			advisory, ok := payload.Advisories["3.8.0"]
			if !ok || advisory.Severity != "warning" || !advisory.AffectsXray || advisory.Text == "" {
				t.Fatal("XUI 3.8.0 must warn about the bundled-core upgrade and unpublished fix")
			}
			if advisory, ok := payload.Advisories["3.8.5"]; !ok || advisory.Severity != "warning" || !advisory.AffectsXray || advisory.Text == "" {
				t.Fatal("XUI 3.8.5 must retain the 3.8-series core upgrade warning")
			}
		})
	}
}

// THE PER-MAJOR MANIFEST AND ITS RANGE OVERLAY REMAIN A LIVE CODE PATH, and this
// repository no longer publishes a document that uses the overlay half: v3.json,
// the one remaining per-major manifest, carries its ranges directly.
//
// SO THE FIXTURE IS INLINE. A test that read a published file would be asserting
// something about that file's contents; this is about the folding itself — the
// base manifest is the conservative answer, the overlay replaces only the ranges,
// and the result carries the window the overlay was reviewed for. Nothing here
// would be a defect in a document.
func TestThePerMajorManifestAndItsRangeOverlay(t *testing.T) {
	const base = `{"schema_version":2,"major":3,"updated_at":"2026-09-19",
	  "range_overlay":"v3-ranges.json",
	  "entries":[{"psp_min":"v3.0.0","psp_max":"v3.99.99","min_xui":"3.4.2","max_tested_xui":"3.7.0"}],
	  "sui_entries":[{"psp_min":"v3.0.0","psp_max":"v3.99.99","max_tested_sui":"1.6.3"}],
	  "xui_advisories":{"3.7.0":{"severity":"warning","affects_xray":false,"text":"reviewed"}}}`
	const overlay = `{"schema_version":3,"major":3,"updated_at":"2026-09-20",
	  "entries":[{"psp_min":"v3.0.0","psp_max":"v3.99.99","min_xui":"3.4.2","max_tested_xui":"3.8.5"}],
	  "sui_entries":[{"psp_min":"v3.0.0","psp_max":"v3.99.99","max_tested_sui":"1.6.3"}]}`

	manifest, err := decodeCompatPayload([]byte(base))
	if err != nil {
		t.Fatal(err)
	}
	if manifest.RangeOverlay != "v3-ranges.json" {
		t.Fatalf("the base manifest lost its range overlay reference: %q", manifest.RangeOverlay)
	}
	// THE BASE IS THE CONSERVATIVE ANSWER for a build that cannot read the overlay.
	for _, version := range []string{"v3.0.0", "v3.9.2", "v3.99.99"} {
		xui, ok := lookupForPSPVersion(manifest, version)
		if !ok || xui.MinXUI != MinXUI || xui.MaxTestedXUI != "3.7.0" {
			t.Fatalf("base XUI contract for %q: %#v found=%v", version, xui, ok)
		}
		sui, ok := lookupSUIForPSPVersion(manifest, version)
		if !ok || sui.MinSUI != "" || sui.MaxTestedSUI != "1.6.3" {
			t.Fatalf("base SUI contract for %q: %#v found=%v", version, sui, ok)
		}
	}
	// AND THE LINE IS BOUNDED. These canonicalise to something, and none of them
	// lands inside the reviewed line.
	for _, version := range []string{"v2.99.99", "v4.0.0", "dev"} {
		if _, ok := lookupForPSPVersion(manifest, version); ok {
			t.Fatalf("the v3 entry leaked onto %q", version)
		}
		if _, ok := lookupSUIForPSPVersion(manifest, version); ok {
			t.Fatalf("the v3 SUI entry leaked onto %q", version)
		}
	}

	// THE OVERLAY REPLACES THE RANGES AND NOTHING ELSE, which is why the
	// advisories stay in the base document: an old reader that ignores the overlay
	// keeps the full upgrade guidance rather than losing it.
	folded, err := decodeCompatPayload([]byte(overlay))
	if err != nil {
		t.Fatal(err)
	}
	manifest.SchemaVersion = folded.SchemaVersion
	manifest.Entries = folded.Entries
	manifest.SUIEntries = folded.SUIEntries
	if advisory := manifest.Advisories["3.7.0"]; advisory.Text == "" {
		t.Fatal("folding the overlay dropped the base document's advisories")
	}
	for _, version := range []string{"v3.0.0", "v3.9.2"} {
		xui, ok := lookupForPSPVersion(manifest, version)
		if !ok || xui.MaxTestedXUI != "3.8.5" {
			t.Fatalf("the folded range is not in force for %q: %#v found=%v", version, xui, ok)
		}
	}
	// A BUILD COMPONENT IS NOT COVERED, AND THAT IS A GAP RATHER THAN A DESIGN.
	// The lookup canonicalises through x/mod/semver, which knows three segments, so
	// a version carrying the optional fourth matches no entry — and the panel would
	// report the ceiling as untested for exactly the builds the fourth segment was
	// added to distinguish. This asserts the gap so that closing it turns this case
	// red and the case is removed deliberately, rather than the gap being
	// remembered only by whoever wrote it.
	if xui, ok := lookupForPSPVersion(manifest, "4.0.0.1"); ok {
		t.Fatalf("the fourth segment is covered now (%#v); delete this case", xui)
	}
}

type compatV4RoundTripper func(*http.Request) (*http.Response, error)

func (f compatV4RoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

// Exercise the actual runtime fetch/apply path with local in-memory HTTP
// responses, ONE PER PRODUCT. No live panel or network is used, and this is not a
// V4 panel smoke.
func TestCompatV4FetchAppliesPublishedShape(t *testing.T) {
	dir := isolatedCompatCache(t, "4.0.0")
	oldClient := httpClient
	oldFloor, _ := activeMinXUI.Load().(string)
	oldSUIFloor, oldSUICeiling := ActiveMinSUI(), ActiveMaxTestedSUI()
	oldAdvisories, _ := activeAdvisories.Load().(map[string]XUIAdvisory)
	oldSUIAdvisories, _ := activeSUIAdvisories.Load().(map[string]XUIAdvisory)
	t.Cleanup(func() {
		httpClient = oldClient
		SetActiveMinXUI(oldFloor)
		SetActiveMinSUI(oldSUIFloor)
		SetActiveMaxTestedSUI(oldSUICeiling)
		SetActiveAdvisories(oldAdvisories)
		SetActiveSUIAdvisories(oldSUIAdvisories)
	})

	// BOTH DOCUMENTS THIS BUILD READS, not a per-major manifest, which a product
	// version cannot derive. The names carry the panel major.
	xuiName := fmt.Sprintf(xuiDocumentPattern, 4)
	suiName := fmt.Sprintf(suiDocumentPattern, 4)
	xuiRaw, err := os.ReadFile(filepath.Join("..", "..", "docs", "compat", xuiName))
	if err != nil {
		t.Fatal(err)
	}
	suiRaw, err := os.ReadFile(filepath.Join("..", "..", "docs", "compat", suiName))
	if err != nil {
		t.Fatal(err)
	}
	served := map[string][]byte{xuiName: xuiRaw, suiName: suiRaw}

	httpClient = &http.Client{Transport: compatV4RoundTripper(func(req *http.Request) (*http.Response, error) {
		if req.Header.Get("Accept") != "application/json" {
			t.Errorf("missing JSON accept header: %s", req.URL)
		}
		body, ok := served[path.Base(req.URL.Path)]
		if !ok {
			return nil, fmt.Errorf("wrong compat request: %s", req.URL)
		}
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(bytes.NewReader(body)), Header: make(http.Header)}, nil
	})}

	sources, err := compatDocumentSources()
	if err != nil {
		t.Fatal(err)
	}
	if err := fetchAndApplyAll(context.Background(), sources); err != nil {
		t.Fatal(err)
	}
	if ActiveMinXUI() != MinXUI || ActiveMaxTestedXUI() != "3.8.5" || ActiveMinSUI() != "" || ActiveMaxTestedSUI() != "1.6.3" {
		t.Fatal("runtime did not apply the V4 XUI/SUI bounds")
	}
	if a, ok := LookupXUIAdvisory("v3.7.0"); !ok || a.AffectsXray || a.Text == "" {
		t.Fatal("runtime lost the canonical XUI advisory")
	}
	if a, ok := LookupXUIAdvisory("v3.8.0"); !ok || !a.AffectsXray || a.Severity != "warning" || a.Text == "" {
		t.Fatal("runtime lost the XUI 3.8.0 core upgrade warning")
	}
	if a, ok := LookupSUIAdvisory("v1.6.0"); !ok || !a.AffectsXray || a.Text == "" {
		t.Fatal("runtime lost the canonical SUI advisory")
	}
	if a, ok := LookupSUIAdvisory("v1.6.3"); !ok || !a.AffectsXray || a.Severity != "info" || a.Text == "" {
		t.Fatal("runtime lost the SUI 1.6.3 core-upgrade advisory")
	}

	// The snapshot stores the VALIDATED DOCUMENTS and their digests, not bare
	// values: a reader replaying it must be able to re-run the same applicability
	// test rather than trust a conclusion whose premises are gone.
	cache, err := os.ReadFile(filepath.Join(dir, policySnapshotFile))
	if err != nil {
		t.Fatal(err)
	}
	var stored policySnapshot
	if err := json.Unmarshal(cache, &stored); err != nil {
		t.Fatal(err)
	}
	if stored.SnapshotSchema != policySnapshotSchema || len(stored.Documents) != 2 {
		t.Fatalf("the snapshot is not a container of one document per product: %#v", stored)
	}
	for product, document := range stored.Documents {
		sum := sha256.Sum256(document.Payload)
		if document.Digest != hex.EncodeToString(sum[:]) {
			t.Fatalf("%s: snapshot integrity fields are wrong: %#v", product, document)
		}
		var replayed remoteCompatPayload
		if err := json.Unmarshal(document.Payload, &replayed); err != nil || replayed.UpdatedAt == "" {
			t.Fatalf("%s: snapshot payload is not the applied document: %v", product, err)
		}
		// THE PRODUCT TRAVELS WITH THE PAYLOAD, which is what lets a boot replay
		// each document through the install path that matches it.
		if replayed.Product != product {
			t.Fatalf("the snapshot keys %s by %q", product, replayed.Product)
		}
		// THE WINDOW, NOT A MAJOR. A product document states the builds it applies
		// to rather than being named after a compatibility major, so this is what
		// the snapshot has to carry: a boot replays the document and re-runs the
		// applicability test.
		if replayed.AppliesToPSP == nil || replayed.AppliesToPSP.Min != "4.0.0" {
			t.Fatalf("%s: snapshot payload declares window=%+v, want the published 4.0.0 floor", product, replayed.AppliesToPSP)
		}
	}

	// AND A DOCUMENT FOR ANOTHER LINE CANNOT REPLACE THIS ONE. The refusal comes
	// from the document's own window, which is the form this build can reach: it
	// asks for its own names, so a document for another build arrives only because
	// its window says so. The range and the snapshot must survive it.
	for name, raw := range served {
		moved := bytes.Replace(raw, []byte(`"min": "4.0.0"`), []byte(`"min": "9.0.0"`), 1)
		if bytes.Equal(moved, raw) {
			t.Fatal("the served document carries no window to move; this case would pass vacuously")
		}
		served[name] = moved
	}
	SetActiveMaxTestedXUI("3.5.0")
	if err := fetchAndApplyAll(context.Background(), sources); err == nil {
		t.Fatal("runtime accepted documents for another release line")
	}
	if ActiveMaxTestedXUI() != "3.5.0" {
		t.Fatal("a refused fetch changed active state")
	}
	after, err := os.ReadFile(filepath.Join(dir, policySnapshotFile))
	if err != nil || !bytes.Equal(after, cache) {
		t.Fatalf("a refused fetch changed the persisted snapshot: %v", err)
	}
}
