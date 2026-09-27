package riskcenter

import (
	"errors"
	"math"
	"math/bits"
	"slices"
	"testing"
	"time"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

func userIDs(v LiveView) []int64 {
	out := make([]int64, 0, len(v.Users))
	for _, u := range v.Users {
		out = append(out, u.UserID)
	}
	return out
}

func page(p, size int) ports.Pagination { return ports.Pagination{Page: p, PageSize: size} }

// The live view is a list of ACCOUNTS, each with its connections: the one
// with the most connections first (the account an admin most likely opened
// the tab for), ties by id, paged by account. Only the page's accounts are
// looked up — a fleet of thousands is one snapshot in memory, not thousands
// of user reads per click.
func TestLive_PaginatesUsersByConnectionCount(t *testing.T) {
	h := newHarness()
	for id := int64(1); id <= 3; id++ {
		h.user(id, "u"+string(rune('0'+id)))
	}
	h.live.snap = pollSnapshot(testNow.Add(-time.Minute),
		conn(1, 1, "198.51.100.1", ""),
		conn(2, 1, "198.51.100.2", ""), conn(2, 1, "198.51.100.3", ""), conn(2, 2, "198.51.100.4", ""),
		conn(3, 1, "198.51.100.5", ""), conn(3, 2, "198.51.100.6", ""),
	)

	v, err := h.svc.Live(t.Context(), LiveQuery{Pagination: page(1, 2)})
	if err != nil {
		t.Fatal(err)
	}
	if got := userIDs(v); !slices.Equal(got, []int64{2, 3}) || v.Total != 3 {
		t.Fatalf("page 1 = %v of %d, want [2 3] of 3 (most connections first)", got, v.Total)
	}
	if len(v.Users[0].Conns) != 3 || v.Users[0].Meta.Stale != 2 {
		t.Fatalf("account 2 = %+v, want its 3 connections and its meta", v.Users[0])
	}
	if !slices.Equal(h.users.calls, []int64{2, 3}) {
		t.Fatalf("looked up %v, want only the page's accounts [2 3]", h.users.calls)
	}

	v, err = h.svc.Live(t.Context(), LiveQuery{Pagination: page(2, 2)})
	if err != nil {
		t.Fatal(err)
	}
	if got := userIDs(v); !slices.Equal(got, []int64{1}) || v.Total != 3 {
		t.Fatalf("page 2 = %v of %d, want [1] of 3", got, v.Total)
	}

	v, err = h.svc.Live(t.Context(), LiveQuery{Pagination: ports.Pagination{Page: 1, PageSize: 10, SortBy: "user_id"}})
	if err != nil {
		t.Fatal(err)
	}
	if got := userIDs(v); !slices.Equal(got, []int64{1, 2, 3}) {
		t.Fatalf("sort_by user_id = %v, want [1 2 3]", got)
	}
	v, err = h.svc.Live(t.Context(), LiveQuery{Pagination: ports.Pagination{Page: 1, PageSize: 10, SortDir: "asc"}})
	if err != nil {
		t.Fatal(err)
	}
	if got := userIDs(v); !slices.Equal(got, []int64{1, 3, 2}) {
		t.Fatalf("fewest connections first = %v, want [1 3 2]", got)
	}
}

// The filters narrow the CONNECTIONS, and an account with none left is not
// listed: an account, a panel, the judged sources, the ones set aside, or
// one reason for setting aside. An exclusion filter the view does not know
// is a validation error, never an empty list — a typo must not read as
// "nobody is connected".
func TestLive_FiltersByUserPanelAndExclusion(t *testing.T) {
	h := newHarness()
	h.user(1, "a")
	h.user(2, "b")
	h.live.snap = pollSnapshot(testNow.Add(-time.Minute),
		conn(1, 1, "198.51.100.1", ""),
		conn(1, 2, "10.0.0.1", domain.AddressExcludedInternal),
		conn(2, 2, "203.0.113.9", domain.AddressExcludedInfra),
		conn(2, 2, "198.51.100.2", ""),
	)
	count := func(v LiveView) (n int) {
		for _, u := range v.Users {
			n += len(u.Conns)
		}
		return n
	}
	for _, c := range []struct {
		name  string
		q     LiveQuery
		users []int64
		conns int
	}{
		{"one account", LiveQuery{UserID: 2}, []int64{2}, 2},
		{"one panel", LiveQuery{PanelID: 1}, []int64{1}, 1},
		{"judged only", LiveQuery{Exclusion: ports.ConnExclusionKept}, []int64{1, 2}, 2},
		{"set aside only", LiveQuery{Exclusion: ports.ConnExclusionExcluded}, []int64{1, 2}, 2},
		{"one reason", LiveQuery{Exclusion: domain.AddressExcludedInfra}, []int64{2}, 1},
		{"account and panel", LiveQuery{UserID: 1, PanelID: 2}, []int64{1}, 1},
		{"nothing matches", LiveQuery{UserID: 9}, []int64{}, 0},
	} {
		t.Run(c.name, func(t *testing.T) {
			c.q.Pagination = ports.Pagination{Page: 1, PageSize: 10, SortBy: "user_id"}
			v, err := h.svc.Live(t.Context(), c.q)
			if err != nil {
				t.Fatal(err)
			}
			if got := userIDs(v); !slices.Equal(got, c.users) || count(v) != c.conns || v.Total != int64(len(c.users)) {
				t.Fatalf("accounts %v with %d connections (total %d), want %v with %d", got, count(v), v.Total, c.users, c.conns)
			}
		})
	}
	if _, err := h.svc.Live(t.Context(), LiveQuery{Exclusion: "nonsense"}); !errors.Is(err, domain.ErrValidation) {
		t.Fatalf("unknown exclusion: err = %v, want ErrValidation", err)
	}
}

// A snapshot older than the staleness setting is marked stale, so an admin
// looking at a view the poll stopped refreshing is told so rather than
// reading an old picture as now.
func TestLive_MarksAStaleSnapshot(t *testing.T) {
	h := newHarness()
	h.live.snap = pollSnapshot(testNow.Add(-16 * time.Minute))
	v, err := h.svc.Live(t.Context(), LiveQuery{})
	if err != nil {
		t.Fatal(err)
	}
	if !v.Stale || v.StaleAfter != 15*time.Minute || v.Age != 16*time.Minute {
		t.Fatalf("16-minute-old snapshot: age %v, stale %v after %v; want 16m, stale after the default 15m", v.Age, v.Stale, v.StaleAfter)
	}
	h.live.snap = pollSnapshot(testNow.Add(-14 * time.Minute))
	if v, _ = h.svc.Live(t.Context(), LiveQuery{}); v.Stale {
		t.Fatal("a 14-minute-old snapshot is marked stale under a 15-minute setting")
	}
	// The setting moves it; an unreadable settings table is the default.
	h.settings.set.RiskLiveSnapshotStaleMinutes = 10
	h.live.snap = pollSnapshot(testNow.Add(-11 * time.Minute))
	if v, _ = h.svc.Live(t.Context(), LiveQuery{}); !v.Stale || v.StaleAfter != 10*time.Minute {
		t.Fatalf("stale %v after %v, want stale after the configured 10m", v.Stale, v.StaleAfter)
	}
	h.settings.err = errors.New("settings table unreadable")
	if v, err = h.svc.Live(t.Context(), LiveQuery{}); err != nil || v.Stale || v.StaleAfter != 15*time.Minute {
		t.Fatalf("unreadable settings: stale %v after %v (err %v), want the default 15m and no error", v.Stale, v.StaleAfter, err)
	}
}

// A poll snapshot is replaced only by the next poll, so at a slow poll it is
// never called stale before two polls were missed, whatever the setting.
func TestLive_StaleFloorIsTwoPolls(t *testing.T) {
	h := newHarness()
	h.settings.set.CronTrafficPullMinutes = 30
	h.live.snap = pollSnapshot(testNow.Add(-40 * time.Minute))
	v, err := h.svc.Live(t.Context(), LiveQuery{})
	if err != nil {
		t.Fatal(err)
	}
	if v.Stale || v.StaleAfter != time.Hour {
		t.Fatalf("40 minutes at a 30-minute poll: stale %v after %v, want fresh until two polls (1h)", v.Stale, v.StaleAfter)
	}
	h.live.snap = pollSnapshot(testNow.Add(-61 * time.Minute))
	if v, _ = h.svc.Live(t.Context(), LiveQuery{}); !v.Stale {
		t.Fatal("61 minutes at a 30-minute poll is not marked stale")
	}
}

// Before the first poll there is no snapshot: the view says so (stale, no
// accounts) and reads nothing else.
func TestLive_NoSnapshotYet(t *testing.T) {
	h := newHarness()
	v, err := h.svc.Live(t.Context(), LiveQuery{})
	if err != nil {
		t.Fatal(err)
	}
	if v.Snapshot != nil || !v.Stale || len(v.Users) != 0 || v.Total != 0 {
		t.Fatalf("view = %+v, want no snapshot, stale, no accounts", v)
	}
	if len(h.users.calls) != 0 || h.fetches.calls != 0 {
		t.Fatalf("read %d users and %d fetch pages with no snapshot", len(h.users.calls), h.fetches.calls)
	}
	if v.RefreshCooldown != 30*time.Second || v.RefreshAvailableIn != 0 {
		t.Fatalf("refresh cooldown %v, available in %v; want 30s and now", v.RefreshCooldown, v.RefreshAvailableIn)
	}
}

// Devices are inferred from the fetch log over risk.device_infer_hours — but
// never from further back than the fetch log is kept: a window past the
// sub-log retention reads rows that are not there and would claim a day's
// inference from hours of data.
func TestLive_DeviceWindowIsClampedBySubLogRetention(t *testing.T) {
	for _, c := range []struct {
		name            string
		hours, retained int
		want            time.Duration
	}{
		{"the default day", 0, 0, 24 * time.Hour},
		{"three days, kept for two", 72, 2, 48 * time.Hour},
		{"twelve hours, kept for two days", 12, 2, 12 * time.Hour},
		{"kept for ever", 100, 0, 100 * time.Hour},
	} {
		t.Run(c.name, func(t *testing.T) {
			h := newHarness()
			h.user(1, "a")
			h.settings.set.RiskDeviceInferHours = c.hours
			h.settings.set.SubLogRetentionDays = c.retained
			h.live.snap = pollSnapshot(testNow.Add(-time.Minute), conn(1, 1, "198.51.100.1", ""))
			v, err := h.svc.Live(t.Context(), LiveQuery{})
			if err != nil {
				t.Fatal(err)
			}
			if v.DeviceWindow != c.want || !h.fetches.gotSince.Equal(testNow.Add(-c.want)) {
				t.Fatalf("window %v, fetches read since %v; want %v (since %v)", v.DeviceWindow, h.fetches.gotSince, c.want, testNow.Add(-c.want))
			}
			if !slices.Equal(h.fetches.gotIDs, []int64{1}) || h.fetches.gotLimit != deviceInferMaxRows {
				t.Fatalf("fetches read for %v, limit %d; want the page's [1] and %d", h.fetches.gotIDs, h.fetches.gotLimit, deviceInferMaxRows)
			}
		})
	}
}

// Every connection carries its panel's name and every account its names;
// the filter's panel list is every panel, by id. An account deleted since
// the snapshot is not listed: its row is gone, and the view must not name
// somebody who no longer exists.
func TestLive_NamesPanelsAndUsers(t *testing.T) {
	h := newHarness()
	h.user(1, "alice")
	h.panels.panels = []*domain.XUIPanel{{ID: 2, Name: "hk-1"}, {ID: 1, Name: "jp-1"}}
	h.live.snap = pollSnapshot(testNow.Add(-time.Minute),
		conn(1, 1, "198.51.100.1", ""), conn(1, 2, "198.51.100.2", ""),
		conn(5, 1, "198.51.100.5", ""), // account 5 was deleted
	)
	v, err := h.svc.Live(t.Context(), LiveQuery{Pagination: page(1, 10)})
	if err != nil {
		t.Fatal(err)
	}
	if len(v.Users) != 1 || v.Users[0].UPN != "alice" || v.Users[0].DisplayName != "Name alice" {
		t.Fatalf("accounts = %+v, want only alice, named", v.Users)
	}
	if names := []string{v.Users[0].Conns[0].PanelName, v.Users[0].Conns[1].PanelName}; !slices.Equal(names, []string{"jp-1", "hk-1"}) {
		t.Fatalf("panel names = %v, want [jp-1 hk-1]", names)
	}
	if !slices.Equal(v.Panels, []PanelRef{{ID: 1, Name: "jp-1"}, {ID: 2, Name: "hk-1"}}) {
		t.Fatalf("panels = %+v, want every panel by id", v.Panels)
	}
	if h.panels.calls != 1 {
		t.Fatalf("panels listed %d times, want once", h.panels.calls)
	}
	h.users.err = errors.New("users table unreadable")
	if _, err := h.svc.Live(t.Context(), LiveQuery{}); err == nil {
		t.Fatal("an unreadable users table answered as if the accounts were deleted")
	}
}

// Device inference is a convenience on top of the connections: when the
// fetch log cannot be read the connections are still listed, with no
// devices and a flag saying the inference is missing (not "none found").
func TestLive_FetchFailureOmitsDevicesOnly(t *testing.T) {
	h := newHarness()
	h.user(1, "a")
	h.live.snap = pollSnapshot(testNow.Add(-time.Minute), conn(1, 1, "198.51.100.1", ""))
	h.fetches.rows = []domain.SubLog{{UserID: 1, IP: "198.51.100.1", UA: "clash", ClientType: "mihomo", AccessedAt: testNow.Add(-time.Hour)}}

	v, err := h.svc.Live(t.Context(), LiveQuery{})
	if err != nil {
		t.Fatal(err)
	}
	if v.DevicesUnavailable || len(v.Users) != 1 || len(v.Users[0].Conns[0].Devices) != 1 || v.Users[0].Conns[0].Devices[0].UA != "clash" {
		t.Fatalf("view = %+v, want the connection with its inferred device", v.Users)
	}

	h.fetches.err = errors.New("sub_logs unreadable")
	v, err = h.svc.Live(t.Context(), LiveQuery{})
	if err != nil {
		t.Fatalf("a fetch-log failure failed the view: %v", err)
	}
	if !v.DevicesUnavailable || len(v.Users) != 1 || len(v.Users[0].Conns) != 1 || len(v.Users[0].Conns[0].Devices) != 0 {
		t.Fatalf("view = %+v (unavailable %v), want the connection, no devices, and the flag", v.Users, v.DevicesUnavailable)
	}
}

// The history and the flag records are the stores' own pages, passed
// through with the filter as given; the history's panel ids are named from
// one panel listing, as the live view's are.
func TestHistoryAndFlags_PassTheFilterThrough(t *testing.T) {
	h := newHarness()
	h.panels.panels = []*domain.XUIPanel{{ID: 1, Name: "jp-1"}}
	uid := int64(7)
	h.history.rows = []domain.ConnectionRecord{{UserID: 7, PanelID: 1}, {UserID: 7, PanelID: 3}}
	h.history.total = 12
	rows, names, total, err := h.svc.History(t.Context(), ports.ConnectionHistoryFilter{UserID: &uid, Search: "jp"})
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 || total != 12 || h.history.got.UserID == nil || *h.history.got.UserID != 7 || h.history.got.Search != "jp" {
		t.Fatalf("history = %d rows of %d, filter %+v; want the store's page and the filter as given", len(rows), total, h.history.got)
	}
	if names[1] != "jp-1" || names[3] != "" {
		t.Fatalf("panel names = %v, want 1 named and a deleted panel 3 unnamed", names)
	}

	h.flags.rows = []domain.FlagRecord{{ID: 1, UserID: 7}}
	h.flags.total = 1
	recs, n, err := h.svc.Flags(t.Context(), ports.FlagRecordFilter{Source: domain.FlagSourceGeo})
	if err != nil || len(recs) != 1 || n != 1 || h.flags.got.Source != domain.FlagSourceGeo {
		t.Fatalf("flags = %+v of %d (%v), filter %+v", recs, n, err, h.flags.got)
	}

	h.history.err = errors.New("history unreadable")
	if _, _, _, err := h.svc.History(t.Context(), ports.ConnectionHistoryFilter{}); err == nil {
		t.Fatal("a history read error was swallowed")
	}
}

// liveNoPanic is Live, with a panic reported as the test's failure.
func liveNoPanic(t *testing.T, h *harness, q LiveQuery) (v LiveView, err error) {
	t.Helper()
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("Live(page %d, size %d) panicked: %v", q.Page, q.PageSize, r)
		}
	}()
	return h.svc.Live(t.Context(), q)
}

// The page number is the caller's, unbounded (parsePagination only floors
// it at 1). A page past the end is an empty page with the true total, like
// every SQL-backed admin list — never a panic from an offset that
// overflowed negative, and never the first page again from one that
// wrapped round to zero.
func TestLive_APageFarPastTheEndIsEmpty(t *testing.T) {
	h := newHarness()
	h.user(1, "u1")
	h.user(2, "u2")
	h.live.snap = pollSnapshot(testNow.Add(-time.Minute), conn(1, 1, "198.51.100.1", ""), conn(2, 1, "198.51.100.2", ""))
	// (page-1)*200 = 25 * 2^UintSize, which wraps to exactly 0.
	wrapsToZero := 1<<(bits.UintSize-3) + 1
	for name, p := range map[string]ports.Pagination{
		"the largest page":      {Page: math.MaxInt, PageSize: 200},
		"a page that wraps":     {Page: wrapsToZero, PageSize: 200},
		"just past the end":     {Page: 2, PageSize: 2},
		"the default size, far": {Page: math.MaxInt / 2, PageSize: 0},
	} {
		h.users.calls = nil
		v, err := liveNoPanic(t, h, LiveQuery{Pagination: p})
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if len(v.Users) != 0 || v.Total != 2 {
			t.Fatalf("%s: page = %v of %d, want an empty page of 2", name, userIDs(v), v.Total)
		}
		if len(h.users.calls) != 0 {
			t.Fatalf("%s: looked up %v for an empty page", name, h.users.calls)
		}
	}
}
