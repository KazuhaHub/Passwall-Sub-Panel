package render

import (
	"encoding/json"
	"os"
	"regexp"
	"strings"
	"testing"

	yaml "gopkg.in/yaml.v3"
)

var (
	placeholderRE = regexp.MustCompile(`\{\{[^}]*\}\}`)
	// a mihomo nameserver-policy key line, e.g.  "geosite:cn,apple-cn":
	mihomoPolicyKeyRE = regexp.MustCompile(`(?m)^\s*"(geosite:[^"]+)":\s*$`)
	// "geosite:private": followed by a `- system` value line
	privateToSystemRE = regexp.MustCompile(`"geosite:private":\s*\n\s*-\s*system\b`)
)

// Private / LAN / mDNS names — geosite:private contains lan, local, localhost,
// *.arpa, internal — MUST resolve via the OS/system resolver in the shipped
// default templates, never a public DoH (which can't resolve internal names).
// Guards the fix for the "internal name resolution breaks" report: routing
// these to a domestic DoH returned NXDOMAIN for .lan / .local / NAS / intranet.
func TestSeedTemplates_PrivateDNSUsesSystemResolver(t *testing.T) {
	// mihomo: the raw template carries {{ … }} placeholders (not valid YAML
	// until rendered), so assert on the text of the placeholder-free DNS block.
	t.Run("mihomo", func(t *testing.T) {
		content := readTemplateContent(t, "../../seed/files/templates/default-mihomo.yaml")
		if !privateToSystemRE.MatchString(content) {
			t.Fatal("mihomo: expected a `\"geosite:private\":` nameserver-policy entry resolving to `- system`")
		}
		for _, m := range mihomoPolicyKeyRE.FindAllStringSubmatch(content, -1) {
			key := m[1]
			if key != "geosite:private" && strings.Contains(key, "private") {
				t.Fatalf("mihomo: `private` is folded into nameserver-policy key %q — that routes internal names to a public DoH", key)
			}
		}
	})

	// sing-box: the {{ … }} placeholders strip cleanly to a string literal, so
	// the DNS skeleton parses as JSON.
	t.Run("sing-box", func(t *testing.T) {
		content := readTemplateContent(t, "../../seed/files/templates/default-sing-box.yaml")
		skeleton := placeholderRE.ReplaceAll([]byte(content), []byte(`"__placeholder__"`))
		var cfg map[string]any
		if err := json.Unmarshal(skeleton, &cfg); err != nil {
			t.Fatalf("sing-box content not valid JSON after placeholder strip: %v", err)
		}
		dns := cfg["dns"].(map[string]any)
		localTags := map[string]bool{}
		for _, s := range dns["servers"].([]any) {
			m := s.(map[string]any)
			if m["type"] == "local" {
				localTags[m["tag"].(string)] = true
			}
		}
		if len(localTags) == 0 {
			t.Fatal("sing-box: dns has no `type: local` (system) server")
		}
		found := false
		for _, r := range dns["rules"].([]any) {
			m := r.(map[string]any)
			sufs := toStrings(m["domain_suffix"])
			if contains(sufs, "lan") && contains(sufs, "local") {
				found = true
				if srv, _ := m["server"].(string); !localTags[srv] {
					t.Fatalf("sing-box: lan/local DNS rule must route to a type:local server, got server=%q", srv)
				}
			}
		}
		if !found {
			t.Fatal("sing-box: no dns rule covering the lan/local suffixes")
		}
	})
}

func readTemplateContent(t *testing.T, path string) string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Content string `yaml:"content"`
	}
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("%s: outer YAML invalid: %v", path, err)
	}
	return doc.Content
}

func toStrings(v any) []string {
	switch x := v.(type) {
	case []any:
		out := make([]string, 0, len(x))
		for _, e := range x {
			if s, ok := e.(string); ok {
				out = append(out, s)
			}
		}
		return out
	case string:
		return []string{x}
	default:
		return nil
	}
}

func contains(s []string, want string) bool {
	for _, e := range s {
		if e == want {
			return true
		}
	}
	return false
}
