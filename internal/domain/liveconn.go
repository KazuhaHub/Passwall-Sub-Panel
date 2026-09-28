package domain

import (
	"errors"
	"net/netip"
	"sort"
	"strings"
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

// ErrLiveJustPolled is a live-connection refresh that read no panel because
// a poll advanced the per-node freshness references moments ago. Under one
// upstream rescan later, every node that poll advanced still reads "not
// rescanned since" against them, so the reading would drop every one of
// those nodes' connections and publish an empty view as the newest. The
// stored snapshot — that poll's, or a reading that finished after it — is
// the answer instead. Not a failure: the caller reports it as such.
var ErrLiveJustPolled = errors.New("live connections: a poll read the panels moments ago")

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

// ---- Device inference ----
//
// The panels say who is connected from where, never with what. The live
// view infers the "what" from the fetch log: the same account fetching its
// subscription from the same source recently. That is an inference, and is
// labelled one wherever it is shown: a client in TUN or global mode fetches
// through its own tunnel, and a fetch can come from a device that is not
// the one connected.

const (
	// ConnDevicesMax caps the devices inferred for one source, newest first:
	// behind a household NAT a dozen clients can fetch from one address.
	ConnDevicesMax = 3
	// ConnDeviceUARunes caps the client string shown per device.
	ConnDeviceUARunes = 96
	// DeviceIDShownLen is how much of a declared device's per-account
	// digest an admin is shown, wherever it is shown (the sub-log list, the
	// live view): enough to tell one account's devices apart at a glance,
	// not a value worth copying anywhere.
	DeviceIDShownLen = 4
)

// The two kinds of client identity (SubLogIdentity).
const (
	SubLogIdentityHWID = "hwid" // the client declared a device id
	SubLogIdentityUA   = "ua"   // known by its client string alone
)

// SubLogIdentity is THE rule that tells one client apart from another in the
// fetch log: the device id the client declared, else its exact client
// string. The risk worker's fetch window and the live view's device
// inference both use it, so "one device" means the same thing in both. It
// errs toward linking: two people on the same app and version are one
// client here, which can hide a spread but never invents one. The client
// type is not part of it: it is the panel's guess from the user agent and
// changes when one client asks for another format.
func SubLogIdentity(l SubLog) (key, kind string) {
	if l.DeviceID != "" {
		return "d:" + l.DeviceID, SubLogIdentityHWID
	}
	return "u:" + l.UA, SubLogIdentityUA
}

// UserSource is one account's source: the key the live view matches fetches
// to connections by.
type UserSource struct {
	UserID    int64
	SourceKey string
}

// ConnDevice is one client inferred behind a connection.
type ConnDevice struct {
	// Label is the newest OS/model label the client declared; "" when it
	// declared none.
	Label string
	// DeviceID4 is the first DeviceIDShownLen characters of the declared
	// device's digest; "" for a client known by its client string.
	DeviceID4 string
	// ClientType and UA are the newest format asked for and client string
	// sent (the latter cut to ConnDeviceUARunes characters).
	ClientType, UA string
	// Fetches is how many of the account's fetches from the source this
	// client made in the window; LastAtMS the newest (unix ms).
	Fetches  int
	LastAtMS int64
}

// InferConnectionDevices matches each connection whose Exclusion is "",
// listed or shared against the same account's fetches at or after since
// from the same source (SourceKey of the fetch's address), and describes the
// clients behind them: one per SubLogIdentity, most recently seen first, at
// most ConnDevicesMax per source. An internal or infrastructure source is
// never matched: it is not the account's egress (a fetch "from" PSP's own
// relay came through the relay, as every other account's did), so a device
// there would be a guess about somebody else.
//
// The result is keyed by (account, source): the same source on two panel
// nodes is one set of fetches. A source nothing matched has no entry.
func InferConnectionDevices(conns []LiveConnection, fetches []SubLog, since time.Time) map[UserSource][]ConnDevice {
	wanted := map[UserSource]bool{}
	for _, c := range conns {
		switch c.Exclusion {
		case "", AddressExcludedListed, AddressExcludedShared:
			wanted[UserSource{UserID: c.UserID, SourceKey: c.SourceKey}] = true
		}
	}
	if len(wanted) == 0 {
		return nil
	}

	type agg struct {
		key                     string
		dev                     ConnDevice
		labelMS, clientMS, uaMS int64
		lastID                  int64
	}
	found := map[UserSource]map[string]*agg{}
	for i := range fetches {
		f := &fetches[i]
		if f.AccessedAt.Before(since) || strings.TrimSpace(f.IP) == "" {
			continue
		}
		key, _, _ := SourceKey(f.IP)
		// Cut as a connection's key is cut, so an overlong unparsed
		// address matches the connection it was folded into.
		us := UserSource{UserID: f.UserID, SourceKey: cutBytes(key, LiveConnKeyMaxBytes)}
		if !wanted[us] {
			continue
		}
		idKey, kind := SubLogIdentity(*f)
		byID := found[us]
		if byID == nil {
			byID = map[string]*agg{}
			found[us] = byID
		}
		a := byID[idKey]
		if a == nil {
			a = &agg{key: idKey}
			if kind == SubLogIdentityHWID && len(f.DeviceID) >= DeviceIDShownLen {
				a.dev.DeviceID4 = f.DeviceID[:DeviceIDShownLen]
			}
			byID[idKey] = a
		}
		at := f.AccessedAt.UnixMilli()
		a.dev.Fetches++
		// The newest value of each field, by fetch time (the id breaks a
		// tie in insert order); a later fetch that sent none does not erase
		// an earlier one.
		newer := func(ms int64) bool { return at > ms || (at == ms && f.ID > a.lastID) }
		if f.DeviceLabel != "" && (a.dev.Label == "" || newer(a.labelMS)) {
			a.dev.Label, a.labelMS = f.DeviceLabel, at
		}
		if f.ClientType != "" && (a.dev.ClientType == "" || newer(a.clientMS)) {
			a.dev.ClientType, a.clientMS = f.ClientType, at
		}
		if a.dev.Fetches == 1 || newer(a.uaMS) {
			a.dev.UA, a.uaMS = firstRunes(f.UA, ConnDeviceUARunes), at
		}
		if a.dev.Fetches == 1 || newer(a.dev.LastAtMS) {
			a.dev.LastAtMS, a.lastID = at, f.ID
		}
	}

	out := make(map[UserSource][]ConnDevice, len(found))
	for us, byID := range found {
		list := make([]*agg, 0, len(byID))
		for _, a := range byID {
			list = append(list, a)
		}
		sort.Slice(list, func(i, j int) bool {
			if list[i].dev.LastAtMS != list[j].dev.LastAtMS {
				return list[i].dev.LastAtMS > list[j].dev.LastAtMS
			}
			return list[i].key < list[j].key
		})
		if len(list) > ConnDevicesMax {
			list = list[:ConnDevicesMax]
		}
		devices := make([]ConnDevice, len(list))
		for i, a := range list {
			devices[i] = a.dev
		}
		out[us] = devices
	}
	return out
}

// UserDeviceSourcesMax caps the sources listed per device in the risk
// center's drawer, newest first; the rest are counted (UserDevice.SourcesMore).
// A phone on mobile data changes address all day, and a list that long says
// nothing a count does not.
const UserDeviceSourcesMax = 8

// UserDevice is one client behind an account's fetches in a window, told
// apart by SubLogIdentity — the drawer's 设备 tab, which answers "what does
// this account fetch with, and from where" over the window rather than per
// connection.
type UserDevice struct {
	// Label, DeviceID4, ClientType and UA are as on ConnDevice: the newest
	// of each (UA cut to ConnDeviceUARunes characters), the digest's first
	// DeviceIDShownLen characters for a declared device.
	Label, DeviceID4, ClientType, UA string
	// Fetches is how many fetches in the window this client made;
	// FirstAtMS and LastAtMS the oldest and the newest (unix ms).
	Fetches             int
	FirstAtMS, LastAtMS int64
	// Sources are the distinct sources (SourceKey: an IPv4 address or an
	// IPv6 /64) it fetched from, newest first, at most
	// UserDeviceSourcesMax; SourcesMore counts the rest.
	Sources     []string
	SourcesMore int
}

// UserDevices groups fetches at or after since by SubLogIdentity, newest
// LastAtMS first (a tie by the identity key, so the order is stable). The
// caller passes ONE account's fetches: a device is an account's client, and
// the identity rule does not include the account.
//
// Unlike InferConnectionDevices, every fetch counts, whatever its source: a
// client fetching through PSP's own relay is still the account's client,
// and here the question is the client, not the connection. A fetch with no
// address counts as a fetch and adds no source. Never nil, so a DTO built
// from it serializes as [].
func UserDevices(fetches []SubLog, since time.Time) []UserDevice {
	type agg struct {
		key                     string
		dev                     UserDevice
		labelMS, clientMS, uaMS int64
		lastID                  int64
		sources                 map[string]int64 // source → newest fetch from it
	}
	byID := map[string]*agg{}
	for i := range fetches {
		f := &fetches[i]
		if f.AccessedAt.Before(since) {
			continue
		}
		idKey, kind := SubLogIdentity(*f)
		a := byID[idKey]
		if a == nil {
			a = &agg{key: idKey, sources: map[string]int64{}}
			if kind == SubLogIdentityHWID && len(f.DeviceID) >= DeviceIDShownLen {
				a.dev.DeviceID4 = f.DeviceID[:DeviceIDShownLen]
			}
			byID[idKey] = a
		}
		at := f.AccessedAt.UnixMilli()
		a.dev.Fetches++
		// The newest value of each field, as InferConnectionDevices takes
		// it: by fetch time, a tie by the id (insert order), and a later
		// fetch that sent none does not erase an earlier one.
		newer := func(ms int64) bool { return at > ms || (at == ms && f.ID > a.lastID) }
		if f.DeviceLabel != "" && (a.dev.Label == "" || newer(a.labelMS)) {
			a.dev.Label, a.labelMS = f.DeviceLabel, at
		}
		if f.ClientType != "" && (a.dev.ClientType == "" || newer(a.clientMS)) {
			a.dev.ClientType, a.clientMS = f.ClientType, at
		}
		if a.dev.Fetches == 1 || newer(a.uaMS) {
			a.dev.UA, a.uaMS = firstRunes(f.UA, ConnDeviceUARunes), at
		}
		if a.dev.Fetches == 1 || newer(a.dev.LastAtMS) {
			a.dev.LastAtMS, a.lastID = at, f.ID
		}
		if a.dev.Fetches == 1 || at < a.dev.FirstAtMS {
			a.dev.FirstAtMS = at
		}
		if strings.TrimSpace(f.IP) != "" {
			key, _, _ := SourceKey(f.IP)
			// Cut as a live connection's key is cut, so the drawer's two
			// lists name one source the same way.
			key = cutBytes(key, LiveConnKeyMaxBytes)
			if prev, seen := a.sources[key]; !seen || at > prev {
				a.sources[key] = at
			}
		}
	}

	list := make([]*agg, 0, len(byID))
	for _, a := range byID {
		list = append(list, a)
	}
	sort.Slice(list, func(i, j int) bool {
		if list[i].dev.LastAtMS != list[j].dev.LastAtMS {
			return list[i].dev.LastAtMS > list[j].dev.LastAtMS
		}
		return list[i].key < list[j].key
	})
	out := make([]UserDevice, 0, len(list))
	for _, a := range list {
		keys := make([]string, 0, len(a.sources))
		for k := range a.sources {
			keys = append(keys, k)
		}
		sort.Slice(keys, func(i, j int) bool {
			if a.sources[keys[i]] != a.sources[keys[j]] {
				return a.sources[keys[i]] > a.sources[keys[j]]
			}
			return keys[i] < keys[j]
		})
		if len(keys) > UserDeviceSourcesMax {
			a.dev.SourcesMore = len(keys) - UserDeviceSourcesMax
			keys = keys[:UserDeviceSourcesMax]
		}
		a.dev.Sources = keys
		out = append(out, a.dev)
	}
	return out
}
