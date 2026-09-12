package noderelease

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"reflect"
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
	for _, tc := range []struct {
		version string
		major   int
	}{
		{"dev", 4},
		{"v4.0.0-beta.2", 4},
		{"v4.0.0", 4},
		{"v3.9.2-beta.20", 3},
		{"v5.0.0-beta.1", 5},
		{"v0.1.0", 0},
		{"", 0},
		{"latest", 0},
		{"v4", 0},
		{"4.0.0", 0},
		{"v04.0.0", 0},
		{"v4.0.0+build", 0},
		{"v4.0.0/../../PRIVATE_RESPONSE", 0},
		{"v999999999999999999999999999.0.0", 0},
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
			if len(catalog.reviewed) != 1 || catalog.reviewed[0].Version != fixtureVersion {
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
