package render

import (
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"strings"

	"github.com/google/uuid"
	"gopkg.in/yaml.v3"
)

const generatedLineOriginKey = "_psp_generated_line_origin"

// Names identify references, not ownership. A custom template may define its
// own proxy using a catalog name while omitting {{ proxies }} entirely. Mark
// only blocks actually inserted by PSP; the per-render token is stripped before
// returning any config and never persisted or sent to a client.
func markGeneratedLines(proxies []map[string]any, key string, names map[string]bool) string {
	origin := uuid.NewString()
	for _, proxy := range proxies {
		name, _ := proxy[key].(string)
		if names[name] {
			proxy[generatedLineOriginKey] = origin
		}
	}
	return origin
}

// subscriptionReferences is a graph of the *emitted* configuration, not the
// candidate catalog. Definitions alone do not retain generated lines. Explicit
// template definitions and selectable groups are roots; their dependencies and
// routing targets retain generated lines transitively.
type subscriptionReferences struct {
	definitions map[string][]string
	roots       []string
	builtins    map[string]bool
}

func (r *subscriptionReferences) add(name string, dependencies []string, root bool) error {
	if name == "" {
		return fmt.Errorf("subscription reference: definition without name")
	}
	if _, exists := r.definitions[name]; exists || r.builtins[name] {
		return fmt.Errorf("subscription reference: duplicate or reserved name %q", name)
	}
	r.definitions[name] = dependencies
	if root {
		r.roots = append(r.roots, name)
	}
	return nil
}

func (r *subscriptionReferences) used() (map[string]bool, error) {
	used, visiting := map[string]bool{}, map[string]bool{}
	var visit func(string) error
	visit = func(name string) error {
		if name == "" || r.builtins[name] {
			return nil
		}
		if visiting[name] {
			return fmt.Errorf("subscription reference: dependency cycle at %q", name)
		}
		if used[name] {
			return nil
		}
		dependencies, exists := r.definitions[name]
		if !exists {
			return fmt.Errorf("subscription reference: undefined proxy or outbound %q", name)
		}
		visiting[name] = true
		for _, dependency := range dependencies {
			if err := visit(dependency); err != nil {
				return fmt.Errorf("%s -> %w", name, err)
			}
		}
		delete(visiting, name)
		used[name] = true
		return nil
	}
	for _, root := range r.roots {
		if err := visit(root); err != nil {
			return nil, err
		}
	}
	return used, nil
}

func emittedProxyNames(proxies []map[string]any, key string) map[string]bool {
	names := make(map[string]bool, len(proxies))
	for _, proxy := range proxies {
		if name, _ := proxy[key].(string); name != "" {
			names[name] = true
		}
	}
	return names
}

// Keep a node tree for Mihomo so custom template fields, ordering, comments and
// proxy values survive filtering. Aliases are resolved when reading references.
func referenceNode(node *yaml.Node) *yaml.Node {
	for node != nil && node.Kind == yaml.AliasNode {
		node = node.Alias
	}
	return node
}

func referenceField(node *yaml.Node, key string) *yaml.Node {
	node = referenceNode(node)
	if node == nil || node.Kind != yaml.MappingNode {
		return nil
	}
	for index := 0; index < len(node.Content); index += 2 {
		if node.Content[index].Value == key {
			return referenceNode(node.Content[index+1])
		}
	}
	// YAML merge keys are commonly used for shared dialer options.
	for index := 0; index < len(node.Content); index += 2 {
		if node.Content[index].Value != "<<" {
			continue
		}
		merged := referenceNode(node.Content[index+1])
		if merged != nil && merged.Kind == yaml.SequenceNode {
			for _, mapping := range merged.Content {
				if value := referenceField(mapping, key); value != nil {
					return value
				}
			}
		} else if value := referenceField(merged, key); value != nil {
			return value
		}
	}
	return nil
}

func referenceString(node *yaml.Node) string {
	node = referenceNode(node)
	if node == nil || node.Tag == "!!null" {
		return ""
	}
	return node.Value
}

func referenceSequence(node *yaml.Node) ([]*yaml.Node, error) {
	node = referenceNode(node)
	if node == nil || node.Tag == "!!null" {
		return nil, nil
	}
	if node.Kind != yaml.SequenceNode {
		return nil, fmt.Errorf("subscription reference: expected a sequence at line %d", node.Line)
	}
	return node.Content, nil
}

// DNS fragments can select either a proxy or a network interface. Only names
// known to be proxies count as dependencies; RULES/h3/ecs options and interface
// names must not become dangling proxy errors. See Mihomo's DNS extra params.
func mihomoDNSProxyReferences(root *yaml.Node, known map[string]bool) []string {
	dependencies := []string{}
	var collect func(*yaml.Node)
	collect = func(node *yaml.Node) {
		node = referenceNode(node)
		if node == nil {
			return
		}
		if node.Kind == yaml.MappingNode {
			for index := 1; index < len(node.Content); index += 2 {
				collect(node.Content[index])
			}
		} else if node.Kind == yaml.SequenceNode {
			for _, child := range node.Content {
				collect(child)
			}
		} else if _, fragment, found := strings.Cut(node.Value, "#"); found {
			for _, option := range strings.Split(fragment, "&") {
				name, err := url.PathUnescape(option)
				if err == nil && known[name] {
					dependencies = append(dependencies, name)
				}
			}
		}
	}
	dns := referenceField(root, "dns")
	for _, field := range []string{"nameserver", "fallback", "default-nameserver", "proxy-server-nameserver", "direct-nameserver", "nameserver-policy", "proxy-server-nameserver-policy"} {
		collect(referenceField(dns, field))
	}
	return dependencies
}

// A template can explicitly use one of the implicit canonical selectors even
// when overridden rule groups no longer depend on it. Only those two built-ins
// may be synthesized, matching the save/inspection dependency policy.
func mihomoMissingTemplateGroups(body string) ([]string, error) {
	var document yaml.Node
	if err := yaml.Unmarshal([]byte(body), &document); err != nil {
		return nil, err
	}
	if len(document.Content) != 1 {
		return nil, fmt.Errorf("subscription YAML must contain one document")
	}
	root := document.Content[0]
	canonical := map[string]bool{"🚀 节点选择": true, "🎮 UDP控制": true}
	defined := map[string]bool{}
	dependencies := mihomoDNSProxyReferences(root, canonical)
	for _, section := range []string{"proxies", "proxy-groups"} {
		values, err := referenceSequence(referenceField(root, section))
		if err != nil {
			return nil, err
		}
		for _, value := range values {
			defined[referenceString(referenceField(value, "name"))] = true
			dependencies = append(dependencies, referenceString(referenceField(value, "dialer-proxy")))
			members, err := referenceSequence(referenceField(value, "proxies"))
			if err != nil {
				return nil, err
			}
			for _, member := range members {
				dependencies = append(dependencies, referenceString(member))
			}
		}
	}
	for _, section := range []string{"proxy-providers", "rule-providers"} {
		providers := referenceField(root, section)
		if providers != nil {
			for index := 1; index < len(providers.Content); index += 2 {
				provider := providers.Content[index]
				dependencies = append(dependencies, referenceString(referenceField(provider, "proxy")), referenceString(referenceField(referenceField(provider, "override"), "dialer-proxy")))
			}
		}
	}
	rules, err := referenceSequence(referenceField(root, "rules"))
	if err != nil {
		return nil, err
	}
	for _, rule := range rules {
		dependencies = append(dependencies, ruleOutboundTarget(splitRuleFields(referenceString(rule))))
	}
	missing := []string{}
	for _, name := range dependencies {
		if canonical[name] && !defined[name] {
			missing = append(missing, name)
			defined[name] = true
		}
	}
	return missing, nil
}

func pruneMihomoLines(body string, generated map[string]bool, origin ...string) (string, error) {
	var document yaml.Node
	if err := yaml.Unmarshal([]byte(body), &document); err != nil {
		return "", fmt.Errorf("parse subscription YAML: %w", err)
	}
	if len(document.Content) != 1 || document.Content[0].Kind != yaml.MappingNode {
		return "", fmt.Errorf("subscription YAML must be a mapping")
	}
	root := document.Content[0]
	// Decode once to validate duplicate mapping keys, including custom sections.
	var validation map[string]any
	if err := root.Decode(&validation); err != nil {
		return "", fmt.Errorf("parse subscription YAML: %w", err)
	}
	refs := subscriptionReferences{definitions: map[string][]string{}, builtins: map[string]bool{
		"DIRECT": true, "REJECT": true, "REJECT-DROP": true, "PASS": true, "COMPATIBLE": true, "GLOBAL": true,
	}}
	proxies := referenceField(root, "proxies")
	proxyNodes, err := referenceSequence(proxies)
	if err != nil {
		return "", err
	}
	if len(origin) > 0 {
		generated = map[string]bool{}
		for _, proxy := range proxyNodes {
			if referenceString(referenceField(proxy, generatedLineOriginKey)) != origin[0] {
				continue
			}
			generated[referenceString(referenceField(proxy, "name"))] = true
			proxy = referenceNode(proxy)
			for index := 0; index < len(proxy.Content); index += 2 {
				if proxy.Content[index].Value == generatedLineOriginKey {
					proxy.Content = append(proxy.Content[:index], proxy.Content[index+2:]...)
					break
				}
			}
		}
	}
	subRules := referenceField(root, "sub-rules")
	checkSubRule := func(name string) error {
		if name != "" && referenceField(subRules, name) == nil {
			return fmt.Errorf("subscription reference: undefined sub-rule %q", name)
		}
		return nil
	}
	for _, proxy := range proxyNodes {
		name := referenceString(referenceField(proxy, "name"))
		dependencies := []string{referenceString(referenceField(proxy, "dialer-proxy"))}
		if err := checkSubRule(referenceString(referenceField(proxy, "target-sub-rule"))); err != nil {
			return "", err
		}
		if err := refs.add(name, dependencies, !generated[name]); err != nil {
			return "", err
		}
	}
	groups, err := referenceSequence(referenceField(root, "proxy-groups"))
	if err != nil {
		return "", err
	}
	for _, group := range groups {
		members, err := referenceSequence(referenceField(group, "proxies"))
		if err != nil {
			return "", err
		}
		dependencies := []string{}
		for _, member := range members {
			dependencies = append(dependencies, referenceString(member))
		}
		if err := refs.add(referenceString(referenceField(group, "name")), dependencies, true); err != nil {
			return "", err
		}
	}
	collectRules := func(node *yaml.Node) error {
		rules, err := referenceSequence(node)
		if err != nil {
			return err
		}
		for _, rule := range rules {
			if referenceNode(rule).Kind != yaml.ScalarNode {
				return fmt.Errorf("subscription rule must be a string")
			}
			fields := splitRuleFields(referenceString(rule))
			if len(fields) < 2 {
				return fmt.Errorf("subscription reference: invalid rule %q", referenceString(rule))
			}
			if err := checkSubRule(subRuleReference(fields)); err != nil {
				return err
			}
			refs.roots = append(refs.roots, ruleOutboundTarget(fields))
		}
		return nil
	}
	if err := collectRules(referenceField(root, "rules")); err != nil {
		return "", err
	}
	// Emitted sub-rule definitions must remain valid even when not currently
	// reached by main rules (a custom Rematch may enter them later).
	if subRules != nil {
		if subRules.Kind != yaml.MappingNode {
			return "", fmt.Errorf("subscription sub-rules must be a mapping")
		}
		for index := 1; index < len(subRules.Content); index += 2 {
			if err := collectRules(subRules.Content[index]); err != nil {
				return "", err
			}
		}
	}
	for _, section := range []string{"proxy-providers", "rule-providers"} {
		providers := referenceField(root, section)
		if providers != nil {
			for index := 1; index < len(providers.Content); index += 2 {
				provider := providers.Content[index]
				refs.roots = append(refs.roots, referenceString(referenceField(provider, "proxy")), referenceString(referenceField(referenceField(provider, "override"), "dialer-proxy")))
			}
		}
	}
	known := make(map[string]bool, len(refs.definitions))
	for name := range refs.definitions {
		known[name] = true
	}
	refs.roots = append(refs.roots, mihomoDNSProxyReferences(root, known)...)
	used, err := refs.used()
	if err != nil {
		return "", err
	}
	if proxies != nil && proxies.Kind == yaml.SequenceNode {
		kept := make([]*yaml.Node, 0, len(proxyNodes))
		for _, proxy := range proxyNodes {
			name := referenceString(referenceField(proxy, "name"))
			if !generated[name] || used[name] {
				kept = append(kept, proxy)
			}
		}
		proxies.Content = kept
	}
	materializeDetachedYAMLAliases(&document)
	raw, err := yaml.Marshal(&document)
	return string(raw), err
}

// Removing a proxy may also remove an anchor used by an unrelated custom
// template field. Preserve that field's value without retaining a proxy merely
// because its parameters happened to be reused as YAML data.
func materializeDetachedYAMLAliases(document *yaml.Node) {
	retained := map[*yaml.Node]bool{}
	var index func(*yaml.Node)
	index = func(node *yaml.Node) {
		retained[node] = true
		for _, child := range node.Content {
			index(child)
		}
	}
	index(document)
	var clone func(*yaml.Node) *yaml.Node
	clone = func(node *yaml.Node) *yaml.Node {
		node = referenceNode(node)
		copy := *node
		copy.Anchor, copy.Alias = "", nil
		copy.Content = make([]*yaml.Node, len(node.Content))
		for i, child := range node.Content {
			copy.Content[i] = clone(child)
		}
		return &copy
	}
	var walk func(*yaml.Node)
	walk = func(node *yaml.Node) {
		for i, child := range node.Content {
			if child.Kind == yaml.AliasNode && !retained[child.Alias] {
				node.Content[i] = clone(child)
			} else {
				walk(child)
			}
		}
	}
	walk(document)
}

func pruneSingBoxLines(body string, generated map[string]bool, origin ...string) (string, error) {
	var config map[string]any
	decoder := json.NewDecoder(strings.NewReader(body))
	decoder.UseNumber() // Do not round large custom numeric values via float64.
	if err := decoder.Decode(&config); err != nil {
		return "", fmt.Errorf("parse subscription JSON: %w", err)
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return "", fmt.Errorf("subscription JSON must contain exactly one object")
	}
	if config == nil {
		return "", fmt.Errorf("subscription JSON must be an object")
	}
	refs := subscriptionReferences{definitions: map[string][]string{}, builtins: map[string]bool{}}
	outbounds, validOutbounds := config["outbounds"].([]any)
	if value, exists := config["outbounds"]; exists && value != nil && !validOutbounds {
		return "", fmt.Errorf("subscription outbounds must be an array")
	}
	if len(origin) > 0 {
		generated = map[string]bool{}
		for _, value := range outbounds {
			if outbound, ok := value.(map[string]any); ok && outbound[generatedLineOriginKey] == origin[0] {
				name, _ := outbound["tag"].(string)
				generated[name] = true
				delete(outbound, generatedLineOriginKey)
			}
		}
	}
	for _, value := range outbounds {
		outbound, ok := value.(map[string]any)
		if !ok {
			return "", fmt.Errorf("subscription outbound must be an object")
		}
		name, _ := outbound["tag"].(string)
		dependencies := singBoxReferenceFields(outbound)
		if members, ok := outbound["outbounds"].([]any); ok {
			for _, member := range members {
				if tag, ok := member.(string); ok {
					dependencies = append(dependencies, tag)
				}
			}
		}
		if selected, _ := outbound["default"].(string); selected != "" {
			dependencies = append(dependencies, selected)
		}
		if name == "" { // Untagged custom outbounds cannot be selected by name.
			refs.roots = append(refs.roots, dependencies...)
			continue
		}
		if err := refs.add(name, dependencies, !generated[name]); err != nil {
			return "", err
		}
	}
	if endpoints, ok := config["endpoints"].([]any); ok {
		for _, value := range endpoints {
			endpoint, ok := value.(map[string]any)
			if !ok {
				return "", fmt.Errorf("subscription endpoint must be an object")
			}
			name, _ := endpoint["tag"].(string)
			dependencies := singBoxReferenceFields(endpoint)
			if name == "" {
				refs.roots = append(refs.roots, dependencies...)
				continue
			}
			if err := refs.add(name, dependencies, true); err != nil {
				return "", err
			}
		}
	}
	// Only fields with outbound semantics are inspected. In particular DNS
	// server tags and rule_set tags are not outbound references.
	for _, section := range []string{"route", "dns", "inbounds", "certificate", "ntp", "experimental"} {
		refs.roots = append(refs.roots, singBoxReferenceFields(config[section])...)
	}
	final := ""
	if route, ok := config["route"].(map[string]any); ok {
		final, _ = route["final"].(string)
	}
	if final == "" && len(outbounds) > 0 {
		// sing-box uses the first outbound when route.final is unspecified.
		first := outbounds[0].(map[string]any)
		final, _ = first["tag"].(string)
	}
	refs.roots = append(refs.roots, final)
	used, err := refs.used()
	if err != nil {
		return "", err
	}
	kept := make([]any, 0, len(outbounds))
	for _, value := range outbounds {
		outbound := value.(map[string]any)
		name, _ := outbound["tag"].(string)
		if !generated[name] || used[name] {
			kept = append(kept, value)
		}
	}
	if _, exists := config["outbounds"]; exists {
		config["outbounds"] = kept
	}
	raw, err := json.MarshalIndent(config, "", "  ")
	return string(raw), err
}

func singBoxReferenceFields(value any) []string {
	refs := []string{}
	switch value := value.(type) {
	case map[string]any:
		for key, child := range value {
			switch key {
			case "outbound", "detour", "download_detour", "external_ui_download_detour":
				if tag, ok := child.(string); ok {
					refs = append(refs, tag)
				}
			default:
				refs = append(refs, singBoxReferenceFields(child)...)
			}
		}
	case []any:
		for _, child := range value {
			refs = append(refs, singBoxReferenceFields(child)...)
		}
	}
	return refs
}
