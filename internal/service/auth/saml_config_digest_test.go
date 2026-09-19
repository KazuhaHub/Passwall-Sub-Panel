package auth

import (
	"reflect"
	"testing"

	"github.com/KazuhaHub/passwall-sub-panel/internal/config"
)

func digestBaseConfig() *config.SAMLConfig {
	return &config.SAMLConfig{
		Enabled: true,
		Mode:    "manual",
		SP: config.SPConf{
			EntityID: "https://panel.example.com/saml/metadata",
			ACSURL:   "https://panel.example.com/api/auth/saml/acs",
			CertPEM:  "cert-pem",
			KeyPEM:   "key-pem",
		},
		IDP: config.IDPConf{
			MetadataURL:             "https://idp.example.com/saml/metadata",
			MetadataRefreshInterval: 24 * 60 * 60 * 1e9,
		},
		AttributeMapping: config.SAMLAttributeMap{
			UPN: "upn", Email: "email", DisplayName: "displayName", Groups: "groups",
		},
		RoleRules: []config.SSORoleRule{
			{Attribute: "groups", Value: "admins", Role: "admin", Keep: true},
			{Attribute: "groups", Value: "ops", Role: "operator", Keep: false},
		},
		GroupRules: []config.SSOGroupRule{
			{Attribute: "groups", Value: "eng", Group: "engineering", Keep: true},
		},
		DefaultGroupSlug: "default",
		AllowAutoCreate:  false,
		NewUserDefaults: config.SAMLNewUserDefaults{
			TrafficLimitBytes: 1024, ExpireDays: 30, TrafficResetPeriod: "monthly",
		},
	}
}

func TestSAMLConfigDigest_IsDeterministic(t *testing.T) {
	a := SAMLConfigDigest(digestBaseConfig())
	b := SAMLConfigDigest(digestBaseConfig())
	if a != b {
		t.Fatalf("two identical configs produced different digests: %q vs %q", a, b)
	}
	if len(a) != 64 {
		t.Fatalf("digest is not a 64-character SHA-256 hex string: %q", a)
	}
}

// Rule order is semantics, not presentation: the matchers are first-match-wins,
// so reordering two rules can change which role a principal gets.
func TestSAMLConfigDigest_TreatsRuleOrderAsSignificant(t *testing.T) {
	reordered := digestBaseConfig()
	reordered.RoleRules[0], reordered.RoleRules[1] = reordered.RoleRules[1], reordered.RoleRules[0]
	if SAMLConfigDigest(reordered) == SAMLConfigDigest(digestBaseConfig()) {
		t.Fatal("reordering the role rules did not change the digest")
	}
}

// Anything that can change who is trusted or how a principal is mapped must be
// reflected, or a login begun under one set of rules could be completed under
// another.
func TestSAMLConfigDigest_ChangesWithEveryTrustOrMappingField(t *testing.T) {
	base := SAMLConfigDigest(digestBaseConfig())
	cases := map[string]func(*config.SAMLConfig){
		"disabled":               func(c *config.SAMLConfig) { c.Enabled = false },
		"mode":                   func(c *config.SAMLConfig) { c.Mode = "auto" },
		"sp entity id":           func(c *config.SAMLConfig) { c.SP.EntityID = "https://other/saml/metadata" },
		"sp acs url":             func(c *config.SAMLConfig) { c.SP.ACSURL = "https://other/api/auth/saml/acs" },
		"sp certificate":         func(c *config.SAMLConfig) { c.SP.CertPEM = "other-cert" },
		"sp key":                 func(c *config.SAMLConfig) { c.SP.KeyPEM = "other-key" },
		"idp metadata url":       func(c *config.SAMLConfig) { c.IDP.MetadataURL = "https://other/metadata" },
		"upn claim":              func(c *config.SAMLConfig) { c.AttributeMapping.UPN = "mail" },
		"email claim":            func(c *config.SAMLConfig) { c.AttributeMapping.Email = "mail" },
		"display name claim":     func(c *config.SAMLConfig) { c.AttributeMapping.DisplayName = "cn" },
		"groups claim":           func(c *config.SAMLConfig) { c.AttributeMapping.Groups = "memberOf" },
		"role rule value":        func(c *config.SAMLConfig) { c.RoleRules[0].Value = "root" },
		"role rule role":         func(c *config.SAMLConfig) { c.RoleRules[0].Role = "user" },
		"role rule keep":         func(c *config.SAMLConfig) { c.RoleRules[0].Keep = false },
		"role rule attribute":    func(c *config.SAMLConfig) { c.RoleRules[0].Attribute = "mail" },
		"role rule count":        func(c *config.SAMLConfig) { c.RoleRules = c.RoleRules[:1] },
		"group rule attribute":   func(c *config.SAMLConfig) { c.GroupRules[0].Attribute = "mail" },
		"group rule group":       func(c *config.SAMLConfig) { c.GroupRules[0].Group = "ops" },
		"group rule keep":        func(c *config.SAMLConfig) { c.GroupRules[0].Keep = false },
		"group rule count":       func(c *config.SAMLConfig) { c.GroupRules = nil },
		"default group":          func(c *config.SAMLConfig) { c.DefaultGroupSlug = "ops" },
		"allow auto create":      func(c *config.SAMLConfig) { c.AllowAutoCreate = true },
		"new user traffic limit": func(c *config.SAMLConfig) { c.NewUserDefaults.TrafficLimitBytes = 2048 },
		"new user expire days":   func(c *config.SAMLConfig) { c.NewUserDefaults.ExpireDays = 7 },
		"new user reset period":  func(c *config.SAMLConfig) { c.NewUserDefaults.TrafficResetPeriod = "weekly" },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			cfg := digestBaseConfig()
			mutate(cfg)
			if got := SAMLConfigDigest(cfg); got == base {
				t.Fatalf("changing %s did not change the digest", name)
			}
		})
	}
}

// Two things are deliberately outside the digest. Both are recorded here rather
// than left implicit, because each one would otherwise log users out mid-flight
// for a reason that decides nothing about trust.
func TestSAMLConfigDigest_IgnoresFieldsThatDecideNoTrust(t *testing.T) {
	base := SAMLConfigDigest(digestBaseConfig())

	// The polling cadence is an operations knob: the IdP metadata CONTENT is not
	// part of the digest either, precisely so a certificate rotation upstream
	// does not invalidate logins that are already in flight.
	cadence := digestBaseConfig()
	cadence.IDP.MetadataRefreshInterval = 60 * 1e9
	if got := SAMLConfigDigest(cadence); got != base {
		t.Error("changing the metadata refresh interval changed the digest")
	}

	// Rule Notes are admin-facing documentation that the resolver never reads.
	noted := digestBaseConfig()
	noted.RoleRules[0].Note = "Entra global admins"
	noted.GroupRules[0].Note = "contractors"
	if got := SAMLConfigDigest(noted); got != base {
		t.Error("editing a rule's documentation note changed the digest")
	}

	// A nil config must not panic, and must not collide with a real one.
	if SAMLConfigDigest(nil) == base {
		t.Error("a nil config produced the same digest as a populated one")
	}
}

// The exclusions are the one place a new config field could silently escape the
// digest — a mistyped path would make an exclusion a no-op rather than an error.
// This asserts every excluded path still exists in the config schema.
func TestSAMLConfigDigest_ExcludedPathsExist(t *testing.T) {
	schema := map[string]bool{}
	collectDigestSchema(reflect.TypeOf(config.SAMLConfig{}), "", schema)

	if len(samlDigestExcludedPaths) == 0 {
		t.Fatal("no exclusions declared; if none are needed, delete the mechanism rather than the test")
	}
	for path := range samlDigestExcludedPaths {
		if !schema[path] {
			t.Errorf("excluded path %q does not exist in config.SAMLConfig — the exclusion is a no-op, so the field is either back IN the digest or was renamed", path)
		}
	}
}

// collectDigestSchema records every leaf field path of t, using the same dotted,
// index-free notation as the exclusion table.
func collectDigestSchema(t reflect.Type, prefix string, out map[string]bool) {
	for t.Kind() == reflect.Pointer || t.Kind() == reflect.Slice || t.Kind() == reflect.Array {
		t = t.Elem()
	}
	if t.Kind() != reflect.Struct {
		return
	}
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		if f.PkgPath != "" {
			continue
		}
		path := f.Name
		if prefix != "" {
			path = prefix + "." + f.Name
		}
		ft := f.Type
		for ft.Kind() == reflect.Pointer || ft.Kind() == reflect.Slice || ft.Kind() == reflect.Array {
			ft = ft.Elem()
		}
		if ft.Kind() == reflect.Struct {
			collectDigestSchema(ft, path, out)
			continue
		}
		out[path] = true
	}
}
