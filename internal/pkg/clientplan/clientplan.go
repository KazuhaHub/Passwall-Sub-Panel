// Package clientplan computes a user's DESIRED v3.9.0 client state on one panel:
// which psp_clients should exist (usually one — the shared client) and which
// nodes each is attached to. It is pure (no I/O) so both live enrollment and the
// one-shot migration build the same plan, and so it is exhaustively testable.
//
// A 3X-UI client carries ONE set of fields: id (UUID), password, auth, and a
// SINGLE flow — and 3X-UI exposes no API to set a per-inbound flowOverride. So
// two nodes can share a client only when neither the password NOR the flow
// conflicts. The partition key is therefore (pwClass, flow):
//
//   - pwClass: which value the single `password` field must hold. The default
//     class (0) stores the RAW UUID — Trojan/SS use it as their password,
//     VLESS/VMess (id) + Hy2 (auth) use the UUID and ignore the password — so it
//     covers everything except SS-2022. SS-2022 needs a real PSK, so it splits
//     into pwClass256 (32-byte, base64(sha256(uuid))) and pwClass128 (16-byte).
//     Crucially every class equals the LEGACY DeriveProxyPassword for that
//     protocol, so consolidating a node into its shared client changes no
//     rendered credential — the migration is SILENT.
//   - flow: the effective VLESS flow ("" or xtls-rprx-vision), "" for every
//     other protocol (they ignore flow). VLESS inbounds needing different flow
//     can't share one client, so they split.
//
// The overwhelming majority of users (no SS-2022, uniform/empty flow) get exactly
// ONE client whose email stays u{uid}@domain; SS-2022 or mixed-flow users get a
// few. The client identity (email) is a STABLE, collision-free function of
// (uid, key) — see PSPClientEmail / partKey.emailSuffix — so re-running, or a
// user's OTHER nodes changing, never re-keys an existing client.
package clientplan

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sort"
	"strconv"

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
		Protocol: crypto.DetectProtocol(n.Protocol, method),
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

func partKeyFor(nc NodeCred) partKey {
	pw := pwClassDefault
	if nc.Protocol == domain.ProtoSS2022 {
		// SS-2022 needs a real PSK, not the raw UUID, so it can't share the
		// default (uuid-password) client; split by key length.
		if crypto.SS2022KeyLen(nc.SSMethod) == 16 {
			pw = pwClass128
		} else {
			pw = pwClass256
		}
	}
	return partKey{pwClass: pw, flow: effectiveFlow(nc.Protocol, nc.Flow)}
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

// DesiredClient is one psp_client PSP should hold for a user on a panel, paired
// with its attachment set. Credentials are filled in (the stored source of
// truth); the Client's ID/CreatedAt/counters are left zero for the repo to
// assign/preserve on upsert. CredClass carries the pwClass bit (0/1).
type DesiredClient struct {
	Client   domain.PSPClient
	Inbounds []domain.PSPClientInbound
}

// Build returns the desired clients for one user on one panel, given the nodes
// they can access there. Deterministic and order-stable (keys sorted by pwClass
// then flow). An empty nodes slice yields no clients.
func Build(userID int64, userUUID string, panelID int64, rules domain.EmailRules, nodes []NodeCred) []DesiredClient {
	if len(nodes) == 0 {
		return nil
	}
	buckets := map[partKey][]NodeCred{}
	keys := make([]partKey, 0, 2)
	for _, n := range nodes {
		k := partKeyFor(n)
		if _, seen := buckets[k]; !seen {
			keys = append(keys, k)
		}
		buckets[k] = append(buckets[k], n)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].pwClass != keys[j].pwClass {
			return keys[i].pwClass < keys[j].pwClass
		}
		return keys[i].flow < keys[j].flow
	})

	out := make([]DesiredClient, 0, len(keys))
	for _, k := range keys {
		bnodes := buckets[k]
		inbounds := make([]domain.PSPClientInbound, 0, len(bnodes))
		for _, n := range bnodes {
			// All nodes in a partition share the key's effective flow, so the
			// per-attachment FlowOverride is that flow (it records what the node
			// uses; the client itself carries the same single flow).
			inbounds = append(inbounds, domain.PSPClientInbound{NodeID: n.NodeID, FlowOverride: k.flow})
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
	return out
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
