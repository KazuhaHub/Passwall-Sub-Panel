package render

import (
	"encoding/json"
	"fmt"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/pkg/crypto"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

// emitProxy turns one (Node, Inbound, User) triple into a Clash Meta proxy
// block represented as map[string]any (later marshaled by yaml.v3).
//
// Returns (nil, nil) when the protocol is recognised but not yet supported;
// returns (nil, err) on configuration errors such as a missing server
// address. Callers skip the node on either nil return value.
func emitProxy(displayName string, n *domain.Node, u *domain.User, inb *ports.Inbound) (map[string]any, error) {
	var settings xuiInboundSettings
	_ = json.Unmarshal([]byte(inb.Settings), &settings)
	var stream xuiStreamSettings
	_ = json.Unmarshal([]byte(inb.StreamSettings), &stream)

	protocol := crypto.DetectProtocol(inb.Protocol, settings.Method)
	if protocol == "" {
		return nil, nil
	}
	if n.ServerAddress == "" {
		return nil, fmt.Errorf("node %d (%s) missing server_address", n.ID, n.DisplayName)
	}

	base := map[string]any{
		"name":   displayName,
		"server": n.ServerAddress,
		"port":   inb.Port,
		"udp":    true,
	}

	switch protocol {
	case domain.ProtoVLESS:
		return emitVLESS(base, u.UUID, stream), nil
	case domain.ProtoVMess:
		return emitVMess(base, u.UUID, stream), nil
	case domain.ProtoTrojan:
		return emitTrojan(base, crypto.DeriveProxyPassword(u.UUID, protocol), stream), nil
	case domain.ProtoSS:
		return emitSSLegacy(base, settings.Method, crypto.DeriveProxyPassword(u.UUID, protocol)), nil
	case domain.ProtoSS2022:
		return emitSS2022(base, settings.Method, settings.Password,
			crypto.DeriveProxyPassword(u.UUID, protocol)), nil
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

func emitVLESS(base map[string]any, uuid string, stream xuiStreamSettings) map[string]any {
	base["type"] = "vless"
	base["uuid"] = uuid
	base["network"] = defaultStr(stream.Network, "tcp")

	switch stream.Security {
	case "reality":
		base["tls"] = true
		base["flow"] = "xtls-rprx-vision"
		if stream.RealitySettings != nil {
			base["client-fingerprint"] = defaultStr(stream.RealitySettings.Settings.Fingerprint, "chrome")
			base["servername"] = first(stream.RealitySettings.ServerNames)
			base["reality-opts"] = map[string]any{
				"public-key": stream.RealitySettings.Settings.PublicKey,
				"short-id":   first(stream.RealitySettings.ShortIds),
			}
		}
	case "tls":
		base["tls"] = true
		if stream.TLSSettings != nil {
			base["servername"] = stream.TLSSettings.ServerName
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
	base["skip-cert-verify"] = false
	if stream.TLSSettings != nil {
		base["sni"] = stream.TLSSettings.ServerName
	}
	applyTransportOpts(base, stream)
	return base
}

func emitSSLegacy(base map[string]any, method, password string) map[string]any {
	base["type"] = "ss"
	base["cipher"] = method
	base["password"] = password
	return base
}

// emitSS2022 composes the EIH password as "<server-psk>:<user-psk>" which is
// the format Clash Meta expects for the 2022-blake3-* ciphers.
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
