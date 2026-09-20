package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/KazuhaHub/passwall-sub-panel/internal/version"
)

func testPolicyDocument(t *testing.T, expiresAt time.Time) string {
	t.Helper()
	body := `{
	  "schema_version": 1, "revision": 7,
	  "issued_at": "2026-09-19T00:00:00Z", "expires_at": "` + expiresAt.UTC().Format(time.RFC3339) + `",
	  "applies_to_psp": {"min": "4.0.0", "max": "4.99.99"},
	  "releases": [{"version": "v0.0.1-beta11", "release_tag": "v0.0.1-beta11", "scheme": "legacy", "evidence": ["node-wire-v1"]}],
	  "upgrade_edges": [], "refusals": []
	}`
	path := filepath.Join(t.TempDir(), "releases-v1.json")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func writeTestKey(t *testing.T) (path string, pub ed25519.PublicKey) {
	t.Helper()
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := x509.MarshalPKCS8PrivateKey(privateKey)
	if err != nil {
		t.Fatal(err)
	}
	path = filepath.Join(t.TempDir(), "signing.pem")
	block := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: encoded})
	if err := os.WriteFile(path, block, 0o600); err != nil {
		t.Fatal(err)
	}
	return path, publicKey
}

// The tool signs the BYTES it read. A procedure that said "sign the document"
// would leave the encoding to whoever performed it, and a signature over a
// re-encoded document is a signature over something the panel will never see.
func TestWhatTheToolSignsIsWhatThePanelVerifies(t *testing.T) {
	now := time.Date(2026, 9, 19, 12, 0, 0, 0, time.UTC)
	documentPath := testPolicyDocument(t, now.Add(24*time.Hour))
	keyPath, publicKey := writeTestKey(t)
	outputPath := filepath.Join(t.TempDir(), version.PolicySignatureAsset)

	if err := signPolicy(documentPath, outputPath, keyPath, "key-1", now, nil); err != nil {
		t.Fatalf("signing a valid document must succeed: %v", err)
	}

	document, err := os.ReadFile(documentPath)
	if err != nil {
		t.Fatal(err)
	}
	signature, err := os.ReadFile(outputPath)
	if err != nil {
		t.Fatal(err)
	}
	root, err := version.NewPolicyTrustRoot(map[string]string{
		"key-1": base64.StdEncoding.EncodeToString(publicKey),
	})
	if err != nil {
		t.Fatal(err)
	}
	// The round trip through the PANEL's verifier is the assertion: signing and
	// verifying are separate implementations of the same contract.
	policy, err := version.ParseSignedReleasesPolicy(document, signature, root, now)
	if err != nil {
		t.Fatalf("the panel must accept what the tool signed: %v", err)
	}
	if policy.Revision != 7 {
		t.Fatalf("revision = %d", policy.Revision)
	}
}

// Signing is the last point at which a malformed policy is cheap to find. After
// publication it is a rejection at the far end, in a deployment, by someone who
// did not write it.
func TestTheToolRefusesToSignADocumentThePanelWouldReject(t *testing.T) {
	now := time.Date(2026, 9, 19, 12, 0, 0, 0, time.UTC)
	expired := testPolicyDocument(t, now.Add(-time.Hour))
	keyPath, _ := writeTestKey(t)
	outputPath := filepath.Join(t.TempDir(), version.PolicySignatureAsset)

	err := signPolicy(expired, outputPath, keyPath, "key-1", now, nil)
	if err == nil || !strings.Contains(err.Error(), "would reject") {
		t.Fatalf("error = %v, want a refusal naming the reason", err)
	}
	// And nothing was written: a signature over a refused document is a
	// publication waiting to happen.
	if _, statErr := os.Stat(outputPath); statErr == nil {
		t.Fatal("a refused document produced a signature file")
	}
}

func TestTheToolRefusesAKeyThatIsNotEd25519PKCS8(t *testing.T) {
	now := time.Date(2026, 9, 19, 12, 0, 0, 0, time.UTC)
	documentPath := testPolicyDocument(t, now.Add(24*time.Hour))
	t.Run("not PEM", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "key.pem")
		if err := os.WriteFile(path, []byte("not a key"), 0o600); err != nil {
			t.Fatal(err)
		}
		err := signPolicy(documentPath, filepath.Join(t.TempDir(), "sig"), path, "key-1", now, nil)
		if err == nil || !strings.Contains(err.Error(), "not PEM") {
			t.Fatalf("error = %v", err)
		}
	})
	t.Run("missing file", func(t *testing.T) {
		err := signPolicy(documentPath, filepath.Join(t.TempDir(), "sig"), filepath.Join(t.TempDir(), "nope"), "key-1", now, nil)
		if err == nil || !strings.Contains(err.Error(), "read signing key") {
			t.Fatalf("error = %v", err)
		}
	})
}

// A rotation is a deliberate act, so each key is named explicitly and a malformed
// one is refused rather than carried into a signed document.
func TestApproveTakesAWholePublicKeyOrNothing(t *testing.T) {
	_, publicKey := writeTestKey(t)
	encoded := base64.StdEncoding.EncodeToString(publicKey)

	var good approvals
	if err := good.Set("next=" + encoded); err != nil {
		t.Fatalf("a well-formed approval must be accepted: %v", err)
	}
	if len(good) != 1 || good[0].KeyID != "next" || good[0].PublicKey != encoded {
		t.Fatalf("approvals = %+v", good)
	}

	for _, value := range []string{
		"next",              // no separator
		"=" + encoded,       // no id
		"next=",             // no key
		"next=not base64!!", // not base64
		"next=" + base64.StdEncoding.EncodeToString([]byte("short")),
	} {
		var bad approvals
		if err := bad.Set(value); err == nil {
			t.Errorf("Set(%q) was accepted", value)
		}
	}
}
