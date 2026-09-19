package passkey

import (
	"context"
	"encoding/binary"
	"errors"
	"testing"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
)

// handleFor is the user handle the panel uses: the account id, big-endian.
func handleFor(userID int64) []byte {
	b := make([]byte, 8)
	binary.BigEndian.PutUint64(b, uint64(userID))
	return b
}

// The whole flow, against a real signature: enroll, then log in without a
// username. If the harness or the stored credential format is wrong, this is
// where it shows.
func TestCeremony_EnrollThenLogInWithoutAUsername(t *testing.T) {
	svc, creds, _ := ceremonyService(t)
	auth := newSoftwareAuthenticator(t)
	ctx := context.Background()

	enroll(t, svc, auth, 1)
	if rows := creds.snapshot(); len(rows) != 1 || rows[0].UserID != 1 {
		t.Fatalf("registration did not store the credential: %+v", rows)
	}

	options, sessionID, err := svc.BeginLogin(ctx)
	if err != nil {
		t.Fatalf("BeginLogin: %v", err)
	}
	u, err := svc.FinishLogin(ctx, sessionID, requestFor(t, auth.assert(t, ceremonyRPID, ceremonyOrigin, options, handleFor(1))))
	if err != nil {
		t.Fatalf("FinishLogin: %v", err)
	}
	if u.ID != 1 {
		t.Fatalf("resolved user %d, want 1", u.ID)
	}
}

// W01 in the form that matters here: the stored record is a self-contained
// webauthn.Credential, so a credential registered by an earlier version is still
// usable after the change — no re-registration, and nothing in the record has to
// be rewritten. The login below runs against a snapshot of the row taken before
// the login begins, standing in for a row read from the database.
func TestCeremony_BaselineCredentialLogsInWithoutReRegistration(t *testing.T) {
	svc, creds, _ := ceremonyService(t)
	auth := newSoftwareAuthenticator(t)
	ctx := context.Background()

	enroll(t, svc, auth, 1)
	baseline := creds.snapshot()
	if len(baseline) != 1 {
		t.Fatalf("expected one stored credential, got %d", len(baseline))
	}

	// A fresh service over the same row: nothing but the record carries the
	// credential forward.
	fresh := &ceremonyCredStore{rows: []*domain.PasskeyCredential{&baseline[0]}, nextID: 1}
	svc2 := New(Deps{Creds: fresh, Users: newStubUsers(1), Settings: svc.d.Settings})

	options, sessionID, err := svc2.BeginLogin(ctx)
	if err != nil {
		t.Fatalf("BeginLogin: %v", err)
	}
	if _, err := svc2.FinishLogin(ctx, sessionID, requestFor(t, auth.assert(t, ceremonyRPID, ceremonyOrigin, options, handleFor(1)))); err != nil {
		t.Fatalf("a credential stored by the previous version no longer logs in: %v", err)
	}
}

// W02: the same credential cannot be registered onto another account. The
// unique index on credential_id is what enforces it, and the store mirrors that.
func TestCeremony_OneCredentialCannotBeRegisteredTwice(t *testing.T) {
	svc, _, _ := ceremonyService(t)
	auth := newSoftwareAuthenticator(t)
	ctx := context.Background()

	enroll(t, svc, auth, 1)

	options, sessionID, err := svc.BeginRegistration(ctx, 2)
	if err != nil {
		t.Fatalf("BeginRegistration: %v", err)
	}
	_, err = svc.FinishRegistration(ctx, 2, sessionID, "stolen", requestFor(t, auth.create(t, ceremonyRPID, ceremonyOrigin, options)))
	if err == nil {
		t.Fatal("the same credential was registered onto a second account")
	}
	if !errors.Is(err, domain.ErrAlreadyExists) {
		t.Fatalf("duplicate registration = %v, want ErrAlreadyExists", err)
	}
}

// W06: the two allow-listed ceremonies cannot be swapped, because each binds its
// own purpose to the challenge.
func TestCeremony_PurposesCannotBeSwapped(t *testing.T) {
	svc, _, _ := ceremonyService(t)
	auth := newSoftwareAuthenticator(t)
	ctx := context.Background()
	enroll(t, svc, auth, 1)

	options, sessionID, err := svc.BeginLoginForUser(ctx, 1, PurposeSecondFactor)
	if err != nil {
		t.Fatalf("BeginLoginForUser: %v", err)
	}
	body := requestFor(t, auth.assert(t, ceremonyRPID, ceremonyOrigin, options, handleFor(1)))

	if err := svc.FinishLoginForUser(ctx, 1, PurposeStepUp, sessionID, body); !errors.Is(err, domain.ErrValidation) {
		t.Fatalf("a second-factor challenge was accepted as step-up: %v", err)
	}
	// And the attempt consumed it, so the second factor cannot simply be retried.
	if err := svc.FinishLoginForUser(ctx, 1, PurposeSecondFactor, sessionID, requestFor(t, auth.assert(t, ceremonyRPID, ceremonyOrigin, options, handleFor(1)))); err == nil {
		t.Fatal("the challenge survived a wrong-purpose attempt")
	}
}

// W06: an unset or unknown purpose is refused rather than defaulted.
func TestCeremony_UnknownPurposeIsRefused(t *testing.T) {
	svc, _, _ := ceremonyService(t)
	ctx := context.Background()

	if _, _, err := svc.BeginLoginForUser(ctx, 1, LoginPurpose("")); !errors.Is(err, domain.ErrValidation) {
		t.Fatalf("an unset purpose = %v, want ErrValidation", err)
	}
	if err := svc.FinishLoginForUser(ctx, 1, LoginPurpose("nonsense"), "any", nil); !errors.Is(err, domain.ErrValidation) {
		t.Fatalf("an unknown purpose = %v, want ErrValidation", err)
	}
}

// W03: a challenge begun for one account cannot be completed as another, even
// with a valid assertion for the second account's own credential.
func TestCeremony_ChallengeIsBoundToTheAccountThatBeganIt(t *testing.T) {
	svc, _, _ := ceremonyService(t)
	one, two := newSoftwareAuthenticator(t), newSoftwareAuthenticator(t)
	ctx := context.Background()
	enroll(t, svc, one, 1)
	enroll(t, svc, two, 2)

	options, sessionID, err := svc.BeginLoginForUser(ctx, 1, PurposeSecondFactor)
	if err != nil {
		t.Fatalf("BeginLoginForUser: %v", err)
	}
	// A valid assertion from a DIFFERENT account's authenticator.
	body := requestFor(t, two.assert(t, ceremonyRPID, ceremonyOrigin, options, handleFor(2)))

	if err := svc.FinishLoginForUser(ctx, 1, PurposeSecondFactor, sessionID, body); err == nil {
		t.Fatal("another account's assertion completed the challenge")
	}
}

// W05: the passwordless path is single-factor, so it requires user verification.
// An authenticator that reports UP without UV must be refused there — and the
// same authenticator must still work as a second factor, where UV is not demanded.
func TestCeremony_UsernamelessLoginRequiresUserVerification(t *testing.T) {
	svc, _, _ := ceremonyService(t)
	auth := newSoftwareAuthenticator(t)
	ctx := context.Background()
	enroll(t, svc, auth, 1)

	// Possession only: UP set, UV clear.
	auth.userVerified = false

	options, sessionID, err := svc.BeginLogin(ctx)
	if err != nil {
		t.Fatalf("BeginLogin: %v", err)
	}
	if _, err := svc.FinishLogin(ctx, sessionID, requestFor(t, auth.assert(t, ceremonyRPID, ceremonyOrigin, options, handleFor(1)))); err == nil {
		t.Fatal("a possession-only assertion satisfied the single-factor passwordless login")
	}

	// As a second factor the same authenticator is acceptable.
	options, sessionID, err = svc.BeginLoginForUser(ctx, 1, PurposeSecondFactor)
	if err != nil {
		t.Fatalf("BeginLoginForUser: %v", err)
	}
	if err := svc.FinishLoginForUser(ctx, 1, PurposeSecondFactor, sessionID, requestFor(t, auth.assert(t, ceremonyRPID, ceremonyOrigin, options, handleFor(1)))); err != nil {
		t.Fatalf("a possession-only assertion was refused as a second factor: %v", err)
	}
}

// W08: a counter that did not advance is a clone warning, and the login is
// refused with the stored record left exactly as it was. Recovering the "before"
// image and comparing it byte for byte is the assertion; a partial write would
// show up here.
func TestCeremony_CounterRegressionIsRefusedAndWritesNothing(t *testing.T) {
	svc, creds, _ := ceremonyService(t)
	auth := newSoftwareAuthenticator(t)
	ctx := context.Background()

	auth.signCount = 7
	enroll(t, svc, auth, 1)
	before := creds.snapshot()
	if len(before) != 1 || before[0].SignCount != 7 {
		t.Fatalf("registration did not record the counter: %+v", before)
	}

	// A second copy of the key reports a counter that did not advance.
	auth.signCount = 7
	options, sessionID, err := svc.BeginLogin(ctx)
	if err != nil {
		t.Fatalf("BeginLogin: %v", err)
	}
	if _, err := svc.FinishLogin(ctx, sessionID, requestFor(t, auth.assert(t, ceremonyRPID, ceremonyOrigin, options, handleFor(1)))); err == nil {
		t.Fatal("a counter that did not advance was accepted")
	}

	after := creds.snapshot()
	if len(after) != 1 {
		t.Fatalf("the refused login changed the row count: %d", len(after))
	}
	if after[0].SignCount != before[0].SignCount {
		t.Fatalf("the refused login advanced the counter: %d then %d", before[0].SignCount, after[0].SignCount)
	}
	if after[0].LastUsedAt != nil {
		t.Fatal("the refused login touched last_used_at")
	}
	if string(after[0].Credential) != string(before[0].Credential) {
		t.Fatal("the refused login rewrote the credential record")
	}
}

// W09: a counter that genuinely advances is accepted and stored.
func TestCeremony_AdvancingCounterIsAcceptedAndStored(t *testing.T) {
	svc, creds, _ := ceremonyService(t)
	auth := newSoftwareAuthenticator(t)
	ctx := context.Background()

	auth.signCount = 7
	enroll(t, svc, auth, 1)

	auth.signCount = 8
	options, sessionID, err := svc.BeginLogin(ctx)
	if err != nil {
		t.Fatalf("BeginLogin: %v", err)
	}
	if _, err := svc.FinishLogin(ctx, sessionID, requestFor(t, auth.assert(t, ceremonyRPID, ceremonyOrigin, options, handleFor(1)))); err != nil {
		t.Fatalf("FinishLogin: %v", err)
	}
	if rows := creds.snapshot(); rows[0].SignCount != 8 {
		t.Fatalf("stored counter = %d, want 8", rows[0].SignCount)
	}
}

// A revoked credential cannot log in even with a perfectly valid assertion.
func TestCeremony_RevokedCredentialCannotLogIn(t *testing.T) {
	svc, _, _ := ceremonyService(t)
	auth := newSoftwareAuthenticator(t)
	ctx := context.Background()
	enroll(t, svc, auth, 1)

	if _, err := svc.RevokeAll(ctx, 1); err != nil {
		t.Fatalf("RevokeAll: %v", err)
	}

	options, sessionID, err := svc.BeginLogin(ctx)
	if err != nil {
		// BeginDiscoverableLogin has no allow-list to check against, so it may
		// still succeed; the refusal then has to come from Finish.
		t.Fatalf("BeginLogin: %v", err)
	}
	if _, err := svc.FinishLogin(ctx, sessionID, requestFor(t, auth.assert(t, ceremonyRPID, ceremonyOrigin, options, handleFor(1)))); err == nil {
		t.Fatal("a revoked credential logged in")
	}
}

// Negative controls for the harness itself. Without these, every case above
// would still pass if go-webauthn were not actually verifying what the
// authenticator sent — the assertions would be decoration.
func TestCeremony_HarnessActuallyVerifiesTheCryptography(t *testing.T) {
	svc, _, _ := ceremonyService(t)
	auth := newSoftwareAuthenticator(t)
	ctx := context.Background()
	enroll(t, svc, auth, 1)

	t.Run("a signature from another key is refused", func(t *testing.T) {
		options, sessionID, err := svc.BeginLogin(ctx)
		if err != nil {
			t.Fatalf("BeginLogin: %v", err)
		}
		impostor := newSoftwareAuthenticator(t)
		// Same credential id, so the lookup finds the registered row — but the
		// public key the panel stored is the real authenticator's.
		impostor.credID = auth.credID
		body := impostor.assert(t, ceremonyRPID, ceremonyOrigin, options, handleFor(1))
		if _, err := svc.FinishLogin(ctx, sessionID, requestFor(t, body)); err == nil {
			t.Fatal("an assertion signed by the wrong key was accepted")
		}
	})

	t.Run("an assertion for another origin is refused", func(t *testing.T) {
		options, sessionID, err := svc.BeginLogin(ctx)
		if err != nil {
			t.Fatalf("BeginLogin: %v", err)
		}
		body := auth.assert(t, ceremonyRPID, "https://evil.example.com", options, handleFor(1))
		if _, err := svc.FinishLogin(ctx, sessionID, requestFor(t, body)); err == nil {
			t.Fatal("an assertion collected at another origin was accepted")
		}
	})

	t.Run("an assertion for another relying-party id is refused", func(t *testing.T) {
		options, sessionID, err := svc.BeginLogin(ctx)
		if err != nil {
			t.Fatalf("BeginLogin: %v", err)
		}
		body := auth.assert(t, "evil.example.com", ceremonyOrigin, options, handleFor(1))
		if _, err := svc.FinishLogin(ctx, sessionID, requestFor(t, body)); err == nil {
			t.Fatal("an assertion hashed against another RP id was accepted")
		}
	})
}
