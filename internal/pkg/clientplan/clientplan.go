// Package clientplan computes a user's DESIRED client rows on one panel: one
// psp_client per subscription LINE the user can see (ADR 0043), each attached
// to the single listener that line's traffic arrives on. It is pure (no I/O) so
// live enrollment and the 4.3 upgrade build the same plan, and so it is
// exhaustively testable.
//
// Before 4.3 the planner packed every node of a panel into the fewest shared
// clients. A line is now what carries a price (its traffic factor), so it must
// own a counter: a relay that forwards to the landing's listener would
// otherwise report into the direct line's client and could not be charged at
// its own factor.
//
//   - The direct line of a node uses the user's own UUID and the legacy
//     password classes, which are byte-identical to what every earlier release
//     rendered, so a user who only uses direct lines sees no change.
//   - Every other line uses domain.DeriveLineUUID, re-derived with the next
//     attempt when it collides with another client on the same listener.
//   - A password-using protocol takes its class from the protocol exactly as
//     before (SS-2022 needs a PSK derived from the row's UUID).
//
// Existing rows are matched by their line and keep their ID, email and
// counters. A legacy row (written before 4.3, no line) is adopted as the
// direct line of the lowest-ID node, the same rule the 4.3 upgrade applies.
package clientplan

import (
	"encoding/json"
	"sort"
	"strconv"
	"strings"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/pkg/crypto"
)

const (
	// Password classes. The class fixes the client's single `password` field, and
	// every class is BYTE-IDENTICAL to what the legacy per-node DeriveProxyPassword
	// emitted for that protocol — so consolidating a node into its shared client
	// never changes the rendered credential. That is what makes the v3.9.0
	// migration SILENT: no subscriber re-fetch, and render can keep deriving.
	pwClassDefault = 0 // password = the raw UUID. Trojan/SS use it as the password; VLESS/VMess (id) + Hy2 (auth) use the UUID and ignore the password field. Covers everything except SS-2022.
	pwClass256     = 1 // SS-2022 with a 32-byte PSK (aes-256-gcm / chacha20): base64(sha256(uuid))
	pwClass128     = 2 // SS-2022 with a 16-byte PSK (aes-128-gcm): base64(sha256(uuid))[:16]

	// flowVision is the only non-empty VLESS flow current Xray uses. The flow
	// dimension is allowlisted to {"", flowVision}; any other value coerces to ""
	// (no real Xray VLESS flow falls outside this, and hashing arbitrary flows
	// into a client would risk an unprovisionable wrong-flow client).
	flowVision = "xtls-rprx-vision"
)

// NodeCred describes one node a user can reach on a panel — just enough to
// assign it a partition key and generate the right password.
type NodeCred struct {
	NodeID   int64
	Protocol domain.Protocol
	SSMethod string // disambiguates the SS-2022 key length (16 vs 32 bytes)
	Flow     string // VLESS flow (only VLESS uses it)
}

// NodeCredFromNode derives a NodeCred from a captured node, reading the protocol
// from its cached Node.Protocol and the SS cipher from the inbound-settings
// snapshot. Both feed crypto.DetectProtocol so an SS-2022 cipher is recognised.
//
// Caveat — an UNcaptured node (empty InboundSettings) yields an empty method, so
// crypto.DetectProtocol classifies a Shadowsocks inbound as plain SS, not
// SS-2022, dropping it into the default (raw-UUID) class. With the silent scheme
// that is now WRONG for BOTH SS-2022 key lengths: their password is a PSK, not
// the UUID, so a mis-classed SS-2022 node would render an unusable credential.
// The migration MUST therefore resolve the live inbound (to read the method)
// before planning any Shadowsocks node; the captured-node path is exact.
func NodeCredFromNode(n *domain.Node) NodeCred {
	method := ssMethodFromSettings(n.InboundSettings)
	return NodeCred{
		NodeID:   n.ID,
		Protocol: crypto.DetectProtocol(n.DesiredProtocol, method),
		SSMethod: method,
		Flow:     n.Flow,
	}
}

// NodeCredsFromNodes maps a node slice through NodeCredFromNode, skipping
// separators and any node whose protocol can't be determined (DetectProtocol
// returns "") — such a node can't be provisioned as a client and is left out.
func NodeCredsFromNodes(nodes []*domain.Node) []NodeCred {
	out := make([]NodeCred, 0, len(nodes))
	for _, n := range nodes {
		if n == nil || n.Kind == domain.NodeKindSeparator {
			continue
		}
		nc := NodeCredFromNode(n)
		if nc.Protocol == "" {
			continue
		}
		out = append(out, nc)
	}
	return out
}

func ssMethodFromSettings(settings string) string {
	if settings == "" {
		return ""
	}
	var s struct {
		Method string `json:"method"`
	}
	if err := json.Unmarshal([]byte(settings), &s); err != nil {
		return ""
	}
	return s.Method
}

// LineCred describes one subscription line a user can see on a panel.
type LineCred struct {
	Line domain.LineKey
	// ListenerNodeID is the node whose listener holds this line's client. It
	// is the line's own node for the direct line and for relay lines (a relay
	// forwards to the landing's listener); a line served by a listener of its
	// own names that listener's node.
	ListenerNodeID int64
	Protocol       domain.Protocol
	SSMethod       string
	Flow           string
	// FactorMilli is the line's traffic factor, recorded on a new row so a
	// delta booked after the line is deleted keeps the line's price. nil
	// leaves the column default (one-for-one).
	FactorMilli *int
}

// LineCredsFromNodes lists the lines of each node: its direct line and every
// enabled relay line. Separators and nodes whose protocol is unknown have no
// client. The caller passes the nodes as the user may see them (lines the
// user's group may not see already removed).
func LineCredsFromNodes(nodes []*domain.Node) []LineCred {
	out := make([]LineCred, 0, len(nodes))
	for _, nc := range NodeCredsFromNodes(nodes) {
		out = append(out, LineCred{Line: domain.DirectLine(nc.NodeID), ListenerNodeID: nc.NodeID,
			Protocol: nc.Protocol, SSMethod: nc.SSMethod, Flow: nc.Flow, FactorMilli: factorOf(nodes, nc.NodeID)})
	}
	for _, n := range nodes {
		if n == nil || n.Kind == domain.NodeKindSeparator {
			continue
		}
		nc := NodeCredFromNode(n)
		if nc.Protocol == "" {
			continue
		}
		for _, r := range n.Relays {
			// A tunnel line lives on another server's listener: see
			// TunnelLineCreds.
			if !r.Enabled || r.ID == "" || r.Tunnel() {
				continue
			}
			out = append(out, LineCred{Line: domain.LineKey{NodeID: n.ID, LineID: r.ID}, ListenerNodeID: n.ID,
				Protocol: nc.Protocol, SSMethod: nc.SSMethod, Flow: nc.Flow, FactorMilli: intPtr(r.EffectiveTrafficFactor())})
		}
	}
	return out
}

// TunnelLineCreds lists, per server, the tunnel-mode lines of the given nodes
// (the nodes as the user may see them). A tunnel line is a line of its landing
// node, so its client is keyed by (landing, line) and carries the landing's
// per-line identity, but it lives on the line's own listener on the entry
// server: the listener's server is the panel, and the listener's protocol and
// flow decide the credential. A line whose listener is gone has no client.
func TunnelLineCreds(nodes []*domain.Node) map[int64][]LineCred {
	out := map[int64][]LineCred{}
	for _, n := range nodes {
		if n == nil || n.Kind == domain.NodeKindSeparator {
			continue
		}
		for _, r := range n.Relays {
			if !r.Enabled || r.ID == "" || !r.Tunnel() || r.TunnelNode == nil {
				continue
			}
			child := NodeCredFromNode(r.TunnelNode)
			if child.Protocol == "" {
				continue
			}
			out[r.TunnelNode.PanelID] = append(out[r.TunnelNode.PanelID], LineCred{
				Line: domain.LineKey{NodeID: n.ID, LineID: r.ID}, ListenerNodeID: r.TunnelNode.ID,
				Protocol: child.Protocol, SSMethod: child.SSMethod, Flow: child.Flow, FactorMilli: intPtr(r.EffectiveTrafficFactor())})
		}
	}
	return out
}

// factorOf is the direct-line factor of the node with this ID.
func factorOf(nodes []*domain.Node, id int64) *int {
	for _, n := range nodes {
		if n != nil && n.ID == id {
			return intPtr(n.EffectiveTrafficFactor())
		}
	}
	return nil
}

func intPtr(v int) *int { return &v }

// pwAgnostic marks a node whose protocol doesn't use the `password` field at all
// (VLESS/VMess authenticate by id; Hysteria2 by auth).
const pwAgnostic = -1

// pwReqFor returns the password CLASS a protocol requires, or pwAgnostic.
func pwReqFor(nc LineCred) int {
	// domain.Protocol.UsesPassword is the single source of truth for which
	// protocols read the password slot; only the class split lives here.
	if !nc.Protocol.UsesPassword() {
		return pwAgnostic
	}
	if nc.Protocol == domain.ProtoSS2022 {
		if crypto.SS2022KeyLen(nc.SSMethod) == 16 {
			return pwClass128
		}
		return pwClass256
	}
	return pwClassDefault
}

// effectiveFlow coerces a VLESS flow into the allowlist; other protocols have
// none.
func effectiveFlow(p domain.Protocol, flow string) string {
	if p == domain.ProtoVLESS && flow == flowVision {
		return flowVision
	}
	return ""
}

// ExistingClient is a persisted row the plan may keep.
type ExistingClient struct {
	ClientID int64
	// Line is the row's line; zero for a legacy row.
	Line  domain.LineKey
	UUID  string
	Email string
}

// DesiredClient is one row the plan wants, with its single attachment.
type DesiredClient struct {
	Client   domain.PSPClient
	Inbounds []domain.PSPClientInbound
}

// UUIDTaken reports whether uuid (compared through domain.LineUUIDKey) is
// already held by a client other than exceptClientID on the listener of
// listenerNodeID. nil means nothing is taken.
type UUIDTaken func(listenerNodeID int64, uuid string, exceptClientID int64) bool

// maxLineUUIDAttempts bounds the re-derivation. A collision of a 122-bit
// derived value is not expected at all; the bound only keeps a broken
// predicate from looping.
const maxLineUUIDAttempts = 8

// Build returns one desired row per line, in input order, with the IDs of the
// existing rows it keeps. taken may be nil.
func Build(userID int64, userUUID string, panelID int64, rules domain.EmailRules, lines []LineCred, existing []ExistingClient, taken UUIDTaken) []DesiredClient {
	if len(lines) == 0 {
		return nil
	}
	byLine := make(map[domain.LineKey]ExistingClient, len(existing))
	var legacy []ExistingClient
	for _, e := range existing {
		if e.Line.NodeID == 0 || e.Line.LineID == "" {
			legacy = append(legacy, e)
			continue
		}
		if prev, dup := byLine[e.Line]; !dup || e.ClientID < prev.ClientID {
			byLine[e.Line] = e
		}
	}
	// A legacy shared row becomes the direct line of the lowest-ID node that
	// has no row of its own yet.
	sort.Slice(legacy, func(i, j int) bool { return legacy[i].ClientID < legacy[j].ClientID })
	if len(legacy) > 0 {
		var lowest *domain.LineKey
		for i := range lines {
			l := lines[i].Line
			if !l.IsDirect() {
				continue
			}
			if _, has := byLine[l]; has {
				continue
			}
			if lowest == nil || l.NodeID < lowest.NodeID {
				lowest = &l
			}
		}
		if lowest != nil {
			adopted := legacy[0]
			adopted.Line = *lowest
			byLine[*lowest] = adopted
		}
	}

	seen := make(map[domain.LineKey]bool, len(lines))
	out := make([]DesiredClient, 0, len(lines))
	for _, lc := range lines {
		if seen[lc.Line] || lc.Line.NodeID == 0 || lc.Line.LineID == "" {
			continue
		}
		seen[lc.Line] = true
		listener := lc.ListenerNodeID
		if listener == 0 {
			listener = lc.Line.NodeID
		}
		prev, kept := byLine[lc.Line]
		uuid := userUUID
		if !lc.Line.IsDirect() {
			uuid = lineUUID(userUUID, lc.Line, listener, prev, taken)
		}
		class := pwReqFor(lc)
		if class == pwAgnostic {
			class = pwClassDefault
		}
		email := domain.PSPClientEmail(userID, domain.LineEmailSuffix(lc.Line), rules)
		if kept && prev.Email != "" {
			email = prev.Email
		}
		c := domain.PSPClient{
			UserID: userID, PanelID: panelID,
			NodeID: lc.Line.NodeID, LineID: lc.Line.LineID,
			CredClass: class, Email: email, UUID: uuid,
			Password: passwordForClass(class, uuid),
		}
		if kept {
			c.ID = prev.ClientID
		} else if lc.FactorMilli != nil {
			factor := *lc.FactorMilli
			c.LastFactorMilli = &factor
		}
		out = append(out, DesiredClient{
			Client:   c,
			Inbounds: []domain.PSPClientInbound{{NodeID: listener, FlowOverride: effectiveFlow(lc.Protocol, lc.Flow)}},
		})
	}
	return out
}

// lineUUID picks the first derivation attempt that no other client holds on
// the listener. A kept row whose current UUID is one of the attempts keeps it
// while it stays free, so an identity does not move just because the holder
// of an earlier attempt left.
func lineUUID(userUUID string, line domain.LineKey, listener int64, prev ExistingClient, taken UUIDTaken) string {
	first := domain.DeriveLineUUID(userUUID, line, 1)
	if taken == nil {
		return first
	}
	for attempt := 1; attempt <= maxLineUUIDAttempts && prev.UUID != ""; attempt++ {
		if candidate := domain.DeriveLineUUID(userUUID, line, attempt); candidate == prev.UUID {
			if !taken(listener, candidate, prev.ClientID) {
				return candidate
			}
			break
		}
	}
	for attempt := 1; attempt <= maxLineUUIDAttempts; attempt++ {
		candidate := domain.DeriveLineUUID(userUUID, line, attempt)
		if !taken(listener, candidate, prev.ClientID) {
			return candidate
		}
	}
	return first
}

// IsSharedClientEmail reports whether email is one the v3.9.0 shared-client scheme
// could have minted for userID — on ANY panel and ANY domain, under ANY shipped
// suffix version. It matches "u{id}@d" (the bare/merged form), "u{id}-k{8hex}@d"
// (a partition content-hash), and the RETIRED literal "u{id}-c{n}@d" (an early-beta
// SS-2022 key-length suffix). It is domain- and suffix-version-agnostic on purpose:
// the orphan reconcile must recognise a client minted under an older scheme (a
// reconstruct-the-email approach would miss those). It deliberately does NOT match
// the legacy per-NODE email "u{id}-n{nodeID}@d" (domain.User.ClientEmail) — those
// are the migration's enforcement fallback, owned by the legacy-cleanup path, and
// must never be swept as shared-client orphans.
func IsSharedClientEmail(email string, userID int64) bool {
	prefix := "u" + strconv.FormatInt(userID, 10)
	if !strings.HasPrefix(email, prefix) {
		return false
	}
	rest := email[len(prefix):]
	at := strings.IndexByte(rest, '@')
	if at < 0 {
		return false
	}
	return isSharedSuffix(rest[:at])
}

func isSharedSuffix(s string) bool {
	switch {
	case s == "": // bare: u{id}@  (default class or the lone-client form)
		return true
	case strings.HasPrefix(s, "-k"): // content-hash: -k{8 hex}
		h := s[2:]
		if len(h) != 8 {
			return false
		}
		for _, ch := range h {
			if !(ch >= '0' && ch <= '9' || ch >= 'a' && ch <= 'f') {
				return false
			}
		}
		return true
	case strings.HasPrefix(s, "-l"): // per-line (4.3): -l{10 hex}
		h := s[2:]
		if len(h) != 10 {
			return false
		}
		for _, ch := range h {
			if !(ch >= '0' && ch <= '9' || ch >= 'a' && ch <= 'f') {
				return false
			}
		}
		return true
	case strings.HasPrefix(s, "-c"): // retired literal: -c{digits}
		d := s[2:]
		if d == "" {
			return false
		}
		for _, ch := range d {
			if ch < '0' || ch > '9' {
				return false
			}
		}
		return true
	default:
		return false // notably -n{nodeID} (legacy per-node) falls here — NOT a shared client
	}
}

// passwordForClass returns the single stored password for a partition's pwClass.
// Each is exactly what the legacy DeriveProxyPassword produced for that protocol,
// so the migration is silent:
//   - default: the raw UUID (legacy Trojan/SS password; VLESS/VMess/Hy2 ignore it)
//   - pwClass256: base64(sha256(uuid)) — the 32-byte SS-2022 PSK
//   - pwClass128: base64(sha256(uuid))[:16] — the 16-byte SS-2022 PSK
func passwordForClass(pwClass int, userUUID string) string {
	switch pwClass {
	case pwClass256:
		return crypto.NewProxyPassword(userUUID)
	case pwClass128:
		return crypto.DeriveProxyPassword(userUUID, domain.ProtoSS2022, "2022-blake3-aes-128-gcm")
	default:
		return userUUID
	}
}
