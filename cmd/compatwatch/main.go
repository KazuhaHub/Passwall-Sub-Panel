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

const compatPath = "docs/compat/v4-ranges.json"

// nodeRegistryPath is the reviewed allowlist that decides which Node releases
// the panel offers for remote upgrade, and nodeRepo is where the releases it
// must account for are published.
const (
	nodeRegistryPath = "internal/adapters/noderelease/reviewed.json"
	nodeRepo         = "KazuhaHub/Passwall-Node"
)

// fetchAttempts exists to keep the job from crying wolf. A single GitHub blip
// would otherwise mark the ceiling unreadable, and a watcher that goes red on
// noise trains its reader to ignore it — the failure mode this repo has spent
// the geo detector's whole hysteresis design avoiding. A real breakage (repo
// moved, API shape changed, rate limited) survives all three attempts.
const fetchAttempts = 3

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "compatwatch:", err)
		os.Exit(2)
	}
}

func run() error {
	raw, err := os.ReadFile(compatPath)
	if err != nil {
		return fmt.Errorf("read %s (run from the repo root): %w", compatPath, err)
	}
	xuiCeiling, suiCeiling, err := version.CeilingsFromCompatJSON(raw)
	if err != nil {
		return fmt.Errorf("%s: %w", compatPath, err)
	}
	ceilings := map[string]string{"3X-UI": xuiCeiling, "S-UI": suiCeiling}

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	reports := make([]version.CeilingReport, 0, len(upstreams))
	for _, u := range upstreams {
		latest, ferr := latestRelease(ctx, u.Repo)
		r := version.CompareCeiling(u.Name, ceilings[u.Name], latest)
		// A fetch error is more informative than the empty tag it produced, so
		// it replaces the generic reason — "rate limited" and "repo not found"
		// need different responses and must not read the same.
		if ferr != nil && r.Verdict == version.CeilingUnknown {
			r.Reason = fmt.Sprintf("could not read %s releases: %v", u.Repo, ferr)
		}
		reports = append(reports, r)
	}

	// The Node registry is judged last, and from the other side: the two rows
	// above are upstreams PSP consumes, while this one is releases PSP publishes
	// itself. Its absence is the failure nothing else can see, because every
	// other check reads the registry instead of questioning it.
	registryRaw, err := os.ReadFile(nodeRegistryPath)
	if err != nil {
		return fmt.Errorf("read %s (run from the repo root): %w", nodeRegistryPath, err)
	}
	var registry nodeRegistry
	if err := json.Unmarshal(registryRaw, &registry); err != nil {
		return fmt.Errorf("%s: %w", nodeRegistryPath, err)
	}
	published, releaseErr := allReleases(ctx, nodeRepo)
	reports = append(reports, nodeRegistryReport(registry, published, releaseErr))

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

// nodeRegistryUpstream labels the row in the report table.
const nodeRegistryUpstream = "Passwall-Node registry"

type nodeRegistryRelease struct {
	Version string `json:"version"`
}

type nodeRegistryExclusion struct {
	Version string `json:"version"`
	Reason  string `json:"reason"`
}

type nodeRegistry struct {
	Releases []nodeRegistryRelease   `json:"releases"`
	Excluded []nodeRegistryExclusion `json:"excluded"`
}

// accounted lists every release the registry has ruled on.
//
// AN EXCLUSION WITHOUT A REASON IS NOT A DECISION, so it does not count. The
// point of recording one is that the next reader learns why a release is not
// offered; an entry that only names the version moves the silence one line
// down instead of resolving it, and would let a release be skipped as quietly as
// forgetting about it.
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
func nodeRegistryReport(registry nodeRegistry, published []string, fetchErr error) version.CeilingReport {
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
	report.Latest = published[0]

	accounted := registry.accounted()
	if missing := unaccountedReleases(published, accounted); len(missing) > 0 {
		// The ceiling is the newest release that WAS ruled on, so the row reads
		// as the gap it is: releases exist past the point anyone has reviewed.
		known := make(map[string]struct{}, len(accounted))
		for _, version := range accounted {
			known[version] = struct{}{}
		}
		for _, version := range published {
			if _, ok := known[version]; ok {
				report.Ceiling = version
				break
			}
		}
		report.Verdict = version.CeilingBehind
		report.Reason = fmt.Sprintf("published but unaccounted: %s", strings.Join(missing, ", "))
		return report
	}
	report.Ceiling = report.Latest
	report.Verdict = version.CeilingCurrent
	report.Reason = fmt.Sprintf("every one of the %d published releases is reviewed or excluded with a reason", len(published))
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
	"Do not bump `" + compatPath + "` without it."

const nodeRegistryRemedy = "has published releases nobody has ruled on. Review each into `" + nodeRegistryPath + "`, " +
	"or record it under `excluded` with the reason it is not offered."

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

// latestRelease returns the newest non-prerelease tag for owner/repo.
func latestRelease(ctx context.Context, repo string) (string, error) {
	var payload struct {
		TagName string `json:"tag_name"`
	}
	if err := getJSONRetry(ctx, "https://api.github.com/repos/"+repo+"/releases/latest", &payload); err != nil {
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
func allReleases(ctx context.Context, repo string) ([]string, error) {
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
		url := fmt.Sprintf("https://api.github.com/repos/%s/releases?per_page=%d&page=%d", repo, perPage, page)
		if err := getJSONRetry(ctx, url, &batch); err != nil {
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

// getJSONRetry retries a GitHub read so a single blip cannot be reported as an
// unreadable registry — see fetchAttempts.
func getJSONRetry(ctx context.Context, url string, out any) error {
	var lastErr error
	for attempt := 1; attempt <= fetchAttempts; attempt++ {
		if attempt > 1 {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(time.Duration(attempt-1) * 2 * time.Second):
			}
		}
		lastErr = getJSON(ctx, url, out)
		if lastErr == nil {
			return nil
		}
	}
	return lastErr
}

func getJSON(ctx context.Context, url string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
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
	resp, err := (&http.Client{Timeout: 15 * time.Second}).Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	return json.NewDecoder(resp.Body).Decode(out)
}
