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

	"github.com/KazuhaHub/passwall-sub-panel/internal/pkg/log"

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

// embeddedCatalog is the document this build ships FOR ITS OWN MAJOR: a
// byte-identical copy of the published one.
//
// WHY A COPY AT ALL, when the published document is fetched anyway. The fetch can
// fail on its own — a network policy that blocks raw.githubusercontent while
// allowing api.github.com is the ordinary case — and this is the allow-list of
// last resort for it. WHAT IT IS NOT IS AN OFFLINE CATALOG: every entry still has
// to be confirmed against the GitHub API before it is offered, so a panel with no
// egress at all has no catalog either way. What the copy buys is that a failed
// fetch cannot look like "no release was ever reviewed".
//
// A COPY RATHER THAN A SECOND DOCUMENT. This used to be a hand-maintained
// registry with its own shape — a psp_major on every row, its own method
// vocabulary — kept in step with the manifest by hand, so adding a release meant
// editing both and republishing the panel. The file below is the same JSON as the
// published document, and TestTheEmbeddedCatalogMatchesThePublishedOne fails the
// build when the two drift.
//
//go:embed passwall-node-v4.json
var embeddedCatalog []byte

var errUnavailable = errors.New("Node release source is unavailable")

type Options struct {
	// HTTPClient is a test seam; requests and redirects remain restricted to
	// the official HTTPS endpoint even with an injected client.
	HTTPClient *http.Client
	Now        func() time.Time
	PSPMajor   int
}

// nodeCatalogDocument is the published Passwall Node document, decoded for the
// fields THIS package reads.
//
// TWO AUDIENCES READ THE SAME FILE AND ASK DIFFERENT QUESTIONS. The CI planner and
// the compatibility matrix read released_nodes[] by position at min_supported to
// decide what to TEST. This reads the same rows for what may be OFFERED, plus the
// per-release review notes the dialog shows. Unknown fields are therefore
// tolerated on purpose: the rows also carry protocol_version, base_sync,
// remote_upgrade and upgrade_methods, and neither audience should have to know the
// other's vocabulary to read its own half.
type nodeCatalogDocument struct {
	SchemaVersion int               `json:"schema_version"`
	PanelMajor    int               `json:"panel_major"`
	Releases      []reviewedRelease `json:"released_nodes"`
}

// reviewedRelease is one reviewed release, in the shape the document publishes.
//
// psp_major IS GONE and its absence is the point: the document is addressed by the
// panel major in its own NAME, so a second copy of that number on every row was a
// second place to get it wrong. The top-level panel_major is the authority.
type reviewedRelease struct {
	Version string `json:"version"`
	Notes   string `json:"notes"`
	// InstallMethods is what the OPERATOR picks — linux, docker, manual — and it is
	// a different axis from the document's upgrade_methods (linux-systemd,
	// managed-docker), which is what the upgrade machinery means by a path. Both
	// survive because they answer different questions, and this one is
	// cross-checked against the published assets in catalogEntry.
	InstallMethods     []string                    `json:"install_methods"`
	Platforms          []ports.NodeReleasePlatform `json:"platforms"`
	DockerPublishedTag string                      `json:"docker_published_tag"`
}

type Catalog struct {
	client *http.Client
	now    func() time.Time
	// major is the panel major this catalog was built for, and it decides which
	// published document this build asks for.
	major int
	// reviewed is the allow-list IN FORCE: the copy this build ships until the
	// published document can be read, and the published document's rows from then
	// on. A refresh that fails leaves it where it was, which is the same
	// degradation the ranges take — against a fetch failure, the last reviewed set
	// rather than no catalog.
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
// THE STAMP IS A VERSION, and reading its major is the whole job. This used to
// ask the INSTALLER's rule, which knows only the legacy v-prefixed shape — so the
// first build stamped `4.0.0` would have been read as having no canonical
// identity at all, and the catalog would have been silently disabled rather than
// answering with the wrong major.
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
	// THE SHIPPED COPY IS VALIDATED AT CONSTRUCTION, because a build that ships a
	// document it cannot read has no allow-list to fall back to and no way to say
	// so later — the failure would surface as an empty catalog on a panel whose
	// network is fine.
	installedMajor, installed, err := decodeCatalogDocument(embeddedCatalog, 0)
	if err != nil {
		return nil, fmt.Errorf("the embedded Node release catalog is unusable: %w", err)
	}
	// A MAJOR THIS BUILD SHIPS NO REVIEWS FOR GETS AN EMPTY SET, NOT AN ERROR.
	// PSPMajorForVersion answers for whatever a build is stamped as, and a build
	// stamped for a panel major this binary was not compiled for has no reviews of
	// its own. Refusing to construct would make an optional metadata feature stop
	// the panel from booting; an empty catalog offers nothing, which is the same
	// answer an incompatible major got before.
	reviewed := installed
	if installedMajor != opts.PSPMajor {
		reviewed = nil
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
	return &Catalog{client: client, now: opts.Now, major: opts.PSPMajor, reviewed: reviewed}, nil
}

// decodeCatalogDocument validates a document and returns the panel major it names
// with the rows for that major.
//
// IT IS APPLIED TO UNTRUSTED INPUT — whatever the fetch returned — as well as to
// the copy this build ships, and the same rules cover both: every row has to name
// a release version, carry review notes, at least one install method and at least
// one platform. The panel major is checked against the DOCUMENT's own field rather
// than trusted from the name it arrived under, so a v5 document served at the v4
// address is refused instead of installing another major's releases.
func decodeCatalogDocument(raw []byte, wantMajor int) (int, []reviewedRelease, error) {
	var document nodeCatalogDocument
	if err := json.Unmarshal(raw, &document); err != nil {
		return 0, nil, errors.New("invalid reviewed Node release compatibility")
	}
	if document.PanelMajor < 1 {
		return 0, nil, errors.New("the Node release catalog names no panel major")
	}
	if wantMajor != 0 && document.PanelMajor != wantMajor {
		return document.PanelMajor, nil, fmt.Errorf("the document is for panel major %d, and this catalog is for %d", document.PanelMajor, wantMajor)
	}
	if len(document.Releases) == 0 || len(document.Releases) > maxReviewed {
		return 0, nil, errors.New("invalid reviewed Node release compatibility")
	}
	seen := make(map[string]bool, len(document.Releases))
	selected := make([]reviewedRelease, 0, len(document.Releases))
	for _, entry := range document.Releases {
		if !validReviewed(entry) || seen[entry.Version] {
			return 0, nil, errors.New("invalid reviewed Node release compatibility")
		}
		seen[entry.Version] = true
		selected = append(selected, entry)
	}
	return document.PanelMajor, selected, nil
}

func validReviewed(entry reviewedRelease) bool {
	if !version.IsReleaseVersion(entry.Version) ||
		len(entry.Notes) == 0 || len(entry.Notes) > 4096 || len(entry.InstallMethods) == 0 ||
		len(entry.Platforms) == 0 || len(entry.Platforms) > 6 {
		return false
	}
	methods := make(map[string]bool)
	for _, method := range entry.InstallMethods {
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
	reviewed, err := c.reviewedReleases(ctx)
	if err != nil {
		return ports.NodeReleaseList{}, err
	}
	for _, reviewed := range reviewed {
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
	// Publication time is the axis a version string cannot reinterpret: the
	// registry used to hold a beta line whose publication order and version order
	// disagreed (beta11 sorts below beta9 as text), and sorting by the version put
	// the older release at the top of the list that drives the upgrade dialog.
	//
	// THE VERSION IS ONLY A TIE-BREAK, for releases sharing a publication instant,
	// and it is now the PROJECT'S OWN order rather than x/mod/semver. Semver was
	// chosen while every version had three segments; it cannot parse the fourth
	// BUILD component at all — it answers zero for `4.0.0.1`, which the sort reads
	// as equality and leaves the registry's file order standing. Two releases with
	// the same publication time cannot disagree about their publication order, but
	// they can disagree about their versions, and that is the one thing this has
	// to get right.
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

// reviewedReleases returns the allow-list: the PUBLISHED document when it can be
// had, and the copy this build ships when it cannot.
//
// IT DEGRADES RATHER THAN REFUSING, and that is this package's existing stance
// rather than a new one — the catalog's failure contract has always been "offer
// nothing rather than offer something unreviewed". A document that cannot be
// fetched, or that arrives and cannot be read, is therefore not an empty catalog:
// it is the same reviewed set the panel shipped with, older by however long the
// fetch has been failing, with the degradation logged rather than swallowed.
func (c *Catalog) reviewedReleases(ctx context.Context) ([]reviewedRelease, error) {
	raw, err := c.fetchCatalogDocument(ctx)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		log.Warn("Node release catalog document not fetched; offering the reviewed set already in force", "err", err)
		return c.reviewedSet(), nil
	}
	_, rows, err := decodeCatalogDocument(raw, c.major)
	if err != nil {
		log.Warn("Node release catalog document not usable; offering the reviewed set already in force", "err", err)
		return c.reviewedSet(), nil
	}
	// THE PUBLISHED DOCUMENT BECOMES THE SET IN FORCE, so a later fetch failure
	// degrades to the newest reviewed set rather than to the one this binary
	// shipped with.
	c.mu.Lock()
	c.reviewed = rows
	c.mu.Unlock()
	return rows, nil
}

// reviewedSet reads the allow-list in force. The mutex is NOT held by the caller:
// List releases it before fetching, which is what lets a refresh publish its
// result here.
func (c *Catalog) reviewedSet() []reviewedRelease {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.reviewed
}

// fetchCatalogDocument reads the published document for THIS build's major.
//
// THE ADDRESS IS THE COMPATIBILITY DOCUMENTS' ADDRESS — same base, same naming —
// while the TRANSPORT stays this package's. The catalog has a test seam that the
// ranges do not, and borrowing their client would make this package's tests depend
// on process-global state it does not own.
func (c *Catalog) fetchCatalogDocument(ctx context.Context) ([]byte, error) {
	name := version.RemoteNodeCatalogDocument(c.major)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, version.RemoteCompatURLBase+name, nil)
	if err != nil {
		return nil, errUnavailable
	}
	req.Header.Set("Accept", "application/json")
	resp, err := c.client.Do(req)
	if err != nil {
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
	return body, nil
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
		if !contains(reviewed.InstallMethods, method) {
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
