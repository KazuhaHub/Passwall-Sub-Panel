// Package crypto provides per-user proxy password derivation and AES-GCM
// helpers for encrypting sensitive config fields at rest.
package crypto

import (
	"crypto/sha256"
	"encoding/base64"
	"strings"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
)

// DeriveProxyPassword returns the credential value for a given protocol,
// derived from the user's UUID. The same rule is applied both when the panel
// syncs a client to 3X-UI and when the subscription renderer emits proxy
// blocks, so both ends stay in lockstep.
//
//	VLESS / VMess: uuid as-is (becomes the "uuid" / "id" field)
//	Trojan:        uuid as-is (uuid string used as the password)
//	SS:            uuid as-is
//	SS-2022:       base64(SHA-256(uuid)) truncated to the method's key length
//
// ssMethod is only consulted for SS-2022: SIP022 fixes the PSK byte length per
// cipher (aes-128-gcm → 16, aes-256-gcm / chacha20-poly1305 → 32). SHA-256
// yields 32 bytes, so 256-gcm uses the full digest while 128-gcm MUST be
// truncated to 16 bytes — otherwise Xray rejects the client with
// "bad key length, required 16". For every other protocol ssMethod is ignored.
func DeriveProxyPassword(userUUID string, protocol domain.Protocol, ssMethod string) string {
	switch protocol {
	case domain.ProtoVLESS, domain.ProtoVMess:
		return userUUID
	case domain.ProtoTrojan:
		return userUUID
	case domain.ProtoSS:
		return userUUID
	case domain.ProtoSS2022:
		h := sha256.Sum256([]byte(userUUID))
		return base64.StdEncoding.EncodeToString(h[:ss2022KeyLen(ssMethod)])
	}
	return userUUID
}

// NewProxyPassword returns the v3.9.0 STORED proxy password for a client,
// derived deterministically from the user's UUID. Unlike DeriveProxyPassword
// (which branches per protocol and is recomputed at render time), this is ONE
// value persisted on domain.PSPClient and used for EVERY password protocol —
// Trojan, Shadowsocks, and SS-2022 (aes-256-gcm / chacha20). It is base64 of the
// full 32-byte SHA-256(uuid): simultaneously a valid 32-byte SS-2022 PSK and a
// perfectly good free-form Trojan/SS password. Storing one value (instead of
// per-protocol derivation) is exactly what lets a single shared client span
// inbounds of different password protocols; determinism lets the v3.9.0
// migration compute it offline from the existing UUID with no panel read-back.
//
// It equals DeriveProxyPassword(uuid, ProtoSS2022, "...aes-256-gcm"), so a
// client that already had an SS-2022-256 inbound keeps the same password across
// the migration. A user who additionally has an SS-2022-*128*-gcm inbound on the
// same panel needs a 16-byte PSK that this 32-byte value can't satisfy — that
// inbound is split into a separate credential class (domain.PSPClient.CredClass).
func NewProxyPassword(userUUID string) string {
	h := sha256.Sum256([]byte(userUUID))
	return base64.StdEncoding.EncodeToString(h[:])
}

// ss2022KeyLen returns the SIP022 PSK byte length for an SS-2022 cipher: 16 for
// the aes-128-gcm variant, 32 for aes-256-gcm and chacha20-poly1305. Unknown /
// empty methods fall back to 32 (the historical default and the more common
// cipher), which keeps 256-gcm correct even if the method string is missing.
func ss2022KeyLen(method string) int {
	if strings.Contains(strings.ToLower(method), "aes-128") {
		return 16
	}
	return 32
}

// SS2022KeyLen exposes the SIP022 PSK byte length for an SS-2022 cipher method
// (16 for aes-128-gcm, 32 otherwise). The v3.9.0 credential-class planner uses
// it to detect the 128-bit case — a 16-byte PSK can't share the single client
// `password` field with the 32-byte protocols, so it forces a separate client.
func SS2022KeyLen(method string) int { return ss2022KeyLen(method) }

// DetectProtocol classifies a 3X-UI inbound protocol string into the
// internal Protocol enum. ssMethod is required to disambiguate SS from
// SS-2022 (both report protocol="shadowsocks").
func DetectProtocol(inboundProtocol, ssMethod string) domain.Protocol {
	switch strings.ToLower(inboundProtocol) {
	case "vless":
		return domain.ProtoVLESS
	case "vmess":
		return domain.ProtoVMess
	case "trojan":
		return domain.ProtoTrojan
	case "shadowsocks", "ss":
		if isSS2022Method(ssMethod) {
			return domain.ProtoSS2022
		}
		return domain.ProtoSS
	case "hysteria2", "hy2":
		return domain.ProtoHysteria2
	}
	return ""
}

func isSS2022Method(method string) bool {
	return strings.HasPrefix(strings.ToLower(method), "2022-blake3-")
}
