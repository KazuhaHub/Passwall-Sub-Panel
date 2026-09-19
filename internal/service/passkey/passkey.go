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
	"errors"
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

// rpParams is the relying-party identity in force. digest binds a challenge to
// it; the display name is branding and is deliberately not part of that digest.
type rpParams struct {
	id     string
	origin string
	digest string
}

// LoginPurpose names the allow-listed ceremony an assertion is being used for.
//
// The SERVER decides it at the call site. It is never taken from the request, so
// a browser cannot promote a step-up ceremony into a full login — the two differ
// in what the caller may do afterwards, not in how the assertion is verified.
type LoginPurpose string

const (
	PurposeSecondFactor LoginPurpose = "login_second_factor"
	PurposeStepUp       LoginPurpose = "step_up"
)

// challengePurpose maps the caller-facing purpose onto the store's, refusing an
// unset one rather than defaulting to something permissive.
func (p LoginPurpose) challengePurpose() (challengePurpose, error) {
	switch p {
	case PurposeSecondFactor:
		return purposeLoginSecondFactor, nil
	case PurposeStepUp:
		return purposeStepUp, nil
	default:
		return "", fmt.Errorf("%w: unknown passkey login purpose %q", domain.ErrValidation, string(p))
	}
}

// newWebAuthn builds the relying-party config from the configured subscription
// base URL. RP ID is the bare hostname and the single allowed origin is the
// scheme+host — deriving these from an attacker-controllable request Host header
// would be the classic RP-ID poisoning hole, so an unconfigured base URL is a
// hard error rather than a fallback.
//
// It returns the parameters alongside the handle so a caller can bind its
// challenge to exactly the RP that was in force when it was issued.
func (s *Service) newWebAuthn(ctx context.Context) (*webauthn.WebAuthn, rpParams, error) {
	set, err := s.d.Settings.Load(ctx, ports.UISettings{})
	if err != nil {
		return nil, rpParams{}, err
	}
	rpID, origin, err := rpFromBaseURL(set.SubBaseURL)
	if err != nil {
		return nil, rpParams{}, err
	}
	params := rpParams{id: rpID, origin: origin, digest: rpDigestOf(rpID, origin)}
	wa, err := webauthn.New(&webauthn.Config{
		RPID:          rpID,
		RPDisplayName: set.BrandName(),
		RPOrigins:     []string{origin},
		AuthenticatorSelection: protocol.AuthenticatorSelection{
			// Prefer a discoverable (resident) credential so usernameless login
			// can find it; preferred (not required) keeps non-resident
			// authenticators usable as a second factor.
			ResidentKey:      protocol.ResidentKeyRequirementPreferred,
			UserVerification: protocol.VerificationPreferred,
		},
	})
	if err != nil {
		return nil, rpParams{}, err
	}
	return wa, params, nil
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
	wa, rp, err := s.newWebAuthn(ctx)
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
	id, err := s.sessions.put(session, challengeRequest{
		purpose: purposeRegistration, userID: userID, rpDigest: rp.digest,
	})
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
	wa, rp, err := s.newWebAuthn(ctx)
	if err != nil {
		return nil, err
	}
	session, err := s.sessions.take(sessionID, challengeRequest{
		purpose: purposeRegistration, userID: userID, rpDigest: rp.digest,
	})
	if err != nil {
		return nil, fmt.Errorf("%w: enrollment session is no longer usable; start again", domain.ErrValidation)
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
	wa, rp, err := s.newWebAuthn(ctx)
	if err != nil {
		return nil, "", err
	}
	// Passwordless login is single-factor (no password, no 2FA step), so the
	// passkey itself must be a strong proof — that requires the authenticator to
	// actually perform user verification (PIN/biometric). The panel-wide config
	// is VerificationPreferred (kept for the passkey-as-2FA path, where it
	// follows a password); go-webauthn only enforces the UV flag when the
	// session's UserVerification is Required, so force it per-ceremony here. The
	// option flows into SessionData.UserVerification (login.go), so
	// FinishDiscoverableLogin rejects a UV=false assertion.
	requireUV := func(o *protocol.PublicKeyCredentialRequestOptions) error {
		o.UserVerification = protocol.VerificationRequired
		return nil
	}
	assertion, session, err := wa.BeginDiscoverableLogin(requireUV)
	if err != nil {
		return nil, "", err
	}
	// userID 0: a discoverable ceremony does not know the account until the
	// credential comes back, so the challenge is bound to no account rather than
	// to a guess.
	id, err := s.sessions.put(session, challengeRequest{
		purpose: purposeDiscoverableLogin, rpDigest: rp.digest,
	})
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
	wa, rp, err := s.newWebAuthn(ctx)
	if err != nil {
		return nil, err
	}
	session, err := s.sessions.take(sessionID, challengeRequest{
		purpose: purposeDiscoverableLogin, rpDigest: rp.digest,
	})
	if err != nil {
		return nil, fmt.Errorf("%w: login session is no longer usable; try again", domain.ErrValidation)
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
func (s *Service) BeginLoginForUser(ctx context.Context, userID int64, purpose LoginPurpose) (*protocol.PublicKeyCredentialRequestOptions, string, error) {
	storePurpose, err := purpose.challengePurpose()
	if err != nil {
		return nil, "", err
	}
	if !s.availableForUser(ctx, userID) {
		return nil, "", fmt.Errorf("%w: passkeys are not enabled on this panel", domain.ErrForbidden)
	}
	wa, rp, err := s.newWebAuthn(ctx)
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
	// The purpose and the account are both bound to the challenge: this ceremony
	// must not be completed as a different one, or for a different user.
	id, err := s.sessions.put(session, challengeRequest{
		purpose: storePurpose, userID: userID, rpDigest: rp.digest,
	})
	if err != nil {
		return nil, "", err
	}
	return &assertion.Response, id, nil
}

// FinishLoginForUser verifies an allow-listed assertion against a single user's
// credentials (the 2FA-step counterpart of FinishLogin). The userID comes from a
// verified pending token, so this never resolves identity from the assertion. On
// success it rejects a cloned authenticator and advances the gated sign count.
func (s *Service) FinishLoginForUser(ctx context.Context, userID int64, purpose LoginPurpose, sessionID string, r *http.Request) error {
	storePurpose, err := purpose.challengePurpose()
	if err != nil {
		return err
	}
	if !s.availableForUser(ctx, userID) {
		return fmt.Errorf("%w: passkeys are not enabled on this panel", domain.ErrForbidden)
	}
	wa, rp, err := s.newWebAuthn(ctx)
	if err != nil {
		return err
	}
	session, err := s.sessions.take(sessionID, challengeRequest{
		purpose: storePurpose, userID: userID, rpDigest: rp.digest,
	})
	if err != nil {
		return fmt.Errorf("%w: login session is no longer usable; try again", domain.ErrValidation)
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
// real clone guard; refuse the login when it's set.
//
// The gated write keeps the stored count monotonic, and when its gate refuses the
// write the cause has to be looked up rather than assumed. A concurrent login
// that stored at least this count is benign; a credential that was revoked
// between verification and here is not, and silently continuing would let an
// administrator's revoke fail to take effect on a login that was already in
// flight (ADR 0036 §7.3).
func (s *Service) finalizeAssertion(ctx context.Context, stored *domain.PasskeyCredential, cred *webauthn.Credential) error {
	if cred.Authenticator.CloneWarning {
		return fmt.Errorf("%w: authenticator state regression (possible clone or replay)", domain.ErrUnauthorized)
	}
	raw, err := json.Marshal(cred)
	if err != nil {
		return err
	}
	presented := int64(cred.Authenticator.SignCount)
	applied, err := s.d.Creds.UpdateAfterLogin(ctx, stored.ID, raw, presented, s.now())
	if err != nil {
		return err
	}
	if applied {
		return nil
	}

	// The gate refused the write. Only a look can say which of the two causes it
	// was, and the two mean opposite things.
	current, err := s.d.Creds.FindByCredentialID(ctx, stored.CredentialID)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			return fmt.Errorf("%w: the credential was revoked during the login", domain.ErrUnauthorized)
		}
		// An unreadable credential is an infrastructure failure, and saying
		// "unauthorized" for it would teach an operator to distrust the word.
		return fmt.Errorf("passkey: re-reading the credential after a refused write gate: %w", err)
	}
	if current == nil || current.ID != stored.ID || current.UserID != stored.UserID {
		// Another row owns this credential ID now, or the row moved to a different
		// account. Either way the assertion does not belong to it.
		return fmt.Errorf("%w: the credential no longer belongs to the verified account", domain.ErrUnauthorized)
	}
	if current.SignCount < presented {
		// The gate should have matched a count this far behind, so its refusal came
		// from something other than a concurrent advance. Refuse rather than guess.
		return fmt.Errorf("%w: the stored sign count is behind this assertion with no concurrent advance", domain.ErrUnauthorized)
	}
	// A concurrent login got there first and stored at least this count: the
	// monotonicity the gate exists to keep, and not a clone.
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
