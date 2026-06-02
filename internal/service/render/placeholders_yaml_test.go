package render

import (
	"testing"

	yaml "gopkg.in/yaml.v3"
)

// TestYamlScalarRoundTrip is the drift guard for yamlScalar/needsQuoting: every
// proxy / proxy-group name, once emitted, must parse back to the EXACT original
// string in both the contexts yamlScalar is used in — a sequence item ("- X")
// and a mapping value ("name: X"). A name like "null", "~", "123", "{x", or one
// with leading/trailing space silently parses as nil/number/flow/trimmed when
// emitted bare, corrupting proxy-group references. Delegating the check to the
// real YAML parser keeps it from drifting from the grammar.
func TestYamlScalarRoundTrip(t *testing.T) {
	cases := []string{
		// ordinary names that must stay readable (ideally unquoted, but must round-trip)
		"Tokyo-01", "US 1", "🇯🇵 Hong Kong 01", "HK-Reality", "node_42",
		// YAML null / bool reserved words (1.2 core)
		"null", "Null", "NULL", "~", "true", "false", "True", "FALSE",
		// numeric-looking
		"123", "0x1F", "1.5", "-7", "010", "1e3", ".inf", ".nan", "+5",
		// whitespace hazards (parser strips → mismatch)
		" leading", "trailing ", " both ", "",
		// flow / indicator leads + interior hazards
		"{flow", "[flow", "}", "]", ",comma", "*anchor", "&anchor", "!tag",
		"#hash", "key: val", "colon:end", "a #b", "with\ttab",
		"%pct", "@at", "back`tick", "|pipe", ">gt", "'quote", "\"dquote",
		"- dash", "? q", ": c",
	}
	for _, s := range cases {
		emitted := yamlScalar(s)

		var seq []any
		if err := yaml.Unmarshal([]byte("- "+emitted), &seq); err != nil {
			t.Errorf("seq %q -> emitted %q -> parse error: %v", s, emitted, err)
		} else if len(seq) != 1 {
			t.Errorf("seq %q -> emitted %q -> %d items, want 1", s, emitted, len(seq))
		} else if got, ok := seq[0].(string); !ok || got != s {
			t.Errorf("seq %q -> emitted %q -> parsed %#v (want string %q)", s, emitted, seq[0], s)
		}

		var m map[string]any
		if err := yaml.Unmarshal([]byte("name: "+emitted), &m); err != nil {
			t.Errorf("map %q -> emitted %q -> parse error: %v", s, emitted, err)
		} else if got, ok := m["name"].(string); !ok || got != s {
			t.Errorf("map %q -> emitted %q -> parsed %#v (want string %q)", s, emitted, m["name"], s)
		}
	}
}

// TestYamlScalarQuotesYaml11Booleans pins the cross-parser hazard: yaml.v3 (1.2)
// reads yes/no/on/off as plain strings, but Clash and other YAML-1.1 parsers
// read them as booleans. yamlScalar must quote them defensively so a node named
// "off" doesn't become the boolean false downstream.
func TestYamlScalarQuotesYaml11Booleans(t *testing.T) {
	for _, s := range []string{"yes", "no", "on", "off", "Yes", "No", "ON", "OFF", "y", "n"} {
		if got := yamlScalar(s); got == s {
			t.Errorf("yamlScalar(%q) = %q, want it quoted (YAML 1.1 boolean)", s, got)
		}
	}
}
