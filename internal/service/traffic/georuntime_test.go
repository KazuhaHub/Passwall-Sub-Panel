package traffic

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/pkg/metrics"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

// The detector's fleet-wide knobs (geo_anomaly.fresh_window_seconds,
// shared_exit_min_users, ban_max_per_poll, lift_max_per_poll,
// infra_host_ttl_minutes) used to be constants. Each test here proves the
// knob is READ where it acts: a setting nobody consults is a claim, not a
// feature. PollOnce resolves them once per poll into the policy cache, the
// same cache Phase 1b and Phase 4 already share.

// withRuntime is a policy cache for users whose fleet-wide runtime is
// built from s, as PollOnce builds it from the poll's global settings.
func withRuntime(svc *Service, users []*domain.User, s domain.GeoRuntimeSettings) *geoPolicyCache {
	pc := svc.newGeoPolicyCache(context.Background(), users)
	pc.rt = domain.GeoRuntimeFromSettings(s)
	return pc
}

// A sighting 200 s behind its node's newest scan: memory under the shipped
// 120 s window, live under a configured 300 s one.
func TestObserveLiveIPs_FreshWindowComesFromThePolicyCache(t *testing.T) {
	users := []*domain.User{{ID: 7}}
	in := func(pc *geoPolicyCache) liveIPInput {
		return liveIPInput{
			users:    users,
			clients:  []*domain.PSPClient{client(7, 1, "u7@x")},
			panelIDs: panelsOf(1),
			read: detailRead(map[string][]domain.LiveIPSighting{
				"u7@x": {seen("1.1.1.1", 1000), seen("2.2.2.2", 800)},
			}),
			policies: pc,
		}
	}

	metrics.Reset()
	store := &upsertStreaks{}
	s := newObserver(twoCountries(), store, domain.DefaultGeoPolicy())
	s.observeLiveIPs(context.Background(), in(withRuntime(s, users, domain.GeoRuntimeSettings{FreshWindowSeconds: 300})))
	if got := store.data[7].Concurrent; got != 2 {
		t.Fatalf("window 300: Concurrent = %d, want 2 — the configured window was not used", got)
	}

	metrics.Reset()
	store = &upsertStreaks{}
	s = newObserver(twoCountries(), store, domain.DefaultGeoPolicy())
	s.observeLiveIPs(context.Background(), in(nil))
	if got := store.data[7].Concurrent; got != 1 {
		t.Fatalf("default window: Concurrent = %d, want 1 — 200 s behind is memory under 120 s", got)
	}
}

// Two accounts on one exit are a household under the shipped threshold of
// three, and a shared exit when the fleet sets it to two.
func TestObserveLiveIPs_SharedExitThresholdComesFromThePolicyCache(t *testing.T) {
	users := []*domain.User{{ID: 7}, {ID: 8}}
	geo := &stubGeo{available: true, places: map[string]domain.GeoLocation{
		"1.1.1.7": at("JP"), "1.1.1.8": at("JP"), "2.2.2.2": at("DE"),
	}}
	in := func(pc *geoPolicyCache) liveIPInput {
		return liveIPInput{
			users:    users,
			clients:  []*domain.PSPClient{client(7, 1, "u7@x"), client(8, 1, "u8@x")},
			panelIDs: panelsOf(1),
			read: plainRead(map[string][]string{
				"u7@x": {"1.1.1.7", "2.2.2.2"},
				"u8@x": {"1.1.1.8", "2.2.2.2"},
			}),
			policies: pc,
		}
	}

	metrics.Reset()
	store := &upsertStreaks{}
	s := newObserver(geo, store, flagAt(1))
	s.observeLiveIPs(context.Background(), in(withRuntime(s, users, domain.GeoRuntimeSettings{SharedExitMinUsers: 2})))
	for _, uid := range []int64{7, 8} {
		rec := store.data[uid]
		if rec.Excluded != 1 || rec.Evidence.Excluded.Shared != 1 {
			t.Fatalf("threshold 2, user %d: excluded %d (shared %d), want the common exit set aside as shared",
				uid, rec.Excluded, rec.Evidence.Excluded.Shared)
		}
	}

	metrics.Reset()
	store = &upsertStreaks{}
	s = newObserver(geo, store, flagAt(1))
	s.observeLiveIPs(context.Background(), in(nil))
	for _, uid := range []int64{7, 8} {
		if got := store.data[uid].Excluded; got != 0 {
			t.Fatalf("default threshold, user %d: excluded %d, want 0 — two accounts are a household", uid, got)
		}
	}
}

// Three suspensions due, a configured cap of two: the two lowest IDs are
// returned, the third deferred with its streak re-armed.
func TestCollectGeoBans_CapComesFromThePolicyCache(t *testing.T) {
	metrics.Reset()
	users := []*domain.User{{ID: 1, Enabled: true}, {ID: 2, Enabled: true}, {ID: 3, Enabled: true}}
	s := newObserver(nil, nil, domain.DefaultGeoPolicy())
	pc := withRuntime(s, users, domain.GeoRuntimeSettings{BanMaxPerPoll: 2})
	next := map[int64]domain.GeoRecord{1: {UserID: 1}, 2: {UserID: 2}, 3: {UserID: 3}}

	bans := collectGeoBans([]geoBan{ban(3), ban(1), ban(2)}, users, next, pc, time.Now())

	if len(bans) != 2 || bans[0].UserID != 1 || bans[1].UserID != 2 {
		t.Fatalf("bans = %+v, want users 1 and 2 (cap 2, lowest IDs first)", bans)
	}
	if got := geoOutcome(t, "deferred"); got != 1 {
		t.Fatalf("deferred = %d, want 1", got)
	}
	if got := next[3].Streak.BanOver; got != domain.DefaultGeoPolicy().BanAfterPolls {
		t.Fatalf("user 3: ban streak = %d, want it re-armed at the threshold", got)
	}
}

// Three lifts due, a configured cap of one: the longest-running is lifted,
// the other two wait for the next poll and are counted.
func TestLiftDueGeoSuspensions_CapComesFromThePolicyCache(t *testing.T) {
	metrics.Reset()
	users := &fakeUserRepo{users: map[int64]*domain.User{}}
	for uid := int64(1); uid <= 3; uid++ {
		// user 3 is the oldest suspension
		users.users[uid] = &domain.User{ID: uid, Enabled: true,
			ServiceDisabledReason: domain.DisabledGeoAutoSuspend, ServiceDisabledAt: minutesAgo(60 + int(uid))}
	}
	s, _, _ := newEnforcer(users, nil)
	list := listed(users)

	s.enforceGeo(context.Background(), list, nil, withRuntime(s, list, domain.GeoRuntimeSettings{LiftMaxPerPoll: 1}), time.Now())

	if got := users.users[3].ServiceDisabledReason; got != domain.DisabledNone {
		t.Fatalf("the longest-running suspension: reason = %q, want lifted", got)
	}
	for _, uid := range []int64{1, 2} {
		if got := users.users[uid].ServiceDisabledReason; got != domain.DisabledGeoAutoSuspend {
			t.Fatalf("user %d: reason = %q, want it left for the next poll (cap 1)", uid, got)
		}
	}
	if got := geoOutcome(t, "lift_deferred"); got != 2 {
		t.Fatalf("lift_deferred = %d, want 2", got)
	}
}

// PollOnce is what builds the cache, from the settings the poll loaded: with
// a fleet-wide cap of two, twenty-one due suspensions apply two.
func TestPollOnce_WiresTheGeoRuntimeFromTheSettings(t *testing.T) {
	metrics.Reset()
	users, live, geo := twentyOneSharers()
	g := newGeoPoll(users, live, geo, map[int64]ports.UISettings{3: armed()})
	g.svc.settings.(*fakeScoped).global = ports.UISettings{GeoAnomalyBanMaxPerPoll: 2}

	g.poll(t)

	var held int
	for _, u := range users.users {
		if u.ServiceDisabledReason == domain.DisabledGeoAutoSuspend {
			held++
		}
	}
	if held != 2 {
		t.Fatalf("suspended in one poll = %d, want 2 (the configured cap)", held)
	}
	if got := geoOutcome(t, "deferred"); got != 19 {
		t.Fatalf("deferred = %d, want 19", got)
	}
}

// A relay hostname's answer is reused for the configured TTL, not the
// shipped ten minutes: at one minute, a refresh 61 s later asks again.
func TestRefreshInfraAddresses_UsesTheConfiguredHostTTL(t *testing.T) {
	s, _, res, clk := newInfraFixture(infraNode(1, "edge.example.com"))
	res.answers["edge.example.com"] = []string{"203.0.113.5"}
	s.settings = &fakeScoped{global: ports.UISettings{GeoAnomalyInfraHostTTLMinutes: 1}}

	refreshOK(t, s)
	if res.total() != 1 {
		t.Fatalf("lookups after the first refresh = %d, want 1", res.total())
	}
	clk.advance(61 * time.Second)
	refreshOK(t, s)
	if res.total() != 2 {
		t.Fatalf("lookups 61 s later with a 1-minute TTL = %d, want 2", res.total())
	}
}

// A settings read that fails is not a reason to hammer the resolver: the
// refresh runs on the shipped TTL, and does not fail.
func TestRefreshInfraAddresses_SettingsFailureUsesTheDefaultTTL(t *testing.T) {
	s, _, res, clk := newInfraFixture(infraNode(1, "edge.example.com"))
	res.answers["edge.example.com"] = []string{"203.0.113.5"}
	s.settings = &fakeScoped{global: ports.UISettings{GeoAnomalyInfraHostTTLMinutes: 1}, err: errors.New("db down")}

	refreshOK(t, s)
	clk.advance(61 * time.Second)
	refreshOK(t, s)
	if res.total() != 1 {
		t.Fatalf("lookups = %d, want 1 — an unreadable setting means the 10-minute default", res.total())
	}
	if !s.infra.Contains(mustAddr("203.0.113.5")) {
		t.Fatal("the refresh did not build the set")
	}
}
