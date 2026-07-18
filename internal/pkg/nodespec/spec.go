// Package nodespec decodes the historical Xray-shaped inbound payload into a
// vendor-neutral representation. The admin API keeps accepting the existing
// wire format for compatibility; panel adapters consume this model instead of
// reaching into another vendor's JSON directly.
package nodespec

import (
	"encoding/json"
	"fmt"
	"net"
	"strconv"
	"strings"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

type Spec struct {
	Remark   string
	Enable   bool
	Listen   string
	Port     int
	Protocol string

	Transport   Transport
	Security    Security
	Socket      SocketOptions
	Shadowsocks Shadowsocks
	Hysteria2   Hysteria2
	AnyTLS      AnyTLS
	TUIC        TUIC
	Naive       Naive
	Sniffing    Sniffing
}

type Transport struct {
	Type                string
	Path                string
	Host                string
	Headers             map[string]string
	ServiceName         string
	TCPHeaderType       string
	AcceptProxyProtocol bool
	Authority           string
	MultiMode           bool
}

type SocketOptions struct {
	Mark                 int64
	AcceptProxyProtocol  bool
	TCPFastOpen          bool
	TCPKeepAliveInterval int64
	TCPKeepAliveIdle     int64
	TCPUserTimeout       int64
	TProxy               string
}

type Security struct {
	Mode    string
	TLS     TLS
	Reality Reality
}

type TLS struct {
	ServerName       string
	ALPN             []string
	MinVersion       string
	MaxVersion       string
	Fingerprint      string
	AllowInsecure    bool
	RejectUnknownSNI bool
	Certificate      string
	Key              string
	CertificatePath  string
	KeyPath          string
}

type Reality struct {
	ServerNames       []string
	HandshakeServer   string
	HandshakePort     int
	PrivateKey        string
	PublicKey         string
	ShortIDs          []string
	Fingerprint       string
	SpiderX           string
	Xver              int64
	MinClientVersion  string
	MaxClientVersion  string
	MaxTimeDiffMillis int64
}

type Shadowsocks struct {
	Method   string
	Password string
	Network  string
	IVCheck  bool
}

type Hysteria2 struct {
	ObfsPassword          string
	UDPIdleTimeoutSeconds int
	MasqueradeType        string
	MasqueradeData        string
}

type AnyTLS struct {
	PaddingScheme []string
}

type TUIC struct {
	CongestionControl string
	AuthTimeout       string
	ZeroRTTHandshake  bool
	Heartbeat         string
}

type Naive struct {
	Network               string
	QUICCongestionControl string
}

type Sniffing struct {
	Enabled             bool
	DestinationOverride []string
	MetadataOnly        bool
	RouteOnly           bool
}

func Decode(in ports.InboundSpec) (*Spec, error) {
	if in.Port < 1 || in.Port > 65535 {
		return nil, fmt.Errorf("%w: inbound port must be between 1 and 65535", domain.ErrValidation)
	}
	settings, err := object(in.Settings, "settings")
	if err != nil {
		return nil, err
	}
	stream, err := object(in.StreamSettings, "stream_settings")
	if err != nil {
		return nil, err
	}
	sniffing, err := object(in.Sniffing, "sniffing")
	if err != nil {
		return nil, err
	}

	spec := &Spec{
		Remark: in.Remark, Enable: in.Enable, Listen: in.Listen, Port: in.Port,
		Protocol:  strings.ToLower(strings.TrimSpace(in.Protocol)),
		Transport: Transport{Type: strings.ToLower(stringValue(stream["network"]))},
		Security:  Security{Mode: strings.ToLower(stringValue(stream["security"]))},
		Sniffing: Sniffing{
			Enabled: boolValue(sniffing["enabled"]), DestinationOverride: stringSlice(sniffing["destOverride"]),
			MetadataOnly: boolValue(sniffing["metadataOnly"]), RouteOnly: boolValue(sniffing["routeOnly"]),
		},
	}
	if spec.Transport.Type == "" || spec.Transport.Type == "hysteria" {
		spec.Transport.Type = "tcp"
	}
	if spec.Security.Mode == "" {
		spec.Security.Mode = "none"
	}
	decodeTransport(&spec.Transport, stream)
	decodeTLS(&spec.Security, stream)
	decodeSocket(&spec.Socket, stream)

	spec.Shadowsocks = Shadowsocks{
		Method: stringValue(settings["method"]), Password: stringValue(settings["password"]),
		Network: stringValue(settings["network"]), IVCheck: boolValue(settings["ivCheck"]),
	}
	if spec.Shadowsocks.Network == "" {
		spec.Shadowsocks.Network = "tcp,udp"
	}
	decodeHysteria2(&spec.Hysteria2, stream)
	spec.AnyTLS = AnyTLS{PaddingScheme: stringSlice(settings["padding_scheme"])}
	spec.TUIC = TUIC{
		CongestionControl: stringValue(settings["congestion_control"]),
		AuthTimeout:       stringValue(settings["auth_timeout"]),
		ZeroRTTHandshake:  boolValue(settings["zero_rtt_handshake"]),
		Heartbeat:         stringValue(settings["heartbeat"]),
	}
	spec.Naive = Naive{
		Network:               stringValue(settings["network"]),
		QUICCongestionControl: stringValue(settings["quic_congestion_control"]),
	}
	return spec, nil
}

func decodeTransport(out *Transport, stream map[string]any) {
	switch out.Type {
	case "tcp":
		cfg := mapValue(stream["tcpSettings"])
		out.AcceptProxyProtocol = boolValue(cfg["acceptProxyProtocol"])
		out.TCPHeaderType = stringValue(mapValue(cfg["header"])["type"])
	case "ws":
		cfg := mapValue(stream["wsSettings"])
		out.AcceptProxyProtocol = boolValue(cfg["acceptProxyProtocol"])
		out.Path = stringValue(cfg["path"])
		out.Host = stringValue(cfg["host"])
		out.Headers = stringMap(cfg["headers"])
		if out.Host == "" {
			out.Host = header(out.Headers, "host")
		}
	case "grpc":
		cfg := mapValue(stream["grpcSettings"])
		out.ServiceName = stringValue(cfg["serviceName"])
		out.Authority = stringValue(cfg["authority"])
		out.MultiMode = boolValue(cfg["multiMode"])
	case "httpupgrade":
		cfg := mapValue(stream["httpupgradeSettings"])
		out.AcceptProxyProtocol = boolValue(cfg["acceptProxyProtocol"])
		out.Path = stringValue(cfg["path"])
		out.Host = stringValue(cfg["host"])
		out.Headers = stringMap(cfg["headers"])
	case "http":
		cfg := mapValue(stream["httpSettings"])
		out.Path = firstString(cfg["path"])
		out.Host = firstString(cfg["host"])
	}
}

func decodeTLS(out *Security, stream map[string]any) {
	switch out.Mode {
	case "tls":
		cfg := mapValue(stream["tlsSettings"])
		out.TLS = TLS{
			ServerName: stringValue(cfg["serverName"]), ALPN: stringSlice(cfg["alpn"]),
			MinVersion: stringValue(cfg["minVersion"]), MaxVersion: stringValue(cfg["maxVersion"]),
			AllowInsecure:    boolValue(cfg["allowInsecure"]),
			RejectUnknownSNI: boolValue(cfg["rejectUnknownSni"]),
			Fingerprint:      stringValue(mapValue(cfg["settings"])["fingerprint"]),
		}
		if certs, ok := cfg["certificates"].([]any); ok && len(certs) > 0 {
			cert := mapValue(certs[0])
			out.TLS.CertificatePath = stringValue(cert["certificateFile"])
			out.TLS.KeyPath = stringValue(cert["keyFile"])
			out.TLS.Certificate = joinPEM(cert["certificate"])
			out.TLS.Key = joinPEM(cert["key"])
		}
	case "reality":
		cfg := mapValue(stream["realitySettings"])
		target := stringValue(cfg["target"])
		if target == "" {
			target = stringValue(cfg["dest"])
		}
		host, port := splitTarget(target)
		client := mapValue(cfg["settings"])
		out.Reality = Reality{
			ServerNames: stringSlice(cfg["serverNames"]), HandshakeServer: host, HandshakePort: port,
			PrivateKey: stringValue(cfg["privateKey"]), PublicKey: stringValue(client["publicKey"]),
			ShortIDs: stringSlice(cfg["shortIds"]), Fingerprint: stringValue(client["fingerprint"]),
			SpiderX: stringValue(client["spiderX"]), Xver: int64Value(cfg["xver"]),
			MinClientVersion: stringValue(cfg["minClientVer"]), MaxClientVersion: stringValue(cfg["maxClientVer"]),
			MaxTimeDiffMillis: int64Value(cfg["maxTimediff"]),
		}
	}
}

func decodeSocket(out *SocketOptions, stream map[string]any) {
	cfg := mapValue(stream["sockopt"])
	out.Mark = int64Value(cfg["mark"])
	out.AcceptProxyProtocol = boolValue(cfg["acceptProxyProtocol"])
	out.TCPFastOpen = boolValue(cfg["tcpFastOpen"])
	out.TCPKeepAliveInterval = int64Value(cfg["tcpKeepAliveInterval"])
	out.TCPKeepAliveIdle = int64Value(cfg["tcpKeepAliveIdle"])
	out.TCPUserTimeout = int64Value(cfg["tcpUserTimeout"])
	out.TProxy = stringValue(cfg["tproxy"])
}

func decodeHysteria2(out *Hysteria2, stream map[string]any) {
	hysteria := mapValue(stream["hysteriaSettings"])
	out.UDPIdleTimeoutSeconds = int(int64Value(hysteria["udpIdleTimeout"]))
	finalmask := mapValue(stream["finalmask"])
	if udp, ok := finalmask["udp"].([]any); ok {
		for _, raw := range udp {
			mask := mapValue(raw)
			if strings.EqualFold(stringValue(mask["type"]), "salamander") {
				out.ObfsPassword = stringValue(mapValue(mask["settings"])["password"])
				break
			}
		}
	}
	masquerade := mapValue(hysteria["masquerade"])
	out.MasqueradeType = stringValue(masquerade["type"])
	switch out.MasqueradeType {
	case "proxy":
		out.MasqueradeData = stringValue(masquerade["url"])
	case "file":
		out.MasqueradeData = stringValue(masquerade["dir"])
	case "string":
		out.MasqueradeData = stringValue(masquerade["content"])
	}
}

func object(raw, field string) (map[string]any, error) {
	if strings.TrimSpace(raw) == "" || strings.TrimSpace(raw) == "null" {
		return map[string]any{}, nil
	}
	var out map[string]any
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		return nil, fmt.Errorf("%w: invalid %s JSON: %v", domain.ErrValidation, field, err)
	}
	if out == nil {
		out = map[string]any{}
	}
	return out, nil
}

func splitTarget(target string) (string, int) {
	target = strings.TrimSpace(target)
	if target == "" {
		return "", 443
	}
	if host, portText, err := net.SplitHostPort(target); err == nil {
		port, _ := strconv.Atoi(portText)
		if port > 0 {
			return host, port
		}
	}
	if i := strings.LastIndex(target, ":"); i > 0 && !strings.Contains(target[i+1:], ":") {
		if port, err := strconv.Atoi(target[i+1:]); err == nil && port > 0 {
			return strings.Trim(target[:i], "[]"), port
		}
	}
	return strings.Trim(target, "[]"), 443
}

func mapValue(v any) map[string]any {
	m, _ := v.(map[string]any)
	if m == nil {
		return map[string]any{}
	}
	return m
}
func stringValue(v any) string { s, _ := v.(string); return s }
func boolValue(v any) bool     { b, _ := v.(bool); return b }
func int64Value(v any) int64 {
	switch n := v.(type) {
	case float64:
		return int64(n)
	case json.Number:
		v, _ := n.Int64()
		return v
	case int64:
		return n
	case int:
		return int64(n)
	}
	return 0
}
func stringSlice(v any) []string {
	switch value := v.(type) {
	case string:
		if value != "" {
			return []string{value}
		}
	case []any:
		out := make([]string, 0, len(value))
		for _, item := range value {
			if s := stringValue(item); s != "" {
				out = append(out, s)
			}
		}
		return out
	case []string:
		return append([]string(nil), value...)
	}
	return nil
}
func firstString(v any) string {
	values := stringSlice(v)
	if len(values) > 0 {
		return values[0]
	}
	return ""
}
func stringMap(v any) map[string]string {
	raw := mapValue(v)
	out := make(map[string]string, len(raw))
	for key, value := range raw {
		if s := stringValue(value); s != "" {
			out[key] = s
		}
	}
	return out
}
func header(headers map[string]string, name string) string {
	for key, value := range headers {
		if strings.EqualFold(key, name) {
			return value
		}
	}
	return ""
}
func joinPEM(v any) string { return strings.Join(stringSlice(v), "\n") }
