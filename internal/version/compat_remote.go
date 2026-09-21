package version

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/mod/semver"
)

// defaultRemoteCompatURLBase is the GitHub raw path under which the compat JSON
// files are published. WHICH files a build reads is compatDocumentSources' job,
// and there are two shapes there:
//
//   - a LEGACY build reads ONE per-major manifest, named after its compatibility
//     major (v3.x → v3.json). That route is FROZEN rather than removed: builds
//     that already shipped read it by a name their own version derives, and a
//     document they fetch by name is a compatibility surface, not a file to tidy.
//   - a PRODUCT build reads ONE DOCUMENT PER PRODUCT, named after the PANEL MAJOR
//     its own version carries (4.0.0 → 3x-ui-v4.json, sui-v4.json).
//
// The address is shared with the Node release catalog, which fetches
// passwall-node-v<major>.json from here through RemoteCompatURLBase.
const defaultRemoteCompatURLBase = "https://raw.githubusercontent.com/KazuhaHub/passwall-sub-panel/main/docs/compat/"

// remoteFetchThrottle gates how often RefreshRemoteCompat actually hits the
// network. The Test handler triggers a refresh on every "test connection"
// click; when admin opens the Servers page the frontend fires N parallel
// testServer calls (one per panel) — without throttling all N would hit
// GitHub raw simultaneously. 60s lets one fetch serve every panel on a
// page open and is well under the lifetime of an admin session.
const remoteFetchThrottle = 60 * time.Second

// httpFetchTimeout caps each remote fetch so a slow / hanging GitHub raw
// can never block the Test handler's response path for long.
const httpFetchTimeout = 8 * time.Second

// pspMajorRe extracts the compatibility major from a LEGACY PSP version — the
// v-prefixed form ("v3.6.0", "v3.6.0-beta.7").
//
// THE v IS REQUIRED, and requiring it is the point. Under the product scheme a
// version is three integers with no prefix, and its first integer is a RELEASE
// LINE, not a compatibility major: 102.1.0 is the hundred-and-second release
// line, not "compat major 102", and there is no v102.json for it. Accepting an
// unprefixed version here would have derived that path and fetched a file that
// does not exist — or worse, that exists and means something else.
//
// So a build whose version is not the legacy form gets no PER-MAJOR URL from this
// pattern. It gets something else entirely: the per-product documents, whose names
// are derived from the same first segment but through compatDocumentSources rather
// than from a v-prefixed string. Keeping the two apart is what stops an unprefixed
// version from silently inheriting a file number it has no claim to — the window
// each document carries is the other half of that guard.
var pspMajorRe = regexp.MustCompile(`^v(\d+)\.`)

// schemaVersion is what the base per-major JSON files must carry. Bumped to 2
// when the v3.6.0-beta.7 redesign switched from a single-file map keyed
// by "vX.Y" to per-major files + entries array + psp_min/psp_max range
// per row.
const schemaVersion = 2

// rangeOverlaySchemaVersion adds full SemVer matching for PSP range endpoints,
// including prerelease identifiers. The base manifest remains schema v2 so old
// PSP binaries keep reading their conservative ranges; builds that understand
// this overlay can distinguish v4.0.0-beta.8 from beta.9 without silently
// certifying the older binary.
const rangeOverlaySchemaVersion = 3

// remoteCompatPayload mirrors docs/compat/v<MAJOR>.json (schema_version 2):
//
//	{
//	  "schema_version": 2,
//	  "major": 3,
//	  "updated_at": "...",
//	  "entries": [
//	    {
//	      "psp_min": "v3.6.0",
//	      "psp_max": "v3.6.99",
//	      "min_xui": "3.1.0",
//	      "max_tested_xui": "3.1.0",
//	      "notes": "..."
//	    }
//	  ]
//	}
//
// Unknown fields are tolerated (Go json default) so an old PSP can still
// consume a newer JSON as long as the v2 essentials are present.

type remoteCompatPayload struct {
	SchemaVersion int                    `json:"schema_version"`
	Major         int                    `json:"major"`
	UpdatedAt     string                 `json:"updated_at"`
	// Product is which product's ranges this payload carries, and it travels with
	// the payload for the same reason the window below does: the snapshot, the
	// revision guard and the install all have to know WHICH document they are
	// about. Empty means the payload came from a per-major manifest, which carries
	// both panels in one document — the frozen legacy route, where "both" is the
	// correct answer rather than a missing one.
	Product   string                 `json:"product,omitempty"`
	Entries   []remoteCompatPSPEntry `json:"entries"`
	// Advisories is the optional top-level version→advisory map surfaced in the
	// pre-upgrade confirm dialog. Top-level (not per-entry) because "what breaks
	// when you upgrade TO 3X-UI X" is independent of which PSP version is asking.
	Advisories map[string]XUIAdvisory `json:"xui_advisories,omitempty"`
	// SUIEntries is the S-UI counterpart of Entries, matched the same
	// first-match-wins way against this PSP version. OPTIONAL: a JSON without it
	// (every file published before S-UI gating existed) is valid and simply
	// leaves the S-UI gate unpublished — see compat_sui.go.
	SUIEntries []remoteCompatSUIEntry `json:"sui_entries,omitempty"`
	// SUIAdvisories mirrors Advisories for S-UI releases.
	SUIAdvisories map[string]XUIAdvisory `json:"sui_advisories,omitempty"`
	// RangeOverlay points to an optional same-origin schema-v3 document whose
	// entries replace only the XUI/SUI ranges. Advisories stay in this base
	// document so old readers retain the full upgrade guidance.
	RangeOverlay string `json:"range_overlay,omitempty"`
	// AppliesToPSP is set when this payload came from a per-product ranges
	// document, and it is what makes such a document installable at all.
	//
	// TWO WAYS TO SAY WHICH BUILDS A DOCUMENT IS FOR, because there are two kinds
	// of document. A per-major manifest says it by its NAME and its `major` field:
	// a legacy build derives its major and reads the file with that name. A
	// per-product document is named after the PANEL major, which a product version
	// carries as its first segment — and a name is not evidence, so it says it
	// HERE as well: the name decides where to look, and this window decides
	// whether what was found counts. Set means "match by this window"; absent
	// means "match by the derived major", which is what every manifest does.
	AppliesToPSP *CompatPSPRange `json:"applies_to_psp,omitempty"`
}

// remoteCompatPSPEntry covers one PSP version range. In a schema-v2 base
// manifest psp_min / psp_max are stable-form closed interval endpoints and
// prereleases match the stable they target. A schema-v3 range overlay compares
// the full SemVer, allowing a fixed beta to be separated from earlier builds.
type remoteCompatPSPEntry struct {
	PSPMin       string `json:"psp_min"`
	PSPMax       string `json:"psp_max"`
	MinXUI       string `json:"min_xui"`
	MaxTestedXUI string `json:"max_tested_xui"`
	Notes        string `json:"notes,omitempty"`
}

// remoteCompatSUIEntry is remoteCompatPSPEntry's S-UI twin. min_sui is optional
// even when the row exists: a ceiling can be verified before anyone has
// established how far back support actually reaches, and CheckSUI only reports
// CompatTooOld against a floor that was explicitly published.
type remoteCompatSUIEntry struct {
	PSPMin       string `json:"psp_min"`
	PSPMax       string `json:"psp_max"`
	MinSUI       string `json:"min_sui,omitempty"`
	MaxTestedSUI string `json:"max_tested_sui"`
	Notes        string `json:"notes,omitempty"`
}

// refreshState — two distinct concurrency mechanisms:
//   - refreshInflight: classic single-flight, at most one fetch in flight.
//     N parallel callers (Servers-page open) collapse to one network call.
//   - refreshLastAt: 60s throttle, ONLY advanced on success. A failed
//     fetch leaves it unchanged so the next caller can immediately retry
//     instead of waiting out the throttle.
var (
	refreshMu        sync.Mutex
	refreshLastAt    time.Time
	refreshLastError error
	refreshInflight  bool
)

// shouldFetchCompat decides whether RefreshRemoteCompat proceeds to the network,
// given the last SUCCESSFUL fetch time, the current time, and the force flag.
// force ignores the 60s throttle — the panel-upgrade gate passes force=true so
// its "is this 3X-UI version supported?" check reflects the freshest published
// tested range, not a possibly-stale cache from boot or the last Servers-page
// open. Without force, a fetch within the throttle window is skipped (reuse the
// active state) so a Servers-page open firing N parallel tests hits GitHub once.
func shouldFetchCompat(lastAt, now time.Time, force bool) bool {
	if force || lastAt.IsZero() {
		return true
	}
	return now.Sub(lastAt) >= remoteFetchThrottle
}

// RefreshRemoteCompat fetches the per-major compat JSON for THIS PSP build,
// finds the entry containing the current version, and installs its
// max_tested_xui via SetActiveMaxTestedXUI. Returns nil on success OR
// when short-circuited by single-flight / throttle. Returns a non-nil
// error only when this call actually attempted the fetch and it failed.
//
// urlOverride is for tests / admin override; "" uses the default per-major
// URL computed from version.Version. force bypasses the 60s throttle (but not
// single-flight) — used by the panel-upgrade pre-flight so the support gate
// never decides on a stale cache.
func RefreshRemoteCompat(ctx context.Context, urlOverride string, force bool) error {
	sources, err := compatDocumentSources()
	if urlOverride != "" {
		// AN OVERRIDE POINTS AT ONE DOCUMENT, and it carries no product because an
		// operator may point at either kind: the apply path takes the product from
		// the document when the source does not already know it.
		sources = []compatSource{{Name: urlOverride, URL: urlOverride}}
		err = nil
	}
	if err != nil {
		return err
	}

	refreshMu.Lock()
	if refreshInflight {
		refreshMu.Unlock()
		return nil
	}
	if !shouldFetchCompat(refreshLastAt, time.Now(), force) {
		refreshMu.Unlock()
		return nil
	}
	refreshInflight = true
	refreshMu.Unlock()

	err = fetchAndApplyAll(ctx, sources)

	refreshMu.Lock()
	refreshInflight = false
	refreshLastError = err
	if err == nil {
		refreshLastAt = time.Now()
	}
	refreshMu.Unlock()
	return err
}

// fetchAndApplyAll reads every document this build reads, and EACH STANDS ON ITS
// OWN.
//
// ONE PRODUCT'S DOCUMENT FAILING MUST NOT HOLD THE OTHER'S CEILING. They are
// separate files, reviewed on separate schedules, and a 404 or a malformed row in
// one says nothing about the other. Today's single document made that question
// moot; with two, all-or-nothing would mean a typo in the S-UI file silently
// freezes the 3X-UI ceiling until somebody notices a number that stopped moving.
// So every source is attempted and the failures are reported together.
func fetchAndApplyAll(ctx context.Context, sources []compatSource) error {
	var failures []string
	for _, source := range sources {
		if err := fetchAndApply(ctx, source); err != nil {
			failures = append(failures, fmt.Sprintf("%s: %v", source.Name, err))
		}
	}
	if len(failures) > 0 {
		return errors.New(strings.Join(failures, "; "))
	}
	return nil
}

// LastRefreshError returns whatever the most recent fetch produced (nil on
// success, fetch/parse error otherwise). Surfaces to admin UI / docs.
func LastRefreshError() error {
	refreshMu.Lock()
	defer refreshMu.Unlock()
	return refreshLastError
}

// LastRefreshAt returns the wall-clock of the most recent SUCCESSFUL fetch
// (zero value when never attempted or only failures so far).
func LastRefreshAt() time.Time {
	refreshMu.Lock()
	defer refreshMu.Unlock()
	return refreshLastAt
}

// pspMajor extracts the compatibility major from a legacy version string.
// Returns 0/false for "dev", for a product-scheme version, and for anything else
// that is not the v-prefixed form — each of which means "no per-major manifest
// is derivable from this".
func pspMajor(v string) (int, bool) {
	m := pspMajorRe.FindStringSubmatch(v)
	if len(m) < 2 {
		return 0, false
	}
	n, err := strconv.Atoi(m[1])
	if err != nil || n <= 0 {
		return 0, false
	}
	return n, true
}

// revisionRegressed reports whether the fetched manifest is OLDER than the one
// already applied.
//
// THE THIRD REFUSAL. fetchAndApply already refuses a document whose schema it
// does not understand and one that declares the wrong major. Neither catches a
// document that is perfectly valid and perfectly well-formed but older than what
// is in force — a stale CDN edge, a reverted commit, a rollback of the published
// file. Without this, that document silently replaces a newer tested range with
// an older one, and the panel then refuses an upgrade an admin was already told
// was supported, with nothing recording why.
//
// A manifest that cannot be ordered is refused rather than trusted, in both
// directions: an unparseable fetched revision cannot be shown to be newer, and
// an unparseable applied revision means there is nothing to compare against. The
// fail-closed outcome is the same either way — the range already in force stays.
func revisionRegressed(applied, fetched string) (bool, error) {
	fetchedAt, err := parseManifestRevision(fetched)
	if err != nil {
		return false, fmt.Errorf("compat JSON updated_at %q cannot be ordered: %w", fetched, err)
	}
	if applied == "" {
		return false, nil
	}
	appliedAt, err := parseManifestRevision(applied)
	if err != nil {
		return false, fmt.Errorf("the applied compat revision %q cannot be ordered, so a fetched one cannot be compared against it: %w", applied, err)
	}
	return fetchedAt.Before(appliedAt), nil
}

// parseManifestRevision accepts the two forms the published manifests use.
//
// DATE-ONLY IS THE FORM THE PUBLISHED DOCUMENTS ACTUALLY CARRY — v3.json says
// "2026-09-16", and the per-product documents that replaced the v4 manifests say
// the same. An RFC3339 parser alone would therefore refuse the real documents and
// take the whole compat load down with them, which is how this was found: a test
// that drives the real document rather than a fixture written to match the parser.
//
// Both forms resolve to an instant, so a file may move from one to the other.
// Moving to a full timestamp on the SAME DAY reads as a regression, because the
// bare date resolves to midnight; keep one form per file.
func parseManifestRevision(value string) (time.Time, error) {
	if at, err := time.Parse(time.RFC3339, value); err == nil {
		return at, nil
	}
	return time.Parse("2006-01-02", value)
}

// appliedRevision is the updated_at of the document currently in force, PER
// DOCUMENT. It is kept independently of the refresh mutex because fetchAndApply
// also runs while that mutex is held.
//
// PER DOCUMENT, NOT PER PROCESS, and the difference is a range silently lost. The
// products are reviewed at different times, so their documents carry different
// dates and arrive in different orders: with one value, the later S-UI document
// advances the revision a 3X-UI document is then compared against, and an
// in-order 3X-UI document is refused as a regression. Nothing reports it — the
// panel keeps serving the previous ceiling and looks healthy.
var (
	appliedRevisionMu sync.Mutex
	appliedRevision   = map[string]string{}
)

// revisionKey is the identity a revision belongs to: the product whose document
// it came from, or "" for the per-major manifest that carries both.
func revisionKey(product string) string { return product }

func setAppliedRevision(product, revision string) {
	appliedRevisionMu.Lock()
	appliedRevision[revisionKey(product)] = revision
	appliedRevisionMu.Unlock()
}

func currentAppliedRevision(product string) string {
	appliedRevisionMu.Lock()
	defer appliedRevisionMu.Unlock()
	return appliedRevision[revisionKey(product)]
}

// resetAppliedRevisions clears every recorded revision. A test seam: the revisions
// are process state, and a test that leaves one behind makes the next test's first
// document look like a regression.
func resetAppliedRevisions() {
	appliedRevisionMu.Lock()
	appliedRevision = map[string]string{}
	appliedRevisionMu.Unlock()
}

// fetchAndApply reads one compat document and installs it if it applies here.
//
// WHICH DOCUMENT IT IS COMES FROM ITS OWN SCHEMA, not from which build is asking.
// A URL override can point either somewhere, and dispatching on the build would
// parse a document by the wrong rules and then accept it.
func fetchAndApply(ctx context.Context, source compatSource) error {
	raw, err := fetchCompatDocument(ctx, source.URL)
	if err != nil {
		return err
	}
	var kind struct {
		SchemaVersion int    `json:"schema_version"`
		Product       string `json:"product"`
	}
	if err := json.Unmarshal(raw, &kind); err != nil {
		return fmt.Errorf("decode JSON: %w", err)
	}
	if kind.SchemaVersion == panelRangesSchema {
		return applyProductRangesDocument(raw, source.Product, time.Now().UTC())
	}
	return applyPerMajorManifest(ctx, raw, source.URL)
}

// applyXUICompatDocument installs the 3X-UI ranges document for the CURRENT build.
func applyXUICompatDocument(raw []byte, now time.Time) error {
	return applyProductRangesDocument(raw, productXUI, now)
}

// applySUICompatDocument installs the S-UI ranges document for the CURRENT build.
func applySUICompatDocument(raw []byte, now time.Time) error {
	return applyProductRangesDocument(raw, productSUI, now)
}

// applyProductRangesDocument validates and installs ONE product's ranges document.
//
// expectedProduct is the product the ADDRESS claims, and is empty when the caller
// does not know — a URL override, which may point at either kind of document.
// When it is known the document has to agree: a `sui-v4.json` served at the 3X-UI
// address is a wrong file at that URL, and installing nothing under the 3X-UI
// name would read as "the range was reviewed and removed" rather than "the wrong
// document is published". The ceiling would simply stop moving.
//
// IT IS CONVERTED, NOT INSTALLED RAW. Its payload is the same shape the manifest
// carries, so once the applicability check has passed it becomes an ordinary
// payload and every step downstream — first-match-wins entries, the advisories,
// the revision guard, the snapshot a boot replays — is unchanged. The window AND
// the product travel with it, so those replays make the same decision the fetch
// just made instead of guessing from which fields are populated.
func applyProductRangesDocument(raw []byte, expectedProduct string, now time.Time) error {
	policy, err := ParsePanelRangesPolicy(raw, now)
	if err != nil {
		return err
	}
	if expectedProduct != "" && policy.Product != expectedProduct {
		return fmt.Errorf("%w: the address names %s and the document says %s", ErrPanelRangesProduct, expectedProduct, policy.Product)
	}
	window := policy.AppliesToPSP
	payload := remoteCompatPayload{
		SchemaVersion: schemaVersion,
		Product:       policy.Product,
		UpdatedAt:     policy.IssuedAt.UTC().Format(time.RFC3339),
		Entries:       policy.Entries,
		SUIEntries:    policy.SUIEntries,
		Advisories:    policy.Advisories,
		SUIAdvisories: policy.SUIAdvisories,
		AppliesToPSP:  &window,
	}
	// COMPARED AGAINST THIS PRODUCT'S OWN REVISION, which is the whole reason the
	// guard is keyed. A document is newer or older than the one it replaces, not
	// than whatever the other product published last week.
	applied := currentAppliedRevision(policy.Product)
	regressed, err := revisionRegressed(applied, payload.UpdatedAt)
	if err != nil {
		return err
	}
	if regressed {
		return fmt.Errorf("%s ranges document is dated %s, older than the applied %s revision %s; refusing to replace a newer tested range with an older one",
			policy.Product, payload.UpdatedAt, policy.Product, applied)
	}
	if err := applyCompatPayload(payload); err != nil {
		return err
	}
	storePolicySnapshotOrWarn(payload)
	return nil
}

// applyPerMajorManifest is the per-major path: a manifest fetched by a URL that
// names its major, optionally with a range overlay folded in.
func applyPerMajorManifest(ctx context.Context, raw []byte, url string) error {
	payload, err := decodeCompatPayload(raw)
	if err != nil {
		return err
	}
	if payload.SchemaVersion != schemaVersion {
		return fmt.Errorf("compat JSON schema_version %d, this PSP build only supports base schema %d", payload.SchemaVersion, schemaVersion)
	}
	currentMajor, ok := pspMajor(Version)
	if !ok {
		// NOT JUST A REFUSAL: it names what this build DOES read. A URL override
		// can point a build at a manifest, and without this the operator gets
		// "cannot derive a major" with no hint that a document exists which is
		// addressed by name and would have worked.
		return fmt.Errorf("cannot derive a PSP major from version %q, so no per-major manifest applies to it, "+
			"and no document is named after a major it does not carry; a product build reads one document per product", Version)
	}
	if payload.Major != currentMajor {
		// Self-validation: PSP fetched v<currentMajor>.json but the
		// file's `major` field says something else. Either GitHub
		// served a wrong file or admin accidentally pushed v3.json
		// content to v4.json. Refuse to apply so we don't install
		// the wrong major's range.
		return fmt.Errorf("compat JSON declares major=%d but this PSP is major=%d (wrong file at URL?)", payload.Major, currentMajor)
	}
	// Checked HERE, before the overlay fetch and before any mutation, so a
	// document that will be refused costs no second request and cannot
	// half-install a range.
	applied := currentAppliedRevision("")
	regressed, err := revisionRegressed(applied, payload.UpdatedAt)
	if err != nil {
		return err
	}
	if regressed {
		return fmt.Errorf("compat JSON updated_at %s is older than the applied revision %s; refusing to replace a newer tested range with an older one", payload.UpdatedAt, applied)
	}

	// New readers can opt into prerelease-aware ranges without changing the
	// schema-v2 base file old readers consume. Fetch and validate the overlay
	// before mutating active state so a missing or malformed overlay cannot
	// partially install a new range.
	if payload.RangeOverlay != "" {
		overlayURL, err := resolveRangeOverlayURL(url, payload.RangeOverlay)
		if err != nil {
			return err
		}
		overlay, err := fetchCompatPayload(ctx, overlayURL)
		if err != nil {
			return fmt.Errorf("fetch range overlay: %w", err)
		}
		if overlay.SchemaVersion != rangeOverlaySchemaVersion {
			return fmt.Errorf("compat range overlay schema_version %d, want %d", overlay.SchemaVersion, rangeOverlaySchemaVersion)
		}
		if overlay.Major != currentMajor {
			return fmt.Errorf("compat range overlay declares major=%d but this PSP is major=%d", overlay.Major, currentMajor)
		}
		payload.SchemaVersion = overlay.SchemaVersion
		payload.Entries = overlay.Entries
		payload.SUIEntries = overlay.SUIEntries
	}

	if err := applyCompatPayload(payload); err != nil {
		return err
	}
	storePolicySnapshotOrWarn(payload)
	return nil
}

// applyCompatPayload installs a fully-resolved policy document for the CURRENT
// build, and is the ONE place that decides whether a document applies here.
//
// Two entry points use it: the network fetch, and the boot path replaying the
// last validated snapshot. They must agree. A boot that installed a cached
// document by a looser rule than the fetch that stored it is how an instance
// comes back from a restart believing a range its own version is not covered by
// — and the cached document could have been written by a different build.
//
// The schema check accepts both the base and the overlay schema because by the
// time this runs the fetch path has merged the overlay in, and the merged
// document carries the overlay's schema number.
// payloadApplies reports whether this payload was published for the build that
// is running, and says why not when it was not.
//
// TWO WAYS TO SAY IT, because there are two kinds of document. A per-major
// manifest says it by its NAME and its `major` field, and only a legacy build
// reads one. A per-product document carries its own window: its name is DERIVED
// from this build's own first segment, so a document that is not about this build
// can still arrive under a name this build asked for — by a mistaken publication,
// or by a window that was narrowed after the fact.
//
// THE CHECK IS THE SAME ONE THE FETCH MAKES. Sharing it is the point: a document
// that installed from a fetch and then fails to install from the cache would
// leave a range in force that the next boot silently drops.
func payloadApplies(payload remoteCompatPayload) error {
	if payload.AppliesToPSP != nil {
		window := *payload.AppliesToPSP
		if !IsReleaseVersion(Version) {
			return fmt.Errorf("this build's version %q is not a release identity, so it cannot be matched against %s..%s",
				Version, window.Min, window.Max)
		}
		if CompareRelease(Version, window.Min) < 0 || CompareRelease(Version, window.Max) > 0 {
			return fmt.Errorf("the document applies to %s..%s, not to %q", window.Min, window.Max, Version)
		}
		return nil
	}
	// Checked BEFORE the overlay fetch so a document that will be refused costs
	// no second request. applyCompatPayload makes the same check again at the end;
	// this one exists for its position, not for its verdict.
	currentMajor, ok := pspMajor(Version)
	if !ok {
		return fmt.Errorf("cannot derive a PSP major from version %q, so no per-major manifest applies to it, "+
			"and no document is named after a major it does not carry; a product build reads one document per product", Version)
	}
	if payload.Major != currentMajor {
		// Self-validation: PSP fetched v<currentMajor>.json but the file's
		// `major` field says something else — a wrong file at the URL, or a
		// cached document from another major. Refuse rather than install
		// another major's range.
		return fmt.Errorf("compat policy declares major=%d but this PSP is major=%d (wrong file at URL?)", payload.Major, currentMajor)
	}
	return nil
}

func applyCompatPayload(payload remoteCompatPayload) error {
	if payload.SchemaVersion != schemaVersion && payload.SchemaVersion != rangeOverlaySchemaVersion {
		return fmt.Errorf("compat policy schema_version %d, this PSP build only supports base schema %d and overlay schema %d",
			payload.SchemaVersion, schemaVersion, rangeOverlaySchemaVersion)
	}
	if err := payloadApplies(payload); err != nil {
		return err
	}
	// WHICH SUBSET TO INSTALL FOLLOWS FROM THE PRODUCT, and the one empty product
	// is the case that installs BOTH: a per-major manifest carries the two panels
	// in one document, which is the frozen legacy route rather than an oversight.
	//
	// THE DISPATCH IS NOT A CONVENIENCE. The S-UI installer CLEARS the S-UI bounds
	// when no row matches, so running it over a 3X-UI document — whose
	// sui_entries are empty by construction — would erase the S-UI ceiling as a
	// side effect of editing the 3X-UI one.
	switch payload.Product {
	case productXUI:
		if err := installXUICeiling(payload); err != nil {
			return err
		}
	case productSUI:
		applySUICompat(payload)
	case "":
		if err := installXUICeiling(payload); err != nil {
			return err
		}
		applySUICompat(payload)
	default:
		return fmt.Errorf("%w: %q is not a product this build applies", ErrPanelRangesProduct, payload.Product)
	}
	// Recorded AFTER the install succeeds, so a failure part-way leaves the old
	// revision in force and the next fetch is still compared against what is
	// actually applied rather than against what was attempted.
	setAppliedRevision(payload.Product, payload.UpdatedAt)
	return nil
}

// installXUICeiling installs the 3X-UI half of a payload, or refuses the payload.
//
// The refusal is the same one the single-document path made: a payload whose
// entries do not cover this build is a range gap, and the answer is to bump the
// document rather than to install a ceiling that matches nothing.
func installXUICeiling(payload remoteCompatPayload) error {
	entry, ok := lookupForPSPVersion(payload, Version)
	if !ok {
		return fmt.Errorf("no compat entry covers PSP %q in %d entries (range gap — bump the JSON)",
			Version, len(payload.Entries))
	}
	if _, ok := parseSemver(entry.MaxTestedXUI); !ok {
		return fmt.Errorf("compat entry [%s..%s] has unparseable max_tested_xui %q",
			entry.PSPMin, entry.PSPMax, entry.MaxTestedXUI)
	}
	// min_xui is optional in the entry; when present it must parse. It feeds
	// the OPERATIONAL floor — ActiveMinXUI clamps it so it can only raise the
	// floor above the compiled MinXUI backstop, never lower it (see compat.go).
	if entry.MinXUI != "" {
		if _, ok := parseSemver(entry.MinXUI); !ok {
			return fmt.Errorf("compat entry [%s..%s] has unparseable min_xui %q",
				entry.PSPMin, entry.PSPMax, entry.MinXUI)
		}
	}
	SetActiveMaxTestedXUI(entry.MaxTestedXUI)
	SetActiveMinXUI(entry.MinXUI) // "" → ActiveMinXUI falls back to the compiled backstop
	// Advisories are top-level (PSP-version-independent) and runtime-only; install
	// the whole map, canonicalizing keys so "v3.5.0"/"3.5" both resolve on lookup.
	SetActiveAdvisories(canonAdvisories(payload.Advisories))
	return nil
}

// fetchCompatDocument reads a compat document's BYTES, whatever kind it is.
//
// The bytes rather than a payload, because which document this is has to be
// decided before parsing it: a per-major manifest and a panel ranges document
// carry different envelopes, and decoding one by the other's rules would either
// refuse a good document or accept a bad one.
func fetchCompatDocument(ctx context.Context, url string) ([]byte, error) {
	fetchCtx, cancel := context.WithTimeout(ctx, httpFetchTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(fetchCtx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	// Reuses the shared safehttp-guarded client declared in
	// latest_xui.go (same package). Pre-v3.6.1-beta.3 this used
	// http.DefaultClient, which would happily follow an admin-supplied
	// urlOverride into loopback / link-local addresses.
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch %s: %w", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("fetch %s: HTTP %d", url, resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20)) // 1 MiB cap
	if err != nil {
		return nil, fmt.Errorf("read body: %w", err)
	}
	return body, nil
}

func decodeCompatPayload(body []byte) (remoteCompatPayload, error) {
	var payload remoteCompatPayload
	if err := json.Unmarshal(body, &payload); err != nil {
		return remoteCompatPayload{}, fmt.Errorf("decode JSON: %w", err)
	}
	return payload, nil
}

func fetchCompatPayload(ctx context.Context, url string) (remoteCompatPayload, error) {
	body, err := fetchCompatDocument(ctx, url)
	if err != nil {
		return remoteCompatPayload{}, err
	}
	return decodeCompatPayload(body)
}

func resolveRangeOverlayURL(baseURL, ref string) (string, error) {
	base, err := url.Parse(baseURL)
	if err != nil {
		return "", fmt.Errorf("parse compat base URL: %w", err)
	}
	overlay, err := url.Parse(strings.TrimSpace(ref))
	if err != nil {
		return "", fmt.Errorf("parse compat range_overlay: %w", err)
	}
	if overlay.IsAbs() || overlay.Host != "" || overlay.User != nil {
		return "", fmt.Errorf("compat range_overlay must be a relative same-origin URL")
	}
	resolved := base.ResolveReference(overlay)
	if resolved.Scheme != base.Scheme || resolved.Host != base.Host {
		return "", fmt.Errorf("compat range_overlay resolved outside the base origin")
	}
	return resolved.String(), nil
}

// applySUICompat installs the S-UI bounds for this PSP version, or CLEARS them
// when the payload publishes none / publishes an unusable row.
//
// Deliberately cannot fail the refresh: S-UI gating is additive and every JSON
// published before it existed omits sui_entries entirely, so a missing or
// malformed row must degrade to "no S-UI compat data" (CheckSUI → Unknown, UI
// stays silent) rather than black out the 3X-UI range that was just installed.
// Clearing on absence is what keeps a stale ceiling from surviving a rollback of
// the JSON row.
func applySUICompat(payload remoteCompatPayload) {
	SetActiveSUIAdvisories(canonAdvisories(payload.SUIAdvisories))
	entry, ok := lookupSUIForPSPVersion(payload, Version)
	if !ok {
		SetActiveMaxTestedSUI("")
		SetActiveMinSUI("")
		return
	}
	if _, ok := parseSemver(entry.MaxTestedSUI); !ok {
		// A row exists but its ceiling is unusable — treat as unpublished
		// rather than guessing, same "refuse to invent a default" stance.
		SetActiveMaxTestedSUI("")
		SetActiveMinSUI("")
		return
	}
	min := entry.MinSUI
	if min != "" {
		if _, ok := parseSemver(min); !ok {
			min = "" // unparseable floor → publish the ceiling alone
		}
	}
	SetActiveMaxTestedSUI(entry.MaxTestedSUI)
	SetActiveMinSUI(min)
}

// lookupSUIForPSPVersion is lookupForPSPVersion over sui_entries: same
// document-order, first-match-wins, skip-malformed-rows semantics.
func lookupSUIForPSPVersion(payload remoteCompatPayload, pspVersion string) (remoteCompatSUIEntry, bool) {
	if payload.SchemaVersion >= rangeOverlaySchemaVersion {
		pv, ok := canonicalPSPSemver(pspVersion)
		if !ok {
			return remoteCompatSUIEntry{}, false
		}
		for _, e := range payload.SUIEntries {
			lo, lok := canonicalPSPSemver(e.PSPMin)
			hi, hok := canonicalPSPSemver(e.PSPMax)
			if !lok || !hok || semver.Compare(lo, hi) > 0 {
				continue
			}
			if semver.Compare(pv, lo) >= 0 && semver.Compare(pv, hi) <= 0 {
				return e, true
			}
		}
		return remoteCompatSUIEntry{}, false
	}
	pv, ok := parseSemver(pspVersion)
	if !ok {
		return remoteCompatSUIEntry{}, false
	}
	for _, e := range payload.SUIEntries {
		lo, lok := parseSemver(e.PSPMin)
		hi, hok := parseSemver(e.PSPMax)
		if !lok || !hok || cmpSemver(lo, hi) > 0 {
			continue
		}
		if cmpSemver(pv, lo) >= 0 && cmpSemver(pv, hi) <= 0 {
			return e, true
		}
	}
	return remoteCompatSUIEntry{}, false
}

// lookupForPSPVersion iterates entries in document order and returns the
// FIRST one whose [psp_min, psp_max] closed interval contains pspVersion.
// "First match wins" is the documented semantics — admin authoring the
// JSON puts narrower / newer ranges earlier so a more-specific entry
// shadows a broader one.
func lookupForPSPVersion(payload remoteCompatPayload, pspVersion string) (remoteCompatPSPEntry, bool) {
	if payload.SchemaVersion >= rangeOverlaySchemaVersion {
		pv, ok := canonicalPSPSemver(pspVersion)
		if !ok {
			return remoteCompatPSPEntry{}, false
		}
		for _, e := range payload.Entries {
			lo, lok := canonicalPSPSemver(e.PSPMin)
			hi, hok := canonicalPSPSemver(e.PSPMax)
			if !lok || !hok || semver.Compare(lo, hi) > 0 {
				continue
			}
			if semver.Compare(pv, lo) >= 0 && semver.Compare(pv, hi) <= 0 {
				return e, true
			}
		}
		return remoteCompatPSPEntry{}, false
	}
	pv, ok := parseSemver(pspVersion)
	if !ok {
		return remoteCompatPSPEntry{}, false
	}
	for _, e := range payload.Entries {
		lo, lok := parseSemver(e.PSPMin)
		hi, hok := parseSemver(e.PSPMax)
		if !lok || !hok {
			// Malformed entry — skip rather than fail the whole
			// lookup so one bad row doesn't black out the file.
			continue
		}
		if cmpSemver(lo, hi) > 0 {
			// Inverted range (psp_min > psp_max) — admin error, skip.
			continue
		}
		if cmpSemver(pv, lo) >= 0 && cmpSemver(pv, hi) <= 0 {
			return e, true
		}
	}
	return remoteCompatPSPEntry{}, false
}

func canonicalPSPSemver(v string) (string, bool) {
	v = strings.TrimSpace(v)
	if v == "" {
		return "", false
	}
	if v[0] != 'v' {
		v = "v" + v
	}
	v = semver.Canonical(v)
	return v, v != ""
}
