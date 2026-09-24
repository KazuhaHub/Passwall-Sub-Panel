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
)

const (
	requestTimeout = 8 * time.Second
	cacheTTL       = 5 * time.Minute
	maxResponse    = 1 << 20
	// maxReleases bounds one refresh. The list endpoint answers a page at a time
	// and the project publishes a handful of releases per line; a bound that is
	// generous rather than tight keeps a wrong endpoint from becoming an unbounded
	// amount of work.
	maxReleases = 100
	// releaseListBase is the COLLECTION endpoint — what the project has published —
	// as opposed to the per-tag endpoint this used to ask one release at a time.
	releaseListBase = "https://api.github.com/repos/KazuhaHub/Passwall-Node/releases"
	releaseBase     = "https://github.com/KazuhaHub/Passwall-Node/releases/"
)

var errUnavailable = errors.New("Node release source is unavailable")

type Options struct {
	// HTTPClient is a test seam; requests and redirects remain restricted to
	// the official HTTPS endpoint even with an injected client.
	HTTPClient *http.Client
	Now        func() time.Time
}

// candidateRelease is the frame a PUBLISHED release is read into: the version it
// names and the choices a release cannot state about itself.
//
// IT IS DERIVED, NOT CURATED, and that is the simplification this replaced a
// registry with. These fields used to come from a document somebody reviewed by
// hand — which is what made adding a release a document edit AND a panel release,
// for a list whose real content is "what has this project published". What is left
// is a frame; what is actually OFFERED is decided by the release's own assets in
// catalogEntry.
type candidateRelease struct {
	Version string
	// Notes is the release's own description, shown in the dialog. It replaced
	// review prose, which is why it is bounded rather than trusted to be short.
	Notes string
	// InstallMethods is the set to CONSIDER, not the answer: catalogEntry offers a
	// method only when the assets that method needs are actually there.
	InstallMethods []string
	Platforms      []ports.NodeReleasePlatform
}

type Catalog struct {
	client  *http.Client
	now     func() time.Time
	mu      sync.Mutex
	cached  ports.NodeReleaseList
	expires time.Time
	flight  *refresh
}

type refresh struct {
	done   chan struct{}
	result ports.NodeReleaseList
	err    error
}

var _ ports.NodeReleaseCatalog = (*Catalog)(nil)

// New performs only local validation; it neither contacts GitHub nor launches
// workers.
//
// IT USED TO SELECT A REVIEWED SET BY THE PANEL'S OWN MAJOR, and there is nothing
// left to select: the catalog is the releases this project has published, which is
// the same answer for every build. That also removed the reason a build with an
// unreadable stamp got no catalog at all.
func New(opts Options) (*Catalog, error) {
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
	return &Catalog{client: client, now: opts.Now}, nil
}
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
	TagName string `json:"tag_name"`
	// Body is the release's own description. It replaced review prose as the text
	// the dialog shows, and it is bounded where it is used rather than here.
	Body        string        `json:"body"`
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
	releases, err := c.listReleases(ctx)
	if err != nil {
		return ports.NodeReleaseList{}, err
	}
	for _, release := range releases {
		if err := ctx.Err(); err != nil {
			return ports.NodeReleaseList{}, err
		}
		// A DRAFT IS NOT PUBLISHED. The endpoint returns an operator's drafts to
		// whoever may see them; the panel has no write access, but offering a
		// release nobody can download costs nothing to guard against.
		//
		// AND A RELEASE WITHOUT A PUBLICATION TIME IS THE SAME STATE: `published_at`
		// is null exactly while a release is a draft.
		if release.Draft || release.PublishedAt == nil || release.PublishedAt.IsZero() {
			continue
		}
		// A TAG OUTSIDE THE NAMESPACE IS SKIPPED RATHER THAN REFUSED. The
		// repository still carries legacy tags, and this catalog is about the
		// releases under one namespace; a list is not a claim that every entry in it
		// is ours.
		tag := release.TagName
		released, ok := version.VersionOfReleaseTag(tag)
		if !ok || !version.IsReleaseVersion(released) {
			continue
		}
		if len(result.Releases) >= maxReleases {
			break
		}
		entry, err := catalogEntry(candidateFor(released, release), release, tag)
		if err != nil {
			return ports.NodeReleaseList{}, err
		}
		if len(entry.Methods) > 0 {
			result.Releases = append(result.Releases, entry)
		}
	}
	// NEWEST PUBLISHED FIRST, NOT HIGHEST VERSION FIRST.
	//
	// Publication time is the axis a version string cannot reinterpret: a project
	// whose publication order and version order disagree — a prerelease published
	// after the release it precedes — would otherwise show an older release at the
	// top of the list that drives the upgrade dialog.
	//
	// THE VERSION IS ONLY A TIE-BREAK, for releases sharing a publication instant,
	// and it is the PROJECT'S OWN order rather than x/mod/semver: that package
	// cannot parse the fourth BUILD component at all, and answers zero for
	// `4.0.0.1`, which a sort reads as equality.
	sort.Slice(result.Releases, func(i, j int) bool {
		left, right := result.Releases[i], result.Releases[j]
		if !left.PublishedAt.Equal(right.PublishedAt) {
			return left.PublishedAt.After(right.PublishedAt)
		}
		return version.CompareRelease(left.Version, right.Version) > 0
	})
	result.CheckedAt = c.now().UTC()
	return result, nil
}

// listReleases reads what this project has published.
//
// THE COLLECTION ENDPOINT, NOT ONE REQUEST PER VERSION. The catalog used to name
// each release it wanted and ask for it by tag, because the list of releases it
// was willing to show was the panel's own — so a published, downloadable release
// nobody had curated was invisible. One request now answers the question the
// catalog is actually asking.
func (c *Catalog) listReleases(ctx context.Context) ([]githubRelease, error) {
	url := fmt.Sprintf("%s?per_page=%d", releaseListBase, maxReleases)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
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
	if resp.StatusCode != http.StatusOK {
		return nil, errUnavailable
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponse+1))
	if err != nil || len(body) > maxResponse {
		return nil, errUnavailable
	}
	var releases []githubRelease
	if err := json.Unmarshal(body, &releases); err != nil {
		return nil, errUnavailable
	}
	return releases, nil
}

// candidateFor is the frame a published release is read into.
//
// THE PLATFORMS ARE THE SIX THIS PROJECT BUILDS, and the methods are the two a
// release can EVIDENCE: linux, whose tarballs catalogEntry checks one asset at a
// time, and manual, which is a person following the instructions. "docker" is
// deliberately absent — a release cannot be asked whether its image was pushed,
// and claiming an install path the panel cannot see would be a claim it cannot
// support.
func candidateFor(released string, release githubRelease) candidateRelease {
	return candidateRelease{
		Version:        released,
		Notes:          releaseNotes(release.Body),
		InstallMethods: []string{"linux", "manual"},
		Platforms: []ports.NodeReleasePlatform{
			{OS: "linux", Arch: "amd64"}, {OS: "linux", Arch: "arm64"},
			{OS: "darwin", Arch: "amd64"}, {OS: "darwin", Arch: "arm64"},
			{OS: "windows", Arch: "amd64"}, {OS: "windows", Arch: "arm64"},
		},
	}
}

// maxNotesRunes bounds what the dialog renders. The release body is written for a
// release page — headings, contributor links, a full changelog — and the dialog
// shows it under a version selector, so it is truncated rather than trusted to be
// short. The bound includes the truncation marker.
const maxNotesRunes = 600

// notesTruncationMarker closes a truncated body as a paragraph of its own, so it
// cannot fuse onto a list item or heading when the dialog renders the notes as
// Markdown.
const notesTruncationMarker = "\n\n…"

// releaseNotes returns the body, or its longest prefix that ends on a LINE
// BOUNDARY and fits the bound with the marker. The dialog renders Markdown, and a
// generated changelog is one pull request per line, each ending in its URL: a cut
// at a fixed rune count lands inside one of those URLs, and the renderer links the
// truncated address. With no line break inside the bound it falls back to the
// last space, which still never splits a URL; only a body with neither is cut
// hard.
func releaseNotes(body string) string {
	trimmed := strings.TrimSpace(body)
	runes := []rune(trimmed)
	if len(runes) <= maxNotesRunes {
		return trimmed
	}
	window := string(runes[:maxNotesRunes-len([]rune(notesTruncationMarker))])
	cut := strings.LastIndex(window, "\n")
	if cut <= 0 {
		cut = strings.LastIndexAny(window, " \t")
	}
	if cut > 0 {
		window = window[:cut]
	}
	return strings.TrimSpace(window) + notesTruncationMarker
}

func packageName(version string, platform ports.NodeReleasePlatform) string {
	ext := ".tar.gz"
	if platform.OS == "windows" {
		ext = ".zip"
	}
	return fmt.Sprintf("passwall-node_%s_%s_%s%s", version, platform.OS, platform.Arch, ext)
}

// catalogEntry builds one entry. tag is the ADDRESS the release was published
// under and candidate.Version is the identity inside it; the asset names use the
// second and the URLs use the first.
//
// THE ASSETS ARE WHAT DECIDE, and they are the reason this catalog needs no
// review: a release is offered a method only when the artifacts that method needs
// are actually published — the checksum manifest is present, each package is in
// the `uploaded` state with a non-zero size, and every download URL is exactly the
// one GitHub serves. What no longer exists is a document saying which of those
// were REHEARSED, so the list is what is installable rather than what was tested.
func catalogEntry(candidate candidateRelease, release githubRelease, tag string) (ports.NodeReleaseCatalogEntry, error) {
	entry := ports.NodeReleaseCatalogEntry{
		Version: candidate.Version, ReleaseTag: tag,
		Channel: "stable", PublishedAt: release.PublishedAt.UTC(),
		ReleaseURL: releaseBase + "tag/" + tag, Notes: candidate.Notes,
		Methods: []string{}, Platforms: []ports.NodeReleasePlatform{},
	}
	if release.Prerelease {
		entry.Channel = "testing"
	}
	// THERE IS ONE SCHEME, AND THE NAMESPACE IS WHAT SAYS SO. A tag inside
	// `release/` carries a product version of its own; the legacy scheme is gone, so
	// a tag outside the namespace is not a release this project publishes — which is
	// why a release whose tag does not parse is skipped rather than reported.
	entry.ProductVersion = candidate.Version
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
	for _, platform := range candidate.Platforms {
		if available(packageName(candidate.Version, platform)) {
			entry.Platforms = append(entry.Platforms, platform)
			if platform.OS == "linux" {
				linux[platform.Arch] = true
			}
		}
	}
	// NO DOCKER ARM. It used to be offered when a curated `docker_published_tag`
	// equalled the version, which is a statement about a container registry this
	// panel cannot read — so it was a claim about someone else's systems, made on
	// the strength of a document edit. Docker installs update through the
	// container, not through this task, so nothing here needs it.
	for _, method := range []string{"linux", "manual"} {
		if !contains(candidate.InstallMethods, method) {
			continue
		}
		if method == "manual" && len(entry.Platforms) > 0 ||
			method == "linux" && linux["amd64"] && linux["arm64"] {
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
