package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/KazuhaHub/passwall-sub-panel/internal/config"
	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

// ErrSAMLEntryPoint means the browser reached the panel at an origin that cannot
// host this login. It carries no host names on purpose: the value on one side of
// the comparison comes from the request, and echoing a request-supplied host
// into a failure page is how a probe learns what the panel believes about itself.
var ErrSAMLEntryPoint = errors.New("saml: sign-in must start from the configured HTTPS entry point")

const (
	// Both are 32 bytes. The token is the only thing an attacker replaying a
	// POST needs, and the binding random is what stops a token captured from the
	// IdP round trip being sufficient on its own.
	samlRelayTokenBytes = 32
	samlBindingBytes    = 32

	// SAMLLoginTTL is how long a begun login may take to come back. It is the
	// server-side expiry: a cookie's MaxAge is browser housekeeping only.
	SAMLLoginTTL = 5 * time.Minute

	// samlLoginCookiePrefix namespaces the per-request binding cookie. The token
	// is in the NAME so two tabs can run two logins at once; the value is the
	// binding random.
	samlLoginCookiePrefix = "psp_saml_"

	// samlRelayTokenLen is the length of a base64url-raw 32-byte value.
	samlRelayTokenLen = 43 // ceil(32 * 8 / 6)
)

// SAMLLoginTicket is the client-side material for one login: the opaque
// RelayState token, which round-trips through the IdP, and the binding random,
// which goes to the browser in a cookie. Only their hashes are ever stored.
type SAMLLoginTicket struct {
	Token       string
	Binding     string
	TokenHash   string
	BindingHash string
	CookieName  string
}

// NewSAMLLoginTicket mints fresh material. The two values are independent
// randoms on purpose: a token that leaked through the IdP round trip is still
// not enough to complete the login from a different browser.
func NewSAMLLoginTicket() (SAMLLoginTicket, error) {
	token, err := randomURLToken(samlRelayTokenBytes)
	if err != nil {
		return SAMLLoginTicket{}, err
	}
	binding, err := randomURLToken(samlBindingBytes)
	if err != nil {
		return SAMLLoginTicket{}, err
	}
	return SAMLLoginTicket{
		Token:       token,
		Binding:     binding,
		TokenHash:   SHA256Hex(token),
		BindingHash: SHA256Hex(binding),
		CookieName:  samlLoginCookiePrefix + token,
	}, nil
}

func randomURLToken(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("saml: generating login material: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// SHA256Hex returns the lowercase hex SHA-256 of s. Only hashes of the RelayState
// token and the binding random are ever persisted, so a stolen row is not itself
// replayable as a login.
func SHA256Hex(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

// SAMLLoginCookieName returns the per-request cookie name for a token. The
// caller MUST have validated the token with ValidSAMLRelayToken first.
func SAMLLoginCookieName(token string) string { return samlLoginCookiePrefix + token }

// ValidSAMLRelayToken reports whether s has exactly the shape this package
// mints, and nothing else.
//
// It must be checked BEFORE the token is interpolated into a cookie name.
// RelayState round-trips through the IdP and is attacker-controlled, so a
// crafted value could otherwise inject cookie-header syntax — a ';', a space, or
// a '=' — through the name the panel echoes back.
func ValidSAMLRelayToken(s string) bool {
	if len(s) != samlRelayTokenLen {
		return false
	}
	raw, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil || len(raw) != samlRelayTokenBytes {
		return false
	}
	// The decoder already accepts only this alphabet; the explicit check keeps
	// the cookie-name guarantee from resting on the decoder's current tolerance.
	for i := 0; i < len(s); i++ {
		switch ch := s[i]; {
		case ch >= 'A' && ch <= 'Z', ch >= 'a' && ch <= 'z', ch >= '0' && ch <= '9', ch == '-', ch == '_':
		default:
			return false
		}
	}
	return true
}

// SetSAMLRequestStore installs the durable one-time login-request store. Like
// the replay set it is REQUIRED whenever SAML is enabled: leaving it unset
// refuses every login rather than silently running without request binding.
func (s *SAMLService) SetSAMLRequestStore(r ports.SAMLRequestRepo) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.requestStore = r
}

// BeginLoginOptions describes one SP-initiated login.
type BeginLoginOptions struct {
	// RequestOrigin is the browser's externally visible origin, derived from
	// trusted proxy headers only — never from a raw Host header.
	RequestOrigin string
	// ReturnTo is the in-site path to bounce to after login. The caller has
	// already sanitized it; the value is re-sanitized again before the final
	// redirect.
	ReturnTo string
	Now      time.Time
}

// BeginLoginResult is what the HTTP layer needs to send the browser on its way.
type BeginLoginResult struct {
	RedirectURL string
	CookieName  string
	CookieValue string
}

// BeginLogin starts an SP-initiated login: it checks the entry point, mints the
// request material, records the request server-side, and returns the IdP
// redirect plus the cookie that binds the login to this browser.
//
// The order matters. The record is written BEFORE the browser is sent to the
// IdP, and a write failure aborts the login: a request the panel cannot record is
// one it could never complete, so spending a real authentication at the IdP on
// it would be pure waste (ADR 0036 D2).
func (s *SAMLService) BeginLogin(ctx context.Context, opts BeginLoginOptions) (*BeginLoginResult, error) {
	s.mu.RLock()
	cfg, sp, store := s.cfg, s.sp, s.requestStore
	s.mu.RUnlock()

	if cfg == nil || !cfg.Enabled || sp == nil || sp.IDPMetadata == nil {
		return nil, fmt.Errorf("saml: not enabled")
	}
	if err := samlOriginMismatch(opts.RequestOrigin, cfg.SP.ACSURL); err != nil {
		return nil, err
	}
	if store == nil {
		return nil, fmt.Errorf("%w: no durable SAML login-request store is configured", domain.ErrUnavailable)
	}

	ticket, err := NewSAMLLoginTicket()
	if err != nil {
		return nil, err
	}
	redirectURL, requestID, err := buildAuthnURL(sp, ticket.Token)
	if err != nil {
		return nil, err
	}

	now := opts.Now.UTC()
	rec := &domain.SAMLLoginRequest{
		TokenHash:    ticket.TokenHash,
		BrowserHash:  ticket.BindingHash,
		RequestID:    requestID,
		ConfigDigest: SAMLConfigDigest(cfg),
		ReturnTo:     opts.ReturnTo,
		CreatedAt:    now,
		ExpiresAt:    now.Add(SAMLLoginTTL),
	}
	if err := store.Create(ctx, rec); err != nil {
		return nil, fmt.Errorf("saml: recording the login request: %w", err)
	}
	return &BeginLoginResult{
		RedirectURL: redirectURL,
		CookieName:  ticket.CookieName,
		CookieValue: ticket.Binding,
	}, nil
}

// CompleteLoginOptions carries what the ACS POST told us, and nothing it claimed.
type CompleteLoginOptions struct {
	// RelayState is the IdP-echoed token. Attacker-controlled: its shape is
	// validated before it is used for anything, including a cookie name.
	RelayState string
	// BindingCookie is the value of the psp_saml_<token> cookie, or "" when the
	// browser did not send one.
	BindingCookie string
	Now           time.Time
}

// CompleteLoginResult is a verified assertion plus the context it was verified
// under.
type CompleteLoginResult struct {
	Assertion *SAMLAssertion
	// Config is the snapshot whose digest was checked, so the caller maps
	// attributes with exactly the rules the login began under rather than with
	// whatever a concurrent admin edit has since published.
	Config *config.SAMLConfig
	// ReturnTo comes from the server-side record. The ACS payload never supplies
	// the redirect target.
	ReturnTo string
}

// CompleteLogin consumes the recorded request and verifies the IdP's response.
//
// Two independent controls are in play and neither substitutes for the other:
// consuming the request proves the panel started this login for this browser,
// and the replay set proves the assertion itself has not been used before. The
// request is claimed BEFORE verification, so a failure later in this function
// still spends it — a caller must begin again, which is the intended cost of a
// binding that cannot be replayed.
func (s *SAMLService) CompleteLogin(ctx context.Context, r *http.Request, opts CompleteLoginOptions) (*CompleteLoginResult, error) {
	if !ValidSAMLRelayToken(opts.RelayState) {
		return nil, fmt.Errorf("%w: RelayState is not a token this panel minted", domain.ErrSAMLRequestInvalid)
	}
	if opts.BindingCookie == "" {
		return nil, fmt.Errorf("%w: the browser binding cookie is absent", domain.ErrSAMLRequestInvalid)
	}

	s.mu.RLock()
	cfg, sp, store := s.cfg, s.sp, s.requestStore
	s.mu.RUnlock()

	if cfg == nil || !cfg.Enabled || sp == nil || sp.IDPMetadata == nil {
		return nil, fmt.Errorf("saml: not enabled")
	}
	if store == nil {
		return nil, fmt.Errorf("%w: no durable SAML login-request store is configured", domain.ErrUnavailable)
	}

	rec, err := store.Consume(ctx,
		SHA256Hex(opts.RelayState),
		SHA256Hex(opts.BindingCookie),
		SAMLConfigDigest(cfg),
		opts.Now.UTC())
	if err != nil {
		return nil, err
	}

	// The record's request ID is the ONLY possible request ID: a response may not
	// nominate its own by declaring an InResponseTo.
	assertion, err := s.parseACSResponse(sp, cfg, r, []string{rec.RequestID})
	if err != nil {
		return nil, err
	}
	return &CompleteLoginResult{Assertion: assertion, Config: cfg, ReturnTo: rec.ReturnTo}, nil
}

// samlOriginMismatch reports whether the browser's externally visible origin can
// host this login.
//
// The binding cookie is host-only and SameSite=None, so it accompanies the ACS
// POST only when the browser's current origin and the IdP's Destination are the
// same host. Comparing the configured ACS URL against itself would prove
// nothing: what has to be compared is where the browser actually is. A browser
// that reached the panel by another name (an IP, an internal name, a stale DNS
// entry) would otherwise begin a login whose binding cookie is never sent back,
// and fail at the end with an error that explains nothing.
//
// requestOrigin must come from the trusted-proxy-aware helper, never from a raw
// Host header.
func samlOriginMismatch(requestOrigin, acsURL string) error {
	origin, err := url.Parse(strings.TrimSpace(requestOrigin))
	if err != nil || origin.Host == "" {
		return fmt.Errorf("%w: the browser's origin could not be determined", ErrSAMLEntryPoint)
	}
	acs, err := url.Parse(strings.TrimSpace(acsURL))
	if err != nil || acs.Host == "" {
		return fmt.Errorf("%w: the configured ACS URL is not an absolute URL", ErrSAMLEntryPoint)
	}
	if !strings.EqualFold(origin.Scheme, "https") || !strings.EqualFold(acs.Scheme, "https") {
		return fmt.Errorf("%w: both the panel and the ACS URL must be HTTPS", ErrSAMLEntryPoint)
	}
	if !strings.EqualFold(origin.Host, acs.Host) {
		return ErrSAMLEntryPoint
	}
	return nil
}
