// Package registration implements self-service account signup. A visitor
// registers with their email (which becomes their login username), and — when
// email verification is required (the default) — the account is created
// disabled and unprovisioned until they confirm the email via a one-time link
// or OTP. Verification reuses the auth_tokens infrastructure (purpose
// email_verify). Gated by admin settings: an allow-list of email domains, a
// default group + quota/expiry, and a master on/off toggle.
package registration

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"math/big"
	"net/mail"
	"net/url"
	"strings"
	"time"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/pkg/idgen"
	"github.com/KazuhaHub/passwall-sub-panel/internal/pkg/log"
	"github.com/KazuhaHub/passwall-sub-panel/internal/pkg/panelpath"
	"github.com/KazuhaHub/passwall-sub-panel/internal/pkg/safego"
	"github.com/KazuhaHub/passwall-sub-panel/internal/pkg/sendthrottle"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
	"github.com/KazuhaHub/passwall-sub-panel/internal/service/user"
)

const (
	defaultTokenTTL = 30 * time.Minute
	gib             = 1024 * 1024 * 1024
	// defaultResendCooldown throttles how often a verification email is (re)sent
	// for one account — the anti-email-bomb guard that lets re-register/resend
	// resume an abandoned signup without becoming a flood vector.
	defaultResendCooldown = 60 * time.Second
	// Panel-wide send cap: OFF by default (0). The per-account cooldown above is
	// always on (harmless to legit users), but a hard global ceiling can throttle
	// a legitimate signup burst — and the admin already has captcha as the real
	// anti-abuse control. The mechanism stays available via Deps.SendGlobalLimit
	// for anyone who wants to wire it to a setting later.
	defaultSendGlobalLimit  = 0
	defaultSendGlobalWindow = time.Minute
)

// UserStore is the slice of user.Service registration needs.
type UserStore interface {
	CreateLocal(ctx context.Context, in user.CreateLocalInput) (*user.CreateLocalResult, error)
	CreateLocalAndSync(ctx context.Context, in user.CreateLocalInput) (*user.CreateLocalSyncedResult, error)
	ActivateAfterVerification(ctx context.Context, userID int64) error
	GetByUPN(ctx context.Context, upn string) (*domain.User, error)
	// SetPassword refreshes a local account's password — used to resume a
	// pending (unverified) signup when the visitor re-registers.
	SetPassword(ctx context.Context, userID int64, newPassword string) error
}

// GroupLookup resolves the default registration group.
type GroupLookup interface {
	GetByID(ctx context.Context, id int64) (*domain.Group, error)
	List(ctx context.Context) ([]*domain.Group, error)
}

// EmailVerifySender delivers the verification email (link or OTP).
type EmailVerifySender interface {
	SendEmailVerification(ctx context.Context, to, displayName, link, code string, expireMinutes int) error
}

type Deps struct {
	Users    UserStore
	Groups   GroupLookup
	Tokens   ports.AuthTokenRepo
	Mail     EmailVerifySender
	Settings ports.SettingsRepo
	Now      func() time.Time
	NewToken func() (string, error)
	NewCode  func() (string, error)
	Dispatch func(func()) // email send runner; defaults to a goroutine
	TokenTTL time.Duration
	// SendGlobalLimit / SendGlobalWindow cap verification emails panel-wide.
	// SendGlobalLimit defaults to 0 (off). Overridable for tests.
	SendGlobalLimit  int
	SendGlobalWindow time.Duration
}

type Service struct {
	d        Deps
	now      func() time.Time
	newToken func() (string, error)
	newCode  func() (string, error)
	dispatch func(func())
	tokenTTL time.Duration
	// throttle gates the verification-email side effect: a per-account cooldown
	// (anti same-victim bomb) AND a panel-wide cap (anti distributed SMTP flood).
	throttle *sendthrottle.Throttle
}

func New(d Deps) *Service {
	s := &Service{d: d, now: d.Now, newToken: d.NewToken, newCode: d.NewCode, dispatch: d.Dispatch, tokenTTL: d.TokenTTL}
	if s.now == nil {
		s.now = time.Now
	}
	if s.newToken == nil {
		s.newToken = idgen.NewSubToken
	}
	if s.newCode == nil {
		s.newCode = newOTP
	}
	if s.dispatch == nil {
		s.dispatch = func(f func()) { safego.Go("email-verify-send", f) }
	}
	if s.tokenTTL <= 0 {
		s.tokenTTL = defaultTokenTTL
	}
	globalLimit := d.SendGlobalLimit
	if globalLimit <= 0 {
		globalLimit = defaultSendGlobalLimit
	}
	globalWindow := d.SendGlobalWindow
	if globalWindow <= 0 {
		globalWindow = defaultSendGlobalWindow
	}
	s.throttle = sendthrottle.New(globalLimit, globalWindow, s.now)
	return s
}

// resendCooldown resolves the per-account verification-email cooldown from the
// live admin setting, falling back to the default when unset.
func resendCooldown(set ports.UISettings) time.Duration {
	if set.CodeResendCooldownSec > 0 {
		return time.Duration(set.CodeResendCooldownSec) * time.Second
	}
	return defaultResendCooldown
}

// RegisterInput is the public signup payload. The email doubles as the login
// username (UPN), so there's no separate username to pick.
type RegisterInput struct {
	Email       string
	Password    string
	DisplayName string
}

// RegisterResult tells the caller whether the account still needs email
// verification before it can be used.
type RegisterResult struct {
	RequiresVerification bool
}

// Register validates and creates a new local account. Unlike password recovery,
// it deliberately DOES reveal "email already registered" (ErrAlreadyExists) —
// that's expected signup UX, and the verification gate means a taken email
// can't be hijacked.
func (s *Service) Register(ctx context.Context, in RegisterInput) (*RegisterResult, error) {
	set, err := s.d.Settings.Load(ctx, ports.UISettings{})
	if err != nil {
		return nil, err
	}
	if !set.RegistrationEnabled {
		return nil, fmt.Errorf("%w: registration is disabled", domain.ErrForbidden)
	}
	email := strings.ToLower(strings.TrimSpace(in.Email))
	if !validEmail(email) {
		return nil, fmt.Errorf("%w: invalid email address", domain.ErrValidation)
	}
	if !emailDomainAllowed(email, set.RegistrationEmailDomains) {
		return nil, fmt.Errorf("%w: email domain not allowed for registration", domain.ErrValidation)
	}
	if !user.IsMinimallyStrongPassword(in.Password) {
		return nil, fmt.Errorf("%w: password too weak (need ≥8 chars with at least one letter and one digit)", domain.ErrValidation)
	}

	groupID, err := s.resolveGroup(ctx, set.RegistrationDefaultGroupID)
	if err != nil {
		return nil, err
	}
	var expireAt *time.Time
	if set.RegistrationDefaultExpireDays > 0 {
		t := s.now().AddDate(0, 0, set.RegistrationDefaultExpireDays)
		expireAt = &t
	}
	requireVerify := !set.RegistrationAllowUnverified
	cin := user.CreateLocalInput{
		UPN:                email, // email IS the login username for self-signup
		Email:              email,
		DisplayName:        strings.TrimSpace(in.DisplayName),
		InitialPassword:    in.Password,
		GroupID:            groupID,
		ExpireAt:           expireAt,
		TrafficLimitBytes:  int64(set.RegistrationDefaultTrafficGB * gib),
		PendingEmailVerify: requireVerify,
		SelfRegistered:     true, // excludes it from silent first-time SSO linking
	}

	if !requireVerify {
		// No verification → create and provision immediately (account is live).
		if _, err := s.d.Users.CreateLocalAndSync(ctx, cin); err != nil {
			return nil, err
		}
		return &RegisterResult{RequiresVerification: false}, nil
	}

	// Resume an abandoned signup: if a PENDING (unverified, disabled) account
	// already owns this email, don't dead-end on "already registered" — refresh
	// the password to what was just typed and re-send a fresh code (cooldown-
	// guarded). Safe because activation is still gated by the email the code is
	// sent to: only the inbox owner can finish. A fully-registered (enabled, or
	// disabled for any OTHER reason) account falls through to CreateLocal, which
	// returns ErrAlreadyExists — a real account can never be hijacked this way.
	if existing, gerr := s.d.Users.GetByUPN(ctx, email); gerr == nil && existing != nil {
		if !existing.Enabled && existing.AutoDisabledReason == domain.DisabledPendingEmailVerify {
			if perr := s.d.Users.SetPassword(ctx, existing.ID, in.Password); perr != nil {
				return nil, perr
			}
			s.sendVerification(ctx, set, existing)
			return &RegisterResult{RequiresVerification: true}, nil
		}
		return nil, domain.ErrAlreadyExists
	}

	// Verification required → create a disabled, unprovisioned account, then
	// email a one-time confirmation.
	res, err := s.d.Users.CreateLocal(ctx, cin)
	if err != nil {
		return nil, err
	}
	s.sendVerification(ctx, set, res.User)
	return &RegisterResult{RequiresVerification: true}, nil
}

// ResendVerification re-sends the email-verify code/link for a PENDING account.
// Enumeration-safe by contract: it returns nothing and the handler always
// answers 200, so a caller can't tell whether the email exists or its state.
// The send itself is cooldown-guarded (anti email-bomb).
func (s *Service) ResendVerification(ctx context.Context, email string) {
	email = strings.ToLower(strings.TrimSpace(email))
	if !validEmail(email) {
		return
	}
	set, err := s.d.Settings.Load(ctx, ports.UISettings{})
	if err != nil || !set.RegistrationEnabled {
		return
	}
	u, err := s.d.Users.GetByUPN(ctx, email)
	if err != nil || u == nil {
		return
	}
	// Only resend for accounts still awaiting their first email verification.
	if u.Enabled || u.AutoDisabledReason != domain.DisabledPendingEmailVerify {
		return
	}
	s.sendVerification(ctx, set, u)
}

// VerifyInput confirms an email. Link delivery fills Token; OTP delivery fills
// Ident (the email) + Code.
type VerifyInput struct {
	Token string
	Ident string
	Code  string
}

// Verify consumes the email_verify token and activates the account (enables it
// and provisions its proxy clients). Generic ErrUnauthorized on a bad/expired
// token so the endpoint can't be used to probe.
func (s *Service) Verify(ctx context.Context, in VerifyInput) error {
	now := s.now()
	var tok *domain.AuthToken
	var err error
	if strings.TrimSpace(in.Token) != "" {
		tok, err = s.d.Tokens.ConsumeByTokenHash(ctx, domain.AuthTokenPurposeEmailVerify, hashSecret(in.Token), now)
	} else {
		u, uerr := s.d.Users.GetByUPN(ctx, strings.ToLower(strings.TrimSpace(in.Ident)))
		if uerr != nil {
			return domain.ErrUnauthorized
		}
		tok, err = s.d.Tokens.ConsumeByUserCode(ctx, domain.AuthTokenPurposeEmailVerify, u.ID, hashSecret(in.Code), now)
	}
	if err != nil || tok == nil {
		return domain.ErrUnauthorized
	}
	return s.d.Users.ActivateAfterVerification(ctx, tok.UserID)
}

// sendVerification generates and emails a one-time verification token/code.
// Cooldown-gated per account so re-register / resend can't flood an inbox.
func (s *Service) sendVerification(ctx context.Context, set ports.UISettings, u *domain.User) {
	if !s.throttle.Allow(u.ID, resendCooldown(set)) {
		log.Info("registration: verification send throttled (cooldown or global cap), suppressed", "user_id", u.ID)
		return
	}
	// Invalidate any prior live verify token so only the freshly-sent one works
	// (a no-op on first registration). Keeps a single live code per account.
	if derr := s.d.Tokens.DeleteByUserPurpose(ctx, u.ID, domain.AuthTokenPurposeEmailVerify); derr != nil {
		log.Warn("registration: invalidate prior verify tokens", "user_id", u.ID, "err", derr)
	}
	now := s.now()
	tok := &domain.AuthToken{
		UserID:    u.ID,
		Purpose:   domain.AuthTokenPurposeEmailVerify,
		Email:     u.Email,
		ExpiresAt: now.Add(s.tokenTTL),
	}
	var link, code string
	if strings.ToLower(strings.TrimSpace(set.RegistrationDelivery)) == "otp" {
		c, err := s.newCode()
		if err != nil {
			log.Warn("registration: gen otp", "err", err)
			return
		}
		code = c
		tok.CodeHash = hashSecret(code)
	} else {
		// Link delivery needs the trusted configured base URL (never a request
		// Host header — same reset-poisoning concern as recovery).
		base := strings.TrimRight(strings.TrimSpace(set.SubBaseURL), "/")
		if base == "" {
			log.Warn("registration: link delivery requires sub_base_url; not sending", "user_id", u.ID)
			return
		}
		raw, err := s.newToken()
		if err != nil {
			log.Warn("registration: gen token", "err", err)
			return
		}
		tok.TokenHash = hashSecret(raw)
		link = panelpath.PanelURL(base, set.PanelPath, "/verify-email?token="+url.QueryEscape(raw))
	}
	if err := s.d.Tokens.Create(ctx, tok); err != nil {
		log.Warn("registration: create token", "user_id", u.ID, "err", err)
		return
	}
	name := u.DisplayName
	if name == "" {
		name = u.UPN
	}
	to, expireMin := u.Email, int(s.tokenTTL.Minutes())
	s.dispatch(func() {
		sendCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if err := s.d.Mail.SendEmailVerification(sendCtx, to, name, link, code, expireMin); err != nil {
			log.Warn("registration: send verify email", "to", to, "err", err)
		}
	})
}

// resolveGroup returns the configured default group, or the first existing group
// when unset (the bootstrap "default" group). Errors if neither resolves so a
// registrant is never created groupless.
func (s *Service) resolveGroup(ctx context.Context, configured int64) (int64, error) {
	if configured > 0 {
		if _, err := s.d.Groups.GetByID(ctx, configured); err != nil {
			return 0, fmt.Errorf("registration default group %d: %w", configured, err)
		}
		return configured, nil
	}
	groups, err := s.d.Groups.List(ctx)
	if err != nil {
		return 0, err
	}
	if len(groups) == 0 {
		return 0, fmt.Errorf("%w: no group available for registration", domain.ErrValidation)
	}
	return groups[0].ID, nil
}

func validEmail(email string) bool {
	if email == "" || !strings.Contains(email, "@") {
		return false
	}
	addr, err := mail.ParseAddress(email)
	return err == nil && addr.Address == email
}

// emailDomainAllowed reports whether email's domain is in the comma-separated
// allow-list. An empty list allows any domain.
func emailDomainAllowed(email, list string) bool {
	at := strings.LastIndex(email, "@")
	if at < 0 {
		return false
	}
	domainPart := strings.ToLower(email[at+1:])
	var any bool
	for _, raw := range strings.Split(list, ",") {
		d := strings.ToLower(strings.TrimSpace(raw))
		if d == "" {
			continue
		}
		any = true
		if d == domainPart {
			return true
		}
	}
	return !any // no entries → unrestricted
}

func hashSecret(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

func newOTP() (string, error) {
	n, err := rand.Int(rand.Reader, big.NewInt(1_000_000))
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%06d", n.Int64()), nil
}
