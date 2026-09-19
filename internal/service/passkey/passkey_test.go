package passkey

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/go-webauthn/webauthn/protocol"
	"github.com/go-webauthn/webauthn/webauthn"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

// The usernameless (passwordless) passkey login is single-factor — there is no
// password and the handler skips any 2FA step, on the premise that the passkey
// was asserted with user verification (PIN/biometric). That premise only holds
// if UV is REQUIRED on this ceremony: with VerificationPreferred go-webauthn
// does not enforce the UV flag, so a possession-only (UV=false) assertion would
// satisfy a full login. Guard that the discoverable-login session demands UV.
func TestBeginLogin_RequiresUserVerification(t *testing.T) {
	svc := New(Deps{Settings: stubSettings{ports.UISettings{
		PasskeyEnabled:      true,
		PasskeyPasswordless: true,
		SubBaseURL:          "https://panel.example.com",
	}}})
	opts, _, err := svc.BeginLogin(context.Background())
	if err != nil {
		t.Fatalf("BeginLogin: %v", err)
	}
	if opts.UserVerification != protocol.VerificationRequired {
		t.Fatalf("passwordless login UserVerification = %q, want %q (single-factor passkey path must enforce UV)",
			opts.UserVerification, protocol.VerificationRequired)
	}
}

func TestRPFromBaseURL(t *testing.T) {
	cases := []struct {
		base, rpID, origin string
		wantErr            bool
	}{
		{"https://panel.example.com", "panel.example.com", "https://panel.example.com", false},
		{"https://panel.example.com:8443", "panel.example.com", "https://panel.example.com:8443", false},
		{"https://panel.example.com/sub/", "panel.example.com", "https://panel.example.com", false},
		{"http://localhost:3000", "localhost", "http://localhost:3000", false},
		{"", "", "", true},
		{"   ", "", "", true},
		{"not a url", "", "", true},
		{"ftp://x.com", "", "", true},
	}
	for _, c := range cases {
		rpID, origin, err := rpFromBaseURL(c.base)
		if c.wantErr {
			if err == nil {
				t.Fatalf("rpFromBaseURL(%q) should error", c.base)
			}
			if !errors.Is(err, domain.ErrValidation) {
				t.Fatalf("rpFromBaseURL(%q) error must be ErrValidation, got %v", c.base, err)
			}
			continue
		}
		if err != nil || rpID != c.rpID || origin != c.origin {
			t.Fatalf("rpFromBaseURL(%q) = (%q, %q, %v), want (%q, %q, nil)", c.base, rpID, origin, err, c.rpID, c.origin)
		}
	}
}

func TestSessionStore_SingleUse(t *testing.T) {
	st := newSessionStore(time.Now)
	id, err := st.put(&webauthn.SessionData{Challenge: "abc"})
	if err != nil {
		t.Fatal(err)
	}
	got := st.take(id)
	if got == nil || got.Challenge != "abc" {
		t.Fatalf("first take should return the session, got %v", got)
	}
	if again := st.take(id); again != nil {
		t.Fatal("a consumed session must not be takeable again (replay guard)")
	}
	if unknown := st.take("nope"); unknown != nil {
		t.Fatal("unknown id must return nil")
	}
}

func TestSessionStore_Expiry(t *testing.T) {
	now := time.Now()
	clock := now
	st := newSessionStore(func() time.Time { return clock })
	id, _ := st.put(&webauthn.SessionData{Challenge: "x"})
	clock = now.Add(sessionTTL + time.Second) // advance past TTL
	if got := st.take(id); got != nil {
		t.Fatal("an expired session must not be returned")
	}
}

type stubSettings struct{ s ports.UISettings }

func (f stubSettings) Load(context.Context, ports.UISettings) (ports.UISettings, error) {
	return f.s, nil
}

func (f stubSettings) LoadForUser(context.Context, *domain.User, ports.UISettings) (ports.UISettings, error) {
	return f.s, nil
}

type stubCredStore struct {
	updated     bool
	revokedUser int64
	revokeN     int

	// gateLost makes UpdateAfterLogin report "the write did not apply", which is
	// what a concurrent login or a revoke produces. The two are indistinguishable
	// at the gate, so the service has to look afterwards.
	gateLost bool
	gateErr  error

	// reRead is what FindByCredentialID answers with after a lost gate.
	reRead     *domain.PasskeyCredential
	reReadErr  error
	reReadHits int
}

func (s *stubCredStore) Save(context.Context, *domain.PasskeyCredential) error { return nil }
func (s *stubCredStore) FindByUserID(context.Context, int64) ([]*domain.PasskeyCredential, error) {
	return nil, nil
}
func (s *stubCredStore) FindByCredentialID(context.Context, string) (*domain.PasskeyCredential, error) {
	s.reReadHits++
	if s.reReadErr != nil {
		return nil, s.reReadErr
	}
	return s.reRead, nil
}
func (s *stubCredStore) UpdateAfterLogin(context.Context, int64, []byte, int64, time.Time) (bool, error) {
	if s.gateErr != nil {
		return false, s.gateErr
	}
	if s.gateLost {
		return false, nil
	}
	s.updated = true
	return true, nil
}
func (s *stubCredStore) Rename(context.Context, int64, int64, string) error { return nil }
func (s *stubCredStore) Delete(context.Context, int64, int64) error         { return nil }
func (s *stubCredStore) DeleteAllByUserID(_ context.Context, userID int64) (int, error) {
	s.revokedUser = userID
	return s.revokeN, nil
}
func (s *stubCredStore) CountByUserIDs(_ context.Context, userIDs []int64) (map[int64]int, error) {
	return map[int64]int{}, nil
}

// RevokeAll is the admin break-glass that drops every passkey on an account; it
// must target the requested user and surface the deleted count unchanged.
func TestRevokeAll(t *testing.T) {
	cs := &stubCredStore{revokeN: 3}
	svc := New(Deps{Creds: cs, Settings: stubSettings{}})
	n, err := svc.RevokeAll(context.Background(), 42)
	if err != nil {
		t.Fatal(err)
	}
	if n != 3 {
		t.Fatalf("RevokeAll returned %d, want 3", n)
	}
	if cs.revokedUser != 42 {
		t.Fatalf("RevokeAll must delete the target user's creds, got user %d", cs.revokedUser)
	}
}

// A cloned/replayed authenticator (sign-count regression) is flagged by
// go-webauthn via CloneWarning, NOT an error — finalizeAssertion must refuse the
// login and must not advance the stored count.
func TestFinalizeAssertion_RejectsClone(t *testing.T) {
	cs := &stubCredStore{}
	svc := New(Deps{Creds: cs, Settings: stubSettings{}})
	cred := &webauthn.Credential{}
	cred.Authenticator.CloneWarning = true
	err := svc.finalizeAssertion(context.Background(), &domain.PasskeyCredential{ID: 1}, cred)
	if !errors.Is(err, domain.ErrUnauthorized) {
		t.Fatalf("a cloned authenticator must be rejected with ErrUnauthorized, got %v", err)
	}
	if cs.updated {
		t.Fatal("a clone must not advance the stored sign count")
	}
}

// A clean login (no CloneWarning) persists the advanced sign count.
func TestFinalizeAssertion_PersistsCleanLogin(t *testing.T) {
	cs := &stubCredStore{}
	svc := New(Deps{Creds: cs, Settings: stubSettings{}})
	cred := &webauthn.Credential{}
	cred.Authenticator.SignCount = 7
	if err := svc.finalizeAssertion(context.Background(), &domain.PasskeyCredential{ID: 1}, cred); err != nil {
		t.Fatal(err)
	}
	if !cs.updated {
		t.Fatal("a clean login must advance the stored sign count")
	}
}

// --- the lost write gate -----------------------------------------------------
//
// UpdateAfterLogin refuses the write when the stored count is already at or above
// the presented one. That happens for two reasons that mean opposite things — a
// concurrent login that got there first, and a revocation between verification
// and the write-back — and the gate cannot tell them apart. So the service looks.

func verifiedCredential(userID int64) *domain.PasskeyCredential {
	return &domain.PasskeyCredential{ID: 1, UserID: userID, CredentialID: "cmF3LWNyZWQtaWQ"}
}

func assertionWithCount(n uint32) *webauthn.Credential {
	cred := &webauthn.Credential{}
	cred.ID = []byte("raw-cred-id")
	cred.Authenticator.SignCount = n
	return cred
}

// A credential that disappeared during the ceremony must fail the login. This is
// the case the discarded bool used to swallow.
func TestFinalizeAssertion_RevokedCredentialIsRefused(t *testing.T) {
	cs := &stubCredStore{gateLost: true, reReadErr: domain.ErrNotFound}
	svc := New(Deps{Creds: cs, Settings: stubSettings{}})

	err := svc.finalizeAssertion(context.Background(), verifiedCredential(7), assertionWithCount(9))
	if !errors.Is(err, domain.ErrUnauthorized) {
		t.Fatalf("a revoked credential must refuse the login, got %v", err)
	}
	if cs.reReadHits == 0 {
		t.Fatal("the lost gate was not investigated")
	}
}

// A concurrent login that already stored at least this count is exactly what the
// gate is for; the login continues.
func TestFinalizeAssertion_BenignConcurrentAdvanceContinues(t *testing.T) {
	cs := &stubCredStore{gateLost: true, reRead: &domain.PasskeyCredential{
		ID: 1, UserID: 7, CredentialID: "cmF3LWNyZWQtaWQ", SignCount: 9,
	}}
	svc := New(Deps{Creds: cs, Settings: stubSettings{}})

	if err := svc.finalizeAssertion(context.Background(), verifiedCredential(7), assertionWithCount(9)); err != nil {
		t.Fatalf("a benign concurrent advance must not fail the login: %v", err)
	}
}

// The gate would have matched a lower stored count, so its refusal came from
// something else and must not be read as a race.
func TestFinalizeAssertion_CountBelowOursIsRefused(t *testing.T) {
	cs := &stubCredStore{gateLost: true, reRead: &domain.PasskeyCredential{
		ID: 1, UserID: 7, CredentialID: "cmF3LWNyZWQtaWQ", SignCount: 3,
	}}
	svc := New(Deps{Creds: cs, Settings: stubSettings{}})

	if err := svc.finalizeAssertion(context.Background(), verifiedCredential(7), assertionWithCount(9)); !errors.Is(err, domain.ErrUnauthorized) {
		t.Fatalf("a stored count behind the assertion must refuse, got %v", err)
	}
}

// The row under that credential ID is no longer the one that was verified, so the
// assertion must not be honoured against it.
func TestFinalizeAssertion_OwnershipChangedIsRefused(t *testing.T) {
	cases := map[string]*domain.PasskeyCredential{
		"a different row id":  {ID: 2, UserID: 7, CredentialID: "cmF3LWNyZWQtaWQ", SignCount: 9},
		"a different account": {ID: 1, UserID: 8, CredentialID: "cmF3LWNyZWQtaWQ", SignCount: 9},
		"no row at all":       nil,
	}
	for name, row := range cases {
		t.Run(name, func(t *testing.T) {
			cs := &stubCredStore{gateLost: true, reRead: row}
			svc := New(Deps{Creds: cs, Settings: stubSettings{}})
			if err := svc.finalizeAssertion(context.Background(), verifiedCredential(7), assertionWithCount(9)); !errors.Is(err, domain.ErrUnauthorized) {
				t.Fatalf("%s must refuse, got %v", name, err)
			}
		})
	}
}

// An unreadable credential is an infrastructure failure. It must refuse the
// login, and must not be reported as a refusal of trust.
func TestFinalizeAssertion_ReReadFailureIsNotAnAuthRefusal(t *testing.T) {
	cs := &stubCredStore{gateLost: true, reReadErr: errors.New("db down")}
	svc := New(Deps{Creds: cs, Settings: stubSettings{}})

	err := svc.finalizeAssertion(context.Background(), verifiedCredential(7), assertionWithCount(9))
	if err == nil {
		t.Fatal("an unreadable credential admitted the login")
	}
	if errors.Is(err, domain.ErrUnauthorized) {
		t.Fatalf("an infrastructure failure was reported as a trust refusal: %v", err)
	}
}

// The write-back's own error still refuses, as it always did.
func TestFinalizeAssertion_WriteErrorIsRefused(t *testing.T) {
	cs := &stubCredStore{gateErr: errors.New("db down")}
	svc := New(Deps{Creds: cs, Settings: stubSettings{}})

	if err := svc.finalizeAssertion(context.Background(), verifiedCredential(7), assertionWithCount(9)); err == nil {
		t.Fatal("a failed write-back admitted the login")
	}
}

func TestAvailableAndPasswordless(t *testing.T) {
	svc := func(enabled, passwordless bool) *Service {
		return New(Deps{Settings: stubSettings{ports.UISettings{PasskeyEnabled: enabled, PasskeyPasswordless: passwordless}}})
	}
	ctx := context.Background()
	if svc(false, false).Available(ctx) {
		t.Fatal("Available must be false when passkey_enabled is off")
	}
	if !svc(true, false).Available(ctx) {
		t.Fatal("Available must be true when passkey_enabled is on")
	}
	if svc(true, false).Passwordless(ctx) {
		t.Fatal("Passwordless requires the passwordless toggle, not just the master switch")
	}
	if svc(false, true).Passwordless(ctx) {
		t.Fatal("Passwordless requires the master switch too")
	}
	if !svc(true, true).Passwordless(ctx) {
		t.Fatal("Passwordless must be true when both toggles are on")
	}
}
