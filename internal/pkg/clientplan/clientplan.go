// Package clientplan computes a user's DESIRED v3.9.0 client state on one panel:
// which psp_clients should exist (usually one — the shared client) and which
// nodes each is attached to. It is pure (no I/O) so both live enrollment and the
// one-shot migration build the same plan, and so it is exhaustively testable.
//
// A single 3X-UI client carries id (UUID), password, auth and a SINGLE flow in
// SEPARATE fields, and each inbound reads only the field its protocol uses —
// VLESS/VMess read `id`, Trojan/SS/SS-2022 read `password`, Hysteria2 reads
// `auth`, VLESS reads `flow`. So one client can serve MANY protocols at once;
// PSP packs ALL of a user's nodes on a panel into the FEWEST clients possible.
//
// The only thing that forces a split is a genuine SAME-field conflict, because a
// client has exactly ONE `password` slot and ONE `flow` slot:
//
//   - password: Trojan / plain-SS need the RAW UUID (pwClass 0); SS-2022 needs a
//     real PSK — pwClass256 (32-byte base64(sha256(uuid))) or pwClass128 (16-byte).
//     A user with two DIFFERENT password requirements on one panel (e.g. plain-SS's
//     UUID and SS-2022's PSK, or both SS-2022 key lengths) needs one client per
//     distinct password. VLESS/VMess/Hy2 don't use the field, so they impose no
//     password constraint. Every class equals the LEGACY DeriveProxyPassword, so
//     merging a node into its shared client changes no rendered credential — SILENT.
//   - flow: only VLESS uses it ("" or xtls-rprx-vision). Two VLESS inbounds with
//     different flow need separate clients; everything else ignores flow.
//
// Crucially, a password-using protocol never uses flow and flow-using VLESS never
// uses password, so no node constrains BOTH at once — the minimum client count is
// max(#distinct-passwords, #distinct-flows, 1). The common user (VLESS-vision +
// SS-2022, different fields) collapses to exactly ONE client per panel. The client
// email is an upstream projection: partitioned rows use a content-hash suffix,
// while a panel with one row uses the bare form. Changing a node's protocol/flow
// can therefore re-key that projection; the durable psp_clients.id survives by
// attachment affinity, and credentials remain byte-identical, so subscribers do
// not need to re-fetch.
package clientplan

import (
	"crypto/sha256"
	"encoding/hex"
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

	keySep = "\x1f" // ASCII Unit Separator — never appears in a flow string, so the canonical key is injective
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

// partKey is the shared-client partition key: nodes share a client iff equal.
type partKey struct {
	pwClass int
	flow    string
}

// pwAgnostic marks a node whose protocol doesn't use the `password` field at all
// (VLESS/VMess authenticate by id; Hysteria2 by auth) — so it never constrains a
// client's single password slot and can join whichever client a merge produces.
const pwAgnostic = -1

// pwReqFor returns the password CLASS a node requires its client's single password
// field to hold, or pwAgnostic when the protocol doesn't use that field. SS-2022
// needs a real PSK (split by key length); Trojan / plain-SS use the raw UUID
// (default class); VLESS/VMess/Hy2 don't touch the password field, so they impose
// no constraint and merge with anything.
func pwReqFor(nc NodeCred) int {
	switch nc.Protocol {
	case domain.ProtoSS2022:
		if crypto.SS2022KeyLen(nc.SSMethod) == 16 {
			return pwClass128
		}
		return pwClass256
	case domain.ProtoTrojan, domain.ProtoSS, domain.ProtoAnyTLS, domain.ProtoTUIC, domain.ProtoNaive:
		return pwClassDefault
	default:
		return pwAgnostic
	}
}

// flowAgnostic marks a node whose protocol ignores the `flow` field (everything
// except VLESS) — 3X-UI drops flow for it, so it never constrains the client's
// single flow slot.
const flowAgnostic = "\x00flow-agnostic"

// flowReqFor returns the flow a node requires its client to carry, or flowAgnostic
// when the protocol doesn't use flow. Only VLESS uses flow (allowlisted to
// {"", flowVision}); a VLESS inbound REQUIRES its exact flow — a no-flow inbound
// would mis-authenticate a client carrying vision, and vice versa.
func flowReqFor(nc NodeCred) string {
	if nc.Protocol == domain.ProtoVLESS {
		return effectiveFlow(nc.Protocol, nc.Flow)
	}
	return flowAgnostic
}

// effectiveFlow is the flow that will actually be pushed for this node: only
// VLESS carries flow, and it is allowlisted to {"", flowVision}. Anything else
// coerces to "" (current Xray VLESS has no other flow). The reconcile service
// MUST push this same effective flow when it creates the client, or a node would
// be partitioned by one flow and provisioned with another.
func effectiveFlow(p domain.Protocol, flow string) string {
	if p == domain.ProtoVLESS && flow == flowVision {
		return flowVision
	}
	return ""
}

// canon is the injective canonical key string that the email suffix hashes. The
// "v1" tag lets the scheme evolve later without re-keying existing v1 clients.
func (k partKey) canon() string {
	return "v1" + keySep + "pw=" + strconv.Itoa(k.pwClass) + keySep + "flow=" + k.flow
}

// emailSuffix is the STABLE, collision-free email suffix for this key. It is a
// pure function of the key's content (never positional), so a key that still
// exists always maps to the same email regardless of the user's other nodes.
func (k partKey) emailSuffix() string {
	if k.pwClass == pwClassDefault && k.flow == "" {
		return "" // the common case → u{uid}@domain
	}
	// Every other partition (SS-2022 of either key length, or a non-empty VLESS
	// flow) gets a stable content-hash suffix. The email is the 3X-UI client
	// identity only — invisible to the subscriber — so this never affects silence.
	sum := sha256.Sum256([]byte(k.canon()))
	return "-k" + hex.EncodeToString(sum[:])[:8]
}

// ExistingClient is the stable identity and attachment shape of a persisted
// psp_client. ClientID is the identity; CredClass and NodeIDs are only affinity
// hints used to keep that identity attached to the closest desired partition
// when the partition layout changes. In particular, neither email nor the
// positional partKey is accepted here, so a caller cannot accidentally turn a
// rendered value back into identity.
type ExistingClient struct {
	ClientID  int64
	CredClass int
	NodeIDs   []int64
}

// DesiredClient is one psp_client PSP should hold for a user on a panel, paired
// with its attachment set. Credentials are filled in (the stored source of
// truth). Build carries forward a persisted Client.ID where one is available;
// a zero ID means the repo must mint a new row. CredClass remains a mutable
// password-shape attribute, not an identity discriminator.
type DesiredClient struct {
	Client   domain.PSPClient
	Inbounds []domain.PSPClientInbound
}

// Build returns the desired clients for one user on one panel, given the nodes
// they can access there. It produces the MINIMUM number of clients: one 3X-UI
// client holds id (VLESS/VMess), password (Trojan/SS/SS-2022) and auth (Hy2) in
// SEPARATE fields, so protocols that use different fields share a single client.
// The only forced split is a genuine SAME-field conflict — two distinct password
// values (plain-SS/Trojan's UUID vs SS-2022's PSK, or the two SS-2022 key lengths)
// or two distinct VLESS flows — because the client has ONE password slot and ONE
// flow slot.
//
// Because password-using protocols never use flow and flow-using VLESS never uses
// password, no node constrains BOTH dimensions, so the minimum client count is
// max(#distinct-required-passwords, #distinct-required-flows, 1). The common pair
// (VLESS-vision + SS-2022) therefore collapses to exactly ONE client. Stored
// credentials stay byte-identical to the legacy DeriveProxyPassword, so the merge
// is SILENT (no subscriber re-fetch). Deterministic + order-stable.
func Build(userID int64, userUUID string, panelID int64, rules domain.EmailRules, nodes []NodeCred, existing []ExistingClient) []DesiredClient {
	if len(nodes) == 0 {
		return nil
	}
	// Distinct REQUIRED password classes + flows (agnostic nodes impose neither).
	var preq []int
	var freq []string
	seenP := map[int]bool{}
	seenF := map[string]bool{}
	for _, n := range nodes {
		if p := pwReqFor(n); p != pwAgnostic && !seenP[p] {
			seenP[p] = true
			preq = append(preq, p)
		}
		if f := flowReqFor(n); f != flowAgnostic && !seenF[f] {
			seenF[f] = true
			freq = append(freq, f)
		}
	}
	sort.Ints(preq)
	sort.Strings(freq)

	// One client per index, pairing the i-th required password with the i-th
	// required flow (the dimensions are independent, so pairing is minimal);
	// unpaired indices fall back to the default password / empty flow.
	n := len(preq)
	if len(freq) > n {
		n = len(freq)
	}
	if n == 0 {
		n = 1
	}
	keys := make([]partKey, n)
	pwToClient := map[int]int{}
	flowToClient := map[string]int{}
	for i := 0; i < n; i++ {
		k := partKey{pwClass: pwClassDefault, flow: ""}
		if i < len(preq) {
			k.pwClass = preq[i]
			pwToClient[preq[i]] = i
		}
		if i < len(freq) {
			k.flow = freq[i]
			flowToClient[freq[i]] = i
		}
		keys[i] = k
	}

	// Assign each node: by its required password, else its required flow, else
	// (fully agnostic — VMess / Hy2) to client 0.
	buckets := make([][]NodeCred, n)
	for _, node := range nodes {
		idx := 0
		if p := pwReqFor(node); p != pwAgnostic {
			idx = pwToClient[p]
		} else if f := flowReqFor(node); f != flowAgnostic {
			idx = flowToClient[f]
		}
		buckets[idx] = append(buckets[idx], node)
	}

	out := make([]DesiredClient, 0, n)
	for i, k := range keys {
		if len(buckets[i]) == 0 {
			continue
		}
		inbounds := make([]domain.PSPClientInbound, 0, len(buckets[i]))
		for _, node := range buckets[i] {
			// The client carries ONE flow (k.flow); record it on every attachment so
			// provisioning pushes a single consistent flow. 3X-UI applies it only to
			// the VLESS inbound(s) and drops it for the non-VLESS ones in the client.
			inbounds = append(inbounds, domain.PSPClientInbound{NodeID: node.NodeID, FlowOverride: k.flow})
		}
		out = append(out, DesiredClient{
			Client: domain.PSPClient{
				UserID:    userID,
				PanelID:   panelID,
				CredClass: k.pwClass,
				Email:     domain.PSPClientEmail(userID, k.emailSuffix(), rules),
				UUID:      userUUID,
				Password:  passwordForClass(k.pwClass, userUUID),
			},
			Inbounds: inbounds,
		})
	}
	// User-facing simplification: when a panel needs only ONE client (the common case
	// once protocols merge), drop the partition-hash suffix and use the bare
	// "u{id}@domain" email. A lone client carries its credentials in its own fields,
	// so the email needs no (pwClass,flow) discriminator — the suffix only earns its
	// keep when 2+ clients must coexist on one panel (a genuine same-field conflict,
	// where distinct emails are mandatory). This makes the email count-dependent rather
	// than a pure function of the partition; crossing the 1↔2 boundary re-keys the
	// client(s), but render derives creds from the UUID so subscribers never re-fetch.
	if len(out) == 1 {
		out[0].Client.Email = domain.PSPClientEmail(userID, "", rules)
	}
	assignStableIDs(out, existing)
	return out
}

// assignStableIDs matches persisted rows to the new partition layout without
// deriving identity from any mutable partition attribute. The matching first
// maximises how many rows survive, then how many node attachments keep the same
// row, with exact attachment sets and the old credential class used only as
// tie-breakers. This handles both important re-key events:
//
//   - a 1→2 split keeps the old row on the partition containing its old nodes;
//   - a domain change keeps every row because attachment sets are unchanged.
//
// The dynamic program is O(existing * desired * 2^desired). desired is bounded
// by the planner's password/flow dimensions (currently at most three), while
// this shape also remains cheap and deterministic if that bound grows later.
func assignStableIDs(desired []DesiredClient, existing []ExistingClient) {
	if len(desired) == 0 || len(existing) == 0 {
		return
	}
	existing = append([]ExistingClient(nil), existing...)
	sort.Slice(existing, func(i, j int) bool { return existing[i].ClientID < existing[j].ClientID })

	type score struct {
		valid      bool
		matched    int
		overlap    int
		exact      int
		classMatch int
		ids        []int64 // desired index -> persisted ID; zero means unmatched
	}
	better := func(a, b score) bool {
		if !a.valid {
			return false
		}
		if !b.valid {
			return true
		}
		if a.matched != b.matched {
			return a.matched > b.matched
		}
		if a.overlap != b.overlap {
			return a.overlap > b.overlap
		}
		if a.exact != b.exact {
			return a.exact > b.exact
		}
		if a.classMatch != b.classMatch {
			return a.classMatch > b.classMatch
		}
		// Deterministic final tie-break: lower durable IDs stay with earlier
		// (deterministically ordered) desired partitions. Treat zero as last.
		for i := range a.ids {
			av, bv := a.ids[i], b.ids[i]
			if av == bv {
				continue
			}
			if av == 0 {
				return false
			}
			if bv == 0 {
				return true
			}
			return av < bv
		}
		return false
	}
	affinity := func(e ExistingClient, d DesiredClient) (overlap int, exact bool) {
		desiredNodes := make(map[int64]struct{}, len(d.Inbounds))
		for _, in := range d.Inbounds {
			desiredNodes[in.NodeID] = struct{}{}
		}
		existingNodes := make(map[int64]struct{}, len(e.NodeIDs))
		for _, nodeID := range e.NodeIDs {
			existingNodes[nodeID] = struct{}{}
			if _, ok := desiredNodes[nodeID]; ok {
				overlap++
			}
		}
		return overlap, len(existingNodes) == len(desiredNodes) && overlap == len(desiredNodes)
	}

	stateCount := 1 << len(desired)
	dp := make([]score, stateCount)
	dp[0] = score{valid: true, ids: make([]int64, len(desired))}
	for _, e := range existing {
		next := make([]score, stateCount)
		for mask, current := range dp {
			if !current.valid {
				continue
			}
			if better(current, next[mask]) {
				next[mask] = current
			}
			for i := range desired {
				bit := 1 << i
				if mask&bit != 0 {
					continue
				}
				candidate := current
				candidate.ids = append([]int64(nil), current.ids...)
				candidate.ids[i] = e.ClientID
				candidate.matched++
				overlap, exact := affinity(e, desired[i])
				candidate.overlap += overlap
				if exact {
					candidate.exact++
				}
				if e.CredClass == desired[i].Client.CredClass {
					candidate.classMatch++
				}
				candidate.valid = true
				newMask := mask | bit
				if better(candidate, next[newMask]) {
					next[newMask] = candidate
				}
			}
		}
		dp = next
	}

	best := score{}
	for _, candidate := range dp {
		if better(candidate, best) {
			best = candidate
		}
	}
	for i, id := range best.ids {
		desired[i].Client.ID = id
	}
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
