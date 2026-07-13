package handler

import (
	"context"
	"fmt"
	"net/url"

	"github.com/KazuhaHub/passwall-sub-panel/internal/config"
	"github.com/KazuhaHub/passwall-sub-panel/internal/pkg/panelpath"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
	"github.com/KazuhaHub/passwall-sub-panel/internal/service/auth"
)

type samlConfigReloader interface {
	Reload(context.Context, *config.SAMLConfig) error
}

type oidcConfigReloader interface {
	Reload(context.Context, *config.OIDCConfig) error
}

// PanelPathSSOMigrator keeps persisted and live SSO callback configuration in
// sync when the externally visible panel path changes.
type PanelPathSSOMigrator struct {
	samlRepo ports.SAMLConfigRepo
	oidcRepo ports.OIDCConfigRepo
	saml     samlConfigReloader
	oidc     oidcConfigReloader
}

func NewPanelPathSSOMigrator(
	samlRepo ports.SAMLConfigRepo,
	oidcRepo ports.OIDCConfigRepo,
	saml *auth.SAMLService,
	oidc *auth.OIDCService,
) *PanelPathSSOMigrator {
	return &PanelPathSSOMigrator{samlRepo: samlRepo, oidcRepo: oidcRepo, saml: saml, oidc: oidc}
}

type panelPathSSOMigration struct {
	owner *PanelPathSSOMigrator

	oldSAML *config.SAMLConfig
	newSAML *config.SAMLConfig
	oldOIDC *config.OIDCConfig
	newOIDC *config.OIDCConfig
}

// preparePanelPathChange only rewrites callback URLs whose path still matches
// the old derived panel route. Custom proxy/callback layouts are left intact.
func (m *PanelPathSSOMigrator) preparePanelPathChange(
	ctx context.Context,
	before, after ports.UISettings,
) (*panelPathSSOMigration, error) {
	tx := &panelPathSSOMigration{owner: m}
	if m == nil || before.PanelPath == after.PanelPath {
		return tx, nil
	}

	if m.samlRepo != nil {
		old, err := m.samlRepo.Load(ctx)
		if err != nil {
			return nil, fmt.Errorf("load SAML configuration before panel path change: %w", err)
		}
		updated := cloneSAMLConfig(old)
		changed := false
		if updated.Mode == "auto" {
			entityID := panelpath.PanelURL(after.SubBaseURL, after.PanelPath, "/api/auth/saml/metadata")
			acsURL := panelpath.PanelURL(after.SubBaseURL, after.PanelPath, "/api/auth/saml/acs")
			changed = updated.SP.EntityID != entityID || updated.SP.ACSURL != acsURL
			updated.SP.EntityID = entityID
			updated.SP.ACSURL = acsURL
		} else {
			if rewritten, ok := rewritePanelCallback(updated.SP.EntityID, before.PanelPath, after.PanelPath, "/api/auth/saml/metadata"); ok {
				updated.SP.EntityID = rewritten
				changed = true
			}
			if rewritten, ok := rewritePanelCallback(updated.SP.ACSURL, before.PanelPath, after.PanelPath, "/api/auth/saml/acs"); ok {
				updated.SP.ACSURL = rewritten
				changed = true
			}
		}
		if changed {
			tx.oldSAML = cloneSAMLConfig(old)
			tx.newSAML = updated
		}
	}

	if m.oidcRepo != nil {
		old, err := m.oidcRepo.Load(ctx)
		if err != nil {
			return nil, fmt.Errorf("load OIDC configuration before panel path change: %w", err)
		}
		updated := cloneOIDCConfig(old)
		if rewritten, ok := rewritePanelCallback(updated.RedirectURL, before.PanelPath, after.PanelPath, "/api/auth/oidc/callback"); ok {
			updated.RedirectURL = rewritten
			tx.oldOIDC = cloneOIDCConfig(old)
			tx.newOIDC = updated
		}
	}

	return tx, nil
}

func (tx *panelPathSSOMigration) apply(ctx context.Context) error {
	if tx == nil || tx.owner == nil {
		return nil
	}
	if tx.newSAML != nil {
		if err := tx.owner.samlRepo.Save(ctx, tx.newSAML); err != nil {
			return fmt.Errorf("save migrated SAML configuration: %w", err)
		}
		if tx.owner.saml != nil {
			if err := tx.owner.saml.Reload(ctx, tx.newSAML); err != nil {
				if rollbackErr := tx.rollback(ctx); rollbackErr != nil {
					return fmt.Errorf("reload migrated SAML configuration: %w; SSO rollback failed: %v", err, rollbackErr)
				}
				return fmt.Errorf("reload migrated SAML configuration: %w", err)
			}
		}
	}
	if tx.newOIDC != nil {
		if err := tx.owner.oidcRepo.Save(ctx, tx.newOIDC); err != nil {
			if rollbackErr := tx.rollback(ctx); rollbackErr != nil {
				return fmt.Errorf("save migrated OIDC configuration: %w; SSO rollback failed: %v", err, rollbackErr)
			}
			return fmt.Errorf("save migrated OIDC configuration: %w", err)
		}
		if tx.owner.oidc != nil {
			if err := tx.owner.oidc.Reload(ctx, tx.newOIDC); err != nil {
				if rollbackErr := tx.rollback(ctx); rollbackErr != nil {
					return fmt.Errorf("reload migrated OIDC configuration: %w; SSO rollback failed: %v", err, rollbackErr)
				}
				return fmt.Errorf("reload migrated OIDC configuration: %w", err)
			}
		}
	}
	return nil
}

func (tx *panelPathSSOMigration) rollback(ctx context.Context) error {
	if tx == nil || tx.owner == nil {
		return nil
	}
	var firstErr error
	if tx.oldOIDC != nil {
		if err := tx.owner.oidcRepo.Save(ctx, tx.oldOIDC); err != nil && firstErr == nil {
			firstErr = err
		}
		if tx.owner.oidc != nil {
			if err := tx.owner.oidc.Reload(ctx, tx.oldOIDC); err != nil && firstErr == nil {
				firstErr = err
			}
		}
	}
	if tx.oldSAML != nil {
		if err := tx.owner.samlRepo.Save(ctx, tx.oldSAML); err != nil && firstErr == nil {
			firstErr = err
		}
		if tx.owner.saml != nil {
			if err := tx.owner.saml.Reload(ctx, tx.oldSAML); err != nil && firstErr == nil {
				firstErr = err
			}
		}
	}
	return firstErr
}

func rewritePanelCallback(raw, oldPanelPath, newPanelPath, suffix string) (string, bool) {
	if raw == "" {
		return raw, false
	}
	u, err := url.Parse(raw)
	if err != nil || u.Path != oldPanelPath+suffix {
		return raw, false
	}
	u.Path = newPanelPath + suffix
	u.RawPath = ""
	return u.String(), true
}

func cloneSAMLConfig(in *config.SAMLConfig) *config.SAMLConfig {
	if in == nil {
		return &config.SAMLConfig{}
	}
	out := *in
	out.RoleRules = append([]config.SSORoleRule(nil), in.RoleRules...)
	return &out
}

func cloneOIDCConfig(in *config.OIDCConfig) *config.OIDCConfig {
	if in == nil {
		return &config.OIDCConfig{}
	}
	out := *in
	out.Scopes = append([]string(nil), in.Scopes...)
	out.RoleRules = append([]config.SSORoleRule(nil), in.RoleRules...)
	return &out
}
