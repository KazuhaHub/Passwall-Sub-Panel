// Package clientplan computes a user's DESIRED v3.9.0 client state on one panel:
// which psp_clients should exist (usually one — the shared client) and which
// nodes each is attached to. It is pure (no I/O) so both live enrollment and the
// one-shot migration build the same plan, and so it is exhaustively testable.
//
// The single subtlety is credential classes. A 3X-UI client carries ONE
// `password` field. v3.9.0 stores one value per client (crypto.NewProxyPassword
// = base64 of SHA-256(uuid), a 32-byte SS-2022 PSK that is also a valid
// Trojan/SS password), so VLESS/VMess (use `id`), Hysteria2 (uses `auth`),
// Trojan/SS/SS-2022-256 (use that 32-byte value) all share ONE client. The lone
// exception is SS-2022 with an aes-128-gcm cipher: its PSK must be 16 bytes,
// which the 32-byte value can't be — those nodes get their own client
// (CredClass 1). Mixing 128 and 256 SS-2022 for one user on one panel is the
// only case that yields two clients; everything else is exactly one.
package clientplan

import (
	"encoding/json"
	"sort"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/pkg/crypto"
)

// credClass128 is the credential class for SS-2022 aes-128-gcm nodes (16-byte
// PSK). credClassDefault (0) covers everything else.
const (
	credClassDefault = 0
	credClass128     = 1
)

// NodeCred describes one node a user can reach on a panel — just enough to
// assign it a credential class, generate the right password, and record the
// per-inbound flow override.
type NodeCred struct {
	NodeID   int64
	Protocol domain.Protocol
	SSMethod string // disambiguates the SS-2022 key length (16 vs 32 bytes)
	Flow     string // VLESS flow → per-attachment FlowOverride
}

// NodeCredFromNode derives a NodeCred from a captured node, reading the protocol
// from its cached Node.Protocol and the SS cipher from the inbound-settings
// snapshot. Both feed crypto.DetectProtocol so an SS-2022 cipher is recognised.
//
// Caveat — an UNcaptured node (empty InboundSettings) yields an empty method, so
// crypto.DetectProtocol classifies a Shadowsocks inbound as plain SS, not
// SS-2022. That is harmless for SS-2022-256 (same default class / 32-byte
// password) but would mis-class an SS-2022-*128* node into the default class.
// Callers that must support that exotic case should resolve the live inbound
// first; the common path (captured nodes) is exact.
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

// DesiredClient is one psp_client PSP should hold for a user on a panel, paired
// with its attachment set. Credentials are filled in (the stored source of
// truth); the Client's ID/CreatedAt/counters are left zero for the repo to
// assign/preserve on upsert.
type DesiredClient struct {
	Client   domain.PSPClient
	Inbounds []domain.PSPClientInbound
}

// Build returns the desired clients for one user on one panel, given the nodes
// they can access there. Deterministic and order-stable (CredClass ascending;
// attachments in input order). An empty nodes slice yields no clients.
func Build(userID int64, userUUID string, panelID int64, rules domain.EmailRules, nodes []NodeCred) []DesiredClient {
	if len(nodes) == 0 {
		return nil
	}
	// Bucket nodes by credential class, preserving input order within a bucket.
	buckets := map[int][]NodeCred{}
	classes := []int{} // first-seen order, sorted below for stability
	for _, n := range nodes {
		cc := credClassFor(n.Protocol, n.SSMethod)
		if _, seen := buckets[cc]; !seen {
			classes = append(classes, cc)
		}
		buckets[cc] = append(buckets[cc], n)
	}
	sort.Ints(classes)

	out := make([]DesiredClient, 0, len(classes))
	for _, cc := range classes {
		bnodes := buckets[cc]
		inbounds := make([]domain.PSPClientInbound, 0, len(bnodes))
		for _, n := range bnodes {
			inbounds = append(inbounds, domain.PSPClientInbound{NodeID: n.NodeID, FlowOverride: n.Flow})
		}
		out = append(out, DesiredClient{
			Client: domain.PSPClient{
				UserID:    userID,
				PanelID:   panelID,
				CredClass: cc,
				Email:     domain.PSPClientEmail(userID, cc, rules),
				UUID:      userUUID,
				Password:  passwordForClass(cc, userUUID, bnodes),
			},
			Inbounds: inbounds,
		})
	}
	return out
}

// credClassFor assigns a node to a credential class: SS-2022-128 (16-byte PSK)
// gets its own class; everything else shares the default class.
func credClassFor(p domain.Protocol, ssMethod string) int {
	if p == domain.ProtoSS2022 && crypto.SS2022KeyLen(ssMethod) == 16 {
		return credClass128
	}
	return credClassDefault
}

// passwordForClass returns the single stored password for a class. The default
// class uses the 32-byte value (NewProxyPassword); the 128 class derives the
// 16-byte PSK from one of its (all aes-128) nodes' methods.
func passwordForClass(cc int, userUUID string, nodes []NodeCred) string {
	if cc == credClass128 {
		method := "2022-blake3-aes-128-gcm"
		if len(nodes) > 0 && nodes[0].SSMethod != "" {
			method = nodes[0].SSMethod
		}
		return crypto.DeriveProxyPassword(userUUID, domain.ProtoSS2022, method)
	}
	return crypto.NewProxyPassword(userUUID)
}
