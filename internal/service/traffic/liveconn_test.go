package traffic

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"reflect"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/pkg/metrics"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

// The live-connection snapshot is the risk center's default 实时连接 view:
// the connections the last poll judged, on the panel and node that reported
// each, kept in memory only. It is built from the reads and the decisions
// the detector already made in the same observation, so it can never show
// an address the verdict did not see, or hide one it did.

// mustSnapshot is the stored snapshot, failing the test when there is none.
func mustSnapshot(t *testing.T, s *Service) *domain.LiveConnSnapshot {
	t.Helper()
	snap := s.LiveSnapshot()
	if snap == nil {
		t.Fatal("no live-connection snapshot is stored")
	}
	return snap
}

// A poll stores one snapshot: its own instant, how many panels it asked and
// which of them could not be read, and every live connection of an owned
// client — the sighting a node restamps inside the freshness window, never
// the address the upstream merely remembers. Each account's window
// addresses that were not live, and the unread panels holding its clients,
// ride along so the view can say the list is not the whole story.
func TestObserveLiveIPs_StoresTheLiveSnapshot(t *testing.T) {
	metrics.Reset()
	now := time.Unix(1_790_000_000, 0)
	s := newObserver(nil, nil, domain.DefaultGeoPolicy())
	s.observeLiveIPs(context.Background(), liveIPInput{
		users:    []*domain.User{{ID: 7}, {ID: 8}},
		clients:  []*domain.PSPClient{client(7, 1, "u7@x"), client(7, 2, "u7@y"), client(8, 1, "u8@x")},
		panelIDs: panelsOf(1, 2),
		read: func(pid int64) domain.PanelLiveIPs {
			if pid == 2 {
				return domain.PanelLiveIPs{Err: errors.New("timeout")}
			}
			return detailRead(map[string][]domain.LiveIPSighting{
				"u7@x":       {seen("1.1.1.1", 1000), seen("2.2.2.2", 700)}, // 2.2.2.2: 300 s behind, memory
				"u8@x":       {seen("3.3.3.3", 990)},
				"stranger@x": {seen("9.9.9.9", 1000)}, // a hand-made client PSP does not own
			})(pid)
		},
		now: now,
	})

	snap := s.LiveSnapshot()
	if snap == nil {
		t.Fatal("the poll stored no live-connection snapshot")
	}
	if snap.Source != domain.LiveSnapshotFromPoll || !snap.TakenAt.Equal(now) || snap.PanelsAsked != 2 {
		t.Fatalf("snapshot source %q taken %v asked %d, want poll / the poll's instant / 2", snap.Source, snap.TakenAt, snap.PanelsAsked)
	}
	if !reflect.DeepEqual(snap.Unread, []int64{2}) || len(snap.Unsupported) != 0 {
		t.Fatalf("unread %v unsupported %v, want [2] and none", snap.Unread, snap.Unsupported)
	}
	want := []domain.LiveConnection{
		{UserID: 7, PanelID: 1, Node: "n1", SourceKey: "1.1.1.1", IP: "1.1.1.1", SeenAt: 1000},
		{UserID: 8, PanelID: 1, Node: "n1", SourceKey: "3.3.3.3", IP: "3.3.3.3", SeenAt: 990},
	}
	if !reflect.DeepEqual(snap.Conns, want) || snap.Truncated != 0 {
		t.Fatalf("connections = %+v (truncated %d), want %+v", snap.Conns, snap.Truncated, want)
	}
	wantUsers := map[int64]domain.LiveUserMeta{7: {Stale: 1, Unread: 1}, 8: {}}
	if !reflect.DeepEqual(snap.Users, wantUsers) {
		t.Fatalf("users = %+v, want %+v", snap.Users, wantUsers)
	}
	if got := gaugeFor(t, "psp_live_connections"); got != 2 {
		t.Fatalf("psp_live_connections = %d, want 2", got)
	}
}

// The spacing rule decides who is JUDGED, not who is connected. A user a
// manual poll judged a minute ago is still online, and the view of who is
// connected right now must list them.
func TestObserveLiveIPs_SnapshotIncludesSpacedUsers(t *testing.T) {
	metrics.Reset()
	now := time.Now()
	store := &upsertStreaks{data: map[int64]domain.GeoRecord{
		7: {UserID: 7, State: domain.GeoStateClean, UpdatedAtMS: now.Add(-time.Minute).UnixMilli()},
	}}
	s := newObserver(twoCountries(), store, domain.DefaultGeoPolicy())
	s.observeLiveIPs(context.Background(), liveIPInput{
		clients:    []*domain.PSPClient{client(7, 1, "u7@x")},
		panelIDs:   panelsOf(1),
		read:       plainRead(map[string][]string{"u7@x": {"1.1.1.1", "2.2.2.2"}}),
		minSpacing: 150 * time.Second,
		now:        now,
	})

	if store.wrote(7) {
		t.Fatal("precondition: user 7 was judged, want spaced")
	}
	snap := s.LiveSnapshot()
	if snap == nil || len(snap.Conns) != 2 || snap.Conns[0].UserID != 7 {
		t.Fatalf("snapshot = %+v, want the spaced user's two connections", snap)
	}
}

// Each connection is placed for the admin: a kept source, and an excluded
// one that has a place (a listed office exit is somewhere). An internal
// address is not somewhere, so the database is never asked about it. The
// place carries names and the region code only — the coordinates the
// database returned stay in the lookup.
func TestObserveLiveIPs_SnapshotPlacesConnections(t *testing.T) {
	metrics.Reset()
	geo := &stubGeo{available: true, places: map[string]domain.GeoLocation{
		"1.1.1.1":     placeAt("JP", "Tokyo", "13", "Shinjuku", 35.69, 139.7, 5),
		"203.0.113.5": placeIn("US", "California", "San Jose"),
	}}
	s := newObserver(geo, nil, domain.DefaultGeoPolicy())
	s.observeLiveIPs(context.Background(), liveIPInput{
		clients:  []*domain.PSPClient{client(7, 1, "u7@x")},
		panelIDs: panelsOf(1),
		read:     plainRead(map[string][]string{"u7@x": {"1.1.1.1", "203.0.113.5", "10.0.0.1"}}),
		ignore:   "203.0.113.5",
	})

	byIP := map[string]domain.LiveConnection{}
	for _, c := range mustSnapshot(t, s).Conns {
		byIP[c.IP] = c
	}
	if got, want := byIP["1.1.1.1"].Place, (domain.ConnPlace{CountryCode: "JP", Country: "JP", Region: "Tokyo", RegionCode: "13", City: "Shinjuku"}); got != want {
		t.Fatalf("kept source placed at %+v, want %+v", got, want)
	}
	if c := byIP["203.0.113.5"]; c.Exclusion != domain.AddressExcludedListed || c.Place.CountryCode != "US" {
		t.Fatalf("listed source = %+v, want excluded as listed and still placed in US", c)
	}
	if c := byIP["10.0.0.1"]; c.Exclusion != domain.AddressExcludedInternal || c.Place != (domain.ConnPlace{}) {
		t.Fatalf("internal source = %+v, want excluded as internal and unplaced", c)
	}
	if slices.Contains(geo.asked, "10.0.0.1") {
		t.Fatalf("the database was asked about an internal address: %v", geo.asked)
	}

	// No database in a position to answer: nothing is asked, nothing placed.
	off := &stubGeo{available: false, places: geo.places}
	s = newObserver(off, nil, domain.DefaultGeoPolicy())
	s.observeLiveIPs(context.Background(), liveIPInput{
		clients:  []*domain.PSPClient{client(7, 1, "u7@x")},
		panelIDs: panelsOf(1),
		read:     plainRead(map[string][]string{"u7@x": {"1.1.1.1"}}),
	})
	if c := mustSnapshot(t, s).Conns[0]; c.Place != (domain.ConnPlace{}) || off.lookups != 0 {
		t.Fatalf("geo unavailable: place %+v after %d lookups, want unplaced and none", c.Place, off.lookups)
	}
}

// A poll with no shared client to attribute still happened: the view shows
// "nobody connected, as of now", not whatever an older poll saw. It reads
// no panel data for it, exactly as before.
func TestObserveLiveIPs_NoClientsStoresAnEmptySnapshot(t *testing.T) {
	now := time.Unix(1_790_000_000, 0)
	noRead := func(int64) domain.PanelLiveIPs {
		t.Fatal("panel data must not be read when there are no clients")
		return domain.PanelLiveIPs{}
	}
	for name, clients := range map[string][]*domain.PSPClient{
		"no clients":           nil,
		"no client with email": {client(7, 1, "")},
	} {
		s := newObserver(nil, nil, domain.DefaultGeoPolicy())
		s.storeLiveSnapshot(&domain.LiveConnSnapshot{TakenAt: now.Add(-time.Hour), Conns: []domain.LiveConnection{{UserID: 9}}})
		s.observeLiveIPs(context.Background(), liveIPInput{clients: clients, panelIDs: panelsOf(1, 2), read: noRead, now: now})
		snap := s.LiveSnapshot()
		if snap == nil || !snap.TakenAt.Equal(now) || snap.Source != domain.LiveSnapshotFromPoll ||
			snap.PanelsAsked != 2 || len(snap.Conns) != 0 {
			t.Fatalf("%s: snapshot = %+v, want an empty poll snapshot taken now over 2 panels", name, snap)
		}
	}

	// No reader at all is not a poll of the panels: nothing is stored.
	s := newObserver(nil, nil, domain.DefaultGeoPolicy())
	s.observeLiveIPs(context.Background(), liveIPInput{panelIDs: panelsOf(1), now: now})
	if snap := s.LiveSnapshot(); snap != nil {
		t.Fatalf("snapshot = %+v with no reader, want none", snap)
	}
}

// An S-UI panel has no live read at all. That is a permanent property of
// the adapter, not a failure, and listing it as "could not be read" on every
// poll would train an admin to ignore the entry that means a real outage.
// So it is reported apart, and it does not count toward any account's
// unread panels.
func TestObserveLiveIPs_SnapshotSeparatesUnsupportedFromUnread(t *testing.T) {
	s := newObserver(nil, nil, domain.DefaultGeoPolicy())
	s.observeLiveIPs(context.Background(), liveIPInput{
		clients: []*domain.PSPClient{
			client(7, 1, "u7@1"), client(7, 2, "u7@2"), client(7, 3, "u7@3"), client(7, 4, "u7@4"),
		},
		panelIDs: panelsOf(1, 2, 3, 4),
		read: func(pid int64) domain.PanelLiveIPs {
			switch pid {
			case 1:
				return domain.PanelLiveIPs{Err: fmt.Errorf("s-ui: %w", ports.ErrPanelCapabilityUnsupported)}
			case 2:
				return domain.PanelLiveIPs{Err: errors.New("timeout")}
			case 3:
				return domain.PanelLiveIPs{Err: errPanelNotRead}
			}
			return domain.PanelLiveIPs{ByEmail: map[string][]string{"u7@4": {"1.1.1.1"}}}
		},
	})

	snap := mustSnapshot(t, s)
	if !reflect.DeepEqual(snap.Unsupported, []int64{1}) || !reflect.DeepEqual(snap.Unread, []int64{2, 3}) {
		t.Fatalf("unsupported %v unread %v, want [1] and [2 3]", snap.Unsupported, snap.Unread)
	}
	if got := snap.Users[7].Unread; got != 2 {
		t.Fatalf("user 7 unread panels = %d, want 2 — the S-UI panel is not a failed read", got)
	}
}

// Right after a restart PSP has no reference for any node, and the
// detector trusts each one's answer once (domain.FreshLiveIPs). The
// snapshot says how many nodes that was, so an admin reading the first
// view after a restart knows part of it may be memory rather than live.
func TestObserveLiveIPs_SnapshotCountsUnreferencedNodes(t *testing.T) {
	s := newObserver(nil, nil, domain.DefaultGeoPolicy())
	in := func(at int64) liveIPInput {
		return liveIPInput{
			clients:  []*domain.PSPClient{client(7, 1, "u7@x"), client(7, 2, "u7@y")},
			panelIDs: panelsOf(1, 2),
			read: func(pid int64) domain.PanelLiveIPs {
				if pid == 2 { // a plain reader has no node to reference
					return domain.PanelLiveIPs{ByEmail: map[string][]string{"u7@y": {"2.2.2.2"}}}
				}
				return detailRead(map[string][]domain.LiveIPSighting{"u7@x": {seen("1.1.1.1", at)}})(pid)
			},
		}
	}

	s.observeLiveIPs(context.Background(), in(1000))
	if got := mustSnapshot(t, s).Unreferenced; got != 1 {
		t.Fatalf("first poll: unreferenced = %d, want 1 (node n1 had no reference)", got)
	}
	s.observeLiveIPs(context.Background(), in(1010))
	if got := mustSnapshot(t, s).Unreferenced; got != 0 {
		t.Fatalf("second poll: unreferenced = %d, want 0", got)
	}
}

// Polls overlap, and a refresh can finish after a poll that started later.
// The view shows the NEWEST reading: an older snapshot arriving late never
// replaces a newer one, and the gauge follows what is actually shown.
func TestStoreLiveSnapshot_OlderNeverReplacesNewer(t *testing.T) {
	s := &Service{}
	t0 := time.Unix(1_790_000_000, 0)
	newer := &domain.LiveConnSnapshot{TakenAt: t0.Add(time.Minute), Source: domain.LiveSnapshotFromRefresh, Conns: make([]domain.LiveConnection, 3)}
	older := &domain.LiveConnSnapshot{TakenAt: t0, Source: domain.LiveSnapshotFromPoll, Conns: make([]domain.LiveConnection, 5)}

	s.storeLiveSnapshot(newer)
	s.storeLiveSnapshot(older)
	if got := s.LiveSnapshot(); got != newer {
		t.Fatalf("stored %+v, want the newer snapshot kept", got)
	}
	if got := gaugeFor(t, "psp_live_connections"); got != 3 {
		t.Fatalf("psp_live_connections = %d, want 3 (the snapshot shown)", got)
	}

	newest := &domain.LiveConnSnapshot{TakenAt: t0.Add(2 * time.Minute), Conns: make([]domain.LiveConnection, 1)}
	s.storeLiveSnapshot(newest)
	s.storeLiveSnapshot(nil)
	if got := s.LiveSnapshot(); got != newest {
		t.Fatalf("stored %+v, want the newest snapshot", got)
	}
	if got := gaugeFor(t, "psp_live_connections"); got != 1 {
		t.Fatalf("psp_live_connections = %d, want 1", got)
	}
}

// Through the real entry point: PollOnce's own Phase 1 reads become the
// snapshot. A 3X-UI panel's detail read keeps its node, an adapter with no
// live read at all (S-UI's shape) is unsupported, and a detail read that
// failed is unread.
func TestPollOnce_StoresTheLiveSnapshot(t *testing.T) {
	metrics.Reset()
	users := &fakeUserRepo{users: map[int64]*domain.User{1: {ID: 1, Enabled: true}}}
	psp := &fakePSPClientRepo{byUser: map[int64][]*domain.PSPClient{1: {
		{ID: 1, UserID: 1, PanelID: 10, Email: "u1@10"},
		{ID: 2, UserID: 1, PanelID: 11, Email: "u1@11"},
		{ID: 3, UserID: 1, PanelID: 12, Email: "u1@12"},
	}}}
	pool := &fakeXUIPool{clients: map[int64]ports.XUIClient{
		10: &liveIPDetailReaderFake{fakeXUIClient: &fakeXUIClient{inbounds: []ports.Inbound{{ID: 20}}},
			sightings: map[string][]domain.LiveIPSighting{"u1@10": {{IP: "1.1.1.1", Node: "guid-a", SeenAt: 1000}}}},
		11: &fakeXUIClient{inbounds: []ports.Inbound{{ID: 21}}},
		12: &liveIPDetailReaderFake{fakeXUIClient: &fakeXUIClient{inbounds: []ports.Inbound{{ID: 22}}},
			detailErr: errors.New("connection reset")},
	}}
	svc := New(users, &fakeOwnershipRepo{byUser: map[int64][]*domain.XUIClientEntry{}},
		&fakeTrafficRepo{}, nil, nil, pool, &fakeDisabler{})
	svc.SetPSPClientRepo(psp)

	if err := svc.PollOnce(context.Background()); err != nil {
		t.Fatalf("PollOnce: %v", err)
	}
	snap := svc.LiveSnapshot()
	if snap == nil || snap.Source != domain.LiveSnapshotFromPoll || snap.PanelsAsked != 3 {
		t.Fatalf("snapshot = %+v, want a poll snapshot over 3 panels", snap)
	}
	if !reflect.DeepEqual(snap.Unsupported, []int64{11}) || !reflect.DeepEqual(snap.Unread, []int64{12}) {
		t.Fatalf("unsupported %v unread %v, want [11] and [12]", snap.Unsupported, snap.Unread)
	}
	want := []domain.LiveConnection{{UserID: 1, PanelID: 10, Node: "guid-a", SourceKey: "1.1.1.1", IP: "1.1.1.1", SeenAt: 1000}}
	if !reflect.DeepEqual(snap.Conns, want) {
		t.Fatalf("connections = %+v, want %+v", snap.Conns, want)
	}
}

// ---------------------------------------------------------------------------
// The on-demand refresh (立即刷新): a fresh reading for the view, never a
// detector sample.
// ---------------------------------------------------------------------------

// refreshPanel is a 3X-UI-shaped panel for the refresh: both live readers
// (each counted by liveIPDetailReaderFake), the inbound lists counted by
// the embedded fakeXUIClient, and a hook that runs inside the live read —
// the window in which a poll can finish while the refresh waits on the
// panel.
type refreshPanel struct {
	*liveIPDetailReaderFake
	during func()
}

func (p *refreshPanel) ListLiveClientIPDetails(ctx context.Context) (map[string][]domain.LiveIPSighting, error) {
	if p.during != nil {
		p.during()
	}
	return p.liveIPDetailReaderFake.ListLiveClientIPDetails(ctx)
}

func detailPanel(sightings map[string][]domain.LiveIPSighting) *refreshPanel {
	return &refreshPanel{liveIPDetailReaderFake: &liveIPDetailReaderFake{fakeXUIClient: &fakeXUIClient{}, sightings: sightings}}
}

// panicPanel's live read panics: one malformed upstream answer.
type panicPanel struct{ *fakeXUIClient }

func (panicPanel) ListLiveClientIPDetails(context.Context) (map[string][]domain.LiveIPSighting, error) {
	panic("malformed live-IP answer")
}

// goexitPanel's live read ends its goroutine without an answer: the shape
// a read that dies half-way leaves behind.
type goexitPanel struct{ *fakeXUIClient }

func (goexitPanel) ListLiveClientIPDetails(context.Context) (map[string][]domain.LiveIPSighting, error) {
	runtime.Goexit()
	return nil, nil
}

// listFailing is a shared-client repository that cannot be read.
type listFailing struct {
	ports.PSPClientRepo
	err error
}

func (f listFailing) ListAll(context.Context) ([]*domain.PSPClient, error) { return nil, f.err }

// countingStreaks counts every streak read and write, and stores nothing.
type countingStreaks struct{ loads, saves int }

func (c *countingStreaks) Load(context.Context) (map[int64]domain.GeoRecord, error) {
	c.loads++
	return nil, nil
}

func (c *countingStreaks) Save(context.Context, map[int64]domain.GeoRecord) error {
	c.saves++
	return nil
}

// newRefresher is a Service wired the way the refresh needs it: a pool of
// panels and the shared clients that own the connections on them.
func newRefresher(panels map[int64]ports.XUIClient, clients ...*domain.PSPClient) *Service {
	psp := &fakePSPClientRepo{byUser: map[int64][]*domain.PSPClient{}}
	for _, c := range clients {
		psp.byUser[c.UserID] = append(psp.byUser[c.UserID], c)
	}
	s := New(&fakeUserRepo{users: map[int64]*domain.User{}}, &fakeOwnershipRepo{byUser: map[int64][]*domain.XUIClientEntry{}},
		&fakeTrafficRepo{}, nil, nil, &fakeXUIPool{clients: panels}, &fakeDisabler{})
	s.SetPSPClientRepo(psp)
	return s
}

// mustRefresh runs one refresh, failing the test on an error or when it
// hands back no snapshot.
func mustRefresh(t *testing.T, s *Service) *domain.LiveConnSnapshot {
	t.Helper()
	snap, err := s.RefreshLiveConnections(context.Background())
	if err != nil {
		t.Fatalf("RefreshLiveConnections: %v", err)
	}
	if snap == nil {
		t.Fatal("the refresh returned no snapshot")
	}
	return snap
}

// detectorFamilies are the metrics the location detector records per
// judged sample, plus the automatic suspension's outcomes.
var detectorFamilies = []string{
	"psp_user_live_ips", "psp_geo_verdict_total", "psp_live_ip_users_incomplete_total",
	"psp_user_concurrent_ips", "psp_live_ip_stale_total", "psp_live_ip_excluded_total",
	"psp_geo_over_tier_total", "psp_geo_spread_km", "psp_geo_samples_spaced_total",
	"psp_geo_auto_suspension_total",
}

// detectorMetrics renders every detector metric by name: counters by value,
// histograms by count and sum. A labelled child is created on first use, so
// a child appearing is a change too.
func detectorMetrics() map[string]string {
	in := func(name string) bool {
		for _, f := range detectorFamilies {
			if name == f || strings.HasPrefix(name, f+"{") {
				return true
			}
		}
		return false
	}
	out := map[string]string{}
	snap := metrics.Take()
	for _, c := range snap.Counters {
		if in(c.Name) {
			out[c.Name] = fmt.Sprint(c.Value)
		}
	}
	for _, h := range snap.Histograms {
		if in(h.Name) {
			out[h.Name] = fmt.Sprintf("count %d sum %g", h.Count, h.Sum)
		}
	}
	return out
}

// One refresh is one live read per panel holding a shared client — the
// detail read where the adapter has one — and nothing else: metering is
// the poll's job, so no inbound list is fetched. The reading is stored as
// a refresh snapshot over every panel it asked, taken when the reads
// completed. With no reference yet for either timestamped node, both were
// trusted once, and the snapshot says so.
func TestRefreshLiveConnections_ReadsEachPanelOnce(t *testing.T) {
	metrics.Reset()
	p10 := detailPanel(map[string][]domain.LiveIPSighting{"u1@10": {seen("1.1.1.1", 1000)}})
	p11 := detailPanel(map[string][]domain.LiveIPSighting{"u2@11": {seen("2.2.2.2", 2000)}})
	p12 := &liveIPReaderFake{fakeXUIClient: &fakeXUIClient{liveIPs: map[string][]string{"u3@12": {"3.3.3.3"}}}}
	s := newRefresher(map[int64]ports.XUIClient{10: p10, 11: p11, 12: p12},
		client(1, 10, "u1@10"), client(2, 11, "u2@11"), client(3, 12, "u3@12"))

	before := time.Now()
	snap := mustRefresh(t, s)

	for pid, p := range map[int64]*refreshPanel{10: p10, 11: p11} {
		if p.detailCalls != 1 || p.liveCalls != 0 || p.listSlimCalled != 0 || p.listFullCalled != 0 {
			t.Fatalf("panel %d: detail %d plain %d slim %d full %d, want one detail read and nothing else",
				pid, p.detailCalls, p.liveCalls, p.listSlimCalled, p.listFullCalled)
		}
	}
	if p12.liveCalls != 1 || p12.listSlimCalled != 0 || p12.listFullCalled != 0 {
		t.Fatalf("plain panel: live %d slim %d full %d, want one live read and nothing else",
			p12.liveCalls, p12.listSlimCalled, p12.listFullCalled)
	}
	if snap.Source != domain.LiveSnapshotFromRefresh || snap.PanelsAsked != 3 || snap.TakenAt.Before(before) {
		t.Fatalf("snapshot source %q asked %d taken %v, want refresh / 3 / after %v", snap.Source, snap.PanelsAsked, snap.TakenAt, before)
	}
	want := []domain.LiveConnection{
		{UserID: 1, PanelID: 10, Node: "n1", SourceKey: "1.1.1.1", IP: "1.1.1.1", SeenAt: 1000},
		{UserID: 2, PanelID: 11, Node: "n1", SourceKey: "2.2.2.2", IP: "2.2.2.2", SeenAt: 2000},
		{UserID: 3, PanelID: 12, SourceKey: "3.3.3.3", IP: "3.3.3.3"},
	}
	if !reflect.DeepEqual(snap.Conns, want) {
		t.Fatalf("connections = %+v, want %+v", snap.Conns, want)
	}
	if snap.Unreferenced != 2 {
		t.Fatalf("unreferenced = %d, want 2 (two timestamped nodes, no reference yet)", snap.Unreferenced)
	}
	if s.LiveSnapshot() != snap {
		t.Fatal("the returned snapshot is not the stored one")
	}
	if got := gaugeFor(t, "psp_live_connections"); got != 3 {
		t.Fatalf("psp_live_connections = %d, want 3", got)
	}
}

// A refresh is never a detector sample (R2). The reading below, judged,
// would move every detector figure — two countries at once, a remembered
// address, an internal one — and the refresh still lists what is live.
// But nothing the detector keeps between polls may change: not the
// per-node references (the next poll judges "rescanned since" against
// them), not a streak, not the poll's settings cache, not one detector
// metric. Only the size of the stored view moves.
func TestRefreshLiveConnections_LeavesDetectorStateAlone(t *testing.T) {
	metrics.Reset()
	panel := detailPanel(map[string][]domain.LiveIPSighting{
		"u7@x": {seen("1.1.1.1", 1000), seen("2.2.2.2", 995), seen("10.0.0.1", 1000), seen("3.3.3.3", 700)},
	})
	s := newRefresher(map[int64]ports.XUIClient{1: panel}, client(7, 1, "u7@x"))
	streaks := &countingStreaks{}
	s.SetGeoStreakStore(streaks)
	s.SetGeoResolver(twoCountries())
	s.WithSettings(&fakeScoped{global: ports.UISettings{CronTrafficPullMinutes: 5}})
	cached := ports.UISettings{CronTrafficPullMinutes: 7, GeoAnomalyIgnoreAddresses: "9.9.9.9"}
	s.pollCfgCache = cached
	s.liveRefs = map[domain.NodeRef]int64{{PanelID: 1, Node: "n1"}: 900, {PanelID: 5, Node: "other"}: 42}
	refs := maps.Clone(s.liveRefs)
	detector := detectorMetrics()
	for _, name := range []string{"psp_user_live_ips", "psp_live_ip_users_incomplete_total", "psp_user_concurrent_ips",
		"psp_live_ip_stale_total", "psp_geo_samples_spaced_total"} {
		if _, ok := detector[name]; !ok {
			t.Fatalf("detector metric %s is not registered; the comparison below would prove nothing", name)
		}
	}

	snap := mustRefresh(t, s)
	if len(snap.Conns) != 3 {
		t.Fatalf("connections = %+v, want the three live sources (3.3.3.3 is memory)", snap.Conns)
	}

	if !reflect.DeepEqual(s.liveRefs, refs) {
		t.Errorf("per-node references = %v, want them untouched %v", s.liveRefs, refs)
	}
	if streaks.loads != 0 || streaks.saves != 0 {
		t.Errorf("streak loads %d saves %d, want none", streaks.loads, streaks.saves)
	}
	if !reflect.DeepEqual(s.pollCfgCache, cached) {
		t.Errorf("poll settings cache = %+v, want it untouched", s.pollCfgCache)
	}
	if got := detectorMetrics(); !reflect.DeepEqual(got, detector) {
		for name, v := range got {
			if detector[name] != v {
				t.Errorf("detector metric %s: %q → %q, want unchanged", name, detector[name], v)
			}
		}
	}
	if got := gaugeFor(t, "psp_live_connections"); got != 3 {
		t.Errorf("psp_live_connections = %d, want 3", got)
	}
}

// The references are copied BEFORE the first panel is asked. A poll that
// finishes while the refresh waits on the panel stores references as new as
// the refresh's own reading; judged against those, every node reads "not
// rescanned since", and the refresh would publish an empty view that wins
// on time. Here the poll lands inside the read, and the address is still
// listed — and the poll's reference is left as the poll stored it.
func TestRefreshLiveConnections_ClonesTheReferencesBeforeReading(t *testing.T) {
	panel := detailPanel(map[string][]domain.LiveIPSighting{"u7@x": {seen("1.1.1.1", 1000)}})
	s := newRefresher(map[int64]ports.XUIClient{1: panel}, client(7, 1, "u7@x"))
	ref := domain.NodeRef{PanelID: 1, Node: "n1"}
	s.liveRefs = map[domain.NodeRef]int64{ref: 900}
	panel.during = func() {
		// A poll finishing now, having read the very batch this refresh is reading.
		s.liveRefsMu.Lock()
		s.liveRefs[ref] = 1000
		s.liveRefsMu.Unlock()
	}

	snap := mustRefresh(t, s)
	want := []domain.LiveConnection{{UserID: 7, PanelID: 1, Node: "n1", SourceKey: "1.1.1.1", IP: "1.1.1.1", SeenAt: 1000}}
	if !reflect.DeepEqual(snap.Conns, want) {
		t.Fatalf("connections = %+v, want %+v — the references were read after the panel", snap.Conns, want)
	}
	if snap.Unreferenced != 0 {
		t.Fatalf("unreferenced = %d, want 0 (the node had a reference)", snap.Unreferenced)
	}
	s.liveRefsMu.Lock()
	got := s.liveRefs[ref]
	s.liveRefsMu.Unlock()
	if got != 1000 {
		t.Fatalf("reference = %d, want the poll's 1000 left as stored", got)
	}
}

// The refresh hands back what is stored once it is done, not what it
// built: a poll that stored a newer reading while the panels were being
// read wins, and the caller reports that one.
func TestRefreshLiveConnections_ReturnsTheStoredSnapshot(t *testing.T) {
	panel := detailPanel(map[string][]domain.LiveIPSighting{"u7@x": {seen("1.1.1.1", 1000)}})
	s := newRefresher(map[int64]ports.XUIClient{1: panel}, client(7, 1, "u7@x"))
	newer := &domain.LiveConnSnapshot{TakenAt: time.Now().Add(time.Hour), Source: domain.LiveSnapshotFromPoll}
	panel.during = func() { s.storeLiveSnapshot(newer) }

	got, err := s.RefreshLiveConnections(context.Background())
	if err != nil {
		t.Fatalf("RefreshLiveConnections: %v", err)
	}
	if got != newer || s.LiveSnapshot() != newer {
		t.Fatalf("returned %+v, stored %+v; want the newer poll snapshot for both", got, s.LiveSnapshot())
	}
}

// A panel whose read failed, one the pool cannot hand out, and one whose
// read ended with no answer at all were not read. Each is listed as unread
// and counts toward its account's unread panels — never read as "nobody
// connected there".
func TestRefreshLiveConnections_UnreadPanelIsReported(t *testing.T) {
	failing := detailPanel(nil)
	failing.detailErr = errors.New("connection reset")
	s := newRefresher(map[int64]ports.XUIClient{
		1: detailPanel(map[string][]domain.LiveIPSighting{"u7@1": {seen("1.1.1.1", 1000)}}),
		2: failing,
		4: goexitPanel{&fakeXUIClient{}},
		// 3 is not in the pool.
	}, client(7, 1, "u7@1"), client(7, 2, "u7@2"), client(7, 3, "u7@3"), client(7, 4, "u7@4"))

	snap := mustRefresh(t, s)
	if !reflect.DeepEqual(snap.Unread, []int64{2, 3, 4}) || len(snap.Unsupported) != 0 {
		t.Fatalf("unread %v unsupported %v, want [2 3 4] and none", snap.Unread, snap.Unsupported)
	}
	if snap.PanelsAsked != 4 || len(snap.Conns) != 1 {
		t.Fatalf("asked %d connections %+v, want 4 panels and the one live connection", snap.PanelsAsked, snap.Conns)
	}
	if got := snap.Users[7].Unread; got != 3 {
		t.Fatalf("user 7 unread panels = %d, want 3", got)
	}
}

// An S-UI panel has no live read at all: the adapter's permanent shape,
// not a failed read. The refresh lists it apart, and it does not count
// toward any account's unread panels.
func TestRefreshLiveConnections_UnsupportedPanelIsNotUnread(t *testing.T) {
	sui := &fakeXUIClient{}
	s := newRefresher(map[int64]ports.XUIClient{
		1: detailPanel(map[string][]domain.LiveIPSighting{"u7@1": {seen("1.1.1.1", 1000)}}),
		2: sui,
	}, client(7, 1, "u7@1"), client(7, 2, "u7@2"))

	snap := mustRefresh(t, s)
	if !reflect.DeepEqual(snap.Unsupported, []int64{2}) || len(snap.Unread) != 0 {
		t.Fatalf("unsupported %v unread %v, want [2] and none", snap.Unsupported, snap.Unread)
	}
	if got := snap.Users[7].Unread; got != 0 {
		t.Fatalf("user 7 unread panels = %d, want 0 — the S-UI panel is not a failed read", got)
	}
	if sui.listSlimCalled != 0 || sui.listFullCalled != 0 {
		t.Fatalf("the S-UI panel was listed (slim %d, full %d); a refresh reads live connections only",
			sui.listSlimCalled, sui.listFullCalled)
	}
}

// One malformed upstream answer must not take the panel process down: the
// panic is recovered, that panel is unread, the others are listed. With a
// single slot the reads run one after another, so a panicking read that
// kept its slot would leave the rest waiting forever.
func TestRefreshLiveConnections_RecoversAPanickingPanel(t *testing.T) {
	s := newRefresher(map[int64]ports.XUIClient{
		1: panicPanel{&fakeXUIClient{}},
		2: detailPanel(map[string][]domain.LiveIPSighting{"u7@2": {seen("2.2.2.2", 1000)}}),
		3: panicPanel{&fakeXUIClient{}},
	}, client(7, 1, "u7@1"), client(7, 2, "u7@2"), client(7, 3, "u7@3"))
	s.WithSettings(&fakeScoped{global: ports.UISettings{MaxPanelConcurrency: 1}})

	done := make(chan *domain.LiveConnSnapshot, 1)
	go func() {
		snap, err := s.RefreshLiveConnections(context.Background())
		if err != nil {
			t.Errorf("RefreshLiveConnections: %v", err)
		}
		done <- snap
	}()
	var snap *domain.LiveConnSnapshot
	select {
	case snap = <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("the refresh did not finish: a panicking read kept its concurrency slot")
	}
	if snap == nil {
		t.Fatal("the refresh returned no snapshot")
	}
	if !reflect.DeepEqual(snap.Unread, []int64{1, 3}) {
		t.Fatalf("unread = %v, want the panicking panels [1 3]", snap.Unread)
	}
	if len(snap.Conns) != 1 || snap.Conns[0].PanelID != 2 {
		t.Fatalf("connections = %+v, want panel 2's connection", snap.Conns)
	}
}

// Without the shared clients or the panel pool there is nothing to read
// and nothing to attribute: the refresh says the dependency is missing,
// and stores nothing.
func TestRefreshLiveConnections_UnwiredIsUnavailable(t *testing.T) {
	for name, s := range map[string]*Service{
		"nothing wired":     {},
		"no shared clients": {pool: &fakeXUIPool{clients: map[int64]ports.XUIClient{}}},
		"no pool":           {pspClient: &fakePSPClientRepo{}},
	} {
		snap, err := s.RefreshLiveConnections(context.Background())
		if !errors.Is(err, domain.ErrUnavailable) || snap != nil {
			t.Fatalf("%s: got %+v, %v; want ErrUnavailable and no snapshot", name, snap, err)
		}
		if s.LiveSnapshot() != nil {
			t.Fatalf("%s: a snapshot was stored", name)
		}
	}
}

// Who owns a connection is the shared-client list. When it cannot be read
// nothing can be attributed, so the refresh fails with the cause and the
// view keeps its previous reading.
func TestRefreshLiveConnections_FailsWhenTheClientsCannotBeListed(t *testing.T) {
	panel := detailPanel(map[string][]domain.LiveIPSighting{"u7@x": {seen("1.1.1.1", 1000)}})
	s := newRefresher(map[int64]ports.XUIClient{1: panel}, client(7, 1, "u7@x"))
	prior := &domain.LiveConnSnapshot{TakenAt: time.Now().Add(-time.Minute), Source: domain.LiveSnapshotFromPoll}
	s.storeLiveSnapshot(prior)
	boom := errors.New("database is locked")
	s.SetPSPClientRepo(listFailing{err: boom})

	snap, err := s.RefreshLiveConnections(context.Background())
	if !errors.Is(err, boom) || snap != nil {
		t.Fatalf("got %+v, %v; want the list error and no snapshot", snap, err)
	}
	if s.LiveSnapshot() != prior || panel.detailCalls != 0 {
		t.Fatalf("stored %+v after %d reads, want the previous snapshot and no panel read", s.LiveSnapshot(), panel.detailCalls)
	}
}

// The refresh classifies with the stored settings (here the ignore list)
// but never writes the poll's last-good settings cache: that cache is what
// the next poll falls back to, and it must hold what a POLL loaded. A
// failed read runs the refresh on the shipped defaults, and still leaves
// the cache alone.
func TestRefreshLiveConnections_DoesNotTouchThePollConfigCache(t *testing.T) {
	panel := detailPanel(map[string][]domain.LiveIPSighting{"u7@x": {seen("1.1.1.1", 1000)}})
	s := newRefresher(map[int64]ports.XUIClient{1: panel}, client(7, 1, "u7@x"))
	cached := ports.UISettings{CronTrafficPullMinutes: 7}
	s.pollCfgCache = cached
	set := &fakeScoped{global: ports.UISettings{CronTrafficPullMinutes: 5, GeoAnomalyIgnoreAddresses: "1.1.1.1"}}
	s.WithSettings(set)

	snap := mustRefresh(t, s)
	if c := snap.Conns; len(c) != 1 || c[0].Exclusion != domain.AddressExcludedListed {
		t.Fatalf("connections = %+v, want 1.1.1.1 excluded as listed by the stored ignore list", c)
	}
	if !reflect.DeepEqual(s.pollCfgCache, cached) {
		t.Fatalf("poll settings cache = %+v, want it untouched %+v", s.pollCfgCache, cached)
	}

	set.err = errors.New("database is locked")
	snap = mustRefresh(t, s)
	if c := snap.Conns; len(c) != 1 || c[0].Exclusion != "" {
		t.Fatalf("connections = %+v, want 1.1.1.1 judged on the defaults (no ignore list)", c)
	}
	if !reflect.DeepEqual(s.pollCfgCache, cached) {
		t.Fatalf("poll settings cache = %+v after a failed read, want it untouched %+v", s.pollCfgCache, cached)
	}
}
