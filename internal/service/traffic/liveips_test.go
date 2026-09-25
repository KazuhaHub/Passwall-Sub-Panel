package traffic

import (
	"context"
	"errors"
	"testing"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/pkg/metrics"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

type stubGeo struct {
	places    map[string]domain.GeoLocation
	available bool
	lookups   int
}

func (g *stubGeo) Lookup(_ context.Context, ips []string) map[string]domain.GeoLocation {
	g.lookups++
	out := map[string]domain.GeoLocation{}
	for _, ip := range ips {
		if p, ok := g.places[ip]; ok {
			out[ip] = p
		}
	}
	return out
}

func (g *stubGeo) Available(context.Context) bool { return g.available }

type memStreaks struct {
	data  map[int64]domain.GeoRecord
	saved int
	loadErr,
	saveErr error
}

func (m *memStreaks) Load(context.Context) (map[int64]domain.GeoRecord, error) {
	if m.loadErr != nil {
		return nil, m.loadErr
	}
	return m.data, nil
}

func (m *memStreaks) Save(_ context.Context, s map[int64]domain.GeoRecord) error {
	m.saved++
	if m.saveErr != nil {
		return m.saveErr
	}
	m.data = s
	return nil
}

func client(user, panel int64, email string) *domain.PSPClient {
	return &domain.PSPClient{UserID: user, PanelID: panel, Email: email}
}

func at(cc string) domain.GeoLocation {
	return domain.GeoLocation{CountryCode: cc, Country: cc, City: cc + "-city"}
}

// counterFor reads one labelled value out of the metrics snapshot. Reading
// the SNAPSHOT rather than the counter object is deliberate: it also proves
// the metric is registered and reaches the diagnostics endpoint, which is the
// only way an operator will ever see any of this.
func counterFor(t *testing.T, name string) int64 {
	t.Helper()
	var total int64
	for _, c := range metrics.Take().Counters {
		if len(c.Name) >= len(name) && c.Name[:len(name)] == name {
			total += c.Value
		}
	}
	return total
}

func newObserver(geo *stubGeo, store GeoStreakStore, p domain.GeoAnomalyPolicy) *Service {
	s := &Service{}
	if geo != nil {
		s.SetGeoResolver(geo)
	}
	if store != nil {
		s.SetGeoStreakStore(store)
	}
	s.SetGeoPolicy(p)
	return s
}

func panelsOf(ids ...int64) map[int64]struct{} {
	m := map[int64]struct{}{}
	for _, id := range ids {
		m[id] = struct{}{}
	}
	return m
}

// The end-to-end shape: two panels, one user, two countries, sustained long
// enough to flag. If this passes, the read, the fold, the policy and the
// state machine are connected.
func TestObserveLiveIPs_FlagsAfterSustainedSpread(t *testing.T) {
	metrics.Reset()
	geo := &stubGeo{available: true, places: map[string]domain.GeoLocation{
		"1.1.1.1": at("JP"), "2.2.2.2": at("DE"),
	}}
	store := &memStreaks{}
	p := domain.DefaultGeoPolicy()
	p.FlagAfterPolls = 2
	s := newObserver(geo, store, p)

	clients := []*domain.PSPClient{client(7, 1, "u7@x"), client(7, 2, "u7@x")}
	ips := func(pid int64) (map[string][]string, error) {
		switch pid {
		case 1:
			return map[string][]string{"u7@x": {"1.1.1.1"}}, nil
		default:
			return map[string][]string{"u7@x": {"2.2.2.2"}}, nil
		}
	}

	s.observeLiveIPs(context.Background(), nil, clients, ips, panelsOf(1, 2))
	if got := counterFor(t, "psp_geo_verdict_total{state=suspect}"); got != 1 {
		t.Fatalf("first cycle must be suspect, not flagged: %d", got)
	}
	s.observeLiveIPs(context.Background(), nil, clients, ips, panelsOf(1, 2))
	if got := counterFor(t, "psp_geo_verdict_total{state=flagged}"); got != 1 {
		t.Fatalf("second cycle must flag: %d", got)
	}
	if store.saved != 2 {
		t.Fatalf("the streak must be persisted every cycle or hysteresis restarts: saved=%d", store.saved)
	}
}

// The per-user histogram must actually be fed, and it must reach the
// snapshot. A metric declared and never observed is indistinguishable from a
// fleet where nobody is connected.
func TestObserveLiveIPs_FeedsTheLiveIPHistogram(t *testing.T) {
	metrics.Reset()
	s := newObserver(&stubGeo{available: false}, nil, domain.DefaultGeoPolicy())
	s.observeLiveIPs(context.Background(), nil,
		[]*domain.PSPClient{client(7, 1, "u7@x")},
		func(int64) (map[string][]string, error) {
			return map[string][]string{"u7@x": {"1.1.1.1", "2.2.2.2"}}, nil
		}, panelsOf(1))

	var found bool
	for _, h := range metrics.Take().Histograms {
		if h.Name == "psp_user_live_ips" {
			found = true
			if h.Count != 1 || h.Max != 2 {
				t.Fatalf("histogram = count %d max %v, want 1 sample of 2", h.Count, h.Max)
			}
		}
	}
	if !found {
		t.Fatal("psp_user_live_ips never reached the diagnostics snapshot")
	}
}

// A panel that could not be read must be counted as incomplete, not silently
// dropped. This is the metric an operator watches to know whether the numbers
// above understate.
func TestObserveLiveIPs_CountsIncompleteCoverage(t *testing.T) {
	metrics.Reset()
	s := newObserver(&stubGeo{available: true}, nil, domain.DefaultGeoPolicy())
	s.observeLiveIPs(context.Background(), nil,
		[]*domain.PSPClient{client(7, 1, "u7@x"), client(7, 2, "u7@x")},
		func(pid int64) (map[string][]string, error) {
			if pid == 2 {
				return nil, errors.New("unreachable")
			}
			return map[string][]string{"u7@x": {"1.1.1.1"}}, nil
		}, panelsOf(1, 2))

	if got := counterFor(t, "psp_live_ip_users_incomplete_total"); got != 1 {
		t.Fatalf("incomplete users = %d, want 1", got)
	}
}

// Geo unavailable must produce Unknown, never Clean. Clean would read as
// evidence that nobody is sharing.
func TestObserveLiveIPs_GeoOffIsUnknownNotClean(t *testing.T) {
	metrics.Reset()
	s := newObserver(&stubGeo{available: false, places: map[string]domain.GeoLocation{
		"1.1.1.1": at("JP"), "2.2.2.2": at("DE"),
	}}, nil, domain.DefaultGeoPolicy())
	s.observeLiveIPs(context.Background(), nil,
		[]*domain.PSPClient{client(7, 1, "u7@x")},
		func(int64) (map[string][]string, error) {
			return map[string][]string{"u7@x": {"1.1.1.1", "2.2.2.2"}}, nil
		}, panelsOf(1))

	if got := counterFor(t, "psp_geo_verdict_total{state=unknown}"); got != 1 {
		t.Fatalf("unknown = %d, want 1", got)
	}
	if got := counterFor(t, "psp_geo_verdict_total{state=clean}"); got != 0 {
		t.Fatalf("clean = %d, want 0 — a missing database is not a clean bill of health", got)
	}
}

// No geo service wired at all is the same honest Unknown, not a crash and not
// a clean result.
func TestObserveLiveIPs_NoGeoServiceIsUnknown(t *testing.T) {
	metrics.Reset()
	s := newObserver(nil, nil, domain.DefaultGeoPolicy())
	s.observeLiveIPs(context.Background(), nil,
		[]*domain.PSPClient{client(7, 1, "u7@x")},
		func(int64) (map[string][]string, error) {
			return map[string][]string{"u7@x": {"1.1.1.1"}}, nil
		}, panelsOf(1))
	if got := counterFor(t, "psp_geo_verdict_total{state=unknown}"); got != 1 {
		t.Fatalf("unknown = %d, want 1", got)
	}
}

// An unreadable streak store must degrade toward UNDER-reporting: nobody
// reaches the flag threshold. The alternative — judging on state we could not
// read — accuses people on missing evidence.
func TestObserveLiveIPs_StreakLoadFailureUnderReports(t *testing.T) {
	metrics.Reset()
	geo := &stubGeo{available: true, places: map[string]domain.GeoLocation{
		"1.1.1.1": at("JP"), "2.2.2.2": at("DE"),
	}}
	p := domain.DefaultGeoPolicy()
	p.FlagAfterPolls = 2
	store := &memStreaks{
		data:    map[int64]domain.GeoRecord{7: {Streak: domain.GeoStreak{Over: 99, Flagged: true}}},
		loadErr: errors.New("db down"),
	}
	s := newObserver(geo, store, p)
	s.observeLiveIPs(context.Background(), nil,
		[]*domain.PSPClient{client(7, 1, "u7@x")},
		func(int64) (map[string][]string, error) {
			return map[string][]string{"u7@x": {"1.1.1.1", "2.2.2.2"}}, nil
		}, panelsOf(1))

	if got := counterFor(t, "psp_geo_verdict_total{state=flagged}"); got != 0 {
		t.Fatalf("flagged = %d; a failed streak load must not resurrect a flag", got)
	}
	if got := counterFor(t, "psp_geo_verdict_total{state=suspect}"); got != 1 {
		t.Fatalf("suspect = %d, want 1", got)
	}
}

// A client PSP does not own contributes to nobody.
func TestObserveLiveIPs_UnownedClientIsNotAttributed(t *testing.T) {
	metrics.Reset()
	s := newObserver(&stubGeo{available: true}, nil, domain.DefaultGeoPolicy())
	s.observeLiveIPs(context.Background(), nil,
		[]*domain.PSPClient{client(7, 1, "u7@x")},
		func(int64) (map[string][]string, error) {
			return map[string][]string{
				"u7@x":       {"1.1.1.1"},
				"stranger@x": {"9.9.9.9", "8.8.8.8", "7.7.7.7"},
			}, nil
		}, panelsOf(1))

	for _, h := range metrics.Take().Histograms {
		if h.Name == "psp_user_live_ips" && h.Max != 1 {
			t.Fatalf("a stranger's addresses leaked into a user's count: max=%v, want 1", h.Max)
		}
	}
}

// With no clients there is nothing to observe and nothing to query — the poll
// must not pay for a geo lookup on an empty fleet.
func TestObserveLiveIPs_NoClientsDoesNothing(t *testing.T) {
	metrics.Reset()
	geo := &stubGeo{available: true}
	s := newObserver(geo, nil, domain.DefaultGeoPolicy())
	s.observeLiveIPs(context.Background(), nil, nil, func(int64) (map[string][]string, error) {
		t.Fatal("panel data must not be read when there are no clients")
		return nil, nil
	}, panelsOf(1))
	if geo.lookups != 0 {
		t.Fatalf("geo lookups = %d, want 0", geo.lookups)
	}
}

// Whether POLLONCE actually runs the observation.
//
// Every test above proves observeLiveIPs works and says nothing about the
// poll calling it. Deleting the call from PollOnce compiles and leaves the
// whole package green — verified by mutation — so this drives the real entry
// point instead. That is the fourth time in this area a correct function has
// been covered while its only caller stayed optional.
func TestPollOnce_RunsTheConcurrentLocationObservation(t *testing.T) {
	metrics.Reset()
	users := &fakeUserRepo{users: map[int64]*domain.User{
		1: {ID: 1, Enabled: true},
	}}
	psp := &fakePSPClientRepo{byUser: map[int64][]*domain.PSPClient{
		1: {{ID: 1, UserID: 1, PanelID: 10, Email: "u1@psp.local"}},
	}}
	base := &fakeXUIClient{
		inbounds: []ports.Inbound{{ID: 20, ClientStats: []ports.ClientTraffic{
			{Email: "u1@psp.local", Up: 1, Down: 1},
		}}},
		liveIPs: map[string][]string{"u1@psp.local": {"1.1.1.1", "2.2.2.2"}},
	}
	pool := &fakeXUIPool{clients: map[int64]ports.XUIClient{
		10: &liveIPReaderFake{fakeXUIClient: base},
	}}

	svc := New(users, &fakeOwnershipRepo{byUser: map[int64][]*domain.XUIClientEntry{}},
		&fakeTrafficRepo{}, nil, nil, pool, &fakeDisabler{})
	svc.SetPSPClientRepo(psp)
	svc.SetGeoResolver(&stubGeo{available: true, places: map[string]domain.GeoLocation{
		"1.1.1.1": at("JP"), "2.2.2.2": at("DE"),
	}})
	p := domain.DefaultGeoPolicy()
	p.FlagAfterPolls = 1
	svc.SetGeoPolicy(p)

	if err := svc.PollOnce(context.Background()); err != nil {
		t.Fatalf("PollOnce: %v", err)
	}

	if base.liveCalls != 1 {
		t.Fatalf("the poll read live IPs %d times, want exactly 1 (it must ride the panel slot it already holds)", base.liveCalls)
	}
	if got := counterFor(t, "psp_geo_verdict_total{state=flagged}"); got != 1 {
		t.Fatalf("flagged = %d, want 1 — PollOnce did not run the observation", got)
	}
	var sampled bool
	for _, h := range metrics.Take().Histograms {
		if h.Name == "psp_user_live_ips" && h.Count == 1 && h.Max == 2 {
			sampled = true
		}
	}
	if !sampled {
		t.Fatal("PollOnce did not feed psp_user_live_ips")
	}
}

// An adapter WITHOUT the capability (S-UI's real shape) must leave the poll
// working and report the user as unread, never as a clean zero.
func TestPollOnce_AdapterWithoutLiveIPsIsUnreadNotZero(t *testing.T) {
	metrics.Reset()
	users := &fakeUserRepo{users: map[int64]*domain.User{1: {ID: 1, Enabled: true}}}
	psp := &fakePSPClientRepo{byUser: map[int64][]*domain.PSPClient{
		1: {{ID: 1, UserID: 1, PanelID: 10, Email: "u1@psp.local"}},
	}}
	pool := &fakeXUIPool{clients: map[int64]ports.XUIClient{
		10: &fakeXUIClient{inbounds: []ports.Inbound{{ID: 20}}},
	}}
	svc := New(users, &fakeOwnershipRepo{byUser: map[int64][]*domain.XUIClientEntry{}},
		&fakeTrafficRepo{}, nil, nil, pool, &fakeDisabler{})
	svc.SetPSPClientRepo(psp)
	svc.SetGeoResolver(&stubGeo{available: true})
	svc.SetGeoPolicy(domain.DefaultGeoPolicy())

	if err := svc.PollOnce(context.Background()); err != nil {
		t.Fatalf("PollOnce: %v", err)
	}
	if got := counterFor(t, "psp_live_ip_users_incomplete_total"); got != 1 {
		t.Fatalf("incomplete = %d, want 1 — an adapter that cannot answer must not read as zero", got)
	}
}

// fakeScoped resolves settings the way ScopedSettings does, and counts the
// resolutions so a test can prove the per-GROUP cache is real rather than a
// comment.
type fakeScoped struct {
	byGroup map[int64]ports.UISettings
	loads   int
	err     error
}

func (f *fakeScoped) Load(context.Context, ports.UISettings) (ports.UISettings, error) {
	return ports.UISettings{}, f.err
}
func (f *fakeScoped) LoadForGroup(_ context.Context, gid int64, _ ports.UISettings) (ports.UISettings, error) {
	f.loads++
	return f.byGroup[gid], f.err
}
func (f *fakeScoped) LoadForUser(_ context.Context, u *domain.User, _ ports.UISettings) (ports.UISettings, error) {
	f.loads++
	if f.err != nil {
		return ports.UISettings{}, f.err
	}
	var gid int64
	if u != nil {
		gid = u.GroupID
	}
	return f.byGroup[gid], nil
}

// Whether a STORED policy actually reaches the judgement. Every test of
// GeoPolicyFromSettings proves the conversion; none of them prove the poll
// consults it, and a knob that is never read is a claim rather than a feature.
func TestObserveLiveIPs_StoredPolicyChangesTheVerdict(t *testing.T) {
	geo := &stubGeo{available: true, places: map[string]domain.GeoLocation{
		"1.1.1.1": at("JP"), "2.2.2.2": at("DE"),
	}}
	users := []*domain.User{{ID: 7, GroupID: 3}}
	clients := []*domain.PSPClient{client(7, 1, "u7@x")}
	ips := func(int64) (map[string][]string, error) {
		return map[string][]string{"u7@x": {"1.1.1.1", "2.2.2.2"}}, nil
	}

	// Tolerance 1, flag immediately: two countries is over.
	metrics.Reset()
	s := newObserver(geo, nil, domain.DefaultGeoPolicy())
	s.settings = &fakeScoped{byGroup: map[int64]ports.UISettings{
		3: {GeoAnomalyMaxPlaces: 1, GeoAnomalyFlagAfterPolls: 1},
	}}
	s.observeLiveIPs(context.Background(), users, clients, ips, panelsOf(1))
	if got := counterFor(t, "psp_geo_verdict_total{state=flagged}"); got != 1 {
		t.Fatalf("with tolerance 1 the user must flag: %d", got)
	}

	// Same input, tolerance raised to 2: the SAME user is now clean. If the
	// stored value were ignored this would still flag.
	metrics.Reset()
	s = newObserver(geo, nil, domain.DefaultGeoPolicy())
	s.settings = &fakeScoped{byGroup: map[int64]ports.UISettings{
		3: {GeoAnomalyMaxPlaces: 2, GeoAnomalyFlagAfterPolls: 1},
	}}
	s.observeLiveIPs(context.Background(), users, clients, ips, panelsOf(1))
	if got := counterFor(t, "psp_geo_verdict_total{state=clean}"); got != 1 {
		t.Fatalf("raising the stored tolerance must clear the same user: clean=%d flagged=%d",
			got, counterFor(t, "psp_geo_verdict_total{state=flagged}"))
	}
}

// A group set to allow-anywhere exempts its members without loosening anyone
// else's detection.
func TestObserveLiveIPs_GroupExemptionAppliesPerGroup(t *testing.T) {
	metrics.Reset()
	geo := &stubGeo{available: true, places: map[string]domain.GeoLocation{
		"1.1.1.1": at("JP"), "2.2.2.2": at("DE"),
	}}
	s := newObserver(geo, nil, domain.DefaultGeoPolicy())
	s.settings = &fakeScoped{byGroup: map[int64]ports.UISettings{
		3: {GeoAnomalyAllowAnywhere: true}, // travelling staff
		4: {GeoAnomalyFlagAfterPolls: 1},   // everyone else
	}}
	s.observeLiveIPs(context.Background(),
		[]*domain.User{{ID: 7, GroupID: 3}, {ID: 8, GroupID: 4}},
		[]*domain.PSPClient{client(7, 1, "u7@x"), client(8, 1, "u8@x")},
		func(int64) (map[string][]string, error) {
			return map[string][]string{
				"u7@x": {"1.1.1.1", "2.2.2.2"},
				"u8@x": {"1.1.1.1", "2.2.2.2"},
			}, nil
		}, panelsOf(1))

	if got := counterFor(t, "psp_geo_verdict_total{state=exempt}"); got != 1 {
		t.Fatalf("exempt = %d, want 1 (the travelling group)", got)
	}
	if got := counterFor(t, "psp_geo_verdict_total{state=flagged}"); got != 1 {
		t.Fatalf("flagged = %d, want 1 — one group's exemption must not cover another", got)
	}
}

// The policy is resolved once per GROUP, not once per user: within a group
// the answer cannot differ, and a per-user round trip would cost one settings
// read per user per poll.
func TestObserveLiveIPs_PolicyIsResolvedOncePerGroup(t *testing.T) {
	metrics.Reset()
	sc := &fakeScoped{byGroup: map[int64]ports.UISettings{3: {GeoAnomalyMaxPlaces: 2}}}
	s := newObserver(&stubGeo{available: true}, nil, domain.DefaultGeoPolicy())
	s.settings = sc

	var users []*domain.User
	var clients []*domain.PSPClient
	live := map[string][]string{}
	for i := int64(1); i <= 5; i++ {
		users = append(users, &domain.User{ID: i, GroupID: 3})
		email := "u" + string(rune('0'+i)) + "@x"
		clients = append(clients, client(i, 1, email))
		live[email] = []string{"1.1.1.1"}
	}
	s.observeLiveIPs(context.Background(), users, clients,
		func(int64) (map[string][]string, error) { return live, nil }, panelsOf(1))

	if sc.loads != 1 {
		t.Fatalf("settings resolved %d times for 5 users in ONE group, want 1", sc.loads)
	}
}

// A settings read that fails must fall back to the deployment default, not to
// a zero policy — a zero MaxPlaces flags every connected user, including one
// sitting at home.
func TestObserveLiveIPs_SettingsFailureFallsBackToTheDefault(t *testing.T) {
	metrics.Reset()
	s := newObserver(&stubGeo{available: true, places: map[string]domain.GeoLocation{
		"1.1.1.1": at("JP"),
	}}, nil, domain.DefaultGeoPolicy())
	s.settings = &fakeScoped{err: errors.New("settings unavailable")}
	s.observeLiveIPs(context.Background(),
		[]*domain.User{{ID: 7, GroupID: 3}},
		[]*domain.PSPClient{client(7, 1, "u7@x")},
		func(int64) (map[string][]string, error) {
			return map[string][]string{"u7@x": {"1.1.1.1"}}, nil
		}, panelsOf(1))

	if got := counterFor(t, "psp_geo_verdict_total{state=clean}"); got != 1 {
		t.Fatalf("clean = %d; a failed settings read must not flag a user in ONE place", got)
	}
}

// And the fallback must be the DEFAULT policy, not a zero one.
//
// A zero policy is not inert: EvaluateGeo sanitises it to MaxPlaces 1 and
// FlagAfterPolls 1, which is STRICTER than the shipped default's 3 — so a
// settings outage would turn the detector trigger-happy and flag people on a
// single sample. Found by mutation: the previous test used one location and
// could not tell the two apart.
func TestObserveLiveIPs_SettingsFailureKeepsTheDefaultHysteresis(t *testing.T) {
	metrics.Reset()
	s := newObserver(&stubGeo{available: true, places: map[string]domain.GeoLocation{
		"1.1.1.1": at("JP"), "2.2.2.2": at("DE"),
	}}, nil, domain.DefaultGeoPolicy())
	s.settings = &fakeScoped{err: errors.New("settings unavailable")}
	s.observeLiveIPs(context.Background(),
		[]*domain.User{{ID: 7, GroupID: 3}},
		[]*domain.PSPClient{client(7, 1, "u7@x")},
		func(int64) (map[string][]string, error) {
			return map[string][]string{"u7@x": {"1.1.1.1", "2.2.2.2"}}, nil
		}, panelsOf(1))

	if got := counterFor(t, "psp_geo_verdict_total{state=flagged}"); got != 0 {
		t.Fatalf("flagged = %d on the FIRST sample; the default needs %d consecutive checks, "+
			"so a settings outage must not fall back to a stricter policy",
			got, domain.DefaultGeoPolicy().FlagAfterPolls)
	}
	if got := counterFor(t, "psp_geo_verdict_total{state=suspect}"); got != 1 {
		t.Fatalf("suspect = %d, want 1 — the ramp must still be visible", got)
	}
}

// The poll must persist the VERDICT, not only the counters.
//
// The counters answer "is this account flagged"; an operator deciding whether
// to act needs why. And the two must come from the same cycle — storing them
// separately would let a reason describe a state that is no longer current,
// with nothing to signal the mismatch.
func TestObserveLiveIPs_PersistsTheVerdictWithItsStreak(t *testing.T) {
	metrics.Reset()
	store := &memStreaks{}
	p := domain.DefaultGeoPolicy()
	p.FlagAfterPolls = 1
	s := newObserver(&stubGeo{available: true, places: map[string]domain.GeoLocation{
		"1.1.1.1": at("JP"), "2.2.2.2": at("DE"),
	}}, store, p)

	s.observeLiveIPs(context.Background(), nil,
		[]*domain.PSPClient{client(7, 1, "u7@x")},
		func(int64) (map[string][]string, error) {
			return map[string][]string{"u7@x": {"1.1.1.1", "2.2.2.2"}}, nil
		}, panelsOf(1))

	rec, ok := store.data[7]
	if !ok {
		t.Fatal("nothing was persisted for the judged user")
	}
	if rec.State != domain.GeoStateFlagged {
		t.Fatalf("state = %q, want flagged", rec.State)
	}
	if !rec.Streak.Flagged {
		t.Fatal("the state and the streak disagree; they must come from one evaluation")
	}
	if rec.Reason == "" {
		t.Fatal("a flag with no reason is not a basis for acting on an account")
	}
	if rec.LiveIPs != 2 {
		t.Fatalf("liveIPs = %d, want 2", rec.LiveIPs)
	}
	if len(rec.Places) != 2 {
		t.Fatalf("places = %v, want the two countries", rec.Places)
	}
}

// A count that is a floor must be persisted as one. This is the field a
// reader is most likely to skip, and skipping it turns a partial count into a
// clean bill of health.
func TestObserveLiveIPs_PersistsThatACountWasOnlyAFloor(t *testing.T) {
	metrics.Reset()
	store := &memStreaks{}
	s := newObserver(&stubGeo{available: true}, store, domain.DefaultGeoPolicy())
	s.observeLiveIPs(context.Background(), nil,
		[]*domain.PSPClient{client(7, 1, "u7@x"), client(7, 2, "u7@x")},
		func(pid int64) (map[string][]string, error) {
			if pid == 2 {
				return nil, errors.New("unreachable")
			}
			return map[string][]string{"u7@x": {"1.1.1.1"}}, nil
		}, panelsOf(1, 2))

	if store.data[7].Complete {
		t.Fatal("a panel could not be read, so the count is a floor — persisting it as complete hides that")
	}
}

// placeIn places an address in one region of a country, with a city: the
// shape a city-granular database returns.
func placeIn(cc, region, city string) domain.GeoLocation {
	return domain.GeoLocation{CountryCode: cc, Country: cc, Region: region, City: city}
}

// Whether the stored CITY tolerance reaches the judgement. Four cities in one
// region of one country is over the default city tolerance of 2 and within
// every coarser tier, so the city tolerance alone decides it: a group that
// raised it to 5 must see the same user clean. If the poll dropped the
// stored value, the group editor would show a tolerance nothing judges with.
func TestObserveLiveIPs_StoredCityToleranceChangesTheVerdict(t *testing.T) {
	geo := &stubGeo{available: true, places: map[string]domain.GeoLocation{
		"1.1.1.1": placeIn("JP", "Kanto", "Tokyo"),
		"1.1.1.2": placeIn("JP", "Kanto", "Yokohama"),
		"1.1.1.3": placeIn("JP", "Kanto", "Chiba"),
		"1.1.1.4": placeIn("JP", "Kanto", "Saitama"),
	}}
	users := []*domain.User{{ID: 7, GroupID: 3}}
	clients := []*domain.PSPClient{client(7, 1, "u7@x")}
	ips := func(int64) (map[string][]string, error) {
		return map[string][]string{"u7@x": {"1.1.1.1", "1.1.1.2", "1.1.1.3", "1.1.1.4"}}, nil
	}

	// Nothing stored: the shipped city tolerance (2) is exceeded.
	metrics.Reset()
	s := newObserver(geo, nil, domain.DefaultGeoPolicy())
	s.settings = &fakeScoped{byGroup: map[int64]ports.UISettings{3: {}}}
	s.observeLiveIPs(context.Background(), users, clients, ips, panelsOf(1))
	if got := counterFor(t, "psp_geo_verdict_total{state=suspect}"); got != 1 {
		t.Fatalf("four cities at once must be over the default city tolerance: suspect=%d clean=%d",
			got, counterFor(t, "psp_geo_verdict_total{state=clean}"))
	}

	// The group raised the city tolerance: the SAME user is within it.
	metrics.Reset()
	s = newObserver(geo, nil, domain.DefaultGeoPolicy())
	s.settings = &fakeScoped{byGroup: map[int64]ports.UISettings{3: {GeoAnomalyMaxCities: 5}}}
	s.observeLiveIPs(context.Background(), users, clients, ips, panelsOf(1))
	if got := counterFor(t, "psp_geo_verdict_total{state=clean}"); got != 1 {
		t.Fatalf("a stored city tolerance of 5 must clear four cities: clean=%d suspect=%d",
			got, counterFor(t, "psp_geo_verdict_total{state=suspect}"))
	}
}

// The same for the REGION tolerance: two provinces of one country (and only
// two cities, within the default city tolerance) is over the default region
// tolerance of 1, and a group that raised it to 2 must see the user clean.
func TestObserveLiveIPs_StoredRegionToleranceChangesTheVerdict(t *testing.T) {
	geo := &stubGeo{available: true, places: map[string]domain.GeoLocation{
		"1.1.1.1": placeIn("JP", "Kanto", "Tokyo"),
		"1.1.1.2": placeIn("JP", "Kansai", "Osaka"),
	}}
	users := []*domain.User{{ID: 7, GroupID: 3}}
	clients := []*domain.PSPClient{client(7, 1, "u7@x")}
	ips := func(int64) (map[string][]string, error) {
		return map[string][]string{"u7@x": {"1.1.1.1", "1.1.1.2"}}, nil
	}

	metrics.Reset()
	s := newObserver(geo, nil, domain.DefaultGeoPolicy())
	s.settings = &fakeScoped{byGroup: map[int64]ports.UISettings{3: {}}}
	s.observeLiveIPs(context.Background(), users, clients, ips, panelsOf(1))
	if got := counterFor(t, "psp_geo_verdict_total{state=suspect}"); got != 1 {
		t.Fatalf("two regions at once must be over the default region tolerance: suspect=%d clean=%d",
			got, counterFor(t, "psp_geo_verdict_total{state=clean}"))
	}

	metrics.Reset()
	s = newObserver(geo, nil, domain.DefaultGeoPolicy())
	s.settings = &fakeScoped{byGroup: map[int64]ports.UISettings{3: {GeoAnomalyMaxRegions: 2}}}
	s.observeLiveIPs(context.Background(), users, clients, ips, panelsOf(1))
	if got := counterFor(t, "psp_geo_verdict_total{state=clean}"); got != 1 {
		t.Fatalf("a stored region tolerance of 2 must clear two regions: clean=%d suspect=%d",
			got, counterFor(t, "psp_geo_verdict_total{state=suspect}"))
	}
}

// Whether the stored AUTOMATIC-SUSPENSION settings reach the policy. The
// suspension streak is the observable: it only counts when suspension is
// armed, and only for samples over the suspension tolerances. Three groups,
// the same two-country spread each (distinct addresses per user, so no exit
// is shared):
//   - armed with the default tolerances: 2 countries is over 1, streak 1;
//   - armed, country tolerance 2: within it, streak 0;
//   - nothing stored: suspension is off by default, streak 0.
//
// If the poll dropped ban_enabled the first would read 0; if it dropped
// ban_max_countries the second would read 1.
func TestObserveLiveIPs_StoredBanSettingsReachThePolicy(t *testing.T) {
	metrics.Reset()
	geo := &stubGeo{available: true, places: map[string]domain.GeoLocation{
		"1.1.1.7": at("JP"), "2.2.2.7": at("DE"),
		"1.1.1.8": at("JP"), "2.2.2.8": at("DE"),
		"1.1.1.9": at("JP"), "2.2.2.9": at("DE"),
	}}
	store := &memStreaks{}
	s := newObserver(geo, store, domain.DefaultGeoPolicy())
	s.settings = &fakeScoped{byGroup: map[int64]ports.UISettings{
		3: {GeoAnomalyBanEnabled: true},
		4: {GeoAnomalyBanEnabled: true, GeoAnomalyBanMaxCountries: 2},
		5: {},
	}}
	s.observeLiveIPs(context.Background(),
		[]*domain.User{{ID: 7, GroupID: 3}, {ID: 8, GroupID: 4}, {ID: 9, GroupID: 5}},
		[]*domain.PSPClient{client(7, 1, "u7@x"), client(8, 1, "u8@x"), client(9, 1, "u9@x")},
		func(int64) (map[string][]string, error) {
			return map[string][]string{
				"u7@x": {"1.1.1.7", "2.2.2.7"},
				"u8@x": {"1.1.1.8", "2.2.2.8"},
				"u9@x": {"1.1.1.9", "2.2.2.9"},
			}, nil
		}, panelsOf(1))

	for uid, want := range map[int64]int{7: 1, 8: 0, 9: 0} {
		rec, ok := store.data[uid]
		if !ok {
			t.Fatalf("nothing persisted for user %d", uid)
		}
		if rec.Streak.BanOver != want {
			t.Errorf("user %d: saved suspension streak = %d, want %d", uid, rec.Streak.BanOver, want)
		}
	}
}
