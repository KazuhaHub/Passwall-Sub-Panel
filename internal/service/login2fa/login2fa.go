// Package login2fa implements the optional "email one-time code" alternative for
// the login 2FA challenge. It reuses the auth_tokens OTP machinery (hashed,
// single-use, short TTL, per-user scoped, 5-attempt burn at the repo) the same
// way password recovery does, plus the login rate limiter at the HTTP layer.
//
// Email is a deliberately WEAKER factor than TOTP/passkey (whoever holds the
// password + inbox passes), so it is admin opt-in (TwoFAAllowEmail, default off).
package login2fa

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"math/big"
	"strings"
	"sync"
	"time"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/pkg/log"
	"github.com/KazuhaHub/passwall-sub-panel/internal/pkg/safego"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

const defaultCodeTTL = 10 * time.Minute

// resendCooldown throttles how often a fresh code may be emailed to one account.
// The send is already password-gated (a valid pending token), but without a
// per-account cooldown a holder could trigger repeated mails to the victim's
// inbox; a short window also avoids accidental double-sends. The outstanding code
// stays valid through the cooldown, so suppressing a re-send is harmless.
const resendCooldown = 60 * time.Second

// Sender delivers the one-time login code email.
type Sender interface {
	SendLogin2FACode(ctx context.Context, to, displayName, code string, expireMinutes int) error
}

// SettingsLoader is the slice of the settings repo this service needs.
type SettingsLoader interface {
	Load(ctx context.Context, defaults ports.UISettings) (ports.UISettings, error)
}

// Deps wires the email-2FA service.
type Deps struct {
	Tokens   ports.AuthTokenRepo
	Mail     Sender
	Settings SettingsLoader
	CodeTTL  time.Duration
	Now      func() time.Time
	NewCode  func() (string, error)
	// Dispatch runs the email send off the request critical path (defaults to a
	// panic-shielded goroutine; tests inject a synchronous runner).
	Dispatch func(func())
}

type Service struct {
	d        Deps
	now      func() time.Time
	codeTTL  time.Duration
	newCode  func() (string, error)
	dispatch func(func())

	mu       sync.Mutex
	lastSent map[int64]time.Time // userID → last code-send time (resend cooldown)
}

func New(d Deps) *Service {
	s := &Service{d: d, now: d.Now, codeTTL: d.CodeTTL, newCode: d.NewCode, dispatch: d.Dispatch, lastSent: map[int64]time.Time{}}
	if s.now == nil {
		s.now = time.Now
	}
	if s.codeTTL <= 0 {
		s.codeTTL = defaultCodeTTL
	}
	if s.newCode == nil {
		s.newCode = newOTP
	}
	if s.dispatch == nil {
		s.dispatch = func(f func()) { safego.Go("login-2fa-email", f) }
	}
	return s
}

// SendCode emails a fresh one-time code to u's address to complete the 2FA
// challenge. It is gated on the admin toggle + a present email; a new request
// invalidates any earlier outstanding code. The send is async so SMTP latency
// can't stall the response. The caller has already verified the pending token,
// so there is no enumeration concern — feature/email errors are returned.
func (s *Service) SendCode(ctx context.Context, u *domain.User) error {
	set, err := s.d.Settings.Load(ctx, ports.UISettings{})
	if err != nil {
		return err
	}
	if !set.TwoFAAllowEmail {
		return fmt.Errorf("%w: email is not an enabled verification method", domain.ErrForbidden)
	}
	to := strings.TrimSpace(u.Email)
	if to == "" {
		return fmt.Errorf("%w: no email address on file", domain.ErrValidation)
	}
	// Per-account resend cooldown: a recent code is still valid, so suppress a
	// rapid re-send (anti email-bombing). Recorded at the gate so a burst of
	// concurrent requests can't all slip through before the first send lands.
	now := s.now()
	s.mu.Lock()
	if last, ok := s.lastSent[u.ID]; ok && now.Sub(last) < resendCooldown {
		s.mu.Unlock()
		return nil
	}
	s.lastSent[u.ID] = now
	s.mu.Unlock()
	code, err := s.newCode()
	if err != nil {
		return err
	}
	tok := &domain.AuthToken{
		UserID:    u.ID,
		Purpose:   domain.AuthTokenPurposeLogin2FA,
		Email:     to,
		CodeHash:  hashSecret(code),
		ExpiresAt: now.Add(s.codeTTL),
	}
	if derr := s.d.Tokens.DeleteByUserPurpose(ctx, u.ID, domain.AuthTokenPurposeLogin2FA); derr != nil {
		log.Warn("login2fa: invalidate prior codes", "user_id", u.ID, "err", derr)
	}
	if cerr := s.d.Tokens.Create(ctx, tok); cerr != nil {
		return cerr
	}
	name := u.DisplayName
	if name == "" {
		name = u.UPN
	}
	expireMin := int(s.codeTTL.Minutes())
	s.dispatch(func() {
		sendCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if merr := s.d.Mail.SendLogin2FACode(sendCtx, to, name, code, expireMin); merr != nil {
			log.Warn("login2fa: send email", "to", to, "err", merr)
		}
	})
	return nil
}

// VerifyCode consumes a previously-emailed code for the user, returning true on a
// valid, unexpired, unconsumed code. Consumption is atomic at the repo (the same
// single-use guarantee password recovery relies on).
func (s *Service) VerifyCode(ctx context.Context, userID int64, code string) (bool, error) {
	code = strings.TrimSpace(code)
	if code == "" {
		return false, nil
	}
	tok, err := s.d.Tokens.ConsumeByUserCode(ctx, domain.AuthTokenPurposeLogin2FA, userID, hashSecret(code), s.now())
	if err != nil || tok == nil {
		return false, nil
	}
	return true, nil
}

// hashSecret returns the hex SHA-256 of an OTP for storage + comparison. OTPs are
// guarded by short TTL, single-use, per-user scoping and the login rate limiter.
func hashSecret(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

// newOTP returns a uniformly-random 6-digit numeric code.
func newOTP() (string, error) {
	n, err := rand.Int(rand.Reader, big.NewInt(1_000_000))
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%06d", n.Int64()), nil
}
