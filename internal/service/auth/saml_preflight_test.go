package auth

import (
	"testing"

	"github.com/KazuhaHub/passwall-sub-panel/internal/config"
	"github.com/KazuhaHub/passwall-sub-panel/internal/pkg/samlkey"
)

const (
	preflightBase   = "https://panel.example.com"
	preflightPanel  = "/panel"
	preflightACSURL = "https://panel.example.com/panel/api/auth/saml/acs"
)

// validPreflightConfig builds a configuration the static checks accept, so each
// case below can vary exactly one thing.
func validPreflightConfig(t *testing.T) *config.SAMLConfig {
	t.Helper()
	cert, key, err := samlkey.GenerateSelfSigned("https://panel.example.com/panel/api/auth/saml/metadata")
	if err != nil {
		t.Fatalf("generate SP keypair: %v", err)
	}
	return &config.SAMLConfig{
		Enabled: true,
		SP: config.SPConf{
			EntityID: "https://panel.example.com/panel/api/auth/saml/metadata",
			ACSURL:   preflightACSURL,
			CertPEM:  cert,
			KeyPEM:   key,
		},
		IDP: config.IDPConf{MetadataURL: "https://idp.example.com/saml/metadata"},
	}
}

func preflightInput(t *testing.T, cfg *config.SAMLConfig) PreflightInput {
	t.Helper()
	return PreflightInput{Config: cfg, PanelPath: preflightPanel, PublicBase: preflightBase}
}

func checkByName(t *testing.T, rep PreflightReport, name string) PreflightCheck {
	t.Helper()
	for _, c := range rep.Checks {
		if c.Name == name {
			return c
		}
	}
	t.Fatalf("no check named %q in %+v", name, rep.Checks)
	return PreflightCheck{}
}

func TestStaticPreflight_AcceptsAValidConfiguration(t *testing.T) {
	rep := StaticPreflight(preflightInput(t, validPreflightConfig(t)))

	if !rep.ConfigurationValid {
		t.Fatalf("a valid configuration was rejected: %+v", rep.Checks)
	}
	for _, c := range rep.Checks {
		if c.Status == PreflightFailed {
			t.Errorf("check %s failed: %s", c.Name, c.Detail)
		}
	}
	if rep.LoginURL != "https://panel.example.com/panel/api/auth/saml/login" {
		t.Errorf("LoginURL = %q", rep.LoginURL)
	}
	if rep.ACSURL != preflightACSURL {
		t.Errorf("ACSURL = %q", rep.ACSURL)
	}
	if rep.SupportedTopology != "single_instance" {
		t.Errorf("SupportedTopology = %q, want the single-instance declaration", rep.SupportedTopology)
	}
}

// The endpoint and the save path must not claim more than they checked. The two
// things they cannot check are stated, not omitted: an operator reading a green
// report needs to know what is still unproven.
func TestStaticPreflight_StatesWhatItCannotCheck(t *testing.T) {
	rep := StaticPreflight(preflightInput(t, validPreflightConfig(t)))

	for _, name := range []string{"database_write", "browser_cookie"} {
		c := checkByName(t, rep, name)
		if c.Status != PreflightNotChecked {
			t.Errorf("check %s = %q, want %q", name, c.Status, PreflightNotChecked)
		}
		if c.Detail == "" {
			t.Errorf("check %s has no detail explaining why it was not checked", name)
		}
	}
	// And a known-missing element must be FAILED, never "not checked": the two
	// statuses mean different things to whoever has to fix it.
	broken := validPreflightConfig(t)
	broken.SP.KeyPEM = ""
	rep = StaticPreflight(preflightInput(t, broken))
	if got := checkByName(t, rep, "sp_keypair").Status; got != PreflightFailed {
		t.Fatalf("a missing key reported %q, want %q", got, PreflightFailed)
	}
}

func TestStaticPreflight_Rejections(t *testing.T) {
	cases := map[string]struct {
		mutate     func(*config.SAMLConfig)
		base, path string
		wantCheck  string
	}{
		"no public base": {
			base: "", path: preflightPanel, wantCheck: "public_base",
		},
		"plain http entry point": {
			base: "http://panel.example.com", path: preflightPanel, wantCheck: "https",
		},
		"acs on another host": {
			mutate: func(c *config.SAMLConfig) { c.SP.ACSURL = "https://other.example.com/panel/api/auth/saml/acs" },
			base:   preflightBase, path: preflightPanel, wantCheck: "same_origin",
		},
		"acs with a fragment": {
			mutate: func(c *config.SAMLConfig) { c.SP.ACSURL = preflightACSURL + "#x" },
			base:   preflightBase, path: preflightPanel, wantCheck: "acs_url_shape",
		},
		"acs with userinfo": {
			mutate: func(c *config.SAMLConfig) { c.SP.ACSURL = "https://user:pw@panel.example.com/panel/api/auth/saml/acs" },
			base:   preflightBase, path: preflightPanel, wantCheck: "acs_url_shape",
		},
		"acs not absolute": {
			mutate: func(c *config.SAMLConfig) { c.SP.ACSURL = "/panel/api/auth/saml/acs" },
			base:   preflightBase, path: preflightPanel, wantCheck: "acs_url_shape",
		},
		"no entity id": {
			mutate: func(c *config.SAMLConfig) { c.SP.EntityID = "" },
			base:   preflightBase, path: preflightPanel, wantCheck: "sp_entity_id",
		},
		"no certificate": {
			mutate: func(c *config.SAMLConfig) { c.SP.CertPEM = "" },
			base:   preflightBase, path: preflightPanel, wantCheck: "sp_keypair",
		},
		"garbage certificate": {
			mutate: func(c *config.SAMLConfig) { c.SP.CertPEM = "not a pem" },
			base:   preflightBase, path: preflightPanel, wantCheck: "sp_keypair",
		},
		"metadata url is plain http": {
			mutate: func(c *config.SAMLConfig) { c.IDP.MetadataURL = "http://idp.example.com/saml/metadata" },
			base:   preflightBase, path: preflightPanel, wantCheck: "idp_metadata_url",
		},
		"metadata url is relative": {
			mutate: func(c *config.SAMLConfig) { c.IDP.MetadataURL = "/saml/metadata" },
			base:   preflightBase, path: preflightPanel, wantCheck: "idp_metadata_url",
		},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			cfg := validPreflightConfig(t)
			if tc.mutate != nil {
				tc.mutate(cfg)
			}
			rep := StaticPreflight(PreflightInput{Config: cfg, PanelPath: tc.path, PublicBase: tc.base})

			if rep.ConfigurationValid {
				t.Fatalf("an invalid configuration was reported valid: %+v", rep.Checks)
			}
			if got := checkByName(t, rep, tc.wantCheck).Status; got != PreflightFailed {
				t.Fatalf("check %s = %q, want %q (checks: %+v)", tc.wantCheck, got, PreflightFailed, rep.Checks)
			}
			if len(rep.FailedChecks()) == 0 {
				t.Fatal("the report lists no failed check names for the save path to quote")
			}
		})
	}
}

// A root-path deployment is the common case, and it must not be mistaken for a
// missing public base.
func TestStaticPreflight_AcceptsARootDeployment(t *testing.T) {
	cfg := validPreflightConfig(t)
	cfg.SP.ACSURL = "https://panel.example.com/api/auth/saml/acs"
	cfg.SP.EntityID = "https://panel.example.com/api/auth/saml/metadata"

	rep := StaticPreflight(PreflightInput{Config: cfg, PanelPath: "", PublicBase: preflightBase})
	if !rep.ConfigurationValid {
		t.Fatalf("a root-path deployment was rejected: %+v", rep.Checks)
	}
	if rep.LoginURL != "https://panel.example.com/api/auth/saml/login" {
		t.Errorf("LoginURL = %q", rep.LoginURL)
	}
}
