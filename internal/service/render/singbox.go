package render

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"sort"
	"strconv"
	"strings"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/pkg/crypto"
	"github.com/KazuhaHub/passwall-sub-panel/internal/pkg/log"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

func (s *Service) renderSingBox(ctx context.Context, u *domain.User, tpl *domain.Template, items []renderItem, rulesCommon string, proxyGroupOrder []string, proxyGroupMembers map[string][]domain.ProxyGroupMember, st ports.UISettings) (*Output, error) {
	outbounds := s.buildSingBoxOutbounds(ctx, u, items, proxyGroupOrder, proxyGroupMembers, st, u.PersonalRules, rulesCommon)
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
	// Emit the route.rule_set definitions required by the route rules
	// (GEOSITE/GEOIP) and the template's dns.rules split. Downloads route
	// through the primary proxy group so the .srs files reach GitHub.
	body, err = assembleSingBoxRuleSets(body, singBoxRuleSetDetour(proxyGroupOrder))
	if err != nil {
		return nil, fmt.Errorf("assemble sing-box rule_set: %w", err)
	}

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

func (s *Service) buildSingBoxOutbounds(ctx context.Context, u *domain.User, items []renderItem, preferredOrder []string, memberConfigs map[string][]domain.ProxyGroupMember, st ports.UISettings, ruleParts ...string) []map[string]any {
	out := []map[string]any{
		{"type": "direct", "tag": "direct"},
		{"type": "block", "tag": "block"},
	}

	// Same EmailRules resolution as mihomo's buildProxies — needed so the
	// WireGuard dispatcher can look up the user's peer entry by email.
	emailRules := domain.EmailRules{Domain: st.EmailDomain}
	sharedEmails := s.sharedClientEmailsByNode(ctx, u)

	// Local snapshot for captured nodes, one batched ListInbounds per panel for
	// the un-captured transition-window remainder. See resolveInbounds.
	inboundByNode := s.resolveInbounds(ctx, items, st)

	selectorItems := make([]renderItem, 0, len(items))
	for _, it := range items {
		if it.isSeparator {
			// Same trick as mihomo's emitSeparator + uri-list's
			// buildSeparatorURI: emit a fake shadowsocks outbound at
			// 127.0.0.1:1 so the separator surfaces as a labeled
			// (non-connectable) entry in sing-box's selector
			// dropdown — preserving the visual grouping admins
			// configured. The item goes into selectorItems so dynamic
			// selector expansions include it in the right order.
			out = append(out, map[string]any{
				"type":        "shadowsocks",
				"tag":         it.name,
				"server":      "127.0.0.1",
				"server_port": 1,
				"method":      "chacha20-ietf-poly1305",
				"password":    "psp-separator",
			})
			selectorItems = append(selectorItems, it)
			continue
		}
		inb := inboundByNode[it.node.ID]
		if inb == nil {
			log.Warn("render: skip node, inbound config unavailable (no local snapshot and live fetch failed)",
				"node_id", it.node.ID, "panel_id", it.node.PanelID, "inbound_id", it.node.InboundID)
			continue
		}
		userEmail := clientEmailForNode(u, it.node.ID, emailRules, sharedEmails)
		block, err := emitSingBoxOutbound(it.name, it.node, u, inb, userEmail, it.relay)
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
		selectorItems = append(selectorItems, it)
	}

	rules := strings.TrimSpace(strings.Join(ruleParts, "\n"))
	out = append(out, buildSingBoxSelectorOutboundsWithMembers(rules, selectorItems, preferredOrder, memberConfigs)...)
	return out
}

func emitSingBoxOutbound(tag string, n *domain.Node, u *domain.User, inb *ports.Inbound, userEmail string, relay *domain.RelayLine) (map[string]any, error) {
	var settings xuiInboundSettings
	_ = json.Unmarshal([]byte(inb.Settings), &settings)
	var stream xuiStreamSettings
	_ = json.Unmarshal([]byte(inb.StreamSettings), &stream)

	protocol := crypto.DetectProtocol(inb.Protocol, settings.Method)
	if protocol == "" {
		return nil, nil
	}
	// Relay variant dials the transit front (and overrides SNI / Host on
	// `stream` in place); direct entry uses the node's own address + port.
	server, port := effectiveEndpoint(relay, n.ServerAddress, inb.Port, &stream)
	if server == "" {
		return nil, fmt.Errorf("node %d (%s) missing server_address", n.ID, n.DisplayName)
	}

	base := map[string]any{
		"tag":         tag,
		"server":      server,
		"server_port": port,
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
		// we hand it the (relay-resolved) tag/server/port directly rather
		// than mutating the shared base map.
		opts := parseHysteria2Opts(inb.Settings, inb.StreamSettings)
		if sni := relaySNIOverride(relay); sni != "" {
			opts.SNI = sni
		}
		return buildSingBoxHysteria2Outbound(tag, server, port, u.UUID, opts), nil
	case domain.ProtoAnyTLS:
		base["type"] = "anytls"
		base["password"] = u.UUID
		applySingBoxTLS(base, stream)
		return base, nil
	case domain.ProtoTUIC:
		base["type"] = "tuic"
		base["uuid"] = u.UUID
		base["password"] = u.UUID
		base["congestion_control"] = defaultStr(settings.CongestionControl, "cubic")
		if settings.ZeroRTTHandshake {
			base["zero_rtt_handshake"] = true
		}
		if settings.Heartbeat != "" {
			base["heartbeat"] = settings.Heartbeat
		}
		applySingBoxTLS(base, stream)
		return base, nil
	case domain.ProtoNaive:
		base["type"] = "naive"
		base["username"] = userEmail
		base["password"] = u.UUID
		if settings.QUICCongestionControl != "" {
			base["quic"] = true
			base["quic_congestion_control"] = normalizedNaiveCongestion(settings.QUICCongestionControl)
		}
		applySingBoxTLS(base, stream)
		return base, nil
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
			if len(stream.TLSSettings.ALPN) > 0 {
				tls["alpn"] = stream.TLSSettings.ALPN
			}
			if fingerprint := stream.TLSSettings.Settings.Fingerprint; fingerprint != "" {
				tls["utls"] = map[string]any{"enabled": true, "fingerprint": fingerprint}
			}
		}
		base["tls"] = tls
	}
}

func normalizedNaiveCongestion(value string) string {
	switch value {
	case "bbr_standard":
		return "bbr"
	case "bbr2_variant":
		return "bbr2"
	default:
		return value
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
	items := make([]renderItem, 0, len(nodeTags))
	for _, tag := range nodeTags {
		items = append(items, renderItem{name: tag})
	}
	return buildSingBoxSelectorOutboundsWithMembers(rules, items, preferredOrder, nil)
}

func buildSingBoxSelectorOutboundsWithMembers(rules string, items []renderItem, preferredOrder []string, memberConfigs map[string][]domain.ProxyGroupMember) []map[string]any {
	targets := withRequiredProxyGroupDependencies(ruleTargetsInOrder(rules))
	targets = withConfiguredProxyGroupDependencies(targets, memberConfigs)
	targets = applyProxyGroupOrder(targets, preferredOrder)
	out := make([]map[string]any, 0, len(targets))
	for _, target := range targets {
		members := defaultMembersForTarget(target)
		if configured, ok := memberConfigs[target]; ok {
			members = configured
		}
		choices := singBoxResolvedChoices(resolveConfiguredMembers(members, items))
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

func singBoxResolvedChoices(raw []string) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, item := range raw {
		tag := singBoxOutboundTag(item)
		if tag == "" || seen[tag] {
			continue
		}
		seen[tag] = true
		out = append(out, tag)
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
		// Web QUIC (HTTP/3 over UDP 443): built-in reject → browsers fall back to
		// h2/TCP. Highest priority among route rules (before the 🎮 UDP控制 rule
		// that comes from the ruleset), mirroring the mihomo template's UDP-443
		// REJECT. The `reject` action (1.11+, same floor as sniff/route) drops the
		// QUIC attempt; the TCP retry still follows the rules below, so it's not a
		// direct leak, and proxy-node dials bypass route rules (Hysteria2 safe).
		{"network": "udp", "port": []int{443}, "action": "reject"},
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
			// Explicit "action":"route" is the canonical sing-box rule form;
			// the bare-outbound shorthand still works but emits a deprecation
			// warning as the rule-action migration continues.
			rule["action"] = "route"
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
	case "NETWORK":
		// Clash NETWORK,tcp|udp → sing-box rule "network" field. Lets a rule
		// match purely by transport (e.g. route all UDP to the UDP-control
		// selector). sing-box wants lowercase "tcp"/"udp".
		net := strings.ToLower(strings.TrimSpace(value))
		if net != "tcp" && net != "udp" {
			return nil
		}
		return map[string]any{"network": net}
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
	case "GEOSITE":
		// sing-box 1.8.0 deprecated the inline `geosite` key and 1.12.0
		// removed it; the migration is a remote rule_set reference whose
		// definition assembleSingBoxRuleSets emits. `geosite-<category>`
		// maps to SagerNet's sing-geosite .srs files. Attribute-filtered
		// categories (e.g. `microsoft@cn`) have no standalone .srs, so we
		// still drop those rather than emit a reference that 404s on
		// download.
		cat := strings.ToLower(strings.TrimSpace(value))
		if cat == "" || strings.Contains(cat, "@") {
			return nil
		}
		return map[string]any{"rule_set": []string{"geosite-" + cat}}
	case "GEOIP":
		// Same rule_set migration as GEOSITE; `geoip-<country>` maps to
		// SagerNet's sing-geoip .srs files. Restores IP-based CN routing
		// that the old silent-drop behavior lost on sing-box 1.12+.
		cc := strings.ToLower(strings.TrimSpace(value))
		if cc == "" {
			return nil
		}
		return map[string]any{"rule_set": []string{"geoip-" + cc}}
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

const (
	singGeositeRuleSetBase = "https://raw.githubusercontent.com/SagerNet/sing-geosite/rule-set/"
	singGeoipRuleSetBase   = "https://raw.githubusercontent.com/SagerNet/sing-geoip/rule-set/"
)

// assembleSingBoxRuleSets fills the `{{ rule_sets }}` placeholder with the
// route.rule_set definitions the config needs. It collects every rule_set tag
// referenced anywhere in the body — both the template's dns.rules split and the
// route rules emitted from GEOSITE/GEOIP — so definitions stay in lockstep with
// whatever is actually referenced, with no hardcoded category list to drift.
func assembleSingBoxRuleSets(body, downloadDetour string) (string, error) {
	// Empty the rule_set array first so the body parses as JSON, then collect
	// references and emit the real definitions.
	probe := substituteBlockPlaceholders(body, map[string]string{"rule_sets": "[]"})
	tags, err := collectSingBoxRuleSetRefs(probe)
	if err != nil {
		return "", err
	}
	defsJSON, err := marshalJSONBlock(buildSingBoxRuleSetDefs(tags, downloadDetour))
	if err != nil {
		return "", err
	}
	return substituteBlockPlaceholders(body, map[string]string{"rule_sets": defsJSON}), nil
}

// collectSingBoxRuleSetRefs walks the parsed config and returns the sorted,
// deduped set of tags referenced by `rule_set: [...]` fields. The route-level
// rule_set DEFINITION array holds objects (not strings) under the same key, so
// it is naturally skipped — only string references are collected.
func collectSingBoxRuleSetRefs(jsonStr string) ([]string, error) {
	var root any
	if err := json.Unmarshal([]byte(jsonStr), &root); err != nil {
		return nil, err
	}
	set := map[string]bool{}
	var walk func(any)
	walk = func(v any) {
		switch t := v.(type) {
		case map[string]any:
			for k, val := range t {
				if k == "rule_set" {
					if arr, ok := val.([]any); ok {
						for _, e := range arr {
							if s, ok := e.(string); ok {
								set[s] = true
							}
						}
					}
				}
				walk(val)
			}
		case []any:
			for _, e := range t {
				walk(e)
			}
		}
	}
	walk(root)
	out := make([]string, 0, len(set))
	for s := range set {
		out = append(out, s)
	}
	sort.Strings(out)
	return out, nil
}

// buildSingBoxRuleSetDefs turns rule_set tags into remote rule_set definitions.
// `geosite-*` / `geoip-*` tags resolve to SagerNet's compiled .srs files;
// unknown prefixes are skipped (no URL to point at). download_detour routes the
// .srs download through the proxy so it works from behind the GFW. We use
// download_detour (not the newer http_client.detour) because the current STABLE
// line is 1.13.x — http_client only exists in 1.14-alpha, and sing-box's strict
// DisallowUnknownFields parser would make it a FATAL load error on stable.
// download_detour stays valid through 1.15 (deprecated in 1.14, removed in 1.16).
func buildSingBoxRuleSetDefs(tags []string, downloadDetour string) []map[string]any {
	sorted := append([]string(nil), tags...)
	sort.Strings(sorted)
	out := make([]map[string]any, 0, len(sorted))
	for _, tag := range sorted {
		var srcURL string
		switch {
		case strings.HasPrefix(tag, "geosite-"):
			srcURL = singGeositeRuleSetBase + tag + ".srs"
		case strings.HasPrefix(tag, "geoip-"):
			srcURL = singGeoipRuleSetBase + tag + ".srs"
		default:
			continue
		}
		out = append(out, map[string]any{
			"tag":             tag,
			"type":            "remote",
			"format":          "binary",
			"url":             srcURL,
			"download_detour": downloadDetour,
			"update_interval": "1d",
		})
	}
	return out
}

// singBoxRuleSetDetour picks the outbound used to download remote rule_sets:
// the ruleset's primary proxy group (first non-empty entry of the declared
// proxy-group order), falling back to the conventional main selector. It must
// name a real selectable proxy outbound so the .srs download reaches GitHub.
func singBoxRuleSetDetour(proxyGroupOrder []string) string {
	for _, g := range proxyGroupOrder {
		if strings.TrimSpace(g) != "" {
			return g
		}
	}
	return "🚀 节点选择"
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
