package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/KazuhaHub/passwall-sub-panel/internal/version"
)

func report(v version.CeilingVerdict) version.CeilingReport {
	return version.CeilingReport{Upstream: "x", Verdict: v, Reason: "r"}
}

func TestExitCode(t *testing.T) {
	cases := []struct {
		name string
		in   []version.CeilingVerdict
		want int
	}{
		{"all current", []version.CeilingVerdict{version.CeilingCurrent, version.CeilingCurrent}, 0},
		{"one behind", []version.CeilingVerdict{version.CeilingCurrent, version.CeilingBehind}, 1},
		{"one unknown", []version.CeilingVerdict{version.CeilingCurrent, version.CeilingUnknown}, 2},
		{"behind outranks unknown", []version.CeilingVerdict{version.CeilingUnknown, version.CeilingBehind}, 1},
		{"unknown first, still 2", []version.CeilingVerdict{version.CeilingUnknown, version.CeilingCurrent}, 2},
		{"no rows at all", nil, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			reports := make([]version.CeilingReport, 0, len(tc.in))
			for _, v := range tc.in {
				reports = append(reports, report(v))
			}
			if got := exitCode(reports); got != tc.want {
				t.Fatalf("exitCode = %d, want %d", got, tc.want)
			}
		})
	}
}

// The single most important property of this job: an all-clear exit is
// reachable ONLY when every row was actually compared. If "unknown" ever
// mapped to 0 the watcher would go green while blind, which is the exact
// failure it was built to prevent.
func TestUnknownIsNeverAnAllClear(t *testing.T) {
	for _, mixed := range [][]version.CeilingVerdict{
		{version.CeilingUnknown},
		{version.CeilingUnknown, version.CeilingCurrent},
		{version.CeilingCurrent, version.CeilingUnknown},
	} {
		reports := make([]version.CeilingReport, 0, len(mixed))
		for _, v := range mixed {
			reports = append(reports, report(v))
		}
		if exitCode(reports) == 0 {
			t.Fatalf("%v exited 0 — a run that could not compare an upstream must not read as all-clear", mixed)
		}
	}
}

// Every row is rendered whatever the verdicts, so one alarm cannot stand in for
// the whole picture: the reader must be able to see that the other upstream was
// checked, and what it said.
func TestRenderKeepsTheDenominatorVisible(t *testing.T) {
	var sb strings.Builder
	writeTable(&sb, []version.CeilingReport{
		{Upstream: "3X-UI", Ceiling: "3.7.0", Latest: "3.8.0", Verdict: version.CeilingBehind, Reason: "ahead"},
		{Upstream: "S-UI", Ceiling: "1.5.5", Latest: "", Verdict: version.CeilingUnknown, Reason: "unreadable"},
	})
	out := sb.String()
	for _, want := range []string{"3X-UI", "S-UI", "3.7.0", "3.8.0", "1.5.5", "behind", "unknown", "ahead", "unreadable"} {
		if !strings.Contains(out, want) {
			t.Errorf("rendered table is missing %q:\n%s", want, out)
		}
	}
	// An empty field must render as something, not vanish into an ambiguous
	// blank cell that reads as "same as above".
	if !strings.Contains(out, "—") {
		t.Errorf("a missing value must render visibly:\n%s", out)
	}
}

// unaccountedReleases is the rule behind "every published release is either
// reviewed or explicitly excluded". It is what makes forgetting impossible
// WITHOUT making the decision automatic: the registry still says which releases
// are offered, and this only refuses to let one go unmentioned.
func TestUnaccountedReleases(t *testing.T) {
	published := []string{"4.0.0", "4.0.1", "4.0.2", "4.1.0", "4.1.1"}

	// Everything decided: nothing to report.
	if got := unaccountedReleases(published, []string{"4.0.0", "4.0.1", "4.0.2", "4.1.0", "4.1.1"}); len(got) != 0 {
		t.Fatalf("fully accounted registry reported %v", got)
	}

	// A release nobody has ruled on — the live case this exists for.
	got := unaccountedReleases(published, []string{"4.0.0", "4.0.1", "4.0.2"})
	if len(got) != 2 || got[0] != "4.1.0" || got[1] != "4.1.1" {
		t.Fatalf("unaccounted = %v, want the two newest", got)
	}

	// A MIDDLE release that was skipped rather than the newest — the case a
	// "is the newest reviewed?" check would miss entirely.
	got = unaccountedReleases(published, []string{"4.0.0", "4.0.1", "4.1.0", "4.1.1"})
	if len(got) != 1 || got[0] != "4.0.2" {
		t.Fatalf("unaccounted = %v, want the skipped middle release", got)
	}

	// Order is the published order, so the report reads the way releases
	// happened rather than the way a map happened to iterate.
	got = unaccountedReleases([]string{"4.1.1", "4.1.0"}, nil)
	if len(got) != 2 || got[0] != "4.1.1" || got[1] != "4.1.0" {
		t.Fatalf("unaccounted = %v, want the published order preserved", got)
	}
}

// The registry row asks the opposite question from the panel rows above it, so
// the thing worth pinning is that both directions still reach the SAME two
// states: a real gap exits 1, and anything unreadable exits 2 rather than
// passing for an all-clear.
func TestNodeRegistryReport(t *testing.T) {
	published, err := publishedReleases([]string{"release/4.1.1", "release/4.1.0", "release/4.0.2", "release/4.0.0"})
	if err != nil {
		t.Fatal(err)
	}

	t.Run("an unruled-on release is a gap, naming the release", func(t *testing.T) {
		got := nodeRegistryReport(nodeRegistry{
			Releases: []nodeRegistryRelease{{Version: "4.0.0"}, {Version: "4.0.2"}},
		}, published, nil)
		if got.Verdict != version.CeilingBehind {
			t.Fatalf("verdict = %s, want behind", got.Verdict)
		}
		// The ceiling is the newest release that WAS ruled on, so the row reads
		// as the gap: releases exist past the point anyone reviewed.
		// REPORTED AS TAGS, which is what a release is addressed by: the match
		// against the registry is made on the version and the tag is what comes
		// out, so a reader can go and look at the release. The two were one
		// string until the legacy scheme was removed, which is why this used to
		// read as a pair of versions.
		if got.Ceiling != "release/4.0.2" || got.Latest != "release/4.1.1" {
			t.Fatalf("ceiling/latest = %s/%s, want the newest reviewed and the newest published", got.Ceiling, got.Latest)
		}
		for _, want := range []string{"4.1.0", "4.1.1"} {
			if !strings.Contains(got.Reason, want) {
				t.Errorf("reason does not name %s, so the reader cannot act: %q", want, got.Reason)
			}
		}
		if exitCode([]version.CeilingReport{got}) != 1 {
			t.Error("a real gap must exit 1")
		}
	})

	t.Run("a full accounting is current", func(t *testing.T) {
		got := nodeRegistryReport(nodeRegistry{
			Releases: []nodeRegistryRelease{
				{Version: "4.0.0"}, {Version: "4.0.2"},
				{Version: "4.1.0"}, {Version: "4.1.1"},
			},
		}, published, nil)
		if got.Verdict != version.CeilingCurrent {
			t.Fatalf("verdict = %s, want current", got.Verdict)
		}
		if exitCode([]version.CeilingReport{got}) != 0 {
			t.Error("a fully accounted registry must exit 0")
		}
	})

	t.Run("an exclusion counts only when it says why", func(t *testing.T) {
		// Decided, and the next reader can see why.
		excluded := nodeRegistryReport(nodeRegistry{
			Releases: []nodeRegistryRelease{{Version: "4.0.0"}},
			Excluded: []nodeRegistryExclusion{
				{Version: "4.0.2", Reason: "broken installer"},
				{Version: "4.1.0", Reason: "superseded by beta11"},
				{Version: "4.1.1", Reason: "withdrawn before rollout"},
			},
		}, published, nil)
		if excluded.Verdict != version.CeilingCurrent {
			t.Fatalf("verdict = %s, want current — an exclusion with a reason is a decision", excluded.Verdict)
		}
		// A bare version moves the silence one line down instead of resolving
		// it, so it must not count as having ruled on the release.
		silent := nodeRegistryReport(nodeRegistry{
			Releases: []nodeRegistryRelease{{Version: "4.0.0"}},
			Excluded: []nodeRegistryExclusion{
				{Version: "4.0.2", Reason: "broken installer"},
				{Version: "4.1.0", Reason: "superseded by beta11"},
				{Version: "4.1.1"},
			},
		}, published, nil)
		if silent.Verdict != version.CeilingBehind || !strings.Contains(silent.Reason, "4.1.1") {
			t.Fatalf("an exclusion with no reason must stay unaccounted: %+v", silent)
		}
	})

	t.Run("unreadable is never an all-clear", func(t *testing.T) {
		failed := nodeRegistryReport(nodeRegistry{}, published, errors.New("HTTP 403"))
		if failed.Verdict != version.CeilingUnknown {
			t.Fatalf("verdict = %s, want unknown", failed.Verdict)
		}
		if exitCode([]version.CeilingReport{failed}) == 0 {
			t.Fatal("a registry that could not be read must not read as all-clear")
		}
		// An empty listing is the shape a silently-wrong endpoint produces, not
		// evidence that there is nothing to review.
		empty := nodeRegistryReport(nodeRegistry{}, nil, nil)
		if empty.Verdict != version.CeilingUnknown || exitCode([]version.CeilingReport{empty}) == 0 {
			t.Fatalf("an empty release listing must not read as all-clear: %+v", empty)
		}
	})
}

// A PRODUCT RELEASE'S TAG IS NOT ITS VERSION, and the registry is keyed by the
// version. Reconciling on the tag would report a fully reviewed product release
// as a gap — a red run whose only remedy is an entry that is already there, and
// which the maintainer cannot satisfy by adding anything.
func TestNodeRegistryReconcilesTheVersionNotTheTag(t *testing.T) {
	published, err := publishedReleases([]string{"release/4.1.0", "release/4.0.0"})
	if err != nil {
		t.Fatal(err)
	}

	t.Run("both versions reviewed is current", func(t *testing.T) {
		got := nodeRegistryReport(nodeRegistry{
			Releases: []nodeRegistryRelease{{Version: "4.0.0"}, {Version: "4.1.0"}},
		}, published, nil)
		if got.Verdict != version.CeilingCurrent {
			t.Fatalf("verdict = %s, want current: %+v", got.Verdict, got)
		}
		if exitCode([]version.CeilingReport{got}) != 0 {
			t.Error("a fully accounted registry must exit 0")
		}
		// The row still speaks in tags, because a tag is what locates a release.
		if got.Latest != "release/4.1.0" {
			t.Fatalf("latest = %q, want the published tag", got.Latest)
		}
	})

	t.Run("an unreviewed version is a gap named by version", func(t *testing.T) {
		got := nodeRegistryReport(nodeRegistry{
			Releases: []nodeRegistryRelease{{Version: "4.0.0"}},
		}, published, nil)
		if got.Verdict != version.CeilingBehind {
			t.Fatalf("verdict = %s, want behind: %+v", got.Verdict, got)
		}
		// The registry is keyed by version, so the reason names the version to add
		// rather than the tag nobody writes there.
		if !strings.Contains(got.Reason, "4.1.0") || strings.Contains(got.Reason, "release/4.1.0") {
			t.Errorf("reason must name the version to review, not the tag: %q", got.Reason)
		}
		// The ceiling is the newest release that WAS ruled on, shown as its tag.
		if got.Ceiling != "release/4.0.0" {
			t.Fatalf("ceiling = %q, want the tag of the newest reviewed release", got.Ceiling)
		}
	})
}

// A TAG THAT NAMES NO VERSION IS NOT SKIPPED. Dropping it would let the registry
// check pass while describing a repository with a release nobody can account for,
// which is the silence this job exists to refuse.
func TestPublishedReleasesRefusesATagItCannotIdentify(t *testing.T) {
	for _, tag := range []string{"main", "release/4.0", "v4.0.0.1", "release/v4.0.0"} {
		t.Run(tag, func(t *testing.T) {
			got, err := publishedReleases([]string{"release/4.0.0", tag})
			if err == nil {
				t.Fatalf("publishedReleases(%q) accepted a tag it cannot identify: %+v", tag, got)
			}
			if !strings.Contains(err.Error(), tag) {
				t.Errorf("refusal does not name the tag the reader has to look at: %v", err)
			}
		})
	}
	// A BARE VERSION IS NOT AN ADDRESS. This used to be the legacy round trip -
	// the tag and the version were one string, so a bare version resolved to
	// itself - and it is a refusal now: a release is published AT
	// release/<version>, and a string that names no address locates no release.
	if _, err := publishedReleases([]string{"4.1.1"}); err == nil {
		t.Fatal("a bare version was accepted as a tag")
	}
}

// The remedy is what a maintainer acts on, so a gap in our own registry must not
// be answered with the instruction for raising a panel ceiling.
func TestRemedyPointsAtTheRightFile(t *testing.T) {
	var sb strings.Builder
	writeTable(&sb, []version.CeilingReport{
		{Upstream: "3X-UI", Ceiling: "3.7.0", Latest: "3.8.0", Verdict: version.CeilingBehind, Reason: "ahead"},
		{Upstream: nodeRegistryUpstream, Ceiling: "4.0.2", Latest: "4.1.1", Verdict: version.CeilingBehind, Reason: "unaccounted"},
	})
	out := sb.String()
	if !strings.Contains(out, compatPath) {
		t.Errorf("the panel row must name %s:\n%s", compatPath, out)
	}
	if !strings.Contains(out, nodeRegistryPath) {
		t.Errorf("the registry row must name %s:\n%s", nodeRegistryPath, out)
	}
	// Line-level, not just presence: each remedy must be attributed to its own
	// row, or a reader cannot tell which gap the instruction answers.
	var panelLine, registryLine bool
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(line, "3X-UI ") && strings.Contains(line, compatPath) {
			panelLine = true
		}
		if strings.HasPrefix(line, nodeRegistryUpstream+" ") && strings.Contains(line, nodeRegistryPath) {
			registryLine = true
		}
	}
	if !panelLine || !registryLine {
		t.Errorf("each remedy must sit on its own row (panel=%v registry=%v):\n%s", panelLine, registryLine, out)
	}
}

// githubRecorder observes what the API asked for. The retry policy is a claim
// about requests and delays, and asserting it through the real backoff would
// cost the test six seconds of wall clock per case — so the delays are recorded
// rather than spent.
type githubRecorder struct {
	mu     sync.Mutex
	urls   []string
	delays []time.Duration
}

func (r *githubRecorder) URLs() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.urls...)
}

func (r *githubRecorder) Delays() []time.Duration {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]time.Duration(nil), r.delays...)
}

// testAPI points the outbound dependency at a local server.
func testAPI(t *testing.T, handler http.HandlerFunc) (githubAPI, *githubRecorder) {
	t.Helper()
	recorder := &githubRecorder{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		recorder.mu.Lock()
		recorder.urls = append(recorder.urls, r.URL.String())
		recorder.mu.Unlock()
		handler(w, r)
	}))
	t.Cleanup(server.Close)
	api := githubAPI{
		client:  server.Client(),
		baseURL: server.URL,
		sleep: func(_ context.Context, d time.Duration) error {
			recorder.mu.Lock()
			recorder.delays = append(recorder.delays, d)
			recorder.mu.Unlock()
			return nil
		},
	}
	return api, recorder
}

func releaseJSON(tag string, draft bool, published string) string {
	return fmt.Sprintf(`{"tag_name":%q,"draft":%t,"published_at":%q}`, tag, draft, published)
}

func listing(entries ...string) string { return "[" + strings.Join(entries, ",") + "]" }

// pageOf builds a full page of entries, tagged by index so one page's contents
// are distinguishable from another's.
func pageOf(prefix string, start, count int) string {
	entries := make([]string, 0, count)
	for i := start; i < start+count; i++ {
		entries = append(entries, releaseJSON(fmt.Sprintf("%s-%03d", prefix, i), false, "2026-09-18T09:37:02Z"))
	}
	return listing(entries...)
}

func serveListing(t *testing.T, pages map[int]string) (githubAPI, *githubRecorder) {
	t.Helper()
	return testAPI(t, func(w http.ResponseWriter, r *http.Request) {
		page := 1
		if raw := r.URL.Query().Get("page"); raw != "" {
			_, _ = fmt.Sscanf(raw, "%d", &page)
		}
		body, ok := pages[page]
		if !ok {
			t.Errorf("unexpected page requested: %s", r.URL)
			w.WriteHeader(http.StatusNotFound)
			return
		}
		_, _ = fmt.Fprint(w, body)
	})
}

// THE ORDERING THE UPGRADE DIALOG DEPENDS ON. Nothing here is derived from the
// tag string: v0.0.1-beta11 sorts BELOW v0.0.1-beta9, so a listing ordered by
// version would present the older release as the newest — which is how the
// dialog came to recommend beta9 while beta11 was already published.
func TestAllReleasesOrdersByPublicationNotVersion(t *testing.T) {
	api, _ := serveListing(t, map[int]string{1: listing(
		releaseJSON("4.0.2", false, "2026-09-17T08:10:41Z"),
		releaseJSON("4.1.1", false, "2026-09-18T10:31:27Z"),
		releaseJSON("4.1.0", false, "2026-09-18T09:37:02Z"),
	)})
	got, err := api.allReleases(context.Background(), "o/r")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"4.1.1", "4.1.0", "4.0.2"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("allReleases = %v, want newest published first %v", got, want)
	}
}

// A draft is not published, and an entry with no tag names nothing to account
// for. Either one reaching the registry check would demand a decision about a
// release that does not exist.
func TestAllReleasesDropsDraftsAndUntagged(t *testing.T) {
	api, _ := serveListing(t, map[int]string{1: listing(
		releaseJSON("4.1.1", false, "2026-09-18T10:31:27Z"),
		releaseJSON("v0.0.1-beta12", true, "2026-09-18T11:00:00Z"),
		releaseJSON("", false, "2026-09-18T12:00:00Z"),
	)})
	got, err := api.allReleases(context.Background(), "o/r")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0] != "4.1.1" {
		t.Fatalf("allReleases = %v, want only the published, tagged release", got)
	}
}

// The listing is a fact about the repository, so it must reach past the first
// page: a release that fell off the end would look exactly like one nobody
// published.
func TestAllReleasesPagesUntilAShortPage(t *testing.T) {
	api, recorder := serveListing(t, map[int]string{
		1: pageOf("v0.0.1-p", 0, 100),
		2: pageOf("v0.0.1-p", 100, 2),
	})
	got, err := api.allReleases(context.Background(), "o/r")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 102 {
		t.Fatalf("allReleases returned %d releases, want both pages", len(got))
	}
	urls := recorder.URLs()
	if len(urls) != 2 || !strings.Contains(urls[0], "page=1") || !strings.Contains(urls[1], "page=2") {
		t.Fatalf("paging stopped early or skipped a page: %v", urls)
	}
}

// The one answer this must never give is an all-clear over a tail it could not
// see, so running out of pages is an error rather than a truncation.
func TestAllReleasesRefusesToTruncateSilently(t *testing.T) {
	full := pageOf("v0.0.1-p", 0, 100)
	pages := map[int]string{}
	for page := 1; page <= 12; page++ {
		pages[page] = full
	}
	api, recorder := serveListing(t, pages)
	got, err := api.allReleases(context.Background(), "o/r")
	if err == nil {
		t.Fatalf("a full last page must not be read as the end of the listing: got %d releases", len(got))
	}
	if len(got) != 0 {
		t.Fatalf("a refused listing must return nothing, got %d releases", len(got))
	}
	if len(recorder.URLs()) != 10 {
		t.Fatalf("paged %d times, want the declared bound of 10", len(recorder.URLs()))
	}
}

func TestAllReleasesReportsTransportFailure(t *testing.T) {
	api, recorder := testAPI(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	})
	got, err := api.allReleases(context.Background(), "o/r")
	if err == nil {
		t.Fatalf("an unreadable listing must not read as an empty one: %v", got)
	}
	// A listing that is retried only once would call a single GitHub blip an
	// unreadable registry, which is the false alarm fetchAttempts exists to
	// prevent — so the retry budget applies here exactly as it does upstream.
	if len(recorder.URLs()) != 3 {
		t.Fatalf("made %d attempts, want the full retry budget of 3", len(recorder.URLs()))
	}
	if d := recorder.Delays(); len(d) != 2 || d[0] != 2*time.Second || d[1] != 4*time.Second {
		t.Fatalf("backoff = %v, want a growing 2s then 4s", d)
	}
}

func TestGetRetry(t *testing.T) {
	t.Run("a transient failure is retried and succeeds", func(t *testing.T) {
		var calls int
		api, recorder := testAPI(t, func(w http.ResponseWriter, _ *http.Request) {
			calls++
			if calls == 1 {
				w.WriteHeader(http.StatusInternalServerError)
				return
			}
			_, _ = fmt.Fprint(w, `{"tag_name":"v1.0.0"}`)
		})
		var payload struct {
			TagName string `json:"tag_name"`
		}
		if err := api.getRetry(context.Background(), "/repos/o/r/releases/latest", &payload); err != nil {
			t.Fatal(err)
		}
		if payload.TagName != "v1.0.0" {
			t.Fatalf("payload = %+v, want the second attempt's body", payload)
		}
		if len(recorder.URLs()) != 2 {
			t.Fatalf("made %d attempts, want 2", len(recorder.URLs()))
		}
	})

	t.Run("a persistent failure stops at the retry budget", func(t *testing.T) {
		api, recorder := testAPI(t, func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
		})
		if err := api.getRetry(context.Background(), "/repos/o/r/releases/latest", &struct{}{}); err == nil {
			t.Fatal("a permanently failing read must return an error")
		}
		if len(recorder.URLs()) != 3 {
			t.Fatalf("made %d attempts, want the full retry budget of 3", len(recorder.URLs()))
		}
	})

	t.Run("a cancelled context is not retried", func(t *testing.T) {
		api, recorder := testAPI(t, func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
		})
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if err := api.getRetry(ctx, "/repos/o/r/releases/latest", &struct{}{}); err == nil {
			t.Fatal("a cancelled read must return an error")
		}
		// Retrying after the caller gave up would spend the budget on a request
		// nobody is waiting for, and the context error must survive instead of
		// being replaced by the last transport failure.
		if len(recorder.Delays()) != 0 {
			t.Fatalf("waited %d times after cancellation", len(recorder.Delays()))
		}
	})
}

// The failure this guards is a read that reports success while returning
// nothing: an empty listing would make every published release look accounted
// for.
func TestGetRetryRejectsUnparseableBody(t *testing.T) {
	api, recorder := testAPI(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = fmt.Fprint(w, `{"tag_name":`)
	})
	if err := api.getRetry(context.Background(), "/repos/o/r/releases/latest", &struct{}{}); err == nil {
		t.Fatal("a truncated body must not decode as a successful read")
	}
	if len(recorder.URLs()) != 3 {
		t.Fatalf("made %d attempts, want the full retry budget of 3", len(recorder.URLs()))
	}
}
