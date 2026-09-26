package traffic

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/pkg/metrics"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

// fakeGeoSuspender is user.Service's two conditional writes over the fake
// user repository: the same CAS predicates, and the lift's due check made on
// a FRESH read. Built on the repo rather than on a call log so a test that
// passes against it has proved what the row ends up holding, not only that a
// method was called.
type fakeGeoSuspender struct {
	repo ports.UserRepo
	// errFor fails that user's call. With appliedWithErr the write still
	// happens and the error rides alongside, which is user.Service's shape
	// when the push failed and could not even be queued.
	errFor         map[int64]error
	appliedWithErr map[int64]bool

	suspendCalls []int64
	liftCalls    []int64
	cutoffs      map[int64]time.Time

	// afterWrite runs once a write has committed, before the call returns:
	// the moment a caller's cancellation (a closed "Poll now" tab, a
	// shutdown) lands in the middle of a transition.
	afterWrite func()
}

// The writes run on the caller's context, as user.Service's do: a context
// already cancelled fails the call before anything is written.
func (f *fakeGeoSuspender) SuspendServiceIfClear(ctx context.Context, userID int64, reason domain.AutoDisabledReason, detail string) (bool, error) {
	f.suspendCalls = append(f.suspendCalls, userID)
	if err := ctx.Err(); err != nil {
		return false, err
	}
	if err := f.errFor[userID]; err != nil && !f.appliedWithErr[userID] {
		return false, err
	}
	applied, err := f.repo.SetServiceStateIfClear(ctx, userID, reason, detail, time.Now())
	if applied && f.afterWrite != nil {
		f.afterWrite()
	}
	if err == nil {
		err = f.errFor[userID]
	}
	return applied, err
}

func (f *fakeGeoSuspender) LiftServiceIfHeldSince(ctx context.Context, userID int64, reason domain.AutoDisabledReason, cutoff time.Time) (bool, error) {
	f.liftCalls = append(f.liftCalls, userID)
	if f.cutoffs == nil {
		f.cutoffs = map[int64]time.Time{}
	}
	f.cutoffs[userID] = cutoff
	if err := ctx.Err(); err != nil {
		return false, err
	}
	if err := f.errFor[userID]; err != nil && !f.appliedWithErr[userID] {
		return false, err
	}
	u, err := f.repo.GetByID(ctx, userID)
	if err != nil {
		return false, err
	}
	if u.ServiceDisabledReason != reason || (u.ServiceDisabledAt != nil && u.ServiceDisabledAt.After(cutoff)) {
		return false, nil
	}
	lifted, err := f.repo.ClearServiceStateIfReason(ctx, userID, reason)
	if lifted && f.afterWrite != nil {
		f.afterWrite()
	}
	if err == nil {
		err = f.errFor[userID]
	}
	return lifted, err
}

type fakeAuditRepo struct {
	ports.AuditRepo
	entries []domain.AuditEntry
}

// Insert fails on a cancelled context, as the database-backed log does.
func (a *fakeAuditRepo) Insert(ctx context.Context, e *domain.AuditEntry) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	a.entries = append(a.entries, *e)
	return nil
}

// geoOutcome reads one outcome of psp_geo_auto_suspension_total.
func geoOutcome(t *testing.T, outcome string) int64 {
	t.Helper()
	return counterFor(t, "psp_geo_auto_suspension_total{outcome="+outcome+"}")
}

// armed is a group that turned automatic suspension on and made a single
// over-sample enough, so one poll can carry a ban end to end.
func armed() ports.UISettings {
	return ports.UISettings{GeoAnomalyBanEnabled: true, GeoAnomalyFlagAfterPolls: 1, GeoAnomalyBanAfterPolls: 1}
}

func emailOf(uid int64) string { return fmt.Sprintf("u%d@psp.local", uid) }

// geoPoll is a whole PollOnce over one panel whose live-IP reader answers
// live: every user in users gets one shared client on it, every group's
// settings come from byGroup, and the global load is empty (so the sample
// spacing is off and repeated polls all count).
type geoPoll struct {
	svc   *Service
	users *fakeUserRepo
	sus   *fakeGeoSuspender
	store *upsertStreaks
	audit *fakeAuditRepo
}

func newGeoPoll(users *fakeUserRepo, live map[string][]string, geo *stubGeo, byGroup map[int64]ports.UISettings) *geoPoll {
	psp := &fakePSPClientRepo{byUser: map[int64][]*domain.PSPClient{}}
	for id := range users.users {
		psp.byUser[id] = []*domain.PSPClient{{ID: id, UserID: id, PanelID: 10, Email: emailOf(id)}}
	}
	pool := &fakeXUIPool{clients: map[int64]ports.XUIClient{
		10: &liveIPReaderFake{fakeXUIClient: &fakeXUIClient{inbounds: []ports.Inbound{{ID: 20}}, liveIPs: live}},
	}}
	g := &geoPoll{users: users, store: &upsertStreaks{}, audit: &fakeAuditRepo{}}
	g.svc = New(users, &fakeOwnershipRepo{byUser: map[int64][]*domain.XUIClientEntry{}},
		&fakeTrafficRepo{}, nil, nil, pool, &fakeDisabler{})
	g.svc.WithSettings(&fakeScoped{byGroup: byGroup})
	g.svc.SetPSPClientRepo(psp)
	g.svc.SetGeoResolver(geo)
	g.svc.SetGeoPolicy(domain.DefaultGeoPolicy())
	g.svc.SetGeoStreakStore(g.store)
	g.sus = &fakeGeoSuspender{repo: users}
	g.svc.SetGeoSuspender(g.sus)
	g.svc.SetAuditRepo(g.audit)
	return g
}

func (g *geoPoll) poll(t *testing.T) {
	t.Helper()
	if err := g.svc.PollOnce(context.Background()); err != nil {
		t.Fatalf("PollOnce: %v", err)
	}
}

// newEnforcer is a bare Service for driving enforceGeo directly: the group
// policies from byGroup, the shipped default as fallback, and the fake
// suspender and audit log wired.
func newEnforcer(users *fakeUserRepo, byGroup map[int64]ports.UISettings) (*Service, *fakeGeoSuspender, *fakeAuditRepo) {
	s := &Service{users: users}
	s.settings = &fakeScoped{byGroup: byGroup}
	s.SetGeoPolicy(domain.DefaultGeoPolicy())
	sus := &fakeGeoSuspender{repo: users}
	s.SetGeoSuspender(sus)
	audit := &fakeAuditRepo{}
	s.SetAuditRepo(audit)
	return s, sus, audit
}

// listed is what listAllUsers hands the poll: copies, so an in-memory
// mutation by the code under test cannot masquerade as a repository write.
func listed(users *fakeUserRepo) []*domain.User {
	out, _, _ := users.List(context.Background(), ports.UserFilter{})
	return out
}

func minutesAgo(n int) *time.Time {
	t := time.Now().Add(-time.Duration(n) * time.Minute)
	return &t
}

func ban(uid int64) geoBan {
	return geoBan{UserID: uid, Tier: domain.GeoTierCountry, Reason: "in 2 countries at once ([DE JP]); tolerance is 1", Spread: 2}
}

// ---------------------------------------------------------------------------
// Applying a ban.
// ---------------------------------------------------------------------------

// D3: observe only unless an admin arms it. A user flagged in two countries
// on every poll, with a group that made one over-sample enough to flag and
// to ban, is still never suspended while ban_enabled is off.
//
// Guard: green on arrival (nothing suspends before this commit either). It
// turns red if the ban collection stops honouring BanDue — for example by
// collecting every Flagged verdict — or if enforcement applies a ban on its
// own judgement.
func TestPollOnce_GeoBanIsOffByDefault(t *testing.T) {
	metrics.Reset()
	users := &fakeUserRepo{users: map[int64]*domain.User{1: {ID: 1, GroupID: 3, Enabled: true}}}
	off := armed()
	off.GeoAnomalyBanEnabled = false
	g := newGeoPoll(users, map[string][]string{emailOf(1): {"1.1.1.1", "2.2.2.2"}}, twoCountries(),
		map[int64]ports.UISettings{3: off})

	for range 3 {
		g.poll(t)
	}
	if got := counterFor(t, "psp_geo_verdict_total{state=flagged}"); got != 3 {
		t.Fatalf("flagged = %d, want 3 (the user must be flagged, or this proves nothing)", got)
	}
	if got := users.users[1].ServiceDisabledReason; got != domain.DisabledNone {
		t.Fatalf("service reason = %q, want none — suspension is off by default", got)
	}
	if len(g.sus.suspendCalls) != 0 {
		t.Fatalf("suspender called for %v with suspension off", g.sus.suspendCalls)
	}
}

// Armed, a ban due in Phase 1b is applied by the same poll: the row carries
// geo_auto, and the detail the user reads (portal, mail) is Chinese, states
// the time box, and names no place — the places stay on the admin side.
func TestPollOnce_GeoBanDueSuspendsWithGeoAuto(t *testing.T) {
	metrics.Reset()
	users := &fakeUserRepo{users: map[int64]*domain.User{1: {ID: 1, GroupID: 3, Enabled: true}}}
	g := newGeoPoll(users, map[string][]string{emailOf(1): {"1.1.1.1", "2.2.2.2"}}, twoCountries(),
		map[int64]ports.UISettings{3: armed()})

	g.poll(t)

	row := users.users[1]
	if row.ServiceDisabledReason != domain.DisabledGeoAutoSuspend {
		t.Fatalf("service reason = %q after a due ban, want geo_auto", row.ServiceDisabledReason)
	}
	if row.ServiceDisabledAt == nil {
		t.Fatal("service_disabled_at not set; the lift has nothing to time from")
	}
	if !strings.Contains(row.ServiceDisableDetail, "2 个国家或地区") || !strings.Contains(row.ServiceDisableDetail, "60 分钟") {
		t.Fatalf("detail = %q, want the country tier's Chinese text with the spread (2) and the 60-minute box", row.ServiceDisableDetail)
	}
	if regexp.MustCompile(`[A-Za-z]`).MatchString(row.ServiceDisableDetail) {
		t.Fatalf("detail = %q names a place (a country code or any Latin text); the user-facing text must not", row.ServiceDisableDetail)
	}
	if got := geoOutcome(t, "suspended"); got != 1 {
		t.Fatalf("suspended = %d, want 1", got)
	}
	if got := g.store.data[1].Streak.BanOver; got != 0 {
		t.Fatalf("saved ban streak = %d, want 0 (consumed by the ban)", got)
	}
}

// A user someone else holds is never re-labelled, even when the detector
// thinks a ban is due: the hold stays, nothing is written, and the skip is
// counted rather than lost.
func TestPollOnce_GeoBanSkipsAUserWithAnotherReason(t *testing.T) {
	metrics.Reset()
	users := &fakeUserRepo{users: map[int64]*domain.User{
		1: {ID: 1, GroupID: 3, Enabled: true, ServiceDisabledReason: domain.DisabledServiceManual, ServiceDisableDetail: "admin"},
	}}
	g := newGeoPoll(users, map[string][]string{emailOf(1): {"1.1.1.1", "2.2.2.2"}}, twoCountries(),
		map[int64]ports.UISettings{3: armed()})

	g.poll(t)

	if got := geoOutcome(t, "skipped_held"); got != 1 {
		t.Fatalf("skipped_held = %d, want 1", got)
	}
	if got := users.users[1]; got.ServiceDisabledReason != domain.DisabledServiceManual || got.ServiceDisableDetail != "admin" {
		t.Fatalf("row = %q/%q, want service_manual/admin untouched", got.ServiceDisabledReason, got.ServiceDisableDetail)
	}
	if len(g.sus.suspendCalls) != 0 {
		t.Fatalf("suspender called for %v; a held user must not even be attempted", g.sus.suspendCalls)
	}
}

// Only a user whose service is actually active is suspended. An expired or
// live-over-quota user would be RE-LABELLED "suspended" (geo_auto wins in
// AccessSnapshot) and their real state hidden; an emergency window is a
// promise of service; an account-disabled user has nothing to suspend. All
// four arrive with no service reason at all, so only the snapshot sees it.
func TestEnforceGeo_NeverSuspendsAUserNotActivelyServed(t *testing.T) {
	metrics.Reset()
	past := time.Now().Add(-time.Hour)
	future := time.Now().Add(time.Hour)
	users := &fakeUserRepo{users: map[int64]*domain.User{
		1: {ID: 1, Enabled: true, ExpireAt: &past},
		2: {ID: 2, Enabled: true, EmergencyUntil: &future},
		3: {ID: 3, Enabled: true, TrafficLimitBytes: 100, LifetimeTotalBytes: 150},
		4: {ID: 4, Enabled: false, AutoDisabledReason: domain.DisabledManual},
		5: {ID: 5, Enabled: true}, // control
	}}
	s, _, _ := newEnforcer(users, nil)

	s.enforceGeo(context.Background(), listed(users), []geoBan{ban(1), ban(2), ban(3), ban(4), ban(5)}, nil, time.Now())

	for uid := int64(1); uid <= 4; uid++ {
		if got := users.users[uid].ServiceDisabledReason; got != domain.DisabledNone {
			t.Errorf("user %d: service reason = %q, want none (not actively served, so never auto-suspended)", uid, got)
		}
	}
	if got := users.users[5].ServiceDisabledReason; got != domain.DisabledGeoAutoSuspend {
		t.Fatalf("control user: service reason = %q, want geo_auto", got)
	}
	if got := geoOutcome(t, "skipped_held"); got != 4 {
		t.Fatalf("skipped_held = %d, want 4", got)
	}
}

// With no suspender wired a ban cannot be applied, and nothing else may
// write geo_auto. It must still be visible: counted, not silently dropped.
func TestEnforceGeo_GeoBanWithoutASuspenderIsCountedNotApplied(t *testing.T) {
	metrics.Reset()
	users := &fakeUserRepo{users: map[int64]*domain.User{1: {ID: 1, Enabled: true}, 2: {ID: 2, Enabled: true}}}
	s := &Service{users: users}

	s.enforceGeo(context.Background(), listed(users), []geoBan{ban(1), ban(2)}, nil, time.Now())

	if got := geoOutcome(t, "skipped_unwired"); got != 2 {
		t.Fatalf("skipped_unwired = %d, want 2", got)
	}
	for uid, u := range users.users {
		if u.ServiceDisabledReason != domain.DisabledNone {
			t.Fatalf("user %d: service reason = %q, want none", uid, u.ServiceDisabledReason)
		}
	}
}

// twentyOneSharers is 21 users, each in Japan and Germany at once on
// addresses of their own — shared addresses would be excluded as a common
// exit and nobody would be over at all.
func twentyOneSharers() (*fakeUserRepo, map[string][]string, *stubGeo) {
	users := &fakeUserRepo{users: map[int64]*domain.User{}}
	live := map[string][]string{}
	geo := &stubGeo{available: true, places: map[string]domain.GeoLocation{}}
	for uid := int64(1); uid <= 21; uid++ {
		users.users[uid] = &domain.User{ID: uid, GroupID: 3, Enabled: true}
		jp, de := fmt.Sprintf("1.1.%d.1", uid), fmt.Sprintf("2.2.%d.2", uid)
		live[emailOf(uid)] = []string{jp, de}
		geo.places[jp] = at("JP")
		geo.places[de] = at("DE")
	}
	return users, live, geo
}

// At most geoMaxSuspensionsPerPoll inline suspensions per poll (each pushes
// to the panels). The overflow is DEFERRED, not lost: its ban streak is put
// back at the threshold, so its next over-sample fires — which the second
// poll shows, once the first twenty are held and no longer compete.
func TestPollOnce_GeoBanOverTheCapIsDeferredNotLost(t *testing.T) {
	metrics.Reset()
	users, live, geo := twentyOneSharers()
	g := newGeoPoll(users, live, geo, map[int64]ports.UISettings{3: armed()})

	g.poll(t)

	var held int
	for uid := int64(1); uid <= 20; uid++ {
		if users.users[uid].ServiceDisabledReason == domain.DisabledGeoAutoSuspend {
			held++
		}
	}
	if held != 20 {
		t.Fatalf("suspended in the first poll = %d of users 1..20, want 20 (lowest IDs first)", held)
	}
	if got := users.users[21].ServiceDisabledReason; got != domain.DisabledNone {
		t.Fatalf("user 21: service reason = %q after the first poll, want none (over the cap)", got)
	}
	if got := g.store.data[21].Streak.BanOver; got != 1 {
		t.Fatalf("user 21: saved ban streak = %d, want 1 (= ban_after_polls, so the next over-sample fires)", got)
	}
	if got := geoOutcome(t, "deferred"); got != 1 {
		t.Fatalf("deferred = %d, want 1", got)
	}

	g.poll(t)
	if got := users.users[21].ServiceDisabledReason; got != domain.DisabledGeoAutoSuspend {
		t.Fatalf("user 21: service reason = %q after the second poll, want geo_auto (deferred, not lost)", got)
	}
}

// A ban that cannot be applied (the user is held already) is consumed
// before the cap is counted, so twenty held users cannot push an eligible
// one out of this poll.
func TestObserveLiveIPs_IneligibleBansDoNotUseCapSlots(t *testing.T) {
	metrics.Reset()
	users, live, geo := twentyOneSharers()
	var list []*domain.User
	for uid := int64(1); uid <= 21; uid++ {
		u := users.users[uid]
		if uid <= 20 {
			u.ServiceDisabledReason = domain.DisabledServiceManual
		}
		list = append(list, u)
	}
	var clients []*domain.PSPClient
	for uid := int64(1); uid <= 21; uid++ {
		clients = append(clients, client(uid, 1, emailOf(uid)))
	}
	store := &memStreaks{}
	s := newObserver(geo, store, domain.DefaultGeoPolicy())
	s.settings = &fakeScoped{byGroup: map[int64]ports.UISettings{3: armed()}}

	bans := s.observeLiveIPs(context.Background(), liveIPInput{
		users: list, clients: clients, panelIDs: panelsOf(1), read: plainRead(live),
	})

	if len(bans) != 1 || bans[0].UserID != 21 {
		t.Fatalf("bans = %+v, want exactly user 21", bans)
	}
	if bans[0].Tier != domain.GeoTierCountry || bans[0].Spread != 2 || !strings.Contains(bans[0].Reason, "2 countries") {
		t.Fatalf("ban = %+v, want the country tier, spread 2 and a reason naming the countries", bans[0])
	}
	if got := geoOutcome(t, "skipped_held"); got != 20 {
		t.Fatalf("skipped_held = %d, want 20", got)
	}
	if got := geoOutcome(t, "deferred"); got != 0 {
		t.Fatalf("deferred = %d, want 0", got)
	}
	if got := store.data[5].Streak.BanOver; got != 0 {
		t.Fatalf("a held user's saved ban streak = %d, want 0 (consumed, not deferred)", got)
	}
}

// ---------------------------------------------------------------------------
// Lifting.
// ---------------------------------------------------------------------------

// The lift is time-based: once the effective duration (60 minutes by
// default) has passed, the suspension ends by itself.
func TestEnforceGeo_GeoAutoLiftsAfterTheDuration(t *testing.T) {
	metrics.Reset()
	users := &fakeUserRepo{users: map[int64]*domain.User{
		1: {ID: 1, Enabled: true, ServiceDisabledReason: domain.DisabledGeoAutoSuspend, ServiceDisableDetail: "d", ServiceDisabledAt: minutesAgo(61)},
	}}
	s, _, _ := newEnforcer(users, nil)

	s.enforceGeo(context.Background(), listed(users), nil, nil, time.Now())

	if got := users.users[1]; got.ServiceDisabledReason != domain.DisabledNone || got.ServiceDisabledAt != nil {
		t.Fatalf("row = %q/%v after 61 of 60 minutes, want lifted", got.ServiceDisabledReason, got.ServiceDisabledAt)
	}
	if got := geoOutcome(t, "lifted_expiry"); got != 1 {
		t.Fatalf("lifted_expiry = %d, want 1", got)
	}
}

// Not a minute early. The suspender is not even asked about a suspension
// that is not due; the due one beside it is the control.
func TestEnforceGeo_GeoAutoDoesNotLiftBeforeTheDuration(t *testing.T) {
	metrics.Reset()
	users := &fakeUserRepo{users: map[int64]*domain.User{
		1: {ID: 1, Enabled: true, ServiceDisabledReason: domain.DisabledGeoAutoSuspend, ServiceDisabledAt: minutesAgo(59)},
		2: {ID: 2, Enabled: true, ServiceDisabledReason: domain.DisabledGeoAutoSuspend, ServiceDisabledAt: minutesAgo(61)},
	}}
	s, sus, _ := newEnforcer(users, nil)

	s.enforceGeo(context.Background(), listed(users), nil, nil, time.Now())

	if got := users.users[1].ServiceDisabledReason; got != domain.DisabledGeoAutoSuspend {
		t.Fatalf("user 1 (59 of 60 minutes): reason = %q, want geo_auto still", got)
	}
	if got := users.users[2].ServiceDisabledReason; got != domain.DisabledNone {
		t.Fatalf("user 2 (61 of 60 minutes, control): reason = %q, want lifted", got)
	}
	if len(sus.liftCalls) != 1 || sus.liftCalls[0] != 2 {
		t.Fatalf("lift calls = %v, want only the due user 2", sus.liftCalls)
	}
}

// geo_anomaly is a person's decision; only a person lifts it. However old.
func TestEnforceGeo_LiftNeverTouchesGeoAnomaly(t *testing.T) {
	metrics.Reset()
	users := &fakeUserRepo{users: map[int64]*domain.User{
		1: {ID: 1, Enabled: true, ServiceDisabledReason: domain.DisabledGeoAnomaly, ServiceDisabledAt: minutesAgo(60 * 24 * 30)},
		2: {ID: 2, Enabled: true, ServiceDisabledReason: domain.DisabledGeoAutoSuspend, ServiceDisabledAt: minutesAgo(120)},
	}}
	s, sus, _ := newEnforcer(users, nil)

	s.enforceGeo(context.Background(), listed(users), nil, nil, time.Now())

	if got := users.users[1].ServiceDisabledReason; got != domain.DisabledGeoAnomaly {
		t.Fatalf("geo_anomaly row: reason = %q, want geo_anomaly untouched", got)
	}
	if got := users.users[2].ServiceDisabledReason; got != domain.DisabledNone {
		t.Fatalf("geo_auto row (control): reason = %q, want lifted", got)
	}
	for _, uid := range sus.liftCalls {
		if uid == 1 {
			t.Fatal("the lift was attempted on a geo_anomaly row")
		}
	}
}

// Lifting must not depend on live-IP data: a suspended user is offline, and
// a poll with no shared clients at all (or an unreadable fleet) still has
// suspensions coming due.
func TestPollOnce_LiftRunsWithoutLiveIPData(t *testing.T) {
	metrics.Reset()
	users := &fakeUserRepo{users: map[int64]*domain.User{
		1: {ID: 1, Enabled: true, ServiceDisabledReason: domain.DisabledGeoAutoSuspend, ServiceDisabledAt: minutesAgo(120)},
	}}
	svc := New(users, &fakeOwnershipRepo{byUser: map[int64][]*domain.XUIClientEntry{}},
		&fakeTrafficRepo{}, nil, nil, &fakeXUIPool{clients: map[int64]ports.XUIClient{}}, &fakeDisabler{})
	svc.SetPSPClientRepo(&fakePSPClientRepo{byUser: map[int64][]*domain.PSPClient{}})
	svc.SetGeoPolicy(domain.DefaultGeoPolicy())
	svc.SetGeoSuspender(&fakeGeoSuspender{repo: users})

	if err := svc.PollOnce(context.Background()); err != nil {
		t.Fatalf("PollOnce: %v", err)
	}
	if got := users.users[1].ServiceDisabledReason; got != domain.DisabledNone {
		t.Fatalf("reason = %q after a poll with no live-IP data, want lifted", got)
	}
}

// The duration is the user's group's CURRENT effective one, and the cutoff
// handed to the suspender is now minus exactly that.
func TestEnforceGeo_LiftUsesTheGroupDuration(t *testing.T) {
	metrics.Reset()
	users := &fakeUserRepo{users: map[int64]*domain.User{
		1: {ID: 1, GroupID: 3, Enabled: true, ServiceDisabledReason: domain.DisabledGeoAutoSuspend, ServiceDisabledAt: minutesAgo(90)},
		2: {ID: 2, GroupID: 4, Enabled: true, ServiceDisabledReason: domain.DisabledGeoAutoSuspend, ServiceDisabledAt: minutesAgo(90)},
	}}
	s, sus, _ := newEnforcer(users, map[int64]ports.UISettings{
		3: {GeoAnomalyBanDurationMinutes: 120},
		4: {}, // the shipped 60
	})
	now := time.Now()

	s.enforceGeo(context.Background(), listed(users), nil, nil, now)

	if got := users.users[1].ServiceDisabledReason; got != domain.DisabledGeoAutoSuspend {
		t.Fatalf("group of 120 minutes, 90 elapsed: reason = %q, want geo_auto still", got)
	}
	if got := users.users[2].ServiceDisabledReason; got != domain.DisabledNone {
		t.Fatalf("group of 60 minutes, 90 elapsed: reason = %q, want lifted", got)
	}
	if got, want := sus.cutoffs[2], now.Add(-60*time.Minute); !got.Equal(want) {
		t.Fatalf("cutoff for user 2 = %v, want now-60m = %v", got, want)
	}
}

// A group whose settings could not be read this poll has no known duration.
// The fallback policy's 60 minutes is a safe stand-in for JUDGING (it can only
// under-report), but for a time box it is the unsafe direction: a 24-hour
// suspension would end after 60 minutes, and nothing restores it. So those
// lifts wait for a poll that can read the group, and are counted as waiting.
//
// User 1's group stores 1440 minutes and its read fails; 90 minutes have
// passed, due on the fallback box and not on the stored one. User 2's group
// resolves to the shipped 60 and is the control.
func TestEnforceGeo_LiftWaitsWhenTheGroupsSettingsCannotBeRead(t *testing.T) {
	metrics.Reset()
	users := &fakeUserRepo{users: map[int64]*domain.User{
		1: {ID: 1, GroupID: 3, Enabled: true, ServiceDisabledReason: domain.DisabledGeoAutoSuspend, ServiceDisabledAt: minutesAgo(90)},
		2: {ID: 2, GroupID: 4, Enabled: true, ServiceDisabledReason: domain.DisabledGeoAutoSuspend, ServiceDisabledAt: minutesAgo(90)},
	}}
	s, sus, _ := newEnforcer(users, map[int64]ports.UISettings{
		3: {GeoAnomalyBanDurationMinutes: 1440},
		4: {},
	})
	s.settings.(*fakeScoped).errFor = map[int64]error{3: errors.New("scope overrides unavailable")}

	s.enforceGeo(context.Background(), listed(users), nil, nil, time.Now())

	if got := users.users[1].ServiceDisabledReason; got != domain.DisabledGeoAutoSuspend {
		t.Fatalf("unreadable group (stored 1440 minutes, 90 elapsed): reason = %q, want geo_auto still", got)
	}
	for _, uid := range sus.liftCalls {
		if uid == 1 {
			t.Fatal("the lift was attempted for a user whose group's duration is unknown")
		}
	}
	if got := geoOutcome(t, "lift_deferred"); got != 1 {
		t.Fatalf("lift_deferred = %d, want 1 (the unreadable group's suspension)", got)
	}
	if got := users.users[2].ServiceDisabledReason; got != domain.DisabledNone {
		t.Fatalf("readable group of 60 minutes, 90 elapsed (control): reason = %q, want lifted", got)
	}
}

// The unknown-owner collision end to end, as Phase 4 sees it: Phase 1b hands
// the SAME cache on, so an owner missing from the users list resolved there
// first must not shorten a no-group user's stored 24-hour box to the
// fallback's 60 minutes.
func TestEnforceGeo_AnUnknownOwnerDoesNotShortenANoGroupSuspension(t *testing.T) {
	metrics.Reset()
	users := &fakeUserRepo{users: map[int64]*domain.User{
		7: {ID: 7, Enabled: true, ServiceDisabledReason: domain.DisabledGeoAutoSuspend, ServiceDisabledAt: minutesAgo(90)},
	}}
	s, _, _ := newEnforcer(users, map[int64]ports.UISettings{0: {GeoAnomalyBanDurationMinutes: 1440}})
	list := listed(users)
	pc := s.newGeoPolicyCache(context.Background(), list)
	pc.forUser(99) // what Phase 1b resolves for a client whose owner is not listed

	s.enforceGeo(context.Background(), list, nil, pc, time.Now())

	if got := users.users[7].ServiceDisabledReason; got != domain.DisabledGeoAutoSuspend {
		t.Fatalf("no-group user, stored 1440 minutes, 90 elapsed: reason = %q, want geo_auto still", got)
	}
}

// A geo_auto row with no timestamp cannot be timed, so it is due now rather
// than never: a suspension the detector cannot end would be permanent.
func TestEnforceGeo_LiftWithoutATimestampLiftsNow(t *testing.T) {
	metrics.Reset()
	users := &fakeUserRepo{users: map[int64]*domain.User{
		1: {ID: 1, Enabled: true, ServiceDisabledReason: domain.DisabledGeoAutoSuspend},
	}}
	s, _, _ := newEnforcer(users, nil)

	s.enforceGeo(context.Background(), listed(users), nil, nil, time.Now())

	if got := users.users[1].ServiceDisabledReason; got != domain.DisabledNone {
		t.Fatalf("reason = %q, want lifted", got)
	}
}

// Turning suspension off stops NEW suspensions; it must not strand the ones
// already running. The lift never reads ban_enabled.
func TestEnforceGeo_LiftStillRunsWhenBanIsDisabled(t *testing.T) {
	metrics.Reset()
	users := &fakeUserRepo{users: map[int64]*domain.User{
		1: {ID: 1, GroupID: 3, Enabled: true, ServiceDisabledReason: domain.DisabledGeoAutoSuspend, ServiceDisabledAt: minutesAgo(120)},
	}}
	s, _, _ := newEnforcer(users, map[int64]ports.UISettings{3: {GeoAnomalyBanEnabled: false}})

	s.enforceGeo(context.Background(), listed(users), nil, nil, time.Now())

	if got := users.users[1].ServiceDisabledReason; got != domain.DisabledNone {
		t.Fatalf("reason = %q with suspension since turned off, want lifted", got)
	}
}

// The lift cap counts DUE suspensions only. Twenty long (7-day) suspensions
// that started earlier must not crowd a due 60-minute one out of the poll —
// with a cap over all candidates oldest-first, they would, every poll for a
// week.
func TestEnforceGeo_LiftCapCountsOnlyDueSuspensions(t *testing.T) {
	metrics.Reset()
	users := &fakeUserRepo{users: map[int64]*domain.User{
		100: {ID: 100, GroupID: 4, Enabled: true, ServiceDisabledReason: domain.DisabledGeoAutoSuspend, ServiceDisabledAt: minutesAgo(120)},
	}}
	for uid := int64(1); uid <= 20; uid++ {
		users.users[uid] = &domain.User{ID: uid, GroupID: 3, Enabled: true,
			ServiceDisabledReason: domain.DisabledGeoAutoSuspend, ServiceDisabledAt: minutesAgo(180)}
	}
	s, _, _ := newEnforcer(users, map[int64]ports.UISettings{
		3: {GeoAnomalyBanDurationMinutes: domain.GeoBanMaxDurationMinutes},
		4: {},
	})

	s.enforceGeo(context.Background(), listed(users), nil, nil, time.Now())

	if got := users.users[100].ServiceDisabledReason; got != domain.DisabledNone {
		t.Fatalf("the due 60-minute suspension: reason = %q, want lifted", got)
	}
	if got := geoOutcome(t, "lift_deferred"); got != 0 {
		t.Fatalf("lift_deferred = %d, want 0 (only one lift was due)", got)
	}
}

// More due lifts than the cap: the longest-running go first, the rest wait
// one poll and are counted.
func TestEnforceGeo_LiftsOverTheCapWaitForTheNextPoll(t *testing.T) {
	metrics.Reset()
	users := &fakeUserRepo{users: map[int64]*domain.User{}}
	for uid := int64(1); uid <= 21; uid++ {
		// user 1 is the most recent suspension, user 21 the oldest.
		users.users[uid] = &domain.User{ID: uid, Enabled: true,
			ServiceDisabledReason: domain.DisabledGeoAutoSuspend, ServiceDisabledAt: minutesAgo(60 + int(uid))}
	}
	s, _, _ := newEnforcer(users, nil)

	s.enforceGeo(context.Background(), listed(users), nil, nil, time.Now())

	if got := users.users[1].ServiceDisabledReason; got != domain.DisabledGeoAutoSuspend {
		t.Fatalf("the most recent due suspension: reason = %q, want it left for the next poll", got)
	}
	for uid := int64(2); uid <= 21; uid++ {
		if got := users.users[uid].ServiceDisabledReason; got != domain.DisabledNone {
			t.Fatalf("user %d: reason = %q, want lifted (among the 20 longest-running)", uid, got)
		}
	}
	if got := geoOutcome(t, "lift_deferred"); got != 1 {
		t.Fatalf("lift_deferred = %d, want 1", got)
	}
}

// Both transitions leave an audit row with a system actor, a timestamp and
// enough to reconstruct why: the tier, the admin-side reason naming the
// places, and the time box; for the lift, when the suspension began.
func TestEnforceGeo_AuditsBothTransitionsWithTimestamps(t *testing.T) {
	metrics.Reset()
	began := time.Now().Add(-2 * time.Hour).UTC().Truncate(time.Second)
	users := &fakeUserRepo{users: map[int64]*domain.User{
		1: {ID: 1, UPN: "sharer@example.test", Enabled: true},
		2: {ID: 2, UPN: "lifted@example.test", Enabled: true, ServiceDisabledReason: domain.DisabledGeoAutoSuspend, ServiceDisabledAt: &began},
	}}
	s, _, audit := newEnforcer(users, nil)
	b := ban(1)

	s.enforceGeo(context.Background(), listed(users), []geoBan{b}, nil, time.Now())

	if len(audit.entries) != 2 {
		t.Fatalf("audit rows = %d, want 2: %+v", len(audit.entries), audit.entries)
	}
	byAction := map[string]domain.AuditEntry{}
	for _, e := range audit.entries {
		if e.Actor != "geo-detector" {
			t.Errorf("actor = %q, want geo-detector", e.Actor)
		}
		if e.At.IsZero() {
			t.Errorf("%s: At is zero", e.Action)
		}
		byAction[e.Action] = e
	}

	sus, ok := byAction["geo_auto_suspend"]
	if !ok {
		t.Fatalf("no geo_auto_suspend row: %+v", audit.entries)
	}
	if sus.Target != "user:1 sharer@example.test" {
		t.Errorf("suspend target = %q", sus.Target)
	}
	var after struct {
		Tier            string `json:"tier"`
		Reason          string `json:"reason"`
		DurationMinutes int    `json:"duration_minutes"`
	}
	if err := json.Unmarshal([]byte(sus.AfterJSON), &after); err != nil {
		t.Fatalf("suspend after_json %q: %v", sus.AfterJSON, err)
	}
	if after.Tier != "country" || after.Reason != b.Reason || after.DurationMinutes != 60 {
		t.Errorf("suspend after_json = %+v, want country / %q / 60", after, b.Reason)
	}

	lift, ok := byAction["geo_auto_lift"]
	if !ok {
		t.Fatalf("no geo_auto_lift row: %+v", audit.entries)
	}
	if lift.Target != "user:2 lifted@example.test" {
		t.Errorf("lift target = %q", lift.Target)
	}
	var before struct {
		SuspendedAt *string `json:"suspended_at"`
	}
	if err := json.Unmarshal([]byte(lift.BeforeJSON), &before); err != nil {
		t.Fatalf("lift before_json %q: %v", lift.BeforeJSON, err)
	}
	if before.SuspendedAt == nil || *before.SuspendedAt != began.Format(time.RFC3339) {
		t.Errorf("lift before_json = %q, want suspended_at %s", lift.BeforeJSON, began.Format(time.RFC3339))
	}
	if !strings.Contains(lift.AfterJSON, `"duration_minutes":60`) {
		t.Errorf("lift after_json = %q, want duration_minutes 60", lift.AfterJSON)
	}
}

// A transition that did not happen is not audited. The poll's snapshot is
// from the top of the cycle: the ban's user was paused by an admin since
// (the CAS finds a reason), and the lift's user was resumed by one (the
// fresh read finds none). Both are counted as skips.
func TestEnforceGeo_UnappliedTransitionIsNotAudited(t *testing.T) {
	metrics.Reset()
	users := &fakeUserRepo{users: map[int64]*domain.User{
		1: {ID: 1, Enabled: true},
		2: {ID: 2, Enabled: true, ServiceDisabledReason: domain.DisabledGeoAutoSuspend, ServiceDisabledAt: minutesAgo(120)},
	}}
	s, _, audit := newEnforcer(users, nil)
	snapshot := listed(users)
	users.users[1].ServiceDisabledReason = domain.DisabledServiceManual
	users.users[2].ServiceDisabledReason = domain.DisabledNone
	users.users[2].ServiceDisabledAt = nil

	s.enforceGeo(context.Background(), snapshot, []geoBan{ban(1)}, nil, time.Now())

	if len(audit.entries) != 0 {
		t.Fatalf("audit rows = %+v, want none", audit.entries)
	}
	if got := geoOutcome(t, "skipped_held"); got != 1 {
		t.Fatalf("skipped_held = %d, want 1", got)
	}
	if got := geoOutcome(t, "lift_skipped"); got != 1 {
		t.Fatalf("lift_skipped = %d, want 1", got)
	}
	if got := users.users[1].ServiceDisabledReason; got != domain.DisabledServiceManual {
		t.Fatalf("user 1: reason = %q, want the admin's service_manual", got)
	}
}

// A failed transition is counted and logged, and does not stop the others.
// A suspender that reports applied together with an error (the push failed
// and could not be queued) did suspend: that is counted and audited as a
// suspension, not as a failure.
func TestEnforceGeo_FailuresAreCountedAndTheRestProceed(t *testing.T) {
	metrics.Reset()
	users := &fakeUserRepo{users: map[int64]*domain.User{
		1: {ID: 1, Enabled: true},
		2: {ID: 2, Enabled: true},
		3: {ID: 3, Enabled: true},
		4: {ID: 4, Enabled: true, ServiceDisabledReason: domain.DisabledGeoAutoSuspend, ServiceDisabledAt: minutesAgo(120)},
		5: {ID: 5, Enabled: true, ServiceDisabledReason: domain.DisabledGeoAutoSuspend, ServiceDisabledAt: minutesAgo(121)},
	}}
	s, sus, audit := newEnforcer(users, nil)
	boom := errors.New("boom")
	sus.errFor = map[int64]error{1: boom, 3: boom, 5: boom}
	sus.appliedWithErr = map[int64]bool{3: true}

	s.enforceGeo(context.Background(), listed(users), []geoBan{ban(1), ban(2), ban(3)}, nil, time.Now())

	for outcome, want := range map[string]int64{
		"suspend_error": 1, "suspended": 2, "lift_error": 1, "lifted_expiry": 1,
	} {
		if got := geoOutcome(t, outcome); got != want {
			t.Errorf("%s = %d, want %d", outcome, got, want)
		}
	}
	for uid, want := range map[int64]domain.AutoDisabledReason{
		1: domain.DisabledNone, 2: domain.DisabledGeoAutoSuspend, 3: domain.DisabledGeoAutoSuspend,
		4: domain.DisabledNone, 5: domain.DisabledGeoAutoSuspend,
	} {
		if got := users.users[uid].ServiceDisabledReason; got != want {
			t.Errorf("user %d: reason = %q, want %q", uid, got, want)
		}
	}
	if len(audit.entries) != 3 {
		t.Fatalf("audit rows = %d, want 3 (two suspensions, one lift)", len(audit.entries))
	}
}

// The user-facing detail per tier: the spread and the time box, in Chinese,
// no place named (F11: the portal and the suspension mail show it verbatim).
func TestGeoAutoSuspendDetail_StatesSpreadAndTimeBoxWithoutPlaces(t *testing.T) {
	for _, c := range []struct {
		tier domain.GeoTier
		want string
	}{
		{domain.GeoTierCountry, "检测到账号同时在 2 个国家或地区使用，代理服务已临时暂停，约 45 分钟后自动恢复"},
		{domain.GeoTierRegion, "检测到账号同时在同一国家的 2 个省或州使用，代理服务已临时暂停，约 45 分钟后自动恢复"},
		{domain.GeoTierCity, "检测到账号同时在同一国家的 2 个城市使用，代理服务已临时暂停，约 45 分钟后自动恢复"},
	} {
		if got := geoAutoSuspendDetail(c.tier, 2, 45); got != c.want {
			t.Errorf("%s: detail = %q, want %q", c.tier, got, c.want)
		}
	}
	if got := geoAutoSuspendDetail(domain.GeoTierNone, 2, 45); got == "" || !strings.Contains(got, "45 分钟") {
		t.Errorf("unknown tier: detail = %q, want a generic text that still states the time box", got)
	}
}

// ---------------------------------------------------------------------------
// Placement in the poll.
// ---------------------------------------------------------------------------

// orderedUserRepo records the order of the metering flush and the
// suspension write.
type orderedUserRepo struct {
	*fakeUserRepo
	order []string
}

func (r *orderedUserRepo) BatchUpdateTrafficState(ctx context.Context, users []*domain.User) error {
	r.order = append(r.order, "flush")
	return r.fakeUserRepo.BatchUpdateTrafficState(ctx, users)
}

func (r *orderedUserRepo) SetServiceStateIfClear(ctx context.Context, userID int64, reason domain.AutoDisabledReason, detail string, at time.Time) (bool, error) {
	r.order = append(r.order, "suspend")
	return r.fakeUserRepo.SetServiceStateIfClear(ctx, userID, reason, detail, at)
}

// Metering is what PollOnce exists for. Enforcement pushes inline and waits
// on the per-user lock, so it runs after the flush has persisted the cycle's
// traffic, never before.
func TestPollOnce_GeoEnforceRunsAfterTheFlush(t *testing.T) {
	metrics.Reset()
	base := &fakeUserRepo{users: map[int64]*domain.User{1: {ID: 1, GroupID: 3, Enabled: true}}}
	users := &orderedUserRepo{fakeUserRepo: base}
	// A pre-seeded raw baseline, so the poll meters a real delta and has
	// user traffic state to flush.
	psp := &fakePSPClientRepo{byUser: map[int64][]*domain.PSPClient{
		1: {{ID: 1, UserID: 1, PanelID: 10, Email: emailOf(1), LastRawUpBytes: 100, LastRawDownBytes: 100, LastRawTotalBytes: 200}},
	}}
	pool := &fakeXUIPool{clients: map[int64]ports.XUIClient{
		10: &liveIPReaderFake{fakeXUIClient: &fakeXUIClient{
			inbounds: []ports.Inbound{{ID: 20, ClientStats: []ports.ClientTraffic{{Email: emailOf(1), Up: 700, Down: 500}}}},
			liveIPs:  map[string][]string{emailOf(1): {"1.1.1.1", "2.2.2.2"}},
		}},
	}}
	svc := New(users, &fakeOwnershipRepo{byUser: map[int64][]*domain.XUIClientEntry{}},
		&fakeTrafficRepo{}, nil, nil, pool, &fakeDisabler{})
	svc.WithSettings(&fakeScoped{byGroup: map[int64]ports.UISettings{3: armed()}})
	svc.SetPSPClientRepo(psp)
	svc.SetGeoResolver(twoCountries())
	svc.SetGeoPolicy(domain.DefaultGeoPolicy())
	svc.SetGeoSuspender(&fakeGeoSuspender{repo: users})

	if err := svc.PollOnce(context.Background()); err != nil {
		t.Fatalf("PollOnce: %v", err)
	}
	if want := []string{"flush", "suspend"}; strings.Join(users.order, ",") != strings.Join(want, ",") {
		t.Fatalf("order = %v, want %v", users.order, want)
	}
}

// ---------------------------------------------------------------------------
// A poll cancelled around Phase 4.
// ---------------------------------------------------------------------------

// consumedRecord is what Phase 1b saved for a user whose ban came due: the
// flag, its reason and evidence, and a ban streak the due verdict consumed.
func consumedRecord(uid int64) domain.GeoRecord {
	return domain.GeoRecord{
		UserID: uid,
		Streak: domain.GeoStreak{Over: 4, Flagged: true, Tier: domain.GeoTierCountry, BanOver: 0},
		State:  domain.GeoStateFlagged,
		Reason: "in 2 countries at once ([DE JP]); tolerance is 1",
		Places: []string{"DE", "JP"},
	}
}

// consumedBan is a due ban carrying the record Phase 1b saved for it.
func consumedBan(uid int64) geoBan {
	b := ban(uid)
	b.Record = consumedRecord(uid)
	return b
}

// banAfter is a group armed with its own ban threshold, so a re-armed streak
// is told apart from a consumed (0) or a fresh (1) one.
func banAfter(n int) ports.UISettings {
	s := armed()
	s.GeoAnomalyBanAfterPolls = n
	return s
}

// A poll whose caller has gone (a closed "Poll now" tab, a shutdown) starts
// no new transition: every lift or suspension is inline pushes under the
// per-user lock, and each one would fail on the cancelled context anyway.
// Nothing is lost by stopping. A due lift is simply due again next poll; a
// due ban already had its streak consumed and saved in Phase 1b, so it is
// deferred the way the per-poll cap defers one — the streak put back AT the
// threshold, the rest of the row as Phase 1b saved it — and fires on the
// user's next over-sample.
func TestEnforceGeo_ACancelledPollStartsNoTransitionAndLosesNoBan(t *testing.T) {
	metrics.Reset()
	users := &fakeUserRepo{users: map[int64]*domain.User{
		1: {ID: 1, GroupID: 3, Enabled: true},
		2: {ID: 2, GroupID: 3, Enabled: true, ServiceDisabledReason: domain.DisabledGeoAutoSuspend, ServiceDisabledAt: minutesAgo(120)},
	}}
	s, sus, audit := newEnforcer(users, map[int64]ports.UISettings{3: banAfter(4)})
	store := &upsertStreaks{data: map[int64]domain.GeoRecord{1: consumedRecord(1)}}
	s.SetGeoStreakStore(store)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	s.enforceGeo(ctx, listed(users), []geoBan{consumedBan(1)}, nil, time.Now())

	if len(sus.suspendCalls) != 0 || len(sus.liftCalls) != 0 {
		t.Fatalf("suspend calls %v, lift calls %v; a cancelled poll must not start a transition", sus.suspendCalls, sus.liftCalls)
	}
	if got := users.users[1].ServiceDisabledReason; got != domain.DisabledNone {
		t.Fatalf("user 1: reason = %q, want none", got)
	}
	if got := users.users[2].ServiceDisabledReason; got != domain.DisabledGeoAutoSuspend {
		t.Fatalf("user 2: reason = %q, want geo_auto still (lifted next poll)", got)
	}
	if len(audit.entries) != 0 {
		t.Fatalf("audit rows = %+v, want none (nothing happened)", audit.entries)
	}
	for outcome, want := range map[string]int64{
		"deferred": 1, "lift_deferred": 1, "suspend_error": 0, "lift_error": 0, "suspended": 0, "lifted_expiry": 0,
	} {
		if got := geoOutcome(t, outcome); got != want {
			t.Errorf("%s = %d, want %d", outcome, got, want)
		}
	}
	got, want := store.data[1], consumedRecord(1)
	want.Streak.BanOver = 4
	if got.Streak != want.Streak || got.State != want.State || got.Reason != want.Reason || strings.Join(got.Places, ",") != "DE,JP" {
		t.Fatalf("saved row = %+v, want Phase 1b's row with the ban streak re-armed to 4: %+v", got, want)
	}
}

// End to end: the poll is cancelled right after Phase 1b saved the consumed
// streak, so the Phase 4 that follows runs on a dead context. The ban is not
// applied then, and it is not lost either: the user's next over-sample makes
// it due again. Three samples to the threshold, so a streak that stayed
// consumed (0, one sample later 1) cannot pass for a re-armed one.
func TestPollOnce_ABanACancelledPollDidNotApplyFiresOnTheNextSample(t *testing.T) {
	metrics.Reset()
	users := &fakeUserRepo{users: map[int64]*domain.User{1: {ID: 1, GroupID: 3, Enabled: true}}}
	g := newGeoPoll(users, map[string][]string{emailOf(1): {"1.1.1.1", "2.2.2.2"}}, twoCountries(),
		map[int64]ports.UISettings{3: banAfter(3)})

	g.poll(t)
	g.poll(t)
	if got := g.store.data[1].Streak.BanOver; got != 2 {
		t.Fatalf("ban streak after two over-samples = %d, want 2 (precondition)", got)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	g.store.afterSave = cancel
	_ = g.svc.PollOnce(ctx)

	if got := users.users[1].ServiceDisabledReason; got != domain.DisabledNone {
		t.Fatalf("reason = %q after the cancelled poll, want none (no transition started)", got)
	}
	saved := g.store.data[1]
	if got := saved.Streak.BanOver; got != 3 {
		t.Fatalf("saved ban streak after the cancelled poll = %d, want 3 (re-armed at the threshold)", got)
	}
	// The re-arm writes back the row this poll judged, not a blank one with
	// only the streak set: the Geo tab and the bell read the rest of it.
	if saved.State != domain.GeoStateFlagged || !strings.Contains(saved.Reason, "2 countries") ||
		strings.Join(saved.Places, ",") != "DE,JP" || !saved.Streak.Flagged || saved.Streak.Over != 3 {
		t.Fatalf("saved row after the cancelled poll = %+v, want this poll's flagged verdict with only the ban streak re-armed", saved)
	}
	if got := geoOutcome(t, "deferred"); got != 1 {
		t.Fatalf("deferred = %d, want 1", got)
	}

	g.poll(t)
	if got := users.users[1].ServiceDisabledReason; got != domain.DisabledGeoAutoSuspend {
		t.Fatalf("reason = %q after the next over-sample, want geo_auto (deferred, not lost)", got)
	}
}

// Cancelled in the middle of a transition: the one whose write committed is
// a real transition and is recorded as one — counted and audited, on a
// context the cancellation does not reach — and the ones after it are not
// started. The check is made before EACH transition, not once at the top.
func TestEnforceGeo_CancelledMidPhaseFinishesTheTransitionInFlightAndStartsNoOther(t *testing.T) {
	t.Run("suspensions", func(t *testing.T) {
		metrics.Reset()
		users := &fakeUserRepo{users: map[int64]*domain.User{
			1: {ID: 1, GroupID: 3, UPN: "first@example.test", Enabled: true},
			2: {ID: 2, GroupID: 3, Enabled: true},
		}}
		s, sus, audit := newEnforcer(users, map[int64]ports.UISettings{3: banAfter(4)})
		store := &upsertStreaks{data: map[int64]domain.GeoRecord{1: consumedRecord(1), 2: consumedRecord(2)}}
		s.SetGeoStreakStore(store)
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		sus.afterWrite = cancel

		s.enforceGeo(ctx, listed(users), []geoBan{consumedBan(1), consumedBan(2)}, nil, time.Now())

		if len(sus.suspendCalls) != 1 || sus.suspendCalls[0] != 1 {
			t.Fatalf("suspend calls = %v, want only user 1 (the one in flight when the poll was cancelled)", sus.suspendCalls)
		}
		if got := users.users[1].ServiceDisabledReason; got != domain.DisabledGeoAutoSuspend {
			t.Fatalf("user 1: reason = %q, want geo_auto", got)
		}
		if len(audit.entries) != 1 || audit.entries[0].Action != "geo_auto_suspend" || audit.entries[0].Target != "user:1 first@example.test" {
			t.Fatalf("audit rows = %+v, want one geo_auto_suspend for user 1", audit.entries)
		}
		if got := geoOutcome(t, "suspended"); got != 1 {
			t.Errorf("suspended = %d, want 1", got)
		}
		if got := geoOutcome(t, "deferred"); got != 1 {
			t.Errorf("deferred = %d, want 1", got)
		}
		if got := store.data[2].Streak.BanOver; got != 4 {
			t.Fatalf("user 2: saved ban streak = %d, want 4 (re-armed)", got)
		}
		if got := store.data[1].Streak.BanOver; got != 0 {
			t.Fatalf("user 1: saved ban streak = %d, want 0 (consumed by the suspension it got)", got)
		}
	})

	t.Run("lifts", func(t *testing.T) {
		metrics.Reset()
		users := &fakeUserRepo{users: map[int64]*domain.User{
			3: {ID: 3, Enabled: true, ServiceDisabledReason: domain.DisabledGeoAutoSuspend, ServiceDisabledAt: minutesAgo(120)},
			4: {ID: 4, UPN: "oldest@example.test", Enabled: true, ServiceDisabledReason: domain.DisabledGeoAutoSuspend, ServiceDisabledAt: minutesAgo(121)},
		}}
		s, sus, audit := newEnforcer(users, nil)
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		sus.afterWrite = cancel

		s.enforceGeo(ctx, listed(users), nil, nil, time.Now())

		if len(sus.liftCalls) != 1 || sus.liftCalls[0] != 4 {
			t.Fatalf("lift calls = %v, want only user 4 (the longest-running, in flight when the poll was cancelled)", sus.liftCalls)
		}
		if got := users.users[4].ServiceDisabledReason; got != domain.DisabledNone {
			t.Fatalf("user 4: reason = %q, want lifted", got)
		}
		if got := users.users[3].ServiceDisabledReason; got != domain.DisabledGeoAutoSuspend {
			t.Fatalf("user 3: reason = %q, want geo_auto still (lifted next poll)", got)
		}
		if len(audit.entries) != 1 || audit.entries[0].Action != "geo_auto_lift" || audit.entries[0].Target != "user:4 oldest@example.test" {
			t.Fatalf("audit rows = %+v, want one geo_auto_lift for user 4", audit.entries)
		}
		if got := geoOutcome(t, "lifted_expiry"); got != 1 {
			t.Errorf("lifted_expiry = %d, want 1", got)
		}
		if got := geoOutcome(t, "lift_deferred"); got != 1 {
			t.Errorf("lift_deferred = %d, want 1", got)
		}
	})
}
