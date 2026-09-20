package version

import (
	"strings"
	"testing"
	"time"
)

// shouldFetchCompat is the throttle/force decision RefreshRemoteCompat makes
// before hitting the network. The panel-upgrade gate calls with force=true so
// its "is this 3X-UI version supported" check uses a freshly-fetched tested
// range, not a possibly-stale cache from boot or the last Servers-page open.
func TestShouldFetchCompat(t *testing.T) {
	now := time.Now()
	cases := []struct {
		name   string
		lastAt time.Time
		force  bool
		want   bool
	}{
		{"never fetched -> fetch", time.Time{}, false, true},
		{"within throttle, no force -> skip", now.Add(-remoteFetchThrottle / 2), false, false},
		{"within throttle, FORCE -> fetch", now.Add(-remoteFetchThrottle / 2), true, true},
		{"past throttle, no force -> fetch", now.Add(-remoteFetchThrottle - time.Second), false, true},
		{"just fetched, FORCE -> fetch", now, true, true},
	}
	for _, c := range cases {
		if got := shouldFetchCompat(c.lastAt, now, c.force); got != c.want {
			t.Errorf("%s: shouldFetchCompat(lastAt=%v, force=%v) = %v, want %v",
				c.name, c.lastAt, c.force, got, c.want)
		}
	}
}

// TestDefaultCompatURLIsServedFromThisBranch pins the one fact the compat
// refresh cannot check for itself: WHICH branch it reads.
//
// A wrong branch here fails in the worst possible shape — silently, in the
// field, months later. The fetch either 404s or serves a manifest nobody on
// this branch edited, RefreshRemoteCompat falls back to the boot cache, and
// the only visible symptom is an operator being refused a panel upgrade whose
// ceiling was raised weeks ago. Nothing in the build, and nothing in the JSON
// guards below, notices: they all read the LOCAL docs/compat/v3.json, which is
// exactly the file that is not being served when this constant is wrong.
//
// So the assertion is deliberately two-sided. "Contains release/v3" catches a
// typo; "does not contain /main/" catches the specific regression this test
// was written for — a merge, a cherry-pick from the V4 line, or a copy of the
// v4 file resurrecting the old URL. V3 code and the V3 manifest live on the
// same branch now; that invariant is what this protects.
func TestDefaultCompatURLIsServedFromThisBranch(t *testing.T) {
	const wantRef = "/refs/heads/release/v3/"
	if !strings.Contains(defaultRemoteCompatURLBase, wantRef) {
		t.Fatalf("defaultRemoteCompatURLBase = %q, want it to fetch through %q — "+
			"the V3 compat manifest is served from the branch that ships it",
			defaultRemoteCompatURLBase, wantRef)
	}
	if strings.Contains(defaultRemoteCompatURLBase, "/main/") {
		t.Fatalf("defaultRemoteCompatURLBase = %q points back at main. main is the V4 "+
			"line and shares no history with release/v3; a V3 build reading it would "+
			"depend on someone hand-copying this branch's docs/compat/v3.json across",
			defaultRemoteCompatURLBase)
	}
}

// TestDefaultURLForCurrentVersion checks the other half of the URL — the
// per-major filename — against the branch base above, and that a build whose
// version carries no major (source builds are the literal "dev") is refused
// rather than guessed into some default file.
func TestDefaultURLForCurrentVersion(t *testing.T) {
	orig := Version
	t.Cleanup(func() { Version = orig })

	for _, c := range []struct {
		version string
		want    string
	}{
		{"v3.9.3", defaultRemoteCompatURLBase + "v3.json"},
		{"v3.9.3-beta.1", defaultRemoteCompatURLBase + "v3.json"},
		{"3.9.3", defaultRemoteCompatURLBase + "v3.json"},
	} {
		Version = c.version
		got, err := defaultURLForCurrentVersion()
		if err != nil {
			t.Fatalf("Version=%q: unexpected error: %v", c.version, err)
		}
		if got != c.want {
			t.Errorf("Version=%q: got %q, want %q", c.version, got, c.want)
		}
	}

	Version = "dev"
	if got, err := defaultURLForCurrentVersion(); err == nil {
		t.Errorf("Version=\"dev\": got %q, want an error — a build with no derivable "+
			"major must not fetch some arbitrary major's file", got)
	}
}
