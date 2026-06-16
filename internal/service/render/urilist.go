package render

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"
	"strings"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/pkg/crypto"
	"github.com/KazuhaHub/passwall-sub-panel/internal/pkg/log"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

// renderURIList produces a base64-encoded list of proxy URIs (one per line),
// which is the de-facto subscription format for V2rayN, OpenWrt Passwall,
// Shadowrocket and other "classic" V2Ray-family clients. This format only
// carries nodes — routing rules live in the client because the spec has no
// concept of a ruleset.
//
// Output layout (after base64 decoding):
//
//	vless://uuid@host:port?...query...#name
//	vmess://base64(json)
//	trojan://password@host:port?...query...#name
//	ss://base64(method:password)@host:port#name
//
// The whole document is then standard-base64 encoded (no line breaks) so it
// matches what these clients fetch with their built-in subscription updater.
func (s *Service) renderURIList(ctx context.Context, u *domain.User, items []renderItem, st ports.UISettings) (*Output, error) {
	emailRules := domain.EmailRules{Domain: st.EmailDomain}

	// Local snapshot for captured nodes, one batched ListInbounds per panel for
	// the un-captured transition-window remainder. See resolveInbounds.
	inboundByNode := s.resolveInbounds(ctx, items, st)

	lines := make([]string, 0, len(items))
	for _, it := range items {
		if it.isSeparator {
			// URI list has no native concept of section dividers, but
			// every V2rayN-family client renders each URI's #fragment
			// as the displayed node name. Emit a fake ss:// at
			// 127.0.0.1:1 — same trick mihomo's emitSeparator uses —
			// so the separator shows up as a labeled (non-connectable)
			// row in the client's server list, preserving the visual
			// grouping admins configured.
			lines = append(lines, buildSeparatorURI(it.name))
			continue
		}
		inb := inboundByNode[it.node.ID]
		if inb == nil {
			log.Warn("uri-list: skip node, inbound config unavailable (no local snapshot and live fetch failed)",
				"node_id", it.node.ID)
			continue
		}
		userEmail := u.ClientEmail(it.node.ID, emailRules)
		// it.name (not DisplayName) carries the layout-applied name — the
		// region-flag prefix and, for relay variants, the per-line suffix —
		// so each transit entry gets a distinct fragment.
		uri, err := buildURI(it.name, it.node, u, inb, userEmail, it.relay)
		if err != nil {
			log.Warn("uri-list: skip node, build uri failed", "node_id", it.node.ID, "err", err)
			continue
		}
		if uri == "" {
			continue
		}
		lines = append(lines, uri)
	}

	plain := strings.Join(lines, "\n")
	encoded := base64.StdEncoding.EncodeToString([]byte(plain))

	profileName := buildProfileName(u, st)
	encodedName := url.PathEscape(profileName)

	updateInterval := 24
	if st.SubUpdateIntervalHours > 0 {
		updateInterval = st.SubUpdateIntervalHours
	}

	headers := map[string]string{
		"Content-Type":            "text/plain; charset=utf-8",
		"Profile-Update-Interval": strconv.Itoa(updateInterval),
		"Content-Disposition":     `attachment; filename*=UTF-8''` + encodedName,
		"Profile-Title":           profileName,
	}
	if info := s.buildSubInfo(ctx, u); info != "" {
		headers["Subscription-Userinfo"] = info
	}

	return &Output{
		Body:        []byte(encoded),
		ContentType: "text/plain; charset=utf-8",
		Headers:     headers,
	}, nil
}

// buildURI dispatches to the per-protocol URI builder. Mirrors emitProxy in
// protocols.go but emits a single URI string instead of a Clash YAML block.
// userEmail is unused for URI builds today (WireGuard has no standard URI
// form) but kept in the signature so adding it in the future is a one-line
// switch case.
func buildURI(name string, n *domain.Node, u *domain.User, inb *ports.Inbound, _ string, relay *domain.RelayLine) (string, error) {
	var settings xuiInboundSettings
	_ = json.Unmarshal([]byte(inb.Settings), &settings)
	var stream xuiStreamSettings
	_ = json.Unmarshal([]byte(inb.StreamSettings), &stream)

	protocol := crypto.DetectProtocol(inb.Protocol, settings.Method)
	if protocol == "" {
		return "", nil
	}
	// Resolve the dialed endpoint (relay front for a transit variant, the
	// node's own address for a direct entry) — also applies the line's SNI /
	// Host override onto `stream` so the per-protocol builders below emit it.
	host, port := effectiveEndpoint(relay, n.ServerAddress, inb.Port, &stream)
	if host == "" {
		return "", fmt.Errorf("node %d (%s) missing server_address", n.ID, n.DisplayName)
	}

	switch protocol {
	case domain.ProtoVLESS:
		return buildVLESSURI(name, host, port, u.UUID, stream, n.Flow), nil
	case domain.ProtoVMess:
		return buildVMessURI(name, host, port, u.UUID, stream), nil
	case domain.ProtoTrojan:
		return buildTrojanURI(name, host, port,
			crypto.DeriveProxyPassword(u.UUID, protocol, settings.Method), stream), nil
	case domain.ProtoSS:
		return buildSSURI(name, host, port, settings.Method,
			crypto.DeriveProxyPassword(u.UUID, protocol, settings.Method)), nil
	case domain.ProtoSS2022:
		return buildSS2022URI(name, host, port, settings.Method,
			settings.Password, crypto.DeriveProxyPassword(u.UUID, protocol, settings.Method)), nil
	case domain.ProtoHysteria2:
		opts := parseHysteria2Opts(inb.Settings, inb.StreamSettings)
		if sni := relaySNIOverride(relay); sni != "" {
			opts.SNI = sni
		}
		return buildHysteria2URI(name, host, port, u.UUID, opts), nil
	}
	return "", nil
}

// buildVLESSURI emits `vless://uuid@host:port?...#name`. Covers TCP / WS /
// gRPC transports and TLS / REALITY security. Query-param keys mirror what
// V2rayN and Xray itself accept — most other clients use the same names.
func buildVLESSURI(name, host string, port int, uuid string, stream xuiStreamSettings, flow string) string {
	q := url.Values{}
	q.Set("type", defaultStr(stream.Network, "tcp"))
	q.Set("encryption", "none")
	// Honor the stored flow verbatim (same rule as the Clash + sing-box
	// renderers): empty = no flow, never default to xtls-rprx-vision.
	if flow != "" {
		q.Set("flow", flow)
	}

	switch stream.Security {
	case "reality":
		q.Set("security", "reality")
		if stream.RealitySettings != nil {
			pub := stream.RealitySettings.Settings.PublicKey
			if pub == "" && stream.RealitySettings.PrivateKey != "" {
				if derived, err := derivePublicKey(stream.RealitySettings.PrivateKey); err == nil {
					pub = derived
				}
			}
			if pub != "" {
				q.Set("pbk", pub)
			}
			if sni := first(stream.RealitySettings.ServerNames); sni != "" {
				q.Set("sni", sni)
			}
			if sid := first(stream.RealitySettings.ShortIds); sid != "" {
				q.Set("sid", sid)
			}
			if fp := stream.RealitySettings.Settings.Fingerprint; fp != "" {
				q.Set("fp", fp)
			}
			if sx := stream.RealitySettings.Settings.SpiderX; sx != "" {
				q.Set("spx", sx)
			}
		}
	case "tls":
		q.Set("security", "tls")
		if stream.TLSSettings != nil {
			if stream.TLSSettings.ServerName != "" {
				q.Set("sni", stream.TLSSettings.ServerName)
			}
			if len(stream.TLSSettings.ALPN) > 0 {
				q.Set("alpn", strings.Join(stream.TLSSettings.ALPN, ","))
			}
			if stream.TLSSettings.AllowInsecure {
				q.Set("allowInsecure", "1")
			}
		}
	}
	applyTransportQuery(q, stream)

	u := &url.URL{
		Scheme:   "vless",
		User:     url.User(uuid),
		Host:     joinHostPort(host, port),
		RawQuery: q.Encode(),
		Fragment: name,
	}
	return u.String()
}

// buildVMessURI emits the v=2 base64-JSON form used by V2rayN. Other clients
// (Passwall, Shadowrocket, etc.) all parse this shape — the newer
// "vmess://userinfo@host:port?..." URI form has weaker tooling support.
func buildVMessURI(name, host string, port int, uuid string, stream xuiStreamSettings) string {
	cfg := map[string]any{
		"v":    "2",
		"ps":   name,
		"add":  host,
		"port": strconv.Itoa(port),
		"id":   uuid,
		"aid":  "0",
		"scy":  "auto",
		"net":  defaultStr(stream.Network, "tcp"),
		"type": "none",
		"host": "",
		"path": "",
		"tls":  "",
		"sni":  "",
		"alpn": "",
		"fp":   "",
	}
	if stream.Security == "tls" {
		cfg["tls"] = "tls"
		if stream.TLSSettings != nil {
			cfg["sni"] = stream.TLSSettings.ServerName
			if len(stream.TLSSettings.ALPN) > 0 {
				cfg["alpn"] = strings.Join(stream.TLSSettings.ALPN, ",")
			}
		}
	}
	switch stream.Network {
	case "ws":
		if stream.WSSettings != nil {
			cfg["path"] = defaultStr(stream.WSSettings.Path, "/")
			if h, ok := stream.WSSettings.Headers["Host"]; ok {
				cfg["host"] = h
			}
		}
	case "grpc":
		if stream.GRPCSettings != nil {
			cfg["path"] = stream.GRPCSettings.ServiceName
			cfg["type"] = "multi"
		}
	}
	body, _ := json.Marshal(cfg)
	return "vmess://" + base64.StdEncoding.EncodeToString(body)
}

// buildTrojanURI emits `trojan://password@host:port?...#name`. Trojan is
// always TLS; the query carries SNI / ALPN / transport opts when present.
func buildTrojanURI(name, host string, port int, password string, stream xuiStreamSettings) string {
	q := url.Values{}
	q.Set("security", defaultStr(stream.Security, "tls"))
	if stream.TLSSettings != nil {
		if stream.TLSSettings.ServerName != "" {
			q.Set("sni", stream.TLSSettings.ServerName)
		}
		if len(stream.TLSSettings.ALPN) > 0 {
			q.Set("alpn", strings.Join(stream.TLSSettings.ALPN, ","))
		}
		if stream.TLSSettings.AllowInsecure {
			q.Set("allowInsecure", "1")
		}
	}
	q.Set("type", defaultStr(stream.Network, "tcp"))
	applyTransportQuery(q, stream)

	u := &url.URL{
		Scheme:   "trojan",
		User:     url.User(password),
		Host:     joinHostPort(host, port),
		RawQuery: q.Encode(),
		Fragment: name,
	}
	return u.String()
}

// buildSSURI emits the SIP002 form `ss://base64(method:password)@host:port#name`.
// We assemble the string by hand instead of going through net/url because
// SIP002's userinfo is itself URL-encoded base64 — passing it through
// url.URL would double-escape the `@` separator.
func buildSSURI(name, host string, port int, method, password string) string {
	creds := base64.RawURLEncoding.EncodeToString([]byte(method + ":" + password))
	return "ss://" + creds + "@" + joinHostPort(host, port) + "#" + url.PathEscape(name)
}

// buildSeparatorURI mirrors protocols.go's emitSeparator for the URI list
// format: a fake Shadowsocks entry pointing at 127.0.0.1:1 so it shows up
// in V2rayN/NG/Shadowrocket's node list as a labeled, non-connectable
// row. Credentials match emitSeparator exactly so admins debugging the
// two outputs see consistent values.
func buildSeparatorURI(name string) string {
	return buildSSURI(name, "127.0.0.1", 1, "chacha20-ietf-poly1305", "psp-separator")
}

// hysteria2Opts carries the optional knobs that influence the URI's
// query string. Mandatory inputs (name, host, port, password) are
// positional arguments so the call site stays compact for the common
// case.
type hysteria2Opts struct {
	SNI          string
	ObfsType     string
	ObfsPassword string
	ALPN         []string
	Insecure     bool
}

// buildHysteria2URI emits the Hysteria 2 subscription URI per
// https://v2.hysteria.network/docs/developers/URI-Scheme/.
//
//	hysteria2://password@host:port/?sni=...&obfs=...&obfs-password=...
//	  &alpn=h3&insecure=0#name
//
// The trailing slash before the query is part of the spec — Hysteria's
// own client refuses URIs without it.
func buildHysteria2URI(name, host string, port int, password string, opts hysteria2Opts) string {
	q := url.Values{}
	if opts.SNI != "" {
		q.Set("sni", opts.SNI)
	}
	// Obfs keys must be absent (not empty) when not configured — the
	// client treats `obfs=` as "salamander with empty password" which
	// the server rejects.
	if opts.ObfsType != "" {
		q.Set("obfs", opts.ObfsType)
		if opts.ObfsPassword != "" {
			q.Set("obfs-password", opts.ObfsPassword)
		}
	}
	if len(opts.ALPN) > 0 {
		q.Set("alpn", strings.Join(opts.ALPN, ","))
	}
	if opts.Insecure {
		q.Set("insecure", "1")
	} else {
		q.Set("insecure", "0")
	}
	return "hysteria2://" + url.QueryEscape(password) + "@" + joinHostPort(host, port) +
		"/?" + q.Encode() + "#" + url.PathEscape(name)
}

// buildSS2022URI emits the EIH form `ss://method:serverPSK:userPSK@host:port#name`
// per SIP022 (https://shadowsocks.org/doc/sip022.html). Unlike SIP002, the
// 2022-blake3-* userinfo MUST NOT be base64url-wrapped — it is the literal
// "method:password" with method and password percent-encoded. The multi-user
// (EIH) password is the colon-joined PSK chain "serverPSK:userPSK"; both PSKs
// are already base64 in 3X-UI's storage, so we keep them verbatim and only
// percent-encode the base64 specials (+ / =) that are unsafe in URI userinfo.
// Wrapping the whole thing in base64 (the old SIP002 trick) makes sing-box,
// shadowsocks-rust and Shadowrocket fail to parse 2022 nodes.
func buildSS2022URI(name, host string, port int, method, serverPSK, userPSK string) string {
	userinfo := method + ":" + url.QueryEscape(serverPSK) + ":" + url.QueryEscape(userPSK)
	return "ss://" + userinfo + "@" + joinHostPort(host, port) + "#" + url.PathEscape(name)
}

func applyTransportQuery(q url.Values, stream xuiStreamSettings) {
	switch stream.Network {
	case "ws":
		if stream.WSSettings != nil {
			q.Set("path", defaultStr(stream.WSSettings.Path, "/"))
			if h, ok := stream.WSSettings.Headers["Host"]; ok {
				q.Set("host", h)
			}
		}
	case "grpc":
		if stream.GRPCSettings != nil {
			q.Set("serviceName", stream.GRPCSettings.ServiceName)
			q.Set("mode", "gun")
		}
	}
}

func joinHostPort(host string, port int) string {
	// IPv6 literals must be bracketed in URI authority; assume any colon-
	// containing host needs wrapping.
	if strings.Contains(host, ":") {
		return "[" + host + "]:" + strconv.Itoa(port)
	}
	return host + ":" + strconv.Itoa(port)
}
