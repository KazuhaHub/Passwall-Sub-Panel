package domain

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/netip"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	nodeprotocol "github.com/KazuhaHub/passwall-protocol/protocol"
)

// Source guard (ADR 0044).
//
// A direct line, a direct forward (managed or external) and a CDN line all end
// on the landing's own listener, which accepts every identity of that listener:
// without a check, a user could use one line's identity through another line's
// path and be billed at the wrong line's factor. The landing therefore refuses
// a connection whose source address does not belong to the identity's line. A
// relay line owns a set of source addresses; the direct line owns every other
// address. This file is the panel's side of the sets: what a line's addresses
// are, how they are written, and when a landing cannot enforce them.

const (
	// MaxSourceCIDRs bounds one line's own addresses (the protocol's limit for a
	// whole source set).
	MaxSourceCIDRs = 1024
)

// Guarded reports whether the line ends on the landing's own listener, and so
// needs its identity checked by source: a direct forward, an external line or a
// CDN line. A tunnel line has a listener of its own.
func (r RelayLine) Guarded() bool { return !r.Tunnel() }

// NormalizeSourceCIDRs validates an administrator's list of source addresses
// and returns it in canonical form, sorted and unique. Every entry is a
// prefix; a bare address is read as a single-host prefix. A prefix with host
// bits set, an IPv4-mapped IPv6 prefix and a zone are refused, and so is more
// than MaxSourceCIDRs entries or a repeated one: one network has one spelling,
// which is what lets a landing compare sets byte for byte.
func NormalizeSourceCIDRs(in []string) ([]string, error) {
	if len(in) == 0 {
		return nil, nil
	}
	if len(in) > MaxSourceCIDRs {
		return nil, fmt.Errorf("%w: source_cidrs has %d entries, the maximum is %d", ErrValidation, len(in), MaxSourceCIDRs)
	}
	seen := make(map[netip.Prefix]struct{}, len(in))
	prefixes := make([]netip.Prefix, 0, len(in))
	for _, raw := range in {
		text := strings.TrimSpace(raw)
		prefix, err := parseSourcePrefix(text)
		if err != nil {
			return nil, fmt.Errorf("%w: source address %q: %v", ErrValidation, text, err)
		}
		if _, dup := seen[prefix]; dup {
			return nil, fmt.Errorf("%w: source address %q is listed twice", ErrValidation, text)
		}
		seen[prefix] = struct{}{}
		prefixes = append(prefixes, prefix)
	}
	return prefixStrings(prefixes), nil
}

func parseSourcePrefix(text string) (netip.Prefix, error) {
	if !strings.Contains(text, "/") {
		addr, err := netip.ParseAddr(text)
		if err != nil {
			return netip.Prefix{}, err
		}
		if addr.Zone() != "" || addr.Is4In6() {
			return netip.Prefix{}, fmt.Errorf("an IPv4-mapped or zoned address is not a source address; write the IPv4 address")
		}
		return netip.PrefixFrom(addr, addr.BitLen()), nil
	}
	prefix, err := netip.ParsePrefix(text)
	if err != nil {
		return netip.Prefix{}, err
	}
	if prefix.Addr().Zone() != "" || prefix.Addr().Is4In6() {
		return netip.Prefix{}, fmt.Errorf("an IPv4-mapped or zoned prefix is not a source address; write the IPv4 prefix")
	}
	if prefix.Masked() != prefix {
		return netip.Prefix{}, fmt.Errorf("has host bits set (want %s)", prefix.Masked())
	}
	return prefix, nil
}

func prefixStrings(prefixes []netip.Prefix) []string {
	sort.Slice(prefixes, func(i, j int) bool {
		if c := prefixes[i].Addr().Compare(prefixes[j].Addr()); c != 0 {
			return c < 0
		}
		return prefixes[i].Bits() < prefixes[j].Bits()
	})
	out := make([]string, len(prefixes))
	for i, p := range prefixes {
		out[i] = p.String()
	}
	return out
}

// SourceInputs are the facts an effective source set is made from besides the
// line itself.
type SourceInputs struct {
	// EntryAddresses are the public addresses a managed entry server is seen
	// from, by server: what it reported of its own interfaces and the address
	// the panel saw its sync come from. Plain addresses, not prefixes.
	EntryAddresses map[int64][]string
	// Cloudflare are the current Cloudflare ranges, used by a CDN line that
	// names no addresses of its own.
	Cloudflare []string
}

// EffectiveSourceCIDRs is the set of source addresses the landing should accept
// the line's identity from, canonical, sorted and unique; empty means unknown
// (the line is then not guarded).
//
//   - managed direct forward: the entry server's addresses plus the line's own;
//   - CDN: the line's own when it has any, otherwise Cloudflare's;
//   - external: the line's own.
//
// A tunnel line has no set.
func (r RelayLine) EffectiveSourceCIDRs(in SourceInputs) []string {
	if !r.Guarded() {
		return nil
	}
	var prefixes []netip.Prefix
	add := func(text string) {
		if p, err := parseSourcePrefix(text); err == nil {
			prefixes = append(prefixes, p)
		}
	}
	for _, own := range r.SourceCIDRs {
		add(own)
	}
	switch {
	case r.CDN():
		if len(r.SourceCIDRs) == 0 {
			for _, cidr := range in.Cloudflare {
				add(cidr)
			}
		}
	case r.Managed():
		for _, addr := range in.EntryAddresses[r.EntryPanelID] {
			add(strings.TrimSpace(addr))
		}
	}
	seen := make(map[netip.Prefix]struct{}, len(prefixes))
	unique := prefixes[:0]
	for _, p := range prefixes {
		if _, dup := seen[p]; !dup {
			seen[p] = struct{}{}
			unique = append(unique, p)
		}
	}
	if len(unique) > MaxSourceCIDRs {
		// The line's own addresses come first, so what is dropped is the tail of
		// the entry's: a set the landing would refuse as a whole is worse.
		unique = unique[:MaxSourceCIDRs]
	}
	if len(unique) == 0 {
		return nil
	}
	return prefixStrings(unique)
}

// SourceSetSuffix is the suffix of a line's source set key: the landing and a hash
// of the line id, so that a key is stable, short and never reveals a line id.
// The protocol's SourceSetKey is "src_" + this.
func SourceSetSuffix(nodeID int64, lineID string) string {
	sum := sha256.Sum256([]byte(lineID))
	return strconv.FormatInt(nodeID, 10) + "_" + hex.EncodeToString(sum[:])[:10]
}

// SourceGuardFactorProblem is the first line ending on the landing's listener
// whose traffic factor differs from the direct line's, or false when they all
// agree. A landing that cannot guard needs them to; a landing that can needs it
// of nothing but an external line without addresses (it must name addresses, see SourceGuardWithheld).
func (n *Node) SourceGuardFactorProblem() (RelayLine, bool) {
	if n == nil {
		return RelayLine{}, false
	}
	direct := n.EffectiveTrafficFactor()
	for _, line := range n.Relays {
		if line.Guarded() && line.EffectiveTrafficFactor() != direct {
			return line, true
		}
	}
	return RelayLine{}, false
}

// SourceGuardWithheld says why a relay line must not be published on a landing,
// or "" when it may. canGuard is whether the landing enforces source guards.
//
//   - A landing that cannot guard cannot tell the lines apart: a line whose
//     factor differs from the direct line's would be billable at the wrong one.
//   - A landing that can guard still has nothing to check an external line
//     against when it names no addresses, and a managed forward whose entry is
//     not yet known would be unguarded, so the line is not published until the
//     address is known (ADR 0044 section 4).
func (n *Node) SourceGuardWithheld(line RelayLine, canGuard, entryKnown bool) string {
	if n == nil || !line.Guarded() {
		return ""
	}
	differs := line.EffectiveTrafficFactor() != n.EffectiveTrafficFactor()
	if !canGuard {
		if differs {
			return SourceWithheldFactor
		}
		return ""
	}
	known := len(line.SourceCIDRs) > 0 || line.CDN() || (line.Managed() && entryKnown)
	switch {
	case line.Managed() && !known:
		return SourceWithheldUnknown
	case differs && !known:
		return SourceWithheldUnknown
	}
	return ""
}

// Reasons a relay line is withheld by the source guard.
const (
	// SourceWithheldFactor: the landing cannot guard and the line's factor
	// differs from the direct line's.
	SourceWithheldFactor = "source_guard_factor"
	// SourceWithheldUnknown: the landing guards and the line's source addresses
	// are not known.
	SourceWithheldUnknown = "source_guard_unknown"
)

// SourceGuardHit is the latest report of connections a landing refused because
// their source did not belong to the identity's line (ADR 0044): how many came
// from IP and were matched by Set on a listener, and when the last was seen.
type SourceGuardHit struct {
	PanelID int64
	// Set is the wire key of the source set that refused it (see
	// SourceSetSuffix); SetNodeID and the line are read back from it.
	Set string
	// ListenerNodeID is the listener that refused the connection.
	ListenerNodeID int64
	IP             string
	Count          int64
	LastSeen       time.Time
}

// CloudflareRanges are the Cloudflare address ranges a CDN line is checked
// against when it names none of its own.
type CloudflareRanges struct {
	CIDRs     []string
	FetchedAt time.Time
}

// CanSourceGuard reports whether the server's agent enforces source guards:
// whether a landing on it can tell a relay line's traffic from a direct one. A
// 3X-UI or S-UI server has no agent and cannot.
func (a *NodeAgent) CanSourceGuard() bool {
	return a != nil && slices.Contains(a.ObservedCapabilities, nodeprotocol.CapabilitySourceGuard)
}

// SourceAddresses are the public addresses a landing sees this server's
// outbound connections from: what its last full report listed for its own
// interfaces, and the address the panel saw its sync come from. Private,
// shared and documentation addresses are left out, since a landing never sees
// them. Plain addresses, sorted and unique.
func (a *NodeAgent) SourceAddresses() []string {
	if a == nil {
		return nil
	}
	seen := map[netip.Addr]struct{}{}
	var addrs []netip.Addr
	add := func(text string) {
		addr, err := netip.ParseAddr(strings.TrimSpace(text))
		if err != nil {
			return
		}
		addr = addr.Unmap()
		if _, dup := seen[addr]; dup || !nodeprotocol.IsPublicSourceAddr(addr) {
			return
		}
		seen[addr] = struct{}{}
		addrs = append(addrs, addr)
	}
	for _, text := range a.InterfaceAddresses {
		add(text)
	}
	add(a.ObservedAddress)
	if len(addrs) == 0 {
		return nil
	}
	sort.Slice(addrs, func(i, j int) bool { return addrs[i].Compare(addrs[j]) < 0 })
	out := make([]string, len(addrs))
	for i, addr := range addrs {
		out[i] = addr.String()
	}
	return out
}
