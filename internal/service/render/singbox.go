package render

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/pkg/crypto"
	"github.com/KazuhaHub/passwall-sub-panel/internal/pkg/log"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

func (s *Service) renderSingBox(ctx context.Context, u *domain.User, tpl *domain.Template, items []renderItem, rulesCommon string) (*Output, error) {
	outbounds := s.buildSingBoxOutbounds(ctx, u, items, u.PersonalRules, rulesCommon)
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
	body = substituteInlinePlaceholders(body, mergePlaceholders(s.profilePlaceholders(ctx, u), map[string]string{
		"final_outbound": finalJSON,
	}))

	headers := map[string]string{
		"Content-Type":            "application/json; charset=utf-8",
		"Profile-Update-Interval": "24",
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

func (s *Service) buildSingBoxOutbounds(ctx context.Context, u *domain.User, items []renderItem, ruleParts ...string) []map[string]any {
	out := []map[string]any{
		{"type": "direct", "tag": "direct"},
		{"type": "block", "tag": "block"},
	}

	nodeTags := make([]string, 0, len(items))
	for _, it := range items {
		if it.isSeparator {
			continue
		}
		inb, err := s.fetchInbound(ctx, it.node)
		if err != nil {
			log.Warn("render: skip node, fetch inbound failed",
				"node_id", it.node.ID, "panel_id", it.node.PanelID, "inbound_id", it.node.InboundID, "err", err)
			continue
		}
		block, err := emitSingBoxOutbound(it.name, it.node, u, inb)
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
	out = append(out, buildSingBoxSelectorOutbounds(rules, nodeTags)...)
	return out
}

func emitSingBoxOutbound(tag string, n *domain.Node, u *domain.User, inb *ports.Inbound) (map[string]any, error) {
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
		base["password"] = crypto.DeriveProxyPassword(u.UUID, protocol)
		applySingBoxTLS(base, stream)
		applySingBoxTransport(base, stream)
		return base, nil
	case domain.ProtoSS:
		base["type"] = "shadowsocks"
		base["method"] = settings.Method
		base["password"] = crypto.DeriveProxyPassword(u.UUID, protocol)
		return base, nil
	case domain.ProtoSS2022:
		base["type"] = "shadowsocks"
		base["method"] = settings.Method
		base["password"] = settings.Password + ":" + crypto.DeriveProxyPassword(u.UUID, protocol)
		return base, nil
	}
	return nil, nil
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

func buildSingBoxSelectorOutbounds(rules string, nodeTags []string) []map[string]any {
	targets := withRequiredProxyGroupDependencies(ruleTargetsInOrder(rules))
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
	rules := []map[string]any{}
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
	case "GEOIP":
		return map[string]any{"geoip": []string{strings.ToLower(value)}}
	case "GEOSITE":
		return map[string]any{"geosite": []string{value}}
	case "PROCESS-NAME":
		return map[string]any{"process_name": []string{value}}
	case "DST-PORT":
		if strings.Contains(value, "-") {
			return map[string]any{"port_range": []string{value}}
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
