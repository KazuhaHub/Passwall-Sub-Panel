package render

import (
	"fmt"
	"regexp"
	"strings"
)

// linePlaceholderRE matches a whole line whose only content is a placeholder
// like "{{ proxies }}", capturing the indentation.
var linePlaceholderRE = regexp.MustCompile(`^(\s*)\{\{\s*([\w]+)\s*\}\}\s*$`)

// inlineNodeRefRE matches a proxy-groups entry referencing a node-set:
// "      - @all" or `      - "@region:TW+tag:reality"`.
var inlineNodeRefRE = regexp.MustCompile(`^(\s+)-\s+"?@([\w:+,\-]+)"?\s*$`)

var inlinePlaceholderRE = regexp.MustCompile(`\{\{\s*([\w]+)\s*\}\}`)

// substituteBlockPlaceholders replaces whole-line {{ tag }} entries with
// the supplied multi-line text, preserving the indentation of the
// placeholder line.
func substituteBlockPlaceholders(body string, blocks map[string]string) string {
	lines := strings.Split(body, "\n")
	out := make([]string, 0, len(lines))
	for _, ln := range lines {
		m := linePlaceholderRE.FindStringSubmatch(ln)
		if m == nil {
			out = append(out, ln)
			continue
		}
		indent, tag := m[1], m[2]
		replacement, ok := blocks[tag]
		if !ok {
			// Leave unknown placeholders intact so YAML stays valid-ish and
			// the operator can spot them.
			out = append(out, ln)
			continue
		}
		for _, rl := range strings.Split(strings.TrimRight(replacement, "\n"), "\n") {
			if rl == "" {
				out = append(out, "")
				continue
			}
			out = append(out, indent+rl)
		}
	}
	return strings.Join(out, "\n")
}

func substituteInlinePlaceholders(body string, values map[string]string) string {
	return inlinePlaceholderRE.ReplaceAllStringFunc(body, func(raw string) string {
		m := inlinePlaceholderRE.FindStringSubmatch(raw)
		if m == nil {
			return raw
		}
		if v, ok := values[m[1]]; ok {
			return v
		}
		return raw
	})
}

// expandNodeRefs walks the body looking for proxy-groups entries that
// reference a node-set (@all, @region:TW, @tag:reality, @region:TW+tag:reality)
// and replaces each with a sequence of `- "<node-name>"` lines that preserve
// the original indentation.
func expandNodeRefs(body string, items []renderItem) string {
	allNames := make([]string, 0, len(items))
	byRegion := map[string][]string{}
	byTag := map[string][]string{}
	for _, it := range items {
		allNames = append(allNames, it.name)
		if it.node == nil {
			continue
		}
		byRegion[it.node.Region] = append(byRegion[it.node.Region], it.name)
		for _, t := range it.node.Tags {
			byTag[t] = append(byTag[t], it.name)
		}
	}

	lines := strings.Split(body, "\n")
	out := make([]string, 0, len(lines))
	for _, ln := range lines {
		m := inlineNodeRefRE.FindStringSubmatch(ln)
		if m == nil {
			out = append(out, ln)
			continue
		}
		indent, ref := m[1], m[2]
		names := resolveNodeRef(ref, allNames, byRegion, byTag)
		if len(names) == 0 {
			// Drop the placeholder line entirely — a Clash proxy-group with
			// zero entries is invalid, so any callers must ensure DIRECT or
			// another fallback exists alongside the reference.
			continue
		}
		for _, name := range names {
			out = append(out, fmt.Sprintf("%s- %s", indent, yamlScalar(name)))
		}
	}
	return strings.Join(out, "\n")
}

// resolveNodeRef expands a reference token. Supported forms:
//
//   - "all"              → every node + separator, in render order
//   - "region:XX"        → nodes with Region == XX, in render order
//   - "tag:YY"           → nodes carrying tag YY, in render order
//   - "region:XX+tag:YY" → AND combination of any number of region:/tag: parts
//
// Unknown forms return an empty slice.
func resolveNodeRef(ref string, all []string, byRegion, byTag map[string][]string) []string {
	if ref == "all" {
		return all
	}
	parts := strings.Split(ref, "+")
	var current map[string]bool
	for _, p := range parts {
		set := map[string]bool{}
		switch {
		case strings.HasPrefix(p, "region:"):
			for _, n := range byRegion[strings.TrimPrefix(p, "region:")] {
				set[n] = true
			}
		case strings.HasPrefix(p, "tag:"):
			for _, n := range byTag[strings.TrimPrefix(p, "tag:")] {
				set[n] = true
			}
		default:
			return nil
		}
		if current == nil {
			current = set
		} else {
			for k := range current {
				if !set[k] {
					delete(current, k)
				}
			}
		}
	}
	out := make([]string, 0, len(current))
	for _, n := range all {
		if current[n] {
			out = append(out, n)
		}
	}
	return out
}

// yamlScalar returns s quoted with double quotes when it contains chars that
// would break the YAML scalar grammar or trip naïve parsers.
func yamlScalar(s string) string {
	if needsQuoting(s) {
		return fmt.Sprintf("%q", s)
	}
	return s
}

func needsQuoting(s string) bool {
	if s == "" {
		return true
	}
	switch s[0] {
	case '-', '?', ':', '*', '&', '!', '%', '@', '`', '#', '|', '>', '\'', '"':
		return true
	}
	for _, c := range s {
		switch c {
		case ':', '#', '\n', '\t', '"':
			return true
		}
	}
	return false
}
