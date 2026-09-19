package config

import "testing"

// A shallow copy is the quiet failure mode here: the caller believes it holds an
// editable copy, mutates a rule, and changes the configuration the runtime is
// still serving — or, on a rollback path, finds it cannot restore what it thought
// it had saved. GroupRules is called out because a previous hand-rolled clone in
// the handler replicated RoleRules but silently shared this one.
func TestCloneSAMLConfig_DeepCopiesEverySlice(t *testing.T) {
	src := &SAMLConfig{
		AttributeMapping: SAMLAttributeMap{UPN: "upn"},
		RoleRules:        []SSORoleRule{{Attribute: "groups", Value: "admins", Role: "admin", Keep: true}},
		GroupRules:       []SSOGroupRule{{Attribute: "groups", Value: "eng", Group: "engineering", Keep: true}},
	}

	cp := CloneSAMLConfig(src)
	if cp == src {
		t.Fatal("clone returned the same pointer")
	}
	if cp.AttributeMapping != src.AttributeMapping {
		t.Fatal("the clone lost scalar fields")
	}

	cp.RoleRules[0].Value = "changed"
	if src.RoleRules[0].Value == "changed" {
		t.Error("RoleRules shares its backing array with the original")
	}
	cp.GroupRules[0].Group = "changed"
	if src.GroupRules[0].Group == "changed" {
		t.Error("GroupRules shares its backing array with the original")
	}

	// Appending to the copy must not write into the original's capacity either.
	cp.RoleRules = append(cp.RoleRules, SSORoleRule{Value: "extra"})
	if len(src.RoleRules) != 1 {
		t.Errorf("appending to the clone changed the original's length: %d", len(src.RoleRules))
	}
}

// The panel-path migration expects an empty configuration when no row has been
// persisted yet, so nil maps to an empty value rather than to nil.
func TestCloneSAMLConfig_NilBecomesEmpty(t *testing.T) {
	cp := CloneSAMLConfig(nil)
	if cp == nil {
		t.Fatal("clone of nil returned nil")
	}
	if len(cp.RoleRules) != 0 || len(cp.GroupRules) != 0 || cp.Enabled {
		t.Fatalf("clone of nil is not an empty configuration: %+v", cp)
	}
}
