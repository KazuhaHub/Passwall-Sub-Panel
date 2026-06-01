package render

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"strconv"
	"strings"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/pkg/crypto"
	"github.com/KazuhaHub/passwall-sub-panel/internal/pkg/log"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

func (s *Service) renderSingBox(ctx context.Context, u *domain.User, tpl *domain.Template, items []renderItem, rulesCommon string, proxyGroupOrder []string, st ports.UISettings) (*Output, error) {
	outbounds := s.buildSingBoxOutbounds(ctx, u, items, proxyGroupOrder, st, u.PersonalRules, rulesCommon)
	outboundsJSON, err := marshalJSONBlock(outbounds)
	if err != nil {
		return nil, fmt.Errorf("marshal sing-box outbounds: %w", err)
	}

	rules, finalOutbound := buildSingBoxRouteRules(u.PersonalRules, rulesCommon)
	rulesJSON, err := marshalJSONBlock(rules)
	if err != nil {
		return nil, fmt.Errorf("marshal sing-box route rules: %w", err)
	}
	finalJSON, err := marshalJSONString(finalOutbound)
	if err != nil {
		return nil, fmt.Errorf("marshal sing-box final outbound: %w", err)
	}

	body := substituteBlockPlaceholders(tpl.Content, map[string]string{
		"outbounds":   outboundsJSON,
		"route_rules": rulesJSON,
	})
	body = substituteInlinePlaceholders(body, mergePlaceholders(s.profilePlaceholders(u, st), map[string]string{
		"final_outbound": finalJSON,
	}))

	// Build profile name for Content-Disposition header.
	profileName := buildProfileName(u, st)
	encodedName := url.PathEscape(profileName)

	updateInterval := 24
	if st.SubUpdateIntervalHours > 0 {
		updateInterval = st.SubUpdateIntervalHours
	}

	headers := map[string]string{
		"Content-Type":            "application/json; charset=utf-8",
		"Profile-Update-Interval": strconv.Itoa(updateInterval),
		"Content-Disposition":     `attachment; filename*=UTF-8''` + encodedName,
		"Profile-Title":           profileName,
	}
	if info := s.buildSubInfo(ctx, u); info != "" {
		headers["Subscription-Userinfo"] = info
	}

	return &Output{
		Body:        []byte(body),
		ContentType: "application/json; charset=utf-8",
		Headers:     headers,
	}, nil
}

func (s *Service) buildSingBoxOutbounds(ctx context.Context, u *domain.User, items []renderItem, preferredOrder []string, st ports.UISettings, ruleParts ...string) []map[string]any {
	out := []map[string]any{
		{"type": "direct", "tag": "direct"},
		{"type": "block", "tag": "block"},
	}

	// Same EmailRules resolution as mihomo's buildProxies — needed so the
	// WireGuard dispatcher can look up the user's peer entry by email.
	emailRules := domain.EmailRules{Domain: st.EmailDomain}

	// Local snapshot for captured nodes, one batched ListInbounds per panel for
	// the un-captured transition-window remainder. See resolveInbounds.
	inboundByNode := s.resolveInbounds(ctx, items, st)

	nodeTags := make([]string, 0, len(items))
	for _, it := range items {
		if it.isSeparator {
			// Same trick as mihomo's emitSeparator + uri-list's
			// buildSeparatorURI: emit a fake shadowsocks outbound at
			// 127.0.0.1:1 so the separator surfaces as a labeled
			// (non-connectable) entry in sing-box's selector
			// dropdown — preserving the visual grouping admins
			// configured. Tag goes into nodeTags so @all-style
			// selector expansions include it in the right order.
			out = append(out, map[string]any{
				"type":        "shadowsocks",
				"tag":         it.name,
				"server":      "127.0.0.1",
				"server_port": 1,
				"method":      "chacha20-ietf-poly1305",
				"password":    "psp-separator",
			})
			nodeTags = append(nodeTags, it.name)
			continue
		}
		inb := inboundByNode[it.node.ID]
		if inb == nil {
			log.Warn("render: skip node, inbound config unavailable (no local snapshot and live fetch failed)",
				"node_id", it.node.ID, "panel_id", it.node.PanelID, "inbound_id", it.node.InboundID)
			continue
		}
		userEmail := u.ClientEmail(it.node.ID, emailRules)
		block, err := emitSingBoxOutbound(it.name, it.node, u, inb, userEmail)
		if err != nil {
			log.Warn("render: skip node, emit sing-box failed", "node_id", it.node.ID, "err", err)
			continue
		}
		if block == nil {
			log.Warn("render: skip node, unsupported protocol",
				"node_id", it.node.ID, "protocol", inb.Protocol)
			continue
		}
		out = append(out, block)
		nodeTags = append(nodeTags, it.name)
	}

	rules := strings.TrimSpace(strings.Join(ruleParts, "\n"))
	out = append(out, buildSingBoxSelectorOutbounds(rules, nodeTags, preferredOrder)...)
	return out
}

func emitSingBoxOutbound(tag string, n *domain.Node, u *domain.User, inb *ports.Inbound, userEmail string) (map[string]any, error) {
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
		"tag":         tag,
		"server":      n.ServerAddress,
		"server_port": inb.Port,
	}

	switch protocol {
	case domain.ProtoVLESS:
		base["type"] = "vless"
		base["uuid"] = u.UUID
		if n.Flow != "" {
			base["flow"] = n.Flow
		}
		applySingBoxTLS(base, stream)
		applySingBoxTransport(base, stream)
		return base, nil
	case domain.ProtoVMess:
		base["type"] = "vmess"
		base["uuid"] = u.UUID
		base["security"] = "auto"
		base["alter_id"] = 0
		applySingBoxTLS(base, stream)
		applySingBoxTransport(base, stream)
		return base, nil
	case domain.ProtoTrojan:
		base["type"] = "trojan"
		base["password"] = crypto.DeriveProxyPassword(u.UUID, protocol, settings.Method)
		applySingBoxTLS(base, stream)
		applySingBoxTransport(base, stream)
		return base, nil
	case domain.ProtoSS:
		base["type"] = "shadowsocks"
		base["method"] = settings.Method
		base["password"] = crypto.DeriveProxyPassword(u.UUID, protocol, settings.Method)
		return base, nil
	case domain.ProtoSS2022:
		base["type"] = "shadowsocks"
		base["method"] = settings.Method
		base["password"] = settings.Password + ":" + crypto.DeriveProxyPassword(u.UUID, protocol, settings.Method)
		return base, nil
	case domain.ProtoHysteria2:
		// buildSingBoxHysteria2Outbound takes its own base map shape, so
		// we hand it the tag/server/port directly rather than mutating the
		// shared base map.
		return buildSingBoxHysteria2Outbound(tag, n.ServerAddress, inb.Port, u.UUID,
			parseHysteria2Opts(inb.Settings, inb.StreamSettings)), nil
	}
	return nil, nil
}

// buildSingBoxHysteria2Outbound emits the sing-box outbound JSON per
// https://sing-box.sagernet.org/configuration/outbound/hysteria2/. Obfs
// is encoded as a nested object and only included when configured —
// passing `obfs: {}` would enable salamander with an empty password,
// which the server rejects.
func buildSingBoxHysteria2Outbound(tag, server string, port int, password string, opts hysteria2Opts) map[string]any {
	out := map[string]any{
		"tag":         tag,
		"type":        "hysteria2",
		"server":      server,
		"server_port": port,
		"password":    password,
	}
	if opts.ObfsType != "" {
		obfs := map[string]any{"type": opts.ObfsType}
		if opts.ObfsPassword != "" {
			obfs["password"] = opts.ObfsPassword
		}
		out["obfs"] = obfs
	}
	tls := map[string]any{
		"enabled":  true,
		"insecure": opts.Insecure,
	}
	if opts.SNI != "" {
		tls["server_name"] = opts.SNI
	}
	if len(opts.ALPN) > 0 {
		tls["alpn"] = opts.ALPN
	}
	out["tls"] = tls
	return out
}

func applySingBoxTLS(base map[string]any, stream xuiStreamSettings) {
	switch stream.Security {
	case "reality":
		tls := map[string]any{"enabled": true}
		if stream.RealitySettings != nil {
			tls["server_name"] = first(stream.RealitySettings.ServerNames)
			fp := defaultStr(stream.RealitySettings.Settings.Fingerprint, "chrome")
			tls["utls"] = map[string]any{"enabled": true, "fingerprint": fp}
			pub := stream.RealitySettings.Settings.PublicKey
			if pub == "" && stream.RealitySettings.PrivateKey != "" {
				if derived, err := derivePublicKey(stream.RealitySettings.PrivateKey); err == nil {
					pub = derived
				}
			}
			tls["reality"] = map[string]any{
				"enabled":    true,
				"public_key": pub,
				"short_id":   first(stream.RealitySettings.ShortIds),
			}
		}
		base["tls"] = tls
	case "tls":
		tls := map[string]any{"enabled": true}
		if stream.TLSSettings != nil {
			tls["server_name"] = stream.TLSSettings.ServerName
			tls["insecure"] = stream.TLSSettings.AllowInsecure
		}
		base["tls"] = tls
	}
}

func applySingBoxTransport(base map[string]any, stream xuiStreamSettings) {
	switch stream.Network {
	case "ws":
		if stream.WSSettings != nil {
			transport := map[string]any{
				"type": "ws",
				"path": defaultStr(stream.WSSettings.Path, "/"),
			}
			if len(stream.WSSettings.Headers) > 0 {
				transport["headers"] = stream.WSSettings.Headers
			}
			base["transport"] = transport
		}
	case "grpc":
		if stream.GRPCSettings != nil {
			base["transport"] = map[string]any{
				"type":         "grpc",
				"service_name": stream.GRPCSettings.ServiceName,
			}
		}
	}
}

func buildSingBoxSelectorOutbounds(rules string, nodeTags []string, preferredOrder []string) []map[string]any {
	targets := withRequiredProxyGroupDependencies(ruleTargetsInOrder(rules))
	targets = applyProxyGroupOrder(targets, preferredOrder)
	out := make([]map[string]any, 0, len(targets))
	for _, target := range targets {
		choices := singBoxSelectorChoices(proxyGroupChoices(target), nodeTags)
		if len(choices) == 0 {
			choices = []string{"direct"}
		}
		selector := map[string]any{
			"type":      "selector",
			"tag":       target,
			"outbounds": choices,
			"default":   choices[0],
		}
		out = append(out, selector)
	}
	return out
}

func singBoxSelectorChoices(raw []string, nodeTags []string) []string {
	seen := map[string]bool{}
	out := []string{}
	add := func(tag string) {
		if tag == "" || seen[tag] {
			return
		}
		seen[tag] = true
		out = append(out, tag)
	}
	for _, item := range raw {
		switch item {
		case "@all":
			for _, tag := range nodeTags {
				add(tag)
			}
		default:
			add(singBoxOutboundTag(item))
		}
	}
	return out
}

func buildSingBoxRouteRules(ruleParts ...string) ([]map[string]any, string) {
	// Legacy inbound `sniff: true` was deprecated in sing-box 1.11.0
	// and removed in 1.13.0; the migration is a global sniff action
	// at the head of route.rules — match-all, runs before subsequent
	// route rules. See
	// https://sing-box.sagernet.org/migration/#migrate-legacy-inbound-fields-to-rule-actions
	rules := []map[string]any{
		{"action": "sniff"},
	}
	finalOutbound := "direct"
	for _, part := range ruleParts {
		for _, rawLine := range strings.Split(part, "\n") {
			kind, value, target, ok := parseClashRuleLine(rawLine)
			if !ok {
				continue
			}
			outbound := singBoxOutboundTag(target)
			if kind == "MATCH" {
				finalOutbound = outbound
				return rules, finalOutbound
			}
			rule := singBoxRouteRule(kind, value)
			if len(rule) == 0 {
				continue
			}
			rule["outbound"] = outbound
			rules = append(rules, rule)
		}
	}
	return rules, finalOutbound
}

func parseClashRuleLine(rawLine string) (kind, value, target string, ok bool) {
	line := strings.TrimSpace(rawLine)
	line = strings.TrimPrefix(line, "- ")
	if line == "" || strings.HasPrefix(line, "#") || strings.Contains(line, "{{") {
		return "", "", "", false
	}
	parts := splitCSVLine(line)
	if len(parts) < 2 {
		return "", "", "", false
	}
	kind = strings.ToUpper(normalizeRulePart(parts[0]))
	if kind == "MATCH" {
		target = normalizeRulePart(parts[1])
		return kind, "", target, target != ""
	}
	value = normalizeRulePart(parts[1])
	for i := len(parts) - 1; i >= 2; i-- {
		candidate := normalizeRulePart(parts[i])
		if candidate == "" || candidate == "no-resolve" {
			continue
		}
		target = candidate
		break
	}
	return kind, value, target, value != "" && target != ""
}

func splitCSVLine(line string) []string {
	r := csv.NewReader(strings.NewReader(line))
	r.FieldsPerRecord = -1
	parts, err := r.Read()
	if err == nil {
		return parts
	}
	if err != nil && err != io.EOF {
		return strings.Split(line, ",")
	}
	return parts
}

func singBoxRouteRule(kind, value string) map[string]any {
	switch kind {
	case "DOMAIN":
		return map[string]any{"domain": []string{value}}
	case "DOMAIN-SUFFIX":
		return map[string]any{"domain_suffix": []string{value}}
	case "DOMAIN-KEYWORD":
		return map[string]any{"domain_keyword": []string{value}}
	case "IP-CIDR", "IP-CIDR6":
		return map[string]any{"ip_cidr": []string{value}}
	case "SRC-IP-CIDR", "SOURCE-IP-CIDR":
		return map[string]any{"source_ip_cidr": []string{value}}
	case "GEOIP", "GEOSITE":
		// Deprecated in sing-box 1.8.0, REMOVED in 1.12.0. The migration
		// path is the rule_set block at the top level of route, which
		// requires the template to also carry the corresponding
		// rule_set definitions (urls, download_detour, formats…) —
		// that's a bigger refactor. For now: silently drop GEOIP /
		// GEOSITE entries so configs at least parse on 1.12+. Users
		// who want IP-based CN routing back can replace
		// `GEOIP,CN,...` with concrete IP-CIDR rules in their ruleset
		// until rule_set support lands.
		return nil
	case "PROCESS-NAME":
		return map[string]any{"process_name": []string{value}}
	case "DST-PORT":
		if before, after, isRange := strings.Cut(value, "-"); isRange {
			// sing-box port_range uses colon syntax ("8000:9000"), NOT Clash's
			// hyphen ("8000-9000") — emitting the hyphen made sing-box reject the
			// rule. Convert, and validate both bounds parse as ints.
			lo, hi := strings.TrimSpace(before), strings.TrimSpace(after)
			if _, e1 := strconv.Atoi(lo); e1 == nil {
				if _, e2 := strconv.Atoi(hi); e2 == nil {
					return map[string]any{"port_range": []string{lo + ":" + hi}}
				}
			}
			return nil
		}
		if port, err := strconv.Atoi(value); err == nil {
			return map[string]any{"port": []int{port}}
		}
	}
	return nil
}

func singBoxOutboundTag(target string) string {
	switch target {
	case "DIRECT":
		return "direct"
	case "REJECT", "REJECT-DROP", "REJECT-DROP-BIT":
		return "block"
	case "PASS":
		return "direct"
	default:
		return target
	}
}

func marshalJSONBlock(v any) (string, error) {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return "", err
	}
	return string(b), nil
}

func marshalJSONString(v string) (string, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

func mergePlaceholders(base map[string]string, extra map[string]string) map[string]string {
	out := make(map[string]string, len(base)+len(extra))
	for k, v := range base {
		out[k] = v
	}
	for k, v := range extra {
		out[k] = v
	}
	return out
}
