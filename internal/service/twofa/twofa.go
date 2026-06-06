// Package twofa implements optional TOTP (authenticator-app) two-factor auth for
// local accounts. The secret is stored encrypted (at the repo boundary) and
// recovery codes are stored as SHA-256 hashes; this package handles enrollment
// (begin/enable), the login-time check (verify), self-disable, and admin reset.
package twofa

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	"github.com/pquerna/otp/totp"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

const recoveryCodeCount = 10

// Store is the slice of the user repo that 2FA needs.
type Store interface {
	SetTOTP(ctx context.Context, userID int64, secret string, enabled bool, recoveryHashes []string) error
	GetTOTP(ctx context.Context, userID int64) (secret string, enabled bool, recoveryHashes []string, err error)
	// ConsumeRecoveryCode atomically swaps prevHashes→nextHashes (compare-and-swap),
	// returning true only when this call won — so a one-time recovery code can't be
	// double-spent by two concurrent logins.
	ConsumeRecoveryCode(ctx context.Context, userID int64, prevHashes, nextHashes []string) (bool, error)
	ClearTOTP(ctx context.Context, userID int64) error
	GetByID(ctx context.Context, userID int64) (*domain.User, error)
}

type SettingsLoader interface {
	Load(ctx context.Context, defaults ports.UISettings) (ports.UISettings, error)
}

type Deps struct {
	Users    Store
	Settings SettingsLoader
	Now      func() time.Time
	// GenSecret generates a new TOTP secret + otpauth URL (issuer, account).
	// Defaults to pquerna/otp.
	GenSecret func(issuer, account string) (secret, otpauthURL string, err error)
	// Validate checks a 6-digit code against a secret. Defaults to pquerna/otp
	// (which already allows ±1 time-step skew).
	Validate func(code, secret string) bool
	// GenRecovery generates a fresh batch of plaintext recovery codes.
	GenRecovery func() ([]string, error)
}

type Service struct {
	d           Deps
	now         func() time.Time
	genSecret   func(issuer, account string) (string, string, error)
	validate    func(code, secret string) bool
	genRecovery func() ([]string, error)
}

func New(d Deps) *Service {
	s := &Service{d: d, now: d.Now, genSecret: d.GenSecret, validate: d.Validate, genRecovery: d.GenRecovery}
	if s.now == nil {
		s.now = time.Now
	}
	if s.genSecret == nil {
		s.genSecret = defaultGenSecret
	}
	if s.validate == nil {
		s.validate = func(code, secret string) bool { return totp.Validate(strings.TrimSpace(code), secret) }
	}
	if s.genRecovery == nil {
		s.genRecovery = defaultGenRecovery
	}
	return s
}

// Available reports whether the admin has enabled 2FA enrollment panel-wide.
func (s *Service) Available(ctx context.Context) bool {
	set, err := s.d.Settings.Load(ctx, ports.UISettings{})
	return err == nil && set.TOTPEnabled
}

// Begin starts enrollment: it generates a secret, stores it DISABLED (so it
// isn't active until confirmed), and returns the otpauth URL (for the QR) + the
// raw secret (for manual entry). Errors if 2FA is off panel-wide or already on.
func (s *Service) Begin(ctx context.Context, userID int64) (otpauthURL, secret string, err error) {
	if !s.Available(ctx) {
		return "", "", fmt.Errorf("%w: two-factor authentication is not enabled on this panel", domain.ErrForbidden)
	}
	u, err := s.d.Users.GetByID(ctx, userID)
	if err != nil {
		return "", "", err
	}
	if !u.HasLocalPassword() {
		return "", "", fmt.Errorf("%w: account has no local password", domain.ErrValidation)
	}
	if _, enabled, _, gerr := s.d.Users.GetTOTP(ctx, userID); gerr == nil && enabled {
		return "", "", fmt.Errorf("%w: two-factor authentication is already enabled", domain.ErrValidation)
	}
	issuer := "Passwall"
	if set, serr := s.d.Settings.Load(ctx, ports.UISettings{}); serr == nil {
		issuer = set.BrandName()
	}
	account := u.UPN
	secret, otpauthURL, err = s.genSecret(issuer, account)
	if err != nil {
		return "", "", err
	}
	// Store the pending secret (disabled, no recovery codes yet).
	if err := s.d.Users.SetTOTP(ctx, userID, secret, false, nil); err != nil {
		return "", "", err
	}
	return otpauthURL, secret, nil
}

// Enable confirms enrollment: it validates a code against the pending secret,
// marks 2FA enabled, generates one-time recovery codes (stored hashed), and
// returns the plaintext codes to show ONCE.
func (s *Service) Enable(ctx context.Context, userID int64, code string) ([]string, error) {
	if !s.Available(ctx) {
		return nil, fmt.Errorf("%w: two-factor authentication is not enabled on this panel", domain.ErrForbidden)
	}
	secret, enabled, _, err := s.d.Users.GetTOTP(ctx, userID)
	if err != nil {
		return nil, err
	}
	if enabled {
		return nil, fmt.Errorf("%w: two-factor authentication is already enabled", domain.ErrValidation)
	}
	if secret == "" {
		return nil, fmt.Errorf("%w: start enrollment first", domain.ErrValidation)
	}
	if !s.validate(code, secret) {
		return nil, domain.ErrUnauthorized
	}
	plain, err := s.genRecovery()
	if err != nil {
		return nil, err
	}
	hashes := make([]string, len(plain))
	for i, c := range plain {
		hashes[i] = hashRecovery(c)
	}
	if err := s.d.Users.SetTOTP(ctx, userID, secret, true, hashes); err != nil {
		return nil, err
	}
	return plain, nil
}

// Disable turns 2FA off for the user, requiring a valid current code (TOTP or a
// recovery code) as proof of possession.
func (s *Service) Disable(ctx context.Context, userID int64, code string) error {
	secret, enabled, codes, err := s.d.Users.GetTOTP(ctx, userID)
	if err != nil {
		return err
	}
	if !enabled {
		return nil // already off — idempotent
	}
	if !s.checkCode(ctx, userID, code, secret, codes) {
		return domain.ErrUnauthorized
	}
	return s.d.Users.ClearTOTP(ctx, userID)
}

// VerifyLogin checks a code at login time (TOTP or a one-time recovery code,
// which is consumed on success).
func (s *Service) VerifyLogin(ctx context.Context, userID int64, code string) (bool, error) {
	secret, enabled, codes, err := s.d.Users.GetTOTP(ctx, userID)
	if err != nil {
		return false, err
	}
	if !enabled {
		return false, nil
	}
	return s.checkCode(ctx, userID, code, secret, codes), nil
}

// AdminReset clears a user's 2FA unconditionally (break-glass when a user loses
// their authenticator and recovery codes).
func (s *Service) AdminReset(ctx context.Context, userID int64) error {
	return s.d.Users.ClearTOTP(ctx, userID)
}

// RegenerateRecovery rotates a user's recovery codes (self-service step-up). It
// requires proof of possession — a current TOTP code or one of the existing
// recovery codes — to stop a hijacked session from silently minting a fresh set.
// Returns the new plaintext codes to show ONCE. 2FA must already be enabled.
func (s *Service) RegenerateRecovery(ctx context.Context, userID int64, code string) ([]string, error) {
	secret, enabled, codes, err := s.d.Users.GetTOTP(ctx, userID)
	if err != nil {
		return nil, err
	}
	if !enabled {
		return nil, fmt.Errorf("%w: two-factor authentication is not enabled", domain.ErrValidation)
	}
	// matchCode (not checkCode) deliberately does NOT consume the recovery code
	// used as proof: the whole set is about to be replaced anyway.
	if !s.matchCode(code, secret, codes) {
		return nil, domain.ErrUnauthorized
	}
	return s.replaceRecovery(ctx, userID, secret)
}

// AdminRegenerateRecovery rotates a user's recovery codes without proof
// (break-glass, same trust level as admin password reset). Returns the new
// plaintext codes for the admin to relay over a secure channel. 2FA must be on.
func (s *Service) AdminRegenerateRecovery(ctx context.Context, userID int64) ([]string, error) {
	secret, enabled, _, err := s.d.Users.GetTOTP(ctx, userID)
	if err != nil {
		return nil, err
	}
	if !enabled {
		return nil, fmt.Errorf("%w: two-factor authentication is not enabled for this user", domain.ErrValidation)
	}
	return s.replaceRecovery(ctx, userID, secret)
}

// replaceRecovery generates a fresh batch of recovery codes, stores their hashes
// (preserving the secret + enabled state), and returns the plaintext.
func (s *Service) replaceRecovery(ctx context.Context, userID int64, secret string) ([]string, error) {
	plain, err := s.genRecovery()
	if err != nil {
		return nil, err
	}
	hashes := make([]string, len(plain))
	for i, c := range plain {
		hashes[i] = hashRecovery(c)
	}
	if err := s.d.Users.SetTOTP(ctx, userID, secret, true, hashes); err != nil {
		return nil, err
	}
	return plain, nil
}

// matchCode reports whether code is a valid TOTP or matches a stored recovery
// hash, WITHOUT consuming anything. Used for step-up proof where the caller
// replaces the whole recovery set afterwards.
func (s *Service) matchCode(code, secret string, recoveryHashes []string) bool {
	if secret != "" && s.validate(code, secret) {
		return true
	}
	want := hashRecovery(code)
	for _, h := range recoveryHashes {
		if subtle.ConstantTimeCompare([]byte(h), []byte(want)) == 1 {
			return true
		}
	}
	return false
}

// checkCode validates a TOTP code, then falls back to recovery codes (consuming
// the matched one). Returns true on either.
func (s *Service) checkCode(ctx context.Context, userID int64, code, secret string, recoveryHashes []string) bool {
	if secret != "" && s.validate(code, secret) {
		return true
	}
	want := hashRecovery(code)
	for i, h := range recoveryHashes {
		if subtle.ConstantTimeCompare([]byte(h), []byte(want)) == 1 {
			// Consume it atomically: a compare-and-swap from the full list to the
			// remaining set. If we lose the race (another concurrent login redeemed
			// the same code first) or the write errors, refuse — never let one
			// single-use code mint two sessions.
			remaining := append(append([]string{}, recoveryHashes[:i]...), recoveryHashes[i+1:]...)
			consumed, err := s.d.Users.ConsumeRecoveryCode(ctx, userID, recoveryHashes, remaining)
			if err != nil || !consumed {
				return false
			}
			return true
		}
	}
	return false
}

// hashRecovery normalizes a recovery code (uppercase, alphanumerics only) and
// returns its hex SHA-256, so "aaaa-bbbb" and "AAAABBBB" hash identically.
func hashRecovery(code string) string {
	var b strings.Builder
	for _, r := range strings.ToUpper(code) {
		if (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		}
	}
	sum := sha256.Sum256([]byte(b.String()))
	return hex.EncodeToString(sum[:])
}

func defaultGenSecret(issuer, account string) (string, string, error) {
	key, err := totp.Generate(totp.GenerateOpts{Issuer: issuer, AccountName: account})
	if err != nil {
		return "", "", err
	}
	return key.Secret(), key.URL(), nil
}

// defaultGenRecovery returns recoveryCodeCount codes formatted XXXXX-XXXXX from
// an unambiguous alphabet (no 0/O/1/I/L).
func defaultGenRecovery() ([]string, error) {
	const alphabet = "ABCDEFGHJKMNPQRSTUVWXYZ23456789"
	out := make([]string, recoveryCodeCount)
	for i := range out {
		buf := make([]byte, 10)
		if _, err := rand.Read(buf); err != nil {
			return nil, err
		}
		var sb strings.Builder
		for j, b := range buf {
			if j == 5 {
				sb.WriteByte('-')
			}
			sb.WriteByte(alphabet[int(b)%len(alphabet)])
		}
		out[i] = sb.String()
	}
	return out, nil
}
