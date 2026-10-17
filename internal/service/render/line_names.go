package render

import (
	"encoding/csv"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/google/uuid"
	"gopkg.in/yaml.v3"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
)

type subscriptionLineName struct {
	nodeID int64
	lineID string
	base   string
	legacy string
	token  string
}

// A catalog is request-local. Public names are aliases, never identities.
// URI and admin previews keep using the original renderItem.name values.
type lineNameCatalog struct {
	lines   map[string]subscriptionLineName
	aliases map[string][]string
	probe   string
	origin  string
}

func newLineNameCatalog(items []renderItem, flags bool, nodes ...[]*domain.Node) (*lineNameCatalog, []renderItem) {
	catalog := &lineNameCatalog{lines: map[string]subscriptionLineName{}, aliases: map[string][]string{}}
	bases := append([]renderItem(nil), items...)
	for i := range bases {
		if bases[i].baseName != "" {
			bases[i].name = bases[i].baseName
		}
	}
	if flags {
		applyRegionFlagPrefix(bases) // Reuse, do not reimplement, the flag policy.
	}
	internal := append([]renderItem(nil), items...)
	prefix := "psp-line-" + uuid.NewString() + "-"
	catalog.probe = prefix + "probe"
	for i, item := range items {
		if item.node == nil || item.isSeparator {
			continue
		}
		token := fmt.Sprintf("%s%d:%s", prefix, item.node.ID, item.lineID())
		line := subscriptionLineName{nodeID: item.node.ID, lineID: item.lineID(), base: bases[i].name, legacy: item.name, token: token}
		catalog.add(line)
		internal[i].name = token
	}
	// Register current but unavailable definitions as tombstone aliases. An
	// explicit reference to a stopped/deleted route must never select a sibling.
	if len(nodes) > 0 {
		for _, node := range nodes[0] {
			if node == nil || node.IsSeparator() {
				continue
			}
			definitions := []renderItem{{node: node, name: node.DisplayName}}
			for i := range node.Relays {
				definitions = append(definitions, renderItem{node: node, relay: &node.Relays[i], name: relayEntryName(node.DisplayName, node.Relays[i])})
			}
			for i := range node.RouteState.Lines {
				definitions = append(definitions, renderItem{node: node, route: &node.RouteState.Lines[i], name: routeEntryName(node.DisplayName, node.RouteState.Lines[i])})
			}
			if flags {
				applyRegionFlagPrefix(definitions)
			}
			for _, definition := range definitions {
				token := fmt.Sprintf("%s%d:%s", prefix, node.ID, definition.lineID())
				if _, exists := catalog.lines[token]; !exists {
					catalog.add(subscriptionLineName{nodeID: node.ID, lineID: definition.lineID(), base: definition.name, legacy: definition.name, token: token})
				}
			}
		}
	}
	return catalog, internal
}

func (c *lineNameCatalog) add(line subscriptionLineName) {
	c.lines[line.token] = line
	for _, alias := range []string{line.base, line.legacy, fmt.Sprintf("%s [%d:%s]", line.base, line.nodeID, line.lineID)} {
		found := false
		for _, existing := range c.aliases[alias] {
			found = found || existing == line.token
		}
		if !found {
			c.aliases[alias] = append(c.aliases[alias], line.token)
		}
	}
}

func (c *lineNameCatalog) resolve(name string, fixed, available map[string]bool) (string, error) {
	if fixed[name] {
		return name, nil
	}
	if _, internal := c.lines[name]; internal {
		if !available[name] {
			return "", fmt.Errorf("unavailable subscription line %q", c.lines[name].legacy)
		}
		return name, nil
	}
	aliases := c.aliases[name]
	if len(aliases) > 1 {
		identities := make([]string, 0, len(aliases))
		for _, token := range aliases {
			line := c.lines[token]
			identities = append(identities, fmt.Sprintf("%d:%s", line.nodeID, line.lineID))
		}
		sort.Strings(identities)
		return "", fmt.Errorf("ambiguous subscription line %q (%s); use an explicit line reference", name, strings.Join(identities, ", "))
	}
	if len(aliases) == 1 {
		if !available[aliases[0]] {
			return "", fmt.Errorf("unavailable subscription line %q", name)
		}
		return aliases[0], nil
	}
	return name, nil
}

func (c *lineNameCatalog) allocate(kept, fixed map[string]bool) map[string]string {
	counts := map[string]int{}
	lines := []subscriptionLineName{}
	for token, line := range c.lines {
		if kept[token] {
			counts[line.base]++
			lines = append(lines, line)
		}
	}
	sort.Slice(lines, func(i, j int) bool {
		if lines[i].nodeID != lines[j].nodeID {
			return lines[i].nodeID < lines[j].nodeID
		}
		return lines[i].lineID < lines[j].lineID
	})
	reserved := map[string]bool{}
	for name := range fixed {
		reserved[name] = true
	}
	result := map[string]string{}
	// Reserve all genuinely unique base labels before allocating any suffixes.
	for _, line := range lines {
		if counts[line.base] == 1 && !fixed[line.base] {
			result[line.token] = line.base
			reserved[line.base] = true
		}
	}
	for _, line := range lines {
		if _, assigned := result[line.token]; assigned {
			continue
		}
		base := fmt.Sprintf("%s [%d:%s]", line.base, line.nodeID, line.lineID)
		name := base
		for index := 2; reserved[name]; index++ {
			name = fmt.Sprintf("%s [%d]", base, index)
		}
		result[line.token], reserved[name] = name, true
	}
	return result
}

func configuredGroupNames(members map[string][]domain.ProxyGroupMember, options map[string]domain.ProxyGroupOptions) map[string]bool {
	fixed := map[string]bool{"🚀 节点选择": true, "🎮 UDP控制": true}
	for name, groupMembers := range members {
		fixed[name] = true
		for _, member := range groupMembers {
			if member.Kind == "proxy_group" {
				fixed[member.Value] = true
			}
		}
	}
	for name := range options {
		fixed[name] = true
	}
	return fixed
}

// Only the outbound target is rewritten. Preserve all condition text and flags,
// including nested logical expressions, quoting and no-resolve/src suffixes.
func rewriteRuleTarget(line string, transform func(string) (string, error), rawPolicy ...bool) (string, error) {
	fields := splitRuleFields(line)
	target := ruleOutboundTarget(fields)
	spans := ruleFieldSpans(line)
	if len(rawPolicy) > 0 && rawPolicy[0] {
		target = mihomoRuleTarget(line)
	}
	if target == "" {
		return line, nil
	}
	replacement, err := transform(target)
	if err == nil && len(rawPolicy) > 0 && rawPolicy[0] && strings.ContainsAny(replacement, ",\r\n") {
		return "", fmt.Errorf("mihomo rule target %q cannot contain a comma or newline; reference this line through a proxy group", replacement)
	}
	if err != nil || replacement == target {
		return line, err
	}
	if index := ruleOutboundTargetIndex(fields); index >= 0 && index < len(spans) {
		span := spans[index]
		part := line[span[0]:span[1]]
		trimmed := strings.TrimSpace(part)
		begin := span[0] + strings.Index(part, trimmed)
		end := begin + len(trimmed)
		if len(rawPolicy) > 0 && rawPolicy[0] {
			return line[:begin] + replacement + line[end:], nil
		}
		var encoded strings.Builder
		writer := csv.NewWriter(&encoded)
		if err := writer.Write([]string{replacement}); err != nil {
			return "", err
		}
		writer.Flush()
		return line[:begin] + strings.TrimSuffix(encoded.String(), "\n") + line[end:], writer.Error()
	}
	return "", fmt.Errorf("cannot locate subscription rule target %q", target)
}

func mihomoRuleTarget(line string) string {
	fields := splitRuleFields(line)
	spans := ruleFieldSpans(line)
	if index := ruleOutboundTargetIndex(fields); index >= 0 && index < len(spans) {
		return strings.TrimSpace(line[spans[index][0]:spans[index][1]])
	}
	return ""
}

func ruleFieldSpans(line string) [][2]int {
	start, depth := 0, 0
	var quote byte
	spans := [][2]int{}
	for index := 0; index < len(line); index++ {
		ch := line[index]
		if quote != 0 {
			if ch == '\\' && index+1 < len(line) {
				index++
			} else if ch == quote {
				if index+1 < len(line) && line[index+1] == quote {
					index++
				} else {
					quote = 0
				}
			}
			continue
		}
		switch ch {
		case '\'', '"':
			quote = ch
		case '(':
			depth++
		case ')':
			if depth > 0 {
				depth--
			}
		case ',':
			if depth == 0 {
				spans = append(spans, [2]int{start, index})
				start = index + 1
			}
		}
	}
	return append(spans, [2]int{start, len(line)})
}

func (c *lineNameCatalog) prepareRules(content string, fixed, available map[string]bool, rawPolicy ...bool) (string, error) {
	var document yaml.Node
	if err := yaml.Unmarshal([]byte(content), &document); err != nil {
		return "", fmt.Errorf("parse subscription rule fragment: %w", err)
	}
	if len(document.Content) == 1 && document.Content[0].Kind == yaml.SequenceNode {
		for _, rule := range document.Content[0].Content {
			if rule.Kind != yaml.ScalarNode {
				return "", fmt.Errorf("subscription rule must be a string")
			}
			rewritten, err := rewriteRuleTarget(rule.Value, func(name string) (string, error) { return c.resolve(name, fixed, available) }, rawPolicy...)
			if err != nil {
				return "", err
			}
			rule.Value = rewritten
		}
		raw, err := yaml.Marshal(&document)
		return string(raw), err
	}
	lines := strings.Split(content, "\n")
	for index, line := range lines {
		rewritten, err := rewriteRuleTarget(line, func(name string) (string, error) { return c.resolve(name, fixed, available) }, rawPolicy...)
		if err != nil {
			return "", err
		}
		lines[index] = rewritten
	}
	return strings.Join(lines, "\n"), nil
}

func parseMihomoSubscription(body string) (*yaml.Node, error) {
	var document yaml.Node
	if err := yaml.Unmarshal([]byte(body), &document); err != nil {
		return nil, err
	}
	if len(document.Content) != 1 || document.Content[0].Kind != yaml.MappingNode {
		return nil, fmt.Errorf("subscription YAML must be a mapping")
	}
	var validation map[string]any
	if err := document.Decode(&validation); err != nil {
		return nil, err
	}
	return independentYAMLValue(&document), nil
}

func mihomoBuiltinNames() map[string]bool {
	return map[string]bool{"DIRECT": true, "REJECT": true, "REJECT-DROP": true, "PASS": true, "COMPATIBLE": true, "GLOBAL": true}
}

func (c *lineNameCatalog) mihomoNames(document *yaml.Node) (map[string]bool, map[string]bool, error) {
	fixed, available := mihomoBuiltinNames(), map[string]bool{}
	for _, section := range []string{"proxies", "proxy-groups"} {
		definitions, err := referenceSequence(referenceField(document.Content[0], section))
		if err != nil {
			return nil, nil, err
		}
		for _, definition := range definitions {
			name := referenceString(referenceField(definition, "name"))
			if _, owned := c.lines[name]; owned && section == "proxies" {
				available[name] = true
			} else {
				fixed[name] = true
			}
		}
	}
	return fixed, available, nil
}

func (c *lineNameCatalog) singBoxNames(config map[string]any) (map[string]bool, map[string]bool) {
	fixed, available := map[string]bool{}, map[string]bool{}
	for _, section := range []string{"outbounds", "endpoints"} {
		definitions, _ := config[section].([]any)
		for _, value := range definitions {
			definition, _ := value.(map[string]any)
			name, _ := definition["tag"].(string)
			if _, owned := c.lines[name]; owned && section == "outbounds" {
				available[name] = true
			} else if name != "" {
				fixed[name] = true
			}
		}
	}
	return fixed, available
}

func (c *lineNameCatalog) bindMihomo(body string) (string, error) {
	document, err := parseMihomoSubscription(body)
	if err != nil {
		return "", err
	}
	for key, kind := range map[string]yaml.Kind{"proxy-groups": yaml.SequenceNode, "sub-rules": yaml.MappingNode} {
		if field := referenceField(document.Content[0], key); field != nil && field.Tag == "!!null" {
			field.Kind, field.Value = kind, ""
			field.Tag = "!!seq"
			if kind == yaml.MappingNode {
				field.Tag = "!!map"
			}
		}
	}
	fixed, available, err := c.mihomoNames(document)
	if err != nil {
		return "", err
	}
	err = transformMihomoReferences(document.Content[0], func(_ string, name string, _ bool) (string, error) { return c.resolve(name, fixed, available) })
	if err != nil {
		return "", err
	}
	raw, err := yaml.Marshal(document)
	return string(raw), err
}

func (c *lineNameCatalog) bindSingBox(body string) (string, error) {
	config, err := parseSingBoxSubscription(body)
	if err != nil {
		return "", err
	}
	fixed, available := c.singBoxNames(config)
	if err := transformSingBoxReferences(config, func(_ string, name string, _ bool) (string, error) { return c.resolve(name, fixed, available) }); err != nil {
		return "", err
	}
	raw, err := json.MarshalIndent(config, "", "  ")
	return string(raw), err
}

func (c *lineNameCatalog) finalizeMihomo(body string) (string, error) {
	document, err := parseMihomoSubscription(body)
	if err != nil {
		return "", err
	}
	fixed, kept, err := c.mihomoNames(document)
	if err != nil {
		return "", err
	}
	if err := c.validateBases(kept); err != nil {
		return "", err
	}
	names := c.allocate(kept, fixed)
	if err := transformMihomoReferences(document.Content[0], func(_ string, name string, _ bool) (string, error) {
		if final, ok := names[name]; ok {
			return final, nil
		}
		return name, nil
	}); err != nil {
		return "", err
	}
	proxies, _ := referenceSequence(referenceField(document.Content[0], "proxies"))
	for _, proxy := range proxies {
		field := referenceField(proxy, "name")
		if field != nil && names[field.Value] != "" {
			field.Value = names[field.Value]
		}
	}
	c.expandPrivateYAMLValues(document, names)
	raw, err := yaml.Marshal(document)
	if err != nil {
		return "", err
	}
	// Validate names and references again without treating any output as owned.
	return pruneMihomoLines(string(raw), nil)
}

func (c *lineNameCatalog) finalizeSingBox(body string) (string, error) {
	config, err := parseSingBoxSubscription(body)
	if err != nil {
		return "", err
	}
	fixed, kept := c.singBoxNames(config)
	if err := c.validateBases(kept); err != nil {
		return "", err
	}
	names := c.allocate(kept, fixed)
	if err := transformSingBoxReferences(config, func(_ string, name string, _ bool) (string, error) {
		if final, ok := names[name]; ok {
			return final, nil
		}
		return name, nil
	}); err != nil {
		return "", err
	}
	outbounds, _ := config["outbounds"].([]any)
	for _, value := range outbounds {
		outbound, _ := value.(map[string]any)
		tag, _ := outbound["tag"].(string)
		if final, ok := names[tag]; ok {
			outbound["tag"] = final
		}
	}
	c.expandPrivateJSONValues(config, names)
	raw, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return "", err
	}
	return pruneSingBoxLines(string(raw), nil)
}

func (c *lineNameCatalog) validateBases(kept map[string]bool) error {
	for token := range kept {
		if line := c.lines[token]; line.base == "" {
			return fmt.Errorf("empty subscription line name for %d:%s", line.nodeID, line.lineID)
		}
	}
	return nil
}

// A template can copy a generated block into unrelated metadata via YAML
// aliases. Expand only opaque request-local values in those copies, never
// replace user strings that merely resemble public names.
func (c *lineNameCatalog) privateName(value string, names map[string]string) string {
	if final, exists := names[value]; exists {
		return final
	}
	if line, exists := c.lines[value]; exists {
		return line.base
	}
	return value
}

func (c *lineNameCatalog) expandPrivateYAMLValues(node *yaml.Node, names map[string]string) {
	if node.Kind == yaml.ScalarNode {
		node.Value = c.privateName(node.Value, names)
		return
	}
	if node.Kind == yaml.MappingNode {
		kept := make([]*yaml.Node, 0, len(node.Content))
		for i := 0; i < len(node.Content); i += 2 {
			key, value := node.Content[i], node.Content[i+1]
			if key.Value == generatedLineOriginKey && value.Value == c.origin {
				continue
			}
			c.expandPrivateYAMLValues(value, names)
			kept = append(kept, key, value)
		}
		node.Content = kept
		return
	}
	for _, child := range node.Content {
		c.expandPrivateYAMLValues(child, names)
	}
}

func (c *lineNameCatalog) expandPrivateJSONValues(value any, names map[string]string) {
	switch value := value.(type) {
	case map[string]any:
		for key, child := range value {
			if key == generatedLineOriginKey && child == c.origin {
				delete(value, key)
			} else if text, ok := child.(string); ok {
				value[key] = c.privateName(text, names)
			} else {
				c.expandPrivateJSONValues(child, names)
			}
		}
	case []any:
		for i, child := range value {
			if text, ok := child.(string); ok {
				value[i] = c.privateName(text, names)
			} else {
				c.expandPrivateJSONValues(child, names)
			}
		}
	}
}

func emittedRenderItems(items []renderItem, names map[string]bool) []renderItem {
	out := make([]renderItem, 0, len(items))
	for _, item := range items {
		if names[item.name] {
			out = append(out, item)
		}
	}
	return out
}

func parseSingBoxSubscription(body string) (map[string]any, error) {
	var config map[string]any
	decoder := json.NewDecoder(strings.NewReader(body))
	decoder.UseNumber()
	if err := decoder.Decode(&config); err != nil {
		return nil, err
	}
	if config == nil {
		return nil, fmt.Errorf("subscription JSON must be an object")
	}
	return config, nil
}
