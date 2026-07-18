package sui

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/pkg/nodespec"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

const managedTLSPrefix = "PSP:"

func (c *Client) AddInbound(ctx context.Context, input ports.InboundSpec) (int, error) {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()

	spec, err := nodespec.Decode(input)
	if err != nil {
		return 0, err
	}
	if err := validateSUIInboundRequest(input, spec); err != nil {
		return 0, err
	}
	summaries, err := c.inboundSummaries(ctx)
	if err != nil {
		return 0, err
	}
	tag := uniqueInboundTag(spec.Remark, spec.Protocol, spec.Port, summaries, 0)
	tlsID, err := c.createManagedTLS(ctx, tag, spec.Security)
	if err != nil {
		return 0, err
	}
	body, err := suiInboundFromSpec(spec, tag, tlsID)
	if err != nil {
		c.deleteManagedTLS(ctx, tlsID)
		return 0, err
	}
	var result struct {
		Inbounds []inboundSummary `json:"inbounds"`
	}
	// S-UI interpolates initUsers into a SQL IN clause. "0" is its safe
	// sentinel for creating an empty inbound; a blank value becomes IN ().
	if err := c.saveIntoWithInitialUsers(ctx, "inbounds", "new", body, "0", &result); err != nil {
		if items, readErr := c.inboundSummaries(ctx); readErr == nil {
			for _, item := range items {
				if item.Tag == tag {
					return item.ID, nil
				}
			}
		}
		c.deleteManagedTLS(ctx, tlsID)
		return 0, err
	}
	for _, item := range result.Inbounds {
		if item.Tag == tag {
			return item.ID, nil
		}
	}
	if items, err := c.inboundSummaries(ctx); err == nil {
		for _, item := range items {
			if item.Tag == tag {
				return item.ID, nil
			}
		}
	}
	c.deleteManagedTLS(ctx, tlsID)
	return 0, fmt.Errorf("S-UI created inbound %q but did not return its id", tag)
}

func (c *Client) UpdateInbound(ctx context.Context, id int, input ports.InboundSpec) error {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()

	current, err := c.fullInbound(ctx, id)
	if err != nil {
		return err
	}
	if current == nil {
		return fmt.Errorf("%w: S-UI inbound %d not found", domain.ErrNotFound, id)
	}
	spec, err := nodespec.Decode(input)
	if err != nil {
		return err
	}
	if err := validateSUIInboundRequest(input, spec); err != nil {
		return err
	}
	summaries, err := c.inboundSummaries(ctx)
	if err != nil {
		return err
	}
	tag := uniqueInboundTag(spec.Remark, spec.Protocol, spec.Port, summaries, id)
	oldTLSID := intValue(current["tls_id"])
	newTLSID, err := c.createManagedTLS(ctx, tag, spec.Security)
	if err != nil {
		return err
	}
	body, err := suiInboundFromSpec(spec, tag, newTLSID)
	if err != nil {
		c.deleteManagedTLS(ctx, newTLSID)
		return err
	}
	body = mergeSUIInboundUpdate(current, body)
	body["id"] = id
	if err := c.save(ctx, "inbounds", "edit", body); err != nil {
		if saved, readErr := c.fullInbound(ctx, id); readErr == nil && saved != nil &&
			stringValue(saved["tag"]) == tag && intValue(saved["tls_id"]) == newTLSID {
			if oldTLSID != newTLSID {
				c.deleteManagedTLS(ctx, oldTLSID)
			}
			return nil
		}
		c.deleteManagedTLS(ctx, newTLSID)
		return err
	}
	if oldTLSID != newTLSID {
		c.deleteManagedTLS(ctx, oldTLSID)
	}
	return nil
}

// mergeSUIInboundUpdate preserves native S-UI options that PSP does not model
// (for example multiplex, detour, tcp_multi_path, udp_fragment, addrs and the
// generated out_json) while replacing every field controlled by PSP's form.
// Without this read-modify-write step, editing an imported inbound silently
// erased those options even when the admin changed only its port or TLS data.
func mergeSUIInboundUpdate(current, desired map[string]any) map[string]any {
	out := make(map[string]any, len(current)+len(desired))
	for key, value := range current {
		if key != "users" {
			out[key] = value
		}
	}
	for _, key := range []string{
		"type", "tag", "listen", "listen_port", "tls_id",
		"transport", "method", "password", "network", "managed",
		"obfs", "udp_timeout", "masquerade",
		"padding_scheme", "congestion_control", "auth_timeout",
		"zero_rtt_handshake", "heartbeat", "quic_congestion_control",
		"routing_mark", "tcp_fast_open", "tcp_keep_alive_interval", "tcp_keep_alive",
	} {
		delete(out, key)
	}
	for key, value := range desired {
		out[key] = value
	}
	return out
}

func (c *Client) DelInbound(ctx context.Context, id int) error {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()

	current, err := c.fullInbound(ctx, id)
	if err != nil {
		return err
	}
	if current == nil {
		return nil
	}
	tag := stringValue(current["tag"])
	if tag == "" {
		return fmt.Errorf("%w: S-UI inbound %d has no tag", domain.ErrValidation, id)
	}
	tlsID := intValue(current["tls_id"])
	if err := c.save(ctx, "inbounds", "del", tag); err != nil {
		if saved, readErr := c.fullInbound(ctx, id); readErr != nil || saved != nil {
			return err
		}
	}
	c.deleteManagedTLS(ctx, tlsID)
	return nil
}

func suiInboundFromSpec(spec *nodespec.Spec, tag string, tlsID int) (map[string]any, error) {
	if spec == nil {
		return nil, fmt.Errorf("%w: inbound spec is nil", domain.ErrValidation)
	}
	protocol := spec.Protocol
	if protocol == "ss2022" {
		protocol = "shadowsocks"
	}
	switch protocol {
	case "vless", "vmess", "trojan", "shadowsocks", "hysteria2", "anytls", "tuic", "naive":
	default:
		return nil, fmt.Errorf("%w: protocol %q is not supported by the S-UI adapter", domain.ErrValidation, spec.Protocol)
	}
	if (protocol == "trojan" || protocol == "hysteria2" || protocol == "anytls" ||
		protocol == "tuic" || protocol == "naive") && spec.Security.Mode != "tls" {
		return nil, fmt.Errorf("%w: S-UI %s inbounds require TLS", domain.ErrValidation, protocol)
	}
	if spec.Security.Mode == "reality" && protocol != "vless" {
		return nil, fmt.Errorf("%w: S-UI REALITY is supported only for VLESS", domain.ErrValidation)
	}

	body := map[string]any{
		"type":        protocol,
		"tag":         tag,
		"listen":      spec.Listen,
		"listen_port": spec.Port,
		"tls_id":      tlsID,
	}
	if spec.Listen == "" {
		body["listen"] = "::"
	}
	if spec.Transport.AcceptProxyProtocol {
		return nil, fmt.Errorf("%w: S-UI/sing-box does not support Xray acceptProxyProtocol on this transport", domain.ErrValidation)
	}
	if spec.Socket.AcceptProxyProtocol {
		return nil, fmt.Errorf("%w: S-UI/sing-box does not support Xray sockopt.acceptProxyProtocol", domain.ErrValidation)
	}
	if spec.Socket.TCPUserTimeout > 0 || (spec.Socket.TProxy != "" && spec.Socket.TProxy != "off") {
		return nil, fmt.Errorf("%w: S-UI does not support Xray tcpUserTimeout or tproxy inbound socket options", domain.ErrValidation)
	}
	if spec.Socket.Mark > 0 {
		body["routing_mark"] = spec.Socket.Mark
	}
	if spec.Socket.TCPFastOpen {
		body["tcp_fast_open"] = true
	}
	if spec.Socket.TCPKeepAliveInterval > 0 {
		body["tcp_keep_alive_interval"] = fmt.Sprintf("%ds", spec.Socket.TCPKeepAliveInterval)
	}
	if spec.Socket.TCPKeepAliveIdle > 0 {
		body["tcp_keep_alive"] = fmt.Sprintf("%ds", spec.Socket.TCPKeepAliveIdle)
	}

	switch protocol {
	case "vless", "vmess", "trojan":
		transport, err := suiTransport(spec.Transport)
		if err != nil {
			return nil, err
		}
		if transport != nil {
			body["transport"] = transport
		}
	case "shadowsocks":
		if spec.Shadowsocks.Method == "" || spec.Shadowsocks.Password == "" {
			return nil, fmt.Errorf("%w: S-UI Shadowsocks requires method and server password", domain.ErrValidation)
		}
		body["method"] = spec.Shadowsocks.Method
		body["password"] = spec.Shadowsocks.Password
		if spec.Shadowsocks.IVCheck {
			return nil, fmt.Errorf("%w: S-UI/sing-box does not support Xray Shadowsocks ivCheck", domain.ErrValidation)
		}
		switch network := strings.ToLower(strings.ReplaceAll(spec.Shadowsocks.Network, " ", "")); network {
		case "", "tcp,udp", "udp,tcp":
			// sing-box enables both networks when this field is absent.
		case "tcp", "udp":
			body["network"] = network
		default:
			return nil, fmt.Errorf("%w: S-UI Shadowsocks network must be tcp, udp, or both", domain.ErrValidation)
		}
		body["managed"] = false
	case "hysteria2":
		if spec.Hysteria2.ObfsPassword != "" {
			body["obfs"] = map[string]any{"type": "salamander", "password": spec.Hysteria2.ObfsPassword}
		}
		if spec.Hysteria2.UDPIdleTimeoutSeconds > 0 {
			body["udp_timeout"] = fmt.Sprintf("%ds", spec.Hysteria2.UDPIdleTimeoutSeconds)
		}
		if masquerade := suiMasquerade(spec.Hysteria2); masquerade != nil {
			body["masquerade"] = masquerade
		}
	case "anytls":
		padding := spec.AnyTLS.PaddingScheme
		if len(padding) == 0 {
			padding = []string{
				"stop=8", "0=30-30", "1=100-400",
				"2=400-500,c,500-1000,c,500-1000,c,500-1000,c,500-1000",
				"3=9-9,500-1000", "4=500-1000", "5=500-1000", "6=500-1000", "7=500-1000",
			}
		}
		body["padding_scheme"] = padding
	case "tuic":
		congestion := strings.ToLower(strings.TrimSpace(spec.TUIC.CongestionControl))
		if congestion == "" {
			congestion = "cubic"
		}
		switch congestion {
		case "cubic", "new_reno", "bbr":
			body["congestion_control"] = congestion
		default:
			return nil, fmt.Errorf("%w: S-UI TUIC congestion control must be cubic, new_reno, or bbr", domain.ErrValidation)
		}
		if value := strings.TrimSpace(spec.TUIC.AuthTimeout); value != "" {
			body["auth_timeout"] = value
		}
		if spec.TUIC.ZeroRTTHandshake {
			body["zero_rtt_handshake"] = true
		}
		if value := strings.TrimSpace(spec.TUIC.Heartbeat); value != "" {
			body["heartbeat"] = value
		}
	case "naive":
		network := strings.ToLower(strings.TrimSpace(spec.Naive.Network))
		switch network {
		case "":
		case "tcp", "udp":
			body["network"] = network
		default:
			return nil, fmt.Errorf("%w: S-UI Naive network must be tcp or udp", domain.ErrValidation)
		}
		congestion := strings.ToLower(strings.TrimSpace(spec.Naive.QUICCongestionControl))
		switch congestion {
		case "":
		case "bbr", "bbr_standard", "bbr2", "bbr2_variant", "cubic", "reno":
			body["quic_congestion_control"] = congestion
		default:
			return nil, fmt.Errorf("%w: unsupported S-UI Naive QUIC congestion control %q", domain.ErrValidation, congestion)
		}
	}
	return body, nil
}

func validateSUIInboundRequest(input ports.InboundSpec, spec *nodespec.Spec) error {
	if !input.Enable {
		return fmt.Errorf("%w: S-UI inbounds are always enabled and cannot persist enable=false", domain.ErrValidation)
	}
	if input.ExpiryTime != 0 {
		return fmt.Errorf("%w: S-UI has no per-inbound expiry field", domain.ErrValidation)
	}
	if !jsonPayloadEmpty(input.Allocate) {
		return fmt.Errorf("%w: S-UI does not support Xray inbound allocation settings", domain.ErrValidation)
	}
	if spec != nil && (spec.Sniffing.Enabled || len(spec.Sniffing.DestinationOverride) > 0 ||
		spec.Sniffing.MetadataOnly || spec.Sniffing.RouteOnly) {
		return fmt.Errorf("%w: S-UI/sing-box does not persist Xray per-inbound sniffing", domain.ErrValidation)
	}
	if spec != nil && spec.Socket.AcceptProxyProtocol {
		return fmt.Errorf("%w: S-UI/sing-box does not support Xray sockopt.acceptProxyProtocol", domain.ErrValidation)
	}
	return nil
}

func jsonPayloadEmpty(raw string) bool {
	if strings.TrimSpace(raw) == "" {
		return true
	}
	var value any
	if err := json.Unmarshal([]byte(raw), &value); err != nil {
		return false
	}
	switch typed := value.(type) {
	case nil:
		return true
	case map[string]any:
		return len(typed) == 0
	case []any:
		return len(typed) == 0
	default:
		return false
	}
}

func suiTransport(in nodespec.Transport) (map[string]any, error) {
	switch in.Type {
	case "", "tcp":
		if in.TCPHeaderType != "" && in.TCPHeaderType != "none" {
			return nil, fmt.Errorf("%w: S-UI does not support Xray TCP header type %q", domain.ErrValidation, in.TCPHeaderType)
		}
		return nil, nil
	case "ws":
		out := map[string]any{"type": "ws"}
		putNonEmpty(out, "path", in.Path)
		if len(in.Headers) > 0 {
			out["headers"] = in.Headers
		} else if in.Host != "" {
			out["headers"] = map[string]string{"Host": in.Host}
		}
		return out, nil
	case "grpc":
		if in.Authority != "" || in.MultiMode {
			return nil, fmt.Errorf("%w: S-UI does not support Xray gRPC authority or multiMode", domain.ErrValidation)
		}
		out := map[string]any{"type": "grpc"}
		putNonEmpty(out, "service_name", in.ServiceName)
		return out, nil
	case "httpupgrade":
		out := map[string]any{"type": "httpupgrade"}
		putNonEmpty(out, "host", in.Host)
		putNonEmpty(out, "path", in.Path)
		if len(in.Headers) > 0 {
			out["headers"] = in.Headers
		}
		return out, nil
	case "http":
		out := map[string]any{"type": "http"}
		putNonEmpty(out, "path", in.Path)
		if in.Host != "" {
			out["host"] = []string{in.Host}
		}
		return out, nil
	default:
		return nil, fmt.Errorf("%w: transport %q is not supported by the S-UI adapter", domain.ErrValidation, in.Type)
	}
}

func suiMasquerade(in nodespec.Hysteria2) map[string]any {
	switch in.MasqueradeType {
	case "proxy":
		return map[string]any{"type": "proxy", "url": in.MasqueradeData}
	case "file":
		return map[string]any{"type": "file", "directory": in.MasqueradeData}
	case "string":
		return map[string]any{"type": "string", "content": in.MasqueradeData}
	default:
		return nil
	}
}

func (c *Client) createManagedTLS(ctx context.Context, tag string, security nodespec.Security) (int, error) {
	if security.Mode == "none" || security.Mode == "" {
		return 0, nil
	}
	server, client, err := suiTLS(security)
	if err != nil {
		return 0, err
	}
	serverJSON, _ := json.Marshal(server)
	clientJSON, _ := json.Marshal(client)
	name := fmt.Sprintf("%s %s %d", managedTLSPrefix, tag, time.Now().UnixNano())
	model := tlsModel{Name: name, Server: serverJSON, Client: clientJSON}
	var result struct {
		TLS []tlsModel `json:"tls"`
	}
	if err := c.saveInto(ctx, "tls", "new", model, &result); err != nil {
		// The transaction may have committed even when the response was lost or
		// malformed. The name is unique, so a read-back can safely recover the
		// created id and prevent a retry from leaking duplicate TLS rows.
		if items, readErr := c.tlsModels(ctx); readErr == nil {
			for _, item := range items {
				if item.Name == name {
					return item.ID, nil
				}
			}
		}
		return 0, err
	}
	for _, item := range result.TLS {
		if item.Name == name {
			return item.ID, nil
		}
	}
	// Current S-UI returns the refreshed TLS list from /save. Keep a read-back
	// fallback for older compatible builds that acknowledge the save without
	// embedding that list in the response.
	items, err := c.tlsModels(ctx)
	if err == nil {
		for _, item := range items {
			if item.Name == name {
				return item.ID, nil
			}
		}
	}
	return 0, fmt.Errorf("S-UI created TLS %q but did not return its id", name)
}

func suiTLS(security nodespec.Security) (map[string]any, map[string]any, error) {
	switch security.Mode {
	case "tls":
		if security.TLS.RejectUnknownSNI {
			return nil, nil, fmt.Errorf("%w: S-UI/sing-box does not support rejectUnknownSni", domain.ErrValidation)
		}
		server := map[string]any{"enabled": true}
		client := map[string]any{"enabled": true}
		putNonEmpty(server, "server_name", security.TLS.ServerName)
		putNonEmpty(client, "server_name", security.TLS.ServerName)
		if len(security.TLS.ALPN) > 0 {
			server["alpn"] = security.TLS.ALPN
			client["alpn"] = security.TLS.ALPN
		}
		putNonEmpty(server, "min_version", security.TLS.MinVersion)
		putNonEmpty(server, "max_version", security.TLS.MaxVersion)
		putNonEmpty(server, "certificate_path", security.TLS.CertificatePath)
		putNonEmpty(server, "key_path", security.TLS.KeyPath)
		if security.TLS.Certificate != "" {
			server["certificate"] = []string{security.TLS.Certificate}
		}
		if security.TLS.Key != "" {
			server["key"] = []string{security.TLS.Key}
		}
		if security.TLS.AllowInsecure {
			client["insecure"] = true
		}
		if security.TLS.Fingerprint != "" {
			client["utls"] = map[string]any{"enabled": true, "fingerprint": security.TLS.Fingerprint}
		}
		if (security.TLS.CertificatePath == "") != (security.TLS.KeyPath == "") ||
			(security.TLS.Certificate == "") != (security.TLS.Key == "") {
			return nil, nil, fmt.Errorf("%w: TLS certificate and key must be supplied together", domain.ErrValidation)
		}
		if security.TLS.CertificatePath == "" && security.TLS.Certificate == "" {
			return nil, nil, fmt.Errorf("%w: S-UI TLS inbound requires a certificate and private key", domain.ErrValidation)
		}
		return server, client, nil
	case "reality":
		r := security.Reality
		if r.Xver != 0 || r.MaxClientVersion != "" || (r.MinClientVersion != "" && r.MinClientVersion != "1.0.0") {
			return nil, nil, fmt.Errorf("%w: S-UI/sing-box does not support Xray REALITY xver or client-version gates", domain.ErrValidation)
		}
		if r.HandshakeServer == "" || r.HandshakePort < 1 || r.PrivateKey == "" || r.PublicKey == "" || len(r.ServerNames) == 0 {
			return nil, nil, fmt.Errorf("%w: S-UI REALITY requires server name, handshake target, private key, and public key", domain.ErrValidation)
		}
		serverReality := map[string]any{
			"enabled":     true,
			"handshake":   map[string]any{"server": r.HandshakeServer, "server_port": r.HandshakePort},
			"private_key": r.PrivateKey,
			"short_id":    r.ShortIDs,
		}
		if r.MaxTimeDiffMillis > 0 {
			serverReality["max_time_difference"] = fmt.Sprintf("%dms", r.MaxTimeDiffMillis)
		}
		server := map[string]any{
			"enabled": true, "server_name": r.ServerNames[0], "reality": serverReality,
		}
		clientReality := map[string]any{"enabled": true, "public_key": r.PublicKey}
		if len(r.ShortIDs) > 0 {
			clientReality["short_id"] = r.ShortIDs[0]
		}
		client := map[string]any{
			"enabled": true, "server_name": r.ServerNames[0], "reality": clientReality,
		}
		if r.Fingerprint != "" {
			client["utls"] = map[string]any{"enabled": true, "fingerprint": r.Fingerprint}
		}
		return server, client, nil
	default:
		return nil, nil, fmt.Errorf("%w: security %q is not supported by the S-UI adapter", domain.ErrValidation, security.Mode)
	}
}

// deleteManagedTLS is intentionally best-effort. The inbound write already
// succeeded, so a cleanup failure must not make PSP retry that destructive
// operation. Only records carrying our prefix and no longer referenced by an
// inbound are eligible; user-managed/shared S-UI TLS rows are never touched.
func (c *Client) deleteManagedTLS(ctx context.Context, id int) {
	if id <= 0 {
		return
	}
	tlsByID, err := c.tlsModels(ctx)
	if err != nil {
		return
	}
	item, ok := tlsByID[id]
	if !ok || !strings.HasPrefix(item.Name, managedTLSPrefix) {
		return
	}
	inbounds, err := c.inboundSummaries(ctx)
	if err != nil {
		return
	}
	for _, inbound := range inbounds {
		if inbound.TLSID == id {
			return
		}
	}
	_ = c.save(ctx, "tls", "del", id)
}

func uniqueInboundTag(remark, protocol string, port int, existing []inboundSummary, excludeID int) string {
	base := strings.TrimSpace(remark)
	if base == "" {
		base = fmt.Sprintf("psp-%s-%d", protocol, port)
	}
	used := make(map[string]bool, len(existing))
	for _, item := range existing {
		if item.ID != excludeID {
			used[item.Tag] = true
		}
	}
	if !used[base] {
		return base
	}
	for n := 2; ; n++ {
		candidate := fmt.Sprintf("%s (%d)", base, n)
		if !used[candidate] {
			return candidate
		}
	}
}

func putNonEmpty(dst map[string]any, key, value string) {
	if strings.TrimSpace(value) != "" {
		dst[key] = value
	}
}
