// Package passkey implements optional WebAuthn ("passkey") authentication for
// local accounts: enrollment (begin/finish) from the profile page, usernameless
// (discoverable) login, and credential management. The relying-party identity is
// derived strictly from the configured subscription base URL (never the request
// Host) to avoid RP-ID poisoning; the per-ceremony challenge lives in a
// single-use in-memory store, and the credential record is stored intact so the
// sign-count / clone check stays valid.
package passkey

import (
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/go-webauthn/webauthn/protocol"
	"github.com/go-webauthn/webauthn/webauthn"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

// CredStore is the slice of the credential repo this service needs.
type CredStore interface {
	Save(ctx context.Context, c *domain.PasskeyCredential) error
	FindByUserID(ctx context.Context, userID int64) ([]*domain.PasskeyCredential, error)
	FindByCredentialID(ctx context.Context, credentialID string) (*domain.PasskeyCredential, error)
	UpdateAfterLogin(ctx context.Context, id int64, credential []byte, newSignCount int64, lastUsed time.Time) (bool, error)
	Rename(ctx context.Context, id, userID int64, name string) error
	Delete(ctx context.Context, id, userID int64) error
	DeleteAllByUserID(ctx context.Context, userID int64) (int, error)
	CountByUserIDs(ctx context.Context, userIDs []int64) (map[int64]int, error)
}

type UserGetter interface {
	GetByID(ctx context.Context, id int64) (*domain.User, error)
}

// SettingsLoader needs the global value (RP-ID / display name / passwordless are
// panel-wide) and the per-user effective value (a group can gate passkey
// enrollment + passkey-as-2FA). Wired to the ScopedSettings resolver.
type SettingsLoader interface {
	Load(ctx context.Context, defaults ports.UISettings) (ports.UISettings, error)
	LoadForUser(ctx context.Context, u *domain.User, defaults ports.UISettings) (ports.UISettings, error)
}

type Deps struct {
	Creds    CredStore
	Users    UserGetter
	Settings SettingsLoader
	Now      func() time.Time
}

type Service struct {
	d        Deps
	now      func() time.Time
	sessions *sessionStore
}

func New(d Deps) *Service {
	now := d.Now
	if now == nil {
		now = time.Now
	}
	return &Service{d: d, now: now, sessions: newSessionStore(now)}
}

// Available reports whether passkeys are enabled panel-wide (global). Kept for
// the global view + tests; enrollment / passkey-as-2FA use availableForUser.
func (s *Service) Available(ctx context.Context) bool {
	set, err := s.d.Settings.Load(ctx, ports.UISettings{})
	return err == nil && set.PasskeyEnabled
}

// availableForUser reports whether passkeys are enabled for the user's EFFECTIVE
// (group-scoped) settings — a group can gate passkey enrollment + passkey-as-2FA.
func (s *Service) availableForUser(ctx context.Context, userID int64) bool {
	u, err := s.d.Users.GetByID(ctx, userID)
	if err != nil {
		return false
	}
	set, err := s.d.Settings.LoadForUser(ctx, u, ports.UISettings{})
	return err == nil && set.PasskeyEnabled
}

// Passwordless reports whether usernameless passkey login is allowed (requires
// both the master switch and the passwordless toggle).
func (s *Service) Passwordless(ctx context.Context) bool {
	set, err := s.d.Settings.Load(ctx, ports.UISettings{})
	return err == nil && set.PasskeyEnabled && set.PasskeyPasswordless
}

// newWebAuthn builds the relying-party config from the configured subscription
// base URL. RP ID is the bare hostname and the single allowed origin is the
// scheme+host — deriving these from an attacker-controllable request Host header
// would be the classic RP-ID poisoning hole, so an unconfigured base URL is a
// hard error rather than a fallback.
func (s *Service) newWebAuthn(ctx context.Context) (*webauthn.WebAuthn, error) {
	set, err := s.d.Settings.Load(ctx, ports.UISettings{})
	if err != nil {
		return nil, err
	}
	rpID, origin, err := rpFromBaseURL(set.SubBaseURL)
	if err != nil {
		return nil, err
	}
	display := set.BrandName()
	return webauthn.New(&webauthn.Config{
		RPID:          rpID,
		RPDisplayName: display,
		RPOrigins:     []string{origin},
		AuthenticatorSelection: protocol.AuthenticatorSelection{
			// Prefer a discoverable (resident) credential so usernameless login
			// can find it; preferred (not required) keeps non-resident
			// authenticators usable as a second factor.
			ResidentKey:      protocol.ResidentKeyRequirementPreferred,
			UserVerification: protocol.VerificationPreferred,
		},
	})
}

// rpFromBaseURL derives the WebAuthn relying-party id (bare hostname, no port)
// and the single allowed origin (scheme://host[:port]) from the configured
// subscription base URL. An empty/invalid base URL is a hard error — falling
// back to the request Host header here is the canonical RP-ID poisoning vector.
func rpFromBaseURL(base string) (rpID, origin string, err error) {
	base = strings.TrimSpace(base)
	if base == "" {
		return "", "", fmt.Errorf("%w: passkeys require the subscription base URL to be configured first", domain.ErrValidation)
	}
	u, perr := url.Parse(base)
	if perr != nil || u.Hostname() == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return "", "", fmt.Errorf("%w: the configured subscription base URL is not a valid http(s) URL", domain.ErrValidation)
	}
	return u.Hostname(), u.Scheme + "://" + u.Host, nil
}

// webauthnUser adapts a domain.User + its stored credentials to webauthn.User.
type webauthnUser struct {
	u     *domain.User
	creds []webauthn.Credential
}

func (w *webauthnUser) WebAuthnID() []byte {
	b := make([]byte, 8)
	binary.BigEndian.PutUint64(b, uint64(w.u.ID))
	return b
}
func (w *webauthnUser) WebAuthnName() string { return w.u.UPN }
func (w *webauthnUser) WebAuthnDisplayName() string {
	if strings.TrimSpace(w.u.DisplayName) != "" {
		return w.u.DisplayName
	}
	return w.u.UPN
}
func (w *webauthnUser) WebAuthnCredentials() []webauthn.Credential { return w.creds }

// loadUser builds the webauthn.User adapter for a user id, loading + decoding
// their stored credentials.
func (s *Service) loadUser(ctx context.Context, userID int64) (*webauthnUser, []*domain.PasskeyCredential, error) {
	u, err := s.d.Users.GetByID(ctx, userID)
	if err != nil {
		return nil, nil, err
	}
	stored, err := s.d.Creds.FindByUserID(ctx, userID)
	if err != nil {
		return nil, nil, err
	}
	creds := make([]webauthn.Credential, 0, len(stored))
	for _, sc := range stored {
		var c webauthn.Credential
		if err := json.Unmarshal(sc.Credential, &c); err != nil {
			return nil, nil, fmt.Errorf("decode stored credential %d: %w", sc.ID, err)
		}
		creds = append(creds, c)
	}
	return &webauthnUser{u: u, creds: creds}, stored, nil
}

// BeginRegistration starts enrollment for a logged-in user: returns the creation
// options to hand to the browser + a session id that must come back to Finish.
func (s *Service) BeginRegistration(ctx context.Context, userID int64) (*protocol.PublicKeyCredentialCreationOptions, string, error) {
	if !s.availableForUser(ctx, userID) {
		return nil, "", fmt.Errorf("%w: passkeys are not enabled on this panel", domain.ErrForbidden)
	}
	wa, err := s.newWebAuthn(ctx)
	if err != nil {
		return nil, "", err
	}
	wu, _, err := s.loadUser(ctx, userID)
	if err != nil {
		return nil, "", err
	}
	if !wu.u.HasLocalPassword() {
		return nil, "", fmt.Errorf("%w: passkeys are only available for local-password accounts", domain.ErrValidation)
	}
	creation, session, err := wa.BeginRegistration(wu)
	if err != nil {
		return nil, "", err
	}
	id, err := s.sessions.put(session)
	if err != nil {
		return nil, "", err
	}
	return &creation.Response, id, nil
}

// FinishRegistration verifies the attestation, stores the new credential under
// the given name, and returns it. The request body is the authenticator response
// produced by the browser.
func (s *Service) FinishRegistration(ctx context.Context, userID int64, sessionID, name string, r *http.Request) (*domain.PasskeyCredential, error) {
	if !s.availableForUser(ctx, userID) {
		return nil, fmt.Errorf("%w: passkeys are not enabled on this panel", domain.ErrForbidden)
	}
	session := s.sessions.take(sessionID)
	if session == nil {
		return nil, fmt.Errorf("%w: enrollment session expired; start again", domain.ErrValidation)
	}
	wa, err := s.newWebAuthn(ctx)
	if err != nil {
		return nil, err
	}
	wu, _, err := s.loadUser(ctx, userID)
	if err != nil {
		return nil, err
	}
	cred, err := wa.FinishRegistration(wu, *session, r)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", domain.ErrValidation, err)
	}
	raw, err := json.Marshal(cred)
	if err != nil {
		return nil, err
	}
	name = strings.TrimSpace(name)
	if name == "" {
		name = "Passkey"
	}
	dc := &domain.PasskeyCredential{
		UserID:       userID,
		CredentialID: base64.RawURLEncoding.EncodeToString(cred.ID),
		Credential:   raw,
		SignCount:    int64(cred.Authenticator.SignCount),
		Name:         name,
	}
	if err := s.d.Creds.Save(ctx, dc); err != nil {
		return nil, err
	}
	return dc, nil
}

// BeginLogin starts a usernameless (discoverable) login: it returns request
// options with no credential allow-list, so the browser offers any passkey for
// this RP, plus a session id. It reveals nothing about which accounts exist.
func (s *Service) BeginLogin(ctx context.Context) (*protocol.PublicKeyCredentialRequestOptions, string, error) {
	if !s.Passwordless(ctx) {
		return nil, "", fmt.Errorf("%w: passwordless passkey login is not enabled", domain.ErrForbidden)
	}
	wa, err := s.newWebAuthn(ctx)
	if err != nil {
		return nil, "", err
	}
	assertion, session, err := wa.BeginDiscoverableLogin()
	if err != nil {
		return nil, "", err
	}
	id, err := s.sessions.put(session)
	if err != nil {
		return nil, "", err
	}
	return &assertion.Response, id, nil
}

// FinishLogin verifies a discoverable-login assertion, resolves the owning user
// from the credential, advances the (gated) sign count, and returns the user.
func (s *Service) FinishLogin(ctx context.Context, sessionID string, r *http.Request) (*domain.User, error) {
	if !s.Passwordless(ctx) {
		return nil, fmt.Errorf("%w: passwordless passkey login is not enabled", domain.ErrForbidden)
	}
	session := s.sessions.take(sessionID)
	if session == nil {
		return nil, fmt.Errorf("%w: login session expired; try again", domain.ErrValidation)
	}
	wa, err := s.newWebAuthn(ctx)
	if err != nil {
		return nil, err
	}

	// resolved is captured by the handler so we can write back the gated sign
	// count after verification succeeds.
	var resolved *domain.User
	var stored *domain.PasskeyCredential
	handler := func(rawID, _ []byte) (webauthn.User, error) {
		credID := base64.RawURLEncoding.EncodeToString(rawID)
		sc, err := s.d.Creds.FindByCredentialID(ctx, credID)
		if err != nil {
			return nil, err
		}
		stored = sc
		wu, _, err := s.loadUser(ctx, sc.UserID)
		if err != nil {
			return nil, err
		}
		resolved = wu.u
		return wu, nil
	}

	cred, err := wa.FinishDiscoverableLogin(handler, *session, r)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", domain.ErrUnauthorized, err)
	}
	if resolved == nil || stored == nil {
		return nil, domain.ErrUnauthorized
	}
	if err := s.finalizeAssertion(ctx, stored, cred); err != nil {
		return nil, err
	}
	return resolved, nil
}

// BeginLoginForUser starts an allow-listed passkey assertion scoped to a single,
// already-identified user — the passkey-as-second-factor flow. Unlike BeginLogin
// (discoverable/usernameless), it only needs the enrollment master switch, not
// passwordless, and it errors if the user has no passkeys. Returns request
// options (with the user's credentials as the allow-list) + a session id.
func (s *Service) BeginLoginForUser(ctx context.Context, userID int64) (*protocol.PublicKeyCredentialRequestOptions, string, error) {
	if !s.availableForUser(ctx, userID) {
		return nil, "", fmt.Errorf("%w: passkeys are not enabled on this panel", domain.ErrForbidden)
	}
	wa, err := s.newWebAuthn(ctx)
	if err != nil {
		return nil, "", err
	}
	wu, stored, err := s.loadUser(ctx, userID)
	if err != nil {
		return nil, "", err
	}
	if len(stored) == 0 {
		return nil, "", fmt.Errorf("%w: no passkeys enrolled", domain.ErrValidation)
	}
	assertion, session, err := wa.BeginLogin(wu)
	if err != nil {
		return nil, "", err
	}
	id, err := s.sessions.put(session)
	if err != nil {
		return nil, "", err
	}
	return &assertion.Response, id, nil
}

// FinishLoginForUser verifies an allow-listed assertion against a single user's
// credentials (the 2FA-step counterpart of FinishLogin). The userID comes from a
// verified pending token, so this never resolves identity from the assertion. On
// success it rejects a cloned authenticator and advances the gated sign count.
func (s *Service) FinishLoginForUser(ctx context.Context, userID int64, sessionID string, r *http.Request) error {
	if !s.availableForUser(ctx, userID) {
		return fmt.Errorf("%w: passkeys are not enabled on this panel", domain.ErrForbidden)
	}
	session := s.sessions.take(sessionID)
	if session == nil {
		return fmt.Errorf("%w: login session expired; try again", domain.ErrValidation)
	}
	wa, err := s.newWebAuthn(ctx)
	if err != nil {
		return err
	}
	wu, stored, err := s.loadUser(ctx, userID)
	if err != nil {
		return err
	}
	cred, err := wa.FinishLogin(wu, *session, r)
	if err != nil {
		return fmt.Errorf("%w: %v", domain.ErrUnauthorized, err)
	}
	credID := base64.RawURLEncoding.EncodeToString(cred.ID)
	var matched *domain.PasskeyCredential
	for _, sc := range stored {
		if sc.CredentialID == credID {
			matched = sc
			break
		}
	}
	if matched == nil {
		return domain.ErrUnauthorized
	}
	return s.finalizeAssertion(ctx, matched, cred)
}

// finalizeAssertion is the post-verification step of a passkey login: it rejects
// a cloned/replayed authenticator and persists the advanced sign count.
//
// go-webauthn does NOT error on a sign-count regression — it sets
// cred.Authenticator.CloneWarning and keeps the OLD count (and deliberately
// exempts the all-zero case, so platform authenticators that always report
// counter 0 are not falsely flagged). So CloneWarning — not the DB gate — is the
// real clone guard; refuse the login when it's set. The gated UpdateAfterLogin
// then keeps the stored count monotonic, but a lost gate there is a benign
// concurrent-login race (another login already advanced it), not a clone, so it
// must NOT fail the login.
func (s *Service) finalizeAssertion(ctx context.Context, stored *domain.PasskeyCredential, cred *webauthn.Credential) error {
	if cred.Authenticator.CloneWarning {
		return fmt.Errorf("%w: authenticator state regression (possible clone or replay)", domain.ErrUnauthorized)
	}
	raw, err := json.Marshal(cred)
	if err != nil {
		return err
	}
	if _, err := s.d.Creds.UpdateAfterLogin(ctx, stored.ID, raw, int64(cred.Authenticator.SignCount), s.now()); err != nil {
		return err
	}
	return nil
}

// List returns a user's registered credentials (profile management).
func (s *Service) List(ctx context.Context, userID int64) ([]*domain.PasskeyCredential, error) {
	return s.d.Creds.FindByUserID(ctx, userID)
}

// CountByUsers returns the passkey count per user id in one query — the admin
// user-list enrichment so the UI can flag accounts that have a passkey factor.
func (s *Service) CountByUsers(ctx context.Context, userIDs []int64) (map[int64]int, error) {
	return s.d.Creds.CountByUserIDs(ctx, userIDs)
}

// Rename / Delete are user-scoped at the repo so a caller can't touch another's.
func (s *Service) Rename(ctx context.Context, id, userID int64, name string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return fmt.Errorf("%w: name is required", domain.ErrValidation)
	}
	return s.d.Creds.Rename(ctx, id, userID, name)
}

func (s *Service) Delete(ctx context.Context, id, userID int64) error {
	return s.d.Creds.Delete(ctx, id, userID)
}

// RevokeAll removes every passkey on an account — the admin break-glass for a
// lost-all-devices / compromised user, mirroring twofa AdminReset. Returns the
// number of credentials removed. The caller (admin handler) decides who may run
// it; this method itself is identity-agnostic and just targets userID.
func (s *Service) RevokeAll(ctx context.Context, userID int64) (int, error) {
	return s.d.Creds.DeleteAllByUserID(ctx, userID)
}
