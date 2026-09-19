package passkey

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/go-webauthn/webauthn/webauthn"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
)

// challengePurpose names what a ceremony's challenge was issued for.
//
// The SERVER decides it at the call site and it is checked on the way back, which
// is what stops a challenge issued for one ceremony being answered against
// another. Without it an enrollment challenge could be presented to a login
// endpoint, or a step-up challenge to a full login, and those allow different
// things about the account.
type challengePurpose string

const (
	purposeRegistration      challengePurpose = "registration"
	purposeDiscoverableLogin challengePurpose = "discoverable_login"
	purposeLoginSecondFactor challengePurpose = "login_second_factor"
	purposeStepUp            challengePurpose = "step_up"
)

const (
	sessionTTL = 5 * time.Minute

	// sessionCapacity is a hard cap, not an eviction threshold. When the store is
	// full and nothing has expired, Put REFUSES rather than discarding a challenge
	// someone is part-way through: dropping a real user's ceremony to admit an
	// attacker's is the wrong direction (ADR 0036 §7.4).
	sessionCapacity = 10_000
)

// ErrChallenge is the class for every way a stored challenge can fail to be used.
// The reasons are not distinguished to the browser — an expired challenge and one
// issued for another purpose are equally useless to a client, which can influence
// neither — but they are distinguished in the error's text for a log line.
var ErrChallenge = errors.New("passkey: challenge is not usable")

// challengeRequest is what a caller knows about a challenge: everything it must
// agree on for the challenge to be usable.
type challengeRequest struct {
	purpose challengePurpose
	// userID is the account the ceremony is for, or 0 for a discoverable login,
	// where the identity is not known until the credential is resolved.
	userID int64
	// rpDigest covers the relying-party parameters in force. A change to them
	// makes an outstanding challenge meaningless, so it is checked too.
	rpDigest string
}

type challenge struct {
	data     *webauthn.SessionData
	purpose  challengePurpose
	userID   int64
	rpDigest string
	expires  time.Time
}

// sessionStore holds the per-ceremony *webauthn.SessionData (the challenge and
// its parameters) between the Begin and Finish steps. It is SINGLE-USE: Take
// removes the entry unconditionally, whether the ceremony that follows succeeds
// or fails, so a challenge can never be replayed and a rejected guess cannot be
// retried against the same one.
type sessionStore struct {
	mu       sync.Mutex
	m        map[string]challenge
	now      func() time.Time
	capacity int
}

func newSessionStore(now func() time.Time) *sessionStore {
	return newSessionStoreWithCapacity(now, sessionCapacity)
}

// newSessionStoreWithCapacity exists so a test can exercise the full-store
// behaviour without inserting ten thousand entries; production always uses the
// package constant through newSessionStore.
func newSessionStoreWithCapacity(now func() time.Time, capacity int) *sessionStore {
	if now == nil {
		now = time.Now
	}
	return &sessionStore{m: make(map[string]challenge), now: now, capacity: capacity}
}

// put stores the session data under a fresh random id and returns that id.
//
// Expired entries are reclaimed first — they are the only entries that may go
// without their owner's involvement — and a store that is still full refuses.
// It never evicts a live entry.
func (s *sessionStore) put(data *webauthn.SessionData, req challengeRequest) (string, error) {
	idBytes := make([]byte, 32)
	if _, err := rand.Read(idBytes); err != nil {
		return "", err
	}
	id := base64.RawURLEncoding.EncodeToString(idBytes)

	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	for k, c := range s.m {
		if !c.expires.After(now) {
			delete(s.m, k)
		}
	}
	if len(s.m) >= s.capacity {
		return "", fmt.Errorf("%w: the challenge store is full (%d outstanding)", domain.ErrResourceExhausted, len(s.m))
	}
	s.m[id] = challenge{
		data:     data,
		purpose:  req.purpose,
		userID:   req.userID,
		rpDigest: req.rpDigest,
		expires:  now.Add(sessionTTL),
	}
	return id, nil
}

// take removes the challenge and returns its session data, or an error saying why
// it could not be used.
//
// The removal happens BEFORE the checks, deliberately: every Finish attempt
// consumes its challenge, including one that fails on purpose, expiry or binding.
// That is what makes a failed guess untryable against the same challenge, and it
// is why the caller cannot be handed a session it should not have.
func (s *sessionStore) take(id string, req challengeRequest) (*webauthn.SessionData, error) {
	if id == "" {
		return nil, fmt.Errorf("%w: no challenge id", ErrChallenge)
	}
	s.mu.Lock()
	c, ok := s.m[id]
	delete(s.m, id)
	s.mu.Unlock()

	if !ok {
		return nil, fmt.Errorf("%w: unknown or already used", ErrChallenge)
	}
	if !c.expires.After(s.now()) {
		return nil, fmt.Errorf("%w: expired", ErrChallenge)
	}
	if c.purpose != req.purpose {
		return nil, fmt.Errorf("%w: issued for %s, presented to %s", ErrChallenge, c.purpose, req.purpose)
	}
	if c.userID != req.userID {
		return nil, fmt.Errorf("%w: issued for another account", ErrChallenge)
	}
	if c.rpDigest != req.rpDigest {
		return nil, fmt.Errorf("%w: the relying-party configuration changed", ErrChallenge)
	}
	return c.data, nil
}

// rpDigestOf digests the relying-party parameters a challenge is bound to: the RP
// id and the allowed origin, the two things WebAuthn verifies a credential
// against.
//
// The display name is deliberately absent. It is branding, and renaming the panel
// must not invalidate challenges people are part-way through (ADR 0036 §7.4).
func rpDigestOf(rpID, origin string) string {
	sum := sha256.Sum256([]byte(rpID + "\x00" + origin))
	return hex.EncodeToString(sum[:])
}
