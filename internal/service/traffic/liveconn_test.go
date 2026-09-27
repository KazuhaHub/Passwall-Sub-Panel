package traffic

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"slices"
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
