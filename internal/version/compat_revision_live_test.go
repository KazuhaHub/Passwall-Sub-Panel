package version

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

// The pure rule is necessary but not sufficient: it proves the comparison is
// right, not that anything calls it. This drives the real fetch-and-apply path
// over an HTTP server so a future edit that drops the call is caught.
func TestFetchAndApplyRefusesAnOlderManifest(t *testing.T) {
	priorVersion := Version
	priorMax := ActiveMaxTestedXUI()
	priorApplied := currentAppliedRevision(productXUI)
	// THE BUILD IS A PRODUCT ONE, so the document it fetches is the one addressed
	// by name: a per-major manifest would be refused, because a product version has
	// no derivable compatibility major.
	Version = "4.0.0"
	t.Cleanup(func() {
		Version = priorVersion
		SetActiveMaxTestedXUI(priorMax)
		setAppliedRevision(productXUI, priorApplied)
	})
	// AND NOTHING IS IN FORCE TO BEGIN WITH. The guard is about the ORDER of two
	// revisions, and the document this repository ships is applied before any test
	// runs — so a fixture dated before it would be refused for a reason that has
	// nothing to do with what is under test.
	setAppliedRevision(productXUI, "")

	var updatedAt, maxTested string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		// A 3X-UI PANEL RANGES DOCUMENT: it says which product it is, states the builds
		// it applies to, and carries its revision as issued_at.
		fmt.Fprintf(w, `{"schema_version":1,"product":"3x-ui","revision":1,"issued_at":%q,"expires_at":"2099-01-01T00:00:00Z","applies_to_psp":{"min":"4.0.0","max":"4.99.99"},"entries":[{"psp_min":"4.0.0","psp_max":"4.99.99","min_xui":"3.4.2","max_tested_xui":%q}]}`, updatedAt, maxTested)
	}))
	defer server.Close()

	// The production client is safehttp, which refuses loopback by design — the
	// SSRF guard doing its job. Swapping the package's client for the test
	// server's is the seam this path already exposes.
	priorClient := httpClient
	httpClient = server.Client()
	defer func() { httpClient = priorClient }()

	apply := func(when, max string) error {
		updatedAt, maxTested = when, max
		return fetchAndApply(context.Background(), compatSource{Product: productXUI, Name: "3x-ui-v4.json", URL: server.URL})
	}

	// The first manifest installs, and its revision is what later ones are
	// compared against.
	if err := apply("2026-09-01T00:00:00Z", "3.8.5"); err != nil {
		t.Fatalf("the first manifest must apply: %v", err)
	}
	if got := currentAppliedRevision(productXUI); got != "2026-09-01T00:00:00Z" {
		t.Fatalf("applied revision = %q, want the first manifest's", got)
	}

	// A newer one installs and moves the tested range.
	if err := apply("2026-09-02T00:00:00Z", "3.9.0"); err != nil {
		t.Fatalf("a newer manifest must apply: %v", err)
	}
	if got := ActiveMaxTestedXUI(); got != "3.9.0" {
		t.Fatalf("tested ceiling = %q, want the newer manifest's 3.9.0", got)
	}

	// An OLDER one is refused, and the range in force does not move with it.
	err := apply("2026-08-01T00:00:00Z", "3.7.0")
	if err == nil {
		t.Fatal("an older manifest replaced a newer tested range")
	}
	if got := ActiveMaxTestedXUI(); got != "3.9.0" {
		t.Fatalf("the tested ceiling regressed to %q; a refused manifest must leave the range in force", got)
	}
	if got := currentAppliedRevision(productXUI); got != "2026-09-02T00:00:00Z" {
		t.Fatalf("the applied revision regressed to %q", got)
	}
}
