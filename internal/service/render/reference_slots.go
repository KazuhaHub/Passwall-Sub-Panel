package render

import (
	"fmt"
	"net/url"
	"strings"

	"gopkg.in/yaml.v3"
)

// All consumers (binding, graph collection and final naming) visit the same
// semantic slots. optional is reserved for Mihomo DNS proxy/interface choices.
type referenceTransform func(owner, name string, optional bool) (string, error)

func transformMihomoReferences(root *yaml.Node, transform referenceTransform) error {
	field := func(mapping *yaml.Node, key, owner string) error {
		value := referenceField(mapping, key)
		if value == nil || value.Tag == "!!null" {
			return nil
		}
		if value.Kind != yaml.ScalarNode {
			return fmt.Errorf("subscription reference %s must be a string", key)
		}
		name, err := transform(owner, value.Value, false)
		if err == nil {
			value.Value, value.Tag = name, "!!str"
		}
		return err
	}
	for _, section := range []string{"proxies", "proxy-groups"} {
		definitions, err := referenceSequence(referenceField(root, section))
		if err != nil {
			return err
		}
		for _, definition := range definitions {
			owner := referenceString(referenceField(definition, "name"))
			if err := field(definition, "dialer-proxy", owner); err != nil {
				return err
			}
			if section == "proxy-groups" {
				members, err := referenceSequence(referenceField(definition, "proxies"))
				if err != nil {
					return err
				}
				for _, member := range members {
					member = referenceNode(member)
					if member.Kind != yaml.ScalarNode {
						return fmt.Errorf("subscription group member must be a string")
					}
					name, err := transform(owner, member.Value, false)
					if err != nil {
						return err
					}
					member.Value, member.Tag = name, "!!str"
				}
			}
		}
	}
	collectRules := func(node *yaml.Node) error {
		rules, err := referenceSequence(node)
		if err != nil {
			return err
		}
		for _, rule := range rules {
			rule = referenceNode(rule)
			if rule.Kind != yaml.ScalarNode {
				return fmt.Errorf("subscription rule must be a string")
			}
			value, err := rewriteRuleTarget(rule.Value, func(name string) (string, error) { return transform("", name, false) }, true)
			if err != nil {
				return err
			}
			rule.Value = value
		}
		return nil
	}
	if err := collectRules(referenceField(root, "rules")); err != nil {
		return err
	}
	if subRules := referenceField(root, "sub-rules"); subRules != nil && subRules.Kind == yaml.MappingNode {
		for i := 1; i < len(subRules.Content); i += 2 {
			if err := collectRules(subRules.Content[i]); err != nil {
				return err
			}
		}
	}
	for _, section := range []string{"proxy-providers", "rule-providers"} {
		providers := referenceField(root, section)
		if providers != nil && providers.Kind == yaml.MappingNode {
			for i := 1; i < len(providers.Content); i += 2 {
				provider := providers.Content[i]
				if err := field(provider, "proxy", ""); err != nil {
					return err
				}
				if err := field(referenceField(provider, "override"), "dialer-proxy", ""); err != nil {
					return err
				}
			}
		}
	}
	var dns func(*yaml.Node) error
	dns = func(node *yaml.Node) error {
		node = referenceNode(node)
		if node == nil {
			return nil
		}
		switch node.Kind {
		case yaml.MappingNode:
			for i := 1; i < len(node.Content); i += 2 {
				if err := dns(node.Content[i]); err != nil {
					return err
				}
			}
		case yaml.SequenceNode:
			for _, child := range node.Content {
				if err := dns(child); err != nil {
					return err
				}
			}
		case yaml.ScalarNode:
			prefix, fragment, found := strings.Cut(node.Value, "#")
			if !found {
				return nil
			}
			options := strings.Split(fragment, "&")
			for index, option := range options {
				name, err := url.PathUnescape(option)
				if err != nil {
					continue
				}
				if name == "RULES" || strings.Contains(name, "=") {
					continue // Respect-rules and DNS parameters are not proxy names.
				}
				replacement, err := transform("", name, true)
				if err != nil {
					return err
				}
				if replacement != name {
					if strings.ContainsAny(replacement, "&=") {
						return fmt.Errorf("mihomo DNS proxy name %q cannot contain & or =; reference this line through a proxy group", replacement)
					}
					options[index] = strings.ReplaceAll(url.QueryEscape(replacement), "+", "%20")
				}
			}
			node.Value = prefix + "#" + strings.Join(options, "&")
		}
		return nil
	}
	for _, key := range []string{"nameserver", "fallback", "default-nameserver", "proxy-server-nameserver", "direct-nameserver", "nameserver-policy", "proxy-server-nameserver-policy"} {
		if err := dns(referenceField(referenceField(root, "dns"), key)); err != nil {
			return err
		}
	}
	return nil
}

func transformSingBoxReferenceFields(value any, owner string, transform referenceTransform) error {
	switch value := value.(type) {
	case map[string]any:
		for key, child := range value {
			switch key {
			case "outbound", "detour", "download_detour", "external_ui_download_detour":
				if name, ok := child.(string); ok {
					replacement, err := transform(owner, name, false)
					if err != nil {
						return err
					}
					value[key] = replacement
				}
			default:
				if err := transformSingBoxReferenceFields(child, owner, transform); err != nil {
					return err
				}
			}
		}
	case []any:
		for _, child := range value {
			if err := transformSingBoxReferenceFields(child, owner, transform); err != nil {
				return err
			}
		}
	}
	return nil
}

func transformSingBoxReferences(config map[string]any, transform referenceTransform) error {
	for _, section := range []string{"outbounds", "endpoints"} {
		values, _ := config[section].([]any)
		for _, value := range values {
			definition, ok := value.(map[string]any)
			if !ok {
				return fmt.Errorf("subscription %s definition must be an object", section)
			}
			owner, _ := definition["tag"].(string)
			if err := transformSingBoxReferenceFields(definition, owner, transform); err != nil {
				return err
			}
			if section != "outbounds" {
				continue
			}
			if members, ok := definition["outbounds"].([]any); ok {
				for i, member := range members {
					name, ok := member.(string)
					if !ok {
						return fmt.Errorf("subscription outbound member must be a string")
					}
					replacement, err := transform(owner, name, false)
					if err != nil {
						return err
					}
					members[i] = replacement
				}
			}
			if name, ok := definition["default"].(string); ok {
				replacement, err := transform(owner, name, false)
				if err != nil {
					return err
				}
				definition["default"] = replacement
			}
		}
	}
	for _, section := range []string{"route", "dns", "inbounds", "certificate", "ntp", "experimental"} {
		if err := transformSingBoxReferenceFields(config[section], "", transform); err != nil {
			return err
		}
	}
	if route, ok := config["route"].(map[string]any); ok {
		if name, ok := route["final"].(string); ok {
			replacement, err := transform("", name, false)
			if err != nil {
				return err
			}
			route["final"] = replacement
		}
	}
	return nil
}

// Alias expansion is value-preserving and makes semantic slots independent:
// renaming an anchored reference must not rename unrelated metadata using it.
func independentYAMLValue(node *yaml.Node) *yaml.Node {
	node = referenceNode(node)
	copy := *node
	copy.Anchor, copy.Alias = "", nil
	copy.Content = make([]*yaml.Node, len(node.Content))
	for i, child := range node.Content {
		copy.Content[i] = independentYAMLValue(child)
	}
	return &copy
}
