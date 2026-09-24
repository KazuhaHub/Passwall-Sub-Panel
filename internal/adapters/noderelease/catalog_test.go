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
	versionpkg "github.com/KazuhaHub/passwall-sub-panel/internal/version"
)

const fixtureVersion = "4.0.0"

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

// THE ADDRESS CARRIES THE TAG AND THE NAME CARRIES THE VERSION, which is the
// publisher's own rule and the third place this file had to be told so: the
// download path was built from the version, which for a product release asks for
// a release that does not exist under that name — and the catalog, correctly,
// found no assets and dropped the entry.
func fixtureAsset(tag, name string) githubAsset {
	return githubAsset{
		Name: name, State: "uploaded", Size: 123,
		BrowserDownloadURL: "https://github.com/KazuhaHub/Passwall-Node/releases/download/" + tag + "/" + name,
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
	// THE TAG IS DERIVED FROM THE VERSION, by the panel's own rule. Setting one to
	// the other is what this whole migration is about: a product release is
	// addressed as v4.0.0 and named 4.0.0, and a fixture that conflated them
	// would build a release the catalog then correctly refuses.
	tag, ok := versionpkg.ReleaseTagFor(version)
	if !ok {
		panic("fixtureRelease: not a version: " + version)
	}
	release := githubRelease{
		TagName: tag, Prerelease: fixturePrerelease(tag), PublishedAt: &published,
		HTMLURL: "https://github.com/KazuhaHub/Passwall-Node/releases/tag/" + tag,
		Assets:  []githubAsset{fixtureAsset(tag, "SHA256SUMS.txt")},
	}
	for _, platform := range fixturePlatforms {
		release.Assets = append(release.Assets, fixtureAsset(tag, fixturePackage(version, platform)))
	}
	return release
}

// fixturePrerelease states what each fixture release was PUBLISHED as.
//
// A LEGACY TAG SPELLS ITS CHANNEL IN ITS TEXT, which is why this used to read the
// hyphen: `v0.0.1-beta11` has always meant a pre-release. A PRODUCT TAG HAS NO
// HYPHEN AT ALL, so the same test classified `release/4.0.0` — published as
// testing, like every Passwall Node release so far — as STABLE, and the catalog
// then dropped it from a testing caller's list. The channel is publication
// metadata; a hyphen in a tag is the legacy scheme's way of writing it down, not
// the rule.
// KEYED BY TAG, like realPublicationTimes and for the same reason: this is the
// field the release object carries, and for a product release the tag is not the
// version. Getting that wrong twice in one file is why both maps say it.
var fixtureProductPrerelease = map[string]bool{
	"v4.0.0": true,
}

// fixturePrerelease states what a fixture release was PUBLISHED as. It used to
// fall back to the hyphen for a tag outside the namespace, because a legacy tag
// spelled its channel in its text; there are no such tags any more, and a tag
// that is not in the namespace is not a release this project publishes.
func fixturePrerelease(tag string) bool {
	return fixtureProductPrerelease[tag]
}

// realPublicationTimes are the instants these releases actually went out on
// GitHub. The registry test stamps releases with them, because ordering by
// anything derived from the version string is the defect this file guards — and
// the table exists to hold a time that is NOT the order a version sort would
// produce.
//
// IT HAS ONE ENTRY NOW, WHICH IS A LIMITATION RATHER THAN A DESIGN. It used to
// hold the whole v0.0.1-beta line, whose publication order and version order
// genuinely disagreed (beta11 sorts below beta9 as text). Integer versions sort
// the way they were published, so the disagreement has to be CONSTRUCTED by a
// case that wants to observe it; the cases below do that with their own
// releases rather than by reading this table.
var realPublicationTimes = map[string]time.Time{
	"v4.0.0": time.Date(2026, 9, 20, 9, 1, 33, 0, time.UTC),
}

// withRealPublicationTime gives a fixture release its real instant, LOOKED UP BY
// TAG because that is what the release carries. Tags outside the table keep the
// shared timestamp.
func withRealPublicationTime(release githubRelease) githubRelease {
	if published, ok := realPublicationTimes[release.TagName]; ok {
		release.PublishedAt = &published
	}
	return release
}

// fixtureList is the answer the COLLECTION endpoint gives for these releases.
//
// THE RESPONSE IS AN ARRAY, and that is what a fixture has to be: the catalog asks
// what has been published rather than asking about one version at a time, so a
// one-element body means "this release is published".
func fixtureList(t *testing.T, releases ...githubRelease) string {
	t.Helper()
	bodies := make([]string, 0, len(releases))
	for _, release := range releases {
		data, err := json.Marshal(release)
		if err != nil {
			t.Fatal(err)
		}
		bodies = append(bodies, string(data))
	}
	return "[" + strings.Join(bodies, ",") + "]"
}

func fixtureBody(t *testing.T, release githubRelease) string {
	t.Helper()
	return fixtureList(t, release)
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
	catalog, err := New(Options{HTTPClient: &http.Client{Transport: transport}, Now: now})
	if err != nil {
		t.Fatal(err)
	}
	return catalog
}

// servingReleases answers every request with one list: the catalog makes exactly
// one call per refresh, and what a case varies is what that call returns.
func servingReleases(t *testing.T, releases ...githubRelease) roundTripFunc {
	t.Helper()
	body := fixtureList(t, releases...)
	return func(req *http.Request) (*http.Response, error) {
		return fixtureResponse(req, http.StatusOK, body), nil
	}
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

// THE CATALOG PRESENTS RELEASES BY PUBLICATION, NOT BY VERSION, AND THAT NEEDS
// TWO RELEASES TO OBSERVE.
//
// It used to read the disagreement off the reviewed registry, where beta11 was
// published after beta9 and sorts below it as text. The registry carries one
// release now — the products publish one — so there is no disagreement in it to
// find, and integer versions sort the way they were published anyway. The case
// therefore CONSTRUCTS the arrangement a version sort would get wrong: two
// releases whose publication times are the reverse of their versions.
//
// The other property it keeps is that the catalog touches EXACTLY the releases
// the registry names: a release added to the file must be fetched, and nothing
// else may be.
func TestCatalogListsByPublicationNotByVersion(t *testing.T) {
	const (
		lower  = "4.0.1" // the HIGHER publication time
		higher = "4.0.2" // published EARLIER, so it must come second
	)
	publishedAt := func(version string) time.Time {
		if version == lower {
			return fixtureNow.Add(-time.Hour)
		}
		return fixtureNow.Add(-48 * time.Hour)
	}
	// Both releases are published, with the publication times swapped relative to
	// their versions — which is the disagreement this case exists to observe.
	// TWO VARIABLES, NOT ONE. Sharing a `published` variable between them makes
	// both releases point at the same instant, which turns this into an ordering
	// test that agrees with whatever the version tie-break does.
	olderAt, newerAt := publishedAt(higher), publishedAt(lower)
	older := fixtureRelease(higher)
	older.PublishedAt = &olderAt
	older.Prerelease = true // published as a candidate, like every release so far
	newer := fixtureRelease(lower)
	newer.PublishedAt = &newerAt
	newer.Prerelease = true

	catalog := fixtureCatalog(t, servingReleases(t, older, newer), nil)

	list, err := catalog.List(context.Background())
	if err != nil || len(list.Releases) != 2 || !list.CheckedAt.Equal(fixtureNow) {
		t.Fatalf("list=%+v err=%v", list, err)
	}
	if list.Releases[0].Version != lower || list.Releases[1].Version != higher {
		t.Fatalf("presented %s then %s, want the newer PUBLICATION first",
			list.Releases[0].Version, list.Releases[1].Version)
	}
	// AND THE IDENTITY SPLIT IS THE SAME FOR EVERY ENTRY, which is what one scheme
	// means: the tag is the namespace plus the version, and the product version is
	// the version.
	for _, entry := range list.Releases {
		if entry.Scheme != "product" || entry.ProductVersion != entry.Version ||
			entry.ReleaseTag != versionpkg.ProductTagNamespace+entry.Version ||
			entry.ReleaseURL != "https://github.com/KazuhaHub/Passwall-Node/releases/tag/"+entry.ReleaseTag {
			t.Fatalf("identity split wrong: %+v", entry)
		}
		if entry.Channel != "testing" {
			t.Fatalf("channel lost: %+v", entry)
		}
	}
}

func TestCatalogPublishedOfficialReleaseAndTrustedReviewMetadata(t *testing.T) {
	release := fixtureRelease(fixtureVersion)
	// THE RELEASE'S OWN DESCRIPTION IS WHAT THE DIALOG SHOWS. It used to be told the
	// opposite — that notes must come from a reviewed document and never from the
	// body — and that is the curation this replaced: a body is written by whoever cut
	// the release, and the panel now reports what the release says about itself.
	release.Body = "A release body written for a release page.\n\n" + strings.Repeat("x", 2000)
	var calls int
	catalog := fixtureCatalog(t, func(req *http.Request) (*http.Response, error) {
		calls++
		if req.Method != http.MethodGet || req.URL.String() != releaseListURL(maxReleases) {
			t.Errorf("unexpected official request: %s %s", req.Method, req.URL)
		}
		if req.Header.Get("Accept") != "application/vnd.github+json" || req.Header.Get("X-GitHub-Api-Version") == "" || req.Header.Get("User-Agent") == "" {
			t.Errorf("missing GitHub API request headers: %+v", req.Header)
		}
		if req.Header.Get("Authorization") != "" || req.Body != nil {
			t.Error("catalog request must not transmit credentials or a request body")
		}
		return fixtureResponse(req, http.StatusOK, fixtureList(t, release)), nil
	}, nil)
	list, err := catalog.List(context.Background())
	if err != nil || calls != 1 || len(list.Releases) != 1 || !list.CheckedAt.Equal(fixtureNow) {
		t.Fatalf("list=%+v calls=%d err=%v", list, calls, err)
	}
	entry := list.Releases[0]
	if entry.Version != fixtureVersion || entry.Channel != "testing" || entry.ReleaseURL != release.HTMLURL || !entry.PublishedAt.Equal(*release.PublishedAt) {
		t.Fatalf("incorrect release identity: %+v", entry)
	}
	if !strings.HasPrefix(entry.Notes, "A release body written for a release page.") {
		t.Fatalf("notes must be the release's own description: %q", entry.Notes)
	}
	// AND BOUNDED. A body is written for a release page — changelogs, contributor
	// links — and the dialog renders it under a version selector.
	if len([]rune(entry.Notes)) > maxNotesRunes+1 {
		t.Fatalf("notes were not bounded to %d runes: %d", maxNotesRunes, len([]rune(entry.Notes)))
	}
	// LINUX AND MANUAL, AND NOT DOCKER. Docker used to be offered on the strength of
	// a curated published tag; a release cannot be asked whether its image was
	// pushed, so the panel does not claim it.
	if !reflect.DeepEqual(entry.Methods, []string{"linux", "manual"}) || !reflect.DeepEqual(entry.Platforms, fixturePlatforms) {
		t.Fatalf("incorrect availability: %+v", entry)
	}
}

// releaseListURL is the address the catalog asks for. Spelled out here rather than
// reading the constant, so a change to the constant is visible in this file.
func releaseListURL(limit int) string {
	return fmt.Sprintf("https://api.github.com/repos/KazuhaHub/Passwall-Node/releases?per_page=%d", limit)
}

func TestCatalogUnavailableAndUnpublishedAreDifferent(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status int
		mutate func(*githubRelease)
	}{
		// NO 404 CASE. A 404 on the COLLECTION endpoint means the repository is not
		// there — a source failure like any other, and the suite below covers it. The
		// per-release endpoint's 404 used to mean "this release is not published",
		// which is now what an empty list says.
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
		{"only Linux", func(r *githubRelease) { r.Assets = r.Assets[:3] }, fixturePlatforms[:2], []string{"linux", "manual"}},
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

func TestCatalogSortsExactVersions(t *testing.T) {
	versions := []string{fixtureVersion, "4.0.0.1", "4.1.0"}
	releases := make([]githubRelease, 0, len(versions))
	for _, version := range versions {
		releases = append(releases, fixtureRelease(version))
	}
	// EVERY RELEASE IS PUBLISHED AT THE SAME INSTANT, so the version is what orders
	// them — which is the tie-break, and the reason it has to be the project's own
	// comparator: x/mod/semver answers zero for a four-segment version.
	for i := range releases {
		same := fixtureNow.Add(-time.Hour)
		releases[i].PublishedAt = &same
	}
	catalog := fixtureCatalog(t, servingReleases(t, releases...), nil)

	list, err := catalog.List(context.Background())
	if err != nil || len(list.Releases) != 3 {
		t.Fatalf("list=%+v err=%v", list, err)
	}
	wanted := []string{"4.1.0", "4.0.0.1", fixtureVersion}
	for i, version := range wanted {
		if list.Releases[i].Version != version {
			t.Fatalf("catalog is not version-descending within one instant: %+v", list)
		}
	}
	// CHANNELS COME FROM THE RELEASE, NOT FROM THE TEXT, so they are read by
	// version rather than by position: one of these is published as a pre-release
	// and the others as released, and nothing about their version strings says
	// which.
	channels := make(map[string]string, len(list.Releases))
	for _, entry := range list.Releases {
		channels[entry.Version] = entry.Channel
	}
	if channels[fixtureVersion] != "testing" || channels["4.0.0.1"] != "stable" || channels["4.1.0"] != "stable" {
		t.Fatalf("stable/testing channels were inferred incorrectly: %v", channels)
	}

	// WHAT A RELEASE CAN EVIDENCE IS WHAT IT IS OFFERED FOR. This used to be a
	// curated list of methods acting as an upper bound; the bound is now the assets
	// themselves, so a release that published only macOS packages is installable by
	// hand and in no other way.
	partial := fixtureRelease(fixtureVersion)
	partial.Assets = []githubAsset{fixtureAsset(partial.TagName, "SHA256SUMS.txt")}
	for _, platform := range []ports.NodeReleasePlatform{{OS: "darwin", Arch: "amd64"}, {OS: "darwin", Arch: "arm64"}} {
		partial.Assets = append(partial.Assets, fixtureAsset(partial.TagName, fixturePackage(fixtureVersion, platform)))
	}
	catalog = fixtureCatalog(t, servingReleases(t, partial), nil)
	list, err = catalog.List(context.Background())
	if err != nil || len(list.Releases) != 1 || !reflect.DeepEqual(list.Releases[0].Methods, []string{"manual"}) {
		t.Fatalf("a release offered a path its assets do not support: %+v %v", list, err)
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
		// AN EMPTY LIST IS A CONFIRMED ANSWER — the project has published nothing —
		// and it is cached like any other. A status code that means the source could
		// not be read is the case below.
		return fixtureResponse(req, http.StatusOK, "[]"), nil
	}, nil)
	for range 2 {
		list, err := catalog.List(context.Background())
		assertEmptyList(t, list, err)
	}
	if calls != 1 {
		t.Fatalf("a confirmed empty catalog did not use cache: calls=%d", calls)
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

func TestAReleaseOutsideTheNamespaceIsSkippedRatherThanRequested(t *testing.T) {
	const malformed = "v0.0.1/../../https://sensitive.invalid/PRIVATE_RESPONSE"
	var asked []string
	// A RELEASE THE REPOSITORY CARRIES BUT THIS PROJECT DOES NOT PUBLISH. The
	// endpoint answers the repository's releases, and the legacy tags are still
	// among them, so a tag outside the namespace is a normal thing to meet — it is
	// skipped, not refused, because a list is not a claim that every entry is ours.
	foreign := fixtureRelease(fixtureVersion)
	foreign.TagName = malformed
	catalog := fixtureCatalog(t, func(req *http.Request) (*http.Response, error) {
		asked = append(asked, req.URL.String())
		return fixtureResponse(req, http.StatusOK, fixtureList(t, foreign)), nil
	}, nil)

	list, err := catalog.List(context.Background())
	if err != nil || len(list.Releases) != 0 {
		t.Fatalf("a release outside the namespace was offered: %+v %v", list, err)
	}
	// AND THE MALFORMED VALUE NEVER BECAME AN ADDRESS. This is the property, and it
	// is asserted on the URL rather than on a call count: a count would also be
	// satisfied by never contacting the release source at all, which is a different
	// fact and one this case does not establish.
	for _, url := range asked {
		if strings.Contains(url, "sensitive.invalid") || strings.Contains(url, "..") {
			t.Fatalf("malformed value reached an address: %s", url)
		}
	}
}
func TestCatalogOrdersByPublicationNotByVersionText(t *testing.T) {
	// Deliberately ascending in publication order, with a segment whose text and
	// numeric order disagree.
	released := []string{"4.0.9", "4.0.10", "4.0.11"}
	base := fixtureNow.Add(-time.Duration(len(released)) * time.Hour)
	releases := make([]githubRelease, 0, len(released))
	for index, version := range released {
		release := fixtureRelease(version)
		at := base.Add(time.Duration(index) * time.Hour)
		release.PublishedAt = &at
		releases = append(releases, release)
	}
	catalog := fixtureCatalog(t, servingReleases(t, releases...), nil)

	list, err := catalog.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	got := make([]string, 0, len(list.Releases))
	for _, entry := range list.Releases {
		got = append(got, entry.Version)
	}
	want := []string{"4.0.11", "4.0.10", "4.0.9"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("catalog order = %v, want newest-published first %v", got, want)
	}
}
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
// it wrong is not a wrong list — it is a list whose every entry is dropped, since
// an asset that is not named for the version is not found. The tag is the release
// page and the download path; the version is the asset names and the entry's own
// version.
func TestAProductReleaseIsAddressedByItsTagAndNamedByItsVersion(t *testing.T) {
	const version, tag = "4.0.0", "release/4.0.0"
	release := fixtureProductRelease(tag, version)
	catalog := fixtureCatalog(t, roundTripFunc(func(req *http.Request) (*http.Response, error) {
		if got := req.URL.String(); got != releaseListURL(maxReleases) {
			t.Fatalf("the catalog asked for %s", got)
		}
		return fixtureResponse(req, http.StatusOK, fixtureBody(t, release)), nil
	}), nil)
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
	// NO DOCKER: a release cannot be asked whether its image was pushed, so the
	// panel does not offer a path it cannot see.
	if !reflect.DeepEqual(entry.Methods, []string{"linux", "manual"}) {
		t.Errorf("methods = %+v", entry.Methods)
	}
	// Published under a prerelease flag, so it is a candidate.
	if entry.Channel != "testing" {
		t.Errorf("channel = %q, want testing", entry.Channel)
	}
}

// THE DIALOG RENDERS NOTES AS MARKDOWN, SO A CUT MUST NOT LAND INSIDE A LINE.
// A release body is GitHub's generated changelog: one PR per line, each ending in
// a URL. A cut at a fixed rune count lands in the middle of one of those URLs,
// and the renderer then links the truncated address — a link that goes somewhere
// other than the PR it names.
func TestReleaseNotesTruncateOnLineBoundaries(t *testing.T) {
	if got := releaseNotes("  ## What's Changed\n* short  "); got != "## What's Changed\n* short" {
		t.Fatalf("a short body must only be trimmed: %q", got)
	}

	var body strings.Builder
	body.WriteString("## What's Changed\n")
	for i := 0; body.Len() < 4*maxNotesRunes; i++ {
		fmt.Fprintf(&body, "* Change number %d by @KKazuhaK in https://github.com/KazuhaHub/Passwall-Node/pull/%d\n", i, 1000+i)
	}
	body.WriteString("\n**Full Changelog**: https://github.com/KazuhaHub/Passwall-Node/compare/v4.0.1.4...v4.0.1.5")
	got := releaseNotes(body.String())
	if n := len([]rune(got)); n > maxNotesRunes {
		t.Fatalf("notes exceed %d runes: %d", maxNotesRunes, n)
	}
	kept, ok := strings.CutSuffix(got, notesTruncationMarker)
	if !ok {
		t.Fatalf("truncated notes must end with the marker on its own paragraph: %q", got)
	}
	original := strings.Split(body.String(), "\n")
	for i, line := range strings.Split(kept, "\n") {
		if line != original[i] {
			t.Fatalf("line %d was cut mid-line: %q, want %q", i, line, original[i])
		}
	}

	// ONE LONG LINE, NO LINE BREAK INSIDE THE BOUND: fall back to the last space,
	// which still never splits a URL, since a URL contains none.
	long := strings.Repeat("word ", maxNotesRunes/5) + "https://github.com/KazuhaHub/Passwall-Node/pull/59 tail"
	got = releaseNotes(long)
	kept, ok = strings.CutSuffix(got, notesTruncationMarker)
	if !ok || strings.Contains(kept, "https://") || strings.HasSuffix(kept, "wor") {
		t.Fatalf("single-line body must be cut at a space: %q", got)
	}
}
