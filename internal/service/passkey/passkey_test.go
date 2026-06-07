package passkey

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/go-webauthn/webauthn/webauthn"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

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

type stubCredStore struct {
	updated     bool
	revokedUser int64
	revokeN     int
}

func (s *stubCredStore) Save(context.Context, *domain.PasskeyCredential) error { return nil }
func (s *stubCredStore) FindByUserID(context.Context, int64) ([]*domain.PasskeyCredential, error) {
	return nil, nil
}
func (s *stubCredStore) FindByCredentialID(context.Context, string) (*domain.PasskeyCredential, error) {
	return nil, nil
}
func (s *stubCredStore) UpdateAfterLogin(context.Context, int64, []byte, int64, time.Time) (bool, error) {
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
