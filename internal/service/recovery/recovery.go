// Package recovery implements self-service local-password recovery: a user asks
// for a reset by username, receives an email (a one-time link OR a short OTP per
// the admin's delivery setting), and sets a new password with it.
//
// Two security invariants shape the design:
//   - No account enumeration: RequestReset returns success regardless of whether
//     the identifier matched, whether the account has a password, or whether it
//     has an email. The only observable difference is whether an email arrives.
//   - One-time, hashed tokens: only SHA-256 hashes are stored, and the repo's
//     Consume* marks a token used atomically so it can't be replayed.
package recovery

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"math/big"
	"net/url"
	"strings"
	"time"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/pkg/idgen"
	"github.com/KazuhaHub/passwall-sub-panel/internal/pkg/log"
	"github.com/KazuhaHub/passwall-sub-panel/internal/pkg/safego"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
	"github.com/KazuhaHub/passwall-sub-panel/internal/service/user"
)

const defaultTokenTTL = 30 * time.Minute

// UserLookup resolves the account a reset is for. ident is the UPN (local-login
// username) — email isn't a lookup key here because it's neither unique nor
// indexed.
type UserLookup interface {
	GetByUPN(ctx context.Context, upn string) (*domain.User, error)
}

// PasswordResetSender delivers the reset email. Exactly one of link/code is set.
type PasswordResetSender interface {
	SendPasswordReset(ctx context.Context, to, displayName, link, code string, expireMinutes int) error
}

// Deps wires the recovery service.
type Deps struct {
	Users    UserLookup
	Tokens   ports.AuthTokenRepo
	Mail     PasswordResetSender
	Settings ports.SettingsRepo
	// SetPassword performs the actual bcrypt set + session revocation
	// (user.Service.SetPassword in production).
	SetPassword func(ctx context.Context, userID int64, newPassword string) error
	TokenTTL    time.Duration
	Now         func() time.Time
	NewToken    func() (string, error) // link token; defaults to idgen.NewSubToken
	NewCode     func() (string, error) // OTP; defaults to a 6-digit code
	// Dispatch runs the email send. Defaults to a panic-shielded goroutine so
	// the SMTP round-trip is off the request critical path (no timing oracle, no
	// stalled response on a slow server). Tests inject a synchronous runner.
	Dispatch func(func())
}

type Service struct {
	d        Deps
	now      func() time.Time
	tokenTTL time.Duration
	newToken func() (string, error)
	newCode  func() (string, error)
	dispatch func(func())
}

func New(d Deps) *Service {
	s := &Service{
		d:        d,
		now:      d.Now,
		tokenTTL: d.TokenTTL,
		newToken: d.NewToken,
		newCode:  d.NewCode,
		dispatch: d.Dispatch,
	}
	if s.now == nil {
		s.now = time.Now
	}
	if s.tokenTTL <= 0 {
		s.tokenTTL = defaultTokenTTL
	}
	if s.newToken == nil {
		s.newToken = idgen.NewSubToken
	}
	if s.newCode == nil {
		s.newCode = newOTP
	}
	if s.dispatch == nil {
		s.dispatch = func(f func()) { safego.Go("password-reset-email", f) }
	}
	return s
}

// RequestReset issues a reset for the account named by ident, delivering it to
// the account's email. It always returns nil for caller-observable outcomes (no
// enumeration); internal failures are logged. The email is sent asynchronously
// so SMTP latency can't be used as a timing oracle and a slow server can't stall
// the response.
func (s *Service) RequestReset(ctx context.Context, ident string) error {
	set, err := s.d.Settings.Load(ctx, ports.UISettings{})
	if err != nil || !set.PasswordRecoveryEnabled {
		return nil
	}
	u, err := s.d.Users.GetByUPN(ctx, strings.TrimSpace(ident))
	if err != nil {
		return nil // unknown account — stay silent
	}
	if !u.HasLocalPassword() || strings.TrimSpace(u.Email) == "" {
		return nil // SSO-only or no delivery address — silent
	}
	// Manually-disabled / pending / blocked accounts can't log in anyway, so
	// there's nothing to recover; skip silently. Self-service disable reasons
	// (traffic-exceeded / expired) CAN still log in, so they keep recovery.
	if !u.Enabled && !domain.SelfServiceDisableReason(u.AutoDisabledReason) {
		return nil
	}

	now := s.now()
	tok := &domain.AuthToken{
		UserID:    u.ID,
		Purpose:   domain.AuthTokenPurposePasswordReset,
		Email:     u.Email,
		ExpiresAt: now.Add(s.tokenTTL),
	}
	var link, code string
	if strings.ToLower(strings.TrimSpace(set.PasswordRecoveryDelivery)) == "otp" {
		code, err = s.newCode()
		if err != nil {
			log.Warn("recovery: gen otp", "err", err)
			return nil
		}
		tok.CodeHash = hashSecret(code)
	} else {
		// Link delivery needs a TRUSTED canonical base URL. We never derive it
		// from the request Host header (attacker-controllable → password-reset
		// poisoning: a forged Host emails the victim a link to the attacker's
		// domain). If SubBaseURL isn't configured, refuse rather than send a
		// poisonable link — the admin-settings PUT guards against enabling this
		// combination, so this is the defensive backstop.
		base := strings.TrimRight(strings.TrimSpace(set.SubBaseURL), "/")
		if base == "" {
			log.Warn("recovery: link delivery requires sub_base_url; not sending", "user_id", u.ID)
			return nil
		}
		raw, terr := s.newToken()
		if terr != nil {
			log.Warn("recovery: gen token", "err", terr)
			return nil
		}
		tok.TokenHash = hashSecret(raw)
		link = base + "/reset-password?token=" + url.QueryEscape(raw)
	}

	// A new request invalidates any earlier outstanding reset for this user.
	if derr := s.d.Tokens.DeleteByUserPurpose(ctx, u.ID, domain.AuthTokenPurposePasswordReset); derr != nil {
		log.Warn("recovery: invalidate prior tokens", "user_id", u.ID, "err", derr)
	}
	if cerr := s.d.Tokens.Create(ctx, tok); cerr != nil {
		log.Warn("recovery: create token", "user_id", u.ID, "err", cerr)
		return nil
	}

	name := u.DisplayName
	if name == "" {
		name = u.UPN
	}
	to, expireMin := u.Email, int(s.tokenTTL.Minutes())
	s.dispatch(func() {
		// Detached from the request context so returning the response doesn't
		// cancel the in-flight SMTP dial.
		sendCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if merr := s.d.Mail.SendPasswordReset(sendCtx, to, name, link, code, expireMin); merr != nil {
			log.Warn("recovery: send email", "to", to, "err", merr)
		}
	})
	return nil
}

// ResetInput carries a reset confirmation. Link delivery fills Token; OTP
// delivery fills Ident+Code.
type ResetInput struct {
	Token       string
	Ident       string
	Code        string
	NewPassword string
}

// Reset validates the token/code and sets the new password. The password is
// checked BEFORE the token is consumed so a weak password doesn't burn a
// single-use token. Returns ErrUnauthorized for an invalid/expired token (a
// deliberately generic signal) and ErrValidation for a weak password.
func (s *Service) Reset(ctx context.Context, in ResetInput) error {
	if !user.IsMinimallyStrongPassword(in.NewPassword) {
		return fmt.Errorf("%w: password too weak (need ≥8 chars with at least one letter and one digit)", domain.ErrValidation)
	}
	now := s.now()
	var tok *domain.AuthToken
	var err error
	if strings.TrimSpace(in.Token) != "" {
		tok, err = s.d.Tokens.ConsumeByTokenHash(ctx, domain.AuthTokenPurposePasswordReset, hashSecret(in.Token), now)
	} else {
		u, uerr := s.d.Users.GetByUPN(ctx, strings.TrimSpace(in.Ident))
		if uerr != nil {
			return domain.ErrUnauthorized
		}
		tok, err = s.d.Tokens.ConsumeByUserCode(ctx, domain.AuthTokenPurposePasswordReset, u.ID, hashSecret(in.Code), now)
	}
	if err != nil || tok == nil {
		return domain.ErrUnauthorized
	}
	return s.d.SetPassword(ctx, tok.UserID, in.NewPassword)
}

// hashSecret returns the hex SHA-256 of a token/code for storage + comparison.
// High-entropy link tokens make this collision-safe; OTPs are additionally
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
