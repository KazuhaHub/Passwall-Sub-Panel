package version

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// Policy signing.
//
// VALIDATION IS NOT AUTHENTICATION. ParseReleasesPolicy answers "is this
// document internally consistent"; it cannot answer "did the maintainer publish
// it". A policy that is only validated is a policy any writer can rewrite — and
// the file says which upgrades this panel will offer, so a writer who can reach
// it can point the fleet at a release nobody reviewed.
//
// The signature is DETACHED and covers the document bytes exactly as published.
// It is not embedded in the document, because a document that carries its own
// signature invites validating the parsed form instead of the bytes, and
// re-encoding is not identity.
const policySignatureSchema = 1

// PolicyTrustRoot is the set of signing keys this build accepts.
//
// It is compiled in rather than fetched: a trust root obtained over the same
// channel as the thing it authenticates authenticates nothing. Rotation works by
// a key the root already trusts approving new keys (see policySigningDoc.Approves),
// which is why NewPolicyTrustRoot starts from a compiled seed.
type PolicyTrustRoot struct {
	keys map[string]ed25519.PublicKey
}

// NewPolicyTrustRoot builds a trust root from key id to base64 public key.
//
// An empty root is a valid root that trusts nothing: every policy will be
// refused. That is the correct starting state for a deployment that has not been
// given keys yet — failing closed means "no policy", not "any policy".
func NewPolicyTrustRoot(keys map[string]string) (*PolicyTrustRoot, error) {
	root := &PolicyTrustRoot{keys: make(map[string]ed25519.PublicKey, len(keys))}
	for id, encoded := range keys {
		raw, err := base64.StdEncoding.DecodeString(encoded)
		if err != nil {
			return nil, fmt.Errorf("release policy: trust root key %q is not base64: %w", id, err)
		}
		if len(raw) != ed25519.PublicKeySize {
			return nil, fmt.Errorf("release policy: trust root key %q is %d bytes, want %d", id, len(raw), ed25519.PublicKeySize)
		}
		root.keys[id] = ed25519.PublicKey(raw)
	}
	return root, nil
}

// Trusts reports whether the root knows a key id. Used to decide whether an
// approval in a signed document may be acted on.
func (r *PolicyTrustRoot) Trusts(keyID string) bool {
	if r == nil {
		return false
	}
	_, ok := r.keys[keyID]
	return ok
}

// Approve adds a public key to the root, and is only ever called after a
// signature by an already-trusted key has verified.
func (r *PolicyTrustRoot) Approve(keyID string, key ed25519.PublicKey) {
	if r == nil {
		return
	}
	r.keys[keyID] = key
}

type policySigningDoc struct {
	SchemaVersion int    `json:"schema_version"`
	KeyID         string `json:"key_id"`
	Signature     string `json:"signature"`
	// Approves lets a trusted key introduce keys for FUTURE policies.
	//
	// It is covered by the signature (see policySigningMessage), which is the
	// whole reason the message is not simply the policy bytes: a signature over
	// the document alone would leave this list editable in transit, and anyone
	// who could edit it could add a key of their own to a rotation nobody
	// performed.
	Approves []PolicyApprovedKey `json:"approves,omitempty"`
}

// PolicyApprovedKey is a key a trusted signer introduces for future policies.
//
// EXPORTED BECAUSE THE SIGNING FUNCTION TAKES IT. It was unexported while
// SignReleasesPolicy named it, which made that function unusable from any other
// package — a signature that lists a type a caller cannot construct is not a
// public API, it is a private one wearing an exported name.
type PolicyApprovedKey struct {
	KeyID     string `json:"key_id"`
	PublicKey string `json:"public_key"`
}

var (
	// ErrPolicyUnsigned means no signature was supplied where one is required.
	ErrPolicyUnsigned = errors.New("release policy: not signed")
	// ErrPolicyUntrustedKey means the signing key is not in the trust root.
	ErrPolicyUntrustedKey = errors.New("release policy: signature key is not trusted")
	// ErrPolicyBadSignature means the signature does not match the document.
	ErrPolicyBadSignature = errors.New("release policy: signature does not verify")
)

// ParseSignedReleasesPolicy verifies the detached signature and then validates
// the document, in that order: an untrusted document is never parsed into
// anything a caller could mistake for reviewed content.
//
// Rotation: when the verifying key is trusted and the signature document
// approves new keys, those keys are added to the root so the NEXT policy may be
// signed by them. The approval is inside the signed bytes, so it carries exactly
// the authority of the key that made it — there is no window in which an
// in-transit edit can introduce a key.
func ParseSignedReleasesPolicy(raw, signatureDoc []byte, root *PolicyTrustRoot, now time.Time) (ReleasesPolicy, error) {
	if len(signatureDoc) == 0 {
		return ReleasesPolicy{}, ErrPolicyUnsigned
	}
	var doc policySigningDoc
	if err := json.Unmarshal(signatureDoc, &doc); err != nil {
		return ReleasesPolicy{}, fmt.Errorf("%w: %v", ErrPolicyUnsigned, err)
	}
	if doc.SchemaVersion != policySignatureSchema {
		return ReleasesPolicy{}, fmt.Errorf("%w: signature format %d, this build reads %d", ErrPolicyUnsigned, doc.SchemaVersion, policySignatureSchema)
	}
	if doc.KeyID == "" || doc.Signature == "" {
		return ReleasesPolicy{}, fmt.Errorf("%w: the signature document needs a key_id and a signature", ErrPolicyUnsigned)
	}
	key, trusted := root.lookup(doc.KeyID)
	if !trusted {
		// An unknown key id does NOT activate a new key: accepting one would make
		// the signature document a self-certifying trust root.
		return ReleasesPolicy{}, fmt.Errorf("%w: %q", ErrPolicyUntrustedKey, doc.KeyID)
	}
	signature, err := base64.StdEncoding.DecodeString(doc.Signature)
	if err != nil {
		return ReleasesPolicy{}, fmt.Errorf("%w: signature is not base64: %v", ErrPolicyBadSignature, err)
	}
	message, err := policySigningMessage(raw, doc.Approves)
	if err != nil {
		return ReleasesPolicy{}, err
	}
	if !ed25519.Verify(key, message, signature) {
		return ReleasesPolicy{}, ErrPolicyBadSignature
	}

	policy, err := ParseReleasesPolicy(raw, now)
	if err != nil {
		return ReleasesPolicy{}, err
	}

	// Only now, with the document authenticated and valid, may an approval take
	// effect.
	for _, approved := range doc.Approves {
		if approved.KeyID == "" {
			return ReleasesPolicy{}, fmt.Errorf("%w: an approved key has no id", ErrPolicyUnsigned)
		}
		decoded, err := base64.StdEncoding.DecodeString(approved.PublicKey)
		if err != nil || len(decoded) != ed25519.PublicKeySize {
			return ReleasesPolicy{}, fmt.Errorf("%w: approved key %q is not a base64 ed25519 public key", ErrPolicyUnsigned, approved.KeyID)
		}
		root.Approve(approved.KeyID, ed25519.PublicKey(decoded))
	}
	return policy, nil
}

func (r *PolicyTrustRoot) lookup(keyID string) (ed25519.PublicKey, bool) {
	if r == nil {
		return nil, false
	}
	key, ok := r.keys[keyID]
	return key, ok
}

// SignReleasesPolicy produces the detached signature document for a policy. It
// exists for the publishing tool and the tests; the panel only ever verifies.
func SignReleasesPolicy(raw []byte, keyID string, privateKey ed25519.PrivateKey, approves []PolicyApprovedKey) ([]byte, error) {
	if keyID == "" || len(privateKey) != ed25519.PrivateKeySize {
		return nil, errors.New("release policy: signing needs a key id and an ed25519 private key")
	}
	message, err := policySigningMessage(raw, approves)
	if err != nil {
		return nil, err
	}
	return json.Marshal(policySigningDoc{
		SchemaVersion: policySignatureSchema,
		KeyID:         keyID,
		Signature:     base64.StdEncoding.EncodeToString(ed25519.Sign(privateKey, message)),
		Approves:      approves,
	})
}

// policySigningMessage is what the signature actually covers: the document
// bytes, a separator, and the approvals that arrived with them.
//
// SIGNING THE DOCUMENT ALONE IS NOT ENOUGH. The approvals authorise keys, so if
// they were outside the signed message an attacker who could edit the file in
// transit could add a key to a rotation and then sign the next policy with it —
// the trust root would have been extended without anyone approving it. Covering
// both in one message is what ties the authorisation to the key that granted it.
//
// The separator is a NUL, which cannot appear in the document: it is JSON text.
func policySigningMessage(raw []byte, approves []PolicyApprovedKey) ([]byte, error) {
	if approves == nil {
		approves = []PolicyApprovedKey{}
	}
	encoded, err := json.Marshal(approves)
	if err != nil {
		return nil, fmt.Errorf("%w: approvals cannot be encoded", ErrPolicyUnsigned)
	}
	message := make([]byte, 0, len(raw)+1+len(encoded))
	message = append(message, raw...)
	message = append(message, 0x00)
	message = append(message, encoded...)
	return message, nil
}
