package domain

import (
	"net/netip"
	"sort"
	"time"
	"unicode/utf8"
)

// The live-connection snapshot: every live source of every account, on the
// panel and node that reported it, as the last observation judged it. It is
// what the risk center's 实时连接 view shows by default, and it is built from
// the very reads, freshness decisions and exclusions the concurrent-location
// check ran on (FreshLiveIPsWithin, ClassifyAddresses), so the view can never
// list an address the detector did not see or hide one it did.
//
// It is a display artifact that lives in memory only (the traffic service
// holds the latest one) and carries addresses, which is why it is served to
// admins only and never persisted as such.

// Safety bounds of one snapshot. Not settings: they bound memory and the
// fixed width each value is shown and stored at, and are nothing an
// operator would tune.
const (
	// LiveConnMaxPerUser caps one account's connections in a snapshot. An
	// account behind a pathological NAT pool, or a shared credential in a
	// crowd, must not grow the snapshot without bound. Excluded sources are
	// cut before judged ones (CollectLiveConnections); the cut is counted
	// in LiveConnSnapshot.Truncated.
	LiveConnMaxPerUser = 64
	// LiveConnNodeMaxBytes, LiveConnKeyMaxBytes and LiveConnIPMaxBytes bound
	// the upstream-supplied strings of one connection. A parsed source key
	// or address is at most 39 bytes (a full IPv6 address); only a node
	// guid or an address PSP could not parse can be longer, and both come
	// from the upstream verbatim.
	LiveConnNodeMaxBytes = 64
	LiveConnKeyMaxBytes  = 64
	LiveConnIPMaxBytes   = 64

	// The widths of a ConnPlace's fields (ConnPlaceOf). A country or region
	// code is a few ASCII bytes; names are the database's display names.
	ConnPlaceCountryCodeMaxBytes = 8
	ConnPlaceCountryMaxBytes     = 64
	ConnPlaceRegionMaxBytes      = 128
	ConnPlaceRegionCodeMaxBytes  = 8
	ConnPlaceCityMaxBytes        = 128
)

// ConnPlace is where the geo database puts one connection's address: names
// and a normalised region code, never coordinates. The point and its radius
// stay in the GeoLocation the lookup returned, which lives for one
// judgement; a coordinate kept beside an account and an address is a map
// pin per subscriber (see TestNoStoredTypeHoldsACoordinate).
type ConnPlace struct {
	CountryCode, Country, Region, RegionCode, City string
}

// ConnPlaceOf keeps the displayable part of a lookup. The region code goes
// through NormalizeRegionCode like every other reader of it; every field is
// cut to its width on a character boundary.
func ConnPlaceOf(g GeoLocation) ConnPlace {
	return ConnPlace{
		CountryCode: cutBytes(g.CountryCode, ConnPlaceCountryCodeMaxBytes),
		Country:     cutBytes(g.Country, ConnPlaceCountryMaxBytes),
		Region:      cutBytes(g.Region, ConnPlaceRegionMaxBytes),
		RegionCode:  cutBytes(NormalizeRegionCode(g.RegionCode), ConnPlaceRegionCodeMaxBytes),
		City:        cutBytes(g.City, ConnPlaceCityMaxBytes),
	}
}

// LiveConnection is one account's live source through one panel node.
type LiveConnection struct {
	UserID, PanelID int64
	// Node is the upstream node guid, shown raw (PSP has no guid -> name
	// mapping); "" for a reader without a node layer (a PSP-native node).
	Node string
	// SourceKey is SourceKey(ip): the address, or its IPv6 /64.
	SourceKey string
	// IP is the smallest live member of the source on this (panel, node),
	// for display only: stable from poll to poll, like SourceAddr.LookupIP.
	IP string
	// SeenAt is the node's newest sighting of any member, unix seconds on
	// the PANEL's clock (compare it with nothing else); 0 = no timestamp.
	SeenAt int64
	// Exclusion is the rule that set the source aside before judging
	// (AddressExcludedInternal, Listed, Infra or Shared), "" when the
	// detector judged it. From the same ClassifyAddresses decision.
	Exclusion string
	// Place is filled by the caller that can look addresses up; zero when
	// nothing placed it.
	Place ConnPlace
}

// LiveUserMeta is what one account's connection list does NOT show.
type LiveUserMeta struct {
	// Stale is how many of the account's window addresses the upstream
	// still remembers but no node restamped inside the freshness window:
	// where the account was, not is.
	Stale int
	// Unread is how many panels holding one of the account's clients could
	// not be read, so the list may be missing connections. A panel whose
	// adapter has no live read at all (LiveConnSnapshot.Unsupported) is
	// not counted: that is a property of the adapter, not a failure.
	Unread int
}

// Where a snapshot came from: the scheduled or manual poll, which also
// judged it, or the risk center's on-demand refresh, which judges nothing.
const (
	LiveSnapshotFromPoll    = "poll"
	LiveSnapshotFromRefresh = "refresh"
)

// LiveConnSnapshot is one reading of every panel's live connections.
// Immutable once stored: readers share the pointer.
type LiveConnSnapshot struct {
	// TakenAt is PSP's clock when the panel reads were in hand.
	TakenAt time.Time
	// Source is LiveSnapshotFromPoll or LiveSnapshotFromRefresh.
	Source string
	// PanelsAsked is how many panels the reading covered.
	PanelsAsked int
	// Unread lists the panels whose live read failed, sorted.
	Unread []int64
	// Unsupported lists the panels whose adapter has no live read at all
	// (S-UI), sorted: never read, and never a failure.
	Unsupported []int64
	// Unreferenced counts the timestamped nodes judged with no previous
	// reference — the first reading after a restart — whose answers were
	// trusted once (FreshLiveIPs), so some of it may be memory, not live.
	Unreferenced int
	// Conns is sorted by (UserID, PanelID, Node, SourceKey).
	Conns []LiveConnection
	// Users holds an entry for every account with at least one connection.
	Users map[int64]LiveUserMeta
	// Truncated counts connections cut by LiveConnMaxPerUser.
	Truncated int
}

// CollectLiveConnections lists each account's live connections from panels
// that went through FreshLiveIPsWithin: one per (account, panel, node,
// source), from a timestamped panel's FreshSightings, or — with no node and
// no time — from a plain reader's Fresh (or its ByEmail, when freshness was
// never computed, as the aggregator reads it). An unread panel contributes
// nothing and an email with no owner is skipped, exactly as in
// AggregateLiveIPsByUser; the exclusion reason is addrs' decision for the
// source (ClassifyAddresses over the same panels).
//
// At most maxPerUser connections per account (≤ 0 means
// LiveConnMaxPerUser). The judged sources are kept first and the excluded
// ones fill what is left: when something must go, it is the part the
// verdict did not look at. The second result counts what was cut.
func CollectLiveConnections(panels []PanelLiveIPs, owners map[ClientKey]int64,
	addrs map[int64]UserAddresses, maxPerUser int) ([]LiveConnection, int) {
	if maxPerUser <= 0 {
		maxPerUser = LiveConnMaxPerUser
	}
	type connKey struct {
		uid, panel int64
		node, key  string
	}
	type member struct {
		lowest    netip.Addr // smallest parsed member
		raw       string     // smallest unparsed member, when none parsed
		seenAt    int64
		exclusion string
	}
	acc := map[connKey]*member{}
	add := func(uid, panel int64, node, ip string, seenAt int64) {
		key, a, ok := SourceKey(ip)
		if key == "" {
			return
		}
		// Cut before keying, so two overlong values that share a prefix
		// are one connection here as they would be one row where they are
		// stored.
		k := connKey{uid: uid, panel: panel, node: cutBytes(node, LiveConnNodeMaxBytes), key: cutBytes(key, LiveConnKeyMaxBytes)}
		ex := addrs[uid].ExcludedBy[key]
		m := acc[k]
		if m == nil {
			m = &member{exclusion: ex}
			acc[k] = m
		} else if ex == "" {
			// Only a cut can merge two sources, and then a judged member
			// wins: which one arrived first is map order.
			m.exclusion = ""
		}
		switch {
		case ok && (!m.lowest.IsValid() || a.Compare(m.lowest) < 0):
			m.lowest = a
		case !ok && (m.raw == "" || key < m.raw):
			m.raw = key
		}
		if seenAt > m.seenAt {
			m.seenAt = seenAt
		}
	}

	for _, p := range panels {
		if p.Err != nil {
			continue
		}
		if p.FreshSightings != nil {
			for email, list := range p.FreshSightings {
				uid, ok := owners[ClientKey{PanelID: p.PanelID, Email: email}]
				if !ok {
					continue
				}
				for _, s := range list {
					add(uid, p.PanelID, s.Node, s.IP, s.SeenAt)
				}
			}
			continue
		}
		live := p.Fresh
		if live == nil {
			live = p.ByEmail
		}
		for email, ips := range live {
			uid, ok := owners[ClientKey{PanelID: p.PanelID, Email: email}]
			if !ok {
				continue
			}
			for _, ip := range ips {
				add(uid, p.PanelID, "", ip, 0)
			}
		}
	}

	perUser := map[int64][]LiveConnection{}
	for k, m := range acc {
		ip := m.raw
		if m.lowest.IsValid() {
			ip = m.lowest.String()
		}
		perUser[k.uid] = append(perUser[k.uid], LiveConnection{
			UserID:    k.uid,
			PanelID:   k.panel,
			Node:      k.node,
			SourceKey: k.key,
			IP:        cutBytes(ip, LiveConnIPMaxBytes),
			SeenAt:    m.seenAt,
			Exclusion: m.exclusion,
		})
	}

	var out []LiveConnection
	truncated := 0
	for _, list := range perUser {
		if len(list) > maxPerUser {
			sort.Slice(list, func(i, j int) bool {
				ji, jj := list[i].Exclusion == "", list[j].Exclusion == ""
				if ji != jj {
					return ji
				}
				return liveConnLess(list[i], list[j])
			})
			truncated += len(list) - maxPerUser
			list = list[:maxPerUser]
		}
		out = append(out, list...)
	}
	sort.Slice(out, func(i, j int) bool { return liveConnLess(out[i], out[j]) })
	return out, truncated
}

// liveConnLess is the snapshot's order: account, panel, node, source.
func liveConnLess(a, b LiveConnection) bool {
	if a.UserID != b.UserID {
		return a.UserID < b.UserID
	}
	if a.PanelID != b.PanelID {
		return a.PanelID < b.PanelID
	}
	if a.Node != b.Node {
		return a.Node < b.Node
	}
	return a.SourceKey < b.SourceKey
}

// cutBytes keeps s within n bytes without splitting a character. Values cut
// here are shown and stored in fixed-width columns; a byte-wise cut can end
// mid-sequence, which some drivers reject and every reader renders as a
// replacement character.
func cutBytes(s string, n int) string {
	if len(s) <= n {
		return s
	}
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n]
}
