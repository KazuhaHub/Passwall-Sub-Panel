package auth

import (
	"context"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/xml"
	"fmt"
	"net/http"
	"net/url"
	"strings"
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
	s.mu.RLock()
	cfg := s.cfg
	s.mu.RUnlock()
	if cfg == nil {
		return fmt.Errorf("saml config not set")
	}
	certPEM := []byte(cfg.SP.CertPEM)
	keyPEM := []byte(cfg.SP.KeyPEM)
	if len(certPEM) == 0 || len(keyPEM) == 0 {
		return fmt.Errorf("SP cert/key PEM not provided")
	}
	keyPair, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		return fmt.Errorf("parse SP keypair: %w", err)
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
	acsURL, err := url.Parse(cfg.SP.ACSURL)
	if err != nil {
		return fmt.Errorf("parse ACS URL: %w", err)
	}

	idpMeta, err := fetchIDPMetadata(ctx, cfg.IDP.MetadataURL)
	if err != nil {
		return fmt.Errorf("fetch IdP metadata: %w", err)
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	s.sp = &saml.ServiceProvider{
		EntityID:    cfg.SP.EntityID,
		Key:         priv,
		Certificate: leaf,
		AcsURL:      *acsURL,
		IDPMetadata: idpMeta,
		// crewjam/saml defaults AuthnNameIDFormat to "transient" when
		// unset — meaning every AuthnRequest forces a random one-shot
		// NameID. Entra honours the SP-requested format over its admin
		// UI configuration, so the IdP-side "Email address" / source =
		// user.userprincipalname setting is silently ignored and every
		// login gets a fresh hash that doesn't match any user row.
		//
		// UnspecifiedNameIDFormat is special-cased in crewjam: it emits
		// NO Format attribute on the NameIDPolicy element at all, which
		// lets the IdP fall back to its admin-configured default. With
		// Entra that means the NameID format from Attributes & Claims
		// (typically Email Address sourced from user.userprincipalname)
		// is what we get — a stable, human-readable UPN.
		AuthnNameIDFormat: saml.UnspecifiedNameIDFormat,
	}
	return nil
}

// Enabled reports whether SAML SSO is configured AND the SP is initialised
// with a usable IdP metadata document.
func (s *SAMLService) Enabled() bool {
	if s == nil {
		return false
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.cfg == nil || !s.cfg.Enabled {
		return false
	}
	return s.sp != nil && s.sp.IDPMetadata != nil
}

// Config returns the active SAML configuration (read-only). Handlers need
// it for the new-user defaults and default group slug.
func (s *SAMLService) Config() *config.SAMLConfig {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.cfg
}

// Reload swaps in a new SAML configuration and rebuilds the underlying
// crewjam ServiceProvider. Admin-UI saves call this so an SSO config edit
// takes effect without restarting the panel. If the new config disables
// SSO, the SP is torn down.
func (s *SAMLService) Reload(ctx context.Context, cfg *config.SAMLConfig) error {
	s.mu.Lock()
	s.cfg = cfg
	if cfg == nil || !cfg.Enabled {
		s.sp = nil
		s.mu.Unlock()
		return nil
	}
	s.mu.Unlock()
	// buildSP takes its own write lock; release first.
	return s.buildSP(ctx)
}

// StartMetadataRefresh launches a goroutine that re-fetches the IdP
// metadata at the configured interval. Recovers from transient failures
// of the initial fetch by retrying here. Reads s.cfg on every tick so
// Reload() can change the IdP metadata URL or disable SSO entirely.
func (s *SAMLService) StartMetadataRefresh(ctx context.Context, wg ...*sync.WaitGroup) {
	if s == nil {
		return
	}
	if len(wg) > 0 && wg[0] != nil {
		wg[0].Add(1)
	}
	go func() {
		if len(wg) > 0 && wg[0] != nil {
			defer wg[0].Done()
		}
		for {
			s.mu.RLock()
			cfg := s.cfg
			s.mu.RUnlock()
			interval := 24 * time.Hour
			if cfg != nil && cfg.IDP.MetadataRefreshInterval > 0 {
				interval = cfg.IDP.MetadataRefreshInterval
			}
			select {
			case <-ctx.Done():
				return
			case <-time.After(interval):
			}
			s.mu.RLock()
			cfg = s.cfg
			s.mu.RUnlock()
			if cfg == nil || !cfg.Enabled {
				continue
			}
			meta, err := fetchIDPMetadata(ctx, cfg.IDP.MetadataURL)
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
			// Replace s.sp wholesale rather than mutating IDPMetadata in
			// place — readers (ParseACSResponse, BuildAuthnURL) take a
			// snapshot of s.sp under RLock and then operate on it without
			// the lock; mutating a shared pointer field underneath them
			// would be a data race on the underlying library's internals.
			newSP := *s.sp
			newSP.IDPMetadata = meta
			s.sp = &newSP
			s.mu.Unlock()
			log.Info("saml: idp metadata refreshed")
		}
	}()
}

// IDPMetadataSummary is a small read-only view the admin UI uses to verify
// that an IdP metadata URL parses and points at the intended directory.
type IDPMetadataSummary struct {
	EntityID         string     `json:"entity_id"`
	NumSigningCerts  int        `json:"num_signing_certs"`
	SigningCertExpAt *time.Time `json:"signing_cert_expires_at,omitempty"`
}

// FetchIDPMetadataSummary fetches the given URL, parses it as SAML metadata,
// and returns a small summary suitable for an admin UI to confirm the URL
// reaches the intended IdP. Does NOT mutate any stored configuration.
func FetchIDPMetadataSummary(ctx context.Context, metadataURL string) (*IDPMetadataSummary, error) {
	ed, err := fetchIDPMetadata(ctx, metadataURL)
	if err != nil {
		return nil, err
	}
	out := &IDPMetadataSummary{EntityID: ed.EntityID}
	// Walk every IDPSSODescriptor's signing key descriptors. Pick the
	// expiry farthest into the future so a rotation in progress (old +
	// new cert both present) shows as healthy rather than near-expired.
	for _, idp := range ed.IDPSSODescriptors {
		for _, kd := range idp.KeyDescriptors {
			if kd.Use != "" && kd.Use != "signing" {
				continue
			}
			out.NumSigningCerts++
			for _, x509 := range kd.KeyInfo.X509Data.X509Certificates {
				if cert, err := parseX509FromBase64(x509.Data); err == nil {
					if out.SigningCertExpAt == nil || cert.NotAfter.After(*out.SigningCertExpAt) {
						t := cert.NotAfter
						out.SigningCertExpAt = &t
					}
				}
			}
		}
	}
	return out, nil
}

func parseX509FromBase64(raw string) (*x509.Certificate, error) {
	// IdP metadata X.509 blobs are base64 with embedded whitespace; the
	// stdlib base64.StdEncoding rejects whitespace, so strip it first.
	clean := strings.Map(func(r rune) rune {
		if r == ' ' || r == '\n' || r == '\r' || r == '\t' {
			return -1
		}
		return r
	}, raw)
	der, err := base64.StdEncoding.DecodeString(clean)
	if err != nil {
		return nil, err
	}
	return x509.ParseCertificate(der)
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
// The AuthnRequest ID is embedded in the RelayState as "id|returnURL" so it
// survives the SAML round-trip without a cookie — the SAML POST binding is
// cross-site, so SameSite=Lax cookies are never sent with the ACS POST.
func (s *SAMLService) BuildAuthnURL(returnURL string) (string, error) {
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
	// Embed req.ID so ACS can validate InResponseTo without a cookie.
	relayState := req.ID + "|" + returnURL
	u, err := req.Redirect(relayState, s.sp)
	if err != nil {
		return "", fmt.Errorf("build redirect: %w", err)
	}
	return u.String(), nil
}

// SAMLAssertion captures the subset of SAML attributes the user store cares about.
type SAMLAssertion struct {
	// Subject is the IdP-side immutable identifier — always the value of
	// <Subject><NameID>. The user store keys SSO accounts on this (not
	// UPN), so a UPN rename in the IdP doesn't reroute the assertion to a
	// different panel row. For deployments where the admin chose
	// `nameid` as the UPN source, Subject and UPN happen to be the
	// same string; otherwise Subject is the NameID (often a per-SP
	// pairwise hash for Entra, or the email/UPN for Okta/Google).
	Subject     string
	UPN         string
	Email       string
	DisplayName string
	Groups      []string
}

// samlStatusEnvelope is a minimal struct to extract the StatusCode and
// StatusMessage from a raw SAML Response without full validation.
type samlStatusEnvelope struct {
	XMLName xml.Name `xml:"Response"`
	Status  struct {
		StatusCode struct {
			Value      string `xml:"Value,attr"`
			StatusCode struct {
				Value string `xml:"Value,attr"`
			} `xml:"StatusCode"`
		} `xml:"StatusCode"`
		StatusMessage string `xml:"StatusMessage"`
	} `xml:"Status"`
}

// parseSAMLStatus decodes the raw base64 SAMLResponse and extracts the
// top-level and sub StatusCode values plus any StatusMessage. This is used
// only for enriching error messages when ParseResponse fails.
func parseSAMLStatus(rawB64 string) (topCode, subCode, message string) {
	// SAML base64 wraps at 76 chars; strip all whitespace before decoding.
	clean := strings.Map(func(r rune) rune {
		switch r {
		case '\n', '\r', '\t', ' ':
			return -1
		}
		return r
	}, rawB64)
	raw, err := base64.StdEncoding.DecodeString(clean)
	if err != nil {
		return "", "", ""
	}
	var env samlStatusEnvelope
	if err := xml.Unmarshal(raw, &env); err != nil {
		return "", "", ""
	}
	shortCode := func(urn string) string {
		if idx := strings.LastIndex(urn, ":"); idx >= 0 {
			return urn[idx+1:]
		}
		return urn
	}
	return shortCode(env.Status.StatusCode.Value),
		shortCode(env.Status.StatusCode.StatusCode.Value),
		strings.TrimSpace(env.Status.StatusMessage)
}

// ParseACSResponse validates the SAML Response posted by the IdP and
// returns the extracted attributes. possibleRequestIDs should contain the
// AuthnRequest ID stored at login time; pass nil only for IdP-initiated SSO.
func (s *SAMLService) ParseACSResponse(r *http.Request, possibleRequestIDs []string) (*SAMLAssertion, error) {
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
	// crewjam/saml uses base64.StdEncoding which rejects newlines. Entra ID
	// (and some other IdPs) wrap the base64 payload at 76 chars (MIME style).
	// Strip whitespace in-place before handing the request to ParseResponse.
	if raw := r.PostForm.Get("SAMLResponse"); raw != "" {
		r.PostForm.Set("SAMLResponse", strings.Map(func(r rune) rune {
			switch r {
			case '\n', '\r', '\t', ' ':
				return -1
			}
			return r
		}, raw))
	}
	assertion, err := sp.ParseResponse(r, possibleRequestIDs)
	if err != nil {
		// Log the private reason hidden inside InvalidResponseError.
		if ire, ok := err.(*saml.InvalidResponseError); ok {
			log.Warn("saml: parse response failed", "private_err", ire.PrivateErr)
		} else {
			log.Warn("saml: parse response failed", "err", err)
		}

		top, sub, msg := parseSAMLStatus(r.FormValue("SAMLResponse"))
		if top != "" && top != "Success" {
			detail := top
			if sub != "" {
				detail += "/" + sub
			}
			if msg != "" {
				detail += ": " + msg
			}
			return nil, fmt.Errorf("IdP rejected authentication (%s)", detail)
		}
		return nil, fmt.Errorf("parse SAML response: %w", err)
	}
	out := &SAMLAssertion{}
	// Subject = NameID, always. This is the panel-side immutable
	// identity key used for SSO account lookup, independent of how
	// the admin chose to source the human-readable UPN below.
	if assertion.Subject != nil && assertion.Subject.NameID != nil {
		out.Subject = assertion.Subject.NameID.Value
	}
	// Subject identifier source is an admin policy decision. Two
	// supported values:
	//   * "nameid" (case-insensitive) — read from <Subject><NameID>.
	//     This is the SAML-spec-canonical location, default for Okta,
	//     Google Workspace, Keycloak, ADFS, OneLogin, etc.
	//   * any other value — treat as an Attribute Name URN and match
	//     it in <AttributeStatement>. Required for Microsoft Entra,
	//     whose default NameID is an opaque pairwise hash; admins on
	//     Entra add a UPN attribute claim and point this field at its
	//     URN.
	// Both paths are EXPLICIT — there is no implicit fallback from one
	// to the other. A misconfiguration fails the login with the exact
	// missing identifier in the error.
	upnFromNameID := strings.EqualFold(strings.TrimSpace(cfg.AttributeMapping.UPN), "nameid")
	if upnFromNameID {
		if assertion.Subject != nil && assertion.Subject.NameID != nil {
			out.UPN = assertion.Subject.NameID.Value
		}
	}
	for _, stmt := range assertion.AttributeStatements {
		for _, attr := range stmt.Attributes {
			for _, v := range attr.Values {
				switch attr.Name {
				case cfg.AttributeMapping.UPN:
					if !upnFromNameID && out.UPN == "" {
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
		if upnFromNameID {
			return nil, fmt.Errorf("SAML response missing <Subject><NameID> — the UPN source is configured as \"nameid\" but the IdP did not send one")
		}
		return nil, fmt.Errorf("SAML response missing UPN claim %q — add the matching attribute on the IdP side (or set the UPN source to \"nameid\" if your IdP carries it in <Subject><NameID>)", cfg.AttributeMapping.UPN)
	}

	// DisplayName missing is fine — empty string flows through to the
	// user record. No suffix-matching fallback even here: an admin who
	// configures a specific URN expects that URN, not "whatever claim
	// happens to end with displayname".
	log.Info("saml: assertion parsed",
		"upn", out.UPN,
		"email", out.Email,
		"display_name", out.DisplayName,
		"groups", out.Groups,
		"group_attr_name", cfg.AttributeMapping.Groups,
		"admin_group_ids", cfg.AdminGroupIDs,
	)
	return out, nil
}

// IsAdmin reports whether the user's group set intersects the configured
// admin group IDs.
func (s *SAMLService) IsAdmin(groups []string) bool {
	if s == nil {
		return false
	}
	s.mu.RLock()
	cfg := s.cfg
	s.mu.RUnlock()
	if cfg == nil {
		return false
	}
	admins := make(map[string]struct{}, len(cfg.AdminGroupIDs))
	for _, g := range cfg.AdminGroupIDs {
		admins[g] = struct{}{}
	}
	for _, g := range groups {
		if _, ok := admins[g]; ok {
			return true
		}
	}
	return false
}
