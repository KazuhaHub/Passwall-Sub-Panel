package version

import (
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
