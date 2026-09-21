// Command compatwatch reports the two compatibility gaps that rot without
// anybody touching the repo.
//
// The first is an upstream panel shipping past the tested ceiling in the active
// v4 range overlay. Its consequence is not cosmetic: PSP refuses a panel upgrade
// whose target exceeds the ceiling, so a stale file surfaces as an ADMIN blocked
// from upgrading, while nobody who could refresh the file learns anything.
//
// The second is our OWN Node releases outrunning our review of them — a
// published release that is neither reviewed into the upgrade registry nor
// recorded there as deliberately excluded. Nothing else in the pipeline can
// catch it, because every other check reads that registry rather than
// questioning it; the upgrade dialog simply keeps recommending an older version.
//
// It does not decide anything. Raising a ceiling still means the live-panel
// review pass in docs/3xui-compat.md, and reviewing a Node release still means a
// human deciding whether to offer it — the whole value of both numbers is that
// someone verified them, and a job that edited either automatically would be
// asserting exactly the thing it did not check. What this refuses to allow is
// SILENCE: neither gap may go unmentioned.
//
// Exit codes: 0 every check is current; 1 at least one gap is real; 2 at least
// one comparison could not be made and none is a gap. Non-zero on "could not
// tell" is deliberate — see version.CeilingUnknown.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/KazuhaHub/passwall-sub-panel/internal/version"
)

// Watched upstreams. Both are read through GitHub's /releases/latest, which
// already means "newest non-prerelease" — the same semantic the ceiling is
// verified against, so a 3X-UI beta does not raise an alarm nobody can clear.
var upstreams = []struct {
	Name string
	Repo string
}{
	{Name: "3X-UI", Repo: "MHSanaei/3x-ui"},
	{Name: "S-UI", Repo: "alireza0/s-ui"},
}

// THE RANGES ARE ONE DOCUMENT PER UPSTREAM, and that is the shape the panel reads:
// a 3X-UI review and an S-UI review are separate publications now, so a watcher
// that read one file for both would be checking something the panel no longer has
// — and would report a ceiling for an upstream whose document moved elsewhere.
const (
	xuiCompatPath = "docs/compat/3x-ui-v4.json"
	suiCompatPath = "docs/compat/sui-v4.json"
)

// nodeRegistryPath is the published manifest whose rows decide which Node releases
// the CI MATRIX tests, nodeVerificationPath is where a version deliberately passed
// over is recorded with its reason, and nodeRepo is where the releases it must
// account for are published.
//
// TWO DOCUMENTS BECAUSE THEY ANSWER DIFFERENT QUESTIONS, and because the one that
// used to sit here is gone: this read a signed POLICY's refusals until that policy
// was deleted, and a refusal is not a thing the panel has any more — it offers what
// is published. What is still worth insisting on is that nothing published goes
// UNNOTICED: every release is either exercised by the matrix or passed over with a
// reason somebody wrote down.
const (
	nodeRegistryPath     = "docs/compat/passwall-node-v4.json"
	nodeVerificationPath = "docs/compat/verification-v1.json"
	nodeRepo             = "KazuhaHub/Passwall-Node"
)

// fetchAttempts exists to keep the job from crying wolf. A single GitHub blip
// would otherwise mark the ceiling unreadable, and a watcher that goes red on
// noise trains its reader to ignore it — the failure mode this repo has spent
// the geo detector's whole hysteresis design avoiding. A real breakage (repo
// moved, API shape changed, rate limited) survives all three attempts.
const fetchAttempts = 3

// githubAPI is the one outbound dependency, behind a struct so the rules that
// actually decide a verdict — paging, draft filtering, ordering, backoff — can
// be tested without reaching GitHub or spending the backoff in wall-clock time.
// The zero value is the production configuration.
type githubAPI struct {
	client  *http.Client
	baseURL string
	// sleep is the backoff seam. Production leaves it nil and waits for real;
	// a test replaces it to assert the retry policy rather than pay for it.
	sleep func(context.Context, time.Duration) error
}

func (g githubAPI) url(path string) string {
	base := g.baseURL
	if base == "" {
		base = "https://api.github.com"
	}
	return base + path
}

func (g githubAPI) wait(ctx context.Context, d time.Duration) error {
	if g.sleep != nil {
		return g.sleep(ctx, d)
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(d):
		return nil
	}
}

// latestRelease returns the newest non-prerelease tag for owner/repo.
func (g githubAPI) latestRelease(ctx context.Context, repo string) (string, error) {
	var payload struct {
		TagName string `json:"tag_name"`
	}
	if err := g.getRetry(ctx, "/repos/"+repo+"/releases/latest", &payload); err != nil {
		return "", err
	}
	if payload.TagName == "" {
		return "", errors.New("release has no tag_name")
	}
	return payload.TagName, nil
}

// allReleases lists every published tag for owner/repo, newest published first.
//
// PRERELEASES ARE INCLUDED AND DRAFTS ARE NOT: the releases this guards are
// betas, which GitHub marks prerelease, so filtering them out would leave the
// check with nothing to look at.
//
// SORTED BY PUBLICATION TIME, THE SAME AXIS THE CATALOG ITSELF USES. The tag
// strings sort lexically, which ranks v0.0.1-beta11 BELOW v0.0.1-beta9 — the
// very confusion that let beta10 and beta11 sit unoffered — so any ordering
// derived from the strings would be actively misleading here.
//
// Paging is bounded and a full last page is an error rather than a silent
// truncation: this registry holds at most sixteen entries, so a repository past
// the page limit would have releases this check cannot see, and reporting
// "all accounted for" over an unseen tail is the one answer it must never give.
func (g githubAPI) allReleases(ctx context.Context, repo string) ([]string, error) {
	const perPage = 100
	const maxPages = 10
	type release struct {
		TagName     string    `json:"tag_name"`
		Draft       bool      `json:"draft"`
		PublishedAt time.Time `json:"published_at"`
	}
	var collected []release
	for page := 1; page <= maxPages; page++ {
		var batch []release
		path := fmt.Sprintf("/repos/%s/releases?per_page=%d&page=%d", repo, perPage, page)
		if err := g.getRetry(ctx, path, &batch); err != nil {
			return nil, err
		}
		collected = append(collected, batch...)
		if len(batch) < perPage {
			break
		}
		if page == maxPages {
			return nil, fmt.Errorf("%s has more releases than this check pages through (%d pages)", repo, maxPages)
		}
	}
	sort.SliceStable(collected, func(i, j int) bool {
		return collected[i].PublishedAt.After(collected[j].PublishedAt)
	})
	tags := make([]string, 0, len(collected))
	for _, release := range collected {
		if release.Draft || release.TagName == "" {
			continue
		}
		tags = append(tags, release.TagName)
	}
	return tags, nil
}

// getRetry retries a GitHub read so a single blip cannot be reported as an
// unreadable registry — see fetchAttempts. A caller that has given up is not
// kept alive for the rest of the retry budget, and the context error survives
// rather than being replaced by the last transport failure.
func (g githubAPI) getRetry(ctx context.Context, path string, out any) error {
	var lastErr error
	for attempt := 1; attempt <= fetchAttempts; attempt++ {
		if attempt > 1 {
			// Checked here AND inside the wait, and neither is redundant: this
			// one keeps the retry policy independent of whatever the wait does,
			// while a real wait is cancellable *during* the gap rather than only
			// before it.
			if err := ctx.Err(); err != nil {
				return err
			}
			if err := g.wait(ctx, time.Duration(attempt-1)*2*time.Second); err != nil {
				return err
			}
		}
		lastErr = g.get(ctx, path, out)
		if lastErr == nil {
			return nil
		}
	}
	return lastErr
}

func (g githubAPI) get(ctx context.Context, path string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, g.url(path), nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	// The workflow passes the run's own GITHUB_TOKEN. Anonymous callers get 60
	// requests an hour per IP and Actions runners share addresses, so without it
	// a rate-limited run would report "unknown" for reasons that have nothing to
	// do with either upstream.
	if tok := os.Getenv("GITHUB_TOKEN"); tok != "" {
		req.Header.Set("Authorization", "Bearer "+tok)
	}
	client := g.client
	if client == nil {
		client = &http.Client{Timeout: 15 * time.Second}
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "compatwatch:", err)
		os.Exit(2)
	}
}

func run() error {
	now := time.Now().UTC()
	ceilings := map[string]string{}
	for _, source := range []struct{ upstream, path string }{
		{"3X-UI", xuiCompatPath},
		{"S-UI", suiCompatPath},
	} {
		raw, err := os.ReadFile(source.path)
		if err != nil {
			return fmt.Errorf("read %s (run from the repo root): %w", source.path, err)
		}
		product, ceiling, err := version.CeilingFromDocument(raw, now)
		if err != nil {
			return fmt.Errorf("%s: %w", source.path, err)
		}
		// THE DOCUMENT HAS TO BE ABOUT THE UPSTREAM IT IS READ FOR. The two files are
		// named after their products, and a 3X-UI document served under the S-UI path
		// would otherwise have its ceiling compared against the wrong upstream — a
		// permanent false alarm, or a permanent silence.
		want := map[string]string{"3X-UI": "3x-ui", "S-UI": "sui"}[source.upstream]
		if product != want {
			return fmt.Errorf("%s says it is a %s document, and it is read here for %s", source.path, product, source.upstream)
		}
		if ceiling == "" {
			return fmt.Errorf("%s publishes no %s ceiling", source.path, source.upstream)
		}
		ceilings[source.upstream] = ceiling
	}

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	api := githubAPI{}
	reports := make([]version.CeilingReport, 0, len(upstreams))
	for _, u := range upstreams {
		latest, ferr := api.latestRelease(ctx, u.Repo)
		r := version.CompareCeiling(u.Name, ceilings[u.Name], latest)
		// A fetch error is more informative than the empty tag it produced, so
		// it replaces the generic reason — "rate limited" and "repo not found"
		// need different responses and must not read the same.
		if ferr != nil && r.Verdict == version.CeilingUnknown {
			r.Reason = fmt.Sprintf("could not read %s releases: %v", u.Repo, ferr)
		}
		reports = append(reports, r)
	}

	// The Node matrix is judged last, and from the other side: the two rows above
	// are upstreams PSP consumes, while this one is releases PSP publishes itself.
	// Its absence is the failure nothing else can see, because every other check
	// reads the list instead of questioning it.
	registryRaw, err := os.ReadFile(nodeRegistryPath)
	if err != nil {
		return fmt.Errorf("read %s (run from the repo root): %w", nodeRegistryPath, err)
	}
	var registry nodeRegistry
	if err := json.Unmarshal(registryRaw, &registry); err != nil {
		return fmt.Errorf("%s: %w", nodeRegistryPath, err)
	}
	verificationRaw, err := os.ReadFile(nodeVerificationPath)
	if err != nil {
		return fmt.Errorf("read %s (run from the repo root): %w", nodeVerificationPath, err)
	}
	var verification struct {
		Excluded []nodeRegistryExclusion `json:"excluded"`
	}
	if err := json.Unmarshal(verificationRaw, &verification); err != nil {
		return fmt.Errorf("%s: %w", nodeVerificationPath, err)
	}
	registry.Excluded = append(registry.Excluded, verification.Excluded...)
	tags, releaseErr := api.allReleases(ctx, nodeRepo)
	published, identifyErr := publishedReleases(tags)
	reports = append(reports, nodeRegistryReport(registry, published, errors.Join(releaseErr, identifyErr)))

	// Print every row on every run, whatever the verdicts. One "behind" row
	// must not become the whole message: the reader needs to see that the OTHER
	// upstream was actually checked, or a silently-broken second probe hides
	// behind the first one's alarm.
	writeTable(os.Stdout, reports)
	if summary := os.Getenv("GITHUB_STEP_SUMMARY"); summary != "" {
		if f, err := os.OpenFile(summary, os.O_APPEND|os.O_WRONLY, 0o644); err == nil {
			writeTable(f, reports)
			_ = f.Close()
		}
	}

	if code := exitCode(reports); code != 0 {
		os.Exit(code)
	}
	return nil
}

// nodeRegistryUpstream labels the row in the report table. It was "Passwall-Node
// registry" while the releases came from a curated registry; the row now compares
// the releases this project publishes against the set the matrix tests.
const nodeRegistryUpstream = "Passwall-Node matrix"

type nodeRegistryRelease struct {
	Version string `json:"version"`
}

type nodeRegistryExclusion struct {
	Version string `json:"version"`
	Reason  string `json:"reason"`
}

// nodeRegistry is the matrix's versions plus the ones deliberately passed over.
//
// released_nodes IS THE MANIFEST'S KEY. It is the same row list the CI planner
// slices by position at min_supported; this watcher needs only the versions, so the
// other per-row fields are ignored.
type nodeRegistry struct {
	Releases []nodeRegistryRelease   `json:"released_nodes"`
	Excluded []nodeRegistryExclusion `json:"excluded"`
}

// accounted lists every release the documents have ruled on.
//
// A REFUSAL WITHOUT A REASON IS NOT A DECISION, so it does not count. The point of
// recording one is that the next reader learns why a release is not offered; an
// entry that only names the version moves the silence one line down instead of
// resolving it, and would let a release be skipped as quietly as forgetting about
// it.
func (r nodeRegistry) accounted() []string {
	versions := make([]string, 0, len(r.Releases)+len(r.Excluded))
	for _, release := range r.Releases {
		versions = append(versions, release.Version)
	}
	for _, excluded := range r.Excluded {
		if excluded.Reason != "" {
			versions = append(versions, excluded.Version)
		}
	}
	return versions
}

// unaccountedReleases reports published releases the registry has neither
// reviewed nor explicitly excluded.
//
// IT ENFORCES ATTENTION WITHOUT TAKING THE DECISION. Whether a release is
// offered stays a human judgement — that is what the registry is for, and a job
// that added entries would be asserting exactly the thing nobody checked. What
// this refuses to allow is SILENCE: a release that shipped and that nobody
// ruled on, which is how beta10 and beta11 sat unoffered while the panel kept
// recommending beta9.
//
// MIDDLE GAPS COUNT. Asking only whether the newest release is accounted for
// would miss a version skipped over on the way, so every published tag is
// checked. Published order is preserved, so the report reads the way releases
// happened.
func unaccountedReleases(published, accounted []string) []string {
	known := make(map[string]struct{}, len(accounted))
	for _, version := range accounted {
		known[version] = struct{}{}
	}
	var missing []string
	for _, version := range published {
		if _, ok := known[version]; !ok {
			missing = append(missing, version)
		}
	}
	return missing
}

// nodeRegistryReport judges the Node registry the way the ceilings above are
// judged, from the opposite side. Those rows ask whether an UPSTREAM has
// outrun the version we tested; this one asks whether OUR OWN releases have
// outrun our review of them. Same three states, same consequence — something
// shipped that nobody is able to act on.
//
// The failure it exists for is silent by construction: every other check in the
// pipeline READS the registry, so none of them can notice a release missing
// from it. beta10 and beta11 shipped weeks apart and the upgrade dialog went on
// recommending beta9, while every check stayed green.
//
// published is the repository's releases, newest first. It is the fact; the
// registry is the ruling on that fact, and this compares the two.
// publishedRelease is one published release in both of its identities.
//
// THE TAG LOCATES IT AND THE VERSION ACCOUNTS FOR IT. GitHub reports a release's
// tag_name, and they are never the same string: a release's tag is `v4.0.0` and
// its version is `4.0.0`. The registry is keyed by version — it is the list of releases the panel may offer —
// so reconciling on the tag would report a fully reviewed release as a gap whose
// only remedy is an entry that is already there. The tag is kept because it is
// what names a release to a reader and what addresses it on GitHub.
type publishedRelease struct {
	Tag     string
	Version string
	// Historical marks a release published under the scheme this project used to
	// have. It is not a version here — nothing reads one out of it — and it is
	// outside what the registry can account for.
	Historical bool
}

// legacyPublishedShape RECOGNISES the historical published form. It does not read
// it as an identity, and no other part of this build accepts one.
//
// THE SCHEME IS GONE AND THE RELEASES ARE NOT. Twelve v0.0.1-beta releases are on
// GitHub and always will be. A watcher that refuses to look at them reports
// "unknown" on every run, and a watcher that cannot run is worse than one that
// classifies: the silence this job refuses would be its own.
//
// IT IS AS NARROW AS THE RELEASES THAT EXIST, and the narrowness is load-bearing
// now that `v` is the namespace this project publishes under. A pattern matching
// any v-prefixed number would file a MALFORMED tag of the current scheme as
// history — `v05.0.0`, a zero release line, a typo — and a release nobody can
// account for is exactly what this check exists to refuse. These twelve match;
// nothing else does.
var legacyPublishedShape = regexp.MustCompile(`^v0\.0\.1-beta\.?[0-9]+$`)

// publishedReleases pairs each published tag with the version it names, and
// separates the releases published before the current scheme from the ones the
// registry can account for.
//
// A TAG IT CANNOT IDENTIFY AT ALL IS STILL AN ERROR RATHER THAN AN ENTRY TO SKIP.
// That is what keeps the check honest about a repository that has grown a release
// nobody can account for — a typo, a second naming scheme, a stray tag — while a
// release under the KNOWN historical form is history rather than a gap.
//
// The mapping for the current scheme is the shared one in internal/version, not a
// second copy here. The same mapping decides whether a node's reported identity is
// newer than this build's version, and two copies would disagree about which
// release a tag names.
func publishedReleases(tags []string) ([]publishedRelease, error) {
	published := make([]publishedRelease, 0, len(tags))
	for _, tag := range tags {
		if named, ok := version.VersionOfReleaseTag(tag); ok {
			published = append(published, publishedRelease{Tag: tag, Version: named})
			continue
		}
		if legacyPublishedShape.MatchString(tag) {
			published = append(published, publishedRelease{Tag: tag, Historical: true})
			continue
		}
		return nil, fmt.Errorf("cannot identify the release published as %q: it is neither a %q tag nor a historical release",
			tag, version.ProductTagNamespace)
	}
	return published, nil
}

func nodeRegistryReport(registry nodeRegistry, published []publishedRelease, fetchErr error) version.CeilingReport {
	report := version.CeilingReport{Upstream: nodeRegistryUpstream}
	if fetchErr != nil {
		// Fail closed rather than assert an empty registry: "could not read"
		// must never render as "nothing to review".
		report.Verdict = version.CeilingUnknown
		report.Reason = fmt.Sprintf("could not read %s releases: %v", nodeRepo, fetchErr)
		return report
	}
	if len(published) == 0 {
		// A repository that publishes releases cannot legitimately report none.
		// This is the shape a silently-wrong endpoint produces, so it is treated
		// as unreadable rather than as an empty registry.
		report.Verdict = version.CeilingUnknown
		report.Reason = fmt.Sprintf("%s reported no published releases at all", nodeRepo)
		return report
	}
	// THE ACCOUNTING IS OVER THE RELEASES THIS PROJECT STILL PUBLISHES. A release
	// under the historical scheme cannot be reviewed into a registry keyed by
	// versions, and reporting it as a gap would make the gap permanent: the only
	// remedy the check names is a registry entry that could never be written. It is
	// COUNTED and it is SAID, so a release nobody has classified is still visible.
	current := make([]publishedRelease, 0, len(published))
	for _, release := range published {
		if !release.Historical {
			current = append(current, release)
		}
	}
	historical := len(published) - len(current)
	note := ""
	if historical > 0 {
		note = fmt.Sprintf(" (%d published before the current scheme, and so not a version the matrix can name)", historical)
	}
	if len(current) == 0 {
		report.Verdict = version.CeilingUnknown
		report.Reason = fmt.Sprintf("%s publishes no release under the current scheme%s", nodeRepo, note)
		return report
	}
	report.Latest = current[0].Tag

	accounted := registry.accounted()
	// THE PUBLISHED SIDE IS COMPARED BY VERSION. The registry names versions; the
	// published side arrives as tags. Comparing the two directly is how a reviewed
	// product release reads as unreviewed.
	named := make([]string, 0, len(current))
	for _, release := range current {
		named = append(named, release.Version)
	}
	if missing := unaccountedReleases(named, accounted); len(missing) > 0 {
		// The ceiling is the newest release that WAS ruled on, so the row reads
		// as the gap it is: releases exist past the point anyone has reviewed.
		//
		// IT IS REPORTED AS A TAG, like Latest beside it, so the match is made on
		// the version and the tag is what comes out.
		known := make(map[string]struct{}, len(accounted))
		for _, ruled := range accounted {
			known[ruled] = struct{}{}
		}
		for _, release := range current {
			if _, ok := known[release.Version]; ok {
				report.Ceiling = release.Tag
				break
			}
		}
		report.Verdict = version.CeilingBehind
		report.Reason = fmt.Sprintf("published but unaccounted: %s%s", strings.Join(missing, ", "), note)
		return report
	}
	report.Ceiling = report.Latest
	report.Verdict = version.CeilingCurrent
	report.Reason = fmt.Sprintf("every one of the %d published releases is either tested by the matrix or passed over with a reason%s",
		len(current), note)
	return report
}

// exitCode collapses the rows into one process result: 1 if any upstream is
// ahead, else 2 if any comparison could not be made, else 0.
//
// "Behind" outranks "unknown" because it is the actionable one — but the
// unknown row is still PRINTED, so a second upstream whose probe is quietly
// broken cannot hide behind the first one's alarm. The precedence decides the
// exit code only, never what the reader is shown.
func exitCode(reports []version.CeilingReport) int {
	code := 0
	for _, r := range reports {
		switch r.Verdict {
		case version.CeilingBehind:
			return 1
		case version.CeilingUnknown:
			code = 2
		}
	}
	return code
}

func writeTable(w io.Writer, reports []version.CeilingReport) {
	fmt.Fprintln(w, "## Upstream compatibility ceilings")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "| Upstream | Tested ceiling | Upstream latest | Verdict |")
	fmt.Fprintln(w, "|---|---|---|---|")
	for _, r := range reports {
		fmt.Fprintf(w, "| %s | %s | %s | **%s** — %s |\n",
			r.Upstream, orDash(r.Ceiling), orDash(r.Latest), r.Verdict, r.Reason)
	}
	fmt.Fprintln(w)
	for _, r := range reports {
		if r.Verdict == version.CeilingBehind {
			fmt.Fprintf(w, "%s %s\n", r.Upstream, remedyFor(r.Upstream))
		}
	}
}

// Remedies are per-row because the two directions need different readers. A
// panel ceiling is raised by reviewing the panel and editing the range file; the
// Node registry is fixed by deciding about our own release, in a different file
// whose entries carry prose. One hardcoded message was correct while every row
// was an upstream — reusing it would send a maintainer to edit the wrong file.
const panelCeilingRemedy = "needs a review pass before the ceiling moves — see `docs/3xui-compat.md`. " +
	"Do not bump `" + xuiCompatPath + "` or `" + suiCompatPath + "` without it."

const nodeRegistryRemedy = "has published releases the matrix does not exercise. Add each to `" + nodeRegistryPath + "` " +
	"and pin its commit, or record it under `excluded` in `" + nodeVerificationPath + "` with the reason it is passed over. " +
	"Neither is an admission decision: the panel offers what is published, so this is about what gets TESTED."

func remedyFor(upstream string) string {
	if upstream == nodeRegistryUpstream {
		return nodeRegistryRemedy
	}
	return panelCeilingRemedy
}

func orDash(s string) string {
	if s == "" {
		return "—"
	}
	return s
}
