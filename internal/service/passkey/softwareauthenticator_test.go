package passkey

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/fxamacker/cbor/v2"
	"github.com/go-webauthn/webauthn/protocol"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

// softwareAuthenticator is a WebAuthn authenticator implemented in the test.
//
// It exists so the ceremony cases exercise a REAL signature over real client data
// — go-webauthn verifies the ES256 signature, the RP id hash, the origin, the
// challenge and the flags — rather than a mocked Finish that returns success.
//
// It also exists because the cases that matter here are about what an
// authenticator REPORTS: a counter that did not advance, a counter that is always
// zero, a ceremony performed without user verification. Those are fields on this
// type, set directly by the case.
type softwareAuthenticator struct {
	key    *ecdsa.PrivateKey
	credID []byte
	aaguid []byte

	// signCount is what the next ceremony reports. Cases move it backwards to
	// produce the counter regression go-webauthn calls a clone warning.
	signCount uint32
	// userVerified and userPresent are the UV and UP flags. go-webauthn enforces
	// UV when the RP required it, which is what makes the passwordless path's
	// requirement testable rather than assumed.
	userVerified bool
	userPresent  bool
}

func newSoftwareAuthenticator(t *testing.T) *softwareAuthenticator {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate authenticator key: %v", err)
	}
	credID := make([]byte, 32)
	if _, err := rand.Read(credID); err != nil {
		t.Fatalf("generate credential id: %v", err)
	}
	return &softwareAuthenticator{
		key:          key,
		credID:       credID,
		aaguid:       make([]byte, 16),
		userPresent:  true,
		userVerified: true,
	}
}

// credentialID is the base64url raw id the panel indexes credentials by.
func (a *softwareAuthenticator) credentialID() string {
	return base64.RawURLEncoding.EncodeToString(a.credID)
}

func clientDataJSON(t *testing.T, ceremony, challenge, origin string) []byte {
	t.Helper()
	b, err := json.Marshal(map[string]any{
		"type":        ceremony,
		"challenge":   challenge,
		"origin":      origin,
		"crossOrigin": false,
	})
	if err != nil {
		t.Fatalf("marshal client data: %v", err)
	}
	return b
}

// coseKey is the credential public key in the COSE encoding WebAuthn carries:
// an EC2 key on P-256 with the ES256 algorithm.
func (a *softwareAuthenticator) coseKey(t *testing.T) []byte {
	t.Helper()
	// The uncompressed SEC 1 point, 0x04 || X || Y, 32 bytes each on P-256: the
	// raw X and Y fields are deprecated since Go 1.26.
	point, err := a.key.PublicKey.Bytes()
	if err != nil || len(point) != 65 {
		t.Fatalf("encode public key: %d bytes, %v", len(point), err)
	}
	x, y := point[1:33], point[33:]
	// 1=kty(EC2) 3=alg(ES256) -1=crv(P-256) -2=x -3=y.
	b, err := cbor.Marshal(map[int]any{1: 2, 3: -7, -1: 1, -2: x, -3: y})
	if err != nil {
		t.Fatalf("marshal COSE key: %v", err)
	}
	return b
}

// authenticatorData is the structure both ceremonies carry and sign over. The
// attested credential data is present only for a registration.
func (a *softwareAuthenticator) authenticatorData(t *testing.T, rpID string, attested bool) []byte {
	t.Helper()
	rpHash := sha256.Sum256([]byte(rpID))

	var flags byte
	if a.userPresent {
		flags |= 0x01
	}
	if a.userVerified {
		flags |= 0x04
	}
	if attested {
		flags |= 0x40
	}

	out := make([]byte, 0, 128)
	out = append(out, rpHash[:]...)
	out = append(out, flags)
	var counter [4]byte
	binary.BigEndian.PutUint32(counter[:], a.signCount)
	out = append(out, counter[:]...)

	if attested {
		out = append(out, a.aaguid...)
		var idLen [2]byte
		binary.BigEndian.PutUint16(idLen[:], uint16(len(a.credID)))
		out = append(out, idLen[:]...)
		out = append(out, a.credID...)
		out = append(out, a.coseKey(t)...)
	}
	return out
}

// create produces the JSON body of a registration ceremony.
func (a *softwareAuthenticator) create(t *testing.T, rpID, origin string, options *protocol.PublicKeyCredentialCreationOptions) string {
	t.Helper()
	clientData := clientDataJSON(t, "webauthn.create", options.Challenge.String(), origin)

	type attestationObject struct {
		Format   string         `cbor:"fmt"`
		AuthData []byte         `cbor:"authData"`
		AttStmt  map[string]any `cbor:"attStmt"`
	}
	// "none" attestation: what a platform authenticator with nothing to attest
	// uses, and what the panel accepts.
	obj, err := cbor.Marshal(attestationObject{
		Format:   "none",
		AuthData: a.authenticatorData(t, rpID, true),
		AttStmt:  map[string]any{},
	})
	if err != nil {
		t.Fatalf("marshal attestation object: %v", err)
	}

	body, err := json.Marshal(protocol.CredentialCreationResponse{
		PublicKeyCredential: protocol.PublicKeyCredential{
			Credential: protocol.Credential{
				ID:   a.credentialID(),
				Type: "public-key",
			},
			RawID: a.credID,
		},
		AttestationResponse: protocol.AuthenticatorAttestationResponse{
			AuthenticatorResponse: protocol.AuthenticatorResponse{ClientDataJSON: clientData},
			AttestationObject:     obj,
		},
	})
	if err != nil {
		t.Fatalf("marshal registration response: %v", err)
	}
	return string(body)
}

// assert produces the JSON body of an assertion ceremony.
func (a *softwareAuthenticator) assert(t *testing.T, rpID, origin string, options *protocol.PublicKeyCredentialRequestOptions, userHandle []byte) string {
	t.Helper()
	clientData := clientDataJSON(t, "webauthn.get", options.Challenge.String(), origin)
	authData := a.authenticatorData(t, rpID, false)

	// The signature covers the authenticator data followed by the hash of the
	// client data, and is ASN.1 DER — which is what go-webauthn parses back out.
	clientHash := sha256.Sum256(clientData)
	signed := append(append([]byte{}, authData...), clientHash[:]...)
	digest := sha256.Sum256(signed)
	sig, err := ecdsa.SignASN1(rand.Reader, a.key, digest[:])
	if err != nil {
		t.Fatalf("sign assertion: %v", err)
	}

	body, err := json.Marshal(protocol.CredentialAssertionResponse{
		PublicKeyCredential: protocol.PublicKeyCredential{
			Credential: protocol.Credential{
				ID:   a.credentialID(),
				Type: "public-key",
			},
			RawID: a.credID,
		},
		AssertionResponse: protocol.AuthenticatorAssertionResponse{
			AuthenticatorResponse: protocol.AuthenticatorResponse{ClientDataJSON: clientData},
			AuthenticatorData:     authData,
			Signature:             sig,
			UserHandle:            userHandle,
		},
	})
	if err != nil {
		t.Fatalf("marshal assertion response: %v", err)
	}
	return string(body)
}

// requestFor wraps a ceremony body as the POST the service consumes.
func requestFor(t *testing.T, body string) *http.Request {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/user/me/passkeys", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	return req
}

// --- an in-memory store, so a saved credential can be found again ------------

// ceremonyCredStore is a CredStore for the ceremony cases. The simple
// stubCredStore returns fixed answers, which cannot support a flow that saves a
// credential and then logs in with it.
type ceremonyCredStore struct {
	mu     sync.Mutex
	rows   []*domain.PasskeyCredential
	nextID int64
	// saveErr, if set, fails every Save.
	saveErr error
}

func (s *ceremonyCredStore) Save(_ context.Context, c *domain.PasskeyCredential) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.saveErr != nil {
		return s.saveErr
	}
	for _, row := range s.rows {
		// The real table has a unique index on credential_id, so a duplicate is
		// refused rather than overwriting — which is what stops one account
		// registering another's credential.
		if row.CredentialID == c.CredentialID {
			return domain.ErrAlreadyExists
		}
	}
	s.nextID++
	stored := *c
	stored.ID = s.nextID
	s.rows = append(s.rows, &stored)
	return nil
}

func (s *ceremonyCredStore) FindByUserID(_ context.Context, userID int64) ([]*domain.PasskeyCredential, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []*domain.PasskeyCredential
	for _, row := range s.rows {
		if row.UserID == userID {
			cp := *row
			out = append(out, &cp)
		}
	}
	return out, nil
}

func (s *ceremonyCredStore) FindByCredentialID(_ context.Context, credentialID string) (*domain.PasskeyCredential, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, row := range s.rows {
		if row.CredentialID == credentialID {
			cp := *row
			return &cp, nil
		}
	}
	return nil, domain.ErrNotFound
}

// UpdateAfterLogin applies the same monotonic gate as the SQL repository:
// the write only lands when the presented count is not behind the stored one.
func (s *ceremonyCredStore) UpdateAfterLogin(_ context.Context, id int64, credential []byte, newSignCount int64, lastUsed time.Time) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, row := range s.rows {
		if row.ID != id {
			continue
		}
		if row.SignCount > newSignCount {
			return false, nil
		}
		row.Credential = credential
		row.SignCount = newSignCount
		row.LastUsedAt = &lastUsed
		return true, nil
	}
	return false, nil
}

func (s *ceremonyCredStore) Rename(context.Context, int64, int64, string) error { return nil }
func (s *ceremonyCredStore) Delete(context.Context, int64, int64) error         { return nil }
func (s *ceremonyCredStore) DeleteAllByUserID(_ context.Context, userID int64) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	kept := s.rows[:0]
	removed := 0
	for _, row := range s.rows {
		if row.UserID == userID {
			removed++
			continue
		}
		kept = append(kept, row)
	}
	s.rows = kept
	return removed, nil
}
func (s *ceremonyCredStore) CountByUserIDs(context.Context, []int64) (map[int64]int, error) {
	return map[int64]int{}, nil
}

// snapshot returns a copy of the stored rows, for asserting that a refused login
// changed nothing.
func (s *ceremonyCredStore) snapshot() []domain.PasskeyCredential {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]domain.PasskeyCredential, 0, len(s.rows))
	for _, row := range s.rows {
		out = append(out, *row)
	}
	return out
}

// --- the users and settings the ceremonies run against -----------------------

type stubUsers struct {
	mu   sync.Mutex
	byID map[int64]*domain.User
}

func newStubUsers(ids ...int64) *stubUsers {
	u := &stubUsers{byID: map[int64]*domain.User{}}
	for _, id := range ids {
		u.byID[id] = &domain.User{ID: id, UPN: "user" + string(rune('0'+id)) + "@corp.example", PasswordHash: "bcrypt-hash"}
	}
	return u
}

func (u *stubUsers) GetByID(_ context.Context, id int64) (*domain.User, error) {
	u.mu.Lock()
	defer u.mu.Unlock()
	if got, ok := u.byID[id]; ok {
		return got, nil
	}
	return nil, domain.ErrNotFound
}

const (
	ceremonyRPID   = "panel.example.com"
	ceremonyOrigin = "https://panel.example.com"
)

// ceremonyService wires a service against the in-memory store, two users, and
// passkeys enabled panel-wide.
func ceremonyService(t *testing.T) (*Service, *ceremonyCredStore, *stubUsers) {
	t.Helper()
	creds := &ceremonyCredStore{}
	users := newStubUsers(1, 2)
	svc := New(Deps{
		Creds: creds,
		Users: users,
		Settings: &mutableSettings{s: ports.UISettings{
			PasskeyEnabled:      true,
			PasskeyPasswordless: true,
			SubBaseURL:          ceremonyOrigin,
		}},
	})
	return svc, creds, users
}

// enroll runs a full registration ceremony and returns the stored credential.
func enroll(t *testing.T, svc *Service, auth *softwareAuthenticator, userID int64) *domain.PasskeyCredential {
	t.Helper()
	ctx := context.Background()
	options, sessionID, err := svc.BeginRegistration(ctx, userID)
	if err != nil {
		t.Fatalf("BeginRegistration: %v", err)
	}
	stored, err := svc.FinishRegistration(ctx, userID, sessionID, "test key", requestFor(t, auth.create(t, ceremonyRPID, ceremonyOrigin, options)))
	if err != nil {
		t.Fatalf("FinishRegistration: %v", err)
	}
	return stored
}
