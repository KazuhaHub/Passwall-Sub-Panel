package version

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
)

type channelTransport func(*http.Request) (*http.Response, error)

func (f channelTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestChannelRequiresPublicationEvidenceForPlainVersions(t *testing.T) {
	oldVersion, oldClient := Version, httpClient
	t.Cleanup(func() { Version, httpClient = oldVersion, oldClient; currentPublication.Store(buildPublication{}) })
	currentPublication.Store(buildPublication{})
	for _, tc := range []struct{ version, want string }{
		{"4.1.0-beta.1", "testing"}, {"4.1.0-rc.1", "testing"}, {"4.1.0", "unknown"}, {"4.0.1.22", "unknown"}, {"dev", "unknown"},
	} {
		Version = tc.version
		if got := Channel(); got != tc.want {
			t.Errorf("%+v: %s", tc, got)
		}
	}
	Version = "4.1.0"
	for _, tc := range []struct {
		body, want string
		ok         bool
	}{
		{`{"tag_name":"v4.1.0","draft":false,"prerelease":true}`, "testing", true},
		{`{"tag_name":"v4.1.0","draft":false,"prerelease":false}`, "stable", true},
		{`{"tag_name":"v4.0.0","draft":false,"prerelease":false}`, "unknown", false},
		{`{"tag_name":"v4.1.0","draft":true,"prerelease":false}`, "unknown", false},
	} {
		currentPublication.Store(buildPublication{})
		httpClient = &http.Client{Transport: channelTransport(func(r *http.Request) (*http.Response, error) {
			if !strings.HasSuffix(r.URL.Path, "/releases/tags/v4.1.0") {
				t.Fatal(r.URL)
			}
			return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(tc.body)), Header: make(http.Header)}, nil
		})}
		err := refreshBuildChannel(context.Background())
		if (err == nil) != tc.ok || Channel() != tc.want {
			t.Errorf("%s: %s %v", tc.body, Channel(), err)
		}
	}
	if _, ok := acceptLatestPSPStable("v4.1.0-beta.1", false); ok {
		t.Fatal("suffix mislabelled stable accepted")
	}
	if !pspBehindStable("4.1.0-rc.1", "v4.1.0") {
		t.Fatal("final version not offered to rc")
	}
}
