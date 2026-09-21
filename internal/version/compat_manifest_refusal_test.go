package version

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

// THE FOUR WAYS A COMPAT MANIFEST FETCH FAILS, AND WHAT MUST SURVIVE EACH.
//
// The rule the plan states is that a failure leaves the last valid value in
// force and shows it as stale — and, separately, that an unavailable cache must
// never read as "supported". Both halves are about the SAME hazard: a panel that
// cannot reach the manifest quietly widening what it claims to support, or
// quietly dropping the range it had already verified.
//
// fetchAndApply returns before mutating in every one of these paths, so the
// behaviour is already right. What was missing is anything that would notice if
// it stopped being right: the only refusal under test was the revision
// regression, and a walk through the fetch path that broke on a schema bump or a
// truncated body would have gone unnoticed.
//
// Each case therefore asserts THREE things, and the third is the one a partial
// fix would drop: an error came back, the range in force did not move, and the
// staleness is reportable. A fetch that failed silently — error swallowed, range
// left alone — would satisfy the first two while leaving an operator with no way
// to know the panel is running on a range it could not refresh.
func TestManifestFailureLeavesTheLastValidRangeInForce(t *testing.T) {
	priorVersion := Version
	priorMax := ActiveMaxTestedXUI()
	priorApplied := currentAppliedRevision("")
	Version = "v4.0.0-beta.20"
	t.Cleanup(func() {
		Version = priorVersion
		SetActiveMaxTestedXUI(priorMax)
		setAppliedRevision("", priorApplied)
	})

	const goodUpdatedAt = "2026-09-19"
	const goodMaxTested = "3.9.0"

	// The body the test server serves is a variable, so one server covers the
	// install and every refusal that follows it.
	var body string
	var status int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(status)
		fmt.Fprint(w, body)
	}))

	priorClient := httpClient
	httpClient = server.Client()
	t.Cleanup(func() {
		httpClient = priorClient
		server.Close()
	})

	refresh := func() error {
		return RefreshRemoteCompat(context.Background(), server.URL, true)
	}

	// A VALID MANIFEST FIRST, so there is something to lose. Without this the
	// refusals below would be asserting that nothing plus nothing is nothing.
	status = http.StatusOK
	body = fmt.Sprintf(
		`{"schema_version":2,"major":4,"updated_at":%q,"entries":[{"psp_min":"v4.0.0-beta.1","psp_max":"v4.0.0-beta.99","max_tested_xui":%q}]}`,
		goodUpdatedAt, goodMaxTested)
	if err := refresh(); err != nil {
		t.Fatalf("a valid manifest must apply: %v", err)
	}
	if got := ActiveMaxTestedXUI(); got != goodMaxTested {
		t.Fatalf("tested ceiling = %q, want %q after a valid manifest", got, goodMaxTested)
	}

	for _, tc := range []struct {
		name   string
		setup  func(t *testing.T)
		body   string
		status int
	}{
		{
			name:   "corrupt: the body is not JSON",
			body:   `{"schema_version":2,"major":4,`,
			status: http.StatusOK,
		},
		{
			name:   "unknown schema: a generation this build does not read",
			body:   `{"schema_version":99,"major":4,"updated_at":"2026-09-20","entries":[]}`,
			status: http.StatusOK,
		},
		{
			name:   "wrong major: the file declares another major",
			body:   `{"schema_version":2,"major":3,"updated_at":"2026-09-20","entries":[{"psp_min":"v4.0.0-beta.1","psp_max":"v4.0.0-beta.99","max_tested_xui":"4.0.0"}]}`,
			status: http.StatusOK,
		},
		{
			name:   "offline: the fetch returns an error status",
			body:   "",
			status: http.StatusBadGateway,
		},
		{
			name: "offline: nothing answers at all",
			setup: func(t *testing.T) {
				// A closed listener. Here the client's own error is the failure
				// rather than a status code, which is a different path through
				// the same refusal.
				server.Close()
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			status = tc.status
			body = tc.body
			if tc.setup != nil {
				tc.setup(t)
			}
			refreshedAt := LastRefreshAt()

			if err := refresh(); err == nil {
				t.Fatal("a manifest that could not be read was applied")
			}

			// THE RANGE IN FORCE DID NOT MOVE. This is the assertion; the error
			// above is only how the refusal announced itself.
			if got := ActiveMaxTestedXUI(); got != goodMaxTested {
				t.Errorf("tested ceiling = %q, want it left at %q", got, goodMaxTested)
			}
			if got := currentAppliedRevision(""); got != goodUpdatedAt {
				t.Errorf("applied revision = %q, want it left at %q", got, goodUpdatedAt)
			}

			// AND THE STALENESS IS REPORTABLE. A failure nobody can see is the
			// "unavailable cache reads as supported" hazard wearing the other
			// face: the range is still whatever it was, and no surface says so.
			if LastRefreshError() == nil {
				t.Error("the failed refresh left no error for an operator to see")
			}
			// `LastRefreshAt` is what the throttle and the "how old is this
			// range" display read. Advancing it on a failure would make a panel
			// that has never refreshed look freshly refreshed.
			if got := LastRefreshAt(); !got.Equal(refreshedAt) {
				t.Errorf("LastRefreshAt moved from %v to %v on a failed fetch, so staleness is unreadable", refreshedAt, got)
			}
		})
	}
}
