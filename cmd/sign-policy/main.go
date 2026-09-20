// Command sign-policy signs a release policy document, producing the detached
// signature the panel verifies beside it.
//
// WHY A TOOL AND NOT A DOCUMENTED PROCEDURE. The signature covers the document's
// BYTES, so anything that re-encodes it produces a signature over something else.
// A step that says "sign the file" leaves the encoding to whoever performs it;
// a tool reads the file, signs what it read, and writes the signature — there is
// no opportunity to pretty-print in between.
//
// It refuses to sign a document the panel would refuse. Signing is the last point
// at which a malformed policy is cheap to find: after publication it is a
// rejection at the far end, in a deployment, by someone who did not write it.
package main

import (
	"crypto/ed25519"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/KazuhaHub/passwall-sub-panel/internal/version"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

// approvals collects -approve flags. A rotation is a deliberate act, so each key
// is named explicitly rather than read from a file that could change unnoticed.
type approvals []version.PolicyApprovedKey

func (a *approvals) String() string { return fmt.Sprint(*a) }

func (a *approvals) Set(value string) error {
	id, key, ok := strings.Cut(value, "=")
	id, key = strings.TrimSpace(id), strings.TrimSpace(key)
	if !ok || id == "" || key == "" {
		return fmt.Errorf("-approve takes key_id=BASE64_PUBLIC_KEY, got %q", value)
	}
	raw, err := base64.StdEncoding.DecodeString(key)
	if err != nil || len(raw) != ed25519.PublicKeySize {
		return fmt.Errorf("-approve %s is not a base64 ed25519 public key", id)
	}
	*a = append(*a, version.PolicyApprovedKey{KeyID: id, PublicKey: key})
	return nil
}

func run() error {
	documentPath := flag.String("document", "", "path to the policy document (releases-v1.json)")
	outputPath := flag.String("output", "", "path to write the detached signature")
	keyPath := flag.String("key", "", "path to the private PKCS#8 PEM")
	keyID := flag.String("key-id", "", "the id this key is known by in the panel's trust root")
	now := flag.String("now", "", "RFC3339 instant to validate the document against (default: the current time)")
	var approve approvals
	flag.Var(&approve, "approve", "a key this signature introduces for future policies, as key_id=BASE64 (repeatable)")
	flag.Parse()

	if flag.NArg() != 0 || *documentPath == "" || *outputPath == "" || *keyPath == "" || *keyID == "" {
		return errors.New("sign-policy requires -document, -output, -key and -key-id")
	}
	at := time.Now().UTC()
	if *now != "" {
		parsed, err := time.Parse(time.RFC3339, *now)
		if err != nil {
			return fmt.Errorf("-now is not RFC3339: %w", err)
		}
		at = parsed
	}
	if err := signPolicy(*documentPath, *outputPath, *keyPath, *keyID, at, approve); err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "signed %s as %s -> %s\n", *documentPath, *keyID, *outputPath)
	return nil
}

// signPolicy is the whole operation, separated from flag parsing so it can be
// tested without a process.
func signPolicy(documentPath, outputPath, keyPath, keyID string, at time.Time, approve []version.PolicyApprovedKey) error {
	// Read the document EXACTLY as it will be published. Nothing re-encodes it.
	document, err := os.ReadFile(documentPath)
	if err != nil {
		return fmt.Errorf("read document: %w", err)
	}
	if _, err := version.ParseReleasesPolicy(document, at); err != nil {
		return fmt.Errorf("refusing to sign a document the panel would reject: %w", err)
	}
	privateKey, err := loadPrivateKey(keyPath)
	if err != nil {
		return err
	}
	signature, err := version.SignReleasesPolicy(document, keyID, privateKey, approve)
	if err != nil {
		return err
	}
	if err := os.WriteFile(outputPath, signature, 0o644); err != nil {
		return fmt.Errorf("write signature: %w", err)
	}
	return nil
}

func loadPrivateKey(path string) (ed25519.PrivateKey, error) {
	body, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read signing key: %w", err)
	}
	block, _ := pem.Decode(body)
	if block == nil {
		return nil, errors.New("signing key is not PEM")
	}
	parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("signing key is not PKCS#8: %w", err)
	}
	key, ok := parsed.(ed25519.PrivateKey)
	if !ok {
		return nil, errors.New("signing key is not an ed25519 private key")
	}
	return key, nil
}
