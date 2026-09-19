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

const testRPDigest = "digest-a"

func challengeReq(purpose challengePurpose, userID int64) challengeRequest {
	return challengeRequest{purpose: purpose, userID: userID, rpDigest: testRPDigest}
}

func mustPut(t *testing.T, st *sessionStore, challenge string, req challengeRequest) string {
	t.Helper()
	id, err := st.put(&webauthn.SessionData{Challenge: challenge}, req)
	if err != nil {
		t.Fatalf("put: %v", err)
	}
	return id
}

func TestSessionStore_SingleUse(t *testing.T) {
	st := newSessionStore(time.Now)
	id := mustPut(t, st, "abc", challengeReq(purposeRegistration, 7))

	got, err := st.take(id, challengeReq(purposeRegistration, 7))
	if err != nil || got == nil || got.Challenge != "abc" {
		t.Fatalf("first take = (%v, %v), want the session", got, err)
	}
	if _, err := st.take(id, challengeReq(purposeRegistration, 7)); !errors.Is(err, ErrChallenge) {
		t.Fatal("a consumed challenge must not be takeable again (replay guard)")
	}
	if _, err := st.take("nope", challengeReq(purposeRegistration, 7)); !errors.Is(err, ErrChallenge) {
		t.Fatal("an unknown id must be refused")
	}
	if _, err := st.take("", challengeReq(purposeRegistration, 7)); !errors.Is(err, ErrChallenge) {
		t.Fatal("an empty id must be refused")
	}
}

func TestSessionStore_Expiry(t *testing.T) {
	now := time.Now()
	clock := now
	st := newSessionStore(func() time.Time { return clock })
	id := mustPut(t, st, "x", challengeReq(purposeRegistration, 7))

	clock = now.Add(sessionTTL + time.Second)
	if _, err := st.take(id, challengeReq(purposeRegistration, 7)); !errors.Is(err, ErrChallenge) {
		t.Fatal("an expired challenge must not be returned")
	}
}

// A challenge is only usable for the ceremony it was issued for. The purposes
// differ in what the caller may do afterwards — enrollment creates a credential,
// step-up authorizes a sensitive change — so a challenge that leaked across them
// would let one ceremony be spent as another.
func TestSessionStore_PurposeIsolation(t *testing.T) {
	purposes := []challengePurpose{
		purposeRegistration, purposeDiscoverableLogin, purposeLoginSecondFactor, purposeStepUp,
	}
	for _, issued := range purposes {
		for _, presented := range purposes {
			if issued == presented {
				continue
			}
			t.Run(string(issued)+"_as_"+string(presented), func(t *testing.T) {
				st := newSessionStore(time.Now)
				// userID 0 for the discoverable case on both sides, so the purpose
				// is the only thing that could refuse this.
				id := mustPut(t, st, "x", challengeReq(issued, 0))

				_, err := st.take(id, challengeReq(presented, 0))
				if !errors.Is(err, ErrChallenge) {
					t.Fatalf("a %s challenge was accepted as %s", issued, presented)
				}
			})
		}
	}
}

// A wrong-purpose attempt must consume the challenge too, or an attacker could
// probe purposes against one stolen challenge until one fits.
func TestSessionStore_PurposeMismatchConsumesTheChallenge(t *testing.T) {
	st := newSessionStore(time.Now)
	id := mustPut(t, st, "x", challengeReq(purposeStepUp, 7))

	if _, err := st.take(id, challengeReq(purposeLoginSecondFactor, 7)); !errors.Is(err, ErrChallenge) {
		t.Fatal("a mismatched purpose must be refused")
	}
	if _, err := st.take(id, challengeReq(purposeStepUp, 7)); !errors.Is(err, ErrChallenge) {
		t.Fatal("the challenge survived a failed attempt and could be tried again")
	}
}

// An allow-listed ceremony is bound to the account it was issued for, so user A's
// challenge cannot be completed as user B even if B can reach the session id.
func TestSessionStore_AccountBinding(t *testing.T) {
	st := newSessionStore(time.Now)
	id := mustPut(t, st, "x", challengeReq(purposeLoginSecondFactor, 7))

	if _, err := st.take(id, challengeReq(purposeLoginSecondFactor, 8)); !errors.Is(err, ErrChallenge) {
		t.Fatal("a challenge issued for one account was accepted for another")
	}
}

// Changing the relying-party parameters makes an outstanding challenge
// meaningless, so it is refused rather than answered against the new RP.
func TestSessionStore_RPBinding(t *testing.T) {
	st := newSessionStore(time.Now)
	id := mustPut(t, st, "x", challengeReq(purposeRegistration, 7))

	other := challengeRequest{purpose: purposeRegistration, userID: 7, rpDigest: "digest-b"}
	if _, err := st.take(id, other); !errors.Is(err, ErrChallenge) {
		t.Fatal("a challenge issued under one relying-party configuration was accepted under another")
	}
}

// The full store REFUSES rather than evicting: discarding a live challenge would
// fail a real user's ceremony to make room for an attacker's, which is the wrong
// direction. The entries already there must survive the refusal.
func TestSessionStore_FullStoreRefusesWithoutEvicting(t *testing.T) {
	st := newSessionStoreWithCapacity(time.Now, 3)
	ids := make([]string, 0, 3)
	for i := 0; i < 3; i++ {
		ids = append(ids, mustPut(t, st, "x", challengeReq(purposeRegistration, 7)))
	}

	_, err := st.put(&webauthn.SessionData{Challenge: "y"}, challengeReq(purposeRegistration, 7))
	if !errors.Is(err, domain.ErrResourceExhausted) {
		t.Fatalf("a full store = %v, want ErrResourceExhausted", err)
	}
	for i, id := range ids {
		if _, err := st.take(id, challengeReq(purposeRegistration, 7)); err != nil {
			t.Fatalf("entry %d was evicted to make room: %v", i, err)
		}
	}
}

// Expired entries are the one thing that may go without its owner's involvement,
// so they are reclaimed first and the store fills up again afterwards.
func TestSessionStore_ExpiredEntriesAreReclaimedFirst(t *testing.T) {
	now := time.Now()
	clock := now
	st := newSessionStoreWithCapacity(func() time.Time { return clock }, 2)
	first := mustPut(t, st, "a", challengeReq(purposeRegistration, 7))
	mustPut(t, st, "b", challengeReq(purposeRegistration, 7))

	clock = now.Add(sessionTTL + time.Second)
	id, err := st.put(&webauthn.SessionData{Challenge: "c"}, challengeReq(purposeRegistration, 7))
	if err != nil {
		t.Fatalf("put after expiry: %v", err)
	}
	if _, err := st.take(first, challengeReq(purposeRegistration, 7)); !errors.Is(err, ErrChallenge) {
		t.Fatal("the expired entry was not reclaimed")
	}
	if _, err := st.take(id, challengeReq(purposeRegistration, 7)); err != nil {
		t.Fatalf("the newly stored entry is not usable: %v", err)
	}
}

// --- relying-party digest ----------------------------------------------------

// mutableSettings lets a case change the settings between two calls, which is how
// a configuration change is simulated without a real ceremony.
type mutableSettings struct{ s ports.UISettings }

func (m *mutableSettings) Load(context.Context, ports.UISettings) (ports.UISettings, error) {
	return m.s, nil
}

func (m *mutableSettings) LoadForUser(context.Context, *domain.User, ports.UISettings) (ports.UISettings, error) {
	return m.s, nil
}

// A challenge is bound to the RP id and origin, and to nothing else. Renaming the
// panel is branding and must not invalidate challenges people are part-way
// through; moving the panel to another origin must.
func TestRPParams_DigestCoversTheOriginAndNotTheBranding(t *testing.T) {
	settings := &mutableSettings{s: ports.UISettings{
		SubBaseURL: "https://panel.example.com", SiteTitle: "Passwall",
	}}
	svc := New(Deps{Settings: settings})
	ctx := context.Background()

	_, first, err := svc.newWebAuthn(ctx)
	if err != nil {
		t.Fatalf("newWebAuthn: %v", err)
	}

	// Same origin, new brand: the digest must not move.
	settings.s.SiteTitle = "Something Else"
	_, rebranded, err := svc.newWebAuthn(ctx)
	if err != nil {
		t.Fatalf("newWebAuthn: %v", err)
	}
	if rebranded.digest != first.digest {
		t.Fatal("renaming the panel invalidated outstanding challenges")
	}

	// Different origin: the digest must move.
	settings.s.SubBaseURL = "https://other.example.com"
	_, moved, err := svc.newWebAuthn(ctx)
	if err != nil {
		t.Fatalf("newWebAuthn: %v", err)
	}
	if moved.digest == first.digest {
		t.Fatal("moving the panel to another origin left outstanding challenges valid")
	}
}
