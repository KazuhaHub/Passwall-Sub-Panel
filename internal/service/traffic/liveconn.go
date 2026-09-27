package traffic

import (
	"context"
	"errors"
	"sort"
	"time"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/pkg/metrics"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

// The live-connection snapshot (domain.LiveConnSnapshot): the risk center's
// default 实时连接 view, the connections the latest reading of the panels
// found, each on the panel and node that reported it.
//
// It lives in memory only, on purpose. It is a display of one moment,
// replaced whole by the next reading and lost on restart (it reappears after
// the first poll); persisting it would be one more table of addresses to age
// out, for a view whose whole point is "now". It is built inside
// observeLiveIPs from the reads and the decisions the detector has already
// made — the same fresh sightings, the same exclusions — so what an admin
// sees is what was judged, and building it costs no panel call. The only
// extra work is one geo lookup to place the connections for display.

// LiveSnapshot is the latest stored snapshot, or nil before the first poll.
// The snapshot is shared and immutable: a caller must not modify it.
func (s *Service) LiveSnapshot() *domain.LiveConnSnapshot {
	s.liveSnapMu.RLock()
	defer s.liveSnapMu.RUnlock()
	return s.liveSnap
}

// storeLiveSnapshot replaces the stored snapshot unless the stored one is
// newer. Polls overlap (the scheduled one and a staff "poll now"), and a
// reading that started first can finish last; the view must show the newest
// reading, never the one that happened to finish last. A tie replaces.
//
// psp_live_connections follows what is stored, so it always describes the
// snapshot an admin would be shown.
func (s *Service) storeLiveSnapshot(snap *domain.LiveConnSnapshot) {
	if snap == nil {
		return
	}
	s.liveSnapMu.Lock()
	defer s.liveSnapMu.Unlock()
	if cur := s.liveSnap; cur != nil && snap.TakenAt.Before(cur.TakenAt) {
		return
	}
	s.liveSnap = snap
	metrics.LiveConnections.Set(int64(len(snap.Conns)))
}

// buildLiveSnapshot assembles one snapshot from panels that went through
// the freshness rule and the exclusions decided over the same panels. It
// reads no panel and writes nothing but the returned value; storing it is
// the caller's decision.
//
// A panel whose adapter has no live read at all (ports.ErrPanelCapabilityUnsupported,
// S-UI's permanent shape) is listed as Unsupported, apart from the panels
// whose read FAILED: showing it as a failure on every reading would teach an
// admin to skip the entry that means a real outage. For the same reason it
// does not count toward an account's unread panels.
func (s *Service) buildLiveSnapshot(ctx context.Context, panels []domain.PanelLiveIPs,
	owners map[domain.ClientKey]int64, addrs map[int64]domain.UserAddresses,
	unreferenced int, source string, at time.Time) *domain.LiveConnSnapshot {
	snap := &domain.LiveConnSnapshot{
		TakenAt:      at,
		Source:       source,
		PanelsAsked:  len(panels),
		Unreferenced: unreferenced,
	}
	unread := map[int64]bool{}
	for _, p := range panels {
		switch {
		case p.Err == nil:
		case errors.Is(p.Err, ports.ErrPanelCapabilityUnsupported):
			snap.Unsupported = append(snap.Unsupported, p.PanelID)
		default:
			snap.Unread = append(snap.Unread, p.PanelID)
			unread[p.PanelID] = true
		}
	}
	sort.Slice(snap.Unread, func(i, j int) bool { return snap.Unread[i] < snap.Unread[j] })
	sort.Slice(snap.Unsupported, func(i, j int) bool { return snap.Unsupported[i] < snap.Unsupported[j] })

	snap.Conns, snap.Truncated = domain.CollectLiveConnections(panels, owners, addrs, domain.LiveConnMaxPerUser)
	s.placeConnections(ctx, snap.Conns)

	// Distinct unread PANELS per account: a user split into two clients on
	// one panel (clientplan) is missing one panel's answer, not two.
	unreadBy := map[int64]map[int64]struct{}{}
	for k, uid := range owners {
		if !unread[k.PanelID] {
			continue
		}
		if unreadBy[uid] == nil {
			unreadBy[uid] = map[int64]struct{}{}
		}
		unreadBy[uid][k.PanelID] = struct{}{}
	}
	snap.Users = map[int64]domain.LiveUserMeta{}
	for _, c := range snap.Conns {
		if _, ok := snap.Users[c.UserID]; !ok {
			snap.Users[c.UserID] = domain.LiveUserMeta{Stale: addrs[c.UserID].Stale, Unread: len(unreadBy[c.UserID])}
		}
	}
	return snap
}

// placeConnections fills each connection's Place with one lookup over the
// distinct addresses, for display. An internal address (private, CGNAT,
// loopback, link-local) is not somewhere, so the database is never asked
// about it; every other exclusion still has a place worth showing (a listed
// office exit, a relay, a shared carrier gateway). Skipped when no database
// is in a position to answer. The lookup's coordinates are dropped here
// (domain.ConnPlaceOf): the snapshot is served beside the address.
func (s *Service) placeConnections(ctx context.Context, conns []domain.LiveConnection) {
	if s.geo == nil || len(conns) == 0 || !s.geo.Available(ctx) {
		return
	}
	seen := map[string]struct{}{}
	var ips []string
	for _, c := range conns {
		if c.Exclusion == domain.AddressExcludedInternal || c.IP == "" {
			continue
		}
		if _, ok := seen[c.IP]; !ok {
			seen[c.IP] = struct{}{}
			ips = append(ips, c.IP)
		}
	}
	if len(ips) == 0 {
		return
	}
	located := s.geo.Lookup(ctx, ips)
	for i := range conns {
		if conns[i].Exclusion == domain.AddressExcludedInternal {
			continue
		}
		if g, ok := located[conns[i].IP]; ok {
			conns[i].Place = domain.ConnPlaceOf(g)
		}
	}
}
