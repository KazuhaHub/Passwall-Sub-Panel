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
//	SS (legacy):   uuid as-is
//	SS-2022:       base64(SHA-256(uuid)) — a 32-byte PSK
func DeriveProxyPassword(userUUID string, protocol domain.Protocol) string {
	switch protocol {
	case domain.ProtoVLESS, domain.ProtoVMess:
		return userUUID
	case domain.ProtoTrojan:
		return userUUID
	case domain.ProtoSS:
		return userUUID
	case domain.ProtoSS2022:
		h := sha256.Sum256([]byte(userUUID))
		return base64.StdEncoding.EncodeToString(h[:])
	}
	return userUUID
}

// DetectProtocol classifies a 3X-UI inbound protocol string into the
// internal Protocol enum. ssMethod is required to disambiguate legacy SS from
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
	}
	return ""
}

func isSS2022Method(method string) bool {
	return strings.HasPrefix(strings.ToLower(method), "2022-blake3-")
}
