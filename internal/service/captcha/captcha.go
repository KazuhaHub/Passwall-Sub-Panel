// Package captcha abstracts the login-form captcha behind one Service with two
// shapes of provider:
//
//   - "image": a self-hosted character captcha (base64Captcha). The panel
//     issues the challenge image server-side and verifies the typed answer
//     against an in-process store. Zero external calls — the CN-safe default
//     for a proxy panel whose admins are often behind the GFW.
//   - token providers ("turnstile" / "recaptcha" / "hcaptcha"): rendered
//     client-side by the provider's JS widget (with the public site key); the
//     panel only verifies the returned token server-side against the provider's
//     siteverify endpoint using the secret key.
//
// The active provider and keys come from the live UISettings passed on every
// call, so an admin can switch providers without a restart.
package captcha

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	base64Captcha "github.com/mojocn/base64Captcha"

	"github.com/KazuhaHub/passwall-sub-panel/internal/pkg/log"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

// Provider identifiers. Single source of truth shared with the settings
// defaults and the admin-settings validation.
const (
	ProviderImage     = "image"
	ProviderTurnstile = "turnstile"
	ProviderRecaptcha = "recaptcha"
	ProviderHCaptcha  = "hcaptcha"
)

// IsValidProvider reports whether p names a captcha provider this service can
// actually verify. Single source of truth shared with the admin-settings
// validation so the API can't accept a provider Verify would reject.
func IsValidProvider(p string) bool {
	switch p {
	case ProviderImage, ProviderTurnstile, ProviderRecaptcha, ProviderHCaptcha:
		return true
	default:
		return false
	}
}

// Challenge is an issued image captcha (image provider only).
type Challenge struct {
	ID    string `json:"captcha_id"`
	Image string `json:"image"` // a data:image/...;base64 URL ready for an <img src>
}

// Response carries the client's captcha answer alongside the login request.
// Image providers fill ChallengeID+Answer; token providers fill Token.
type Response struct {
	ChallengeID string
	Answer      string
	Token       string
	RemoteIP    string
	// ExpectedHost, when non-empty, is the hostname the panel is served from
	// (derived from the trusted SubBaseURL via HostOf). Token providers return
	// the hostname the challenge was solved on; verifyToken rejects a mismatch
	// so a token solved on a different site sharing this sitekey can't be
	// replayed here. Empty (no base URL configured) skips the check — no
	// false-positive lockout for zero-config deployments.
	ExpectedHost string
}

// HostOf extracts the bare hostname (no scheme/port) from a base URL like
// "https://panel.example.com:8443/". Returns "" for an empty/unparseable URL,
// which callers pass through to Response.ExpectedHost to mean "don't check".
func HostOf(baseURL string) string {
	b := strings.TrimSpace(baseURL)
	if b == "" {
		return ""
	}
	u, err := url.Parse(b)
	if err != nil {
		return ""
	}
	return strings.ToLower(u.Hostname())
}

// Service issues and verifies captchas.
type Service struct {
	store     base64Captcha.Store
	http      *http.Client
	endpoints map[string]string // provider → siteverify URL (overridable in tests)
}

// NewService builds a Service with an in-process image-captcha store (5-minute
// TTL) and the public siteverify endpoints for the token providers.
func NewService() *Service {
	return &Service{
		store: base64Captcha.NewMemoryStore(base64Captcha.GCLimitNumber, 5*time.Minute),
		http:  &http.Client{Timeout: 10 * time.Second},
		endpoints: map[string]string{
			ProviderTurnstile: "https://challenges.cloudflare.com/turnstile/v0/siteverify",
			ProviderRecaptcha: "https://www.google.com/recaptcha/api/siteverify",
			ProviderHCaptcha:  "https://hcaptcha.com/siteverify",
		},
	}
}

func providerOf(s ports.UISettings) string {
	p := strings.ToLower(strings.TrimSpace(s.CaptchaProvider))
	if p == "" {
		return ProviderImage
	}
	return p
}

// imageCaptcha builds a digit captcha over the shared store. Digits avoid the
// O/0, l/1 ambiguity of alphanumerics, so users mistype them less.
func (s *Service) imageCaptcha() *base64Captcha.Captcha {
	driver := base64Captcha.NewDriverDigit(80, 240, 5, 0.7, 80)
	return base64Captcha.NewCaptcha(driver, s.store)
}

// Issue creates a new challenge. Only the image provider issues server-side;
// token providers return (nil, nil) because their widget is rendered on the
// client with the site key.
func (s *Service) Issue(_ context.Context, set ports.UISettings) (*Challenge, error) {
	if providerOf(set) != ProviderImage {
		return nil, nil
	}
	id, b64s, _, err := s.imageCaptcha().Generate()
	if err != nil {
		return nil, fmt.Errorf("captcha: generate image: %w", err)
	}
	return &Challenge{ID: id, Image: b64s}, nil
}

// Verify checks a captcha response against the configured provider. A (false,
// nil) result is an ordinary failed/empty answer; a non-nil error signals a
// misconfiguration (unknown provider, missing secret) that the caller should
// treat as fail-closed and log.
func (s *Service) Verify(ctx context.Context, set ports.UISettings, r Response) (bool, error) {
	switch provider := providerOf(set); provider {
	case ProviderImage:
		if r.ChallengeID == "" || r.Answer == "" {
			return false, nil
		}
		// clear=true: a captcha is single-use, so a correct answer is consumed.
		return s.store.Verify(r.ChallengeID, r.Answer, true), nil
	case ProviderTurnstile, ProviderRecaptcha, ProviderHCaptcha:
		if r.Token == "" {
			return false, nil
		}
		secret := strings.TrimSpace(set.CaptchaSecretKey)
		if secret == "" {
			return false, fmt.Errorf("captcha: %s secret key not configured", provider)
		}
		return s.verifyToken(ctx, s.endpoints[provider], secret, r.Token, r.RemoteIP, r.ExpectedHost)
	default:
		return false, fmt.Errorf("captcha: unknown provider %q", provider)
	}
}

// verifyToken POSTs the standard siteverify form (shared by Turnstile,
// reCAPTCHA and hCaptcha) and validates the JSON reply: success must be true,
// and — when an expectedHost is configured and the provider returned one — the
// solved hostname must match (rejects cross-site token replay). error-codes are
// logged so a misconfigured secret / domain surfaces in the logs.
func (s *Service) verifyToken(ctx context.Context, endpoint, secret, token, remoteIP, expectedHost string) (bool, error) {
	if endpoint == "" {
		return false, fmt.Errorf("captcha: no siteverify endpoint configured")
	}
	form := url.Values{"secret": {secret}, "response": {token}}
	if remoteIP != "" {
		form.Set("remoteip", remoteIP)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return false, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := s.http.Do(req)
	if err != nil {
		return false, fmt.Errorf("captcha: siteverify: %w", err)
	}
	defer resp.Body.Close()
	var out struct {
		Success    bool     `json:"success"`
		Hostname   string   `json:"hostname"`
		ErrorCodes []string `json:"error-codes"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return false, fmt.Errorf("captcha: decode siteverify: %w", err)
	}
	if !out.Success {
		if len(out.ErrorCodes) > 0 {
			log.Warn("captcha: siteverify rejected", "error_codes", out.ErrorCodes)
		}
		return false, nil
	}
	// Defense-in-depth hostname pinning: opt-in (skipped when no base URL is
	// configured) and lenient (skipped when the provider omits a hostname, e.g.
	// reCAPTCHA with domain verification disabled) so it can't lock out a
	// correctly-configured admin.
	if expectedHost != "" && out.Hostname != "" && !strings.EqualFold(out.Hostname, expectedHost) {
		log.Warn("captcha: hostname mismatch — token rejected", "got", out.Hostname, "want", expectedHost)
		return false, nil
	}
	return true, nil
}
