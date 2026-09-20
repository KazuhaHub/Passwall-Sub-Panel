package noderelease

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"reflect"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

const fixtureVersion = "v0.0.1-beta3"

var fixturePlatforms = []ports.NodeReleasePlatform{
	{OS: "linux", Arch: "amd64"},
	{OS: "linux", Arch: "arm64"},
	{OS: "darwin", Arch: "amd64"},
	{OS: "darwin", Arch: "arm64"},
	{OS: "windows", Arch: "amd64"},
	{OS: "windows", Arch: "arm64"},
}

var fixtureNow = time.Date(2026, 9, 12, 12, 30, 0, 0, time.UTC)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }

func fixtureAsset(version, name string) githubAsset {
	return githubAsset{
		Name: name, State: "uploaded", Size: 123,
		BrowserDownloadURL: "https://github.com/KazuhaHub/Passwall-Node/releases/download/" + version + "/" + name,
	}
}

// The fixture spells the public package convention independently of packageName.
func fixturePackage(version string, platform ports.NodeReleasePlatform) string {
	ext := ".tar.gz"
	if platform.OS == "windows" {
		ext = ".zip"
	}
	return "passwall-node_" + version + "_" + platform.OS + "_" + platform.Arch + ext
}

func fixtureRelease(version string) githubRelease {
	published := fixtureNow.Add(-time.Hour)
	release := githubRelease{
		TagName: version, Prerelease: strings.Contains(version, "-"), PublishedAt: &published,
		HTMLURL: "https://github.com/KazuhaHub/Passwall-Node/releases/tag/" + version,
		Assets:  []githubAsset{fixtureAsset(version, "SHA256SUMS.txt")},
	}
	for _, platform := range fixturePlatforms {
		release.Assets = append(release.Assets, fixtureAsset(version, fixturePackage(version, platform)))
	}
	return release
}

// realPublicationTimes are the instants these releases actually went out on
// GitHub. The registry test stamps releases with them, because ordering by
// anything derived from the version string is the defect this file guards:
// v0.0.1-beta11 sorts BELOW v0.0.1-beta9, so a version-derived order files the
// newest release seventh and labels the seventh as recommended.
//
// Every other fixture deliberately keeps ONE shared timestamp, so the semver
// tie-break that applies to genuinely simultaneous releases stays under test.
var realPublicationTimes = map[string]time.Time{
	"v0.0.1-beta3":  time.Date(2026, 9, 12, 12, 36, 16, 0, time.UTC),
	"v0.0.1-beta4":  time.Date(2026, 9, 13, 4, 58, 3, 0, time.UTC),
	"v0.0.1-beta6":  time.Date(2026, 9, 17, 2, 24, 43, 0, time.UTC),
	"v0.0.1-beta7":  time.Date(2026, 9, 17, 3, 38, 27, 0, time.UTC),
	"v0.0.1-beta8":  time.Date(2026, 9, 17, 7, 19, 57, 0, time.UTC),
	"v0.0.1-beta9":  time.Date(2026, 9, 17, 8, 10, 41, 0, time.UTC),
	"v0.0.1-beta10": time.Date(2026, 9, 18, 9, 37, 2, 0, time.UTC),
	"v0.0.1-beta11": time.Date(2026, 9, 18, 10, 31, 27, 0, time.UTC),
}

// withRealPublicationTime gives a fixture release its real instant. Versions
// outside the table keep the shared timestamp.
func withRealPublicationTime(release githubRelease) githubRelease {
	if published, ok := realPublicationTimes[release.TagName]; ok {
		release.PublishedAt = &published
	}
	return release
}

func fixtureBody(t *testing.T, release githubRelease) string {
	t.Helper()
	data, err := json.Marshal(release)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func fixtureResponse(req *http.Request, status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body)), Request: req,
	}
}

func fixtureCatalog(t *testing.T, transport roundTripFunc, now func() time.Time) *Catalog {
	t.Helper()
	if now == nil {
		now = func() time.Time { return fixtureNow }
	}
	catalog, err := New(Options{HTTPClient: &http.Client{Transport: transport}, Now: now, PSPMajor: 4})
	if err != nil {
		t.Fatal(err)
	}
	// Semantic single-release HTTP fixtures must not inherit additional
	// production registry entries as compatibility reviews are appended.
	for _, reviewed := range catalog.reviewed {
		if reviewed.Version == fixtureVersion {
			catalog.reviewed = []reviewedRelease{reviewed}
			return catalog
		}
	}
	t.Fatalf("single-release fixture is no longer reviewed: %s", fixtureVersion)
	return catalog
}

func assertEmptyList(t *testing.T, list ports.NodeReleaseList, err error) {
	t.Helper()
	if err != nil || list.Releases == nil || len(list.Releases) != 0 || !list.CheckedAt.Equal(fixtureNow) {
		t.Fatalf("expected a checked, non-null empty catalog: list=%+v err=%v", list, err)
	}
}

func assertUnavailable(t *testing.T, list ports.NodeReleaseList, err error) {
	t.Helper()
	if err == nil || len(list.Releases) != 0 || !list.CheckedAt.IsZero() {
		t.Fatalf("source failure must not look like a successfully checked catalog: list=%+v err=%v", list, err)
	}
	for _, secret := range []string{"PRIVATE_RESPONSE", "sensitive.invalid", "api.github.com", "Passwall-Node/releases"} {
		if strings.Contains(err.Error(), secret) {
			t.Fatalf("source failure exposes transport/body details: %v", err)
		}
	}
}

func TestPSPMajorForVersionBindsStampedCompatibility(t *testing.T) {
	// A BUILD STAMP IS NOT A RELEASE TAG. This function answers "which reviewed
	// major does the build that is running belong to", so the input is the
	// version PSP's own workflow stamped into it.
	//
	// `4.0.0` used to be in the refusal list below, and that was the defect
	// rather than the rule: the product scheme stamps exactly that, so the
	// first release named this way would have had its Node release catalog
	// silently disabled — `newNodeReleaseCatalog` returns nil and the list is
	// simply unavailable. The table now covers both schemes.
	for _, tc := range []struct {
		version string
		major   int
	}{
		{"dev", 4},
		// The legacy scheme, still stamped by every build in the field.
		{"v4.0.0-beta.2", 4},
		{"v4.0.0", 4},
		{"v3.9.2-beta.20", 3},
		{"v5.0.0-beta.1", 5},
		{"v102.1.0", 102},
		// The product scheme: three integers, no prefix.
		{"4.0.0", 4},
		{"102.1.0", 102},
		// Refusals. A stamp that is not canonical must not inherit reviewed
		// compatibility, and must not be repaired into something that does.
		{"v0.1.0", 0},
		{"", 0},
		{"latest", 0},
		{"v4", 0},
		{"v04.0.0", 0},
		{"v4.0.0+build", 0},
		{"v4.0.0/../../PRIVATE_RESPONSE", 0},
		{"v999999999999999999999999999.0.0", 0},
		// The product scheme is exactly three segments with no prerelease: a
		// candidate is distinguished by its CHANNEL, not by its version.
		{"4.0", 0},
		{"4.0.0.1", 0},
		{"04.0.0", 0},
		{"0.1.0", 0},
		{"4.0.0-beta.1", 0},
		{"4.0.0+build", 0},
		// A tag is not a version, and this is the swap that produces it.
		{"release/4.0.0", 0},
	} {
		t.Run(tc.version, func(t *testing.T) {
			major, err := PSPMajorForVersion(tc.version)
			if tc.major == 0 {
				if err == nil || major != 0 {
					t.Fatalf("invalid stamp inherited compatibility: major=%d err=%v", major, err)
				}
				return
			}
			if err != nil || major != tc.major {
				t.Fatalf("stamp %q: expected major=%d got=%d err=%v", tc.version, tc.major, major, err)
			}
		})
	}
}

func TestNewUsesOnlyReviewedMajorAndDoesNotFetch(t *testing.T) {
	var calls atomic.Int32
	transport := roundTripFunc(func(req *http.Request) (*http.Response, error) {
		calls.Add(1)
		return fixtureResponse(req, http.StatusOK, fixtureBody(t, fixtureRelease(fixtureVersion))), nil
	})
	for _, major := range []int{0, 4, 3, 5} {
		catalog, err := New(Options{
			HTTPClient: &http.Client{Transport: transport}, Now: func() time.Time { return fixtureNow }, PSPMajor: major,
		})
		if err != nil {
			t.Fatalf("major %d: %v", major, err)
		}
		if calls.Load() != 0 {
			t.Fatal("New must not contact the release source")
		}
		if major == 0 || major == 4 {
			versions := make([]string, len(catalog.reviewed))
			for i, reviewed := range catalog.reviewed {
				versions[i] = reviewed.Version
			}
			if !reflect.DeepEqual(versions, []string{fixtureVersion, "v0.0.1-beta4", "v0.0.1-beta6", "v0.0.1-beta7", "v0.0.1-beta8", "v0.0.1-beta9", "v0.0.1-beta10", "v0.0.1-beta11"}) {
				t.Fatalf("unexpected current registry: %+v", catalog.reviewed)
			}
			continue
		}
		list, err := catalog.List(context.Background())
		assertEmptyList(t, list, err)
		if calls.Load() != 0 {
			t.Fatal("an incompatible PSP major must not probe unreviewed releases")
		}
	}
	if _, err := New(Options{PSPMajor: -1}); err == nil {
		t.Fatal("negative PSP major accepted")
	}
}

// reviewedVersions is the reviewed registry in file order, and
// reviewedNewestPublishedFirst is the order the catalog must present it in.
//
// Declared once because two different properties are worth pinning and neither
// is worth restating per release: the catalog may touch EXACTLY these versions,
// and it must present them by publication. The second is not the first reversed
// — beta11 was published after beta9 while sorting below it, which is the whole
// reason the ordering fix exists. The two lists differ on purpose for that one
// pair; anywhere else a difference between them is a bug.
var (
	reviewedVersions = []string{
		fixtureVersion, "v0.0.1-beta4", "v0.0.1-beta6", "v0.0.1-beta7",
		"v0.0.1-beta8", "v0.0.1-beta9", "v0.0.1-beta10", "v0.0.1-beta11",
	}
	reviewedNewestPublishedFirst = []string{
		"v0.0.1-beta11", "v0.0.1-beta10", "v0.0.1-beta9", "v0.0.1-beta8",
		"v0.0.1-beta7", "v0.0.1-beta6", "v0.0.1-beta4", fixtureVersion,
	}
)

func TestCatalogFullReviewedRegistryRetainsReviewedReleasesAndListsNewestFirst(t *testing.T) {
	var requested []string
	catalog, err := New(Options{
		HTTPClient: &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			version, ok := strings.CutPrefix(req.URL.String(), "https://api.github.com/repos/KazuhaHub/Passwall-Node/releases/tags/")
			if !ok || !slices.Contains(reviewedVersions, version) {
				t.Fatalf("registry requested an unreviewed endpoint: %s", req.URL)
			}
			requested = append(requested, version)
			return fixtureResponse(req, http.StatusOK, fixtureBody(t, withRealPublicationTime(fixtureRelease(version)))), nil
		})},
		Now: func() time.Time { return fixtureNow }, PSPMajor: 4,
	})
	if err != nil {
		t.Fatal(err)
	}
	list, err := catalog.List(context.Background())
	if err != nil || !reflect.DeepEqual(requested, reviewedVersions) || len(list.Releases) != len(reviewedVersions) || !list.CheckedAt.Equal(fixtureNow) {
		t.Fatalf("full registry requests=%v list=%+v err=%v", requested, list, err)
	}
	for i, version := range reviewedNewestPublishedFirst {
		entry := list.Releases[i]
		// A LEGACY RELEASE HAS NO PRODUCT VERSION, and the tag is its version.
		// Stating that explicitly is what lets a consumer tell the two schemes
		// apart without inferring anything from the characters in a string.
		if entry.Scheme != "legacy" || entry.ReleaseTag != version || entry.ProductVersion != "" {
			t.Fatalf("legacy identity split wrong: scheme=%q tag=%q product_version=%q", entry.Scheme, entry.ReleaseTag, entry.ProductVersion)
		}
		if entry.Version != version || entry.Channel != "testing" || entry.ReleaseURL != "https://github.com/KazuhaHub/Passwall-Node/releases/tag/"+version ||
			!reflect.DeepEqual(entry.Methods, []string{"linux", "docker", "manual"}) || !reflect.DeepEqual(entry.Platforms, fixturePlatforms) {
			t.Fatalf("full registry did not retain exact reviewed installation availability: %+v", entry)
		}
	}
	// Located by VERSION, not by index: an index would have to be re-derived
	// every time a release is added, and being wrong about it would silently
	// assert the notes of a different release.
	for _, entry := range list.Releases {
		if entry.Version == "v0.0.1-beta4" && !strings.Contains(entry.Notes, "startup does not prove PSP sync, core or proxy readiness") {
			t.Fatal("beta4 review lost the startup-only limitation")
		}
	}
}

func TestCatalogPublishedOfficialReleaseAndTrustedReviewMetadata(t *testing.T) {
	release := fixtureRelease(fixtureVersion)
	body := strings.TrimSuffix(fixtureBody(t, release), "}") + `,"body":"PRIVATE_RESPONSE unreviewed claims","name":"unreviewed name"}`
	var calls int
	catalog := fixtureCatalog(t, func(req *http.Request) (*http.Response, error) {
		calls++
		if req.Method != http.MethodGet || req.URL.String() != "https://api.github.com/repos/KazuhaHub/Passwall-Node/releases/tags/v0.0.1-beta3" {
			t.Errorf("unexpected official request: %s %s", req.Method, req.URL)
		}
		if req.Header.Get("Accept") != "application/vnd.github+json" || req.Header.Get("X-GitHub-Api-Version") == "" || req.Header.Get("User-Agent") == "" {
			t.Errorf("missing GitHub API request headers: %+v", req.Header)
		}
		if req.Header.Get("Authorization") != "" || req.Body != nil {
			t.Error("catalog request must not transmit credentials or a request body")
		}
		return fixtureResponse(req, http.StatusOK, body), nil
	}, nil)
	list, err := catalog.List(context.Background())
	if err != nil || calls != 1 || len(list.Releases) != 1 || !list.CheckedAt.Equal(fixtureNow) {
		t.Fatalf("list=%+v calls=%d err=%v", list, calls, err)
	}
	entry := list.Releases[0]
	if entry.Version != fixtureVersion || entry.Channel != "testing" || entry.ReleaseURL != release.HTMLURL || !entry.PublishedAt.Equal(*release.PublishedAt) {
		t.Fatalf("incorrect release identity: %+v", entry)
	}
	if entry.Notes != catalog.reviewed[0].Notes || strings.Contains(entry.Notes, "PRIVATE_RESPONSE") {
		t.Fatalf("notes must come from reviewed compatibility, not GitHub body: %q", entry.Notes)
	}
	if !strings.Contains(entry.Notes, "Docker and Windows end-to-end installation has not been verified") {
		t.Fatalf("review metadata lost the explicit E2E limitation: %q", entry.Notes)
	}
	if !reflect.DeepEqual(entry.Methods, []string{"linux", "docker", "manual"}) || !reflect.DeepEqual(entry.Platforms, fixturePlatforms) {
		t.Fatalf("incorrect reviewed availability: %+v", entry)
	}
}

func TestCatalogUnavailableAndUnpublishedAreDifferent(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status int
		mutate func(*githubRelease)
	}{
		{name: "missing", status: http.StatusNotFound},
		{name: "draft", status: http.StatusOK, mutate: func(r *githubRelease) { r.Draft = true }},
		{name: "unpublished", status: http.StatusOK, mutate: func(r *githubRelease) { r.PublishedAt = nil }},
		{name: "zero published date", status: http.StatusOK, mutate: func(r *githubRelease) { zero := time.Time{}; r.PublishedAt = &zero }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			release := fixtureRelease(fixtureVersion)
			if tc.mutate != nil {
				tc.mutate(&release)
			}
			body := fixtureBody(t, release)
			if tc.status == http.StatusNotFound {
				body = "PRIVATE_RESPONSE not valid JSON"
			}
			catalog := fixtureCatalog(t, func(req *http.Request) (*http.Response, error) {
				return fixtureResponse(req, tc.status, body), nil
			}, nil)
			list, err := catalog.List(context.Background())
			assertEmptyList(t, list, err)
		})
	}
}

func TestCatalogRejectsSourceFailuresWithoutExposingDetails(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status int
		body   string
	}{
		{"forbidden", http.StatusForbidden, "PRIVATE_RESPONSE"},
		{"rate limited", http.StatusTooManyRequests, "PRIVATE_RESPONSE"},
		{"server error", http.StatusInternalServerError, "PRIVATE_RESPONSE"},
		{"gateway error", http.StatusBadGateway, "PRIVATE_RESPONSE"},
		{"unexpected no content", http.StatusNoContent, ""},
		{"invalid JSON", http.StatusOK, `{"PRIVATE_RESPONSE":`},
		{"wrong field type", http.StatusOK, `{"tag_name":123,"PRIVATE_RESPONSE":"hidden"}`},
		{"trailing JSON", http.StatusOK, fixtureBody(t, fixtureRelease(fixtureVersion)) + `{}`},
		{"oversized body", http.StatusOK, strings.Repeat(" ", maxResponse+1)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			catalog := fixtureCatalog(t, func(req *http.Request) (*http.Response, error) {
				return fixtureResponse(req, tc.status, tc.body), nil
			}, nil)
			list, err := catalog.List(context.Background())
			assertUnavailable(t, list, err)
		})
	}
	t.Run("network", func(t *testing.T) {
		catalog := fixtureCatalog(t, func(*http.Request) (*http.Response, error) {
			return nil, errors.New("PRIVATE_RESPONSE https://sensitive.invalid/token")
		}, nil)
		list, err := catalog.List(context.Background())
		assertUnavailable(t, list, err)
	})
	t.Run("body read", func(t *testing.T) {
		catalog := fixtureCatalog(t, func(req *http.Request) (*http.Response, error) {
			response := fixtureResponse(req, http.StatusOK, "")
			response.Body = io.NopCloser(errorReader{})
			return response, nil
		}, nil)
		list, err := catalog.List(context.Background())
		assertUnavailable(t, list, err)
	})
}

type errorReader struct{}

func (errorReader) Read([]byte) (int, error) {
	return 0, errors.New("PRIVATE_RESPONSE sensitive.invalid")
}

func TestCatalogRejectsRedirectEvenWithInjectedPermissiveClient(t *testing.T) {
	var calls int
	client := &http.Client{
		Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			calls++
			if req.URL.Host != "api.github.com" {
				t.Errorf("redirect left official API: %s", req.URL)
			}
			response := fixtureResponse(req, http.StatusFound, "PRIVATE_RESPONSE")
			response.Header.Set("Location", "https://sensitive.invalid/private")
			return response, nil
		}),
		CheckRedirect: func(*http.Request, []*http.Request) error { return nil },
	}
	catalog, err := New(Options{HTTPClient: client, Now: func() time.Time { return fixtureNow }})
	if err != nil {
		t.Fatal(err)
	}
	list, err := catalog.List(context.Background())
	assertUnavailable(t, list, err)
	if calls != 1 {
		t.Fatalf("redirect was followed: requests=%d", calls)
	}
	if client.Timeout != 0 || client.CheckRedirect == nil {
		t.Fatal("New mutated the caller's HTTP client")
	}
}

func TestCatalogRejectsPublishedReleaseIdentityMismatch(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*githubRelease)
	}{
		{"wrong exact tag", func(r *githubRelease) { r.TagName = "v0.0.1-beta2" }},
		{"beta advertised stable", func(r *githubRelease) { r.Prerelease = false }},
		{"wrong release owner", func(r *githubRelease) {
			r.HTMLURL = "https://github.com/attacker/Passwall-Node/releases/tag/" + fixtureVersion
		}},
		{"URL query", func(r *githubRelease) { r.HTMLURL += "?private=PRIVATE_RESPONSE" }},
		{"URL fragment", func(r *githubRelease) { r.HTMLURL += "#download" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			release := fixtureRelease(fixtureVersion)
			tc.mutate(&release)
			catalog := fixtureCatalog(t, func(req *http.Request) (*http.Response, error) {
				return fixtureResponse(req, http.StatusOK, fixtureBody(t, release)), nil
			}, nil)
			list, err := catalog.List(context.Background())
			assertUnavailable(t, list, err)
		})
	}
}

func TestCatalogFiltersPlatformsAndInstallationMethodsByExactUploadedAssets(t *testing.T) {
	for _, tc := range []struct {
		name      string
		mutate    func(*githubRelease)
		platforms []ports.NodeReleasePlatform
		methods   []string
	}{
		{"missing checksum", func(r *githubRelease) { r.Assets = r.Assets[1:] }, nil, nil},
		{"missing one Linux arch", func(r *githubRelease) { r.Assets = append(r.Assets[:2], r.Assets[3:]...) }, append(append([]ports.NodeReleasePlatform{}, fixturePlatforms[:1]...), fixturePlatforms[2:]...), []string{"manual"}},
		{"only Windows arm64", func(r *githubRelease) { r.Assets = []githubAsset{r.Assets[0], r.Assets[6]} }, []ports.NodeReleasePlatform{{OS: "windows", Arch: "arm64"}}, []string{"manual"}},
		{"only Linux", func(r *githubRelease) { r.Assets = r.Assets[:3] }, fixturePlatforms[:2], []string{"linux", "docker", "manual"}},
		{"no packages", func(r *githubRelease) { r.Assets = r.Assets[:1] }, nil, nil},
		{"upload pending", func(r *githubRelease) { r.Assets[2].State = "new" }, append(append([]ports.NodeReleasePlatform{}, fixturePlatforms[:1]...), fixturePlatforms[2:]...), []string{"manual"}},
		{"empty package", func(r *githubRelease) { r.Assets[2].Size = 0 }, append(append([]ports.NodeReleasePlatform{}, fixturePlatforms[:1]...), fixturePlatforms[2:]...), []string{"manual"}},
		{"negative package size", func(r *githubRelease) { r.Assets[2].Size = -1 }, append(append([]ports.NodeReleasePlatform{}, fixturePlatforms[:1]...), fixturePlatforms[2:]...), []string{"manual"}},
		{"wrong package owner", func(r *githubRelease) {
			r.Assets[2].BrowserDownloadURL = "https://github.com/attacker/Passwall-Node/releases/download/" + fixtureVersion + "/" + r.Assets[2].Name
		}, append(append([]ports.NodeReleasePlatform{}, fixturePlatforms[:1]...), fixturePlatforms[2:]...), []string{"manual"}},
		{"wrong package tag", func(r *githubRelease) {
			r.Assets[2].BrowserDownloadURL = strings.ReplaceAll(r.Assets[2].BrowserDownloadURL, fixtureVersion, "v0.0.1-beta2")
		}, append(append([]ports.NodeReleasePlatform{}, fixturePlatforms[:1]...), fixturePlatforms[2:]...), []string{"manual"}},
		{"package URL query", func(r *githubRelease) { r.Assets[2].BrowserDownloadURL += "?private=PRIVATE_RESPONSE" }, append(append([]ports.NodeReleasePlatform{}, fixturePlatforms[:1]...), fixturePlatforms[2:]...), []string{"manual"}},
		{"wrong exact name", func(r *githubRelease) { r.Assets[2].Name += ".bak" }, append(append([]ports.NodeReleasePlatform{}, fixturePlatforms[:1]...), fixturePlatforms[2:]...), []string{"manual"}},
		{"invalid checksum URL", func(r *githubRelease) { r.Assets[0].BrowserDownloadURL = "https://sensitive.invalid/SHA256SUMS.txt" }, nil, nil},
		{"invalid checksum state", func(r *githubRelease) { r.Assets[0].State = "new" }, nil, nil},
		{"empty checksum", func(r *githubRelease) { r.Assets[0].Size = 0 }, nil, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			release := fixtureRelease(fixtureVersion)
			tc.mutate(&release)
			catalog := fixtureCatalog(t, func(req *http.Request) (*http.Response, error) {
				return fixtureResponse(req, http.StatusOK, fixtureBody(t, release)), nil
			}, nil)
			list, err := catalog.List(context.Background())
			if err != nil || !list.CheckedAt.Equal(fixtureNow) {
				t.Fatalf("list=%+v err=%v", list, err)
			}
			if len(tc.methods) == 0 {
				assertEmptyList(t, list, err)
				return
			}
			if len(list.Releases) != 1 || !reflect.DeepEqual(list.Releases[0].Methods, tc.methods) || !reflect.DeepEqual(list.Releases[0].Platforms, tc.platforms) {
				t.Fatalf("expected methods=%v platforms=%v, got %+v", tc.methods, tc.platforms, list)
			}
		})
	}
}

func TestCatalogRejectsDuplicateAssetNames(t *testing.T) {
	for _, assetIndex := range []int{0, 1, 6} {
		t.Run(fmt.Sprintf("asset-%d", assetIndex), func(t *testing.T) {
			release := fixtureRelease(fixtureVersion)
			duplicate := release.Assets[assetIndex]
			duplicate.BrowserDownloadURL = "https://sensitive.invalid/PRIVATE_RESPONSE"
			release.Assets = append(release.Assets, duplicate)
			catalog := fixtureCatalog(t, func(req *http.Request) (*http.Response, error) {
				return fixtureResponse(req, http.StatusOK, fixtureBody(t, release)), nil
			}, nil)
			list, err := catalog.List(context.Background())
			assertUnavailable(t, list, err)
		})
	}
}

func TestCatalogOnlyExplicitReviewedReleasesAndSortsExactVersions(t *testing.T) {
	versions := []string{fixtureVersion, "v0.0.1", "v0.0.2-beta.1"}
	var requested []string
	catalog := fixtureCatalog(t, func(req *http.Request) (*http.Response, error) {
		version := strings.TrimPrefix(req.URL.String(), "https://api.github.com/repos/KazuhaHub/Passwall-Node/releases/tags/")
		requested = append(requested, version)
		return fixtureResponse(req, http.StatusOK, fixtureBody(t, fixtureRelease(version))), nil
	}, nil)
	base := catalog.reviewed[0]
	for _, version := range versions[1:] {
		reviewed := base
		reviewed.Version, reviewed.DockerPublishedTag = version, version
		catalog.reviewed = append(catalog.reviewed, reviewed)
	}
	list, err := catalog.List(context.Background())
	if err != nil || len(list.Releases) != 3 || !reflect.DeepEqual(requested, versions) {
		t.Fatalf("requests=%v list=%+v err=%v", requested, list, err)
	}
	wanted := []string{"v0.0.2-beta.1", "v0.0.1", fixtureVersion}
	for i, version := range wanted {
		if list.Releases[i].Version != version {
			t.Fatalf("catalog is not semver descending: %+v", list)
		}
	}
	if list.Releases[1].Channel != "stable" || list.Releases[0].Channel != "testing" {
		t.Fatalf("stable/testing channels were inferred incorrectly: %+v", list)
	}
	// The curated methods remain an upper bound even if all public assets exist.
	catalog = fixtureCatalog(t, func(req *http.Request) (*http.Response, error) {
		return fixtureResponse(req, http.StatusOK, fixtureBody(t, fixtureRelease(fixtureVersion))), nil
	}, nil)
	catalog.reviewed[0].Methods = []string{"manual"}
	catalog.reviewed[0].DockerPublishedTag = ""
	list, err = catalog.List(context.Background())
	if err != nil || len(list.Releases) != 1 || !reflect.DeepEqual(list.Releases[0].Methods, []string{"manual"}) {
		t.Fatalf("unreviewed installation method became available: %+v %v", list, err)
	}
}

func TestCatalogCacheIsDeepClonedAndExpiresWithoutStaleFallback(t *testing.T) {
	now := fixtureNow
	var calls int
	fail := false
	body := fixtureBody(t, fixtureRelease(fixtureVersion))
	catalog := fixtureCatalog(t, func(req *http.Request) (*http.Response, error) {
		calls++
		if fail {
			return fixtureResponse(req, http.StatusServiceUnavailable, "PRIVATE_RESPONSE"), nil
		}
		return fixtureResponse(req, http.StatusOK, body), nil
	}, func() time.Time { return now })
	first, err := catalog.List(context.Background())
	if err != nil || len(first.Releases) != 1 {
		t.Fatalf("initial fetch: %+v %v", first, err)
	}
	// Do not use production cloneList to construct this independent oracle.
	wanted := ports.NodeReleaseList{CheckedAt: first.CheckedAt, Releases: []ports.NodeReleaseCatalogEntry{first.Releases[0]}}
	wanted.Releases[0].Methods = append([]string{}, first.Releases[0].Methods...)
	wanted.Releases[0].Platforms = append([]ports.NodeReleasePlatform{}, first.Releases[0].Platforms...)
	first.Releases[0].Version = "caller-mutated"
	first.Releases[0].Methods[0] = "caller-mutated"
	first.Releases[0].Platforms[0].OS = "caller-mutated"
	now = fixtureNow.Add(5*time.Minute - time.Nanosecond)
	second, err := catalog.List(context.Background())
	if err != nil || calls != 1 || !reflect.DeepEqual(second, wanted) {
		t.Fatalf("cached value was mutated or expired early: calls=%d list=%+v err=%v", calls, second, err)
	}
	second.Releases[0].Notes = "second caller mutation"
	second.Releases[0].Methods[0] = "second caller mutation"
	second.Releases[0].Platforms[0].Arch = "second caller mutation"
	third, err := catalog.List(context.Background())
	if err != nil || !reflect.DeepEqual(third, wanted) || calls != 1 {
		t.Fatalf("cache return aliases nested slices: %+v %v", third, err)
	}
	now = fixtureNow.Add(5 * time.Minute)
	fail = true
	failed, err := catalog.List(context.Background())
	assertUnavailable(t, failed, err)
	if calls != 2 {
		t.Fatalf("cache did not expire at five minutes: calls=%d", calls)
	}
	fail = false
	recovered, err := catalog.List(context.Background())
	if err != nil || calls != 3 || len(recovered.Releases) != 1 || !recovered.CheckedAt.Equal(now) {
		t.Fatalf("failed refresh was cached or blocked recovery: calls=%d list=%+v err=%v", calls, recovered, err)
	}
}

func TestCatalogCachesConfirmedEmptyButNotFailures(t *testing.T) {
	var calls int
	catalog := fixtureCatalog(t, func(req *http.Request) (*http.Response, error) {
		calls++
		return fixtureResponse(req, http.StatusNotFound, ""), nil
	}, nil)
	for range 2 {
		list, err := catalog.List(context.Background())
		assertEmptyList(t, list, err)
	}
	if calls != 1 {
		t.Fatalf("confirmed missing release did not use cache: calls=%d", calls)
	}
	catalog = fixtureCatalog(t, func(req *http.Request) (*http.Response, error) {
		calls++
		return fixtureResponse(req, http.StatusTooManyRequests, "PRIVATE_RESPONSE"), nil
	}, nil)
	calls = 0
	for range 2 {
		list, err := catalog.List(context.Background())
		assertUnavailable(t, list, err)
	}
	if calls != 2 {
		t.Fatalf("source failures were cached as available: calls=%d", calls)
	}
}

// Done observation makes the shared-flight tests deterministic: the signal is
// emitted only when List has entered the pending refresh select, not on Err().
type observedDoneContext struct {
	context.Context
	observed chan struct{}
	once     sync.Once
}

func (ctx *observedDoneContext) Done() <-chan struct{} {
	ctx.once.Do(func() { close(ctx.observed) })
	return ctx.Context.Done()
}

type listResult struct {
	list ports.NodeReleaseList
	err  error
}

func awaitSignal(t *testing.T, signal <-chan struct{}) {
	t.Helper()
	select {
	case <-signal:
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for catalog coordination")
	}
}

func awaitList(t *testing.T, results <-chan listResult) listResult {
	t.Helper()
	select {
	case result := <-results:
		return result
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for List")
		return listResult{}
	}
}

func TestCatalogConcurrentRefreshIsSingleFlightAndWaiterCancellationIndependent(t *testing.T) {
	started, releaseRequest := make(chan struct{}), make(chan struct{})
	var calls atomic.Int32
	body := fixtureBody(t, fixtureRelease(fixtureVersion))
	catalog := fixtureCatalog(t, func(req *http.Request) (*http.Response, error) {
		if calls.Add(1) == 1 {
			close(started)
		}
		select {
		case <-releaseRequest:
			return fixtureResponse(req, http.StatusOK, body), nil
		case <-req.Context().Done():
			return nil, req.Context().Err()
		}
	}, nil)
	ownerCtx, cancelOwner := context.WithCancel(context.Background())
	defer cancelOwner()
	ownerResult := make(chan listResult, 1)
	go func() { list, err := catalog.List(ownerCtx); ownerResult <- listResult{list, err} }()
	awaitSignal(t, started)

	waiterBase, cancelWaiter := context.WithCancel(context.Background())
	defer cancelWaiter()
	waiterCtx := &observedDoneContext{Context: waiterBase, observed: make(chan struct{})}
	canceledResult := make(chan listResult, 1)
	go func() { list, err := catalog.List(waiterCtx); canceledResult <- listResult{list, err} }()
	awaitSignal(t, waiterCtx.observed)
	cancelWaiter()
	canceled := awaitList(t, canceledResult)
	if !errors.Is(canceled.err, context.Canceled) || len(canceled.list.Releases) != 0 {
		t.Fatalf("waiter cancellation did not return context cancellation: %+v", canceled)
	}

	const waiters = 8
	results := make(chan listResult, waiters)
	for range waiters {
		ctx := &observedDoneContext{Context: context.Background(), observed: make(chan struct{})}
		go func() { list, err := catalog.List(ctx); results <- listResult{list, err} }()
		awaitSignal(t, ctx.observed)
	}
	if calls.Load() != 1 {
		t.Fatalf("concurrent callers initiated duplicate refreshes: %d", calls.Load())
	}
	close(releaseRequest)
	owner := awaitList(t, ownerResult)
	if owner.err != nil || len(owner.list.Releases) != 1 {
		t.Fatalf("canceled waiter disrupted owner: %+v", owner)
	}
	owner.list.Releases[0].Methods[0] = "owner mutation"
	for range waiters {
		result := awaitList(t, results)
		if result.err != nil || len(result.list.Releases) != 1 || result.list.Releases[0].Methods[0] != "linux" {
			t.Fatalf("shared refresh results alias or failed: %+v", result)
		}
		result.list.Releases[0].Methods[0] = "waiter mutation"
	}
	if calls.Load() != 1 {
		t.Fatalf("refresh was not single flight: calls=%d", calls.Load())
	}
}

func TestCatalogOwnerCancellationAbortsSharedRefreshAndDoesNotCache(t *testing.T) {
	started := make(chan struct{})
	var calls atomic.Int32
	body := fixtureBody(t, fixtureRelease(fixtureVersion))
	catalog := fixtureCatalog(t, func(req *http.Request) (*http.Response, error) {
		if calls.Add(1) == 1 {
			close(started)
			<-req.Context().Done()
			return nil, req.Context().Err()
		}
		return fixtureResponse(req, http.StatusOK, body), nil
	}, nil)
	ownerCtx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ownerResults := make(chan listResult, 1)
	go func() { list, err := catalog.List(ownerCtx); ownerResults <- listResult{list, err} }()
	awaitSignal(t, started)
	waiterCtx := &observedDoneContext{Context: context.Background(), observed: make(chan struct{})}
	waiterResults := make(chan listResult, 1)
	go func() { list, err := catalog.List(waiterCtx); waiterResults <- listResult{list, err} }()
	awaitSignal(t, waiterCtx.observed)
	cancel()
	for _, results := range []<-chan listResult{ownerResults, waiterResults} {
		result := awaitList(t, results)
		if !errors.Is(result.err, context.Canceled) || len(result.list.Releases) != 0 || !result.list.CheckedAt.IsZero() {
			t.Fatalf("canceled shared refresh returned availability: %+v", result)
		}
	}
	list, err := catalog.List(context.Background())
	if err != nil || len(list.Releases) != 1 || calls.Load() != 2 {
		t.Fatalf("canceled refresh populated cache or failed to reset: calls=%d list=%+v err=%v", calls.Load(), list, err)
	}
}

func TestCatalogCanceledContextNeverFetchesOrReadsCache(t *testing.T) {
	var calls int
	body := fixtureBody(t, fixtureRelease(fixtureVersion))
	catalog := fixtureCatalog(t, func(req *http.Request) (*http.Response, error) {
		calls++
		return fixtureResponse(req, http.StatusOK, body), nil
	}, nil)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	list, err := catalog.List(ctx)
	if !errors.Is(err, context.Canceled) || calls != 0 || len(list.Releases) != 0 {
		t.Fatalf("canceled context fetched releases: calls=%d list=%+v err=%v", calls, list, err)
	}
	if _, err := catalog.List(context.Background()); err != nil {
		t.Fatal(err)
	}
	list, err = catalog.List(ctx)
	if !errors.Is(err, context.Canceled) || calls != 1 || len(list.Releases) != 0 {
		t.Fatalf("canceled context received cached success: calls=%d list=%+v err=%v", calls, list, err)
	}
}

func TestCatalogInvalidCuratedTagCannotChooseAnotherHTTPSource(t *testing.T) {
	var calls int
	catalog := fixtureCatalog(t, func(req *http.Request) (*http.Response, error) {
		calls++
		return fixtureResponse(req, http.StatusOK, "PRIVATE_RESPONSE"), nil
	}, nil)
	catalog.reviewed[0].Version = "v0.0.1/../../https://sensitive.invalid/PRIVATE_RESPONSE"
	list, err := catalog.List(context.Background())
	assertUnavailable(t, list, err)
	if calls != 0 {
		t.Fatalf("malformed compatibility tag selected an HTTP source: calls=%d", calls)
	}
}

func TestReviewedRegistryRejectsUnsafeOrUnverifiedRecords(t *testing.T) {
	catalog := fixtureCatalog(t, func(*http.Request) (*http.Response, error) {
		t.Fatal("registry validation must not fetch")
		return nil, nil
	}, nil)
	for _, tc := range []struct {
		name   string
		mutate func(*reviewedRelease)
	}{
		{"floating version", func(r *reviewedRelease) { r.Version = "latest" }},
		{"version URL injection", func(r *reviewedRelease) { r.Version = "v0.0.1/../../PRIVATE_RESPONSE" }},
		{"missing PSP major", func(r *reviewedRelease) { r.PSPMajor = 0 }},
		{"no notes", func(r *reviewedRelease) { r.Notes = "" }},
		{"oversized notes", func(r *reviewedRelease) { r.Notes = strings.Repeat("a", 4097) }},
		{"unknown method", func(r *reviewedRelease) { r.Methods = []string{"ssh"} }},
		{"duplicate method", func(r *reviewedRelease) { r.Methods = []string{"manual", "manual"} }},
		{"no methods", func(r *reviewedRelease) { r.Methods = nil }},
		{"docker floating tag", func(r *reviewedRelease) { r.DockerPublishedTag = "beta" }},
		{"docker other exact tag", func(r *reviewedRelease) { r.DockerPublishedTag = "v0.0.1-beta2" }},
		{"docker not reviewed", func(r *reviewedRelease) { r.Methods = []string{"manual"} }},
		{"unsupported OS", func(r *reviewedRelease) { r.Platforms = []ports.NodeReleasePlatform{{OS: "freebsd", Arch: "amd64"}} }},
		{"unsupported arch", func(r *reviewedRelease) { r.Platforms = []ports.NodeReleasePlatform{{OS: "linux", Arch: "386"}} }},
		{"duplicate platform", func(r *reviewedRelease) {
			r.Platforms = []ports.NodeReleasePlatform{fixturePlatforms[0], fixturePlatforms[0]}
		}},
		{"no platforms", func(r *reviewedRelease) { r.Platforms = nil }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			reviewed := catalog.reviewed[0]
			tc.mutate(&reviewed)
			if validReviewed(reviewed) {
				t.Fatalf("invalid compatibility record accepted: %+v", reviewed)
			}
		})
	}
	if !validReviewed(catalog.reviewed[0]) {
		t.Fatal("current reviewed registry is invalid")
	}
}

// A PRERELEASE SUFFIX IS NOT A NUMBER, and this is the test that would have
// caught it before an operator noticed instead.
//
// semver compares prerelease identifiers character by character, so
// v0.0.1-beta11 ranks BELOW v0.0.1-beta9. Sorting "newest first" with it
// therefore puts beta9 at the top and, once beta10 and beta11 were reviewed in,
// would have filed them behind beta3 — while the UI marks index 0 as the
// recommended version, so every operator would have been recommended the older
// release. The fixtures never noticed because none of them had a two-digit
// prerelease.
//
// RECENCY COMES FROM THE PUBLICATION TIME, which is the only axis a version
// string cannot reinterpret.
func TestCatalogOrdersByPublicationNotByPrereleaseNumber(t *testing.T) {
	// Deliberately ascending in publication order while the version strings are
	// not: beta9 shipped first, then beta10, then beta11.
	released := []string{"v0.0.1-beta9", "v0.0.1-beta10", "v0.0.1-beta11"}
	published := make(map[string]time.Time, len(released))
	base := fixtureNow.Add(-time.Duration(len(released)) * time.Hour)
	for index, version := range released {
		published[version] = base.Add(time.Duration(index) * time.Hour)
	}
	catalog := fixtureCatalog(t, func(req *http.Request) (*http.Response, error) {
		version := strings.TrimPrefix(req.URL.String(), "https://api.github.com/repos/KazuhaHub/Passwall-Node/releases/tags/")
		release := fixtureRelease(version)
		at := published[version]
		release.PublishedAt = &at
		return fixtureResponse(req, http.StatusOK, fixtureBody(t, release)), nil
	}, nil)

	template := catalog.reviewed[0]
	catalog.reviewed = nil
	for _, version := range released {
		reviewed := template
		reviewed.Version, reviewed.DockerPublishedTag = version, version
		catalog.reviewed = append(catalog.reviewed, reviewed)
	}

	list, err := catalog.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	got := make([]string, 0, len(list.Releases))
	for _, entry := range list.Releases {
		got = append(got, entry.Version)
	}
	want := []string{"v0.0.1-beta11", "v0.0.1-beta10", "v0.0.1-beta9"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("catalog order = %v, want newest-published first %v", got, want)
	}
}

// The agreement check between GitHub's prerelease flag and the tag text is a
// LEGACY defence, and it is load-bearing there: an older beta cut before the
// workflow set the flag arrives with prerelease=false, and a hyphen has always
// meant a pre-release in that form.
//
// It is not a rule about product tags, which have no hyphen at all. Requiring
// agreement there would reject every testing candidate — and it would do it in
// the catalog's LOOP, so one misclassified release empties the whole list an
// operator chooses an upgrade from rather than dropping one row.
func TestReleaseChannelAgreementIsScopedToTheLegacyForm(t *testing.T) {
	for _, tc := range []struct {
		name       string
		tag        string
		prerelease bool
		want       bool
		why        string
	}{
		{
			name: "a legacy beta the flag caught", tag: "v0.0.1-beta11", prerelease: true, want: true,
			why: "flag and tag agree",
		},
		{
			name: "a legacy beta the flag missed", tag: "v0.0.1-beta11", prerelease: false, want: false,
			why: "the gap this check exists for: published before the workflow set the flag",
		},
		{
			name: "a legacy stable", tag: "v0.0.1", prerelease: false, want: true,
			why: "no hyphen, no flag",
		},
		{
			name: "a product tag the flag marks testing", tag: "release/4.0.0", prerelease: true, want: true,
			why: "no hyphen to find, so the flag decides",
		},
		{
			name: "a product tag the flag calls released", tag: "release/4.0.0", prerelease: false, want: true,
			why: "the flag is the authority for this namespace",
		},
		{
			name: "a tag in neither scheme", tag: "4.0.0-rc.1", prerelease: false, want: false,
			why: "for an unrecognised form the characters are all there is to go on",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := releaseChannelAgrees(tc.tag, tc.prerelease); got != tc.want {
				t.Fatalf("releaseChannelAgrees(%q, %v) = %v, want %v — %s", tc.tag, tc.prerelease, got, tc.want, tc.why)
			}
		})
	}
}

// fixtureProductAsset names the asset by the VERSION and addresses it under the
// TAG, which is what the release workflow publishes.
func fixtureProductAsset(tag, version, name string) githubAsset {
	return githubAsset{
		Name: name, State: "uploaded", Size: 123,
		BrowserDownloadURL: "https://github.com/KazuhaHub/Passwall-Node/releases/download/" + tag + "/" + name,
	}
}

func fixtureProductRelease(tag, version string) githubRelease {
	published := fixtureNow.Add(-time.Hour)
	release := githubRelease{
		TagName: tag, Prerelease: true, PublishedAt: &published,
		HTMLURL: "https://github.com/KazuhaHub/Passwall-Node/releases/tag/" + tag,
		Assets:  []githubAsset{fixtureProductAsset(tag, version, "SHA256SUMS.txt")},
	}
	for _, platform := range fixturePlatforms {
		release.Assets = append(release.Assets, fixtureProductAsset(tag, version, fixturePackage(version, platform)))
	}
	return release
}

// A release under the product scheme is addressed by its TAG and names its
// contents by its VERSION, and the two are different strings.
//
// Every address in the catalog is one or the other, and the failure of getting
// it wrong is not a wrong list — it is `errUnavailable`, so the WHOLE catalog
// goes dark rather than one entry disappearing. The API path, the tag GitHub
// reports back, the release page and the download path are all the tag; the
// asset names and the entry's own version are the version.
func TestAProductReleaseIsAddressedByItsTagAndNamedByItsVersion(t *testing.T) {
	const version, tag = "4.0.0", "release/4.0.0"
	release := fixtureProductRelease(tag, version)
	catalog := fixtureCatalog(t, roundTripFunc(func(req *http.Request) (*http.Response, error) {
		if got := req.URL.String(); got != "https://api.github.com/repos/KazuhaHub/Passwall-Node/releases/tags/"+tag {
			t.Fatalf("the catalog asked for %s, and the release lives at %s", got, tag)
		}
		return fixtureResponse(req, http.StatusOK, fixtureBody(t, release)), nil
	}), nil)
	catalog.reviewed = []reviewedRelease{{
		Version: version, PSPMajor: 4, Notes: "reviewed",
		Methods: []string{"linux", "docker", "manual"}, Platforms: fixturePlatforms,
		DockerPublishedTag: version,
	}}

	list, err := catalog.List(context.Background())
	if err != nil {
		t.Fatalf("a product-scheme release must be readable: %v", err)
	}
	if len(list.Releases) != 1 {
		t.Fatalf("releases = %+v", list.Releases)
	}
	entry := list.Releases[0]
	if entry.Version != version {
		t.Errorf("entry version = %q, want the version %q", entry.Version, version)
	}
	// THE IDENTITIES ARE STATED, NOT LEFT TO BE DERIVED. A consumer that builds
	// the release page or the download path needs the TAG, and before this it had
	// to re-derive the mapping from the version — which is how the front end came
	// to ask for tag/4.0.0.
	if entry.ReleaseTag != tag {
		t.Errorf("entry release_tag = %q, want the tag %q", entry.ReleaseTag, tag)
	}
	if entry.ProductVersion != version {
		t.Errorf("entry product_version = %q, want %q", entry.ProductVersion, version)
	}
	if entry.Scheme != "product" {
		t.Errorf("entry scheme = %q, want product", entry.Scheme)
	}
	if want := releaseBase + "tag/" + tag; entry.ReleaseURL != want {
		t.Errorf("release url = %q, want %q", entry.ReleaseURL, want)
	}
	// The platforms are only reported when every asset was found at the address
	// built from the tag with the name built from the version, so this is the
	// assertion that the whole path/name split is right.
	if !reflect.DeepEqual(entry.Platforms, fixturePlatforms) {
		t.Errorf("platforms = %+v, want %+v", entry.Platforms, fixturePlatforms)
	}
	if !reflect.DeepEqual(entry.Methods, []string{"linux", "docker", "manual"}) {
		t.Errorf("methods = %+v", entry.Methods)
	}
	// Published under a prerelease flag, so it is a candidate.
	if entry.Channel != "testing" {
		t.Errorf("channel = %q, want testing", entry.Channel)
	}
}
