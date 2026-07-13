package handler

import (
	"context"
	"testing"

	"github.com/KazuhaHub/passwall-sub-panel/internal/config"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

type panelPathSAMLRepo struct {
	value *config.SAMLConfig
	saves int
}

func (r *panelPathSAMLRepo) Load(context.Context) (*config.SAMLConfig, error) {
	return cloneSAMLConfig(r.value), nil
}
func (r *panelPathSAMLRepo) Save(_ context.Context, value *config.SAMLConfig) error {
	r.value = cloneSAMLConfig(value)
	r.saves++
	return nil
}

type panelPathOIDCRepo struct {
	value *config.OIDCConfig
	saves int
}

func (r *panelPathOIDCRepo) Load(context.Context) (*config.OIDCConfig, error) {
	return cloneOIDCConfig(r.value), nil
}
func (r *panelPathOIDCRepo) Save(_ context.Context, value *config.OIDCConfig) error {
	r.value = cloneOIDCConfig(value)
	r.saves++
	return nil
}

func TestPanelPathSSOMigrationRewritesDerivedCallbacks(t *testing.T) {
	samlRepo := &panelPathSAMLRepo{value: &config.SAMLConfig{
		Mode: "auto",
		SP: config.SPConf{
			EntityID: "https://panel.example/api/auth/saml/metadata",
			ACSURL:   "https://panel.example/api/auth/saml/acs",
		},
	}}
	oidcRepo := &panelPathOIDCRepo{value: &config.OIDCConfig{
		RedirectURL: "https://panel.example/api/auth/oidc/callback",
	}}
	m := &PanelPathSSOMigrator{samlRepo: samlRepo, oidcRepo: oidcRepo}
	tx, err := m.preparePanelPathChange(context.Background(),
		ports.UISettings{SubBaseURL: "https://panel.example"},
		ports.UISettings{SubBaseURL: "https://panel.example", PanelPath: "/mypanel"},
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := tx.apply(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := samlRepo.value.SP.ACSURL; got != "https://panel.example/mypanel/api/auth/saml/acs" {
		t.Fatalf("SAML ACSURL = %q", got)
	}
	if got := oidcRepo.value.RedirectURL; got != "https://panel.example/mypanel/api/auth/oidc/callback" {
		t.Fatalf("OIDC RedirectURL = %q", got)
	}
}

func TestPanelPathSSOMigrationPreservesCustomOIDCCallback(t *testing.T) {
	oidcRepo := &panelPathOIDCRepo{value: &config.OIDCConfig{
		RedirectURL: "https://auth-proxy.example/oidc/finish",
	}}
	m := &PanelPathSSOMigrator{oidcRepo: oidcRepo}
	tx, err := m.preparePanelPathChange(context.Background(),
		ports.UISettings{}, ports.UISettings{PanelPath: "/mypanel"},
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := tx.apply(context.Background()); err != nil {
		t.Fatal(err)
	}
	if oidcRepo.saves != 0 {
		t.Fatalf("custom OIDC callback was unexpectedly saved %d times", oidcRepo.saves)
	}
}
