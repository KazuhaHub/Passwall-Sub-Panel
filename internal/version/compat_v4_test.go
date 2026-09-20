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
	"path/filepath"
	"testing"
)

func TestShippedCompatManifestRangesAndAdvisories(t *testing.T) {
	for _, major := range []int{3, 4} {
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

func TestCompatV4ReleaseRange(t *testing.T) {
	payload := readCompatJSONForMajor(t, 4)
	if payload.RangeOverlay != "v4-ranges.json" {
		t.Fatalf("V4 base manifest lost its prerelease-aware range overlay: %q", payload.RangeOverlay)
	}
	for _, version := range []string{"v4.0.0-beta.1", "4.0.0", "4.0.1", "v4.99.99"} {
		xui, ok := lookupForPSPVersion(payload, version)
		if !ok || xui.MinXUI != MinXUI || xui.MaxTestedXUI != "3.7.0" {
			t.Fatalf("V4 initial XUI contract for %q: %#v found=%v", version, xui, ok)
		}
		sui, ok := lookupSUIForPSPVersion(payload, version)
		if !ok || sui.MinSUI != "" || sui.MaxTestedSUI != "1.6.3" {
			t.Fatalf("V4 must retain the verified SUI ceiling without inventing a floor: %q %#v found=%v", version, sui, ok)
		}
	}
	for _, version := range []string{"v3.99.99", "v5.0.0", "dev"} {
		if _, ok := lookupForPSPVersion(payload, version); ok {
			t.Fatalf("V4 XUI entry leaked onto %q", version)
		}
		if _, ok := lookupSUIForPSPVersion(payload, version); ok {
			t.Fatalf("V4 SUI entry leaked onto %q", version)
		}
	}
	for _, key := range []string{"3.5.0", "3.6.0", "3.7.0"} {
		if payload.Advisories[key].Text == "" {
			t.Fatalf("V4 lost existing XUI upgrade warning %q", key)
		}
	}
	if payload.Advisories["3.7.0"].AffectsXray || !payload.SUIAdvisories["1.6.0"].AffectsXray {
		t.Fatal("inherited core restart advisories changed meaning")
	}
}

// THE OVERLAY COVERS THE RELEASE LINE, AND NOTHING FINER.
//
// It used to be prerelease-aware: a row for beta.9 and up on 3.8.5 and a narrower
// one for beta.1 through beta.8 on 3.7.0, so a panel reported the ceiling its own
// build had earned. The legacy scheme is gone, the beta line is not a set of
// identities any more, and what is left is the property the finer rows existed to
// protect — that the ceiling a build is given is a reviewed one. There is one
// review now, so there is one answer.
func TestCompatV4RangeOverlay(t *testing.T) {
	payload := readCompatRangeOverlay(t)
	for _, version := range []string{"4.0.0", "4.0.1", "4.99.99"} {
		xui, ok := lookupForPSPVersion(payload, version)
		if !ok || xui.MinXUI != MinXUI || xui.MaxTestedXUI != "3.8.5" {
			t.Fatalf("v4 range missing for %q: %#v found=%v", version, xui, ok)
		}
		sui, ok := lookupSUIForPSPVersion(payload, version)
		if !ok || sui.MinSUI != "" || sui.MaxTestedSUI != "1.6.3" {
			t.Fatalf("SUI overlay range missing for %q: %#v found=%v", version, sui, ok)
		}
	}
	// AND THE LINE IS BOUNDED. These canonicalise to something, and none of them
	// lands inside the reviewed line: the two outside it belong to other release
	// lines, and the legacy stamp belongs BELOW it, where it used to have a row
	// of its own.
	for _, version := range []string{"3.9.2", "5.0.0", "v4.0.0-beta.9", "v4.0.0-beta.1"} {
		if xui, ok := lookupForPSPVersion(payload, version); ok {
			t.Fatalf("a version outside the reviewed line was certified: %q -> %#v", version, xui)
		}
	}
	// A BUILD COMPONENT IS NOT COVERED, AND THAT IS A GAP RATHER THAN A DESIGN.
	// The lookup canonicalises through x/mod/semver, which knows three segments,
	// so a version carrying the optional fourth matches no entry — and the panel
	// would report the ceiling as untested for exactly the builds the fourth
	// segment was added to distinguish. This asserts the gap so that closing it
	// turns this case red and the case is removed deliberately, rather than the
	// gap being remembered only by whoever wrote it.
	if xui, ok := lookupForPSPVersion(payload, "4.0.0.1"); ok {
		t.Fatalf("the fourth segment is covered now (%#v); delete this case", xui)
	}
}

type compatV4RoundTripper func(*http.Request) (*http.Response, error)

func (f compatV4RoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

// Exercise the actual runtime fetch/apply path with a local in-memory HTTP
// response. No live panel or network is used, and this is not a V4 panel smoke.
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
	// THE DOCUMENT THIS BUILD FETCHES, not the per-major one it cannot derive.
	raw, err := os.ReadFile(filepath.Join("..", "..", "docs", "compat", "panel-ranges-v1.json"))
	if err != nil {
		t.Fatal(err)
	}
	httpClient = &http.Client{Transport: compatV4RoundTripper(func(req *http.Request) (*http.Response, error) {
		if req.Header.Get("Accept") != "application/json" {
			t.Errorf("missing JSON accept header: %s", req.URL)
		}
		// THE NAMED DOCUMENT, WHICH IS THE ONLY ONE THIS BUILD FETCHES. A product
		// version has no derivable compatibility major, so the per-major route —
		// v4.json with its overlay folded in — is not one this build can take,
		// and the URL the fetch composes is the document's own name.
		if req.URL.String() != defaultRemoteCompatURLBase+"panel-ranges-v1.json" {
			return nil, fmt.Errorf("wrong compat request: %s", req.URL)
		}
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(bytes.NewReader(raw)), Header: make(http.Header)}, nil
	})}
	url, err := defaultURLForCurrentVersion()
	if err != nil {
		t.Fatal(err)
	}
	if err := fetchAndApply(context.Background(), url); err != nil {
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
	if a, ok := LookupSUIAdvisory("v1.6.1"); !ok || a.AffectsXray || a.Severity != "info" || a.Text == "" {
		t.Fatal("runtime lost the SUI 1.6.1 maintenance/login advisory")
	}
	if a, ok := LookupSUIAdvisory("v1.6.2"); !ok || a.AffectsXray || a.Severity != "info" || a.Text == "" {
		t.Fatal("runtime lost the SUI 1.6.2 settings-save advisory")
	}
	if a, ok := LookupSUIAdvisory("v1.6.3"); !ok || !a.AffectsXray || a.Severity != "info" || a.Text == "" {
		t.Fatal("runtime lost the SUI 1.6.3 core-upgrade advisory")
	}
	// The snapshot stores the VALIDATED DOCUMENT and its digest, not a bare
	// value: a reader replaying it must be able to re-run the same applicability
	// test rather than trust a conclusion whose premises are gone.
	cache, err := os.ReadFile(filepath.Join(dir, policySnapshotFile))
	if err != nil {
		t.Fatal(err)
	}
	var stored policySnapshot
	if err := json.Unmarshal(cache, &stored); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(stored.Payload)
	if stored.SnapshotSchema != policySnapshotSchema || stored.Digest != hex.EncodeToString(sum[:]) {
		t.Fatalf("snapshot integrity fields are wrong: %#v", stored)
	}
	var replayed remoteCompatPayload
	if err := json.Unmarshal(stored.Payload, &replayed); err != nil || replayed.UpdatedAt == "" {
		t.Fatalf("snapshot payload is not the applied document: %v", err)
	}
	// THE WINDOW, NOT A MAJOR. A panel ranges document states the builds it
	// applies to rather than being named after one, so this is what the snapshot
	// has to carry: a boot replays the document and re-runs the applicability
	// test, rather than trusting a conclusion whose premises are gone.
	if replayed.AppliesToPSP == nil || replayed.AppliesToPSP.Min != "4.0.0" {
		t.Fatalf("snapshot payload declares window=%+v, want the published 4.0.0 floor", replayed.AppliesToPSP)
	}

	// AND A DOCUMENT FOR ANOTHER LINE CANNOT REPLACE THIS ONE. This used to serve
	// the per-major v3.json and rely on its `major` not matching. A product build
	// derives no major and never asks for that file, so the refusal now comes a
	// step earlier — from the document's own window, which is the same guard in
	// the only form this build can reach. The range and the snapshot must survive
	// it either way.
	wrong := bytes.Replace(raw, []byte(`"min": "4.0.0"`), []byte(`"min": "9.0.0"`), 1)
	if bytes.Equal(wrong, raw) {
		t.Fatal("the served document carries no window to move; this case would pass vacuously")
	}
	raw = wrong
	SetActiveMaxTestedXUI("3.5.0")
	if err := fetchAndApply(context.Background(), url); err == nil {
		t.Fatal("runtime accepted a document for another release line")
	}
	if ActiveMaxTestedXUI() != "3.5.0" {
		t.Fatal("a refused fetch changed active state")
	}
	after, err := os.ReadFile(filepath.Join(dir, policySnapshotFile))
	if err != nil || !bytes.Equal(after, cache) {
		t.Fatalf("a refused fetch changed the persisted snapshot: %v", err)
	}
}

func readCompatRangeOverlay(t *testing.T) remoteCompatPayload {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "..", "docs", "compat", "v4-ranges.json"))
	if err != nil {
		t.Fatal(err)
	}
	var payload remoteCompatPayload
	if err := json.Unmarshal(raw, &payload); err != nil {
		t.Fatal(err)
	}
	if payload.SchemaVersion != rangeOverlaySchemaVersion || payload.Major != 4 {
		t.Fatalf("unexpected V4 range overlay identity: schema=%d major=%d", payload.SchemaVersion, payload.Major)
	}
	return payload
}
