package version

import (
	"context"
	"encoding/json"
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

// defaultRemoteCompatURLBase is the GitHub raw path under which per-major
// compat JSON files live. Each PSP build pulls the file matching its own
// major (v3.x → v3.json, v4.x → v4.json) — the major number is appended
// at fetch time. Per-major split (v3.6.0-beta.7) replaces the v3.6.0-beta.5
// single-file model: each file is naturally bounded by "how many minors
// a single major ships" (~10), maintainers only ever edit the active-
// major file, and bumping to a new major (v4) is just "create v4.json,
// leave v3.json frozen".
const defaultRemoteCompatURLBase = "https://raw.githubusercontent.com/KazuhaHub/passwall-sub-panel/main/docs/compat/"

// panelRangesDocumentName is the document a build reaches when no major names a
// file for it. It carries its own applicability window, so the name never has to
// be inferred from the version — which is the whole reason it exists.
const panelRangesDocumentName = "panel-ranges-v1.json"

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
// So a build whose version is not the legacy form gets no per-major URL at all.
//
// WHAT IT GETS INSTEAD IS NOT BUILT YET, and saying so is the point of this line.
// The release policy that governs Node releases carries `applies_to_psp` — an
// explicit applicable range rather than a number to index a file by — but it
// carries RELEASES, not XUI/SUI COMPATIBILITY RANGES. So as things stand a
// product-versioned build reads neither: its ceiling is empty and a probed panel
// is reported as untested. That is fail-closed, and it is a GAP rather than a
// design; the earlier wording here said the build "reads its policy instead",
// which described a document that does not exist.
//
// Closing it is V03's remaining work: carry the compat ranges in a policy-shaped
// document with its own applicable range, and keep the per-major files for builds
// that still read them. What must NOT happen first is an unprefixed version
// deriving a path from this pattern.
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
// UpgradeEdge is one Node upgrade edge as the documents record it: a claim that
// upgrading a Node from From to To has been checked, rather than that To happens
// to be a release this panel knows about.
//
// IT IS RECORDED AND NOT DECIDED ON. Admission used to require a reviewed edge for
// the specific pair, which made a compatible peer un-upgradeable and put the
// remedy in a policy document the operator had no reason to know about. PSP is the
// source of truth for what is supported, so the field is carried — the documents
// still make the claim, and a reviewer can still read it — while nothing refuses a
// request for the absence of one. The same shape is validated by
// deploy/compat/plan.mjs in docs/compat/verification-v1.json.
type UpgradeEdge struct {
	ID   string `json:"id"`
	From string `json:"from"`
	To   string `json:"to"`
}

type remoteCompatPayload struct {
	SchemaVersion int                    `json:"schema_version"`
	Major         int                    `json:"major"`
	UpdatedAt     string                 `json:"updated_at"`
	Entries       []remoteCompatPSPEntry `json:"entries"`
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
	// UpgradeEdges lists the Node upgrade edges that have been VERIFIED. It is
	// the same model the planner validates in docs/compat/verification-v1.json,
	// republished here because the runtime can only read this document.
	//
	// OPTIONAL, and its absence is meaningful rather than tolerated: a manifest
	// without it publishes no verified edge, so no upgrade is recommended. That
	// is the state every manifest is in today, and it is the truthful one — an
	// edge is a claim that somebody checked a specific path, not a property of
	// the target release.
	UpgradeEdges []UpgradeEdge `json:"upgrade_edges,omitempty"`
	// AppliesToPSP is set ONLY when this payload came from a panel ranges
	// document, and it is what makes such a document installable at all.
	//
	// A per-major manifest says which builds it is for by its NAME and its
	// `major` field: a build derives its major and reads the file with that name.
	// A panel ranges document is reached by a FIXED name and says it HERE,
	// because under the product scheme the first segment of a version is a
	// RELEASE LINE, not a compatibility major — there is no number that could
	// name the file. Set means "match by this window"; absent means "match by the
	// derived major", which is what every manifest does.
	AppliesToPSP *PolicyPSPRange `json:"applies_to_psp,omitempty"`
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
	url := urlOverride
	if url == "" {
		var err error
		url, err = defaultURLForCurrentVersion()
		if err != nil {
			return err
		}
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

	err := fetchAndApply(ctx, url)

	refreshMu.Lock()
	refreshInflight = false
	refreshLastError = err
	if err == nil {
		refreshLastAt = time.Now()
	}
	refreshMu.Unlock()
	return err
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

// defaultURLForCurrentVersion returns the GitHub raw URL of the per-major
// JSON file matching THIS PSP build's major. "dev" / unparseable PSP
// version → error (RefreshRemoteCompat surfaces it; dev builds get
// CompatUnknown until admin uses force override, which is the documented
// trade-off).
func defaultURLForCurrentVersion() (string, error) {
	if major, ok := pspMajor(Version); ok {
		return defaultRemoteCompatURLBase + "v" + strconv.Itoa(major) + ".json", nil
	}
	// A product-scheme build reaches its ranges BY NAME. Its first segment is a
	// release line rather than a compatibility major, so there is no per-major
	// file to derive and none to fetch: the document named here states the builds
	// it applies to instead. That is what closes the gap the previous version of
	// this function named — a build that could reach no ranges at all.
	if IsReleaseVersion(Version) {
		return defaultRemoteCompatURLBase + panelRangesDocumentName, nil
	}
	return "", fmt.Errorf("version %q is neither a legacy v-prefixed build nor a release version, so no compat document applies to it; "+
		"its supported ceiling stays unknown until one is", Version)
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
// DATE-ONLY IS THE FORM EVERY PUBLISHED FILE ACTUALLY CARRIES — v3.json,
// v4.json, v4-ranges.json and node-v4.json all say "2026-09-16". An RFC3339
// parser alone would therefore refuse the real manifests and take the whole
// compat load down with them, which is how this was found: a test that drives
// the real document rather than a fixture written to match the parser.
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

// appliedRevision is the updated_at of the manifest currently in force. It is
// kept independently of the refresh mutex because fetchAndApply also runs while
// that mutex is held.
var (
	appliedRevisionMu sync.Mutex
	appliedRevision   string
)

func setAppliedRevision(revision string) {
	appliedRevisionMu.Lock()
	appliedRevision = revision
	appliedRevisionMu.Unlock()
}

func currentAppliedRevision() string {
	appliedRevisionMu.Lock()
	defer appliedRevisionMu.Unlock()
	return appliedRevision
}

// fetchAndApply reads one compat document and installs it if it applies here.
//
// WHICH DOCUMENT IT IS COMES FROM ITS OWN SCHEMA, not from which build is asking.
// A URL override can point either somewhere, and dispatching on the build would
// parse a document by the wrong rules and then accept it.
func fetchAndApply(ctx context.Context, url string) error {
	raw, err := fetchCompatDocument(ctx, url)
	if err != nil {
		return err
	}
	var kind struct {
		SchemaVersion int `json:"schema_version"`
	}
	if err := json.Unmarshal(raw, &kind); err != nil {
		return fmt.Errorf("decode JSON: %w", err)
	}
	if kind.SchemaVersion == panelRangesSchema {
		return applyPanelRangesDocument(raw, time.Now().UTC())
	}
	return applyPerMajorManifest(ctx, raw, url)
}

// applyPanelRangesDocument validates and installs a named panel ranges document.
//
// IT IS CONVERTED, NOT INSTALLED RAW. Its payload is the same shape the manifest
// carries, so once the applicability check has passed it becomes an ordinary
// payload and every step downstream — first-match-wins entries, the S-UI gate,
// advisories, the revision guard, the snapshot a boot replays — is unchanged.
// The window travels with it so those replays can make the same decision the
// fetch just made.
func applyPanelRangesDocument(raw []byte, now time.Time) error {
	policy, err := ParsePanelRangesPolicy(raw, now)
	if err != nil {
		return err
	}
	window := policy.AppliesToPSP
	payload := remoteCompatPayload{
		SchemaVersion: schemaVersion,
		UpdatedAt:     policy.IssuedAt.UTC().Format(time.RFC3339),
		Entries:       policy.Entries,
		SUIEntries:    policy.SUIEntries,
		Advisories:    policy.Advisories,
		SUIAdvisories: policy.SUIAdvisories,
		AppliesToPSP:  &window,
	}
	applied := currentAppliedRevision()
	regressed, err := revisionRegressed(applied, payload.UpdatedAt)
	if err != nil {
		return err
	}
	if regressed {
		return fmt.Errorf("panel ranges document is dated %s, older than the applied revision %s; refusing to replace a newer tested range with an older one", payload.UpdatedAt, applied)
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
		return fmt.Errorf("cannot derive a PSP major from version %q, so no per-major manifest applies to it; "+
			"a build with no derivable major reads the %s document instead", Version, panelRangesDocumentName)
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
	applied := currentAppliedRevision()
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
// manifest says it by its NAME and its `major` field. A panel ranges document
// carries its own window, because a product version's first segment is a release
// line rather than a compatibility major and no number could name its file.
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
		return fmt.Errorf("cannot derive a PSP major from version %q, so no per-major manifest applies to it; "+
			"a build with no derivable major reads the %s document instead", Version, panelRangesDocumentName)
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
	applySUICompat(payload)
	// Recorded AFTER the install succeeds, so a failure part-way leaves the old
	// revision in force and the next fetch is still compared against what is
	// actually applied rather than against what was attempted.
	setAppliedRevision(payload.UpdatedAt)
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
