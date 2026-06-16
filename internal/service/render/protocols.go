package render

import (
	"encoding/base64"
	"encoding/json"
	"fmt"

	"golang.org/x/crypto/curve25519"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/pkg/crypto"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

// emitProxy turns one (Node, Inbound, User) triple into a mihomo proxy
// block represented as map[string]any (later marshaled by yaml.v3).
//
// userEmail is the 3X-UI client.email this user maps to on the given
// inbound — required for WireGuard's per-peer lookup; ignored by every
// other protocol whose per-user credential is derived from the UUID
// directly.
//
// Returns (nil, nil) when the protocol is recognised but not yet supported;
// returns (nil, err) on configuration errors such as a missing server
// address. Callers skip the node on either nil return value.
func emitProxy(displayName string, n *domain.Node, u *domain.User, inb *ports.Inbound, userEmail string, relay *domain.RelayLine) (map[string]any, error) {
	var settings xuiInboundSettings
	_ = json.Unmarshal([]byte(inb.Settings), &settings)
	var stream xuiStreamSettings
	_ = json.Unmarshal([]byte(inb.StreamSettings), &stream)

	protocol := crypto.DetectProtocol(inb.Protocol, settings.Method)
	if protocol == "" {
		return nil, nil
	}
	// Resolve the dialed endpoint: the node's own address for a direct entry,
	// the relay front for a transit variant (which also overrides SNI / Host
	// on `stream` in place when the line carries them).
	server, port := effectiveEndpoint(relay, n.ServerAddress, inb.Port, &stream)
	if server == "" {
		return nil, fmt.Errorf("node %d (%s) missing server_address", n.ID, n.DisplayName)
	}

	base := map[string]any{
		"name":   displayName,
		"server": server,
		"port":   port,
		"udp":    true,
	}

	switch protocol {
	case domain.ProtoVLESS:
		return emitVLESS(base, u.UUID, stream, n.Flow), nil
	case domain.ProtoVMess:
		return emitVMess(base, u.UUID, stream), nil
	case domain.ProtoTrojan:
		return emitTrojan(base, crypto.DeriveProxyPassword(u.UUID, protocol, settings.Method), stream), nil
	case domain.ProtoSS:
		return emitSSProxy(base, settings.Method, crypto.DeriveProxyPassword(u.UUID, protocol, settings.Method)), nil
	case domain.ProtoSS2022:
		return emitSS2022(base, settings.Method, settings.Password,
			crypto.DeriveProxyPassword(u.UUID, protocol, settings.Method)), nil
	case domain.ProtoHysteria2:
		// Per-user password is the user's UUID (same convention as VLESS:
		// the panel-managed credential is what 3X-UI stores per client).
		opts := parseHysteria2Opts(inb.Settings, inb.StreamSettings)
		if sni := relaySNIOverride(relay); sni != "" {
			opts.SNI = sni
		}
		return emitHysteria2(base, u.UUID, opts), nil
	}
	return nil, nil
}

// emitSeparator produces a fake proxy entry whose only job is to appear as a
// visual divider in Clash clients.
func emitSeparator(name string) map[string]any {
	return map[string]any{
		"name":     name,
		"type":     "ss",
		"server":   "127.0.0.1",
		"port":     1,
		"cipher":   "chacha20-ietf-poly1305",
		"password": "psp-separator",
		"udp":      false,
	}
}

func emitVLESS(base map[string]any, uuid string, stream xuiStreamSettings, flow string) map[string]any {
	base["type"] = "vless"
	base["uuid"] = uuid
	base["network"] = defaultStr(stream.Network, "tcp")
	// Honor the node's stored flow verbatim — it mirrors what 3X-UI configured
	// for this client. Empty means "no flow"; never substitute a default
	// (xtls-rprx-vision only works over raw TCP and must match the server, so
	// guessing it breaks ws/grpc or pure-reality inbounds). sing-box and the
	// URI builder follow the same rule.
	if flow != "" {
		base["flow"] = flow
	}

	switch stream.Security {
	case "reality":
		base["tls"] = true
		if stream.RealitySettings != nil {
			base["client-fingerprint"] = defaultStr(stream.RealitySettings.Settings.Fingerprint, "chrome")
			base["servername"] = first(stream.RealitySettings.ServerNames)
			// publicKey is what the client actually needs. Modern 3X-UI stores
			// it alongside privateKey under realitySettings.settings.publicKey.
			// Older versions only persisted privateKey; in that case derive
			// the public key on the fly via X25519(scalar=priv, base=9).
			pub := stream.RealitySettings.Settings.PublicKey
			if pub == "" && stream.RealitySettings.PrivateKey != "" {
				if derived, err := derivePublicKey(stream.RealitySettings.PrivateKey); err == nil {
					pub = derived
				}
			}
			base["reality-opts"] = map[string]any{
				"public-key": pub,
				"short-id":   first(stream.RealitySettings.ShortIds),
			}
		}
	case "tls":
		base["tls"] = true
		if stream.TLSSettings != nil {
			base["servername"] = stream.TLSSettings.ServerName
			base["skip-cert-verify"] = stream.TLSSettings.AllowInsecure
		}
	}
	applyTransportOpts(base, stream)
	return base
}

func emitVMess(base map[string]any, uuid string, stream xuiStreamSettings) map[string]any {
	base["type"] = "vmess"
	base["uuid"] = uuid
	base["alterId"] = 0
	base["cipher"] = "auto"
	base["network"] = defaultStr(stream.Network, "tcp")
	if stream.Security == "tls" {
		base["tls"] = true
		if stream.TLSSettings != nil {
			base["servername"] = stream.TLSSettings.ServerName
			base["skip-cert-verify"] = stream.TLSSettings.AllowInsecure
		}
	} else {
		base["tls"] = false
	}
	applyTransportOpts(base, stream)
	return base
}

func emitTrojan(base map[string]any, password string, stream xuiStreamSettings) map[string]any {
	base["type"] = "trojan"
	base["password"] = password
	if stream.TLSSettings != nil {
		base["sni"] = stream.TLSSettings.ServerName
		base["skip-cert-verify"] = stream.TLSSettings.AllowInsecure
	} else {
		base["skip-cert-verify"] = false
	}
	applyTransportOpts(base, stream)
	return base
}

func emitSSProxy(base map[string]any, method, password string) map[string]any {
	base["type"] = "ss"
	base["cipher"] = method
	base["password"] = password
	return base
}

// parseHysteria2Opts pulls the renderer-friendly options out of 3X-UI's
// settings + stream JSON. Per frontend/src/models/inbound.js, salamander
// obfs lives in streamSettings.finalmask.udp[] (NOT settings.obfs);
// SNI / ALPN are on tlsSettings.
//
// Designed to be lenient — malformed or missing fields produce a
// zero-value option, and downstream builders gate their own emissions
// on presence (e.g. empty ObfsType skips obfs keys).
func parseHysteria2Opts(_settingsJSON, streamJSON string) hysteria2Opts {
	var stream xuiStreamSettings
	_ = json.Unmarshal([]byte(streamJSON), &stream)
	opts := hysteria2Opts{ALPN: []string{"h3"}}
	if stream.FinalMask != nil {
		for _, m := range stream.FinalMask.UDP {
			if m.Type == "salamander" {
				if pwd, ok := m.Settings["password"].(string); ok {
					opts.ObfsType = "salamander"
					opts.ObfsPassword = pwd
				}
				break
			}
		}
	}
	if stream.TLSSettings != nil {
		opts.SNI = stream.TLSSettings.ServerName
		opts.Insecure = stream.TLSSettings.AllowInsecure
		if len(stream.TLSSettings.ALPN) > 0 {
			opts.ALPN = stream.TLSSettings.ALPN
		}
	}
	return opts
}

// emitHysteria2 builds the mihomo proxy block. Mihomo's hysteria2 schema
// is documented at https://wiki.metacubex.one/config/proxies/hysteria2/.
// Obfs keys are conditionally added so clients don't initialise the
// salamander handshake when the server isn't expecting it.
func emitHysteria2(base map[string]any, password string, opts hysteria2Opts) map[string]any {
	base["type"] = "hysteria2"
	base["password"] = password
	if opts.SNI != "" {
		base["sni"] = opts.SNI
	}
	if len(opts.ALPN) > 0 {
		base["alpn"] = opts.ALPN
	}
	base["skip-cert-verify"] = opts.Insecure
	if opts.ObfsType != "" {
		base["obfs"] = opts.ObfsType
		if opts.ObfsPassword != "" {
			base["obfs-password"] = opts.ObfsPassword
		}
	}
	return base
}

// emitSS2022 composes the EIH password as "<server-psk>:<user-psk>" which is
// the format mihomo expects for the 2022-blake3-* ciphers.
func emitSS2022(base map[string]any, method, serverPSK, userPSK string) map[string]any {
	base["type"] = "ss"
	base["cipher"] = method
	base["password"] = serverPSK + ":" + userPSK
	return base
}

func applyTransportOpts(base map[string]any, stream xuiStreamSettings) {
	switch stream.Network {
	case "ws":
		if stream.WSSettings != nil {
			opts := map[string]any{"path": defaultStr(stream.WSSettings.Path, "/")}
			if len(stream.WSSettings.Headers) > 0 {
				opts["headers"] = stream.WSSettings.Headers
			}
			base["ws-opts"] = opts
		}
	case "grpc":
		if stream.GRPCSettings != nil {
			base["grpc-opts"] = map[string]any{
				"grpc-service-name": stream.GRPCSettings.ServiceName,
			}
		}
	}
}

func first(s []string) string {
	if len(s) > 0 {
		return s[0]
	}
	return ""
}

func defaultStr(v, def string) string {
	if v != "" {
		return v
	}
	return def
}

// derivePublicKey computes the X25519 public key from a Reality private key
// (base64-encoded). Tries URL-safe base64 first — 3X-UI's format — and falls
// back to standard base64 with optional padding. Returns base64-url(no pad)
// for the public key, matching what 3X-UI emits elsewhere.
func derivePublicKey(privateKeyB64 string) (string, error) {
	priv, err := decodeBase64Any(privateKeyB64)
	if err != nil {
		return "", err
	}
	if len(priv) != 32 {
		return "", fmt.Errorf("reality private key must be 32 bytes, got %d", len(priv))
	}
	pub, err := curve25519.X25519(priv, curve25519.Basepoint)
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(pub), nil
}

// decodeBase64Any accepts any of: raw URL-safe, padded URL-safe, raw std,
// padded std. Reality keys are typically raw URL-safe but operators may
// paste either variant.
func decodeBase64Any(s string) ([]byte, error) {
	for _, enc := range []*base64.Encoding{
		base64.RawURLEncoding,
		base64.URLEncoding,
		base64.RawStdEncoding,
		base64.StdEncoding,
	} {
		if b, err := enc.DecodeString(s); err == nil {
			return b, nil
		}
	}
	return nil, fmt.Errorf("not a valid base64 (URL or std)")
}
