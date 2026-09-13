package version

import (
	"bytes"
	"context"
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
			if !ok || sui.MaxTestedSUI != "1.6.1" || sui.MinSUI != "" {
				t.Fatalf("SUI 1.6.1 ceiling missing or an unverified floor was introduced: %#v found=%v", sui, ok)
			}
			if !payload.SUIAdvisories["1.6.0"].AffectsXray {
				t.Fatal("SUI 1.6.0 core migration warning must remain available")
			}
			advisory, ok := payload.SUIAdvisories["1.6.1"]
			if !ok || advisory.Severity != "info" || advisory.AffectsXray || advisory.Text == "" {
				t.Fatal("SUI 1.6.1 advisory must describe the unchanged core and new maintenance/login behaviour")
			}
		})
	}
}

func TestCompatV4ReleaseRange(t *testing.T) {
	payload := readCompatJSONForMajor(t, 4)
	for _, version := range []string{"v4.0.0-beta.1", "v4.0.0", "4.0.1", "v4.99.99"} {
		xui, ok := lookupForPSPVersion(payload, version)
		if !ok || xui.MinXUI != MinXUI || xui.MaxTestedXUI != "3.7.0" {
			t.Fatalf("V4 initial XUI contract for %q: %#v found=%v", version, xui, ok)
		}
		sui, ok := lookupSUIForPSPVersion(payload, version)
		if !ok || sui.MinSUI != "" || sui.MaxTestedSUI != "1.6.1" {
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

type compatV4RoundTripper func(*http.Request) (*http.Response, error)

func (f compatV4RoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

// Exercise the actual runtime fetch/apply path with a local in-memory HTTP
// response. No live panel or network is used, and this is not a V4 panel smoke.
func TestCompatV4FetchAppliesPublishedShape(t *testing.T) {
	dir := isolatedCompatCache(t, "v4.0.0-beta.1")
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
	raw, err := os.ReadFile(filepath.Join("..", "..", "docs", "compat", "v4.json"))
	if err != nil {
		t.Fatal(err)
	}
	httpClient = &http.Client{Transport: compatV4RoundTripper(func(req *http.Request) (*http.Response, error) {
		if req.URL.String() != defaultRemoteCompatURLBase+"v4.json" || req.Header.Get("Accept") != "application/json" {
			t.Errorf("wrong per-major manifest request: %s", req.URL)
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
	if ActiveMinXUI() != MinXUI || ActiveMaxTestedXUI() != "3.7.0" || ActiveMinSUI() != "" || ActiveMaxTestedSUI() != "1.6.1" {
		t.Fatal("runtime did not apply the V4 XUI/SUI bounds")
	}
	if a, ok := LookupXUIAdvisory("v3.7.0"); !ok || a.AffectsXray || a.Text == "" {
		t.Fatal("runtime lost the canonical XUI advisory")
	}
	if a, ok := LookupSUIAdvisory("v1.6.0"); !ok || !a.AffectsXray || a.Text == "" {
		t.Fatal("runtime lost the canonical SUI advisory")
	}
	if a, ok := LookupSUIAdvisory("v1.6.1"); !ok || a.AffectsXray || a.Severity != "info" || a.Text == "" {
		t.Fatal("runtime lost the SUI 1.6.1 maintenance/login advisory")
	}
	cache, err := os.ReadFile(filepath.Join(dir, compatCacheFile))
	if err != nil {
		t.Fatal(err)
	}
	var stored compatCachePayload
	if err := json.Unmarshal(cache, &stored); err != nil || stored.PSPVersion != Version || stored.MaxTestedXUI != "3.7.0" {
		t.Fatalf("runtime cache has wrong provenance: %#v error=%v", stored, err)
	}

	// Even an override serving a valid v3 document must not replace the
	// established V4 range or persist it under the V4 build identity.
	wrong := readCompatJSONForMajor(t, 3)
	raw, err = json.Marshal(wrong)
	if err != nil {
		t.Fatal(err)
	}
	SetActiveMaxTestedXUI("3.5.0")
	if err := fetchAndApply(context.Background(), url); err == nil {
		t.Fatal("runtime accepted another major's manifest")
	}
	if ActiveMaxTestedXUI() != "3.5.0" {
		t.Fatal("wrong-major fetch changed active state")
	}
	after, err := os.ReadFile(filepath.Join(dir, compatCacheFile))
	if err != nil || !bytes.Equal(after, cache) {
		t.Fatalf("wrong-major fetch changed the persisted cache: %v", err)
	}
}
