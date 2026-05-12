package auth

import (
	"context"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"encoding/xml"
	"fmt"
	"net/http"
	"net/url"
	"sync"
	"time"

	"github.com/crewjam/saml"

	"github.com/KazuhaHub/passwall-sub-panel/internal/config"
	"github.com/KazuhaHub/passwall-sub-panel/internal/pkg/log"
)

// SAMLService is a thin wrapper around crewjam/saml's ServiceProvider that
// exposes only what the panel's HTTP handlers need: AuthnRequest URL,
// Response parsing, SP metadata, and admin-group resolution.
//
// IdP metadata is fetched at construction time and refreshed periodically
// by StartMetadataRefresh so IdP-side certificate rotations are picked up
// without restarting the panel.
type SAMLService struct {
	cfg *config.SAMLConfig
	mu  sync.RWMutex
	sp  *saml.ServiceProvider
}

// NewSAML constructs the service. If cfg.Enabled is false, the returned
// service is a no-op whose Enabled() reports false and whose other methods
// return errors. If IdP metadata cannot be fetched at construction, the
// service stays disabled until StartMetadataRefresh succeeds.
func NewSAML(cfg *config.SAMLConfig) (*SAMLService, error) {
	s := &SAMLService{cfg: cfg}
	if cfg == nil || !cfg.Enabled {
		return s, nil
	}
	if err := s.buildSP(context.Background()); err != nil {
		log.Warn("saml: initial SP build failed, will retry on metadata refresh", "err", err)
	}
	return s, nil
}

func (s *SAMLService) buildSP(ctx context.Context) error {
	keyPair, err := tls.LoadX509KeyPair(s.cfg.SP.CertPEMPath, s.cfg.SP.KeyPEMPath)
	if err != nil {
		return fmt.Errorf("load SP keypair: %w", err)
	}
	if len(keyPair.Certificate) == 0 {
		return fmt.Errorf("SP cert has no entries")
	}
	leaf, err := x509.ParseCertificate(keyPair.Certificate[0])
	if err != nil {
		return fmt.Errorf("parse SP cert: %w", err)
	}
	priv, ok := keyPair.PrivateKey.(*rsa.PrivateKey)
	if !ok {
		return fmt.Errorf("SP private key must be RSA")
	}
	acsURL, err := url.Parse(s.cfg.SP.ACSURL)
	if err != nil {
		return fmt.Errorf("parse ACS URL: %w", err)
	}

	idpMeta, err := fetchIDPMetadata(ctx, s.cfg.IDP.MetadataURL)
	if err != nil {
		return fmt.Errorf("fetch IdP metadata: %w", err)
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	s.sp = &saml.ServiceProvider{
		EntityID:    s.cfg.SP.EntityID,
		Key:         priv,
		Certificate: leaf,
		AcsURL:      *acsURL,
		IDPMetadata: idpMeta,
	}
	return nil
}

// Enabled reports whether SAML SSO is configured AND the SP is initialised
// with a usable IdP metadata document.
func (s *SAMLService) Enabled() bool {
	if s == nil || s.cfg == nil || !s.cfg.Enabled {
		return false
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.sp != nil && s.sp.IDPMetadata != nil
}

// Config returns the active SAML configuration (read-only). Handlers need
// it for the new-user defaults and default group slug.
func (s *SAMLService) Config() *config.SAMLConfig {
	return s.cfg
}

// StartMetadataRefresh launches a goroutine that re-fetches the IdP
// metadata at the configured interval. Recovers from transient failures
// of the initial fetch by retrying here.
func (s *SAMLService) StartMetadataRefresh(ctx context.Context) {
	if s == nil || s.cfg == nil || !s.cfg.Enabled {
		return
	}
	interval := s.cfg.IDP.MetadataRefreshInterval
	if interval <= 0 {
		interval = 24 * time.Hour
	}
	go func() {
		t := time.NewTicker(interval)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				meta, err := fetchIDPMetadata(ctx, s.cfg.IDP.MetadataURL)
				if err != nil {
					log.Warn("saml: idp metadata refresh failed", "err", err)
					continue
				}
				s.mu.Lock()
				if s.sp == nil {
					// Initial build hadn't succeeded yet — do it now.
					s.mu.Unlock()
					if err := s.buildSP(ctx); err != nil {
						log.Warn("saml: deferred SP build failed", "err", err)
					}
					continue
				}
				s.sp.IDPMetadata = meta
				s.mu.Unlock()
				log.Info("saml: idp metadata refreshed")
			}
		}
	}()
}

func fetchIDPMetadata(ctx context.Context, metaURL string) (*saml.EntityDescriptor, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, metaURL, nil)
	if err != nil {
		return nil, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected status %s fetching idp metadata", resp.Status)
	}
	var ed saml.EntityDescriptor
	if err := xml.NewDecoder(resp.Body).Decode(&ed); err != nil {
		return nil, err
	}
	return &ed, nil
}

// SPMetadataXML returns the SP metadata XML that the IdP admin imports.
func (s *SAMLService) SPMetadataXML() ([]byte, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.sp == nil {
		return nil, fmt.Errorf("saml not initialised")
	}
	return xml.MarshalIndent(s.sp.Metadata(), "", "  ")
}

// BuildAuthnURL returns the IdP redirect URL for an SP-initiated login.
func (s *SAMLService) BuildAuthnURL(relayState string) (string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.sp == nil {
		return "", fmt.Errorf("saml not initialised")
	}
	idpURL := s.sp.GetSSOBindingLocation(saml.HTTPRedirectBinding)
	if idpURL == "" {
		return "", fmt.Errorf("idp metadata missing HTTP-Redirect binding")
	}
	req, err := s.sp.MakeAuthenticationRequest(idpURL, saml.HTTPRedirectBinding, saml.HTTPPostBinding)
	if err != nil {
		return "", fmt.Errorf("make authn request: %w", err)
	}
	u, err := req.Redirect(relayState, s.sp)
	if err != nil {
		return "", fmt.Errorf("build redirect: %w", err)
	}
	return u.String(), nil
}

// SAMLAssertion captures the subset of SAML attributes the user store cares about.
type SAMLAssertion struct {
	UPN         string
	Email       string
	DisplayName string
	Groups      []string
}

// ParseACSResponse validates the SAML Response posted by the IdP and
// returns the extracted attributes. Stateless mode: AuthnRequest IDs are
// not pinned to a session cookie, so replay protection relies on the IdP's
// nonce + the SP's timestamp checks.
func (s *SAMLService) ParseACSResponse(r *http.Request) (*SAMLAssertion, error) {
	s.mu.RLock()
	sp := s.sp
	cfg := s.cfg
	s.mu.RUnlock()
	if sp == nil {
		return nil, fmt.Errorf("saml not initialised")
	}
	if err := r.ParseForm(); err != nil {
		return nil, err
	}
	assertion, err := sp.ParseResponse(r, nil)
	if err != nil {
		return nil, fmt.Errorf("parse SAML response: %w", err)
	}
	out := &SAMLAssertion{}
	for _, stmt := range assertion.AttributeStatements {
		for _, attr := range stmt.Attributes {
			for _, v := range attr.Values {
				switch attr.Name {
				case cfg.AttributeMapping.UPN:
					if out.UPN == "" {
						out.UPN = v.Value
					}
				case cfg.AttributeMapping.Email:
					if out.Email == "" {
						out.Email = v.Value
					}
				case cfg.AttributeMapping.DisplayName:
					if out.DisplayName == "" {
						out.DisplayName = v.Value
					}
				case cfg.AttributeMapping.Groups:
					out.Groups = append(out.Groups, v.Value)
				}
			}
		}
	}
	if out.UPN == "" {
		// Fall back to NameID — some IdPs put UPN there instead of as a claim.
		if assertion.Subject != nil && assertion.Subject.NameID != nil {
			out.UPN = assertion.Subject.NameID.Value
		}
	}
	if out.UPN == "" {
		return nil, fmt.Errorf("SAML response missing UPN/NameID")
	}
	return out, nil
}

// IsAdmin reports whether the user's group set intersects the configured
// admin group IDs.
func (s *SAMLService) IsAdmin(groups []string) bool {
	if s == nil || s.cfg == nil {
		return false
	}
	admins := make(map[string]struct{}, len(s.cfg.AdminGroupIDs))
	for _, g := range s.cfg.AdminGroupIDs {
		admins[g] = struct{}{}
	}
	for _, g := range groups {
		if _, ok := admins[g]; ok {
			return true
		}
	}
	return false
}
