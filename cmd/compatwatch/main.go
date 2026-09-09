// Command compatwatch reports whether either upstream panel has shipped a
// release past the tested ceiling in docs/compat/v3.json.
//
// It exists because that ceiling is the one compat fact that rots without
// anybody touching the repo. Its consequence is not cosmetic: PSP refuses a
// panel upgrade whose target exceeds the ceiling, so a stale file surfaces as
// an ADMIN blocked from upgrading, while nobody who could refresh the file
// learns anything. This closes that loop from the maintainer's side.
//
// It does not decide anything. Raising a ceiling still means the live-panel
// review pass in docs/3xui-compat.md — the whole value of the number is that a
// human verified it, and a job that bumped it automatically would be asserting
// exactly the thing it did not check.
//
// Exit codes: 0 every upstream is at or below its ceiling; 1 at least one is
// ahead; 2 at least one comparison could not be made and none is ahead.
// Non-zero on "could not tell" is deliberate — see version.CeilingUnknown.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
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

const compatPath = "docs/compat/v3.json"

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
			fmt.Fprintf(w, "%s needs a review pass before the ceiling moves — see `docs/3xui-compat.md`. Do not bump `%s` without it.\n",
				r.Upstream, compatPath)
		}
	}
}

func orDash(s string) string {
	if s == "" {
		return "—"
	}
	return s
}

// latestRelease returns the newest non-prerelease tag for owner/repo.
func latestRelease(ctx context.Context, repo string) (string, error) {
	url := "https://api.github.com/repos/" + repo + "/releases/latest"
	var lastErr error
	for attempt := 1; attempt <= fetchAttempts; attempt++ {
		if attempt > 1 {
			select {
			case <-ctx.Done():
				return "", ctx.Err()
			case <-time.After(time.Duration(attempt-1) * 2 * time.Second):
			}
		}
		tag, err := fetchTag(ctx, url)
		if err == nil {
			return tag, nil
		}
		lastErr = err
	}
	return "", lastErr
}

func fetchTag(ctx context.Context, url string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
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
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	var payload struct {
		TagName string `json:"tag_name"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return "", err
	}
	if payload.TagName == "" {
		return "", errors.New("release has no tag_name")
	}
	return payload.TagName, nil
}
