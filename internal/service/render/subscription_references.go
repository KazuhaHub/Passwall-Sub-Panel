package render

import (
	"fmt"

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

func (r *subscriptionReferences) collectSlot(owner, name string, optional bool) (string, error) {
	if optional {
		if _, exists := r.definitions[name]; !exists && !r.builtins[name] {
			return name, nil // DNS interface or option, not a proxy dependency.
		}
	}
	if _, exists := r.definitions[owner]; owner != "" && exists {
		r.definitions[owner] = append(r.definitions[owner], name)
	} else {
		r.roots = append(r.roots, name)
	}
	return name, nil
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

// A template may use the two canonical implicit groups without shared rules.
func mihomoMissingTemplateGroups(body string) ([]string, error) {
	document, err := parseMihomoSubscription(body)
	if err != nil {
		return nil, err
	}
	root := document.Content[0]
	defined := map[string]bool{}
	for _, section := range []string{"proxies", "proxy-groups"} {
		values, err := referenceSequence(referenceField(root, section))
		if err != nil {
			return nil, err
		}
		for _, value := range values {
			defined[referenceString(referenceField(value, "name"))] = true
		}
	}
	needed := map[string]bool{}
	err = transformMihomoReferences(root, func(_ string, name string, _ bool) (string, error) {
		needed[name] = true
		return name, nil
	})
	if err != nil {
		return nil, err
	}
	missing := []string{}
	for _, name := range []string{"🚀 节点选择", "🎮 UDP控制"} {
		if needed[name] && !defined[name] {
			missing = append(missing, name)
		}
	}
	return missing, nil
}

func mihomoReferences(document *yaml.Node, generated map[string]bool) (*subscriptionReferences, error) {
	root := document.Content[0]
	refs := &subscriptionReferences{definitions: map[string][]string{}, builtins: mihomoBuiltinNames()}
	subRules := referenceField(root, "sub-rules")
	checkSubRule := func(name string) error {
		if name != "" && referenceField(subRules, name) == nil {
			return fmt.Errorf("subscription reference: undefined sub-rule %q", name)
		}
		return nil
	}
	for _, section := range []string{"proxies", "proxy-groups"} {
		values, err := referenceSequence(referenceField(root, section))
		if err != nil {
			return nil, err
		}
		for _, value := range values {
			name := referenceString(referenceField(value, "name"))
			if section == "proxies" {
				if err := checkSubRule(referenceString(referenceField(value, "target-sub-rule"))); err != nil {
					return nil, err
				}
			}
			if err := refs.add(name, nil, section == "proxy-groups" || !generated[name]); err != nil {
				return nil, err
			}
		}
	}
	checkRules := func(node *yaml.Node) error {
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
		}
		return nil
	}
	if err := checkRules(referenceField(root, "rules")); err != nil {
		return nil, err
	}
	if subRules != nil {
		if subRules.Kind != yaml.MappingNode {
			return nil, fmt.Errorf("subscription sub-rules must be a mapping")
		}
		for index := 1; index < len(subRules.Content); index += 2 {
			if err := checkRules(subRules.Content[index]); err != nil {
				return nil, err
			}
		}
	}
	// This visitor is the only source of outbound dependencies.
	if err := transformMihomoReferences(root, refs.collectSlot); err != nil {
		return nil, err
	}
	return refs, nil
}

func pruneMihomoDocument(document *yaml.Node, generated map[string]bool, origin string) error {
	proxies := referenceField(document.Content[0], "proxies")
	proxyNodes, err := referenceSequence(proxies)
	if err != nil {
		return err
	}
	if origin != "" {
		generated = map[string]bool{}
		for _, proxy := range proxyNodes {
			if referenceString(referenceField(proxy, generatedLineOriginKey)) != origin {
				continue
			}
			generated[referenceString(referenceField(proxy, "name"))] = true
			for index := 0; index < len(proxy.Content); index += 2 {
				if proxy.Content[index].Value == generatedLineOriginKey {
					proxy.Content = append(proxy.Content[:index], proxy.Content[index+2:]...)
					break
				}
			}
		}
	}
	refs, err := mihomoReferences(document, generated)
	if err != nil {
		return err
	}
	used, err := refs.used()
	if err != nil {
		return err
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
	return nil
}

func validateMihomoDocument(document *yaml.Node) error {
	refs, err := mihomoReferences(document, nil)
	if err != nil {
		return err
	}
	_, err = refs.used()
	return err
}

func singBoxReferences(config map[string]any, generated map[string]bool) (*subscriptionReferences, error) {
	refs := &subscriptionReferences{definitions: map[string][]string{}, builtins: map[string]bool{}}
	for _, section := range []string{"outbounds", "endpoints"} {
		values, valid := config[section].([]any)
		if value, exists := config[section]; exists && value != nil && !valid {
			return nil, fmt.Errorf("subscription %s must be an array", section)
		}
		for _, value := range values {
			definition, ok := value.(map[string]any)
			if !ok {
				return nil, fmt.Errorf("subscription %s definition must be an object", section)
			}
			name, _ := definition["tag"].(string)
			if name == "" {
				continue
			}
			if err := refs.add(name, nil, section == "endpoints" || !generated[name]); err != nil {
				return nil, err
			}
		}
	}
	// An omitted final selects the first outbound, even when it is generated.
	final := ""
	if route, ok := config["route"].(map[string]any); ok {
		final, _ = route["final"].(string)
	}
	if values, _ := config["outbounds"].([]any); final == "" && len(values) > 0 {
		first := values[0].(map[string]any)
		name, _ := first["tag"].(string)
		refs.roots = append(refs.roots, name)
	}
	if err := transformSingBoxReferences(config, refs.collectSlot); err != nil {
		return nil, err
	}
	return refs, nil
}

func pruneSingBoxDocument(config map[string]any, generated map[string]bool, origin string) error {
	outbounds, _ := config["outbounds"].([]any)
	if origin != "" {
		generated = map[string]bool{}
		for _, value := range outbounds {
			if outbound, ok := value.(map[string]any); ok && outbound[generatedLineOriginKey] == origin {
				name, _ := outbound["tag"].(string)
				generated[name] = true
				delete(outbound, generatedLineOriginKey)
			}
		}
	}
	refs, err := singBoxReferences(config, generated)
	if err != nil {
		return err
	}
	used, err := refs.used()
	if err != nil {
		return err
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
	return nil
}

func validateSingBoxDocument(config map[string]any) error {
	refs, err := singBoxReferences(config, nil)
	if err != nil {
		return err
	}
	_, err = refs.used()
	return err
}
