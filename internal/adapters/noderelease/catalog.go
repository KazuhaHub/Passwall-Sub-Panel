// Package noderelease intersects official published Node-agent releases with
// compatibility reviewed for this PSP major. It never infers compatibility from
// a GitHub prerelease flag or fetches unreviewed historical releases.
package noderelease

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/KazuhaHub/passwall-sub-panel/internal/pkg/safehttp"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
	"github.com/KazuhaHub/passwall-sub-panel/internal/version"
	"golang.org/x/mod/semver"
)

const (
	requestTimeout = 8 * time.Second
	cacheTTL       = 5 * time.Minute
	maxResponse    = 1 << 20
	maxReviewed    = 16
	compiledMajor  = 4
	apiBase        = "https://api.github.com/repos/KazuhaHub/Passwall-Node/releases/tags/"
	releaseBase    = "https://github.com/KazuhaHub/Passwall-Node/releases/"
)

//go:embed reviewed.json
var reviewedJSON []byte

var errUnavailable = errors.New("Node release source is unavailable")

type Options struct {
	// HTTPClient is a test seam; requests and redirects remain restricted to
	// the official HTTPS endpoint even with an injected client.
	HTTPClient *http.Client
	Now        func() time.Time
	PSPMajor   int
}

type reviewedRelease struct {
	Version            string                      `json:"version"`
	PSPMajor           int                         `json:"psp_major"`
	Notes              string                      `json:"notes"`
	Methods            []string                    `json:"methods"`
	Platforms          []ports.NodeReleasePlatform `json:"platforms"`
	DockerPublishedTag string                      `json:"docker_published_tag"`
}

type Catalog struct {
	client   *http.Client
	now      func() time.Time
	reviewed []reviewedRelease
	mu       sync.Mutex
	cached   ports.NodeReleaseList
	expires  time.Time
	flight   *refresh
}

type refresh struct {
	done   chan struct{}
	result ports.NodeReleaseList
	err    error
}

var _ ports.NodeReleaseCatalog = (*Catalog)(nil)

// PSPMajorForVersion binds review selection to the actual stamped PSP major.
// Unstamped local builds explicitly use this catalog's compiled major; malformed
// release identities must not accidentally inherit v4 compatibility.
//
// THE STAMP IS A VERSION, IN EITHER SCHEME, and reading its major is the whole
// job. This used to ask the INSTALLER's rule, which knows only the legacy
// v-prefixed shape — so the first build stamped `4.0.0` would have been read as
// having no canonical identity at all, and the catalog would have been silently
// disabled rather than answering with the wrong major.
//
// A RELEASE LINE OF ZERO HAS NO ANSWER HERE. This function answers "which
// compatibility major is this build", and a build whose own version names no
// release line cannot be answered — so the refusal is deliberate, not a leftover
// of the shape rule. The history it used to protect was the v0.0.1-* line, every
// Node release in the field at the time; that scheme is gone, and with it every
// caller for whom zero was a meaningful release line.
func PSPMajorForVersion(stamp string) (int, error) {
	if stamp == "dev" {
		return compiledMajor, nil
	}
	major, ok := version.MajorOfRelease(stamp)
	if !ok || major < 1 {
		return 0, errors.New("invalid PSP version for Node release catalog")
	}
	return major, nil
}

// New performs only local validation; it neither contacts GitHub nor launches
// workers. Compatibility remains an explicit, version-specific review decision.
func New(opts Options) (*Catalog, error) {
	if opts.PSPMajor == 0 {
		opts.PSPMajor = compiledMajor
	}
	if opts.PSPMajor < 1 {
		return nil, errors.New("invalid PSP major for Node release catalog")
	}
	var registry struct {
		Releases []reviewedRelease `json:"releases"`
	}
	if err := json.Unmarshal(reviewedJSON, &registry); err != nil || len(registry.Releases) > maxReviewed {
		return nil, errors.New("invalid reviewed Node release compatibility")
	}
	seen := make(map[string]bool, len(registry.Releases))
	selected := make([]reviewedRelease, 0, len(registry.Releases))
	for _, entry := range registry.Releases {
		if !validReviewed(entry) || seen[entry.Version] {
			return nil, errors.New("invalid reviewed Node release compatibility")
		}
		seen[entry.Version] = true
		if entry.PSPMajor == opts.PSPMajor {
			selected = append(selected, entry)
		}
	}
	client := safehttp.NewClient(requestTimeout)
	if opts.HTTPClient != nil {
		clone := *opts.HTTPClient
		client = &clone
		if client.Timeout <= 0 || client.Timeout > requestTimeout {
			client.Timeout = requestTimeout
		}
	}
	// The canonical GitHub API requires no redirects. Reject all of them, so
	// neither a redirect nor an injected client's policy can widen the source.
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return errUnavailable }
	if opts.Now == nil {
		opts.Now = time.Now
	}
	return &Catalog{client: client, now: opts.Now, reviewed: selected}, nil
}

func validReviewed(entry reviewedRelease) bool {
	if !version.IsReleaseVersion(entry.Version) || entry.PSPMajor < 1 ||
		len(entry.Notes) == 0 || len(entry.Notes) > 4096 || len(entry.Methods) == 0 ||
		len(entry.Platforms) == 0 || len(entry.Platforms) > 6 {
		return false
	}
	methods := make(map[string]bool)
	for _, method := range entry.Methods {
		if (method != "linux" && method != "docker" && method != "manual") || methods[method] {
			return false
		}
		methods[method] = true
	}
	if methods["docker"] && entry.DockerPublishedTag != entry.Version {
		return false
	}
	if !methods["docker"] && entry.DockerPublishedTag != "" {
		return false
	}
	platforms := make(map[ports.NodeReleasePlatform]bool)
	for _, platform := range entry.Platforms {
		if !validPlatform(platform) || platforms[platform] {
			return false
		}
		platforms[platform] = true
	}
	return true
}

func validPlatform(p ports.NodeReleasePlatform) bool {
	return (p.OS == "linux" || p.OS == "darwin" || p.OS == "windows") &&
		(p.Arch == "amd64" || p.Arch == "arm64")
}

// List shares an in-flight refresh without creating background workers. A
// waiting request may cancel independently; the refresh owner's cancellation
// aborts that refresh and never populates the cache with partial results.
func (c *Catalog) List(ctx context.Context) (ports.NodeReleaseList, error) {
	if err := ctx.Err(); err != nil {
		return ports.NodeReleaseList{}, err
	}
	c.mu.Lock()
	if !c.expires.IsZero() && c.now().Before(c.expires) {
		result := cloneList(c.cached)
		c.mu.Unlock()
		return result, nil
	}
	if pending := c.flight; pending != nil {
		c.mu.Unlock()
		select {
		case <-ctx.Done():
			return ports.NodeReleaseList{}, ctx.Err()
		case <-pending.done:
			if err := ctx.Err(); err != nil {
				return ports.NodeReleaseList{}, err
			}
			return cloneList(pending.result), pending.err
		}
	}
	pending := &refresh{done: make(chan struct{})}
	c.flight = pending
	c.mu.Unlock()

	requestCtx, cancel := context.WithTimeout(ctx, requestTimeout)
	result, err := c.fetch(requestCtx)
	if err == nil {
		err = requestCtx.Err()
	}
	cancel()
	c.mu.Lock()
	if err == nil {
		c.cached = cloneList(result)
		c.expires = c.now().Add(cacheTTL)
	}
	pending.result, pending.err = cloneList(result), err
	c.flight = nil
	close(pending.done)
	c.mu.Unlock()
	return cloneList(result), err
}

func cloneList(value ports.NodeReleaseList) ports.NodeReleaseList {
	copy := ports.NodeReleaseList{CheckedAt: value.CheckedAt, Releases: make([]ports.NodeReleaseCatalogEntry, len(value.Releases))}
	for i, entry := range value.Releases {
		copy.Releases[i] = entry
		copy.Releases[i].Methods = append([]string{}, entry.Methods...)
		copy.Releases[i].Platforms = append([]ports.NodeReleasePlatform{}, entry.Platforms...)
	}
	return copy
}

type githubRelease struct {
	TagName     string        `json:"tag_name"`
	Draft       bool          `json:"draft"`
	Prerelease  bool          `json:"prerelease"`
	PublishedAt *time.Time    `json:"published_at"`
	HTMLURL     string        `json:"html_url"`
	Assets      []githubAsset `json:"assets"`
}

type githubAsset struct {
	Name               string `json:"name"`
	State              string `json:"state"`
	Size               int64  `json:"size"`
	BrowserDownloadURL string `json:"browser_download_url"`
}

func (c *Catalog) fetch(ctx context.Context) (ports.NodeReleaseList, error) {
	result := ports.NodeReleaseList{Releases: []ports.NodeReleaseCatalogEntry{}}
	for _, reviewed := range c.reviewed {
		if err := ctx.Err(); err != nil {
			return ports.NodeReleaseList{}, err
		}
		// THE TAG ADDRESSES THE RELEASE; THE VERSION NAMES WHAT IS INSIDE IT.
		// Deriving the tag here from the version keeps the reviewed records to
		// one field, and every address below uses it — the API path, the tag
		// GitHub reports back, the release page and the download path. Using the
		// version for the last of those is how a catalogue ends up empty for
		// exactly the releases it was extended to cover.
		tag, ok := version.ReleaseTagFor(reviewed.Version)
		if !ok {
			return ports.NodeReleaseList{}, errUnavailable
		}
		release, err := c.fetchRelease(ctx, tag)
		if err != nil {
			return ports.NodeReleaseList{}, err
		}
		if release == nil || release.Draft || release.PublishedAt == nil || release.PublishedAt.IsZero() {
			continue
		}
		if release.TagName != tag || !releaseChannelAgrees(tag, release.Prerelease) ||
			release.HTMLURL != releaseBase+"tag/"+tag {
			return ports.NodeReleaseList{}, errUnavailable
		}
		entry, err := catalogEntry(reviewed, release, tag)
		if err != nil {
			return ports.NodeReleaseList{}, err
		}
		if len(entry.Methods) > 0 {
			result.Releases = append(result.Releases, entry)
		}
	}
	// NEWEST PUBLISHED FIRST, NOT HIGHEST VERSION FIRST.
	//
	// semver compares prerelease identifiers character by character, so
	// v0.0.1-beta11 ranks BELOW v0.0.1-beta9. Sorting by it put the older
	// release at the top, and had beta10/beta11 been reviewed into the registry
	// they would have been filed behind beta3 — in the list that drives the
	// upgrade dialog, where the first entry is labelled the recommended version.
	//
	// Publication time is the axis a version string cannot reinterpret. The
	// version is only a tie-break, for releases sharing a publication instant —
	// and semver is the right one there, because it ranks a prerelease below its
	// own stable, which lexically comparing the strings gets backwards. The
	// two-digit pathology cannot arise between two releases published in the
	// same second.
	sort.Slice(result.Releases, func(i, j int) bool {
		left, right := result.Releases[i], result.Releases[j]
		if !left.PublishedAt.Equal(right.PublishedAt) {
			return left.PublishedAt.After(right.PublishedAt)
		}
		return semver.Compare(left.Version, right.Version) > 0
	})
	result.CheckedAt = c.now().UTC()
	return result, nil
}

// releaseChannelAgrees reports whether GitHub's prerelease flag may be taken at
// face value for this tag.
//
// FOR A LEGACY TAG IT MUST MATCH THE TAG TEXT, and that check is load-bearing:
// an older beta cut before the workflow began setting the flag would arrive with
// prerelease=false, and a hyphen has always meant a pre-release in that form.
//
// FOR A PRODUCT TAG THE FLAG IS THE AUTHORITY, because the tag has no hyphen at
// all — release/4.0.0 is three integers in a namespace. Requiring agreement there
// would reject every testing candidate, and it would reject it here, where the
// failure is the WHOLE catalog rather than one entry: a single misclassified
// release would empty the list an operator chooses an upgrade from.
func releaseChannelAgrees(tagName string, prerelease bool) bool {
	if strings.HasPrefix(tagName, version.ProductTagNamespace) {
		return true
	}
	return prerelease == strings.Contains(tagName, "-")
}

// fetchRelease reads one release by its TAG, which is what the API path is made
// of. The version is not the tag under the product scheme, and asking for the
// version there returns 404 — which reads as a missing release rather than a
// wrong address.
func (c *Catalog) fetchRelease(ctx context.Context, tag string) (*githubRelease, error) {
	// Even test-injected compatibility records cannot select an arbitrary URL.
	// The tag is derived from a validated version by the caller, and this is the
	// second check: the path segment is built by concatenation, so what reaches
	// it must not be able to contain a separator.
	if !version.IsReleaseTag(tag) {
		return nil, errUnavailable
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, apiBase+tag, nil)
	if err != nil {
		return nil, errUnavailable
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	req.Header.Set("User-Agent", "Passwall-Sub-Panel-Node-Release-Catalog")
	resp, err := c.client.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, errUnavailable
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return nil, nil
	}
	if resp.StatusCode != http.StatusOK {
		return nil, errUnavailable
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponse+1))
	if err != nil || len(body) > maxResponse {
		return nil, errUnavailable
	}
	var release githubRelease
	if err := json.Unmarshal(body, &release); err != nil {
		return nil, errUnavailable
	}
	return &release, nil
}

func packageName(version string, platform ports.NodeReleasePlatform) string {
	ext := ".tar.gz"
	if platform.OS == "windows" {
		ext = ".zip"
	}
	return fmt.Sprintf("passwall-node_%s_%s_%s%s", version, platform.OS, platform.Arch, ext)
}

// catalogEntry builds one entry. tag is the ADDRESS the release was published
// under and reviewed.Version is the identity inside it; the asset names use the
// second and the URLs use the first.
func catalogEntry(reviewed reviewedRelease, release *githubRelease, tag string) (ports.NodeReleaseCatalogEntry, error) {
	entry := ports.NodeReleaseCatalogEntry{
		Version: reviewed.Version, ReleaseTag: tag,
		Channel: "stable", PublishedAt: release.PublishedAt.UTC(),
		ReleaseURL: releaseBase + "tag/" + tag, Notes: reviewed.Notes,
		Methods: []string{}, Platforms: []ports.NodeReleasePlatform{},
	}
	if release.Prerelease {
		entry.Channel = "testing"
	}
	// THERE IS ONE SCHEME, AND THE NAMESPACE IS WHAT SAYS SO. This used to branch:
	// a tag inside `release/` carried a product version of its own, and a tag
	// outside it was a legacy release whose version WAS its tag. The legacy scheme
	// is gone, so a tag outside the namespace is not a release this project has —
	// the reviewed registry cannot hold one, and ReleaseTagFor cannot build one —
	// and what remains is the namespace as the single address form.
	entry.ProductVersion = reviewed.Version
	entry.Scheme = "product"
	assets := make(map[string]githubAsset, len(release.Assets))
	for _, asset := range release.Assets {
		if _, duplicate := assets[asset.Name]; duplicate {
			return ports.NodeReleaseCatalogEntry{}, errUnavailable
		}
		assets[asset.Name] = asset
	}
	available := func(name string) bool {
		asset, ok := assets[name]
		return ok && asset.State == "uploaded" && asset.Size > 0 &&
			asset.BrowserDownloadURL == releaseBase+"download/"+tag+"/"+name
	}
	if !available("SHA256SUMS.txt") {
		return entry, nil
	}
	linux := make(map[string]bool, 2)
	for _, platform := range reviewed.Platforms {
		if available(packageName(reviewed.Version, platform)) {
			entry.Platforms = append(entry.Platforms, platform)
			if platform.OS == "linux" {
				linux[platform.Arch] = true
			}
		}
	}
	for _, method := range []string{"linux", "docker", "manual"} {
		if !contains(reviewed.Methods, method) {
			continue
		}
		if method == "manual" && len(entry.Platforms) > 0 ||
			method == "linux" && linux["amd64"] && linux["arm64"] ||
			method == "docker" && linux["amd64"] && linux["arm64"] && reviewed.DockerPublishedTag == reviewed.Version {
			entry.Methods = append(entry.Methods, method)
		}
	}
	return entry, nil
}

func contains(values []string, wanted string) bool {
	for _, value := range values {
		if value == wanted {
			return true
		}
	}
	return false
}
