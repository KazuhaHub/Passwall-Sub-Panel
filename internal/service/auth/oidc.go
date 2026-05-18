package auth

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"strconv"
	"strings"
	"sync"

	"github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"

	"github.com/KazuhaHub/passwall-sub-panel/internal/config"
	"github.com/KazuhaHub/passwall-sub-panel/internal/pkg/log"
)

// OIDCService is the thin wrapper around go-oidc + oauth2 the panel uses
// for OAuth2/OIDC SSO logins. Parallels SAMLService — Enabled() reports
// whether the service has a valid Provider and Reload() rebuilds it
// after an admin config change.
//
// State and nonce are stored in client-set cookies by the handler layer;
// the service itself stays stateless beyond the immutable Provider /
// oauth2.Config snapshot.
type OIDCService struct {
	cfg *config.OIDCConfig
	mu  sync.RWMutex

	provider *oidc.Provider
	oauth2   *oauth2.Config
	verifier *oidc.IDTokenVerifier
}

func NewOIDC(cfg *config.OIDCConfig) (*OIDCService, error) {
	s := &OIDCService{cfg: cfg}
	if cfg == nil || !cfg.Enabled {
		return s, nil
	}
	if err := s.build(context.Background()); err != nil {
		log.Warn("oidc: initial provider build failed, will retry on reload", "err", err)
	}
	return s, nil
}

// build discovers the IdP and constructs the oauth2.Config + ID-token verifier.
func (s *OIDCService) build(ctx context.Context) error {
	s.mu.RLock()
	cfg := s.cfg
	s.mu.RUnlock()
	if cfg == nil {
		return fmt.Errorf("oidc config not set")
	}
	if cfg.IssuerURL == "" || cfg.ClientID == "" || cfg.RedirectURL == "" {
		return fmt.Errorf("oidc: issuer_url, client_id and redirect_url are required")
	}
	provider, err := oidc.NewProvider(ctx, strings.TrimRight(cfg.IssuerURL, "/"))
	if err != nil {
		return fmt.Errorf("oidc discovery: %w", err)
	}
	scopes := append([]string{oidc.ScopeOpenID}, deDup(cfg.Scopes, oidc.ScopeOpenID)...)
	oauth2Cfg := &oauth2.Config{
		ClientID:     cfg.ClientID,
		ClientSecret: cfg.ClientSecret,
		Endpoint:     provider.Endpoint(),
		RedirectURL:  cfg.RedirectURL,
		Scopes:       scopes,
	}
	verifier := provider.Verifier(&oidc.Config{ClientID: cfg.ClientID})

	s.mu.Lock()
	defer s.mu.Unlock()
	s.provider = provider
	s.oauth2 = oauth2Cfg
	s.verifier = verifier
	return nil
}

func (s *OIDCService) Enabled() bool {
	if s == nil {
		return false
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.cfg == nil || !s.cfg.Enabled {
		return false
	}
	return s.provider != nil && s.oauth2 != nil
}

func (s *OIDCService) Config() *config.OIDCConfig {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.cfg
}

// Reload swaps in a new OIDC config and rebuilds the live provider.
func (s *OIDCService) Reload(ctx context.Context, cfg *config.OIDCConfig) error {
	s.mu.Lock()
	s.cfg = cfg
	if cfg == nil || !cfg.Enabled {
		s.provider = nil
		s.oauth2 = nil
		s.verifier = nil
		s.mu.Unlock()
		return nil
	}
	s.mu.Unlock()
	return s.build(ctx)
}

// AuthCodeURL returns the IdP authorize URL with a generated state.
// The caller persists `state` (and `nonce` if used) in a short-lived
// HttpOnly cookie to verify on the callback.
func (s *OIDCService) AuthCodeURL(state, nonce string) (string, error) {
	s.mu.RLock()
	cfg := s.oauth2
	s.mu.RUnlock()
	if cfg == nil {
		return "", fmt.Errorf("oidc not initialised")
	}
	opts := []oauth2.AuthCodeOption{}
	if nonce != "" {
		opts = append(opts, oidc.Nonce(nonce))
	}
	return cfg.AuthCodeURL(state, opts...), nil
}

// OIDCAssertion mirrors SAMLAssertion: the subset of claims downstream
// user-store code cares about.
type OIDCAssertion struct {
	Subject     string
	Username    string
	Email       string
	DisplayName string
	Groups      []string
	// Attributes carries every ID-token claim flattened to []string so
	// role-rule matching can target any claim, not just the four
	// well-known fields above. Mirrors SAMLAssertion.Attributes.
	// Numbers, bools, and other scalars are stringified; arrays of
	// scalars become multiple values; nested objects are skipped (no
	// sensible string projection).
	Attributes map[string][]string
}

// Exchange swaps the authorization code for tokens, verifies the ID token,
// and extracts the configured attribute claims.
func (s *OIDCService) Exchange(ctx context.Context, code, expectedNonce string) (*OIDCAssertion, error) {
	s.mu.RLock()
	oauthCfg := s.oauth2
	verifier := s.verifier
	cfg := s.cfg
	s.mu.RUnlock()
	if oauthCfg == nil || verifier == nil || cfg == nil {
		return nil, fmt.Errorf("oidc not initialised")
	}
	tok, err := oauthCfg.Exchange(ctx, code)
	if err != nil {
		return nil, fmt.Errorf("oauth2 exchange: %w", err)
	}
	rawID, ok := tok.Extra("id_token").(string)
	if !ok || rawID == "" {
		return nil, fmt.Errorf("token response missing id_token")
	}
	idTok, err := verifier.Verify(ctx, rawID)
	if err != nil {
		return nil, fmt.Errorf("verify id_token: %w", err)
	}
	// Nonce is hard-required: the Login handler always sets a nonce
	// cookie, so an empty expectedNonce at this point means the cookie
	// vanished between auth-redirect and callback (cleared by user,
	// jar pressure, cross-tab race, …). Treating that as a pass would
	// silently weaken the OIDC replay protection that the cookie is
	// there for. Fail closed instead.
	if expectedNonce == "" {
		return nil, fmt.Errorf("oidc nonce cookie missing — cannot validate id_token replay protection")
	}
	if idTok.Nonce != expectedNonce {
		return nil, fmt.Errorf("nonce mismatch")
	}

	var claims map[string]any
	if err := idTok.Claims(&claims); err != nil {
		return nil, fmt.Errorf("decode id_token claims: %w", err)
	}
	out := &OIDCAssertion{Subject: idTok.Subject, Attributes: flattenClaims(claims)}
	out.Username = claimString(claims, cfg.AttributeMapping.Username)
	out.Email = claimString(claims, cfg.AttributeMapping.Email)
	out.DisplayName = claimString(claims, cfg.AttributeMapping.DisplayName)
	out.Groups = claimStringSlice(claims, cfg.AttributeMapping.Groups)
	// Per-login INFO: short audit-style line. Mirrors saml.go's slim
	// format so log volume is comparable across protocols.
	log.Info("oidc: id_token verified",
		"sub", out.Subject,
		"username", out.Username,
		"email", out.Email,
		"display_name", out.DisplayName,
		"groups_count", len(out.Groups),
		"role_rules", len(cfg.RoleRules),
	)
	// Self-diagnostic: if admin configured an attribute mapping but the
	// IdP didn't send a value, dump the claim names so admin can match
	// it up. Only fires on misconfig, not every login.
	if out.DisplayName == "" && strings.TrimSpace(cfg.AttributeMapping.DisplayName) != "" {
		claimNames := make([]string, 0, len(claims))
		for k := range claims {
			claimNames = append(claimNames, k)
		}
		log.Warn("oidc: display_name claim not found in id_token",
			"configured_claim_name", cfg.AttributeMapping.DisplayName,
			"idp_sent_claim_names", claimNames,
		)
	}
	// Identity = the configured username claim, period. No fallback to
	// `sub`, `preferred_username`, or email-local-part — see saml.go for
	// the rationale. If the IdP doesn't emit the claim the admin asked
	// for, fail loudly rather than mint an identity from whatever's
	// handy.
	if out.Username == "" {
		return nil, fmt.Errorf("oidc id_token missing username claim %q", cfg.AttributeMapping.Username)
	}
	return out, nil
}

// RandomState returns a 32-byte base64url string suitable for the OAuth2
// state parameter / OIDC nonce.
func RandomState() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

func claimString(claims map[string]any, key string) string {
	if key == "" {
		return ""
	}
	v, ok := claims[key]
	if !ok {
		return ""
	}
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}

func claimStringSlice(claims map[string]any, key string) []string {
	if key == "" {
		return nil
	}
	v, ok := claims[key]
	if !ok {
		return nil
	}
	switch x := v.(type) {
	case []any:
		out := make([]string, 0, len(x))
		for _, e := range x {
			if s, ok := e.(string); ok {
				out = append(out, s)
			}
		}
		return out
	case []string:
		return x
	case string:
		// Some IdPs encode groups as a single space-separated string.
		return strings.Fields(x)
	}
	return nil
}

// flattenClaims projects an ID-token claims map into the same
// map[string][]string shape SAMLAssertion.Attributes uses, so the
// role-rule matcher can run one implementation against either
// protocol. Strings become single-element slices; arrays of scalars
// become multi-element slices; numbers / booleans are stringified.
// Nested objects (rare in ID tokens) are dropped — they have no
// sensible string projection and would surprise rule authors.
func flattenClaims(claims map[string]any) map[string][]string {
	out := make(map[string][]string, len(claims))
	for k, v := range claims {
		switch x := v.(type) {
		case string:
			if x != "" {
				out[k] = []string{x}
			}
		case []any:
			for _, e := range x {
				if s := scalarToString(e); s != "" {
					out[k] = append(out[k], s)
				}
			}
		case []string:
			for _, e := range x {
				if e != "" {
					out[k] = append(out[k], e)
				}
			}
		default:
			if s := scalarToString(v); s != "" {
				out[k] = []string{s}
			}
		}
	}
	return out
}

func scalarToString(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case bool:
		if x {
			return "true"
		}
		return "false"
	case float64:
		// json.Unmarshal default for numbers — render without scientific
		// notation when integer-valued to keep rule strings predictable.
		if x == float64(int64(x)) {
			return strconv.FormatInt(int64(x), 10)
		}
		return strconv.FormatFloat(x, 'f', -1, 64)
	case int:
		return strconv.Itoa(x)
	case int64:
		return strconv.FormatInt(x, 10)
	}
	return ""
}

func deDup(in []string, skip string) []string {
	seen := map[string]struct{}{skip: {}}
	out := make([]string, 0, len(in))
	for _, s := range in {
		if _, ok := seen[s]; ok {
			continue
		}
		seen[s] = struct{}{}
		out = append(out, s)
	}
	return out
}
