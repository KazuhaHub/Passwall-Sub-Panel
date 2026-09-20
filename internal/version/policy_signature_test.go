package version

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"testing"
	"time"
)

const policyForSigning = `{
  "schema_version": 1, "revision": 7,
  "issued_at": "2026-09-19T00:00:00Z", "expires_at": "2026-09-26T00:00:00Z",
  "applies_to_psp": {"min": "4.0.0", "max": "4.99.99"},
  "releases": [{"version": "v0.0.1-beta11", "release_tag": "v0.0.1-beta11", "scheme": "legacy", "evidence": ["node-wire-v1"]}],
  "upgrade_edges": [], "refusals": []
}`

func signingKey(t *testing.T) (string, ed25519.PublicKey, ed25519.PrivateKey) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return "key-1", pub, priv
}

func trustRoot(t *testing.T, id string, pub ed25519.PublicKey) *PolicyTrustRoot {
	t.Helper()
	root, err := NewPolicyTrustRoot(map[string]string{id: base64.StdEncoding.EncodeToString(pub)})
	if err != nil {
		t.Fatal(err)
	}
	return root
}

func policyNow() time.Time {
	return time.Date(2026, 9, 19, 12, 0, 0, 0, time.UTC)
}

func TestSignedPolicyParsesWhenTheSignatureVerifies(t *testing.T) {
	id, pub, priv := signingKey(t)
	doc, err := SignReleasesPolicy([]byte(policyForSigning), id, priv, nil)
	if err != nil {
		t.Fatal(err)
	}
	policy, err := ParseSignedReleasesPolicy([]byte(policyForSigning), doc, trustRoot(t, id, pub), policyNow())
	if err != nil {
		t.Fatalf("a correctly signed policy must parse: %v", err)
	}
	if policy.Revision != 7 || len(policy.Releases) != 1 {
		t.Fatalf("parsed the wrong document: %+v", policy)
	}
}

func TestUnsignedPolicyIsRefused(t *testing.T) {
	id, pub, _ := signingKey(t)
	// A valid document with no signature at all: the failure mode that would let
	// any writer set the upgrade policy.
	if _, err := ParseSignedReleasesPolicy([]byte(policyForSigning), nil, trustRoot(t, id, pub), policyNow()); !errors.Is(err, ErrPolicyUnsigned) {
		t.Fatalf("unsigned policy error = %v, want ErrPolicyUnsigned", err)
	}
	if _, err := ParseSignedReleasesPolicy([]byte(policyForSigning), []byte(`{"schema_version":1,"key_id":"key-1"}`), trustRoot(t, id, pub), policyNow()); !errors.Is(err, ErrPolicyUnsigned) {
		t.Fatalf("signatureless document error = %v, want ErrPolicyUnsigned", err)
	}
}

func TestAnUnknownKeyDoesNotBecomeTrusted(t *testing.T) {
	_, strangerPub, strangerPriv := signingKey(t)
	_, trustedPub, _ := signingKey(t)
	doc, err := SignReleasesPolicy([]byte(policyForSigning), "key-stranger", strangerPriv, nil)
	if err != nil {
		t.Fatal(err)
	}
	root := trustRoot(t, "key-1", trustedPub)

	// Signed under an id the root does not hold: refused, because accepting the
	// document's own claim about who signed it would make every signature
	// self-certifying.
	if _, err := ParseSignedReleasesPolicy([]byte(policyForSigning), doc, root, policyNow()); !errors.Is(err, ErrPolicyUntrustedKey) {
		t.Fatalf("error = %v, want ErrPolicyUntrustedKey", err)
	}
	// A KNOWN id whose key does not match is a different failure, and the
	// distinction is worth keeping: one means "I do not know you", the other
	// means "that is not your signature".
	mismatched, err := SignReleasesPolicy([]byte(policyForSigning), "key-1", strangerPriv, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ParseSignedReleasesPolicy([]byte(policyForSigning), mismatched, root, policyNow()); !errors.Is(err, ErrPolicyBadSignature) {
		t.Fatalf("mismatched-key error = %v, want ErrPolicyBadSignature", err)
	}
	_ = strangerPub

	// And an unknown id with an in-band public key is still not trusted.
	selfCertifying, err := json.Marshal(map[string]any{
		"schema_version": 1,
		"key_id":         "attacker",
		"signature":      "AAAA",
		"public_key":     base64.StdEncoding.EncodeToString(strangerPub),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ParseSignedReleasesPolicy([]byte(policyForSigning), selfCertifying, root, policyNow()); !errors.Is(err, ErrPolicyUntrustedKey) {
		t.Fatalf("self-certifying error = %v, want ErrPolicyUntrustedKey", err)
	}
}

func TestATamperedDocumentFailsTheSignature(t *testing.T) {
	id, pub, priv := signingKey(t)
	doc, err := SignReleasesPolicy([]byte(policyForSigning), id, priv, nil)
	if err != nil {
		t.Fatal(err)
	}
	// The document is still perfectly VALID — one extra release, evidence and
	// all. Only the signature distinguishes it from what was published.
	tampered := []byte(`{
	  "schema_version": 1, "revision": 7,
	  "issued_at": "2026-09-19T00:00:00Z", "expires_at": "2026-09-26T00:00:00Z",
	  "applies_to_psp": {"min": "4.0.0", "max": "4.99.99"},
	  "releases": [{"version": "v0.0.1-beta11", "release_tag": "v0.0.1-beta11", "scheme": "legacy", "evidence": ["node-wire-v1"]},
	               {"version": "v9.9.9", "release_tag": "v9.9.9", "scheme": "legacy", "evidence": ["made-up"]}],
	  "upgrade_edges": [], "refusals": []
	}`)
	if _, err := ParseReleasesPolicy(tampered, policyNow()); err != nil {
		t.Fatalf("the harness is wrong: the tampered document should still be well-formed: %v", err)
	}
	if _, err := ParseSignedReleasesPolicy(tampered, doc, trustRoot(t, id, pub), policyNow()); !errors.Is(err, ErrPolicyBadSignature) {
		t.Fatalf("error = %v, want ErrPolicyBadSignature", err)
	}
}

func TestAnApprovalInjectedInTransitBreaksTheSignature(t *testing.T) {
	id, pub, priv := signingKey(t)
	_, approvedPub, _ := signingKey(t)
	doc, err := SignReleasesPolicy([]byte(policyForSigning), id, priv, nil)
	if err != nil {
		t.Fatal(err)
	}
	var parsed policySigningDoc
	if err := json.Unmarshal(doc, &parsed); err != nil {
		t.Fatal(err)
	}
	parsed.Approves = []policyApprovedKey{{
		KeyID:     "attacker",
		PublicKey: base64.StdEncoding.EncodeToString(approvedPub),
	}}
	injected, err := json.Marshal(parsed)
	if err != nil {
		t.Fatal(err)
	}

	root := trustRoot(t, id, pub)
	// The approvals are part of the signed message, so editing them invalidates
	// it. Without that, anyone who could edit the file could extend the trust
	// root and then sign the next policy with a key nobody approved.
	if _, err := ParseSignedReleasesPolicy([]byte(policyForSigning), injected, root, policyNow()); !errors.Is(err, ErrPolicyBadSignature) {
		t.Fatalf("injected approval error = %v, want ErrPolicyBadSignature", err)
	}
	if root.Trusts("attacker") {
		t.Fatal("an injected approval extended the trust root")
	}
}

func TestAKeyRotatedInByATrustedKeyIsAccepted(t *testing.T) {
	id, pub, priv := signingKey(t)
	nextID, nextPub, nextPriv := signingKey(t)

	rotation, err := SignReleasesPolicy([]byte(policyForSigning), id, priv, []policyApprovedKey{{
		KeyID:     nextID,
		PublicKey: base64.StdEncoding.EncodeToString(nextPub),
	}})
	if err != nil {
		t.Fatal(err)
	}
	root := trustRoot(t, id, pub)
	if _, err := ParseSignedReleasesPolicy([]byte(policyForSigning), rotation, root, policyNow()); err != nil {
		t.Fatalf("a rotation signed by a trusted key must verify: %v", err)
	}
	if !root.Trusts(nextID) {
		t.Fatal("the rotation did not take effect")
	}

	// The next policy may now be signed by the new key alone.
	nextDoc, err := SignReleasesPolicy([]byte(policyForSigning), nextID, nextPriv, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ParseSignedReleasesPolicy([]byte(policyForSigning), nextDoc, root, policyNow()); err != nil {
		t.Fatalf("the rotated key must be able to sign: %v", err)
	}
}

func TestAnEmptyTrustRootTrustsNothing(t *testing.T) {
	id, _, priv := signingKey(t)
	doc, err := SignReleasesPolicy([]byte(policyForSigning), id, priv, nil)
	if err != nil {
		t.Fatal(err)
	}
	root, err := NewPolicyTrustRoot(nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ParseSignedReleasesPolicy([]byte(policyForSigning), doc, root, policyNow()); !errors.Is(err, ErrPolicyUntrustedKey) {
		t.Fatalf("an empty root must refuse everything, got %v", err)
	}
}

func TestAMalformedTrustRootIsRefusedAtConstruction(t *testing.T) {
	if _, err := NewPolicyTrustRoot(map[string]string{"k": "not base64!!"}); err == nil {
		t.Fatal("a non-base64 key must be refused")
	}
	if _, err := NewPolicyTrustRoot(map[string]string{"k": base64.StdEncoding.EncodeToString([]byte("short"))}); err == nil {
		t.Fatal("a wrong-length key must be refused")
	}
}

// The signature is over the DOCUMENT BYTES, so a policy that is valid but signed
// by a different document's signature must fail: re-encoding is not identity.
func TestTheSignatureCoversTheExactBytes(t *testing.T) {
	id, pub, priv := signingKey(t)
	doc, err := SignReleasesPolicy([]byte(policyForSigning), id, priv, nil)
	if err != nil {
		t.Fatal(err)
	}
	// Semantically identical, byte-wise different.
	var compacted any
	if err := json.Unmarshal([]byte(policyForSigning), &compacted); err != nil {
		t.Fatal(err)
	}
	reencoded, err := json.Marshal(compacted)
	if err != nil {
		t.Fatal(err)
	}
	if string(reencoded) == policyForSigning {
		t.Skip("the re-encoding happened to be byte-identical; nothing to assert")
	}
	if _, err := ParseSignedReleasesPolicy(reencoded, doc, trustRoot(t, id, pub), policyNow()); !errors.Is(err, ErrPolicyBadSignature) {
		t.Fatalf("re-encoded document error = %v, want ErrPolicyBadSignature", err)
	}
}
