package domain

import (
	"fmt"
	"net/netip"
	"sort"
	"strings"
	"unicode"
)

// Address hygiene for the concurrent-location check: which of a user's
// addresses are live right now, which of them are one source, and which say
// nothing about where the PERSON is.
//
// Every rule here errs toward excluding. An address that is excluded costs a
// sharer one data point; an address that is wrongly kept can make an honest
// user read as being in two places at once, and that is the verdict an
// operator acts on.

const (
	// LiveIPFreshWindowSeconds is how far behind its node's newest scan a
	// sighting may be and still count as live. 3X-UI rescans every 10
	// seconds and restamps every address whose stream is still open, so a
	// live address is never more than one scan behind; 120 is 12 scans of
	// slack for a busy or briefly stalled job. Compared on the panel's own
	// clock only (see LiveIPSighting).
	LiveIPFreshWindowSeconds = 120
	// SharedExitMinUsers is how many distinct accounts must hold one
	// source at the same moment before it reads as a shared exit (a
	// campus, a carrier NAT, an office) rather than a place. Two accounts
	// on one address is a household; three is infrastructure.
	SharedExitMinUsers = 3
	// GeoIgnoreListMaxEntries bounds the admin ignore list, which
	// Contains scans linearly for every kept source on every poll.
	GeoIgnoreListMaxEntries = 256
)

// cgnatPrefix is RFC 6598 shared address space. netip's IsPrivate does not
// cover it, yet it is an "internal" range a subscriber's traffic can still
// arrive from — a node reached from inside a carrier NAT or an overlay
// network — and whoever holds such an address shares it with strangers.
var cgnatPrefix = netip.MustParsePrefix("100.64.0.0/10")

// NodeRef names one upstream node: a panel's node guid is unique only
// within that panel.
type NodeRef struct {
	PanelID int64
	Node    string
}

// GeoIgnoreList is the admin's list of addresses and networks that never
// describe where a user is: a relay the panel does not know about, a CDN, an
// office exit. Parsed once per poll; the zero value ignores nothing.
type GeoIgnoreList struct{ prefixes []netip.Prefix }

// ParseGeoIgnoreList reads one entry per line or comma, "#" to end of line
// being a comment. An entry is an address or a CIDR.
//
// The returned list always carries every valid entry, even alongside an
// error. The settings PUT uses the error to reject a typo up front, because
// a typo here fails OPEN — the address the admin meant to exclude keeps
// accusing someone — and nothing downstream would ever repair it. The poll
// uses the list regardless, so one bad line stored before validation existed
// does not switch off every good one.
func ParseGeoIgnoreList(raw string) (GeoIgnoreList, error) {
	var (
		list GeoIgnoreList
		bad  []string
	)
	for _, line := range strings.Split(raw, "\n") {
		if i := strings.IndexByte(line, '#'); i >= 0 {
			line = line[:i]
		}
		tokens := strings.FieldsFunc(line, func(r rune) bool { return r == ',' || unicode.IsSpace(r) })
		for _, tok := range tokens {
			p, ok := parseIgnoreEntry(tok)
			if !ok {
				bad = append(bad, tok)
				continue
			}
			list.prefixes = append(list.prefixes, p)
		}
	}
	if len(bad) > 0 {
		return list, fmt.Errorf("%w: invalid ignore-list entries: %s", ErrValidation, strings.Join(bad, ", "))
	}
	if n := len(list.prefixes); n > GeoIgnoreListMaxEntries {
		return list, fmt.Errorf("%w: at most %d ignore-list entries (got %d)", ErrValidation, GeoIgnoreListMaxEntries, n)
	}
	return list, nil
}

// parseIgnoreEntry turns one token into the prefix Contains matches against.
//
// Everything is normalised to the form ClassifyAddresses checks: unmapped,
// zone-free, host bits masked. netip matches no IPv4 address against an
// IPv4-mapped IPv6 prefix, so an entry left mapped would silently match
// nothing — the fail-open this list must never have. A mapped prefix shorter
// than /96 reaches outside the mapped range and has no IPv4 meaning, so it
// is rejected rather than guessed at.
func parseIgnoreEntry(tok string) (netip.Prefix, bool) {
	if strings.Contains(tok, "/") {
		p, err := netip.ParsePrefix(tok)
		if err != nil {
			return netip.Prefix{}, false
		}
		if p.Addr().Is4In6() {
			if p.Bits() < 96 {
				return netip.Prefix{}, false
			}
			p = netip.PrefixFrom(p.Addr().Unmap(), p.Bits()-96)
		}
		return p.Masked(), true
	}
	a, err := netip.ParseAddr(tok)
	// A zone names an interface on some host, not an address anybody
	// connects from; accepting it would store an entry that matches nothing.
	if err != nil || a.Zone() != "" {
		return netip.Prefix{}, false
	}
	a = a.Unmap()
	return netip.PrefixFrom(a, a.BitLen()), true
}

// Contains reports whether a is on the list. The address is unmapped and
// zone-stripped first, for the same reason the entries are.
func (l GeoIgnoreList) Contains(a netip.Addr) bool {
	a = a.Unmap().WithZone("")
	for _, p := range l.prefixes {
		if p.Contains(a) {
			return true
		}
	}
	return false
}

// AddressExclusions is what ClassifyAddresses may exclude, in precedence
// order. Each field off means that rule excludes nothing.
type AddressExclusions struct {
	// Internal excludes carrier-grade NAT (100.64.0.0/10), private,
	// loopback, link-local and unspecified addresses. None of them can be
	// placed on a map, and a CGNAT address in particular is shared by
	// strangers behind one carrier gateway.
	Internal bool
	// Ignore is the admin list (global only).
	Ignore GeoIgnoreList
	// Infra matches PSP's own node and relay addresses: traffic arriving
	// from a relay carries the relay's address, not the user's. nil = none.
	Infra func(netip.Addr) bool
	// SharedMinUsers is the shared-exit threshold; 0 disables the rule.
	SharedMinUsers int
}

// SourceAddr is one source a user connected from. An IPv6 source is a whole
// /64: a single phone rotates privacy addresses inside its /64 every few
// hours, and counting each one would turn one handset into a crowd.
type SourceAddr struct {
	// Key identifies the source: "1.2.3.4", "2001:db8:1:2::/64", or the
	// raw string when it did not parse.
	Key string
	// LookupIP is the address the geo database is asked about: the
	// smallest member, so the choice is stable from poll to poll.
	LookupIP string
	// Network is display-only: the IPv4 /24 or IPv6 /48 around LookupIP,
	// "" if unparseable. Never used to merge sources — two households in
	// one /24 are still two households.
	Network string
}

// GeoExcluded counts the sources each rule removed, so a verdict can say
// what it did NOT look at. Counted per source, not per address.
type GeoExcluded struct {
	Shared   int `json:"shared"`
	Listed   int `json:"listed"`
	Infra    int `json:"infra"`
	Internal int `json:"internal"`
}

// Total is every excluded source.
func (e GeoExcluded) Total() int {
	return e.Shared + e.Listed + e.Infra + e.Internal
}

// UserAddresses is one user's sources after hygiene: what is left to place,
// and what was set aside and why.
type UserAddresses struct {
	UserID int64
	// Kept is sorted by Key.
	Kept     []SourceAddr
	Excluded GeoExcluded
	// Stale is how many window addresses were not live at poll time.
	Stale int
}

// SourceKey maps an address to the source it belongs to. ok is false when
// ip does not parse; key is then the trimmed string itself, so an address
// PSP cannot read is still counted rather than silently dropped.
//
// addr is the member itself (unmapped, zone-free), not the network: the
// caller needs a concrete address to look up.
func SourceKey(ip string) (key string, addr netip.Addr, ok bool) {
	s := strings.TrimSpace(ip)
	a, err := netip.ParseAddr(s)
	if err != nil {
		return s, netip.Addr{}, false
	}
	a = a.Unmap().WithZone("")
	if a.Is4() {
		return a.String(), a, true
	}
	return netip.PrefixFrom(a, 64).Masked().String(), a, true
}

// FreshLiveIPs decides, per panel, which sightings were live at poll time,
// and returns copies of the panels with Fresh set plus this poll's per-node
// reference timestamps. Pure: the caller owns prev (and the lock around it)
// and merges the returned references into it with max.
//
// The upstream's window is 30 minutes of MEMORY, not a picture of now: an
// address stays listed for half an hour after its stream closed. Read as
// "concurrent", one commuter's phone is in three cities at once. What does
// distinguish live from remembered is the timestamp, which 3X-UI restamps on
// every 10-second scan that still finds the stream open. So, per node:
//
//   - ref is the newest timestamp on that node, over EVERY email — owned or
//     not, because the node's clock is the node's;
//   - the node has advanced when ref is newer than last poll's ref (or there
//     was none: the first poll after a restart has nothing to compare to and
//     trusts the data once, as v1 always did);
//   - a sighting is fresh when it has no timestamp (nothing to judge — read
//     as live, the v1 behaviour and all a PSP-native node offers), or its
//     node advanced and it is within LiveIPFreshWindowSeconds of ref.
//
// A node nobody is streaming through stops being rescanned, so its frozen
// batch keeps the same ref poll after poll and reads entirely stale — which
// is the point: otherwise yesterday's commute is replayed as evidence all
// day. Comparing only timestamps from one node also makes any skew between
// PSP's clock and the panel's irrelevant. A panel clock that steps BACKWARDS
// reads as stale until it passes its old newest timestamp again; that is
// the safe direction (idle, never accused).
//
// An unread panel passes through with Fresh nil and contributes no
// reference. A plain-reader panel (Sightings nil) gets Fresh = ByEmail.
func FreshLiveIPs(panels []PanelLiveIPs, prev map[NodeRef]int64) ([]PanelLiveIPs, map[NodeRef]int64) {
	out := make([]PanelLiveIPs, len(panels))
	next := map[NodeRef]int64{}
	for i, p := range panels {
		out[i] = p
		if p.Err != nil {
			out[i].Fresh = nil
			continue
		}
		if p.Sightings == nil {
			out[i].Fresh = p.ByEmail
			continue
		}

		ref := map[string]int64{}
		for _, list := range p.Sightings {
			for _, s := range list {
				if s.SeenAt > ref[s.Node] {
					ref[s.Node] = s.SeenAt
				}
			}
		}
		// Only timestamped nodes are in ref, so only they get a reference:
		// a stored 0 could not be told apart from "never seen".
		advanced := make(map[string]bool, len(ref))
		for node, r := range ref {
			k := NodeRef{PanelID: p.PanelID, Node: node}
			last := prev[k]
			advanced[node] = last == 0 || r > last
			if r > next[k] {
				next[k] = r
			}
		}

		// Non-nil even when empty: "computed, nobody is live" must not read
		// as "not computed", which the aggregator would fill from ByEmail.
		fresh := make(map[string][]string, len(p.Sightings))
		for email, list := range p.Sightings {
			if email == "" {
				continue
			}
			set := map[string]struct{}{}
			for _, s := range list {
				ip := strings.TrimSpace(s.IP)
				if ip == "" {
					continue
				}
				if s.SeenAt <= 0 || (advanced[s.Node] && s.SeenAt >= ref[s.Node]-LiveIPFreshWindowSeconds) {
					set[ip] = struct{}{}
				}
			}
			if len(set) > 0 {
				fresh[email] = sortedKeys(set)
			}
		}
		out[i].Fresh = fresh
	}
	return out, next
}

// ClassifyAddresses applies the exclusions to every user's LIVE addresses
// (UserLiveIPs.Fresh) and groups what is left into sources.
//
// Each source is excluded for the first reason that matches, in a fixed
// order — internal, listed, infrastructure, shared — so the counts an admin
// reads are stable: a private address that is also on the ignore list
// reports as internal, the more fundamental reason; three users arriving
// through one relay report as infrastructure, not as a shared exit.
//
// The shared-exit rule counts distinct USERS holding a source at the same
// moment, which is why this runs over the whole fleet at once rather than
// per user.
//
// Every input user gets a row, idle ones included, so a caller can tell
// "idle" from "not looked at".
func ClassifyAddresses(users map[int64]UserLiveIPs, ex AddressExclusions) map[int64]UserAddresses {
	perUser := make(map[int64]map[string]*sourceMembers, len(users))
	holders := map[string]int{}
	for uid, u := range users {
		sources := map[string]*sourceMembers{}
		for _, ip := range u.Fresh {
			key, a, ok := SourceKey(ip)
			if key == "" {
				continue
			}
			m := sources[key]
			if m == nil {
				m = &sourceMembers{}
				sources[key] = m
			}
			if ok {
				m.addrs = append(m.addrs, a)
			} else {
				m.raw = key
			}
		}
		for key := range sources {
			holders[key]++
		}
		perUser[uid] = sources
	}

	out := make(map[int64]UserAddresses, len(users))
	for uid, u := range users {
		row := UserAddresses{
			UserID: uid,
			Kept:   []SourceAddr{},
			Stale:  max(0, len(u.IPs)-len(u.Fresh)),
		}
		for key, m := range perUser[uid] {
			switch {
			case ex.Internal && m.any(isInternalAddr):
				row.Excluded.Internal++
			case m.any(ex.Ignore.Contains):
				row.Excluded.Listed++
			case ex.Infra != nil && m.any(ex.Infra):
				row.Excluded.Infra++
			case ex.SharedMinUsers > 0 && holders[key] >= ex.SharedMinUsers:
				row.Excluded.Shared++
			default:
				row.Kept = append(row.Kept, m.source(key))
			}
		}
		sort.Slice(row.Kept, func(i, j int) bool { return row.Kept[i].Key < row.Kept[j].Key })
		out[uid] = row
	}
	return out
}

// sourceMembers is what one source key collected from one user's addresses:
// the parsed members, or the raw string when the address did not parse.
type sourceMembers struct {
	addrs []netip.Addr
	raw   string
}

// any reports whether some parsed member satisfies f. An unparseable source
// has no member any address rule can match, so only the shared rule can
// exclude it.
func (m *sourceMembers) any(f func(netip.Addr) bool) bool {
	for _, a := range m.addrs {
		if f(a) {
			return true
		}
	}
	return false
}

// source builds the kept SourceAddr. The lookup address is the smallest
// member so a phone rotating inside its /64 is looked up at a stable address
// and cannot hop between two database rows from one poll to the next.
func (m *sourceMembers) source(key string) SourceAddr {
	if len(m.addrs) == 0 {
		return SourceAddr{Key: key, LookupIP: m.raw}
	}
	lowest := m.addrs[0]
	for _, a := range m.addrs[1:] {
		if a.Compare(lowest) < 0 {
			lowest = a
		}
	}
	bits := 48
	if lowest.Is4() {
		bits = 24
	}
	return SourceAddr{
		Key:      key,
		LookupIP: lowest.String(),
		Network:  netip.PrefixFrom(lowest, bits).Masked().String(),
	}
}

// isInternalAddr is the Internal rule: nothing in these ranges can be placed
// on a map, and none of it is a place a subscriber is in.
func isInternalAddr(a netip.Addr) bool {
	return cgnatPrefix.Contains(a) ||
		a.IsPrivate() ||
		a.IsLoopback() ||
		a.IsLinkLocalUnicast() ||
		a.IsLinkLocalMulticast() ||
		a.IsUnspecified()
}
